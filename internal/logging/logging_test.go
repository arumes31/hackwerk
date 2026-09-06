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

func TestLoggerHonorsTextFormatAndConfiguredLevels(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		level     string
		log       func(*slog.Logger)
		want      string
		forbidden string
	}{
		{
			name: "debug is enabled", format: "text", level: "debug",
			log: func(logger *slog.Logger) { logger.Debug("debug event") }, want: "debug event",
		},
		{
			name: "warning suppresses debug", format: "text", level: "warn",
			log: func(logger *slog.Logger) { logger.Debug("debug event"); logger.Warn("warning event") }, want: "warning event", forbidden: "debug event",
		},
		{
			name: "error suppresses warning", format: "text", level: "error",
			log: func(logger *slog.Logger) { logger.Warn("warning event"); logger.Error("error event") }, want: "error event", forbidden: "warning event",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := New(config.Config{LogFormat: test.format, LogLevel: test.level}, &output, "serve", "test")
			test.log(logger)
			value := output.String()
			if !strings.Contains(value, test.want) || test.forbidden != "" && strings.Contains(value, test.forbidden) {
				t.Fatalf("log output = %q, want %q and no %q", value, test.want, test.forbidden)
			}
		})
	}
}

func TestLoggerRedactsNestedAttributesAndProducesStableEventNames(t *testing.T) {
	var output bytes.Buffer
	logger := New(config.Config{LogFormat: "json", LogLevel: "info"}, &output, "serve", "test").WithGroup("request")
	logger.Info("  Anmeldung fehlgeschlagen!  ",
		slog.Group("details", slog.String("email", "kunde@example.test"), slog.String("comment", "token /confirm/abcdefghijklmnopqrstuvwxyz012345")),
		slog.Any("attempt", 3),
	)
	value := output.String()
	for _, forbidden := range []string{"kunde@example.test", "abcdefghijklmnopqrstuvwxyz012345"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("nested log contains %q: %s", forbidden, value)
		}
	}
	if !strings.Contains(value, `"event":"anmeldung_fehlgeschlagen"`) || !strings.Contains(value, "[REDACTED]") {
		t.Fatalf("unexpected nested log: %s", value)
	}
	attribute := sanitizeAttribute(slog.Group("details", slog.String("password", "secret"), slog.String("summary", "user@example.test")))
	children := attribute.Value.Group()
	if len(children) != 2 || children[0].Value.String() != "[REDACTED]" || children[1].Value.String() != "[REDACTED-EMAIL]" {
		t.Fatalf("sanitizeAttribute() = %#v", attribute)
	}
	if eventName(" ") != "log_event" || eventName(strings.Repeat("x", 81)) != "log_event" {
		t.Fatal("eventName() did not bound empty or oversized event names")
	}
}

type assertError string

func (err assertError) Error() string { return string(err) }
