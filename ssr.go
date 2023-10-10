package psr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/device"
	"github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(Prerender{})
	httpcaddyfile.RegisterHandlerDirective("crawler_prerender", parseCaddyfile)
}

type Prerender struct {
	config Config

	chromeCtx context.Context
	mu        *sync.Mutex
	logger    *zap.Logger
	cache     *cache.Cache
}

type Config struct {
	AfterLoadDelay int `json:"after_load_delay,omitempty"`
	CacheTTL       int `json:"cache_ttl,omitempty"`
}

// CaddyModule returns the Caddy module information.
func (Prerender) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.prerender",
		New: func() caddy.Module { return new(Prerender) },
	}
}

// Provision implements caddy.Provisioner.
func (m *Prerender) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger()
	m.mu = new(sync.Mutex)

	m.InitChromeCtx(ctx.Context)

	m.cache = cache.New(
		time.Duration(m.config.CacheTTL)*time.Second,
		time.Duration(m.config.CacheTTL*2)*time.Second,
	)

	return nil
}

func (m *Prerender) InitChromeCtx(ctx context.Context) {
	if m.chromeCtx != nil && m.chromeCtx.Err() == nil {
		return
	}

	if !m.mu.TryLock() {
		return
	}
	defer m.mu.Unlock()

	chromeCtx, _ := chromedp.NewContext(
		ctx,
		chromedp.WithErrorf(func(s string, _ ...interface{}) {
			m.logger.Error(s)
		}),
		chromedp.WithDebugf(func(s string, _ ...interface{}) {
			m.logger.Debug(s)
		}),
		chromedp.WithLogf(func(s string, _ ...interface{}) {
			m.logger.Info(s)
		}),
	)

	m.chromeCtx = chromeCtx
}

// Validate implements caddy.Validator.
func (m *Prerender) Validate() error {
	if m.chromeCtx == nil {
		return fmt.Errorf("no chrome context")
	}

	if m.cache == nil {
		return fmt.Errorf("no cache")
	}

	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (m Prerender) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if r.Method != "GET" {
		m.logger.Debug("not a GET request [bypass]")
		return next.ServeHTTP(w, r)
	}

	if !strings.HasPrefix(r.Header.Get("Accept"), "text/html") {
		m.logger.Debug("not a text/html request: " + r.Header.Get("Accept"))
		return next.ServeHTTP(w, r)
	}

	if strings.Contains(r.UserAgent(), "PSR/1") {
		m.logger.Debug("PSR user agent")
		return next.ServeHTTP(w, r)
	}

	isCrawler, isMobile := detectCrawler(r.UserAgent())
	if !isCrawler {
		m.logger.Debug("not a crawler: " + r.UserAgent())
		return next.ServeHTTP(w, r)
	}

	var d device.Info
	if isMobile {
		d = device.Pixel2.Device()
	} else {
		d = device.Reset.Device()
		d.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36"
		d.Width = 1024
		d.Height = 768
		d.Landscape = true
		d.Scale = 1
	}

	key := d.UserAgent + r.URL.String()
	if cached, ok := m.cache.Get(key); ok {
		m.logger.Debug("cache hit")
		_, err := w.Write([]byte(cached.(string)))

		return err
	}

	// TODO: move worker pool
	m.logger.Debug("render as PSR: " + d.UserAgent)
	var page string
	err := chromedp.Run(m.chromeCtx,
		chromedp.Emulate(d),
		emulation.SetUserAgentOverride(d.UserAgent+" PSR/1"),
		chromedp.Navigate(r.URL.String()),
		chromedp.ActionFunc(func(_ context.Context) error {
			if m.config.AfterLoadDelay > 0 {
				time.Sleep(time.Duration(m.config.AfterLoadDelay) * time.Millisecond)
			}

			return nil
		}),
		chromedp.OuterHTML("html", &page),
	)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.logger.Error("context canceled")
			// TODO: start new context
		}

		m.logger.Error(err.Error())

		return next.ServeHTTP(w, r)
	}

	m.cache.Set(key, page, cache.DefaultExpiration)

	_, err = w.Write([]byte(page))

	return err
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (m *Prerender) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	config := Config{
		AfterLoadDelay: 100,
		CacheTTL:       5 * 60,
	}

	for d.Next() {
		var aftreLoadDelayRaw string
		var cacheTTLRaw string
		d.Args(&aftreLoadDelayRaw, &cacheTTLRaw)

		if aftreLoadDelayRaw != "" {
			aftreLoadDelay, err := strconv.Atoi(aftreLoadDelayRaw)
			if err != nil {
				return d.WrapErr(err)
			}

			config.AfterLoadDelay = aftreLoadDelay
		}

		if cacheTTLRaw != "" {
			cacheTTL, err := strconv.Atoi(cacheTTLRaw)
			if err != nil {
				return d.WrapErr(err)
			}

			config.CacheTTL = cacheTTL
		}

		if d.NextArg() {
			return d.ArgErr()
		}
	}

	m.config = config

	return nil
}

func (m *Prerender) Cleanup() error {
	chromedp.Cancel(m.chromeCtx)

	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Prerender
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return m, err
}

// Interface guards
var (
	_ caddy.Provisioner           = (*Prerender)(nil)
	_ caddy.Validator             = (*Prerender)(nil)
	_ caddyhttp.MiddlewareHandler = (*Prerender)(nil)
	_ caddyfile.Unmarshaler       = (*Prerender)(nil)
	_ caddy.CleanerUpper          = (*Prerender)(nil)
)
