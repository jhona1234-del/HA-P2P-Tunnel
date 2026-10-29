FROM golang:alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN go build -ldflags="-s -w" -o p2p-server main.go

FROM alpine:3.20
COPY --from=builder /src/p2p-server /usr/bin/p2p-server
COPY run.sh /
RUN chmod a+x /run.sh
COPY ha-p2p-tunnel.apk /app/ha-p2p-tunnel.apk
CMD [ "/run.sh" ]
