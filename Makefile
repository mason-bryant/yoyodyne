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

.PHONY: build test race vet fmt fmtcheck links check dist dist-verify clean-dist release release-notes
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

# Every link this repository's Markdown makes to itself, resolved rather than
# believed: a relative path that is not here, or a `#fragment` naming a heading
# its target does not carry. Neither is visible to somebody reading a patch --
# a relative path resolves only against the directory layout, and a slug is
# written down nowhere -- which is why reviews of this project kept ending in
# "the anchor could not be verified from the evidence available".
#
# It asserts what `test` already asserts, and it is named separately so that a
# documentation change is answered in a second rather than behind the whole Go
# suite. The whole package runs rather than one test by name: the fixtures beside
# the repository-wide pass are what prove the checker still catches anything, and
# a `-run` pattern is a second place for a test name to drift out of. `-count=1`
# is deliberate -- the assertion is about files outside the package, so a cached
# pass would be reporting on the documents of some earlier run.
links:
	$(GO) test -count=1 ./internal/doclink

# `links` runs first and is the cheapest thing here, so a moved anchor stops a
# change before `race` spends a suite on one that is already red.
check: fmtcheck links test race vet

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
	@# This path has consumers outside this file: the release workflow publishes
	@# it by name, and `make release` reports the cut's checksums from it. A
	@# rename that missed them would otherwise surface as a published release
	@# with no checksums, or a cut that prints none, so it fails here instead --
	@# in the target CI runs on every change.
	@if [ ! -f $(DIST)/checksums.txt ]; then \
		echo "dist: no $(DIST)/checksums.txt; the checksum step wrote somewhere the release does not read" >&2; \
		exit 1; \
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

# Cutting one release, gate included. `dist` is what a release consists of;
# this is the one invocation around it that makes a daily cadence cheap enough
# to keep and safe enough to trust: the adoption walkthrough and `check` green
# first, then the archives and checksums for the tag, then the tag itself. A
# red gate refuses the cut, names what was red, and writes nothing. Publishing
# stays the operator's own `git push origin <tag>`, which the release workflow
# acts on.
#
# VERSION carries a git-describe default so `build` and `dist` work from a
# checkout, and that default is not a release tag. Pass it on only where
# somebody actually set it, so `make release` with nothing set asks for a tag
# rather than cutting whatever the checkout happens to describe itself as.
release:
	scripts/cut-release.sh $(if $(filter command line environment,$(origin VERSION)),$(VERSION))

# One release's notes, drafted from the work items that landed since the last
# tag and then edited: which work is key functionality, which is an enhancement,
# and which fix is critical enough to go to the top is a judgement the draft
# does not make. `release` above refuses a tag whose notes are missing and
# drafts them for you, so this is for drafting ahead of the cut, or again after
# more work lands. VERSION is withheld the same way, for the same reason.
release-notes:
	bash scripts/release-notes.sh $(if $(filter command line environment,$(origin VERSION)),$(VERSION))
