---
spec-version: 1.1
updated: 2026-04-29
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | Valkey queue | Ollama qwen3-embedding:8b | pandoc + pdftotext
source: content-engine/engine.py (Python prototype) + platform intake protocol (CLAUDE.md)
status: handoff-ready
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Document Factory Pipeline

**Service Type:** Background Service + CLI Tool
**Last Code Review:** N/A — new Go implementation; Python reference at `content-engine/engine.py`
**Related:** `data-model.md` (full schema), `Brain/wiki/agent-card-format.md` (card format), `docs/sdds/platform/factory-pipeline.md`

---

## Purpose

The Document Factory Pipeline is the knowledge ingestion layer for the Canary platform. It accepts working papers, vendor documents, operational reports, and correspondence in their native binary formats and transforms them into structured wiki knowledge cards and pgvector embeddings that agents can retrieve via the memory bus.

**Governing principle:** Raw documents enter at the intake stage and exit as agent-queryable memory. Every stage is tracked, every document has a processing state, and the human-in-the-loop gate sits exactly where it belongs — between draft synthesis and committed wiki content.

The pipeline is not a document management system. It does not index or search raw files. Its job is permanent knowledge extraction: pull the signal, discard the container, embed the result.

**Tenant scope: platform-level, not per-merchant.** The factory pipeline is platform-internal infrastructure — it processes the GrowDirect knowledge corpus (Brain wiki, SDDs, dispatches), not merchant data. The `factory.documents` table lives in the `public` schema (or a dedicated platform schema), not in tenant schemas. Memory bus embeddings produced from this pipeline are queryable by all platform-internal services and agents; they are not exposed in tenant-scoped MCP surfaces.

---

## Dependencies

| Dependency | Type | Required | Notes |
|------------|------|----------|-------|
| PostgreSQL (`canary_go` DB, `factory` schema) | Database | Yes | Pipeline state, document registry |
| Valkey (DB 0) | Queue | Yes | Processing queue for background stages |
| Ollama (`qwen3-embedding:8b`) | Embedding | Yes | pgvector seed (stage 5); must use growdirect_ollama container |
| pgvector (`growdirect_memory` DB) | Vector store | Yes | Embedding target; memory bus recall surface |
| pandoc | CLI tool | Yes | Binary → markdown extraction (DOCX, PPTX, RTF) |
| pdftotext | CLI tool | Yes | Native PDF → text extraction |
| Brain/raw/inbox/ | Filesystem | Yes | Scratch output for extracted markdown |
| Brain/wiki/ | Filesystem | Yes | Committed wiki card output (post-synthesis) |
| content-engine/engine.py | Reference | No | Python prototype for behavioral reference only |

---

## Data Flow & Document Map

### What Enters

| Source | Format | Entry Point |
|--------|--------|-------------|
| CLI (`factory ingest <path>`) | Binary (DOCX, PDF, PPTX, XLSX, XLS, RTF) or markdown | `cmd/factory` |
| HTTP upload endpoint | Multipart form | `POST /factory/upload` |
| Directory scan | Mixed binary + markdown | `factory ingest <dir>` |

### What Is Stored

| Table | Purpose | Notes |
|-------|---------|-------|
| `factory.documents` | Per-document pipeline state and metadata | UUID PK, slug unique index |
| `factory.processing_errors` | Stage-level error log | FK to documents; append-only |
| `Brain/raw/inbox/{slug}.md` | Extracted markdown scratch | Source path in frontmatter; not a wiki card |
| `Brain/wiki/...` | Committed wiki card | Written only after synthesis approval |
| `growdirect_memory` pgvector | 1024-dim embeddings | Seeded from wiki card content only |

### What Exits

| Destination | Data | Trigger |
|-------------|------|---------|
| Brain/raw/inbox/ | Extracted markdown with frontmatter | Stage 1–2 completion |
| Brain/wiki/ | Approved wiki card | Stage 4 (synthesis) approval |
| growdirect_memory pgvector | 1024-dim vector + metadata | Stage 5 (seed) |
| `factory status` CLI | Pipeline state per document | On-demand |
| `GET /factory/documents` | Document list with status | HTTP API |

