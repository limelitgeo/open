BINARY  := limelit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt vet clean run

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/limelit

test:
	go test ./... -count=1

lint: fmt vet

fmt:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt found unformatted files"; exit 1)

vet:
	go vet ./...

run: build
	./bin/$(BINARY) serve

clean:
	rm -rf bin data
