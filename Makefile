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
# cyclonedx-gomod v1.12.0 : publiée le 2026-08-21.
CYCLONEDX_GOMOD_VERSION := v1.12.0
# nfpm v2.47.0 : publiée le 2026-06-20 (v2.48.0, du 2026-10-12, est trop récente).
NFPM_TOOL_VERSION := v2.47.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
export SOURCE_DATE_EPOCH

# Options de build reproductible. Ce Makefile fait foi : scripts/repro-check.sh
# reprend BUILD_FLAGS et LDFLAGS depuis l'environnement.
BUILD_ENV := CGO_ENABLED=0
BUILD_FLAGS := -trimpath -buildvcs=false
LDFLAGS := -s -w -buildid= -X main.version=$(VERSION)
export VERSION BUILD_FLAGS LDFLAGS CYCLONEDX_GOMOD_VERSION NFPM_TOOL_VERSION

TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all vet test lint vuln ci build repro-check release clean
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

# Release : `make release VERSION=vX.Y.Z`. Le contrôle des vulnérabilités est un
# prérequis bloquant. ALLOW_VULN=1 (essais locaux uniquement) le saute, écrit
# dist/UNSAFE-DRY-RUN et interdit la signature : ne jamais l'utiliser pour publier.
release:
ifeq ($(ALLOW_VULN),1)
	@echo "ATTENTION : ALLOW_VULN=1, govulncheck ignoré. Dry run local, ne JAMAIS publier." >&2
else
	$(MAKE) vuln
endif
	VULN_CHECKED=$(if $(filter 1,$(ALLOW_VULN)),,1) ALLOW_VULN="$(ALLOW_VULN)" TARGETS="$(TARGETS)" sh scripts/release.sh

clean:
	rm -rf dist
