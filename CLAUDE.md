# CanaryGo — Agent Context

## Module
`github.com/ruptiv/canary` — monorepo, single go.mod at root.

## This build
Greenfield Go service tree for the Canary Go platform. 19 services (20 binaries with edge).
Active milestone: M1 hardening and Phase 5 readiness.

Read these first:
- `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md` — active program map.
- `docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md` — active Phase 9 hardening dispatch.
- `docs/architecture/canary-go-vision-fit-matrix.md` — fit between the Go work, Ruptiv vision, AtlasView, retail capabilities, and agent execution.
- `docs/architecture/component-led-ui-vision.md` — Canary/AtlasView UI alignment.

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

## Architecture docs (read before touching a service)
- `docs/architecture/`
- `docs/decisions/`
- `docs/conventions.md`
- `docs/conventions/`
- `docs/superpowers/specs/`
- `docs/superpowers/plans/`

Historical comments may still mention `docs/sdds/go-handoff/`; that directory is not present in this checkout. Treat those references as provenance unless the active docs above supersede them.

## sqlc rule (amended 2026-05-03 — Loop 4 Wave A Phase A.1)
- Read paths: direct pgx + inline SQL is acceptable
- Simple writes (single INSERT/UPDATE/DELETE): direct pgx acceptable
- Complex writes (multi-statement tx, cross-table, ≥3 callers): SHOULD use sqlc
- Input queries: `internal/db/sqlc/<service>.sql`
- Generated output: `internal/db/query/<service>/` — do not hand-edit
- Run: `make sqlc-gen` (requires sqlc binary installed)
- Full convention: `docs/conventions.md`

## Migrations — two-tier model
- **`deploy/schema/`** — declarative full schema. The canonical source-of-truth for what the database *should* look like at HEAD. Use `make db-reset` (LOCAL ONLY) to drop + recreate from `deploy/schema/*.sql` in order. Edit these files when changing the schema for greenfield discipline.
- **`deploy/migrations/`** — incremental forward migrations (golang-migrate format, `NNN_<slug>.{up,down}.sql`). Numbering is flat: 001–018 covered M1 foundation, 019+ are post-M1 additions. Each migration has a `.up.sql` + `.down.sql` pair. Use `make migrate-up DATABASE_URL=...` against deployed databases (CI / staging / production) where `db-reset` would lose data.
- **Both must agree at HEAD.** A new feature ships an incremental migration AND updates the corresponding declarative schema file so a fresh `db-reset` produces the same result as `migrate-up` on an existing database.
- Dirty state fix: `migrate -path=deploy/migrations -database=$DATABASE_URL force <version>`
