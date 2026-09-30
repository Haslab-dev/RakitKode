BINARY := bin/rakitkode
VERSION ?= $(shell git describe --long 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build run install test vet fmt clean chmod

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/rakitkode
	@chmod +x $(BINARY)

run: build
	./$(BINARY)

install: build
	install -m 0755 $(BINARY) $(GOPATH)/bin/rakitkode

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)

chmod:
	chmod +x $(BINARY)
