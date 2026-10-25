BINARY := bin/rakit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build run install test vet fmt check clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/rakit
	@chmod +x $(BINARY)

run: build
	./$(BINARY)

install: build
	install -m 0755 $(BINARY) $(GOPATH)/bin/rakit

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

check:
	@files="$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"; \
	if [ -n "$$files" ]; then echo "gofmt required:"; echo "$$files"; exit 1; fi
	go vet ./...
	go build ./...
	go test ./...

clean:
	rm -rf bin
