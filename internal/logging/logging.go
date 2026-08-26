// Package logging constructs HackWerk's structured logger.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"example.invalid/hackplan/internal/config"
)

// New creates a structured logger without logging configuration values.
func New(cfg config.Config, output io.Writer, service string, version string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(output, options)
	} else {
		handler = slog.NewJSONHandler(output, options)
	}
	return slog.New(redactingHandler{next: handler}).With(
		slog.String("service", service),
		slog.String("version", version),
	)
}

type redactingHandler struct{ next slog.Handler }

func (handler redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, sanitizeText(record.Message), record.PC)
	clean.AddAttrs(slog.String("event", eventName(record.Message)))
	record.Attrs(func(attribute slog.Attr) bool { clean.AddAttrs(sanitizeAttribute(attribute)); return true })
	return handler.next.Handle(ctx, clean)
}

func (handler redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attributes))
	for _, attribute := range attributes {
		clean = append(clean, sanitizeAttribute(attribute))
	}
	return redactingHandler{next: handler.next.WithAttrs(clean)}
}

func (handler redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{next: handler.next.WithGroup(name)}
}

var (
	emailPattern     = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9.-]+\.[a-z]{2,}\b`)
	phonePattern     = regexp.MustCompile(`\+?\d(?:[ ()-]*\d){7,}`)
	tokenPathPattern = regexp.MustCompile(`(?i)/(feeds|confirm|confirmation)/[A-Za-z0-9_-]{20,}`)
	hexSecretPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{32,}\b`)
)

func sanitizeAttribute(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if sensitiveLogKey(attribute.Key) {
		return slog.String(attribute.Key, "[REDACTED]")
	}
	if attribute.Value.Kind() == slog.KindGroup {
		children := attribute.Value.Group()
		for index := range children {
			children[index] = sanitizeAttribute(children[index])
		}
		return slog.Group(attribute.Key, childrenToAny(children)...)
	}
	if attribute.Value.Kind() == slog.KindString {
		return slog.String(attribute.Key, sanitizeText(attribute.Value.String()))
	}
	if attribute.Value.Kind() == slog.KindAny {
		if err, ok := attribute.Value.Any().(error); ok {
			return slog.String(attribute.Key+"_type", fmt.Sprintf("%T", err))
		}
	}
	return attribute
}

func childrenToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}
	return values
}

func sensitiveLogKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{"password", "passwd", "secret", "token", "cookie", "authorization", "recipient", "phone", "email", "transcript", "audio", "api_key", "database_url", "request_body", "message_text"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func sanitizeText(value string) string {
	value = emailPattern.ReplaceAllString(value, "[REDACTED-EMAIL]")
	value = phonePattern.ReplaceAllString(value, "[REDACTED-PHONE]")
	value = tokenPathPattern.ReplaceAllString(value, "/$1/[REDACTED]")
	return hexSecretPattern.ReplaceAllString(value, "[REDACTED-SECRET]")
}

func eventName(message string) string {
	message = strings.ToLower(strings.TrimSpace(message))
	message = strings.Map(func(value rune) rune {
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
			return value
		}
		return '_'
	}, message)
	message = strings.Trim(message, "_")
	if message == "" || len(message) > 80 {
		return "log_event"
	}
	return message
}
