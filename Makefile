# LeanSignal Agent - developer Makefile
# Release builds are driven by GitHub Actions + goreleaser; these targets are
# for local development and CI checks.

OCB_VERSION    ?= 0.141.0
BINARY         ?= leansignal-agent
BUILD_DIR      ?= _build
GOBIN          ?= $(shell go env GOPATH)/bin

# Pinned VictoriaMetrics version bundled with the agent (single source of truth).
VM_VERSION     := $(shell cat VM_VERSION 2>/dev/null)

# `make local-run` settings - override on the command line if your local setup differs.
# (Keep these free of trailing inline comments: make would fold the spaces into the value.)
# LOCAL_ENDPOINT  = local lean-api gRPC target (h2c)
# LOCAL_VM        = vm-ag (everything) write endpoint
# LOCAL_DATAPLANE = dataplane (demanded subset)
# LOCAL_VM_QUERY  = vm-ag query base (UI edit-mode queries tunnel here)
LOCAL_ENDPOINT  ?= localhost:9090
LOCAL_AGENT_KEY ?= deadbeef-dead-beef-dead-beefdeadbeef
LOCAL_VM        ?= http://localhost:8482/api/v1/write
LOCAL_DATAPLANE ?= http://localhost:8483/api/v1/write
LOCAL_VM_QUERY  ?= http://localhost:8482

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: install-tools
install-tools: ## Install pinned dev tools (ocb, addlicense, goreleaser)
	go install go.opentelemetry.io/collector/cmd/builder@v$(OCB_VERSION)
	go install github.com/google/addlicense@latest
	go install github.com/goreleaser/goreleaser/v2@latest

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race ./components/...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (if installed)
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed; skipping"

.PHONY: license
license: ## Add/refresh SPDX Apache-2.0 headers on first-party sources
	$(GOBIN)/addlicense -s -l apache -c "LeanSignal" -y 2026 components/

.PHONY: license-check
license-check: ## Verify license headers are present (CI)
	$(GOBIN)/addlicense -check -s -l apache -c "LeanSignal" -y 2026 components/

.PHONY: generate
generate: ## Generate the collector distribution sources into _build/ (run after manifest.yaml changes)
	$(GOBIN)/ocb --config manifest.yaml --skip-compilation

.PHONY: compile
compile: ## Fast recompile of _build/ - picks up component code edits via the local replace (generates first if needed)
	@[ -f "$(BUILD_DIR)/go.mod" ] || $(MAKE) generate
	cd $(BUILD_DIR) && CGO_ENABLED=0 go build -trimpath -o $(BINARY) .

.PHONY: build
build: generate compile ## Full build: regenerate sources from manifest.yaml + compile

.PHONY: local-run
local-run: compile ## Build and run against a local lean-api (:8080) + local VictoriaMetrics (:8482)
	@echo "endpoint=$(LOCAL_ENDPOINT)  vm-ag=$(LOCAL_VM)"
	LEANSIGNAL_ENDPOINT="$(LOCAL_ENDPOINT)" \
	LEANSIGNAL_AGENT_KEY="$(LOCAL_AGENT_KEY)" \
	LEANSIGNAL_LOCAL_VM="$(LOCAL_VM)" \
	LEANSIGNAL_DATAPLANE_ENDPOINT="$(LOCAL_DATAPLANE)" \
	LEANSIGNAL_LOCAL_VM_QUERY="$(LOCAL_VM_QUERY)" \
	$(BUILD_DIR)/$(BINARY) --config config/agent-config.local.yaml

.PHONY: snapshot
snapshot: generate ## Local goreleaser snapshot (all platforms, no publish)
	goreleaser release --snapshot --clean

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm dependency update deploy/helm/leansignal-agent
	helm lint deploy/helm/leansignal-agent

.PHONY: helm-template
helm-template: ## Render the Helm chart with example values
	helm template lsa deploy/helm/leansignal-agent -f deploy/helm/leansignal-agent/values-example.yaml

.PHONY: shellcheck
shellcheck: ## Lint install scripts (if shellcheck installed)
	@command -v shellcheck >/dev/null 2>&1 && shellcheck scripts/install/*.sh scripts/release/*.sh || echo "shellcheck not installed; skipping"

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) dist
