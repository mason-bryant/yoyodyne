GO ?= go
BINARY ?= bin/yoyo
DIST ?= dist

# What a build reports as its version. A release passes the tag in; a build
# from a checkout describes itself from git, so a bug report about a local
# binary names a commit rather than only saying "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# The platforms a release ships prebuilt binaries for. macOS is where Yoyodyne
# is actually used; linux/amd64 is built and run by CI and nothing else. See
# the README's install section, which says so rather than implying parity.
PLATFORMS ?= darwin/arm64 darwin/amd64 linux/amd64

.PHONY: build test race vet fmt fmtcheck check walk dist dist-verify clean-dist
.NOTPARALLEL: check

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/yoyo

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

# Formatting is a gate, not a suggestion: gofmt -l exits 0 even when it finds
# unformatted files, so the result has to be inspected rather than trusted.
fmtcheck:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi

check: fmtcheck test race vet

# The adoption walkthrough executes the README's install and getting-started
# claims against a throwaway project that is not this one. It is the only check
# that reads those claims the way a stranger would; every other check reads the
# code, so a README that promises something the product stopped doing passes all
# of them. It must run before a change to the README's install or first-run path
# lands -- see docs/developing-yoyo.md.
#
# It is not folded into `check` for one reason only: it requires `bd` on PATH and
# exits 2 without one, and the CI runner installs no `bd`, so `check` would fail
# every run rather than gate anything. That is an unmet dependency in CI rather
# than a judgement that this is optional. Making it a real gate needs `bd`
# installed in .github/workflows/ci.yml and `make walk` added to the checks list
# in .yoyodyne/config.yaml, which is the list a run actually integrates behind.
walk:
	scripts/walk-adoption.sh

clean-dist:
	rm -rf $(DIST)

# One release's binaries. This is the whole of what makes a release, so a later
# release is a rerun of this target rather than a fresh act of judgement: pass
# the tag as VERSION and the archives, their names, and their checksums follow.
# CGO is off and paths are trimmed so the build does not depend on the machine
# it ran on.
dist: clean-dist
	mkdir -p $(DIST)
	@set -e; for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		stem=yoyo_$(VERSION)_$${goos}_$${goarch}; \
		echo "building $$stem"; \
		mkdir -p $(DIST)/$$stem; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GO) build -trimpath \
			-ldflags '$(LDFLAGS)' -o $(DIST)/$$stem/yoyo ./cmd/yoyo; \
		tar -czf $(DIST)/$$stem.tar.gz -C $(DIST)/$$stem yoyo; \
		rm -rf $(DIST)/$$stem; \
	done
	@set -e; cd $(DIST); \
	if command -v shasum >/dev/null 2>&1; then \
		shasum -a 256 *.tar.gz > checksums.txt; \
	elif command -v sha256sum >/dev/null 2>&1; then \
		sha256sum *.tar.gz > checksums.txt; \
	else \
		echo "dist needs shasum or sha256sum to write checksums" >&2; exit 1; \
	fi
	@echo "wrote $(DIST)/checksums.txt"

# A release whose binaries do not name the tag they were built from is worse
# than no release, because every report filed against it is unattributable.
# This unpacks the archive for whatever platform it is running on and asks the
# binary what it is, so the check that guards a release is the same one CI runs
# on every change rather than a copy of it that first executes at a tag push.
dist-verify: dist
	@set -e; \
	goos=$$($(GO) env GOOS); goarch=$$($(GO) env GOARCH); \
	stem=yoyo_$(VERSION)_$${goos}_$${goarch}; \
	archive=$(DIST)/$$stem.tar.gz; \
	if [ ! -f "$$archive" ]; then \
		echo "dist-verify: no $$archive; PLATFORMS does not cover $$goos/$$goarch" >&2; \
		exit 1; \
	fi; \
	unpacked=$(DIST)/.verify; \
	rm -rf "$$unpacked"; mkdir -p "$$unpacked"; \
	trap 'rm -rf "$$unpacked"' EXIT; \
	tar -xzf "$$archive" -C "$$unpacked"; \
	reported=$$("$$unpacked/yoyo" version); \
	if [ "$$reported" != "$(VERSION)" ]; then \
		echo "dist-verify: yoyo version reported '$$reported', expected '$(VERSION)'" >&2; \
		exit 1; \
	fi; \
	echo "$$stem reports version $$reported"
