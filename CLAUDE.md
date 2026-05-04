# CanaryGo — Agent Context

## Module
`github.com/growdirect-llc/rapidpos` — monorepo, single go.mod at root.

## This build
Greenfield Go service tree for the Canary Go platform. 19 services (20 binaries with edge).
Active milestone: M1 Foundation. See spec: `docs/superpowers/specs/2026-04-28-canary-go-m1-foundation-design.md`.

## Stack
Go 1.22+ · Chi v5 · pgx/v5 · sqlc v2 · golang-migrate v4 · PostgreSQL 17 · Valkey 8

## Databases
Primary: `canary_gcp` on growdirect_postgres :5432
Test:    `canary_gcp_test` on growdirect_postgres :5432
Valkey:  DB 2 on growdirect_valkey :6379

## Never
- Never import from Canary/ (Python prototype — frozen)
- Never use pgx/v4 — this codebase is pgx/v5 only
- Never write raw SQL strings in service code — all queries go through sqlc
- Never use `canary` or `canary_test` DB names — those belong to the Python prototype
- Never use Valkey DB 0 (Python Canary) or DB 1 (Cove)

## Service ports (go-module-layout.md)
identity :8086 · tsp :8080 · chirp :8081 · hawk :8082 · fox :8083 · owl :8084
bull :8085 · alert :8087 · analytics :8088 · asset :8089 · item :8090
customer :8091 · employee :8092 · returns :8093 · report :8094

## SDDs (read before touching a service)
`docs/sdds/go-handoff/` — data-model.md, go-module-layout.md, microservice-architecture.md, identity.md

## sqlc rule (amended 2026-05-03 — Loop 4 Wave A Phase A.1)
- Read paths: direct pgx + inline SQL is acceptable
- Simple writes (single INSERT/UPDATE/DELETE): direct pgx acceptable
- Complex writes (multi-statement tx, cross-table, ≥3 callers): SHOULD use sqlc
- Input queries: `internal/db/sqlc/<service>.sql`
- Generated output: `internal/db/query/<service>/` — do not hand-edit
- Run: `make sqlc-gen` (requires sqlc binary installed)
- Full convention: `docs/conventions.md`

## Migrations
- Path: `deploy/migrations/` (flat numbered, 001–014 for M1)
- Run: `make migrate-up DATABASE_URL=...`
- Dirty state fix: `migrate -path=deploy/migrations -database=$DATABASE_URL force <version>`
