// Package devops serves the Canary Go devops console at /devops.
//
// Pages:
//   GET /devops                  → pipeline monitor (HTML)
//   GET /devops/api              → API explorer shell (HTML, iframe)
//   GET /devops/api/redoc        → Redoc standalone page (served in iframe)
//   GET /devops/api/spec.yaml    → raw OpenAPI YAML
//   GET /devops/api/pipeline-state → pipeline state JSON
//   GET /devops/static/*         → embedded CSS / assets
//
// Auth: dev-only guard via DEV_CONSOLE env var (any non-empty value enables).
// In production set DEV_CONSOLE="" to disable the entire route group.
package devops

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

//go:embed static templates
var embedFS embed.FS

// Handler is the devops console handler.
type Handler struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	logger *zap.Logger
	tmpl   *template.Template
}

// New constructs a Handler. Logger may be nil.
func New(pool *pgxpool.Pool, rdb *redis.Client, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	tmpl := template.Must(template.ParseFS(embedFS,
		"templates/base.html",
		"templates/pipeline.html",
		"templates/api.html",
	))
	return &Handler{pool: pool, rdb: rdb, logger: logger, tmpl: tmpl}
}

// Mount registers all devops routes under /devops on r.
// If DEV_CONSOLE env var is empty the group is not mounted.
func (h *Handler) Mount(r chi.Router) {
	if os.Getenv("DEV_CONSOLE") == "" {
		h.logger.Info("devops console disabled (set DEV_CONSOLE=1 to enable)")
		return
	}

	staticFS, _ := fs.Sub(embedFS, "static")

	r.Route("/devops", func(r chi.Router) {
		r.Get("/", h.pipelinePage)
		r.Get("/api", h.apiExplorerPage)
		r.Get("/api/redoc", h.redocPage)
		r.Get("/api/spec.yaml", h.apiSpec)
		r.Get("/api/pipeline-state", h.pipelineState)
		r.Handle("/static/*", http.StripPrefix("/devops/static/",
			http.FileServer(http.FS(staticFS))))
	})

	h.logger.Info("devops console mounted", zap.String("path", "/devops"))
}

// ── Pages ─────────────────────────────────────────────────────────────

func (h *Handler) pipelinePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "base.html", map[string]any{
		"Page": "pipeline",
	}); err != nil {
		h.logger.Error("pipeline template", zap.Error(err))
	}
}

func (h *Handler) apiExplorerPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "base.html", map[string]any{
		"Page": "api",
	}); err != nil {
		h.logger.Error("api template", zap.Error(err))
	}
}

const redocHTML = `<!DOCTYPE html>
<html>
<head>
  <title>Canary API Reference</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;600;700&display=swap" rel="stylesheet">
  <style>body { margin: 0; padding: 0; background: #060608; }</style>
</head>
<body>
  <redoc spec-url="/devops/api/spec.yaml"
         theme='{"colors":{"primary":{"main":"#FBBF24"}},"typography":{"fontFamily":"Inter, system-ui, sans-serif"}}'
         hide-loading
         expand-responses="200,201"
         required-props-first
         sort-props-alphabetically></redoc>
  <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
</body>
</html>`

func (h *Handler) redocPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(redocHTML))
}

