package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestRedactAttrMasksSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: redactAttr})
	log := slog.New(h)
	log.Info("login attempt",
		"password", "hunter2",
		"stripe_webhook_secret", "whsec_abc",
		"email", "buyer@example.com",
		"order_id", 42,
	)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	for _, key := range []string{"password", "stripe_webhook_secret", "email"} {
		if line[key] != "[redacted]" {
			t.Errorf("%s = %v, want [redacted]", key, line[key])
		}
	}
	if line["order_id"] != float64(42) {
		t.Errorf("order_id = %v, want 42 (non-sensitive fields must pass through)", line["order_id"])
	}
}
