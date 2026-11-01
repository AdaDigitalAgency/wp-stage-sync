VERSION_FILE := .version
CURRENT_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null || echo "0.0.0")

.PHONY: build version release-patch release-minor release-major ship

build:
	go build -ldflags "-s -w -X main.version=$(CURRENT_VERSION)" -o wp-stage-sync ./cmd/wp-stage-sync/

version:
	@echo "Current version: v$(CURRENT_VERSION)"

release-patch:
	@go run ./cmd/release patch

release-minor:
	@go run ./cmd/release minor

release-major:
	@go run ./cmd/release major

ship:
	@go run ./cmd/release ship
