# Code generation for the LeanSignal agent control-plane protos.
#
# Preferred: `buf generate` (uses buf.gen.yaml). Fallback: protoc (this Makefile),
# which only needs `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` on PATH.
MODULE  := github.com/leansignal/leansignal-agent/proto
PROTOS  := $(shell find proto -name '*.proto')

.PHONY: generate
generate: ## Generate Go from the .proto files (protoc)
	protoc \
	  --proto_path=proto \
	  --go_out=. --go_opt=module=$(MODULE) \
	  --go-grpc_out=. --go-grpc_opt=module=$(MODULE) \
	  $(PROTOS)
	go mod tidy

.PHONY: generate-buf
generate-buf: ## Generate via buf (if installed)
	buf generate
	go mod tidy

.PHONY: lint
lint: ## buf lint (if installed)
	buf lint

.PHONY: tidy
tidy:
	go mod tidy
