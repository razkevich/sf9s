BINARY  := sf9s
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race lint vet fmt run clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/sf9s

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY) coverage.out
