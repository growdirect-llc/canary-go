---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
type: shared-library
package: internal/testutil
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# go-testing — Test Infrastructure and Conventions

**Package:** `internal/testutil`  
**Used by:** All `_test.go` files across the Canary Go module  
**Depends on:** `internal/errors`, `pgxpool`, `go-redis`, `github.com/google/uuid`, `github.com/stretchr/testify` (assertions only)  
**Test database:** `canary_go_test` (separate from `canary_go`; never shared)

The test infrastructure is a first-class engineering asset. A test suite that passes against mocks while the real DB migrations fail is worse than no tests — it produces false confidence. `internal/testutil` establishes the platform's non-negotiable testing contract: integration tests hit a real database, fixtures are deterministic and fully populated, and chain correctness is verified against pre-computed reference vectors.

---

## Business

### The Problem This Solves

Mock-based testing for database-heavy services produces a specific failure mode: the mock passes, the migration breaks, and the bug surfaces in production. This happened in the Python Canary prototype. The fix is structural — the test infrastructure must make it easier to test against real infrastructure than to mock it.

`internal/testutil` makes that easy: one call to spin up migrations and seed data, one call to reset between test cases, deterministic fixtures for all three merchant archetypes. Engineers write real tests against a real schema without boilerplate.

### What Breaks Without It

- Individual service test packages each define their own fixture setup — they drift, producing different test conditions for the same scenario across services
- Tests that pass locally fail on CI because the migration was applied in the wrong order
- Hash chain tests compare computed values to re-computed values using the same code being tested — a bug in the hash function passes both sides of the assertion
- Tests with `time.Sleep` produce flaky pipelines that take 30+ minutes to run on CI
- Non-deterministic test data (`uuid.New()` without a seed) makes test failures non-reproducible

---

## Technical

### Test Layers

| Layer | Build tag | Scope | Speed target | What it tests |
|-------|-----------|-------|-------------|---------------|
| Unit | (none — default) | Single function, no I/O | <1ms/test | Logic, transformation, validation, error wrapping |
| Integration | `//go:build integration` | Real DB (`canary_go_test`), real Valkey | 10–500ms/test | Repository functions, sqlc queries, Valkey cache behaviour |
| Smoke | `//go:build smoke` | Full HTTP stack against a running service instance | 100ms–5s/test | End-to-end API contracts, auth flow, webhook delivery |

Run unit tests: `go test ./...`  
Run integration tests: `go test -tags integration ./...`  
Run smoke tests: `go test -tags smoke ./...` (requires running service stack)

### TestMain Pattern

Every package with integration tests implements `TestMain`. This is not optional — it ensures migrations run before any test in the package, and cleanup runs after, regardless of test outcome.

```go
// In any package's main_test.go
func TestMain(m *testing.M) {
    testutil.MustMigrateTestDB()   // run all migrations on canary_go_test; fatal on error
    testutil.MustSeedBaseData()    // load base fixtures (merchants A, B, C + reference data)
    code := m.Run()
    testutil.MustCleanTestDB()     // truncate all tables in reverse FK order; do not drop
    os.Exit(code)
}
```

`MustMigrateTestDB` applies migrations using the same migration runner as production (`internal/migrate`). It does not create the database — `canary_go_test` must exist before tests run. The CI pipeline creates it; local developers run `make test-db-create` once.

`MustCleanTestDB` truncates, not drops. The schema persists between test runs; only the data is cleared. This makes the next `TestMain` in the same process fast — migrations are idempotent and skip if already applied.

### Fixture Catalog

All fixtures use `@test.growdirect.io` email addresses and `test_` prefix on all external system IDs. No fixture may use real merchant names, real external IDs, or real Square/NCR credentials.

