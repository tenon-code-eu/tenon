# Makefile de tenon : construction reproductible, qualité, CI.
# Nécessite GNU make (sur macOS : `gmake` ou `make` 3.81+) ; les outils
# userland BSD (macOS) fonctionnent ; scripts en POSIX sh.

# Code du projet : dépendances vendorisées.
export GOFLAGS := -mod=vendor

# Outils épinglés : dernière version publiée au moins deux semaines avant le 2026-10-15.
# Exécutés en mode module (GOFLAGS vide) avec GOPROXY=direct.
# staticcheck v0.8.1 : publiée le 2026-08-21.
STATICCHECK_VERSION := v0.8.1
# govulncheck v1.8.0 : publiée le 2026-09-08.
GOVULNCHECK_VERSION := v1.8.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
export SOURCE_DATE_EPOCH

# Options de build reproductible. Ce Makefile fait foi : scripts/repro-check.sh
# reprend BUILD_FLAGS et LDFLAGS depuis l'environnement.
BUILD_ENV := CGO_ENABLED=0
BUILD_FLAGS := -trimpath -buildvcs=false
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION)
export VERSION BUILD_FLAGS LDFLAGS

TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all vet test lint vuln ci build repro-check clean
all: ci build

vet:
	go vet ./...

test:
	go test -race ./...

lint:
	GOFLAGS= GOPROXY=direct go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

vuln:
	GOFLAGS= GOPROXY=direct go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

ci: vet test lint vuln

build:
	mkdir -p dist
	$(BUILD_ENV) go build $(BUILD_FLAGS) -ldflags "$(LDFLAGS)" -o dist/tenon ./cmd/tenon

repro-check:
	TARGETS="$(TARGETS)" sh scripts/repro-check.sh

clean:
	rm -rf dist
