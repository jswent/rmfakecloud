VERSION :=$(shell git describe --tags --always)
LDFLAGS := "-s -w -X main.version=$(VERSION)"
OUT_DIR := dist
CMD := ./cmd/rmfakecloud
BINARY := rmfakecloud
BUILD = go build -ldflags $(LDFLAGS) -o $(@) $(CMD) 
ASSETS = ui/dist
GOFILES := $(shell find . \( -path ./.build -o -path ./.worktrees \) -prune -o -iname '*.go' ! -iname "*_test.go" -print)
GOFILES += $(ASSETS)
UIFILES := $(shell find ui/src)
UIFILES += $(shell find ui/public)
UIFILES += ui/package.json
TARGETS := $(addprefix $(OUT_DIR)/$(BINARY)-, x64 armv6 armv7 arm64 win64 docker)
PNPM	= cd ui; pnpm

.PHONY: all run runui clean test testgo testui rmlines-lib build-rmlines test-rmlines probe-rmlines docker

build: $(OUT_DIR)/$(BINARY)-x64

all: $(TARGETS)

$(OUT_DIR)/$(BINARY)-x64:$(GOFILES)
	GOOS=linux $(BUILD)

$(OUT_DIR)/$(BINARY)-armv6:$(GOFILES)
	GOARCH=arm GOARM=6 $(BUILD)

$(OUT_DIR)/$(BINARY)-armv7:$(GOFILES)
	GOARCH=arm GOARM=7 $(BUILD)

$(OUT_DIR)/$(BINARY)-win64:$(GOFILES)
	GOOS=windows $(BUILD)

$(OUT_DIR)/$(BINARY)-arm64:$(GOFILES)
	GOARCH=arm64 $(BUILD)

$(OUT_DIR)/$(BINARY)-docker:$(GOFILES)
	CGO_ENABLED=0 $(BUILD)

container: $(OUT_DIR)/$(BINARY)-docker
	docker build -t rmfakecloud -f Dockerfile.make .

# Full multi-stage image. RMLINES=1 (default) includes the v6 renderer;
# make docker RMLINES=0 builds the pure-Go image.
RMLINES ?= 1
DOCKER_IMAGE ?= rmfakecloud
docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg RMLINES=$(RMLINES) -t $(DOCKER_IMAGE) .

# --- v6 renderer (librm_lines, linked with cgo) -------------------------------
# Opt-in for local builds; `make build` above stays pure Go. Needs CMake >= 3.30,
# a C++20 compiler and Python 3 with fontTools (a venv is created if missing).
RMLINES_DIR ?= $(CURDIR)/.build/rmlines
RMLINES_LIB := $(RMLINES_DIR)/lib/librm_lines.a
RMLINES_TAGS := rmlines,rmlines_static
RMLINES_ENV = CGO_ENABLED=1 CGO_LDFLAGS="-L$(RMLINES_DIR)/lib $(CGO_LDFLAGS)"
RMLINES_PKG := ./internal/storage/exporter/rmlines
RMLINES_INPUTS := $(wildcard build/rmlines/VERSION build/rmlines/SOURCES build/rmlines/SHA256SUMS build/rmlines/build.sh build/rmlines/patches/*.patch)
RMLINES_FIXTURE := testdata/rm/v6/Test_all_tools.rm

rmlines-lib: $(RMLINES_LIB)

$(RMLINES_LIB): $(RMLINES_INPUTS)
	@mkdir -p $(RMLINES_DIR) && echo '*' > $(RMLINES_DIR)/.gitignore
	if python3 -c 'import fontTools' 2>/dev/null; then py=python3; \
	else py=$(RMLINES_DIR)/venv/bin/python; \
		$$py -c 'import fontTools' 2>/dev/null || { python3 -m venv $(RMLINES_DIR)/venv && \
			$(RMLINES_DIR)/venv/bin/pip install -q 'fonttools>=4.40,<5'; } || exit 1; \
	fi; \
	PYTHON3=$$py build/rmlines/build.sh all $(RMLINES_DIR)
	@touch $@

build-rmlines: $(OUT_DIR)/$(BINARY)-rmlines

$(OUT_DIR)/$(BINARY)-rmlines: $(GOFILES) $(RMLINES_LIB)
	$(RMLINES_ENV) go build -tags $(RMLINES_TAGS) -ldflags $(LDFLAGS) -o $(@) $(CMD)

# Runs the link probe, then the rmlines package tests once that package exists.
test-rmlines: probe-rmlines
	@if [ -d $(RMLINES_PKG) ]; then \
		echo '$(RMLINES_ENV) go test -tags $(RMLINES_TAGS) $(RMLINES_PKG)/...'; \
		$(RMLINES_ENV) go test -tags $(RMLINES_TAGS) $(RMLINES_PKG)/...; \
	else echo "$(RMLINES_PKG) does not exist yet; skipping its tests"; fi

probe-rmlines: $(RMLINES_LIB)
	cd build/rmlines/probe && $(RMLINES_ENV) go run -tags $(RMLINES_TAGS) . \
		$(CURDIR)/$(RMLINES_FIXTURE) $(RMLINES_DIR)/probe.png
	
run: $(ASSETS)
	go run $(CMD) $(ARG)

$(ASSETS): $(UIFILES) ui/pnpm-lock.yaml
	#@cp ui/node_modules/pdfjs-dist/build/pdf.worker.js ui/public/
	$(PNPM) build
	@#remove unneeded stuff, todo: eject
	@rm ui/build/service-worker.js ui/build/precache-manifest* ui/build/asset-manifest.json 2> /dev/null || true

ui/pnpm-lock.yaml: ui/node_modules ui/package.json
	$(PNPM) i
	@touch -mr $(shell ls -Atd $? | head -1) $@

ui/node_modules:
	mkdir -p $@

runui: ui/pnpm-lock.yaml
	$(PNPM) run dev

clean:
	rm -f $(OUT_DIR)/*
	rm -fr $(ASSETS)

test: testui testgo

testui:
	echo "TODO: fix this"
	#CI=true $(PNPM) test

testgo:
	go test ./...