```go
// internal/testutil/seeds.go

// Seeds is the package-level fixture accessor. Call after MustSeedBaseData().
var Seeds = &SeedSet{}

// MerchantA — single-location Square merchant, 25 active SKUs
// Use for: basic namespace resolution, simple receipt chain, Square webhook ingestion
func (s *SeedSet) MerchantA() *MerchantFixture

// MerchantB — 5-location NCR Counterpoint merchant, 120 active SKUs
// Use for: multi-location inventory, NCR event ingestion, cross-location OTB
func (s *SeedSet) MerchantB() *MerchantFixture

// MerchantC — dual-channel Square + Shopify merchant, 60 active SKUs
// Use for: multi-source namespace resolution, BOPIS flow, cross-channel reconciliation
func (s *SeedSet) MerchantC() *MerchantFixture

// TransactionSet generates synthetic transactions for a given merchant.
// count: number of transactions to generate
// scenarioTag: one of "normal_sales", "high_returns", "multi_location_transfer", "shrink_event"
// All generated UUIDs are deterministic from (merchantID, count, scenarioTag) — same inputs, same output.
func (s *SeedSet) TransactionSet(merchantID uuid.UUID, count int, scenarioTag string) []TransactionFixture

// TestDataReset truncates and re-seeds in one call. Use between test cases that modify data.
func TestDataReset()
```

`MerchantFixture` exposes:

```go
type MerchantFixture struct {
    MerchantID   uuid.UUID
    Namespace    string   // "raas:{merchant_id}"
    Email        string   // "{slug}@test.growdirect.io"
    ExternalIDs  map[string]string  // provider → test_ prefixed external ID
    Locations    []LocationFixture
    SKUs         []SKUFixture
}
```

### Table-Driven Tests

Required for all unit tests with more than one input/output case. The pattern is not optional — a test with a long if/else chain of assertions is not reviewable and fails asymmetrically.

```go
func TestResolveNamespace(t *testing.T) {
    cases := []struct {
        name       string
        merchantID uuid.UUID
        wantNS     string
        wantErr    error
    }{
        {
            name:       "known merchant returns namespace",
            merchantID: knownID,
            wantNS:     "raas:" + knownID.String(),
            wantErr:    nil,
        },
        {
            name:       "unknown merchant returns ErrNotFound",
            merchantID: uuid.New(),
            wantNS:     "",
            wantErr:    cerrors.ErrNotFound,
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            ns, err := resolver.ResolveNamespace(ctx, tc.merchantID)
            if tc.wantErr != nil {
                require.Error(t, err)
                assert.True(t, cerrors.Is(err, tc.wantErr.(cerrors.ErrorCode)))
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tc.wantNS, ns)
        })
    }
}
```

### Chain Test Vectors

Hash chain correctness tests must use pre-computed reference vectors. Never test a hash output against a value computed by the same Go code being tested — that is testing that the code is self-consistent, not that it is correct.

```go
// internal/testutil/chain_test_vectors.go

// ChainVector is a reference event with a pre-computed hash, derived from
// Python hashlib.sha256 or openssl dgst. The Go implementation must produce
// the same hash from the same inputs — if it does not, the chain is broken.
type ChainVector struct {
    SequenceNum    int64
    MerchantID     uuid.UUID
    EventPayload   []byte
    PriorHash      string
    ExpectedHash   string  // computed externally; never by the code under test
}

// StandardVectors returns the canonical set of 20 test vectors for raas chain tests.
// These vectors are frozen — do not add to them without a corresponding update to
// the Python reference script at tools/chain_reference/generate_vectors.py.
func StandardVectors() []ChainVector
```

Vector generation script: `tools/chain_reference/generate_vectors.py` — outputs JSON that is manually converted to the Go constant table. This script is the reference implementation. If the Go implementation disagrees with this script, the Go implementation is wrong.

### Deterministic Random

Any test that generates data must use a seeded source:

```go
rng := rand.New(rand.NewSource(42))
```

Never use `rand.Intn` directly (uses the global source, which may not be seeded). Never use `rand.New(rand.NewSource(time.Now().UnixNano()))` — non-reproducible failures are the primary cost of CI flakiness.

### Rules

1. **No mocking the database.** Integration tests hit `canary_go_test`. The cost of a real DB round-trip (10–50ms) is the price of knowing the test is actually correct. A mocked DB test that passes is not evidence of correctness — it is evidence that the mock matches your assumptions, which may be wrong.

2. **All test data uses `@test.growdirect.io` and `test_` external ID prefixes.** This makes test data trivially identifiable in shared infrastructure and prevents accidental pollution of the production DB if a test somehow runs against the wrong connection string.

