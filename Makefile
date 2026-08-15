GO ?= go
BINARY ?= bin/yoyodyne

.PHONY: build test race vet check
.NOTPARALLEL: check

build:
	mkdir -p $(dir $(BINARY))
	$(GO) build -o $(BINARY) ./cmd/yoyodyne

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: test race vet
