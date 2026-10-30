FROM golang:alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN go build -ldflags="-s -w" -o p2p-server main.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && \
    ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then CF_ARCH="amd64"; \
    elif [ "$ARCH" = "aarch64" ]; then CF_ARCH="arm64"; \
    elif [ "$ARCH" = "armv7l" ]; then CF_ARCH="arm"; \
    else CF_ARCH="amd64"; fi && \
    wget -qO /usr/bin/cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${CF_ARCH} && \
    chmod +x /usr/bin/cloudflared

COPY --from=builder /src/p2p-server /usr/bin/p2p-server
COPY run.sh /
RUN chmod a+x /run.sh
COPY ha-p2p-tunnel.apk /app/ha-p2p-tunnel.apk
CMD [ "/run.sh" ]
