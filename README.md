# caddy-prerender

not works yet :(

have to deal with caddy http request
it does not contain original host and scheme

```go
// not in r *http.Request or in
or, _ := r.Context().Value(caddyhttp.OriginalRequestCtxKey).(http.Request)
```

As a solution run local file server and bypass chrome requests to it
