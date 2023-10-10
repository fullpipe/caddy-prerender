package psr

import (
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`okhttp|Googlebot|bingbot|facebookexternalhit|AhrefsBot|SemrushBot|Applebot|Bytespider|YandexBot|telegram`)

func detectCrawler(userAgent string) (bool, bool) {
	isCrawler := re.MatchString(userAgent)
	if isCrawler {
		return isCrawler, strings.Contains(userAgent, "Mobile")
	}

	return isCrawler, false
}
