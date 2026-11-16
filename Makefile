GO ?= go
BIN := bin/pwsgw
PKG := ./...

.PHONY: all build test check lint fmt web web-install schemas clean

all: build

build:
	$(GO) build -o $(BIN) ./cmd/pwsgw

build-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/pwsgw-linux-arm64 ./cmd/pwsgw

test:
	$(GO) test $(PKG)
	$(MAKE) schemas

check: fmt-check lint test

lint:
	golangci-lint run

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './web/*')

fmt-check:
	@unformatted=$$(gofmt -l $$(find . -name '*.go' -not -path './web/*')); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

schemas:
	$(GO) run ./tools/schemacheck

web-install:
	cd web && npm ci

web:
	cd web && npm run build

clean:
	rm -rf bin dist web/dist
