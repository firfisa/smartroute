PROJECT_GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: build check docs fmt test vet

build:
	GOCACHE=$(PROJECT_GOCACHE) go build -o bin/smartroute ./cmd/smartroute

fmt:
	gofmt -w cmd internal

test:
	GOCACHE=$(PROJECT_GOCACHE) go test ./...

vet:
	GOCACHE=$(PROJECT_GOCACHE) go vet ./...

docs:
	bash scripts/check-docs.sh

check: fmt vet test docs
