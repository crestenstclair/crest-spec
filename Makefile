.PHONY: all build install test fmt lint lint-fix mocks sqlc update clean dashboard

all: fmt test lint

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

dashboard: build
	CREST_SPEC_SPEC_DIR=./e2e/spec ./bin/crest-spec dashboard
