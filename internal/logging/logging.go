// Package logging constructs HackWerk's structured logger.
package logging

import (
	"io"
	"log/slog"
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
	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("version", version),
	)
}
