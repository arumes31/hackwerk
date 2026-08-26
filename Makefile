SHELL := /bin/sh
.DEFAULT_GOAL := help

VERSION ?= $(shell sh scripts/version.sh 2>/dev/null || printf 0.1.0)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
ifeq ($(OS),Windows_NT)
BUILD_TIME ?= $(shell powershell -NoProfile -Command "[DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')")
else
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
endif
LDFLAGS := -s -w -X example.invalid/hackplan/internal/buildinfo.Version=$(VERSION) -X example.invalid/hackplan/internal/buildinfo.Commit=$(COMMIT) -X example.invalid/hackplan/internal/buildinfo.BuildTime=$(BUILD_TIME)

ifeq ($(OS),Windows_NT)
DOCKER_RUN_PREFIX := set MSYS_NO_PATHCONV=1&&
ENSURE_DIST := if not exist dist mkdir dist
else
DOCKER_RUN_PREFIX := MSYS_NO_PATHCONV=1
ENSURE_DIST := mkdir -p dist
endif

.PHONY: help version assets assets-check workflow-lint dev up down logs clean generate generate-check format format-check lint test test-integration test-migrations test-e2e test-race build build-image image-archive check scan scan-code scan-license scan-image sbom backup-restore-smoke container-smoke release-check

help:
	@printf '%s\n' 'HackWerk: version assets assets-check workflow-lint dev up down logs clean generate generate-check format format-check lint test test-integration test-migrations test-e2e test-race build check scan scan-license backup-restore-smoke container-smoke release-check'

version:
	@printf '%s\n' '$(VERSION)'

assets:
	go tool minify --quiet --type js --output web/assets/static/app.js web/assets/src/app.js
	go tool minify --quiet --type js --output web/assets/static/login-background.js web/assets/src/login-background.js
	go tool minify --quiet --type js --output web/assets/static/login-background-loader.js web/assets/src/login-background-loader.js

assets-check:
	sh scripts/assets-check.sh

workflow-lint:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

dev: up

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f app worker

clean:
	rm -rf -- bin dist

generate: assets
	go tool templ generate
	go tool sqlc generate -f db/sqlc.yaml

generate-check:
	sh scripts/generate-check.sh

format:
	gofmt -w $$(find cmd db internal tests web -type f -name '*.go')
	go tool templ fmt web/templates

format-check:
	test -z "$$(gofmt -l $$(find cmd db internal tests web -type f -name '*.go'))"
	go tool templ fmt -fail web/templates

lint:
	go vet ./...
	go tool golangci-lint run ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/integration/...

test-migrations: build-image
	go test -count=1 -tags=integration ./tests/integration/... -run '^TestMigrationsUpDownUp$$'
	sh scripts/release/migration-smoke.sh

test-e2e:
	go test -count=1 -tags=e2e ./tests/e2e/...

test-race:
	go test -race ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/hackwerk ./cmd/hackwerk

check: generate-check format-check workflow-lint lint test test-integration build

scan-license:
	go tool go-licenses check --ignore example.invalid/hackplan ./cmd/hackwerk

scan-code: scan-license
	go mod verify
	go tool govulncheck ./...
	go run ./cmd/repo-audit

image-archive: build-image
	$(ENSURE_DIST)
	docker save --output dist/hackwerk-scan.tar hackwerk-scan:local

scan-image: image-archive
	$(DOCKER_RUN_PREFIX) docker run --rm -v "$(CURDIR)/dist:/work:ro" -v "$(CURDIR)/dist:/out" ghcr.io/aquasecurity/trivy:0.74.0 image --input /work/hackwerk-scan.tar --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed --format json --output /out/trivy-report.json

sbom: image-archive
	$(DOCKER_RUN_PREFIX) docker run --rm -v "$(CURDIR)/dist:/work:ro" -v "$(CURDIR)/dist:/out" anchore/syft:v1.51.0 docker-archive:/work/hackwerk-scan.tar -o cyclonedx-json=/out/hackwerk.cdx.json -o spdx-json=/out/hackwerk.spdx.json

build-image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t hackwerk-scan:local .

scan: scan-code scan-image sbom

backup-restore-smoke: build-image
	sh scripts/release/backup-restore-smoke.sh

container-smoke: build-image
	sh scripts/release/container-smoke.sh

release-check: clean check test-race test-e2e test-migrations backup-restore-smoke scan container-smoke
