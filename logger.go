package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/tayyebi/gig/config"
)

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr})
	return slog.New(h).With("service", "gig", "role", cfg.AppRole)
}

// redactedFieldSubstrings flags a log attribute key for redaction if its
// lowercased name contains any of these substrings. This is deliberately
// broad (PLAN.md section 17: "Redact payment and identity data from logs")
// so a new field named e.g. "card_number" or "wallet_address" is redacted
// by default rather than needing an explicit allowlist entry per call site.
var redactedFieldSubstrings = []string{
	"password", "secret", "token", "session", "csrf",
	"card", "cvv", "cvc", "iban", "account_number", "routing_number",
	"ssn", "tax_id", "passport", "email",
	"wallet_address", "private_key", "seed_phrase", "mnemonic",
	"webhook_secret", "api_key", "authorization",
}

// redactAttr is a slog.HandlerOptions.ReplaceAttr that masks the value of
// any attribute whose key matches redactedFieldSubstrings, at any nesting
// depth (slog.Group). It never drops the key, so the shape of the log line
// stays stable for downstream parsing; only the value is replaced.
func redactAttr(groups []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	for _, needle := range redactedFieldSubstrings {
		if strings.Contains(key, needle) {
			a.Value = slog.StringValue("[redacted]")
			return a
		}
	}
	return a
}

type loggerKey struct{}

func withLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
