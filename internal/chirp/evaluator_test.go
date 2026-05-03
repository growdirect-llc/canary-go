package chirp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/chirp/rules"
	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// stubStore is an in-memory chirp.Store for unit tests. Only the
// methods the engine actually exercises in EvaluateTransaction are
// implemented; the rest return empty/zero values.
type stubStore struct {
	tx          *chirp.Transaction
	rules       []chirp.Rule
	ec          *chirp.EvalContext
	insertedDet []chirp.Detection
	insertErr   error
}

func (s *stubStore) LoadRules(_ context.Context, _ uuid.UUID, _ string) ([]chirp.Rule, error) {
	return s.rules, nil
}
func (s *stubStore) LoadTransaction(_ context.Context, _ uuid.UUID) (*chirp.Transaction, error) {
	return s.tx, nil
}
func (s *stubStore) LoadEvalContext(_ context.Context, _ *chirp.Transaction) (*chirp.EvalContext, error) {
	return s.ec, nil
}
func (s *stubStore) LoadCashierWindow(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]chirp.CashierAction, error) {
	return nil, nil
}
func (s *stubStore) InsertDetection(_ context.Context, d *chirp.Detection) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	d.ID = uuid.New()
	d.CreatedAt = time.Now().UTC()
	s.insertedDet = append(s.insertedDet, *d)
	return nil
}
func (s *stubStore) ListTransactionsSince(_ context.Context, _ uuid.UUID, _ time.Time) ([]uuid.UUID, error) {
	return nil, nil
}
func (s *stubStore) ListRules(_ context.Context, _ uuid.UUID) ([]chirp.Rule, error) {
	return s.rules, nil
}
func (s *stubStore) ListDetections(_ context.Context, _ chirp.DetectionQuery) ([]chirp.Detection, error) {
	return s.insertedDet, nil
}

func makeRule(t *testing.T, code, ruleType string, params any) chirp.Rule {
	t.Helper()
	envelope := map[string]any{
		"rule_type":  ruleType,
		"parameters": params,
	}
	def, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal rule_definition: %v", err)
	}
	return chirp.Rule{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		RuleCode:            code,
		Name:                code,
		RuleCategory:        "shrink",
		RuleDefinition:      def,
		Severity:            "high",
		Status:              "active",
		EvaluationFrequency: "on_event",
		Attributes:          json.RawMessage(`{}`),
	}
}

func makeTx(loc, cashier uuid.UUID) *chirp.Transaction {
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	cashierPtr := cashier
	return &chirp.Transaction{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		TransactionNumber: "TX-1001",
		TransactionType:   "sale",
		LocationID:        loc,
		CashierEmployeeID: &cashierPtr,
		StartedAt:         now,
		EndedAt:           now.Add(2 * time.Minute),
		BusinessDate:      now,
		Status:            "completed",
		Subtotal:          "100.0000",
		GrandTotal:        "108.0000",
		Currency:          "USD",
		Channel:           "pos",
		Attributes:        json.RawMessage(`{}`),
	}
}

func TestVoidThreshold(t *testing.T) {
	cases := []struct {
		name           string
		threshold      int64
		voidedTotals   []string
		wantDetections int
	}{
		{"under threshold", 5000, []string{"10.0000", "5.0000"}, 0},
		{"at threshold", 5000, []string{"50.0000"}, 1},
		{"over threshold many lines", 5000, []string{"30.0000", "30.0000"}, 1},
		{"none voided", 5000, []string{}, 0},
		{"zero threshold uses default", 0, []string{"75.0000"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := makeRule(t, "C-VOID", "void_threshold", map[string]any{"threshold_cents": tc.threshold})
			loc := uuid.New()
			cashier := uuid.New()
			tx := makeTx(loc, cashier)
			var lis []chirp.LineItem
			for i, v := range tc.voidedTotals {
				lis = append(lis, chirp.LineItem{
					ID:            uuid.New(),
					TransactionID: tx.ID,
					LineNumber:    int32(i + 1),
					IsVoid:        true,
					LineTotal:     v,
					UnitPrice:     "10.0000",
					Quantity:      "1.0000",
					UnitOfMeasure: "EA",
					Description:   "test",
					Attributes:    json.RawMessage(`{}`),
				})
			}
			ec := &chirp.EvalContext{
				TenantID:    rule.TenantID,
				Transaction: tx,
				LineItems:   lis,
			}
			got, err := (rules.VoidThreshold{}).Evaluate(context.Background(), &rule, ec)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if len(got) != tc.wantDetections {
				t.Fatalf("want %d detections, got %d", tc.wantDetections, len(got))
			}
		})
	}
}

