FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o socratex .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/socratex /usr/local/bin/socratex
EXPOSE 80 443
ENTRYPOINT ["socratex"]
