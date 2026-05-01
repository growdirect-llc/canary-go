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
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

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
	log.Printf("hello starting on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
