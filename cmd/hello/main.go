// cmd/hello/main.go
//
// Minimal HTTP service for CI/CD smoke testing — proves the
// git → Cloud Build → Artifact Registry → Cloud Run pipeline.
// No domain logic, no config dependencies. Receiving team should
// delete this binary once the real services are deploying cleanly.
//
// Endpoints:
//   GET /        → 200 "hello from canary-rapidpos drop zone"
//   GET /health  → 200 "ok"
//
// Note: /healthz is reserved by Google's Cloud Run frontend (Knative
// activator readiness probe) — using /health instead, matching the
// existing cmd/gateway convention.
//
// Reads PORT from env (Cloud Run sets this); defaults to 8080 for local.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/cmdutil"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "hello from canary-rapidpos drop zone")
	})

	addr := ":" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("hello starting on %s", ln.Addr().String())
	srv := &http.Server{Handler: mux}
	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}
// touch