func TestNoSaleFrequency(t *testing.T) {
	cashier := uuid.New()
	loc := uuid.New()
	tx := makeTx(loc, cashier)
	mkAction := func(actionType string) chirp.CashierAction {
		return chirp.CashierAction{
			ID:                uuid.New(),
			TenantID:          tx.TenantID,
			LocationID:        loc,
			CashierEmployeeID: cashier,
			ActionType:        actionType,
			PerformedAt:       time.Now().UTC(),
			Details:           json.RawMessage(`{}`),
			Attributes:        json.RawMessage(`{}`),
		}
	}
	cases := []struct {
		name      string
		threshold int
		actions   []chirp.CashierAction
		want      int
	}{
		{"under", 5, []chirp.CashierAction{mkAction("no_sale"), mkAction("no_sale")}, 0},
		{"at", 3, []chirp.CashierAction{mkAction("no_sale"), mkAction("no_sale"), mkAction("no_sale")}, 1},
		{"mixed types ignored", 3, []chirp.CashierAction{mkAction("no_sale"), mkAction("price_lookup"), mkAction("price_lookup")}, 0},
		{"alt action type", 2, []chirp.CashierAction{mkAction("drawer_open_no_sale"), mkAction("drawer_open_no_sale")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := makeRule(t, "C-NS", "no_sale_frequency", map[string]any{"threshold_count": tc.threshold, "window_minutes": 60})
			ec := &chirp.EvalContext{TenantID: rule.TenantID, Transaction: tx, CashierActions: tc.actions}
			got, err := (rules.NoSaleFrequency{}).Evaluate(context.Background(), &rule, ec)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("want %d, got %d", tc.want, len(got))
			}
		})
	}
}

