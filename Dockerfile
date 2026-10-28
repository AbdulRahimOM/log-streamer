FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/logstreamer ./cmd/logstreamer

FROM alpine:3

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 logstreamer \
    && adduser -S -D -H -u 10001 -G logstreamer logstreamer \
    && mkdir -p /etc/logstreamer /var/log \
    && chown logstreamer:logstreamer /etc/logstreamer
COPY --from=build --chown=10001:10001 /out/logstreamer /usr/local/bin/logstreamer

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/logstreamer"]
