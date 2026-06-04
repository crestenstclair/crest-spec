# krusty-spec development Makefile
# Run `make help` to see available targets

SPEC      := crest-spec.ts
CLI       := bun run ../src/cli/main.ts
MODEL     := claude-sonnet-4-6
TEST_DIR  := test-project
FIXTURE   := fixtures/test-spec.ts

.PHONY: help test setup plan apply apply-inc apply-target validate graph clean reset nuke

# Show available targets
help:
	@echo "krusty-spec development"
	@echo ""
	@echo "  Engine (run from repo root):"
	@echo "    make test          run unit tests"
	@echo "    make validate      check spec invariants"
	@echo "    make plan          show what would change"
	@echo "    make graph         dump resource dependency graph (DOT)"
	@echo ""
	@echo "  Generate (against test-project/):"
	@echo "    make setup         create test-project/ from fixtures/test-spec.ts"
	@echo "    make apply         generate all resources (flat mode)"
	@echo "    make apply-inc     generate with incremental wave verification"
	@echo "    make apply-target T=<id>  generate a single resource"
	@echo ""
	@echo "  Cleanup:"
	@echo "    make clean         remove generated code from test-project/"
	@echo "    make reset         clean + delete state db (full re-gen next apply)"
	@echo "    make nuke          reset + remove test-project/ entirely"
	@echo ""
	@echo "  Variables:"
	@echo "    MODEL=$(MODEL)     override with MODEL=claude-opus-4-6"
	@echo "    SPEC=$(SPEC)       spec filename inside test-project/"

# ── Engine ───────────────────────────────────────────────

# Run the krusty-spec unit tests
test:
	bun test tests/engine/ tests/registry/ tests/planner/ tests/invariants/ tests/dsl/ tests/cli/

# ── Setup ────────────────────────────────────────────────

# Create test-project/ and copy the fixture spec into it
setup:
	@mkdir -p $(TEST_DIR)
	@cp $(FIXTURE) $(TEST_DIR)/$(SPEC)
	@echo "Created $(TEST_DIR)/ with $(SPEC) from $(FIXTURE)"

# Ensure test-project exists before any command that needs it
$(TEST_DIR)/$(SPEC): $(FIXTURE)
	@$(MAKE) --no-print-directory setup

# ── Inspect ──────────────────────────────────────────────

# Check structural invariants without generating
validate: $(TEST_DIR)/$(SPEC)
	cd $(TEST_DIR) && $(CLI) validate --spec $(SPEC)

# Show the plan (what would be created/modified/destroyed)
plan: $(TEST_DIR)/$(SPEC)
	cd $(TEST_DIR) && $(CLI) plan --spec $(SPEC) --model $(MODEL)

# Dump the resource dependency graph as DOT
graph: $(TEST_DIR)/$(SPEC)
	cd $(TEST_DIR) && $(CLI) graph --spec $(SPEC)

# ── Generate ─────────────────────────────────────────────

# Generate all resources in flat concurrent mode
apply: $(TEST_DIR)/$(SPEC)
	cd $(TEST_DIR) && $(CLI) apply --spec $(SPEC) --model $(MODEL)

# Generate with incremental wave verification (build between waves)
apply-inc: $(TEST_DIR)/$(SPEC)
	cd $(TEST_DIR) && $(CLI) apply --spec $(SPEC) --model $(MODEL) --incremental

# Generate a single resource by ID (e.g., make apply-target T=aggregate.Catalog.Product)
apply-target: $(TEST_DIR)/$(SPEC)
	@test -n "$(T)" || (echo "Usage: make apply-target T=<resource-id>" && exit 1)
	cd $(TEST_DIR) && $(CLI) apply --spec $(SPEC) --model $(MODEL) --target $(T)

# ── Cleanup ──────────────────────────────────────────────

# Remove generated source files but keep the state db
clean:
	rm -rf $(TEST_DIR)/src $(TEST_DIR)/tests
	@echo "Cleaned generated code from $(TEST_DIR)/"

# Remove generated code AND state db (next apply does a full regeneration)
reset: clean
	rm -f $(TEST_DIR)/crest-spec.db $(TEST_DIR)/crest-spec.db-shm $(TEST_DIR)/crest-spec.db-wal
	rm -f $(TEST_DIR)/apply-errors.log
	@echo "State reset — next apply will regenerate everything"

# Remove the entire test-project directory
nuke:
	rm -rf $(TEST_DIR)
	@echo "Nuked $(TEST_DIR)/ — run 'make setup' to recreate"
