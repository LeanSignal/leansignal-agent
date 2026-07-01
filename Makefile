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
# LOCAL_VM        = vm-ag base URL (the config builds write + the agent builds query)
# LOCAL_DATAPLANE = dataplane base URL (demanded subset)
LOCAL_ENDPOINT  ?= localhost:9090
LOCAL_AGENT_KEY ?= deadbeef-dead-beef-dead-beefdeadbeef
LOCAL_VM        ?= http://localhost:8482
LOCAL_DATAPLANE ?= http://localhost:8483

# `make cloud-run` settings - point the local agent at a cloud tenant over TLS(443).
# Usually you only set TENANT (+ CLOUD_AGENT_KEY); the gRPC control host and the
# vmauth ingest host are derived as <tenant>-grpc.<domain> and <tenant>-ingest.<domain>.
# (The <tenant>-api host is REST/UI only - the agent never connects to it.)
# Override CLOUD_ENDPOINT / CLOUD_DATAPLANE directly for a non-standard host.
TENANT           ?= mb1
CLOUD_DOMAIN     ?= eu11.leansignal.io
CLOUD_ENDPOINT   ?= $(TENANT)-grpc.$(CLOUD_DOMAIN):443
CLOUD_AGENT_KEY  ?=
CLOUD_VM         ?= http://localhost:8482
CLOUD_DATAPLANE  ?= https://$(TENANT)-ingest.$(CLOUD_DOMAIN)

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

.PHONY: local-build
local-build: compile ## Build the local agent binary into _build/ (run once; re-run after code edits)

.PHONY: local-run
local-run: ## Run the pre-built agent vs local lean-api (:9090) + VM (:8482). Run `make local-build` first.
	@[ -x "$(BUILD_DIR)/$(BINARY)" ] || { echo "$(BUILD_DIR)/$(BINARY) not found — run 'make local-build' first"; exit 1; }
	@echo "endpoint=$(LOCAL_ENDPOINT)  vm-ag=$(LOCAL_VM)"
	LEANSIGNAL_ENDPOINT="$(LOCAL_ENDPOINT)" \
	LEANSIGNAL_AGENT_KEY="$(LOCAL_AGENT_KEY)" \
	LEANSIGNAL_LOCAL_VM="$(LOCAL_VM)" \
	LEANSIGNAL_DATAPLANE_ENDPOINT="$(LOCAL_DATAPLANE)" \
	$(BUILD_DIR)/$(BINARY) --config config/agent-config.local.yaml

.PHONY: cloud-run
cloud-run: ## Run the pre-built agent vs a CLOUD tenant over TLS(443). Requires CLOUD_AGENT_KEY. Run `make local-build` first.
	@[ -x "$(BUILD_DIR)/$(BINARY)" ] || { echo "$(BUILD_DIR)/$(BINARY) not found — run 'make local-build' first"; exit 1; }
	@[ -n "$(CLOUD_AGENT_KEY)" ] || { echo "set CLOUD_AGENT_KEY=<tenant agent key> (see the tenant's agents table)"; exit 1; }
	@echo "tenant=$(TENANT)  grpc=$(CLOUD_ENDPOINT)  ingest=$(CLOUD_DATAPLANE)  (api=$(TENANT)-api.$(CLOUD_DOMAIN), not used)  local-vm=$(CLOUD_VM)"
	LEANSIGNAL_ENDPOINT="$(CLOUD_ENDPOINT)" \
	LEANSIGNAL_AGENT_KEY="$(CLOUD_AGENT_KEY)" \
	LEANSIGNAL_LOCAL_VM="$(CLOUD_VM)" \
	LEANSIGNAL_DATAPLANE_ENDPOINT="$(CLOUD_DATAPLANE)" \
	$(BUILD_DIR)/$(BINARY) --config config/agent-config.cloud.yaml

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
