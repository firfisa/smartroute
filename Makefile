PROJECT_GOCACHE ?= $(CURDIR)/.cache/go-build

.PHONY: build check docs fmt test testlab vet

build:
	GOCACHE=$(PROJECT_GOCACHE) go build -o bin/smartroute ./cmd/smartroute
	GOCACHE=$(PROJECT_GOCACHE) go build -o bin/smartroute-testlab ./cmd/smartroute-testlab

fmt:
	gofmt -w cmd internal

test:
	GOCACHE=$(PROJECT_GOCACHE) go test ./...

testlab:
	GOCACHE=$(PROJECT_GOCACHE) go run ./cmd/smartroute-testlab

vet:
	GOCACHE=$(PROJECT_GOCACHE) go vet ./...

docs:
	bash scripts/check-docs.sh

check: fmt vet test docs
