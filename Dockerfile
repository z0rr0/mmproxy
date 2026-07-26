ARG GOLANG_VERSION=1.26.5
ARG ALPINE_VERSION=3.24

# The builder runs natively on the host arch ($BUILDPLATFORM) and cross-compiles:
# with CGO_ENABLED=0 that is free, while an emulated amd64 builder would not be.
FROM --platform=$BUILDPLATFORM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_LDFLAGS=""
# TARGETOS/TARGETARCH come from BuildKit's --platform: `make docker` builds for
# the host arch, `make docker-push` for linux/amd64 and linux/arm64. Not named
# LDFLAGS: that one conventionally holds C linker flags and is often already
# exported by the host shell (Homebrew sets it).
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "$GO_LDFLAGS" -o /app/mmproxy .

# No --platform here: BuildKit resolves this to the target platform, so the
# binary above and this base image always match.
FROM alpine:${ALPINE_VERSION}
# ca-certificates are required for TLS to Telegram and Mattermost.
RUN apk --no-cache add ca-certificates && adduser -D appuser
LABEL org.opencontainers.image.authors="me@axv.email" \
    org.opencontainers.image.url="https://hub.docker.com/r/z0rr0/mmproxy" \
    org.opencontainers.image.documentation="https://github.com/z0rr0/mmproxy" \
    org.opencontainers.image.source="https://github.com/z0rr0/mmproxy" \
    org.opencontainers.image.licenses="MIT" \
    org.opencontainers.image.title="MMProxy" \
    org.opencontainers.image.description="Telegram and Miniflux to Mattermost proxy"

COPY --from=builder /app/mmproxy /app/mmproxy
USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/mmproxy"]
CMD ["-config", "/data/config.toml"]
