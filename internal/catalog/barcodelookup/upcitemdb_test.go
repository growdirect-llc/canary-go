package barcodelookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUPCItemDB_Happy(t *testing.T) {
	t.Parallel()

	const body = `{
		"code": "OK",
		"items": [{
			"title": "Acme Widget",
			"brand": "Acme",
			"description": "A widget",
			"category": "Tools",
			"images": ["https://images.example/widget.jpg"]
		}]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/prod/trial/lookup") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("upc"); got != "0123456789012" {
			t.Errorf("upc query = %q, want %q", got, "0123456789012")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	src := NewUPCItemDBWithBaseURL(srv.Client(), srv.URL)
	got, err := src.Lookup(context.Background(), "0123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != upcitemdbName {
		t.Errorf("Source = %q, want %q", got.Source, upcitemdbName)
	}
	if got.Confidence != upcitemdbConfidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, upcitemdbConfidence)
	}
	if got.Fields["name"] != "Acme Widget" {
		t.Errorf("Fields[name] = %v", got.Fields["name"])
	}
	if got.Fields["brand"] != "Acme" {
		t.Errorf("Fields[brand] = %v", got.Fields["brand"])
	}
	if got.Fields["description"] != "A widget" {
		t.Errorf("Fields[description] = %v", got.Fields["description"])
	}
	if got.Fields["category"] != "Tools" {
		t.Errorf("Fields[category] = %v", got.Fields["category"])
	}
	if got.Fields["image_url"] != "https://images.example/widget.jpg" {
		t.Errorf("Fields[image_url] = %v", got.Fields["image_url"])
	}
}

func TestUPCItemDB_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"INVALID_UPC","items":[]}`))
	}))
	defer srv.Close()

	src := NewUPCItemDBWithBaseURL(srv.Client(), srv.URL)
	_, err := src.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrBarcodeNotFound) {
		t.Fatalf("err = %v, want ErrBarcodeNotFound", err)
	}
}

func TestUPCItemDB_MalformedResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json-at-all`))
	}))
	defer srv.Close()

	src := NewUPCItemDBWithBaseURL(srv.Client(), srv.URL)
	_, err := src.Lookup(context.Background(), "0123456789012")
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
	if errors.Is(err, ErrBarcodeNotFound) {
		t.Fatalf("err = %v, must not be ErrBarcodeNotFound for malformed JSON", err)
	}
}

func TestUPCItemDB_RateLimitedDoesNotPanic(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	src := NewUPCItemDBWithBaseURL(srv.Client(), srv.URL)
	_, err := src.Lookup(context.Background(), "0123456789012")
	if !errors.Is(err, ErrUPCItemDBRateLimited) {
		t.Fatalf("err = %v, want ErrUPCItemDBRateLimited", err)
	}
}