---

## API Contract

### CLI Interface (`cmd/factory`)

| Command | Arguments | Description |
|---------|-----------|-------------|
| `factory ingest <path>` | File or directory path | Intake a single file or batch directory |
| `factory classify <slug>` | Document slug | Run classification on an ingested document |
| `factory synthesize <slug>` | Document slug | Interactive agent synthesis pass |
| `factory seed` | — | Incremental seed (new/modified wiki cards only) |
| `factory seed --drop-first` | — | Full reseed (truncates vector store first) |
| `factory status [<slug>]` | Optional slug | Show pipeline state for one or all documents |
| `factory list --status=<status>` | Status filter | List documents by pipeline stage |
| `factory registry build` | — | Rebuild the document registry index |

### HTTP Endpoints

| Path | Method | Auth | Description |
|------|--------|------|-------------|
| `/factory/upload` | POST | API key | Upload a binary file; enqueues for intake |
| `/factory/documents` | GET | API key | List documents; filterable by status |
| `/factory/documents/{slug}` | GET | API key | Get document detail and pipeline state |
| `/factory/documents/{slug}/classify` | POST | API key | Trigger classification stage |
| `/factory/documents/{slug}/seed` | POST | API key | Trigger seed stage for a seeded wiki card |
| `/factory/health` | GET | Public | `{"service": "canary-factory", "healthy": true}` |

Synthesis (`/factory/documents/{slug}/synthesize`) is intentionally not an HTTP endpoint — the agent synthesis pass requires interactive review and runs via CLI only.

---

## Data Model

All factory tables live in the `factory` schema within the `canary_go` database.

### factory.documents

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | Canary-internal document ID |
| `slug` | TEXT | NOT NULL, UNIQUE | URL-safe identifier derived from filename |
| `source_path` | TEXT | NOT NULL | Original file path; preserved in intake frontmatter |
| `file_type` | TEXT | NOT NULL, CHECK IN ('docx','pdf','pptx','xlsx','xls','rtf','md') | Binary format type |
| `intake_path` | TEXT | NOT NULL | `Brain/raw/inbox/{slug}.md` — extracted markdown location |
| `wiki_path` | TEXT | NULLABLE | `Brain/wiki/...` — set after synthesis approval |
| `status` | TEXT | NOT NULL, DEFAULT 'ingested' | See pipeline state machine below |
| `classification` | JSONB | NULLABLE | `{doc_type, client_refs[], pii_present, retention_class, word_count}` |
| `document_date` | TEXT | NULLABLE | Document date (from content or filename); ISO 8601 |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_factory_documents_slug ON (slug)`
- `idx_factory_documents_status ON (status)`
- `idx_factory_documents_file_type ON (file_type)`

### factory.processing_errors

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `document_id` | UUID | NOT NULL, FK → factory.documents.id | |
| `stage` | TEXT | NOT NULL, CHECK IN ('intake','extract','classify','synthesize','seed') | Which pipeline stage failed |
| `error` | TEXT | NOT NULL | Error message and stack trace |
| `occurred_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

### Pipeline State Machine

```
ingested → classified → pending_synthesis → synthesized → seeded
                                                         ↓
                                                       failed (any stage)
```

| Status | Meaning | Next Step |
|--------|---------|-----------|
| `ingested` | Binary extracted to markdown scratch; source path preserved | Run classify |
| `classified` | doc_type, client_refs, PII flag, retention class set | Await synthesis review |
| `pending_synthesis` | Agent drafted wiki card; awaiting founder approval | Approve → synthesized |
| `synthesized` | Wiki card committed to Brain/wiki/; wiki_path set | Run seed |
| `seeded` | pgvector embedding written to growdirect_memory | Terminal — success |
| `failed` | Error in any stage; see processing_errors | Manual remediation |

---

## Pipeline Stages

