# Developer lifecycle for go-template.
APP        := go-template
MODULE     := github.com/luisfelipecoelho/go-template
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
COVER_MIN  := 85
DUPE_MAX   := 3
FUZZTIME   ?= 30s
ENV        ?= dev

# Tool versions are pinned and MUST match .github/workflows/ci.yml. With
# @latest, a tool that gains a check between your run and CI's fails the build
# only after you push — local and CI must reach the same verdict.
#
# golangci-lint must also be built with a Go at least as new as this module's
# `go` directive: its release binary embeds a toolchain, and an older one
# refuses the module outright ("the Go language version used to build
# golangci-lint is lower than the targeted Go version"). v2.13.2 is the first
# release built with go1.27 — do not pin below it while go.mod targets 1.27.
GOLANGCI_VERSION  := v2.13.2
GOVULNCHECK_VERSION := v1.6.0
JSCPD_VERSION       := 4.0.5

LDFLAGS    := -s -w \
	-X $(MODULE)/internal/cli.version=$(VERSION) \
	-X $(MODULE)/internal/cli.commit=$(COMMIT) \
	-X $(MODULE)/internal/cli.buildTime=$(BUILD_TIME)

.DEFAULT_GOAL := help

.PHONY: help build run run-worker test cover bench fuzz lint dupe vuln tools hooks \
	tidy clean docker-build compose-up compose-down k8s-render k8s-apply \
	k8s-delete spec-lint gates

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary into ./bin with version metadata
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd

run: ## Run the HTTP server locally
	go run ./cmd server

run-worker: ## Run the worker locally
	go run ./cmd worker

test: ## Run all tests with the race detector
	go test -race ./...

cover: ## Run tests with coverage and enforce the $(COVER_MIN)% gate
	go test -race -coverprofile=coverage.out -covermode=atomic ./cmd/... ./internal/... ./pkg/...
	@go tool cover -func=coverage.out | tail -1
	@go tool cover -func=coverage.out | tail -1 | awk '{gsub(/%/,"",$$3); if ($$3+0 < $(COVER_MIN)) {printf "coverage %.1f%% is below $(COVER_MIN)%%\n", $$3; exit 1}}'

bench: ## Run every benchmark with allocation counts
	go test -bench=. -benchmem -run='^$$' ./cmd/... ./internal/... ./pkg/...

fuzz: ## Fuzz each target for $(FUZZTIME) (override: make fuzz FUZZTIME=5m)
	@# Go fuzzes ONE target per invocation, so each is driven in turn. Seed
	@# corpora live in testdata/fuzz/ and are committed: a crash found today
	@# becomes a permanent regression test.
	@#
	@# `make test` already replays those seeds, which is what gates every PR.
	@# This target is the SEARCH for new inputs — minutes per run. In CI it is
	@# on demand, behind the `fuzz` PR label, or on a weekly schedule that
	@# ships DISABLED (set the FUZZ_SCHEDULE repo variable to enable; see
	@# .github/workflows/fuzz.yml for the cost baseline). Run it locally after
	@# touching a parser.
	@set -e; for pkg in $$(go list ./cmd/... ./internal/... ./pkg/...); do \
		for fn in $$(go test -list='^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz' || true); do \
			echo "==> $$pkg $$fn"; \
			go test -run='^$$' -fuzz="^$$fn$$" -fuzztime=$(FUZZTIME) $$pkg; \
		done; \
	done

lint: ## gofmt check + go vet + golangci-lint (install via make tools)
	@fmt_out=$$(gofmt -l .); if [ -n "$$fmt_out" ]; then echo "gofmt needed:"; echo "$$fmt_out"; exit 1; fi
	go vet ./...
	golangci-lint run

dupe: ## Copy-paste detection (fails above $(DUPE_MAX)% duplication)
	npx -y jscpd@$(JSCPD_VERSION) .

vuln: ## Scan dependencies for known vulnerabilities
	govulncheck ./...

gates: lint dupe cover vuln ## Run every quality gate the CI enforces

tools: ## Install lint/vuln tooling into GOPATH/bin (versions pinned to CI)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

hooks: ## Install git hooks (blocks committing .env / .envrc files)
	git config core.hooksPath .githooks
	@echo "git hooks enabled from .githooks/"

tidy: ## go mod tidy
	go mod tidy

clean: ## Remove build artifacts
	rm -rf bin coverage.out

docker-build: ## Build the production image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(APP):$(VERSION) -t $(APP):latest .

compose-up: ## Start server + worker + OTel collector locally
	docker compose up --build -d

compose-down: ## Stop the local stack
	docker compose down

k8s-render: ## Render an overlay (ENV=dev|staging|prod, default dev)
	kubectl kustomize ops/k8s/overlays/$(ENV)

k8s-apply: ## Apply an overlay to the current context (ENV=dev|staging|prod)
	kubectl apply -k ops/k8s/overlays/$(ENV)

k8s-delete: ## Delete an overlay from the current context (ENV=dev|staging|prod)
	kubectl delete -k ops/k8s/overlays/$(ENV)

spec-lint: ## Lint the OpenAPI contract
	npx -y @stoplight/spectral-cli lint api-spec.yaml --ruleset .spectral.yaml
