FROM golang AS builder

WORKDIR /app

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

COPY . .

RUN xcaddy build --with github.com/fullpipe/caddy-prerender

FROM scratch 

WORKDIR /app

COPY --from=builder /app/caddy /bin/caddy

ENTRYPOINT ["/bin/caddy"]
