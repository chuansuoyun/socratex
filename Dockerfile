FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /socratex ./cmd/socratex

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /socratex /usr/local/bin/socratex
WORKDIR /data
EXPOSE 80 443
ENTRYPOINT ["socratex", "-config", "/data/config.json"]
