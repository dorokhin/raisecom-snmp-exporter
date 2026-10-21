# Release version (SemVer, without a "v" prefix) lives in the VERSION file.
VERSION  ?= $(shell cat VERSION)
REVISION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
CREATED  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE    ?= andrewpinkerton/raisecom-snmp-exporter
PLATFORMS ?= linux/amd64,linux/arm64
# Images are tagged with the exact version only: no floating tags (1.2, 1, latest).
TAGS     := $(VERSION)
BUILD_ARGS := --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) --build-arg CREATED=$(CREATED)
PKG      := github.com/prometheus/common/version
LDFLAGS  := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Revision=$(REVISION) -X $(PKG).BuildUser=$(USER)

.PHONY: all build test lint golden mibs mibcheck run docker docker-push docker-release clean

all: lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/raisecom_exporter ./cmd/raisecom_exporter

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...

# Regenerate collector golden files after an intentional output change; review the diff.
golden:
	go test ./internal/collector -run TestGolden -update

# Regenerate mibs/RAISECOM-ISCOM2600G-MIB.mib and mibs/modules/*.mib from mibs/gen_mibs.py.
mibs:
	python3 mibs/gen_mibs.py

# Load both MIB variants with net-snmp and check every OID the exporter reads resolves.
mibcheck: mibs
	bash mibs/check_mibs.sh

run: build
	./bin/raisecom_exporter --config.file=config.example.yml

# Local image for the host platform, tagged $(TAGS).
docker:
	docker build $(BUILD_ARGS) $(foreach t,$(TAGS),-t $(IMAGE):$(t)) .

# Push the tags built by `make docker` (host platform only). Requires `docker login`.
docker-push:
	$(foreach t,$(TAGS),docker push $(IMAGE):$(t) &&) true

# Multi-arch build ($(PLATFORMS)) pushed straight to the registry. Requires `docker login`.
# The default "docker" buildx driver cannot build several platforms at once, so a dedicated
# docker-container builder is created on first use (the default builder is left unchanged).
BUILDER ?= raisecom-multiarch
docker-release:
	docker buildx inspect $(BUILDER) >/dev/null 2>&1 || docker buildx create --name $(BUILDER) --driver docker-container
	docker buildx build --builder $(BUILDER) --platform $(PLATFORMS) $(BUILD_ARGS) $(foreach t,$(TAGS),-t $(IMAGE):$(t)) --push .

clean:
	rm -rf bin
