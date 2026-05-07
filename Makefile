.PHONY: migrate-up migrate-down migrate-test-up sqlc-gen test test-integration \
        build-identity build-all build-edge-windows lint vulncheck \
        check-no-dev-secrets \
        db-reset db-reset-test db-seed db-seed-test \
        dev dev-down dev-logs

# Default DATABASE_URL — override on command line
DATABASE_URL ?= postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp?sslmode=disable
TEST_DATABASE_URL ?= postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable

# Local Docker Postgres connection params (used by db-reset / db-seed)
PG_CONTAINER ?= growdirect_postgres
PG_USER      ?= growdirect

# ─────────────────────────────────────────────────────────────────────
# db-reset — drop + recreate canary_gcp from declarative schema files.
# Greenfield discipline: the schema is the source of truth, not a
# numbered migration history. Edit deploy/schema/*.sql, run db-reset.
# ─────────────────────────────────────────────────────────────────────
db-reset:
	@echo "==> dropping + recreating canary_gcp (LOCAL ONLY)"
	docker exec $(PG_CONTAINER) psql -U $(PG_USER) -d postgres -c "DROP DATABASE IF EXISTS canary_gcp" >/dev/null
	docker exec $(PG_CONTAINER) psql -U $(PG_USER) -d postgres -c "CREATE DATABASE canary_gcp" >/dev/null
	@echo "==> applying deploy/schema/*.sql in order"
	@for f in deploy/schema/*.sql; do \
	    echo "  -- $$f"; \
	    docker exec -i $(PG_CONTAINER) psql -U $(PG_USER) -d canary_gcp -v ON_ERROR_STOP=1 < $$f >/dev/null \
	      || { echo "FAILED: $$f"; exit 1; }; \
	done
	@echo "==> done. tables:"
	@docker exec $(PG_CONTAINER) psql -U $(PG_USER) -d canary_gcp -t -c \
	    "SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1" \
	  | sed 's/^/    /' | grep -v '^    $$' | head -80

db-reset-test:
	@echo "==> dropping + recreating canary_gcp_test (LOCAL ONLY)"
	docker exec $(PG_CONTAINER) psql -U $(PG_USER) -d postgres -c "DROP DATABASE IF EXISTS canary_gcp_test" >/dev/null
	docker exec $(PG_CONTAINER) psql -U $(PG_USER) -d postgres -c "CREATE DATABASE canary_gcp_test" >/dev/null
	@for f in deploy/schema/*.sql; do \
	    docker exec -i $(PG_CONTAINER) psql -U $(PG_USER) -d canary_gcp_test -v ON_ERROR_STOP=1 < $$f >/dev/null \
	      || { echo "FAILED: $$f"; exit 1; }; \
	done
	@echo "==> canary_gcp_test ready"

db-seed:
	@docker exec -i $(PG_CONTAINER) psql -U $(PG_USER) -d canary_gcp -v ON_ERROR_STOP=1 < deploy/schema/99_seed.sql

db-seed-test:
	@docker exec -i $(PG_CONTAINER) psql -U $(PG_USER) -d canary_gcp_test -v ON_ERROR_STOP=1 < deploy/schema/99_seed.sql

migrate-up:
	migrate -path=deploy/migrations \
	        -database="$(DATABASE_URL)" up

migrate-down:
	migrate -path=deploy/migrations \
	        -database="$(DATABASE_URL)" down 1

migrate-test-up:
	migrate -path=deploy/migrations \
	        -database="$(TEST_DATABASE_URL)" up

sqlc-gen:
	sqlc generate

# test — run pure unit tests (no DB / no integration tag).
# Bare `go test ./...` works for this — the integration-shaped tests
# under //go:build integration are skipped without the tag.
test:
	go test ./... -v -count=1

# test-integration — run the full corpus including integration tests.
# Requires Postgres + Valkey reachable per the URLs below.
# GRO-857 / Sprint 2 T-K — integration tests gated on //go:build integration.
test-integration:
	DATABASE_URL="$(TEST_DATABASE_URL)" \
	VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
	SESSION_SECRET="test-session-secret-at-least-32-bytes!" \
	INTERNAL_SERVICE_SECRET=test-internal-secret \
	go test -tags integration ./... -v -count=1

build-identity:
	go build -o bin/identity ./cmd/identity

build-all:
	@for svc in identity tsp gateway chirp alert fox owl analytics hawk bull \
	            asset item inventory receiving transfer pricing employee \
	            customer returns report; do \
	    echo "Building $$svc..."; \
	    go build -o bin/$$svc ./cmd/$$svc || exit 1; \
	done
	@echo "Building edge..."
	go build -o bin/edge ./cmd/edge

build-edge-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/edge.exe ./cmd/edge

lint:
	go vet ./...

# vulncheck — scan dependencies for known CVEs via govulncheck.
# Installs the tool if missing. Runs against the loaded module graph.
# Wire into CI (cloudbuild.gateway.yaml) as a deploy gate before staging.
# GRO-849 / Sprint 2 T-G.
vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	@govulncheck ./...

# check-no-dev-secrets — fail the build if a known dev-secret literal
# leaks into source-controlled files. The seed file ships placeholders
# (e.g. __SEED_HMAC_PLACEHOLDER__) which deploy/scripts/seed-dev-secrets.sh
# substitutes at apply time. This guard catches a regression where a
# real-looking secret is committed back into deploy/. GRO-859 / Sprint 2 T-W.
check-no-dev-secrets:
	@echo "==> checking for dev-secret literals committed to deploy/"
	@if grep -rE "dev-only-do-not-ship-this-secret-[0-9a-f]+" deploy/ >/dev/null 2>&1; then \
	  echo "FAIL: dev-secret literal found in deploy/ — replace with __SEED_HMAC_PLACEHOLDER__"; \
	  grep -rnE "dev-only-do-not-ship-this-secret-[0-9a-f]+" deploy/; \
	  exit 1; \
	fi
	@echo "==> ok"

test-cockroach:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -tags integration ./internal/protocol/cockroach/... -v -timeout 60s

# ─────────────────────────────────────────────────────────────────────
# dev — start shared infra + Canary Go stack in one command.
# Run from CanaryGo/: make dev
# ─────────────────────────────────────────────────────────────────────
dev:
	@echo "==> shared infra"
	docker compose -f ../devops/docker-compose.yml up -d
	@echo "==> canary go stack"
	docker compose -f deploy/docker-compose.yml up -d --build

dev-down:
	docker compose -f deploy/docker-compose.yml down
	docker compose -f ../devops/docker-compose.yml down

dev-logs:
	docker compose -f deploy/docker-compose.yml logs -f canarygo-gateway
