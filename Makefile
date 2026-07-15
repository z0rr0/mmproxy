NAME=mmproxy
TS=$(shell date -u +"%FT%T")
TAG=$(shell git tag | sort -V | tail -1 | grep . || echo "v0.0.0")
REVISION=$(shell git rev-parse --short HEAD 2>/dev/null || echo "0000000")
# GO_LDFLAGS, not LDFLAGS: the latter conventionally carries C linker flags and
# is commonly exported by the host shell, which would leak into the build.
GO_LDFLAGS=-X main.Version=$(TAG) -X main.Revision=git:$(REVISION) -X main.BuildDate=$(TS)
DIST=dist
PLATFORMS=darwin/arm64 linux/amd64

.PHONY: all fmt lint vuln test build dist run docker clean

all: build

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files need formatting" && exit 1)
	go vet ./...
	golangci-lint -c golangci.yml run
	$(MAKE) vuln

vuln:
	-govulncheck ./...

test: lint
	go test -race -cover ./...

build:
	go build -ldflags "$(GO_LDFLAGS)" -o $(NAME) .

# Cross-compiled release binaries; `build` above stays native to the host.
# CGO_ENABLED=0 is what makes cross-compiling work without a C toolchain.
dist:
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags "$(GO_LDFLAGS)" -o $(DIST)/$(NAME)-$$os-$$arch . || exit 1; \
	done

run: build
	./$(NAME) -config docs/config.toml

docker:
	docker compose build --build-arg GO_LDFLAGS="$(GO_LDFLAGS)"

clean:
	rm -f $(NAME)
	rm -rf $(DIST)