### Stage 1: Intake

Accepts a binary file or directory. For each file:
1. Derive `slug` from filename — lowercase, non-alphanumeric → hyphens, deduped.
2. Write `factory.documents` row with `status = 'ingested'`.
3. Write frontmatter to `Brain/raw/inbox/{slug}.md`:
   ```yaml
   ---
   source_path: <original absolute path>
   file_type: <docx|pdf|...>
   intake_date: <ISO 8601>
   factory_id: <UUID>
   status: ingested
   ---
   ```
4. Enqueue for Stage 2 (extract) via Valkey queue `factory:extract`.

Markdown files skip extraction and move directly to `classified` queue.

### Stage 2: Extract

Per-format extraction strategies:

| Format | Tool | Strategy | Notes |
|--------|------|----------|-------|
| DOCX | pandoc | `pandoc -f docx -t markdown` | Preserves headings, tables |
| PPTX | pandoc | `pandoc -f pptx -t markdown` | Slide titles become H2; speaker notes appended |
| RTF | pandoc | `pandoc -f rtf -t markdown` | |
| XLSX / XLS | ssconvert → pandoc | Sheet → CSV → markdown table | Install gnumeric for ssconvert |
| PDF (text) | pdftotext | `pdftotext -layout` | Preserves column layout |
| PDF (scanned) | pdftotext fallback + OCR flag | Flag `pii_present=true`, `doc_type=scanned_image` if text < 50 chars/page | OCR is a P1 enhancement |
| MD | passthrough | Already markdown; copy to intake_path | No extraction needed |

On extraction failure: write error to `factory.processing_errors`, set `status = 'failed'`. Do not silently truncate.

### Stage 3: Classify

Reads extracted markdown. Outputs structured `classification` JSONB:

```json
{
  "doc_type": "retail_planning",
  "client_refs": ["Merchant A", "Store 4"],
  "pii_present": false,
  "retention_class": "7_years",
  "word_count": 4820
}
```

Classification uses keyword pattern matching against document content and filename. The classifier is rule-based (not LLM) — fast, deterministic, auditable. LLM-assisted classification is a P2 enhancement.

**Document type classification table:**

| Doc Type | File Types | Scrub Rule | Retention | Notes |
|----------|-----------|------------|-----------|-------|
| `retail_planning` | XLSX, DOCX | Named clients → deployment archetypes at synthesis | 7 years | OTB plans, assortment, buy plans, promotional calendars |
| `lp_exception` | XLSX, PDF | Named clients + employees → roles at synthesis | 7 years | Exception reports, shrink analysis, inventory adjustments |
| `vendor_correspondence` | DOCX, PDF | Vendor names preserved | Contract term + 7 years | RFPs, term sheets, price lists, invoices, onboarding packets |
| `financial` | XLSX, PDF | Named clients → archetypes at synthesis | 7 years + SOX | P&L, budget variance, cost sheets |
| `operations_sop` | DOCX, MD | None | Current version only | Runbooks, store audit forms, receiving logs |
| `legal_compliance` | PDF, DOCX | Preserve all proper nouns | Contract term + 7 years | Contracts, audit findings, regulatory correspondence |
| `market_research` | PDF, DOCX, PPTX | Preserve analyst/vendor names | 3 years | Competitive analysis, industry benchmarks, customer research |
| `scanned_image` | PDF | Flag for manual review | TBD | Scanned documents with insufficient text extraction |

**Governing scrub rule:** Named clients in synthesis layers (wiki cards, briefs, playbooks) are always abstracted to deployment archetypes. Raw intakes in `Brain/raw/inbox/` stay intact. This is a platform-level requirement; the classifier does not scrub — it flags. Scrubbing is a synthesis-stage responsibility.

### Stage 4: Synthesize (Human-in-the-Loop Gate)

The only interactive stage. Runs via `factory synthesize <slug>`. Stages 1–3 and 5 are fully automated; stage 4 requires agent + founder review before any wiki write.

