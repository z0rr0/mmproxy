NAME=mmproxy
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.0.1")
LDFLAGS=-X main.version=$(VERSION)

.PHONY: all fmt lint test build run docker clean

all: build

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files need formatting" && exit 1)
	go vet ./...
	golangci-lint -c golangci.yml run
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
