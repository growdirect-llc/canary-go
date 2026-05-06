# Canary Protocol — OpenAPI 3.0 Surface

Auto-generated from the canonical retail data model. **Do not hand-edit `openapi.yaml`.** Edit the source SDD and regenerate.

This is the **developer-facing** layer of the Canary Protocol substrate.
The agent-facing surface is the MCP connector (see `../mcp/`, `GRO-741`).
The patent-architecture layer (Six-Node pipeline + L402 + `.jeffe` namespace)
lives in dedicated dispatches (`GRO-746` through `GRO-754`); its endpoints are
*reserved* in this OpenAPI spec under the `Protocol-Patent` tag and built in
those dispatches.

Patent reference: Application 63/991,596 — *Universal Event Notarization,
Six-Node Architecture* (filed 2026-02-26). This OpenAPI surface is parallel to
the patent claims, not part of them.

---

## Pipeline

```
docs/sdds/go-handoff/canonical-data-model.md   ──┐
                                                  ├──►  gen/generate.py  ──►  openapi.yaml
docs/sdds/go-handoff/mcp-service-junctions.md   ──┘
```

The generator reads the canonical SDD, parses every `## <schema>.<entity>` (or
`### <schema>.<entity>`) heading + its `CREATE TABLE` block, maps SQL types to
OpenAPI types, and emits CRUD + list endpoints for every canonical entity. Two
"preserve as-is" entities (`app.users`, `app.external_identities`) that the SDD
declines to re-define are synthesized in `gen/generate.py`'s `SUPPLEMENTAL_DDL`
constant from the live Canary models and parsed through the same pipeline.

## Regenerate

From the repo root:

```bash
# One-time: set up the generator's tooling environment
cd services/canary-protocol/openapi/gen
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt

# Every time the SDD changes
cd ../../../..  # repo root
services/canary-protocol/openapi/gen/.venv/bin/python services/canary-protocol/openapi/gen/generate.py
```

The generator prints a coverage summary (entities by schema, paths, schemas
emitted) and writes `openapi.yaml`.

## Validation

The generator's environment includes [`openapi-spec-validator`](https://pypi.org/project/openapi-spec-validator/).
A one-liner verification:

```bash
services/canary-protocol/openapi/gen/.venv/bin/python -c "
from openapi_spec_validator import validate_spec
import yaml
with open('services/canary-protocol/openapi/openapi.yaml') as f:
    validate_spec(yaml.safe_load(f))
print('OK')"
```

