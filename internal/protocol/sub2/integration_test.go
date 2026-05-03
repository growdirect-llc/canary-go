//go:build integration

// Integration test for Sub 2 (Parse & Route) — patent Application
// 63/991,596 Node 4. Wires the real stack: Postgres + Valkey Streams +
// the worker + the adapter registry with both Square and Counterpoint
// reference parsers. The substrate proof is a single t.transactions
// row populated from a Square envelope AND a single t.transactions
// row populated from a Counterpoint envelope, both routed through the
// same dispatcher.
//
// Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/protocol/sub2/...
//
// The Wave 3 coordinator runs this post-merge to avoid races with the
// other 6 subagents.
//
// Lives in sub2_test (external test package) — necessary because it
// imports the adapters packages which themselves import sub2, which
// would otherwise form a test-time import cycle.
package sub2_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/adapters"
	"github.com/growdirect-llc/rapidpos/internal/adapters/counterpoint"
	"github.com/growdirect-llc/rapidpos/internal/adapters/square"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub2"
)

func skipIfNoIntegration(t *testing.T) (dbURL, valkeyURL string) {
	t.Helper()
	dbURL = os.Getenv("GATEWAY_TEST_DATABASE_URL")
	valkeyURL = os.Getenv("GATEWAY_TEST_VALKEY_URL")
	if dbURL == "" || valkeyURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL and GATEWAY_TEST_VALKEY_URL to run integration tests")
	}
	return
}

// seedTenantWithLocation inserts org → tenant → merchant → l.locations
// row. Returns merchantID, tenantID, locationCode, and a cleanup func.
func seedTenantWithLocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, string, func()) {
	t.Helper()
	orgID := uuid.New()
	tenantID := uuid.New()
	merchantID := uuid.New()
	locationID := uuid.New()
	tenantCode := "sub2-it-" + uuid.NewString()[:8]
	schemaName := "tenant_sub2_it_" + uuid.NewString()[:8]
	sourceMerchantID := "sub2-it-source-" + merchantID.String()[:8]
	locationCode := "L-IT-" + uuid.NewString()[:8]

	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec %q: %v", sql, err)
		}
	}

	exec(`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-761 Sub2 IT Org")
	exec(`INSERT INTO app.tenants (id, organization_id, tenant_code, name, status, schema_name)
	       VALUES ($1, $2, $3, $4, 'active', $5)`,
		tenantID, orgID, tenantCode, "Sub2 IT Tenant", schemaName)
	exec(`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
	       VALUES ($1, $2, $3, $4, 'GRO-761 Sub2 IT Merchant')`,
		merchantID, orgID, tenantID, sourceMerchantID)
	exec(`INSERT INTO l.locations (id, tenant_id, location_code, name, location_type)
	       VALUES ($1, $2, $3, 'Sub2 IT Location', 'store')`,
		locationID, tenantID, locationCode)

	cleanup := func() {
		// In dependency order. Triggers don't block t.* rows.
		_, _ = pool.Exec(ctx, `DELETE FROM t.transaction_tenders WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM t.transaction_line_items WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM t.transactions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM l.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return merchantID, tenantID, locationCode, cleanup
}

// makeEnvelope wraps an arbitrary payload as a publisher.Event.
func makeEnvelope(merchantID uuid.UUID, sourceCode string, payload []byte) publisher.Event {
	return publisher.Event{
		EventID:    uuid.New(),
		EventHash:  "sub2-it-" + uuid.NewString(),
		SourceCode: sourceCode,
		MerchantID: merchantID,
		Timestamp:  time.Now().UTC(),
		IngestedAt: time.Now().UTC(),
		Payload:    payload,
		Nonce:      uuid.NewString(),
	}
}

