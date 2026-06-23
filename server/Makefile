# Ouro Pass Issuer — build & test targets.
# Run from server/ (the Go module root):  make test  |  make test-integration
GO ?= go

# Integration tests (real Postgres). If OUROPASS_TEST_PG_DSN is set they use that
# instance; otherwise testcontainers-go starts an ephemeral Postgres (needs Docker).
OUROPASS_TEST_PG_DSN ?=
export OUROPASS_TEST_PG_DSN

.PHONY: build vet fmt tidy test test-e2e cover test-integration test-all clean

## build: compile all packages
build:
	$(GO) build ./...

## vet: static checks (default + integration tag)
vet:
	$(GO) vet ./...
	$(GO) vet -tags integration ./internal/inttest/...

## fmt: format all sources in place
fmt:
	gofmt -l -w .

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

## test: unit + end-to-end (SQLite + mocks, no external deps)
test:
	$(GO) test -count=1 ./...

## test-e2e: just the full-router end-to-end suite
test-e2e:
	$(GO) test -count=1 ./internal/e2e/...

## cover: unit + e2e with a total coverage summary
cover:
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1

## test-integration: Postgres concurrency/dialect tests (needs PG via DSN or Docker)
test-integration:
	$(GO) test -tags integration -count=1 -timeout 300s ./internal/inttest/...

## test-all: everything (unit + e2e + integration)
test-all: test test-integration

clean:
	rm -f coverage.out
	$(GO) clean -testcache
