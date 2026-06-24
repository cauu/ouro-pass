# Ouro Pass Issuer — build & test targets.
# Run from server/ (the Go module root):  make test  |  make test-integration
GO ?= go

# Integration tests (real Postgres). If OUROPASS_TEST_PG_DSN is set they use that
# instance; otherwise testcontainers-go starts an ephemeral Postgres (needs Docker).
OUROPASS_TEST_PG_DSN ?=
export OUROPASS_TEST_PG_DSN

# ---- Local dev / manual browser-wallet testing (override on the command line) ----
# These FIELD_KEY/SERVER_SALT values are fixed and INSECURE — dev only, so the
# persistent .dev/ouro.db keeps working across runs. Never use them in production.
DEV_ADDR         ?= :8080
DEV_POOL_ID      ?= pool1abc
DEV_NETWORK      ?=               # wallet network the page expects; blank → issuer default (a testnet) → use a testnet wallet, or set =mainnet
DEV_FIELD_KEY    ?= 0000000000000000000000000000000000000000000000000000000000000001
DEV_SERVER_SALT  ?= 00000000000000000000000000000007
DEV_TELEGRAM_BOT ?= ouro_dev_bot
DEV_DB           ?= file:.dev/ouro.db
DEV_REDIRECT     ?= http://localhost:9999/cb
DEV_OWNER_KEYS   ?=               # comma-sep owner stake-key hashes; set to log in to /admin as owner

# ---- Admin SPA (../web) embedded into the issuer binary ----
WEB_DIR      ?= ../web
ADMINUI_DIST ?= internal/httpapi/adminui/dist

.PHONY: build vet fmt tidy test test-e2e cover test-integration test-all clean dev dev-stop dev-seed-client web web-clean stake-hash

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

## dev: run the issuer locally for manual/browser-wallet testing (OAuth on, mock chain).
## Open http://localhost:8080/bind in a browser with a Cardano wallet — a real
## wallet signature returns not_eligible (403), which proves the S0003 signature
## path. Override e.g.: make dev DEV_NETWORK=preprod DEV_ADDR=:9000
dev:
	@mkdir -p .dev
	$(GO) build -o .dev/issuer ./cmd/issuer
	@OUROPASS_ADDR=$(DEV_ADDR) \
	OUROPASS_POOL_ID=$(DEV_POOL_ID) \
	OUROPASS_NETWORK=$(DEV_NETWORK) \
	OUROPASS_CHAIN_KIND=mock \
	OUROPASS_FIELD_KEY=$(DEV_FIELD_KEY) \
	OUROPASS_SERVER_SALT=$(DEV_SERVER_SALT) \
	OUROPASS_TELEGRAM_BOT=$(DEV_TELEGRAM_BOT) \
	OUROPASS_OWNER_KEYS=$(DEV_OWNER_KEYS) \
	OUROPASS_DB_DSN="$(DEV_DB)" \
	exec .dev/issuer

## dev-stop: kill a leftover dev issuer still holding the port
dev-stop:
	@pkill -f '\.dev/issuer' && echo "stopped dev issuer" || echo "no dev issuer running"

## dev-seed-client: insert a test OAuth client into the dev DB so /connect works,
## then print the authorization URL. Needs sqlite3 and a prior `make dev` (to
## create/migrate .dev/ouro.db). Run `make dev` once, stop it, then this, then `make dev` again.
dev-seed-client:
	@command -v sqlite3 >/dev/null || { echo "sqlite3 not found"; exit 1; }
	@test -f .dev/ouro.db || { echo "run 'make dev' once first to create .dev/ouro.db"; exit 1; }
	sqlite3 .dev/ouro.db "INSERT OR REPLACE INTO OAuthClient (client_id,name,client_type,client_secret_hash,party,redirect_uris,allowed_audiences,allowed_scopes,pkce_required,status,created_at) VALUES ('testcli','Test','confidential',NULL,'first_party','[\"$(DEV_REDIRECT)\"]','[\"app:test\"]','[\"read\"]',0,'active','2026-01-01T00:00:00Z');"
	@echo ""
	@echo "Seeded client 'testcli'. Authorization URL:"
	@echo "  http://localhost$(DEV_ADDR)/connect?response_type=code&client_id=testcli&redirect_uri=$(DEV_REDIRECT)&aud=app:test&state=xyz"

## web: build the Admin SPA (../web) and stage it into the issuer embed dir, so a
## subsequent `go build`/`make build` bakes the UI into the binary (served at /admin).
web:
	cd $(WEB_DIR) && pnpm install --frozen-lockfile && pnpm build
	rm -rf $(ADMINUI_DIST)
	mkdir -p $(ADMINUI_DIST)
	cp -R $(WEB_DIR)/dist/. $(ADMINUI_DIST)/
	@echo "staged Admin SPA -> $(ADMINUI_DIST); run 'make build' to embed it"

## stake-hash: print the owner stake-key hash for a wallet's stake address, e.g.
##   make stake-hash ADDR=stake1...   ->   then: make dev DEV_OWNER_KEYS=<hash>
stake-hash:
	@$(GO) run ./cmd/stakehash "$(ADDR)"

## web-clean: drop the staged SPA (back to the placeholder)
web-clean:
	rm -rf $(ADMINUI_DIST)
	mkdir -p $(ADMINUI_DIST)
	touch $(ADMINUI_DIST)/.gitkeep

clean:
	rm -f coverage.out
	$(GO) clean -testcache