func TestRefundNoReceipt(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()

	t.Run("refund without parent fires", func(t *testing.T) {
		tx := makeTx(loc, cashier)
		tx.TransactionType = "refund"
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx}
		rule := makeRule(t, "C-REF", "refund_no_receipt", struct{}{})
		got, err := (rules.RefundNoReceipt{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1, got %d", len(got))
		}
	})

	t.Run("refund with parent does not fire", func(t *testing.T) {
		tx := makeTx(loc, cashier)
		tx.TransactionType = "refund"
		parent := uuid.New()
		tx.ParentTransactionID = &parent
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx}
		rule := makeRule(t, "C-REF", "refund_no_receipt", struct{}{})
		got, err := (rules.RefundNoReceipt{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})

	t.Run("return line item with no parent fires", func(t *testing.T) {
		tx := makeTx(loc, cashier)
		ec := &chirp.EvalContext{
			TenantID:    tx.TenantID,
			Transaction: tx,
			LineItems: []chirp.LineItem{{
				ID: uuid.New(), TransactionID: tx.ID, LineNumber: 1,
				IsReturn:      true,
				LineTotal:     "-10.0000",
				UnitPrice:     "10.0000",
				Quantity:      "1.0000",
				UnitOfMeasure: "EA",
				Description:   "test",
				Attributes:    json.RawMessage(`{}`),
			}},
		}
		rule := makeRule(t, "C-REF", "refund_no_receipt", struct{}{})
		got, err := (rules.RefundNoReceipt{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1, got %d", len(got))
		}
	})

	t.Run("plain sale does not fire", func(t *testing.T) {
		tx := makeTx(loc, cashier)
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx}
		rule := makeRule(t, "C-REF", "refund_no_receipt", struct{}{})
		got, err := (rules.RefundNoReceipt{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})
}

func TestManagerOverrideFrequency(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	tx := makeTx(loc, cashier)
	mgr1 := uuid.New()
	mgr2 := uuid.New()
	mkAction := func(actionType string, mgr *uuid.UUID) chirp.CashierAction {
		return chirp.CashierAction{
			ID:                     uuid.New(),
			TenantID:               tx.TenantID,
			LocationID:             loc,
			CashierEmployeeID:      cashier,
			ActionType:             actionType,
			PerformedAt:            time.Now().UTC(),
			AuthorizedByEmployeeID: mgr,
			Details:                json.RawMessage(`{}`),
			Attributes:             json.RawMessage(`{}`),
		}
	}
	rule := makeRule(t, "C-MO", "manager_override_frequency", map[string]any{"threshold_count": 3, "window_minutes": 60})

	t.Run("two managers, one over threshold", func(t *testing.T) {
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx, CashierActions: []chirp.CashierAction{
			mkAction("manager_override", &mgr1),
			mkAction("manager_override", &mgr1),
			mkAction("manager_override", &mgr1),
			mkAction("price_override", &mgr2),
		}}
		got, err := (rules.ManagerOverrideFrequency{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1, got %d", len(got))
		}
	})

	t.Run("no overrides", func(t *testing.T) {
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx}
		got, err := (rules.ManagerOverrideFrequency{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})

	t.Run("nil authorizer ignored", func(t *testing.T) {
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx, CashierActions: []chirp.CashierAction{
			mkAction("manager_override", nil),
			mkAction("manager_override", nil),
			mkAction("manager_override", nil),
		}}
		got, err := (rules.ManagerOverrideFrequency{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})
}

func TestAfterHoursTransaction(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	rule := makeRule(t, "C-AH", "after_hours_transaction", map[string]any{"tolerance_minutes": 5})

	// Saturday 2026-05-02 in UTC.
	mkTxAt := func(h, m int) *chirp.Transaction {
		tx := makeTx(loc, cashier)
		tx.StartedAt = time.Date(2026, 5, 2, h, m, 0, 0, time.UTC)
		tx.EndedAt = tx.StartedAt.Add(time.Minute)
		return tx
	}

	cases := []struct {
		name  string
		hours string
		hour  int
		tz    string
		want  int
	}{
		{"open", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 14, "", 0},
		{"closed all day", `{"sunday":[{"open":"07:00","close":"22:00"}]}`, 14, "", 1},
		{"before open", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 4, "", 1},
		{"after close", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 23, "", 1},
		{"empty operating_hours", `{}`, 14, "", 1},
		{"missing operating_hours", "", 14, "", 0}, // no config = skip silently
		{"split shift, lunch closed", `{"saturday":[{"open":"07:00","close":"11:00"},{"open":"17:00","close":"22:00"}]}`, 13, "", 1},
		{"split shift, dinner open", `{"saturday":[{"open":"07:00","close":"11:00"},{"open":"17:00","close":"22:00"}]}`, 19, "", 0},
		// TZ cases: 2026-05-02 14:00 UTC = 10:00 EDT (within 07:00-22:00 NY local) — should NOT fire
		{"NY tz, mid-day local, open", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 14, "America/New_York", 0},
		// 2026-05-02 02:00 UTC Saturday = 22:00 EDT Friday (after close on Friday) — should fire (was previously falsely-passing as 02:00 Saturday "closed all day" by hitting Saturday key with no entry)
		{"NY tz, late evening local Friday after close", `{"friday":[{"open":"07:00","close":"21:00"}]}`, 2, "America/New_York", 1},
		// 2026-05-02 14:00 UTC = 23:00 JST (Saturday, after Tokyo close at 22:00) — should fire
		{"Tokyo tz, after close local", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 14, "Asia/Tokyo", 1},
		// Bogus tz string falls back to UTC — keeps existing UTC behavior (open at UTC 14:00)
		{"invalid tz falls back to UTC", `{"saturday":[{"open":"07:00","close":"22:00"}]}`, 14, "Not/AReal/Zone", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := mkTxAt(tc.hour, 0)
			var rawHours []byte
			if tc.hours != "" {
				rawHours = []byte(tc.hours)
			}
			ec := &chirp.EvalContext{
				TenantID:               tx.TenantID,
				Transaction:            tx,
				LocationOperatingHours: rawHours,
				LocationTimezone:       tc.tz,
			}
			got, err := (rules.AfterHoursTransaction{}).Evaluate(context.Background(), &rule, ec)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("want %d, got %d", tc.want, len(got))
			}
		})
	}
}

func TestLargeDiscountPct(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	tx := makeTx(loc, cashier)
	rule := makeRule(t, "C-DISC", "large_discount_pct", map[string]any{"threshold_pct": 50.0})

	mkLine := func(price, disc string, num int32, void bool) chirp.LineItem {
		return chirp.LineItem{
			ID:            uuid.New(),
			TransactionID: tx.ID,
			LineNumber:    num,
			UnitPrice:     price,
			UnitDiscount:  disc,
			Quantity:      "1.0000",
			UnitOfMeasure: "EA",
			Description:   "x",
			IsVoid:        void,
			Attributes:    json.RawMessage(`{}`),
		}
	}

	t.Run("two over, one under, one void", func(t *testing.T) {
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx, LineItems: []chirp.LineItem{
			mkLine("10.0000", "6.0000", 1, false), // 60%
			mkLine("10.0000", "3.0000", 2, false), // 30%
			mkLine("10.0000", "5.0000", 3, false), // 50% — at threshold, fires
			mkLine("10.0000", "8.0000", 4, true),  // void, ignored
		}}
		got, err := (rules.LargeDiscountPct{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
	})

	t.Run("zero discount", func(t *testing.T) {
		ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx, LineItems: []chirp.LineItem{
			mkLine("10.0000", "0.0000", 1, false),
		}}
		got, err := (rules.LargeDiscountPct{}).Evaluate(context.Background(), &rule, ec)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0, got %d", len(got))
		}
	})
}

func TestCashDrawerVariance(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	tx := makeTx(loc, cashier)
	rule := makeRule(t, "C-CV", "cash_drawer_variance", map[string]any{"threshold_cents": 500})

	mkEvt := func(variance string) chirp.CashDrawerEvent {
		v := variance
		var vp *string
		if variance != "" {
			vp = &v
		}
		return chirp.CashDrawerEvent{
			ID:            uuid.New(),
			TenantID:      tx.TenantID,
			LocationID:    loc,
			POSTerminalID: "REG-1",
			EventType:     "drawer_count",
			EventAt:       time.Now().UTC(),
			Variance:      vp,
			Attributes:    json.RawMessage(`{}`),
		}
	}

	cases := []struct {
		name string
		evts []chirp.CashDrawerEvent
		want int
	}{
		{"no events", nil, 0},
		{"under threshold", []chirp.CashDrawerEvent{mkEvt("2.0000")}, 0},
		{"over threshold positive", []chirp.CashDrawerEvent{mkEvt("10.0000")}, 1},
		{"over threshold negative", []chirp.CashDrawerEvent{mkEvt("-7.0000")}, 1},
		{"nil variance ignored", []chirp.CashDrawerEvent{mkEvt("")}, 0},
		{"two events, one over", []chirp.CashDrawerEvent{mkEvt("1.0000"), mkEvt("100.0000")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ec := &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx, DrawerEvents: tc.evts}
			got, err := (rules.CashDrawerVariance{}).Evaluate(context.Background(), &rule, ec)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("want %d, got %d", tc.want, len(got))
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	r := chirp.NewRegistry()
	if got, _ := r.Lookup("nope"); got != nil {
		t.Fatalf("want nil for unknown rule type, got %v", got)
	}
	if r.Register(rules.VoidThreshold{}) {
		t.Fatal("first register should not report replacement")
	}
	if !r.Register(rules.VoidThreshold{}) {
		t.Fatal("second register should report replacement")
	}
	if e, ok := r.Lookup("void_threshold"); !ok || e.RuleType() != "void_threshold" {
		t.Fatal("registered evaluator not retrievable")
	}
	if len(r.RegisteredTypes()) != 1 {
		t.Fatalf("want 1 type, got %d", len(r.RegisteredTypes()))
	}
}

func TestEngineEvaluateTransaction_DispatchesAndPersists(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	tx := makeTx(loc, cashier)

	rule := makeRule(t, "C-VOID-1", "void_threshold", map[string]any{"threshold_cents": 1000})
	rule.TenantID = tx.TenantID
	store := &stubStore{
		tx:    tx,
		rules: []chirp.Rule{rule},
		ec: &chirp.EvalContext{
			TenantID:    tx.TenantID,
			Transaction: tx,
			LineItems: []chirp.LineItem{{
				ID: uuid.New(), TransactionID: tx.ID, LineNumber: 1, IsVoid: true,
				LineTotal: "50.0000", UnitPrice: "50.0000", Quantity: "1.0000", UnitOfMeasure: "EA",
				Description: "x", Attributes: json.RawMessage(`{}`),
			}},
		},
	}
	registry := chirp.NewRegistry()
	registry.Register(rules.VoidThreshold{})

	engine := chirp.NewEngine(store, registry, zap.NewNop())
	got, err := engine.EvaluateTransaction(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 detection, got %d", len(got))
	}
	if got[0].RuleID != rule.ID {
		t.Fatalf("rule id mismatch: want %s, got %s", rule.ID, got[0].RuleID)
	}
	if got[0].SourceEntityType != "transaction" {
		t.Fatalf("want source_entity_type=transaction, got %s", got[0].SourceEntityType)
	}
	if got[0].LocationID == nil || *got[0].LocationID != loc {
		t.Fatalf("location_id not propagated")
	}
}

func TestEngineEvaluateTransaction_UnknownRuleTypeSkipped(t *testing.T) {
	loc := uuid.New()
	cashier := uuid.New()
	tx := makeTx(loc, cashier)

	rule := makeRule(t, "C-WHAT", "totally_unknown_type", map[string]any{})
	rule.TenantID = tx.TenantID
	store := &stubStore{
		tx:    tx,
		rules: []chirp.Rule{rule},
		ec:    &chirp.EvalContext{TenantID: tx.TenantID, Transaction: tx},
	}
	registry := chirp.NewRegistry()

	engine := chirp.NewEngine(store, registry, zap.NewNop())
	got, err := engine.EvaluateTransaction(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("engine should swallow unknown rule type, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}

// Compile-time assertion that the stubStore satisfies the interface
// — keeps the test honest if the interface grows.
var _ chirp.Store = (*stubStore)(nil)

// Touch the types import so it stays — chirp.Detection is an alias
// of types.Detection and this guards against an accidental import
// removal that would silently drop coverage.
var _ = types.Detection{}
