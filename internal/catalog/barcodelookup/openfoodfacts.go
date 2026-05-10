package barcodelookup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// openFoodFactsBaseURL is the public REST endpoint. Adapters call
// {base}/api/v0/product/{barcode}.json. Overridable via NewWithBaseURL
// for tests.
const openFoodFactsBaseURL = "https://world.openfoodfacts.org"

// openFoodFactsConfidence reflects how strict the source's own curation
// is. OFF is well-curated for food but coverage is narrow — confidence
// 0.85 lets it lose to canarynetwork (0.99) / gs1 (0.95) when those are
// configured, but beat upcitemdb (0.65).
const openFoodFactsConfidence = 0.85

// openFoodFactsName is the stable label used for logs, metrics and the
// Result.Source field.
const openFoodFactsName = "Open Food Facts"

// OpenFoodFactsSource implements Source against the Open Food Facts
// public REST API.
type OpenFoodFactsSource struct {
	httpClient *http.Client
	baseURL    string
}

// NewOpenFoodFacts returns a Source bound to the supplied http.Client.
// Pass nil to use http.DefaultClient.
func NewOpenFoodFacts(client *http.Client) *OpenFoodFactsSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenFoodFactsSource{httpClient: client, baseURL: openFoodFactsBaseURL}
}

// NewOpenFoodFactsWithBaseURL is the test-friendly constructor that
// targets a custom base URL (typically an httptest.NewServer URL).
func NewOpenFoodFactsWithBaseURL(client *http.Client, baseURL string) *OpenFoodFactsSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenFoodFactsSource{httpClient: client, baseURL: baseURL}
}

// Name returns the stable source label.
func (s *OpenFoodFactsSource) Name() string { return openFoodFactsName }

// openFoodFactsResponse mirrors the shape we care about from the OFF
// product endpoint. Fields we don't consume are omitted; encoding/json
// silently drops them.
type openFoodFactsResponse struct {
	Status  int `json:"status"` // 1 = found, 0 = not found
	Product struct {
		ProductName string `json:"product_name"`
		Brands      string `json:"brands"`
		ImageURL    string `json:"image_url"`
		Allergens   string `json:"allergens"`
	} `json:"product"`
}

// Lookup hits {base}/api/v0/product/{barcode}.json and maps the
// response into a Result. Returns ErrBarcodeNotFound when the API
// reports status=0 (or the HTTP layer 404s); other transport / decode
// failures are returned wrapped.
func (s *OpenFoodFactsSource) Lookup(ctx context.Context, barcode string) (Result, error) {
	start := time.Now()

	endpoint := fmt.Sprintf("%s/api/v0/product/%s.json", s.baseURL, url.PathEscape(barcode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, fmt.Errorf("openfoodfacts: build request: %w", err)
	}
	req.Header.Set("User-Agent", "canary-go/barcodelookup (+https://canary.go)")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("openfoodfacts: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Result{}, ErrBarcodeNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a small amount of the body to keep the error helpful
		// without buffering arbitrarily-large error pages.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("openfoodfacts: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload openFoodFactsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("openfoodfacts: decode: %w", err)
	}

	if payload.Status != 1 {
		return Result{}, ErrBarcodeNotFound
	}

	fields := map[string]any{}
	var partial []string

	if v := strings.TrimSpace(payload.Product.ProductName); v != "" {
		fields["name"] = v
	} else {
		partial = append(partial, "name")
	}
	if v := strings.TrimSpace(payload.Product.Brands); v != "" {
		fields["brand"] = v
	} else {
		partial = append(partial, "brand")
	}
	if v := strings.TrimSpace(payload.Product.ImageURL); v != "" {
		fields["image_url"] = v
	}
	if v := strings.TrimSpace(payload.Product.Allergens); v != "" {
		fields["allergens"] = v
	}

	return Result{
		Source:        openFoodFactsName,
		Confidence:    openFoodFactsConfidence,
		Fields:        fields,
		PartialFields: partial,
		Latency:       time.Since(start),
	}, nil
}
