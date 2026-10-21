BINARY_NAME := dbb
IMAGE_NAME := db-backup
SRC_DIR := src
BUILD_DIR := ./cmd/dbb
GO := go
LDFLAGS := -s -w
VERSION_SOURCE := $(shell sed -n 's/^var Version = "\([^"]*\)"/\1/p' src/cmd/dbb/main.go)
GIT_REV := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(if $(GIT_REV),$(shell git diff --quiet HEAD 2>/dev/null || echo -dirty))
VERSION_DEV := $(if $(GIT_REV),$(VERSION_SOURCE)-g$(GIT_REV)$(GIT_DIRTY),$(VERSION_SOURCE))
VERSION := $(shell if [ -n "$$DBBACKUP_VERSION" ] && [ "$$DBBACKUP_VERSION" != "dev" ]; then echo "$$DBBACKUP_VERSION"; else echo "$(VERSION_DEV)"; fi)
GIT_COMMIT ?= $(shell if [ -n "$$DBBACKUP_COMMIT" ]; then echo "$$DBBACKUP_COMMIT"; else git rev-parse --short HEAD 2>/dev/null; fi)
# auto (default: infer from version) | edge | beta | stable
CHANNEL ?= auto
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_FLAGS := -X main.Version=$(VERSION) -X main.buildChannel=$(CHANNEL) -X main.buildCommit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_TIME)
BASE_IMAGE := docker.io/nfrastack/base:alpine_3.24
comma := ,
empty :=
space := $(empty) $(empty)

all: build

EDITION ?= supported
ENGINES ?=

GO_TAGS :=
ifeq ($(EDITION),community)
GO_TAGS := community
endif
ifneq ($(ENGINES),)
GO_TAGS := $(if $(GO_TAGS),$(GO_TAGS) ,)$(foreach e,$(subst $(comma), ,$(ENGINES)),engine_$(e))
endif

build:
	cd $(SRC_DIR) && CGO_ENABLED=0 $(GO) build -mod=mod -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS) $(BUILD_FLAGS)" -o ../$(BINARY_NAME) $(BUILD_DIR)

build-community:
	$(MAKE) build EDITION=community

build-engine:
	$(MAKE) build ENGINES=$(ENGINES)
#   make build-engine ENGINES=mysql
#   make build-engine ENGINES=mysql,postgres

build-supported:
	$(MAKE) build EDITION=supported

build-release:
	cd $(SRC_DIR) && CGO_ENABLED=0 $(GO) build -mod=mod -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS) $(BUILD_FLAGS)" -o ../$(BINARY_NAME) $(BUILD_DIR)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 freebsd/amd64 freebsd/arm64 openbsd/amd64 openbsd/arm64 windows/amd64 windows/arm64

build-all:
	@for p in $(PLATFORMS); do \
		os=$${p%%/*}; arch=$${p##*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="$(BINARY_NAME)_$(VERSION)_$${os}_$${arch}$${ext}"; \
		echo "==> building $${out}"; \
		cd $(SRC_DIR) && CGO_ENABLED=0 GOOS=$${os} GOARCH=$${arch} \
			$(GO) build -mod=mod -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS) $(BUILD_FLAGS)" \
			-o ../$${out} $(BUILD_DIR) && cd .. || exit 1; \
	done
	@echo "==> all platforms built"

test:
	@if [ -d ../dbb-test/unit-tests ]; then \
		rsync -a --include='*/' --include='*_test.go' --exclude='*' \
			../dbb-test/unit-tests/ src/; \
	fi
	cd $(SRC_DIR) && $(GO) test -count=1 -short ./internal/... ./supported/... ./cmd/dbb/...
	@find src -name "*_test.go" -delete 2>/dev/null || true

test-all: test

test-integration:
	@if [ -x ../dbb-test/matrix/run-all.sh ]; then \
		../dbb-test/matrix/run-all.sh; \
	else \
		echo "no dbb repo"; \
		exit 2; \
	fi

clean:
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME)_*_linux_amd64 $(BINARY_NAME)_*_linux_arm64
	rm -f $(BINARY_NAME)_*_darwin_amd64 $(BINARY_NAME)_*_darwin_arm64
	rm -f $(BINARY_NAME)_*_freebsd_amd64 $(BINARY_NAME)_*_freebsd_arm64
	rm -f $(BINARY_NAME)_*_openbsd_amd64 $(BINARY_NAME)_*_openbsd_arm64
	rm -f $(BINARY_NAME)_*_windows_amd64.exe $(BINARY_NAME)_*_windows_arm64.exe

install:
	mkdir -p /usr/local/bin
	cp $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

container-build:
	docker build --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg DBBACKUP_VERSION=$(VERSION) --build-arg DBBACKUP_COMMIT=$(GIT_COMMIT) -t nfrastack/$(IMAGE_NAME):$(VERSION) -f container/Containerfile .
	docker tag nfrastack/$(IMAGE_NAME):$(VERSION) nfrastack/$(IMAGE_NAME):latest