Synthesis pass:
1. Agent reads `Brain/raw/inbox/{slug}.md` and `classification` JSONB.
2. Determines wiki placement:
   - Topic wiki article → `Brain/wiki/<topic>.md`
   - Knowledge card → `Brain/wiki/cards/<slug>.md`
3. Drafts wiki content applying the agent-card-format template (`Brain/wiki/agent-card-format.md`).
4. Applies client name scrubbing per `classification.client_refs` — named merchants become deployment archetypes.
5. Proposes tags and backlinks.
6. Presents draft for founder approval (diff view in terminal).
7. On approval: writes wiki file, sets `wiki_path`, updates `status = 'synthesized'`.
8. On rejection: logs feedback, returns to `classified` status; document remains in inbox.

### Stage 5: Seed

Incremental by default. Triggered by `factory seed` or `POST /factory/documents/{slug}/seed`.

Seed logic:
1. Find all `synthesized` documents whose `wiki_path` mtime postdates last seed timestamp.
2. Read wiki card markdown.
3. Call Ollama `qwen3-embedding:8b` via `growdirect_ollama` container (never host Ollama).
4. Write 1024-dim vector + metadata to `growdirect_memory` pgvector table.
5. Update `status = 'seeded'`, record seed timestamp.

`factory seed --drop-first` truncates the vector store before reseeding. Use for full corpus rebuilds after bulk wiki edits.

---

## Workflows

### Single-Document Intake

```
factory ingest /path/to/file.pdf
  → status: ingested
  → Brain/raw/inbox/file-pdf.md (frontmatter only)

factory classify file-pdf
  → status: classified
  → classification JSONB populated

factory synthesize file-pdf
  → [interactive: agent drafts, founder approves]
  → status: synthesized
  → Brain/wiki/cards/file-pdf.md written

factory seed
  → status: seeded
  → growdirect_memory embedding written
```

### Batch Directory Intake

```
factory ingest /path/to/working-papers/
  → N documents ingested
  → factory list --status=ingested  (verify)
  → run classify, synthesize, seed per document
```

### Post-Commit Auto-Seed

A post-commit hook at `.git/hooks/post-commit` triggers incremental seed on Brain/wiki/ changes. This keeps the memory bus current without manual `factory seed` calls after wiki edits. Reinstall if repo is re-cloned.

---

## Operations

### Startup Sequence

1. PostgreSQL `canary_go` available and `factory` schema current
2. Valkey available — processing queue operational
3. `growdirect_ollama` container reachable at `http://growdirect_ollama:11434`
4. `Brain/raw/inbox/` and `Brain/wiki/` directories writable
5. pandoc and pdftotext binaries present in PATH
6. Factory routes registered on Chi router

### Failure Modes

| Failure | Impact | Behavior |
|---------|--------|----------|
| pandoc not found | Extract stage fails | Set `status = 'failed'`; log missing binary |
| Ollama unreachable | Seed stage fails | Set `status = 'failed'`; retry on next `factory seed` run |
| Wiki path conflict | Synthesis write fails | Prompt for alternate path; do not overwrite existing wiki content |
| Valkey down | Queue unavailable | CLI fallback: process synchronously without queue |
| PostgreSQL down | All pipeline state writes fail | CLI exits with error; no partial state written |

### Monitoring

| Metric | Alert Threshold | Notes |
|--------|----------------|-------|
| Documents stuck in `classified` | >10 documents, >7 days | Synthesis backlog |
| Seed failures | Any | Ollama connectivity or model issue |
| Extract failures | >3/day | Binary format or tool issue |
| `factory seed` last run | >48 hours | Memory bus drift risk |

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `CANARY_ENV` | Yes | `development` / `production` / `testing` |
| `DATABASE_URL` | Yes | `canary_go` database connection string |
| `VALKEY_URL` | Yes | Valkey connection string (default: `redis://growdirect_valkey:6379/0`) |
| `MEMORY_DATABASE_URL` | Yes | `growdirect_memory` pgvector database |
| `OLLAMA_URL` | Yes | `http://growdirect_ollama:11434` — never use host Ollama |
| `BRAIN_INBOX_PATH` | Yes | Absolute path to `Brain/raw/inbox/` |
| `BRAIN_WIKI_PATH` | Yes | Absolute path to `Brain/wiki/` |
| `FACTORY_API_KEY` | Yes (HTTP) | API key for `/factory/*` endpoints |