3. **Deterministic random.** `rand.New(rand.NewSource(42))` for any generated test data. Non-deterministic tests are not reproducible; non-reproducible failures cannot be debugged.

4. **No `time.Sleep` in tests.** Sleeping for "enough time" to wait for an async operation is a race condition with a timeout. Use channel signals for goroutine synchronization and `context.WithTimeout` for operations that have a real deadline. If the operation has no synchronization mechanism, that is a design gap — add one.

5. **Test the error path, not just the happy path.** Every function that returns an error must have at least one test case that exercises a non-nil error return. Error path coverage is not optional — the chain violation and SLA breach paths in particular must have test coverage because they are the platform's evidentiary guarantees.

### Trade-off: testify vs stdlib testing

`testify/require` and `testify/assert` add one external dependency for a significant readability improvement: `assert.Equal(t, expected, actual)` produces a diff on failure; `if expected != actual { t.Errorf(...) }` requires the engineer to construct the failure message manually. The cost is one pinned dependency. The benefit is test output that is useful without a debugger.

`testify/mock` is explicitly excluded. It enables the DB-mocking anti-pattern. Only `testify/require` and `testify/assert` are approved imports from this package.

---

## Ops

### Local Setup

```bash
# Create the test database (run once)
make test-db-create

# Run unit tests
go test ./...

# Run integration tests (requires shared infra Docker stack up)
go test -tags integration ./...

# Run a specific package's integration tests with verbose output
go test -tags integration -v ./internal/raas/...
```

### CI Pipeline

The CI pipeline runs in this order:
1. `go vet ./...` — static analysis
2. `go test ./...` — unit tests only (no Docker required)
3. Start Docker Compose test stack (postgres on `canary_go_test`, Valkey)
4. `go test -tags integration ./...` — integration tests
5. Smoke tests run separately against a deployed staging service — not in unit CI

### Failure Modes

| Failure | Cause | Fix |
|---------|-------|-----|
| `MustMigrateTestDB` fatal | `canary_go_test` does not exist | Run `make test-db-create` |
| Integration test panic on nil pool | Shared infra Docker stack not running | `cd devops && docker compose up -d` |
| Chain vector mismatch | Go hash implementation diverges from Python reference | Diff `tools/chain_reference/generate_vectors.py` against Go constants |
| Flaky test in CI | `time.Sleep` or non-deterministic random | Find the sleep, replace with channel sync; find the random, seed it |

---

## Compliance

### Test Data Classification

All fixture data is synthetic. `MerchantA`, `MerchantB`, and `MerchantC` represent no real business. External IDs are `test_`-prefixed and will be rejected by any real POS system's validation. `@test.growdirect.io` email addresses are not associated with real individuals.

Synthetic test data must never be promoted to the production database. The test DB connection string must not be the same as the production connection string — `MustMigrateTestDB` validates that the target database name ends in `_test` and fatals if it does not. This is a deliberate safety check, not a convenience.

### Chain Vector Custody

`StandardVectors()` is a frozen constant set derived from a Python reference implementation. Adding a vector requires running the Python script, reviewing the output independently of the Go implementation, and committing both the script run output and the Go constant update in the same commit. Vectors must not be derived by running the Go implementation and copying its output — that is testing that code is self-consistent, not correct.

---

## Related

- [[go-runtime]] — services under test import `internal/runtime`; `MustConnectDB` and `MustConnectValkey` are the connection points integration tests assert against
- [[go-module-layout]] — `internal/testutil/` package location; CI pipeline structure
- [[go-security]] — `TEST_ENCRYPTION_KEY`, `TEST_JWT_SECRET`, `TEST_PHONE_HASH_KEY`, `TEST_EMAIL_HASH_KEY` are the test-only key constants integration tests use
- [[go-observability]] — log assertions in tests use the standard field names defined here
- [[go-errors]] — error-path coverage requirement consumes the error model
- [[retail-lifecycle-test-data]] — the lifecycle test methodology that this test infrastructure supports
- [[platform-overview]] — top-level testing posture and quality bar