container-build-test:
	docker build --build-arg BASE_IMAGE=$(BASE_IMAGE) --build-arg DBBACKUP_VERSION=$(VERSION) --build-arg DBBACKUP_COMMIT=$(GIT_COMMIT) -t db-backup:test -f container/Containerfile .

version:
	@if [ -n "$(V)" ]; then \
		sed -i 's/^\(      version = "\)[^"]*"/\1$(V)"/' flake.nix; \
		echo "version set to $(V): flake.nix"; \
	else \
		echo "flake.nix:  $$(grep -o 'version = "[^"$$][^"]*"' flake.nix | head -1 | cut -d'"' -f2)"; \
		echo "git:        $$(git describe --tags --exact-match 2>/dev/null || git describe --always --dirty 2>/dev/null || echo dev)"; \
	fi

go-version:
	@if [ -n "$(V)" ]; then \
		echo "$(V)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: V must be full X.Y.Z (got '$(V)')"; exit 1; }; \
		MAJMIN=$$(echo "$(V)" | cut -d. -f1,2); \
		ATTR=$$(echo "$$MAJMIN" | tr . _); \
		sed -i "s/expectedGoVersion = \"[^\"]*\"/expectedGoVersion = \"$(V)\"/" flake.nix; \
		sed -i "s/goOverrideVersion = \"[^\"]*\"/goOverrideVersion = \"$(V)\"/" flake.nix; \
		sed -i "s/go_[0-9_]*/go_$$ATTR/g" flake.nix; \
		for f in .github/workflows/branch-build.yml .github/workflows/release.yml; do \
			[ -f "$$f" ] && sed -i "s/GO_VERSION: '[^']*'/GO_VERSION: '$(V)'/" "$$f"; \
		done; \
		[ -f container/Containerfile ] && sed -i 's/GOLANG_VERSION="[^"]*"/GOLANG_VERSION="$(V)"/' container/Containerfile; \
		if grep -q 'GoLang [0-9][0-9]*\.[0-9][0-9]*' README.md 2>/dev/null; then \
			sed -i "s/GoLang [0-9][0-9]*\.[0-9][0-9]*/GoLang $$MAJMIN/" README.md; \
		fi; \
		(cd $(SRC_DIR) && $(GO) mod edit -go=$(V)); \
		echo "go $(V): go.mod + flake + gha + container"; \
		HASH=$$(nix store prefetch-file --json "https://go.dev/dl/go$(V).src.tar.gz" 2>/dev/null | grep -o '"hash": *"[^"]*"' | cut -d'"' -f4); \
		if [ -n "$$HASH" ]; then \
			sed -i "s|goOverrideHash = \"[^\"]*\"|goOverrideHash = \"$$HASH\"|" flake.nix; \
			echo "goOverrideHash auto-set to $$HASH"; \
		else \
			echo "WARN: auto-prefetch failed - update goOverrideHash in flake.nix manually:"; \
			echo "  nix store prefetch-file --json https://go.dev/dl/go$(V).src.tar.gz"; \
		fi; \
	else \
		echo "go.mod:     $$(grep -o '^go [0-9.]*' $(SRC_DIR)/go.mod)"; \
		echo "flake:      $$(grep -o 'expectedGoVersion = "[^"]*"' flake.nix) + $$(grep -o 'go_[0-9_]*' flake.nix | head -1) + override $$(grep -o 'goOverrideVersion = "[^"]*"' flake.nix)"; \
		echo "gha branch: $$(grep -o "GO_VERSION: '[^']*'" .github/workflows/branch-build.yml)"; \
		echo "gha release:$$(grep -o "GO_VERSION: '[^']*'" .github/workflows/release.yml)"; \
		echo "container:  $$(grep -o 'GOLANG_VERSION="[^"]*"' container/Containerfile)"; \
	fi

help:
	@echo "make build                Build the full binary (supporter edition)"
	@echo "make build-community      Build the community binary"
	@echo "make build-supported      Build the supporter binary explicitly"
	@echo "make build-engine ENGINES=mysql,postgres  Build with only named engines"
	@echo "make build-release        Build with version information"
	@echo "make build-all            Build for all PLATFORMS"
	@echo "make test                 Run unit tests"
	@echo "make test-integration     Run integration matrix"
	@echo "make test-all             Run unit tests"
	@echo "make clean                Clean build artifacts"
	@echo "make install              Install binary locally"
	@echo "make container-build      Build container image"
	@echo "make container-build-test Build container for testing"
	@echo "make version [V=x]        Show app version, or set it (flake.nix)"
	@echo "make go-version [V=x]     Show Go version, or set it (go.mod + flake + gha + container)"
	@echo "make help                 Yer lookin' at it"