### Production Infrastructure Target

| Component | Service | Notes |
|-----------|---------|-------|
| CLI | ECS task (one-off) | `factory ingest`, `factory seed` run as task definitions |
| Background service | ECS/Fargate (long-running) | Valkey queue consumer for stages 1–3 and 5 |
| PostgreSQL | RDS PostgreSQL 17 | `canary_go` database, `factory` schema |
| Valkey | ElastiCache | Queue + processing state cache |
| Brain filesystem | EFS mount | Shared across CLI and service containers |
| Ollama | Separate ECS task or self-hosted GPU | `qwen3-embedding:8b` must be reachable |

---

## Compliance

| Requirement | Enforcement |
|-------------|-------------|
| Client name scrubbing | Synthesis stage only; raw intakes never modified |
| PII detection | `classification.pii_present` flag set at classify stage; flagged documents excluded from bulk seed jobs pending review |
| Retention class | `retention_class = '7_years'` documents excluded from all deletion jobs |
| Seeded embeddings | Metadata and wiki text only — binary originals are never processed into the vector store |
| Test data isolation | `canary_go_test` database only; no production documents in test seeds |

---

## Open Items

### P1 — Before GA

**P1-1: OCR for scanned PDFs not implemented**

Documents classified as `scanned_image` cannot be extracted by pdftotext. Implement Tesseract-based OCR fallback. Flag extracted text as OCR-quality in frontmatter for synthesis context.

**P1-2: XLSX multi-sheet handling**

Current strategy processes only the first sheet. Multi-sheet workbooks (OTB plans, budget files) need sheet-level extraction with sheet names as H2 headings.

**P1-3: No retry budget for Valkey queue failures**

Processing errors should carry a retry counter. After 3 failures, move to a dead-letter queue and alert. Currently, failed documents require manual re-trigger.

### P2 — Post-Launch

**P2-1: LLM-assisted classification**

Rule-based classifier will misclassify edge cases. A secondary LLM pass (qwen3 via Ollama) can validate and correct classifications for ambiguous documents. Adds latency; acceptable at P2.

**P2-2: Audit trail for wiki writes**

Synthesis approvals should be logged (who approved, timestamp, diff hash). Currently no evidentiary record of wiki card provenance.

---

## Production Readiness Checklist

- [ ] pandoc and pdftotext present in production container image
- [ ] Ollama `qwen3-embedding:8b` reachable from production service
- [ ] `factory` schema migration applied to `canary_go` database
- [ ] Brain filesystem (EFS or equivalent) mounted and writable
- [ ] Post-commit hook installed for incremental seed
- [ ] Valkey queue operational; dead-letter queue configured (P1-3)
- [ ] OCR fallback for scanned PDFs (P1-1)
- [ ] Multi-sheet XLSX handling (P1-2)
- [ ] Health check at `/factory/health`
- [ ] `FACTORY_API_KEY` in Secrets Manager (not environment file)

---

## Related

- [[go-runtime]] — service lifecycle, middleware stack, health endpoints
- [[go-module-layout]] — `cmd/factory` binary location
- [[go-security]] — `FACTORY_API_KEY` handling, secret loading
- [[go-observability]] — pipeline stage metrics
- [[data-model.md]] — `factory.documents` schema
- [[platform-overview.md]] — top-level product context
- `Brain/wiki/cards/runbook-memory-bus-seed.md` — the seeder this pipeline triggers
- `Brain/wiki/cards/runbook-brain-wiki-commit.md` — the commit workflow that completes the pipeline
- `content-engine/engine.py` — Python prototype reference
