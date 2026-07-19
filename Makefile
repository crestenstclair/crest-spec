.PHONY: all build install test test-e2e test-race test-production test-cross-owner-production test-fmt test-sqlc release-test vet fmt lint lint-fix mocks sqlc update clean dashboard

all: fmt test lint

build:
	go build -o bin/crest-spec ./cmd/crest-spec

install:
	go install ./cmd/crest-spec

test:
	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE= CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE= go test -count=1 ./...

test-e2e:
	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE= CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE= go test -count=1 -run '^TestRealBinary' -v ./e2e

test-race:
	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE= CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE= go test -race -count=1 ./...

test-production:
	@if [ -z "$$CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE" ]; then \
		echo "test-production requires CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE=/path/to/preserved/crest-synth" >&2; \
		exit 2; \
	fi
	go test -count=1 -run '^TestRealBinaryRecoversPreservedProductionSession$$' -v ./e2e

test-cross-owner-production:
	@if [ -z "$$CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE" ]; then \
		echo "test-cross-owner-production requires CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE=/path/to/captured/crest-synth" >&2; \
		exit 2; \
	fi
	go test -count=1 -run '^TestRealBinaryRecoversCapturedCrossOwnerProductionSession$$' -v ./e2e

test-fmt:
	@unformatted="$$(gofmt -l $$(rg --files -g '*.go'))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files require gofmt:" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi

test-sqlc:
	@command -v sqlc >/dev/null 2>&1 || { echo "test-sqlc requires sqlc" >&2; exit 2; }
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/internal"; \
	cp sqlc.yaml "$$tmp/"; \
	cp -R sql migrations "$$tmp/"; \
	(cd "$$tmp" && sqlc generate); \
	diff -ru internal/db "$$tmp/internal/db"

release-test:
	@if [ -z "$$CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE" ]; then \
		echo "release-test requires CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE=/path/to/preserved/crest-synth" >&2; \
		exit 2; \
	fi
	@if [ -z "$$CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE" ]; then \
		echo "release-test requires CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE=/path/to/captured/crest-synth" >&2; \
		exit 2; \
	fi
	$(MAKE) test-fmt test-sqlc vet
	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE= CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE= go test -count=1 ./...
	CREST_SPEC_PRODUCTION_RECOVERY_FIXTURE= CREST_SPEC_CROSS_OWNER_RECOVERY_FIXTURE= go test -race -count=1 ./...
	$(MAKE) test-production test-cross-owner-production

vet:
	go vet ./...

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

dashboard: build
	CREST_SPEC_SPEC_DIR=./e2e/spec ./bin/crest-spec dashboard
