PROJECT_GOCACHE ?= $(CURDIR)/.cache/go-build
PROJECT_GOMODCACHE ?= $(CURDIR)/.cache/go-mod
VERSION ?= 0.1.0-dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo none)
BUILD_DATE ?= unknown
VERSION_LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

.PHONY: active-candidate-test benchmark-lab benchmark-mihomo benchmark-mihomo-tls benchmark-tls build capacity-lab capacity-mihomo check clash-transform-test clash-transform-mihomo docs fmt load-lab load-mihomo load-sweep load-sweep-mihomo macos-launch-agent-test mihomo-lab mihomo-lab-build release relocate-live-runtime-test runtime-lab test testlab trial-lab vet

build:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -trimpath -ldflags "$(VERSION_LDFLAGS)" -o bin/smartroute ./cmd/smartroute
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-testlab ./cmd/smartroute-testlab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-trial-lab ./cmd/smartroute-trial-lab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-benchmark-lab ./cmd/smartroute-benchmark-lab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-load-lab ./cmd/smartroute-load-lab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-load-sweep ./cmd/smartroute-load-sweep
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-capacity-lab ./cmd/smartroute-capacity-lab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-mihomo-lab ./cmd/smartroute-mihomo-lab
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go build -o bin/smartroute-runtime-lab ./cmd/smartroute-runtime-lab

fmt:
	gofmt -w cmd internal

test:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go test ./...

testlab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-testlab

trial-lab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-trial-lab

benchmark-lab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-benchmark-lab

benchmark-tls:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-benchmark-lab -tls

benchmark-mihomo: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-benchmark-lab -mihomo .cache/tools/mihomo-v1.19.29

benchmark-mihomo-tls: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-benchmark-lab -mihomo .cache/tools/mihomo-v1.19.29 -tls

load-lab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-load-lab

load-mihomo: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-load-lab -mihomo .cache/tools/mihomo-v1.19.29

load-sweep:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-load-sweep

load-sweep-mihomo: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-load-sweep -mihomo .cache/tools/mihomo-v1.19.29

capacity-lab:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-capacity-lab

capacity-mihomo: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-capacity-lab -mihomo .cache/tools/mihomo-v1.19.29

mihomo-lab-build:
	bash scripts/build-test-mihomo.sh

mihomo-lab: mihomo-lab-build
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go run ./cmd/smartroute-mihomo-lab -mihomo .cache/tools/mihomo-v1.19.29

runtime-lab: mihomo-lab-build build
	./bin/smartroute-runtime-lab -mihomo .cache/tools/mihomo-v1.19.29 -smartroute ./bin/smartroute -node node -composer scripts/compose-clash-script.mjs -apply-script scripts/apply-composed-clash-script.mjs

clash-transform-test:
	node scripts/test-clash-transform.mjs

clash-transform-mihomo: mihomo-lab-build
	node scripts/test-clash-transform-mihomo.mjs --mihomo .cache/tools/mihomo-v1.19.29

active-candidate-test: mihomo-lab-build build
	ruby scripts/test-prepare-active-clash-candidate.rb --mihomo .cache/tools/mihomo-v1.19.29 --smartroute ./bin/smartroute

macos-launch-agent-test:
	ruby scripts/test-prepare-macos-launch-agent.rb

relocate-live-runtime-test:
	ruby scripts/test-relocate-live-runtime.rb

release:
	bash scripts/build-release.sh $(VERSION)

vet:
	GOCACHE=$(PROJECT_GOCACHE) GOMODCACHE=$(PROJECT_GOMODCACHE) go vet ./...

docs:
	bash scripts/check-docs.sh

check: fmt vet test clash-transform-test docs
