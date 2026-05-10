package barcodelookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// upcitemdbBaseURL is the public trial endpoint. Production deployments
// switch to the paid plan via NewUPCItemDBWithBaseURL.
const upcitemdbBaseURL = "https://api.upcitemdb.com"

// upcitemdbConfidence is intentionally lower than Open Food Facts: UPC
// Item DB has broad coverage but lower fidelity (user-submitted titles,
// inconsistent brand normalization). Confidence 0.65 means it loses to
// any of the other three configured sources.
const upcitemdbConfidence = 0.65

// upcitemdbName is the stable label used in Result.Source and logs.
const upcitemdbName = "UPC Item DB"

// ErrUPCItemDBRateLimited surfaces an HTTP 429 from the trial endpoint
// without crashing the resolver. Wraps a plain error so callers can use
// errors.Is to distinguish "the service is throttling us" from a real
// transport failure.
var ErrUPCItemDBRateLimited = errors.New("upcitemdb: rate limited")

// UPCItemDBSource implements Source against the UPC Item DB REST API.
type UPCItemDBSource struct {
	httpClient *http.Client
	baseURL    string
}

// NewUPCItemDB returns a Source bound to the supplied http.Client. Pass
// nil to use http.DefaultClient.
func NewUPCItemDB(client *http.Client) *UPCItemDBSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &UPCItemDBSource{httpClient: client, baseURL: upcitemdbBaseURL}
}

// NewUPCItemDBWithBaseURL is the test-friendly constructor that targets
// a custom base URL.
func NewUPCItemDBWithBaseURL(client *http.Client, baseURL string) *UPCItemDBSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &UPCItemDBSource{httpClient: client, baseURL: baseURL}
}

// Name returns the stable source label.
func (s *UPCItemDBSource) Name() string { return upcitemdbName }

// upcitemdbResponse mirrors the shape we care about from the UPC Item
// DB lookup endpoint.
type upcitemdbResponse struct {
	Code  string         `json:"code"`
	Items []upcitemdbRow `json:"items"`
}

type upcitemdbRow struct {
	Title       string   `json:"title"`
	Brand       string   `json:"brand"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Images      []string `json:"images"`
}

// Lookup hits {base}/prod/trial/lookup?upc={barcode} and maps the
// response into a Result. Returns:
//
//   - ErrBarcodeNotFound        on code=INVALID_UPC or empty items[]
//   - ErrUPCItemDBRateLimited   on HTTP 429
//   - a wrapped error           on transport / decode failures
func (s *UPCItemDBSource) Lookup(ctx context.Context, barcode string) (Result, error) {
	start := time.Now()

	endpoint := fmt.Sprintf("%s/prod/trial/lookup?upc=%s", s.baseURL, url.QueryEscape(barcode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, fmt.Errorf("upcitemdb: build request: %w", err)
	}
	req.Header.Set("User-Agent", "canary-go/barcodelookup (+https://canary.go)")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("upcitemdb: http: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return Result{}, ErrUPCItemDBRateLimited
	case resp.StatusCode == http.StatusNotFound:
		return Result{}, ErrBarcodeNotFound
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Result{}, fmt.Errorf("upcitemdb: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload upcitemdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("upcitemdb: decode: %w", err)
	}

	if payload.Code != "OK" || len(payload.Items) == 0 {
		return Result{}, ErrBarcodeNotFound
	}

	row := payload.Items[0]
	fields := map[string]any{}
	var partial []string

	if v := strings.TrimSpace(row.Title); v != "" {
		fields["name"] = v
	} else {
		partial = append(partial, "name")
	}
	if v := strings.TrimSpace(row.Brand); v != "" {
		fields["brand"] = v
	} else {
		partial = append(partial, "brand")
	}
	if v := strings.TrimSpace(row.Description); v != "" {
		fields["description"] = v
	}
	if v := strings.TrimSpace(row.Category); v != "" {
		fields["category"] = v
	}
	if len(row.Images) > 0 {
		if v := strings.TrimSpace(row.Images[0]); v != "" {
			fields["image_url"] = v
		}
	}

	return Result{
		Source:        upcitemdbName,
		Confidence:    upcitemdbConfidence,
		Fields:        fields,
		PartialFields: partial,
		Latency:       time.Since(start),
	}, nil
}