For deeper linting, [Spectral](https://stoplight.io/open-source/spectral) is the
recommended tool — install via `npm install -g @stoplight/spectral-cli` and run
`spectral lint services/canary-protocol/openapi/openapi.yaml`. (Not part of the
generator's Python environment; it's a separate Node tool.)

## What the generator emits

| For each canonical entity | Schema name | Purpose |
|---|---|---|
| Full schema | `<schema>_<entity>` | Read response shape |
| Create payload | `<schema>_<entity>_Create` | POST body (omits `id`, `created_at`, `updated_at`) |
| Update payload | `<schema>_<entity>_Update` | PATCH body (all fields optional, omits immutable fields) |
| Paginated list | `<schema>_<entity>_Page` | GET list response with `items[]` + `next_cursor` |

| For each canonical entity | Path | Operation |
|---|---|---|
| Collection list | `GET /v1/<schema>/<entity>` | Paginated list with cursor |
| Collection create | `POST /v1/<schema>/<entity>` | Create new |
| Item read | `GET /v1/<schema>/<entity>/{id}` | Get by UUID |
| Item update | `PATCH /v1/<schema>/<entity>/{id}` | Partial update |
| Item delete | `DELETE /v1/<schema>/<entity>/{id}` | Delete (or soft-delete) |

Plus the **patent-reserved endpoints** under the `Protocol-Patent` tag:

| Path | Built in |
|---|---|
| `POST /v1/protocol/webhook/{source}` | `GRO-746` (API Gateway / Node 2) |
| `POST /v1/protocol/namespace` | `GRO-751` (`.jeffe` namespace) |
| `GET /v1/protocol/evidence/{event_hash}` | `GRO-748` (Sub 1 / L1 Evidence Store) |
| `GET /v1/protocol/anchor/{event_hash}` | `GRO-750` (Sub 3 / Bitcoin anchor) |
| `GET /v1/protocol/verify/{event_hash}` | `GRO-752` (L402 sat-gated verification) |

## Type mapping

| SQL type | OpenAPI type | Notes |
|---|---|---|
| `uuid` | `string` (format `uuid`) | |
| `text` | `string` | No length limit |
| `varchar(N)` | `string` (maxLength `N`) | |
| `int` / `integer` | `integer` (format `int32`) | |
| `bigint` | `integer` (format `int64`) | |
| `numeric(M,N)` | `string` (format `decimal`) | String-encoded for precision; clients should not coerce to float |
| `boolean` | `boolean` | |
| `timestamptz` | `string` (format `date-time`) | RFC 3339 |
| `date` | `string` (format `date`) | |
| `jsonb` | `object` (additionalProperties true) | |
| `ltree` | `string` | Postgres ltree path |
| `bytea` | `string` (format `byte`) | Base64 |

Unknown types fall through to `string` with a description noting the unmapped
SQL type — surfaces gaps in the type-map without breaking the build.

## Coverage

As of the most recent regeneration:

- **65 canonical entities** parsed (50 from §3-§9 business domains + 15 from §10
  platform mechanics, including the supplemental `app.users` and
  `app.external_identities`).
- **991 fields** total across those entities (~15.2 average — fits the SDD's
  10-30 column target).
- **261 component schemas** emitted (4 per entity + the `Error` schema).
- **135 paths** emitted (130 entity CRUD + 5 patent-reserved).
- **14 tags** grouping by domain (one per schema + `Protocol-Patent`).

## Authentication

The spec declares two security schemes corresponding to the patent architecture:

- **`LNURLAuth`** — wallet-derived passwordless identity. `Authorization: LNURL <linkingKey>:<signature>`. Built in `GRO-753`.
- **`L402SatGated`** — HTTP 402 → Lightning invoice → `Authorization: L402 <macaroon>:<preimage>`. Built in `GRO-752`. Required only on `Protocol-Patent` verify endpoints.

The default top-level `security` is `LNURLAuth`; per-operation `security` overrides apply to the patent-reserved endpoints.

## Adding a new entity

1. Add a `## <schema>.<entity>` (or `### <schema>.<entity>` inside §10) section to `docs/sdds/go-handoff/canonical-data-model.md`, including the `CREATE TABLE` block.
2. Re-run the generator (one-line command above).
3. Verify with `validate_spec(...)` and `spot-check`.
4. Reseed the memory bus: `python3 services/memory-bus/scripts/seed_standalone.py`.

If the entity is "preserve as-is" (no fresh DDL in the SDD), add a synthetic
`CREATE TABLE` block to `SUPPLEMENTAL_DDL` in `gen/generate.py`.

## Limitations / known gaps

- **`metrics` schema** is mentioned in the SDD's design principles (§2.8) but
  not yet given entity definitions; emit will pick those up automatically when
  they're added.
- **`memory` schema** is preserved-as-is without DDL; add to `SUPPLEMENTAL_DDL`
  if/when memory-bus tables need to be in the public OpenAPI surface.
- **Junction archetypes** (`mcp-service-junctions.md`, 166 junctions across 10
  archetypes) are not yet emitted as separate operation paths. The current spec
  emits only canonical CRUD + the 5 patent-reserved endpoints. Junction-derived
  operations are deferred to a follow-up dispatch.
- **CHECK constraints** in the DDL are not represented in the OpenAPI schema
  (e.g., `entity_type IN ('employee', 'location', ...)` doesn't become an
  `enum`). Could be added by enriching the parser.
- **DEFERRABLE / INITIALLY DEFERRED** constraint flags, partial indexes,
  `EXCLUDE` constraints, and trigram (`gin_trgm_ops`) indexes are not surfaced
  in the OpenAPI spec — they're operational concerns of the underlying DB, not
  the contract surface.

## Files in this folder

| Path | Purpose |
|---|---|
| `openapi.yaml` | The generated spec (do not hand-edit) |
| `README.md` | This file |
| `gen/generate.py` | The generator script |
| `gen/requirements.txt` | Python deps for the generator (`PyYAML`, `openapi-spec-validator`) |
| `gen/.gitignore` | Ignores `.venv/` and `__pycache__/` |
| `gen/.venv/` | Local Python tooling environment (not committed) |

---

Patent context: Application 63/991,596 (Universal Event Notarization).
This dispatch (`GRO-740`) is the developer-facing surface — parallel to the
patent architecture, not part of the patent claims themselves.
