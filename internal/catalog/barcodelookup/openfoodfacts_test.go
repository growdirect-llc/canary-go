package barcodelookup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenFoodFacts_Happy(t *testing.T) {
	t.Parallel()

	const body = `{
		"status": 1,
		"product": {
			"product_name": "Organic Almonds",
			"brands": "Acme Farms",
			"image_url": "https://images.example/almonds.jpg",
			"allergens": "en:nuts"
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v0/product/0123456789012.json") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	src := NewOpenFoodFactsWithBaseURL(srv.Client(), srv.URL)
	got, err := src.Lookup(context.Background(), "0123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != openFoodFactsName {
		t.Errorf("Source = %q, want %q", got.Source, openFoodFactsName)
	}
	if got.Confidence != openFoodFactsConfidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, openFoodFactsConfidence)
	}
	if got.Fields["name"] != "Organic Almonds" {
		t.Errorf("Fields[name] = %v, want Organic Almonds", got.Fields["name"])
	}
	if got.Fields["brand"] != "Acme Farms" {
		t.Errorf("Fields[brand] = %v, want Acme Farms", got.Fields["brand"])
	}
	if got.Fields["image_url"] != "https://images.example/almonds.jpg" {
		t.Errorf("Fields[image_url] = %v", got.Fields["image_url"])
	}
	if got.Fields["allergens"] != "en:nuts" {
		t.Errorf("Fields[allergens] = %v", got.Fields["allergens"])
	}
	if len(got.PartialFields) != 0 {
		t.Errorf("PartialFields = %v, want empty", got.PartialFields)
	}
	if got.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0", got.Latency)
	}
}

func TestOpenFoodFacts_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":0,"product":{}}`))
	}))
	defer srv.Close()

	src := NewOpenFoodFactsWithBaseURL(srv.Client(), srv.URL)
	_, err := src.Lookup(context.Background(), "0000000000000")
	if !errors.Is(err, ErrBarcodeNotFound) {
		t.Fatalf("err = %v, want ErrBarcodeNotFound", err)
	}
}

func TestOpenFoodFacts_MalformedResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`<html>not json</html>`))
	}))
	defer srv.Close()

	src := NewOpenFoodFactsWithBaseURL(srv.Client(), srv.URL)
	_, err := src.Lookup(context.Background(), "0123456789012")
	if err == nil {
		t.Fatal("err = nil, want decode error")
	}
	if errors.Is(err, ErrBarcodeNotFound) {
		t.Fatalf("err = %v, must not be ErrBarcodeNotFound for malformed JSON", err)
	}
}

func TestOpenFoodFacts_PartialFields(t *testing.T) {
	t.Parallel()

	// status=1 but missing brand and name → both should appear in
	// PartialFields so the UI can flag the gap.
	const body = `{"status":1,"product":{"image_url":"https://x"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	src := NewOpenFoodFactsWithBaseURL(srv.Client(), srv.URL)
	got, err := src.Lookup(context.Background(), "0123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.PartialFields) != 2 {
		t.Errorf("PartialFields = %v, want 2 entries (name, brand)", got.PartialFields)
	}
}
