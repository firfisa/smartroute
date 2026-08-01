PROJECT_GOCACHE ?= $(CURDIR)/.cache/go-build
PROJECT_GOMODCACHE ?= $(CURDIR)/.cache/go-mod

.PHONY: build check docs fmt mihomo-lab mihomo-lab-build test testlab vet

build:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute ./cmd/smartroute
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-testlab ./cmd/smartroute-testlab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-mihomo-lab ./cmd/smartroute-mihomo-lab

fmt:
	gofmt -w cmd internal

test:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go test ./...

testlab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-testlab

mihomo-lab-build:
	bash scripts/build-test-mihomo.sh

mihomo-lab: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-mihomo-lab -mihomo .cache/tools/mihomo-v1.19.29

vet:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go vet ./...

docs:
	bash scripts/check-docs.sh

check: fmt vet test docs
