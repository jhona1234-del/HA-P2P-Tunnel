FROM golang:alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o iabot main.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /src/iabot /usr/bin/iabot
COPY run.sh /
RUN chmod a+x /run.sh
CMD [ "/run.sh" ]