func (h *Handler) apiSpec(w http.ResponseWriter, r *http.Request) {
	data, err := embedFS.ReadFile("static/canary-api-v1.yaml")
	if err != nil {
		http.Error(w, "spec not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ── Pipeline state JSON ───────────────────────────────────────────────

type pipelineStateResp struct {
	Timestamp time.Time    `json:"timestamp"`
	Evidence  evidenceSnap `json:"evidence"`
	Stream    streamSnap   `json:"stream"`
	DLQ       dlqSnap      `json:"dlq"`
	Sequence  seqSnap      `json:"sequence"`
}

type evidenceSnap struct {
	Total  int64           `json:"total"`
	Recent []evidenceRow   `json:"recent"`
}

type evidenceRow struct {
	EventID      string    `json:"event_id"`
	EventHash    string    `json:"event_hash"`
	ChainHash    string    `json:"chain_hash"`
	SourceCode   string    `json:"source_code"`
	IngestedAt   time.Time `json:"ingested_at"`
}

type streamSnap struct {
	Length         int64           `json:"length"`
	ConsumerGroups []consumerGroup `json:"consumer_groups"`
}

type consumerGroup struct {
	Name    string `json:"name"`
	Pending int64  `json:"pending"`
	Lag     int64  `json:"lag"`
}

type dlqSnap struct {
	Pending   int64 `json:"pending"`
	Abandoned int64 `json:"abandoned"`
	Replayed  int64 `json:"replayed"`
}

type seqSnap struct {
	Gaps       int64    `json:"gaps"`
	RecentGaps []seqRow `json:"recent_gaps"`
}

type seqRow struct {
	SourceCode      string    `json:"source_code"`
	SequenceID      string    `json:"sequence_id"`
	EventID         string    `json:"event_id"`
	ExpectedPrevSeq string    `json:"expected_prev_seq"`
	ReceivedAt      time.Time `json:"received_at"`
}

func (h *Handler) pipelineState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := pipelineStateResp{Timestamp: time.Now().UTC()}

	// Evidence counts + recent rows
	resp.Evidence = h.queryEvidence(ctx)

	// Valkey stream
	resp.Stream = h.queryStream(ctx)

	// DLQ
	resp.DLQ = h.queryDLQ(ctx)

	// Sequence gaps
	resp.Sequence = h.querySequence(ctx)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// ── DB helpers ────────────────────────────────────────────────────────

func (h *Handler) queryEvidence(ctx context.Context) evidenceSnap {
	snap := evidenceSnap{Recent: []evidenceRow{}}

	row := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM protocol.evidence`)
	_ = row.Scan(&snap.Total)

	rows, err := h.pool.Query(ctx, `
		SELECT event_id::text, event_hash, chain_hash, source_code, ingested_at
		FROM protocol.evidence
		ORDER BY ingested_at DESC
		LIMIT 20`)
	if err != nil {
		h.logger.Warn("evidence query", zap.Error(err))
		return snap
	}
	defer rows.Close()
	for rows.Next() {
		var er evidenceRow
		if err := rows.Scan(&er.EventID, &er.EventHash, &er.ChainHash, &er.SourceCode, &er.IngestedAt); err == nil {
			snap.Recent = append(snap.Recent, er)
		}
	}
	return snap
}

func (h *Handler) queryDLQ(ctx context.Context) dlqSnap {
	var snap dlqSnap
	rows, err := h.pool.Query(ctx, `
		SELECT status, COUNT(*) FROM protocol.dlq GROUP BY status`)
	if err != nil {
		h.logger.Warn("dlq query", zap.Error(err))
		return snap
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		switch status {
		case "pending":
			snap.Pending = count
		case "abandoned":
			snap.Abandoned = count
		case "replayed":
			snap.Replayed = count
		}
	}
	return snap
}

func (h *Handler) querySequence(ctx context.Context) seqSnap {
	snap := seqSnap{RecentGaps: []seqRow{}}

	row := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM protocol.tsp_sequence_log WHERE gap_detected = TRUE`)
	_ = row.Scan(&snap.Gaps)

	rows, err := h.pool.Query(ctx, `
		SELECT source_code, sequence_id, event_id::text,
		       COALESCE(expected_prev_seq, ''), received_at
		FROM protocol.tsp_sequence_log
		WHERE gap_detected = TRUE
		ORDER BY received_at DESC
		LIMIT 20`)
	if err != nil {
		h.logger.Warn("sequence gap query", zap.Error(err))
		return snap
	}
	defer rows.Close()
	for rows.Next() {
		var sr seqRow
		if err := rows.Scan(&sr.SourceCode, &sr.SequenceID, &sr.EventID, &sr.ExpectedPrevSeq, &sr.ReceivedAt); err == nil {
			snap.RecentGaps = append(snap.RecentGaps, sr)
		}
	}
	return snap
}

func (h *Handler) queryStream(ctx context.Context) streamSnap {
	snap := streamSnap{ConsumerGroups: []consumerGroup{}}

	length, err := h.rdb.XLen(ctx, "protocol:events").Result()
	if err != nil {
		h.logger.Warn("xlen protocol:events", zap.Error(err))
		return snap
	}
	snap.Length = length

	groups, err := h.rdb.XInfoGroups(ctx, "protocol:events").Result()
	if err != nil {
		// Stream may not have groups yet — not an error
		return snap
	}
	for _, g := range groups {
		snap.ConsumerGroups = append(snap.ConsumerGroups, consumerGroup{
			Name:    g.Name,
			Pending: g.Pending,
			Lag:     g.Lag,
		})
	}
	return snap
}
