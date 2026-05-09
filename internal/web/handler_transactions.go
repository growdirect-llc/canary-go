package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/protocol/validate"
	"github.com/ruptiv/canary/internal/transaction"
)

// transactionDetailPage renders one canonical transaction with hydrated
// line items, tenders, and discounts. Falls back to the stub view when the
// TransactionStore is unavailable (pre-wire dev path). Wired W2a.
func (h *Handler) transactionDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transactions", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.TransactionStore == nil {
		h.render(w, r, "transaction_detail", "transactions", map[string]any{
			"Transaction": map[string]any{
				"ID": idStr, "ShortID": shortID, "POSSource": "—",
				"Amount": "—", "Cashier": "—", "StoreID": "—",
				"Hash":        deriveTxnHash(idStr),
				"SealStatus":  "pending",
				"ParseStatus": "pending",
				"CreatedAt":   "—",
			},
			"Events": nil, "LineItems": nil, "AlertCount": 0,
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	dto, err := h.deps.TransactionStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, transaction.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transactions", nil)
			return
		}
		h.logger.Error("transactionDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transactions", nil)
		return
	}

	cashier := "—"
	if dto.CashierEmployeeID != nil {
		cashier = dto.CashierEmployeeID.String()[:8]
	}
	pos := "—"
	if dto.POSTerminalID != nil && *dto.POSTerminalID != "" {
		pos = *dto.POSTerminalID
	}

	lineItems := make([]map[string]any, 0, len(dto.LineItems))
	for _, li := range dto.LineItems {
		sku := "—"
		if li.ItemID != nil {
			sku = li.ItemID.String()[:8]
		}
		lineItems = append(lineItems, map[string]any{
			"SKU":         sku,
			"Description": li.Description,
			"Qty":         li.Quantity.String(),
			"UnitPrice":   li.UnitPrice.String(),
			"Extended":    li.LineTotal.String(),
		})
	}

	// Canonical events for the transaction header — render a single event
	// summarizing the txn type + amount until tsp event ingestion lands.
	events := []map[string]any{
		{
			"Type":      dto.TransactionType,
			"Amount":    dto.GrandTotal.String(),
			"Cashier":   cashier,
			"Timestamp": dto.EndedAt.Format(time.RFC3339),
		},
	}

	h.render(w, r, "transaction_detail", "transactions", map[string]any{
		"Transaction": map[string]any{
			"ID":          dto.ID.String(),
			"ShortID":     dto.ID.String()[:8],
			"POSSource":   pos,
			"Amount":      dto.GrandTotal.String() + " " + dto.Currency,
			"Cashier":     cashier,
			"StoreID":     dto.LocationID.String()[:8],
			"Hash":        deriveTxnHash(dto.ID.String()),
			"SealStatus":  txnSealStatus(dto),
			"ParseStatus": "ok",
			"CreatedAt":   dto.CreatedAt.Format(time.RFC3339),
		},
		"Events":     events,
		"LineItems":  lineItems,
		"AlertCount": 0, // populated when alert→transaction join lands (out of scope)
	})
}

// transactionProofPage renders the audit proof for a transaction by looking
// up the anchor record keyed by the transaction's derived event_hash.
// Returns "pending" when the protocol pipeline hasn't anchored this txn yet —
// the common state today since the demo path doesn't yet feed retail txns
// into Sub1/Sub3. Wired W2a.
func (h *Handler) transactionProofPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transactions", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	view := map[string]any{
		"Transaction": map[string]any{
			"ID":        idStr,
			"ShortID":   shortID,
			"Hash":      deriveTxnHash(idStr),
			"CreatedAt": "—",
		},
		"ProofStatus": "pending",
		"MerklePath":  nil,
		"RootHash":    "—",
		"AnchorRef":   "—",
		"AnchoredAt":  "—",
	}

	// Fill CreatedAt from the txn record when available.
	if h.deps.TransactionStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		dto, err := h.deps.TransactionStore.GetByID(ctx, tenantID, id)
		if err == nil {
			view["Transaction"].(map[string]any)["CreatedAt"] = dto.CreatedAt.Format(time.RFC3339)
		} else if errors.Is(err, transaction.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transactions", nil)
			return
		}
	}

	if h.deps.ValidateStore != nil {
		eventHash := deriveTxnHash(idStr)
		proof, err := h.deps.ValidateStore.GetAnchorProof(r.Context(), eventHash)
		switch {
		case err == nil:
			view["ProofStatus"] = "valid"
			view["RootHash"] = proof.MerkleRoot
			view["AnchoredAt"] = proof.AnchoredAt.Format(time.RFC3339)
			view["AnchorRef"] = formatAnchorRef(proof)
			view["MerklePath"] = decodeMerklePath(proof.MerkleProof)
		case errors.Is(err, validate.ErrNotAnchored), errors.Is(err, validate.ErrNotFound):
			// Stay "pending" — common case until protocol pipeline anchors retail txns.
		default:
			h.logger.Error("transactionProofPage: get anchor proof",
				zap.String("event_hash", eventHash), zap.Error(err))
			// Render as pending rather than 5xx so the operator still sees the page.
		}
	}

	h.render(w, r, "transaction_proof", "transactions", view)
}

// deriveTxnHash returns a deterministic event_hash for a transaction's UUID.
// Used as the protocol-pipeline lookup key. Hex-encoded SHA-256(uuid-string).
func deriveTxnHash(txnID string) string {
	sum := sha256.Sum256([]byte(txnID))
	return hex.EncodeToString(sum[:])
}

// txnSealStatus reports the seal state for a transaction. Until tsp event
// ingestion lands, every persisted txn is treated as "sealed" by the canonical
// store (the row exists). Reflects the current on-the-wire reality.
func txnSealStatus(_ *transaction.TransactionDTO) string {
	return "sealed"
}

// formatAnchorRef collapses an AnchorProof's chain coordinates into a single
// display string for the proof page sidebar.
func formatAnchorRef(p *validate.AnchorProof) string {
	switch {
	case p.InscriptionID != nil:
		return *p.InscriptionID
	case p.BtcTxID != nil && p.BtcBlockHeight != nil:
		return p.Network + " " + (*p.BtcTxID)[:12] + "@" + intToString(*p.BtcBlockHeight)
	case p.BtcTxID != nil:
		return p.Network + " " + (*p.BtcTxID)[:12]
	default:
		return p.Network + " (anchor pending)"
	}
}

// decodeMerklePath unmarshals the proof.MerkleProof jsonb into a slice of
// {Index, Hash} maps for template rendering. Returns nil on parse error so
// the template falls back to its empty-state branch.
func decodeMerklePath(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var nodes []struct {
		Index int    `json:"index"`
		Hash  string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{"Index": n.Index, "Hash": n.Hash})
	}
	return out
}

func intToString(n int64) string {
	return strconv.FormatInt(n, 10)
}