func TestIntegration_Sub2_BothAdaptersLandInSameTable(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	opts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		t.Fatalf("parse valkey: %v", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("valkey ping: %v", err)
	}

	merchantID, tenantID, locationCode, cleanup := seedTenantWithLocation(t, ctx, pool)
	defer cleanup()

	// Build envelopes that point at the seeded location_code.
	squarePayload := json.RawMessage(`{
	  "type": "payment.created",
	  "data": {
	    "type": "payment",
	    "id": "pay-it-1",
	    "object": {
	      "payment": {
	        "id": "pay-it-1",
	        "location_id": "` + locationCode + `",
	        "amount_money": {"amount": 1000, "currency": "USD"},
	        "created_at": "2026-05-02T18:00:00Z"
	      }
	    }
	  }
	}`)

	cpPayload := json.RawMessage(`{
	  "DocumentNumber": "DOC-IT-1",
	  "DocumentType": "TKT",
	  "DocumentDate": "2026-05-02T19:00:00Z",
	  "StoreNumber": "` + locationCode + `",
	  "Currency": "USD",
	  "Subtotal": 25.00,
	  "Total": 27.50,
	  "TaxAmount": 2.50,
	  "Lines": [],
	  "Payments": []
	}`)

	stream := "protocol:events:sub2-it-" + uuid.NewString()[:8]
	group := "sub2-it-" + uuid.NewString()[:8]
	_, _ = rdb.Del(ctx, stream).Result()

	reg := adapters.NewRegistry()
	reg.MustRegister(square.New())
	reg.MustRegister(counterpoint.New())

	logger := zap.NewNop()
	w := sub2.NewWorker(sub2.WorkerConfig{
		Pool:         pool,
		Redis:        rdb,
		Lookup:       adapters.NewLookup(reg),
		Stream:       stream,
		Group:        group,
		DLQStream:    stream + ":dlq",
		Consumer:     "sub2-it-consumer",
		BlockTimeout: 200 * time.Millisecond,
		Logger:       logger,
	})
	if err := w.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	pub := publisher.NewValkey(rdb, stream)
	if err := pub.Publish(ctx, makeEnvelope(merchantID, "square", squarePayload)); err != nil {
		t.Fatalf("publish square: %v", err)
	}
	if err := pub.Publish(ctx, makeEnvelope(merchantID, "counterpoint", cpPayload)); err != nil {
		t.Fatalf("publish counterpoint: %v", err)
	}

	// Drive the worker for a couple of batches.
	for i := 0; i < 4; i++ {
		if err := w.ProcessBatch(ctx); err != nil {
			t.Fatalf("ProcessBatch %d: %v", i, err)
		}
	}

	// Verify both rows landed with distinct source markers in
	// external_ids (the discriminator for "did this row come from
	// adapter X?").
	rows, err := pool.Query(ctx, `
		SELECT transaction_number, transaction_type, grand_total, external_ids
		FROM t.transactions
		WHERE tenant_id = $1
		ORDER BY transaction_number
	`, tenantID)
	if err != nil {
		t.Fatalf("select transactions: %v", err)
	}
	defer rows.Close()

	type row struct {
		num    string
		typ    string
		total  string
		extIDs []byte
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.num, &r.typ, &r.total, &r.extIDs); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 transactions, got %d: %+v", len(got), got)
	}

	// Indexed-by-number lookup
	byNum := map[string]row{}
	for _, r := range got {
		byNum[r.num] = r
	}
	cp, ok := byNum["DOC-IT-1"]
	if !ok {
		t.Fatal("Counterpoint row not found")
	}
	if cp.typ != "sale" {
		t.Errorf("CP txn type = %q want sale", cp.typ)
	}

	sq, ok := byNum["pay-it-1"]
	if !ok {
		t.Fatal("Square row not found")
	}
	if sq.typ != "sale" {
		t.Errorf("Square txn type = %q want sale", sq.typ)
	}

	// Verify external_ids carry source-specific keys — proves they
	// came through the right adapter.
	var sqExt, cpExt map[string]any
	_ = json.Unmarshal(sq.extIDs, &sqExt)
	_ = json.Unmarshal(cp.extIDs, &cpExt)
	if _, ok := sqExt["square_payment_id"]; !ok {
		t.Errorf("Square row external_ids missing square_payment_id: %s", sq.extIDs)
	}
	if _, ok := cpExt["counterpoint_document_number"]; !ok {
		t.Errorf("Counterpoint row external_ids missing counterpoint_document_number: %s", cp.extIDs)
	}

	t.Logf("substrate proven: 2 sources → 2 rows in t.transactions, dispatched by SourceCode")

	// Cleanup the stream
	_, _ = rdb.Del(ctx, stream).Result()
	_, _ = rdb.Del(ctx, stream+":dlq").Result()
}

func TestIntegration_Sub2_UnknownSource_AcksAndDoesNotPersist(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()
	opts, _ := redis.ParseURL(valkeyURL)
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	merchantID, tenantID, _, cleanup := seedTenantWithLocation(t, ctx, pool)
	defer cleanup()

	stream := "protocol:events:sub2-it-unk-" + uuid.NewString()[:8]
	group := "sub2-unk-" + uuid.NewString()[:8]
	_, _ = rdb.Del(ctx, stream).Result()

	reg := adapters.NewRegistry()
	reg.MustRegister(square.New())

	w := sub2.NewWorker(sub2.WorkerConfig{
		Pool:         pool,
		Redis:        rdb,
		Lookup:       adapters.NewLookup(reg),
		Stream:       stream,
		Group:        group,
		Consumer:     "sub2-unk-consumer",
		BlockTimeout: 200 * time.Millisecond,
	})
	if err := w.EnsureGroup(ctx); err != nil {
		t.Fatalf("ensure group: %v", err)
	}

	pub := publisher.NewValkey(rdb, stream)
	if err := pub.Publish(ctx, makeEnvelope(merchantID, "neverheardofit", []byte(`{}`))); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := w.ProcessBatch(ctx); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM t.transactions WHERE tenant_id = $1`,
		tenantID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("unknown source should not persist; got %d rows", count)
	}

	pending, err := rdb.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("xpending: %v", err)
	}
	if pending.Count != 0 {
		t.Errorf("unknown source must be ACKed; pending=%d", pending.Count)
	}

	_, _ = rdb.Del(ctx, stream).Result()
}
