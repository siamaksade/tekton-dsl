BINARY := tkn-dsl
VERSION ?= dev
GOFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test test-unit test-integration lint clean golden-update

build:
	go build $(GOFLAGS) -o bin/$(BINARY) ./cmd/tkn-dsl

test: test-unit

test-unit:
	go test ./internal/... ./pkg/... -v

test-integration:
	go test ./test/integration/ -v -timeout=10m

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

golden-update:
	go test ./internal/compiler/ -run TestGoldenFiles -update
