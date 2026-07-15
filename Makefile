NAME=mmproxy
TS=$(shell date -u +"%FT%T")
TAG=$(shell git tag | sort -V | tail -1 | grep . || echo "v0.0.0")
REVISION=$(shell git rev-parse --short HEAD 2>/dev/null || echo "0000000")
LDFLAGS=-X main.Version=$(TAG) -X main.Revision=git:$(REVISION) -X main.BuildDate=$(TS)

.PHONY: all fmt lint vuln test build run docker clean

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
	go build -ldflags "$(LDFLAGS)" -o $(NAME) .

run: build
	./$(NAME) -config docs/config.toml

docker:
	docker compose build --build-arg LDFLAGS="$(LDFLAGS)"

clean:
	rm -f $(NAME)
