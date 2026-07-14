ARG GOLANG_VERSION=1.26.5
ARG ALPINE_VERSION=3.24

FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG LDFLAGS=""
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "$LDFLAGS" -o /app/mmproxy .

FROM alpine:${ALPINE_VERSION}
# ca-certificates are required for TLS to Telegram and Mattermost.
RUN apk --no-cache add ca-certificates && adduser -D appuser
COPY --from=builder /app/mmproxy /app/mmproxy
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/mmproxy"]
CMD ["-config", "/data/config.toml"]
