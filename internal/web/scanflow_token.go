package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	scanFlowTokenVersion = 1
	defaultScanFlowTTL   = 15 * time.Minute
)

var (
	errScanFlowInvalid = errors.New("scan flow token invalid")
	errScanFlowExpired = errors.New("scan flow token expired")
)

type scanFlowTokenCodec struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

type scanFlowState struct {
	Version       int                   `json:"v"`
	TenantID      string                `json:"tenant_id"`
	Barcode       string                `json:"barcode"`
	Source        string                `json:"source,omitempty"`
	Confidence    float64               `json:"confidence,omitempty"`
	PartialFields []string              `json:"partial_fields,omitempty"`
	Product       scanProductFields     `json:"product"`
	Operational   scanOperationalFields `json:"operational"`
	IssuedAt      int64                 `json:"iat"`
	ExpiresAt     int64                 `json:"exp"`
}

type scanProductFields struct {
	Name               string `json:"name,omitempty"`
	Brand              string `json:"brand,omitempty"`
	Size               string `json:"size,omitempty"`
	ImageURL           string `json:"image_url,omitempty"`
	CategorySuggestion string `json:"category_suggestion,omitempty"`
}

type scanOperationalFields struct {
	SKU           string `json:"sku,omitempty"`
	CategoryID    string `json:"category_id,omitempty"`
	VendorID      string `json:"vendor_id,omitempty"`
	UnitOfMeasure string `json:"unit_of_measure,omitempty"`
	UnitCost      string `json:"unit_cost,omitempty"`
	SellingPrice  string `json:"selling_price,omitempty"`
	CasePack      string `json:"case_pack,omitempty"`
	Status        string `json:"status,omitempty"`
}

func newScanFlowTokenCodec(secret []byte) *scanFlowTokenCodec {
	return &scanFlowTokenCodec{
		secret: append([]byte(nil), secret...),
		now:    func() time.Time { return time.Now().UTC() },
		ttl:    defaultScanFlowTTL,
	}
}

func (c *scanFlowTokenCodec) Encode(tenantID uuid.UUID, state scanFlowState) (string, error) {
	if len(c.secret) < 32 {
		return "", fmt.Errorf("%w: short secret", errScanFlowInvalid)
	}
	now := c.now().UTC()
	if state.Version == 0 {
		state.Version = scanFlowTokenVersion
	}
	state.TenantID = tenantID.String()
	state.IssuedAt = now.Unix()
	state.ExpiresAt = now.Add(c.ttl).Unix()

	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("scan flow token marshal: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := c.sign(encodedPayload)
	return encodedPayload + "." + signature, nil
}

func (c *scanFlowTokenCodec) Decode(token string, tenantID uuid.UUID) (scanFlowState, error) {
	if len(c.secret) < 32 {
		return scanFlowState{}, fmt.Errorf("%w: short secret", errScanFlowInvalid)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return scanFlowState{}, errScanFlowInvalid
	}
	expected := c.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return scanFlowState{}, errScanFlowInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return scanFlowState{}, fmt.Errorf("%w: payload decode", errScanFlowInvalid)
	}
	var state scanFlowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return scanFlowState{}, fmt.Errorf("%w: payload json", errScanFlowInvalid)
	}
	if state.Version != scanFlowTokenVersion {
		return scanFlowState{}, errScanFlowInvalid
	}
	if state.TenantID != tenantID.String() {
		return scanFlowState{}, errScanFlowInvalid
	}
	if state.ExpiresAt < c.now().UTC().Unix() {
		return scanFlowState{}, errScanFlowExpired
	}
	return state, nil
}

func (c *scanFlowTokenCodec) sign(encodedPayload string) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
