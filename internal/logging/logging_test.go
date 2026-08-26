package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/config"
)

func TestLoggerRedactsPIISecretsAndTokenPaths(t *testing.T) {
	var output bytes.Buffer
	logger := New(config.Config{LogFormat: "json", LogLevel: "info"}, &output, "serve", "test")
	logger.Error("provider failed for canary@example.test +43 699 12345678 /feeds/abcdefghijklmnopqrstuvwxyz012345/calendar.ics", slog.String("api_key", "not-for-logs"), slog.Any("error", assertError("raw provider body")))
	value := output.String()
	for _, forbidden := range []string{"canary@example.test", "699 12345678", "abcdefghijklmnopqrstuvwxyz012345", "not-for-logs", "raw provider body"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("log contains forbidden canary %q: %s", forbidden, value)
		}
	}
	for _, required := range []string{`"service":"serve"`, `"event":`, "REDACTED"} {
		if !strings.Contains(value, required) {
			t.Fatalf("log missing %q: %s", required, value)
		}
	}
}

type assertError string

func (err assertError) Error() string { return string(err) }
