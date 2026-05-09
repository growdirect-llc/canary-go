//go:build integration

// Concurrency hardening for GRO-908. Without the per-merchant
// advisory lock in WriteEvidence, two workers (or two retries)
// processing different events for the same merchant could both read
// the same prev_chain_hash, compute valid-but-divergent chain_hash
// values, and both insert — producing a fork. The patent claim of a
// linear per-merchant chain (Application 63/991,596, FIG. 4) depends
// on this not happening under horizontal scale.
//
// This test races N goroutines through WriteEvidence for one merchant
// and asserts the resulting rows form a single linear chain — every
// row's prev_chain_hash equals the previous row's chain_hash, no two
// rows share a prev, and the first row has an empty prev.
package sub1

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

func TestWriteEvidence_NoForkUnderConcurrency(t *testing.T) {
	dbURL, _ := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}

	merchantID, cleanup := seedMerchant(t, ctx, pool)
	defer cleanup()

	// N goroutines, each sealing a unique event for the same merchant.
	const N = 10

	// Shared barrier — every goroutine waits on the channel close to
	// release simultaneously. This maximizes the window in which they
	// would race to the same prev_chain_hash if the lock were absent.
	start := make(chan struct{})

	// Pre-build events outside the goroutines so we don't measure
	// allocation noise. We deliberately leave IngestedAt zero so that
	// WriteEvidence stamps it from time.Now() *inside* the per-merchant
	// lock — that way the chain's ingested_at order matches its commit
	// order. (LookupPrevChainHash uses ORDER BY ingested_at DESC; with
	// pre-set ingested_at and racy commit order, the chain definition
	// itself becomes ambiguous regardless of locking. In production the
	// gateway's clock is monotonic per merchant and Sub 1 processes in
	// order, so this isn't an issue — but the test must mirror that.)
	events := make([]publisher.Event, N)
	for i := 0; i < N; i++ {
		evt := makeEvent(merchantID, time.Time{})
		// Force unique event_hash per iteration. makeEvent already
		// derives it from the new event_id, so this is just defensive.
		evt.EventHash = fmt.Sprintf("eh-fork-race-%d-%s", i, evt.EventID.String())
		events[i] = evt
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errs    []error
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(evt publisher.Event) {
			defer wg.Done()
			<-start
			if _, err := WriteEvidence(ctx, pool, evt); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(events[i])
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("WriteEvidence returned error: %v", e)
		}
		t.FailNow()
	}

	// Read back every row for this merchant. We do NOT sort by
	// ingested_at — under concurrency the goroutines pick their
	// ingested_at before they race to commit, so commit order is
	// effectively random. The chain integrity invariant we care about
	// is *graph-shaped*: starting from the row with empty prev, walking
	// forward by chain_hash → next row's prev_chain_hash should visit
	// every row exactly once. A fork shows up as two rows sharing a
	// prev_chain_hash; a break shows up as orphan rows.
	rows, err := pool.Query(ctx, `
		SELECT event_id, chain_hash, COALESCE(prev_chain_hash, '')
		FROM protocol.evidence
		WHERE merchant_id = $1
	`, merchantID)
	if err != nil {
		t.Fatalf("select evidence: %v", err)
	}
	defer rows.Close()

	type ev struct {
		id        uuid.UUID
		chainHash string
		prevHash  string
	}
	var got []ev
	byPrev := map[string][]ev{}
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.chainHash, &e.prevHash); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, e)
		byPrev[e.prevHash] = append(byPrev[e.prevHash], e)
	}

	if len(got) != N {
		t.Fatalf("expected %d evidence rows, got %d", N, len(got))
	}

	// Fork check: no two rows may share a prev_chain_hash. The empty
	// prev (chain start) must appear exactly once.
	for prev, evs := range byPrev {
		if len(evs) > 1 {
			ids := make([]string, len(evs))
			for i, e := range evs {
				ids[i] = e.id.String()
			}
			t.Errorf("FORK DETECTED: %d rows share prev_chain_hash %q (event_ids: %v)",
				len(evs), prev, ids)
		}
	}

	// Walk the chain forward from the start (empty prev). It must visit
	// every row exactly once. byPrev maps prev_chain_hash → rows with
	// that prev; the unique successor of a row R is byPrev[R.chainHash].
	starts, ok := byPrev[""]
	if !ok || len(starts) != 1 {
		t.Fatalf("expected exactly 1 chain start (row with empty prev), found %d", len(starts))
	}
	visited := map[uuid.UUID]bool{}
	cur := starts[0]
	for {
		if visited[cur.id] {
			t.Fatalf("chain walk revisited %s — cycle?", cur.id)
		}
		visited[cur.id] = true
		successors, ok := byPrev[cur.chainHash]
		if !ok || len(successors) == 0 {
			break // end of chain
		}
		// Fork-detection above already errored if len(successors) > 1.
		cur = successors[0]
	}
	if len(visited) != N {
		t.Fatalf("chain walk visited %d rows, expected %d — chain is broken or forked",
			len(visited), N)
	}

	if !t.Failed() {
		t.Logf("linear chain of %d rows — no fork, no break.", N)
	}
}
