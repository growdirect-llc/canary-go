// Package routewalk enumerates every mounted route on a chi router and
// emits a structured JSON inventory.
//
// At gateway boot, when MANIFEST_ROUTEWALK=1 is set, main.go calls
// routewalk.Walk(r, "gateway", "") to write build/routes-seen.json. That
// file is consumed by services/canary-protocol/manifest/gen/reconcile.py
// to compare the live router against manifest.yaml + openapi.yaml and
// produce the drift report.
//
// This package is observation-only. It must never change runtime behavior;
// it runs after route mounting completes and writes a single file.
//
// Phase 1 / GRO-837 sub-task T1.2 of the sysadmin module epic (GRO-836).
package routewalk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"

	"github.com/go-chi/chi/v5"
)

// Route is one mounted (method, path) tuple.
type Route struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler,omitempty"`
}

// Output is the top-level JSON document. The reconcile script consumes
// this shape verbatim — keep field names stable.
//
// Deliberately no GeneratedAt field — gateway boot runs frequently in dev
// and a wall-clock timestamp churned the file on every restart for no
// reproducibility benefit. The output is content-addressable from the
// (sorted) route list itself.
type Output struct {
	Service string  `json:"service"`
	Count   int     `json:"count"`
	Routes  []Route `json:"routes"`
}

// DefaultOutPath is where Walk writes when no path is provided.
const DefaultOutPath = "build/routes-seen.json"

// Walk enumerates all routes on r and writes the inventory to outPath.
// service is recorded in the output for cross-service merging if the
// reconciler ever needs to consume multiple route-walk dumps.
//
// If outPath is empty, DefaultOutPath is used.
func Walk(r chi.Routes, service, outPath string) error {
	if outPath == "" {
		outPath = DefaultOutPath
	}

	var routes []Route
	walkFn := func(method, path string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, Route{
			Method:  method,
			Path:    path,
			Handler: handlerName(handler),
		})
		return nil
	}
	if err := chi.Walk(r, walkFn); err != nil {
		return fmt.Errorf("chi.Walk: %w", err)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})

	out := Output{
		Service: service,
		Count:   len(routes),
		Routes:  routes,
	}

	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

// handlerName returns the function name of an http.Handler when it's a
// HandlerFunc, or the type name otherwise. Best-effort identification —
// chi adapter wrappers may produce opaque names; the path+method are the
// load-bearing identifiers, this is just a hint.
func handlerName(h http.Handler) string {
	if h == nil {
		return ""
	}
	v := reflect.ValueOf(h)
	if v.Kind() == reflect.Func {
		if fn := runtime.FuncForPC(v.Pointer()); fn != nil {
			return fn.Name()
		}
		return "func"
	}
	t := reflect.TypeOf(h)
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.String()
}
