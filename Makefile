SHELL := /bin/sh
.DEFAULT_GOAL := help

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X example.invalid/hackplan/internal/buildinfo.Version=$(VERSION) -X example.invalid/hackplan/internal/buildinfo.Commit=$(COMMIT) -X example.invalid/hackplan/internal/buildinfo.BuildTime=$(BUILD_TIME)

.PHONY: help dev up down logs generate generate-check format lint test test-integration test-e2e test-race build check scan release-check

help:
	@printf '%s\n' 'HackWerk: dev up down logs generate generate-check format lint test test-integration test-e2e test-race build check scan release-check'

dev: up

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f app worker

generate:
	go tool templ generate
	go tool sqlc generate -f db/sqlc.yaml

generate-check:
	sh scripts/generate-check.sh

format:
	gofmt -w $$(find cmd db internal tests web -type f -name '*.go')
	go tool templ fmt web/templates

lint:
	go vet ./...
	go tool golangci-lint run ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/integration/...

test-e2e:
	go test -count=1 -tags=e2e ./tests/e2e/...

test-race:
	go test -race ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/hackwerk ./cmd/hackwerk

check: generate-check format lint test test-integration build

scan:
	go tool govulncheck ./...

release-check: check test-race test-e2e scan
