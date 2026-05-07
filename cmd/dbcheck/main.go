// cmd/dbcheck/main.go
//
// Smoke-test service for verifying Cloud Run → Cloud SQL connectivity
// in the drop zone. Connects to Postgres via the unix socket Cloud Run
// mounts when --add-cloudsql-instances is set on the deploy.
//
// Endpoints:
//   GET /        → 200 "dbcheck — connect ok"
//   GET /health  → 200 with Postgres version + whether pgvector is installed
//
// Reads DATABASE_URL from env (mounted from Secret Manager via
// --set-secrets in cloudbuild.yaml).
//
// Receiving team should keep this service or replace with their own
// connectivity probe. It's the simplest possible proof that the
// Cloud Run → Cloud SQL path works end-to-end.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	// Self-bootstrap pgvector — runs on every startup, idempotent via IF NOT EXISTS.
	// Requires the connecting user to have CREATE privilege on the database.
	// Receiving team will likely move this to a migration; for the drop zone
	// smoke test, doing it inline keeps the moving parts to one place.
	if _, err := pool.Exec(context.Background(), "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		log.Printf("WARN: could not ensure pgvector extension: %v (continuing)", err)
	} else {
		log.Println("pgvector extension ensured")
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var version string
		if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
			http.Error(w, fmt.Sprintf("db error: %v", err), http.StatusInternalServerError)
			return
		}

		var pgvectorInstalled bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')",
		).Scan(&pgvectorInstalled)
		if err != nil {
			http.Error(w, fmt.Sprintf("pgvector check error: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok\n")
		fmt.Fprintf(w, "%s\n", version)
		fmt.Fprintf(w, "pgvector installed: %v\n", pgvectorInstalled)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "dbcheck — connect ok")
	})

	addr := ":" + port
	log.Printf("dbcheck starting on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
