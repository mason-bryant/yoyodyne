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

.PHONY: build test race vet fmt fmtcheck cachecheck check dist dist-verify clean-dist release release-notes
.NOTPARALLEL: check

# Every Go command below writes what it compiles to the build cache before it
# compiles anything, so a cache the environment does not grant fails the whole
# gate at setup -- "operation not permitted" on a path in the message and no
# mention of a cache anywhere, which reads as a broken toolchain rather than as
# a directory nobody granted. That is exactly what an agent sandbox looks like
# from in here: it grants writes to the worktree, to .git, and to TMPDIR, and
# the cache defaults under the user's home. The harness sets GOCACHE for the
# runs it makes, so this is for an environment it did not make -- an interactive
# agent session, or any other sandbox -- and it names the redirect rather than
# leaving it to be rediscovered.
cachecheck:
	@cache="$$($(GO) env GOCACHE)"; \
	if ! mkdir -p "$$cache" 2>/dev/null || ! touch "$$cache/.yoyodyne-writable" 2>/dev/null; then \
		echo "The Go build cache at $$cache cannot be written, so every Go command here fails at setup." >&2; \
		echo "Point it somewhere this environment grants, such as its temporary directory:" >&2; \
		echo "  export GOCACHE=\"$${TMPDIR:-/tmp}/go-build\"" >&2; \
		echo "docs/developing-yoyo.md says what else this affects." >&2; \
		exit 1; \
	fi; \
	rm -f "$$cache/.yoyodyne-writable"

build: cachecheck
	mkdir -p $(dir $(BINARY))
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/yoyo

test: cachecheck
	$(GO) test ./...

race: cachecheck
	$(GO) test -race ./...

vet: cachecheck
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
