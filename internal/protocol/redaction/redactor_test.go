package redaction

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedactor_StripsCommonPII is the GRO-952 acceptance probe for
// the redaction substrate. A payload with PII / payment / employee
// fields run through SquarePolicy MUST emit no trace of the
// sensitive values — neither as allowlisted keys nor as substring
// matches anywhere in the output.
//
// Multi-assert:
//
//   1. Allowlisted fields survive verbatim (event_id, merchant_id).
//   2. PII shapes never appear in the output bytes:
//      email, phone, ssn, dob, customer name, address fields,
//      card_number, cvv, account_number, routing_number, pan.
//   3. The output is itself valid JSON (downstream pipeline can parse).
//
// Fails with no policy because the redactor would either error or
// emit identity output. The test pins SquarePolicy specifically so
// drift in the allowlist (e.g. someone allowlists "data.object" to
// fix a bug elsewhere) shows up as a probe failure.
func TestRedactor_StripsCommonPII(t *testing.T) {
	r := New(SquarePolicy)

	payload := []byte(`{
		"event_id": "evt_abc",
		"merchant_id": "M123",
		"type": "payment.created",
		"data": {
			"id": "pmt_42",
			"type": "payment",
			"object": {
				"id": "obj_42",
				"customer": {
					"name": "Jane Smith",
					"email": "jane@example.com",
					"phone": "+15551234567",
					"ssn": "123-45-6789",
					"dob": "1985-06-15",
					"address": {"line1": "123 Main St", "zip": "94110"}
				},
				"payment": {
					"card_number": "4111111111111111",
					"cvv": "123",
					"pan": "4111-1111-1111-1111"
				},
				"employee": {
					"name": "Bob Cashier",
					"ssn": "987-65-4321"
				},
				"bank": {
					"account_number": "000123456789",
					"routing_number": "021000021"
				}
			}
		}
	}`)

	out, err := r.Redact(payload)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}

	// Allowlisted fields survive.
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (out=%s)", err, string(out))
	}
	if got["event_id"] != "evt_abc" {
		t.Errorf("event_id should survive: got %v", got["event_id"])
	}
	if got["merchant_id"] != "M123" {
		t.Errorf("merchant_id should survive: got %v", got["merchant_id"])
	}
	if got["type"] != "payment.created" {
		t.Errorf("type should survive: got %v", got["type"])
	}

	// PII / payment / employee fields stripped — assert none of the
	// raw values appear anywhere in the serialized output. Substring
	// match on the bytes is the strictest possible check.
	bannedValues := []string{
		"jane@example.com",
		"15551234567", "5551234567",
		"123-45-6789", "987-65-4321",
		"1985-06-15",
		"Jane Smith", "Bob Cashier",
		"123 Main St", "94110",
		"4111111111111111", "4111-1111-1111-1111",
		"000123456789", "021000021",
	}
	body := string(out)
	for _, banned := range bannedValues {
		if strings.Contains(body, banned) {
			t.Errorf("banned value %q leaked into redacted output: %s", banned, body)
		}
	}

	// Allowlisted nested keys also survive: data.id, data.type, data.object.id.
	dataMap, _ := got["data"].(map[string]any)
	if dataMap["id"] != "pmt_42" {
		t.Errorf("data.id should survive: got %v", dataMap["id"])
	}
	if obj, _ := dataMap["object"].(map[string]any); obj["id"] != "obj_42" {
		t.Errorf("data.object.id should survive: got %v", obj["id"])
	}
}

// TestRedactor_DefaultDeny verifies the conservative-by-default
// behavior: an unknown source's payload is reduced to {} (every key
// dropped) — the protocol would rather lose signal than persist
// unclassified PII.
func TestRedactor_DefaultDeny(t *testing.T) {
	r := New(DefaultDenyPolicy)
	out, err := r.Redact([]byte(`{"any":"thing","customer":{"email":"a@b.com"}}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if string(out) != "{}" {
		t.Errorf("default-deny should empty the object; got %s", string(out))
	}
}

// TestRedactor_ModeReplace verifies the alternative mode: keys
// remain in the shape but values are replaced with the sentinel.
// Useful when downstream consumers expect a stable schema.
func TestRedactor_ModeReplace(t *testing.T) {
	p := Policy{
		SourceCode: "test",
		Mode:       ModeReplace,
		Allowlist:  []string{"keep"},
	}
	r := New(p)
	out, err := r.Redact([]byte(`{"keep":"yes","drop":"hidden"}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["keep"] != "yes" {
		t.Errorf("kept field changed: %v", got["keep"])
	}
	if got["drop"] != RedactionToken {
		t.Errorf("dropped field should be %q, got %v", RedactionToken, got["drop"])
	}
}

// TestRedactor_WildcardMatchesArrayElements verifies that "items.*.sku"
// allowlist entries permit arbitrary array index. Without wildcard
// support, every items[N] would have to be enumerated.
func TestRedactor_WildcardMatchesArrayElements(t *testing.T) {
	p := Policy{
		SourceCode: "test",
		Mode:       ModeDrop,
		Allowlist:  []string{"items.*.sku"},
	}
	r := New(p)
	out, err := r.Redact([]byte(`{
		"items": [
			{"sku": "ABC", "price": 9.99, "customer_name": "Jane"},
			{"sku": "XYZ", "price": 4.99, "customer_name": "Bob"}
		]
	}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, `"sku":"ABC"`) || !strings.Contains(body, `"sku":"XYZ"`) {
		t.Errorf("sku values should survive both array elements; got %s", body)
	}
	if strings.Contains(body, "Jane") || strings.Contains(body, "Bob") {
		t.Errorf("customer_name values should not leak: %s", body)
	}
	if strings.Contains(body, "9.99") || strings.Contains(body, "4.99") {
		t.Errorf("price values should not leak (not allowlisted): %s", body)
	}
}

// TestRedactor_InvalidJSON returns ErrInvalidJSON so callers can
// distinguish "input was malformed" from "redactor configuration
// produced an empty result".
func TestRedactor_InvalidJSON(t *testing.T) {
	r := New(SquarePolicy)
	_, err := r.Redact([]byte(`not json`))
	if err != ErrInvalidJSON {
		t.Errorf("expected ErrInvalidJSON, got %v", err)
	}
}

// TestRedactor_EmptyPayload short-circuits to empty output without
// error. Lets callers pass through nil/empty bodies (e.g. health
// pings) without special-casing.
func TestRedactor_EmptyPayload(t *testing.T) {
	r := New(SquarePolicy)
	out, err := r.Redact(nil)
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %s", string(out))
	}
}
