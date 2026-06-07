.PHONY: all build install test fmt lint lint-fix mocks sqlc update clean
.PHONY: help plan apply apply-target validate graph status e2e-clean e2e-reset e2e-nuke

CLI       := ./bin/crest-spec
SPEC_DIR  := ./e2e/spec
MODEL     := claude-sonnet-4-6

all: fmt test lint

help:
	@echo "crest-spec development"
	@echo ""
	@echo "  Build:"
	@echo "    make build         compile binary to bin/crest-spec"
	@echo "    make install       go install"
	@echo "    make test          run unit tests"
	@echo "    make fmt           format code"
	@echo "    make lint          run linter"
	@echo ""
	@echo "  E2E (against e2e/spec/):"
	@echo "    make plan          show what would change"
	@echo "    make apply         generate all resources (real LLM calls)"
	@echo "    make apply-target T=<id>  generate a single resource"
	@echo "    make validate      check spec structural validity"
	@echo "    make graph         dump resource dependency graph"
	@echo "    make status        show current state"
	@echo ""
	@echo "  Cleanup:"
	@echo "    make e2e-clean     remove generated code from e2e/"
	@echo "    make e2e-reset     clean + delete state db"
	@echo "    make e2e-nuke      reset + remove e2e/output/"
	@echo ""
	@echo "  Variables:"
	@echo "    MODEL=$(MODEL)     override with MODEL=claude-opus-4-6"
	@echo "    SPEC_DIR=$(SPEC_DIR)"

build:
	go build -o bin/crest-spec ./cmd/crest-spec

install:
	go install ./cmd/crest-spec

test:
	go test ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix

mocks:
	go generate ./internal/mocks/...

sqlc:
	sqlc generate

update:
	go get -u ./...
	go mod tidy

clean:
	rm -rf bin/

# ── E2E targets ─────────────────────────────────────────

plan: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) CREST_SPEC_GENERATE_MODEL=$(MODEL) $(CLI) plan

apply: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) CREST_SPEC_GENERATE_MODEL=$(MODEL) $(CLI) apply

apply-target: build
	@test -n "$(T)" || (echo "Usage: make apply-target T=<resource-id>" && exit 1)
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) CREST_SPEC_GENERATE_MODEL=$(MODEL) $(CLI) apply --target $(T)

validate: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) $(CLI) validate

graph: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) $(CLI) graph

status: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) $(CLI) status

dashboard: build
	CREST_SPEC_SPEC_DIR=$(SPEC_DIR) $(CLI) dashboard

e2e-clean:
	rm -rf e2e/output
	@echo "Cleaned generated code from e2e/"

e2e-reset: e2e-clean
	rm -rf .crest-spec/
	@echo "State reset — next apply will regenerate everything"

e2e-nuke: e2e-reset
	@echo "Nuked e2e output and state"
