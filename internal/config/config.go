// Package config loads and validates HackWerk's startup configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentTest        = "test"
	EnvironmentProduction  = "production"
)

// Config contains the startup settings shared by serve, worker, and CLI modes.
type Config struct {
	Environment     string
	AppName         string
	BaseURL         string
	ListenAddr      string
	Timezone        string
	Locale          string
	LogLevel        string
	LogFormat       string
	ShutdownTimeout time.Duration
	HTTP            HTTP
	Database        Database
	Auth            Auth
}

// HTTP contains bounded server timeouts and header limits.
type HTTP struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// Database contains connection details. URL must never be logged.
type Database struct {
	URL              string
	MaxConnections   int32
	MinConnections   int32
	ConnectTimeout   time.Duration
	ReadinessTimeout time.Duration
}

// Auth contains password, session, cookie, and login protection settings.
type Auth struct {
	SessionCookieName   string
	CSRFCookieName      string
	SessionIdleTTL      time.Duration
	SessionAbsoluteTTL  time.Duration
	CookieSecure        bool
	PasswordMinLength   int
	Argon2MemoryKiB     uint32
	Argon2Iterations    uint32
	Argon2Parallelism   uint8
	LoginLimitPerMinute int
}

// Load reads process environment variables and validates the complete result.
func Load() (Config, error) {
	return load(os.Getenv, os.ReadFile)
}

type readFileFunc func(string) ([]byte, error)

func load(getenv func(string) string, readFile readFileFunc) (Config, error) {
	databaseURL, err := secretValue(getenv, readFile, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	if databaseURL == "" {
		databaseURL = "postgres://hackplan_app:development-only@postgres:5432/hackplan?sslmode=disable"
	}

	cfg := Config{
		Environment:     valueOrDefault(getenv("APP_ENV"), EnvironmentDevelopment),
		AppName:         valueOrDefault(getenv("APP_NAME"), "HackWerk"),
		BaseURL:         valueOrDefault(getenv("APP_BASE_URL"), "http://localhost:18533"),
		ListenAddr:      valueOrDefault(getenv("APP_LISTEN_ADDR"), ":18533"),
		Timezone:        valueOrDefault(getenv("APP_TIMEZONE"), "Europe/Vienna"),
		Locale:          valueOrDefault(getenv("APP_LOCALE"), "de-AT"),
		LogLevel:        valueOrDefault(getenv("APP_LOG_LEVEL"), "info"),
		LogFormat:       valueOrDefault(getenv("APP_LOG_FORMAT"), "json"),
		ShutdownTimeout: 20 * time.Second,
		HTTP: HTTP{
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		Database: Database{
			URL:              databaseURL,
			MaxConnections:   20,
			MinConnections:   2,
			ConnectTimeout:   5 * time.Second,
			ReadinessTimeout: 2 * time.Second,
		},
		Auth: Auth{
			SessionCookieName:   "hackplan_session",
			CSRFCookieName:      "hackplan_csrf",
			SessionIdleTTL:      8 * time.Hour,
			SessionAbsoluteTTL:  24 * time.Hour,
			CookieSecure:        false,
			PasswordMinLength:   14,
			Argon2MemoryKiB:     64 * 1024,
			Argon2Iterations:    3,
			Argon2Parallelism:   2,
			LoginLimitPerMinute: 10,
		},
	}

	if err := applyOverrides(&cfg, getenv); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyOverrides(cfg *Config, getenv func(string) string) error {
	var err error
	if cfg.ShutdownTimeout, err = durationValue(getenv, "APP_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	if cfg.Database.ConnectTimeout, err = durationValue(getenv, "DATABASE_CONNECT_TIMEOUT", cfg.Database.ConnectTimeout); err != nil {
		return err
	}
	if cfg.Database.ReadinessTimeout, err = durationValue(getenv, "DATABASE_READINESS_TIMEOUT", cfg.Database.ReadinessTimeout); err != nil {
		return err
	}
	if cfg.Database.MaxConnections, err = int32Value(getenv, "DATABASE_MAX_CONNS", cfg.Database.MaxConnections); err != nil {
		return err
	}
	if cfg.Database.MinConnections, err = int32Value(getenv, "DATABASE_MIN_CONNS", cfg.Database.MinConnections); err != nil {
		return err
	}
	if cfg.Auth.SessionIdleTTL, err = durationValue(getenv, "SESSION_IDLE_TTL", cfg.Auth.SessionIdleTTL); err != nil {
		return err
	}
	if cfg.Auth.SessionAbsoluteTTL, err = durationValue(getenv, "SESSION_ABSOLUTE_TTL", cfg.Auth.SessionAbsoluteTTL); err != nil {
		return err
	}
	if cfg.Auth.PasswordMinLength, err = intValue(getenv, "PASSWORD_MIN_LENGTH", cfg.Auth.PasswordMinLength); err != nil {
		return err
	}
	if cfg.Auth.LoginLimitPerMinute, err = intValue(getenv, "LOGIN_RATE_LIMIT_PER_MINUTE", cfg.Auth.LoginLimitPerMinute); err != nil {
		return err
	}
	if value := strings.TrimSpace(getenv("SESSION_COOKIE_SECURE")); value != "" {
		cfg.Auth.CookieSecure, err = strconv.ParseBool(value)
		if err != nil {
			return errors.New("config: invalid boolean for session_cookie_secure")
		}
	}
	return nil
}

// Validate checks startup invariants and rejects insecure production defaults.
func (cfg Config) Validate() error {
	validEnvironment := cfg.Environment == EnvironmentDevelopment ||
		cfg.Environment == EnvironmentTest ||
		cfg.Environment == EnvironmentProduction
	if !validEnvironment {
		return fmt.Errorf("config: invalid app environment %q", cfg.Environment)
	}
	if strings.TrimSpace(cfg.AppName) == "" {
		return errors.New("config: app name is required")
	}
	if cfg.Timezone != "Europe/Vienna" {
		return fmt.Errorf("config: unsupported timezone %q", cfg.Timezone)
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return fmt.Errorf("config: loading timezone: %w", err)
	}
	if cfg.Locale != "de-AT" {
		return fmt.Errorf("config: unsupported locale %q", cfg.Locale)
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return fmt.Errorf("config: invalid log format %q", cfg.LogFormat)
	}
	if cfg.LogLevel != "debug" && cfg.LogLevel != "info" && cfg.LogLevel != "warn" && cfg.LogLevel != "error" {
		return fmt.Errorf("config: invalid log level %q", cfg.LogLevel)
	}
	if cfg.Database.MinConnections < 0 || cfg.Database.MaxConnections < 1 || cfg.Database.MinConnections > cfg.Database.MaxConnections {
		return errors.New("config: invalid database pool limits")
	}
	if strings.TrimSpace(cfg.Database.URL) == "" {
		return errors.New("config: database url is required")
	}
	if cfg.Auth.SessionIdleTTL > cfg.Auth.SessionAbsoluteTTL {
		return errors.New("config: session idle ttl must not exceed absolute ttl")
	}
	if cfg.Auth.PasswordMinLength < 12 || cfg.Auth.PasswordMinLength > 256 {
		return errors.New("config: password minimum length must be between 12 and 256")
	}
	if cfg.Auth.LoginLimitPerMinute < 1 || cfg.Auth.LoginLimitPerMinute > 1000 {
		return errors.New("config: invalid login rate limit")
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil || baseURL.Host == "" {
		return errors.New("config: app base url must be an absolute url")
	}
	if cfg.Environment == EnvironmentProduction {
		if baseURL.Scheme != "https" {
			return errors.New("config: production app base url must use https")
		}
		if strings.Contains(cfg.Database.URL, "development-only") || strings.Contains(cfg.Database.URL, "sslmode=disable") {
			return errors.New("config: production database configuration is insecure")
		}
		if !cfg.Auth.CookieSecure {
			return errors.New("config: production session cookie must be secure")
		}
	}
	return nil
}

func intValue(getenv func(string) string, name string, fallback int) (int, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("config: invalid integer for %s", strings.ToLower(name))
	}
	return parsed, nil
}

func secretValue(getenv func(string) string, readFile readFileFunc, name string) (string, error) {
	direct := strings.TrimSpace(getenv(name))
	path := strings.TrimSpace(getenv(name + "_FILE"))
	if direct != "" && path != "" {
		return "", fmt.Errorf("config: %s and %s_file are mutually exclusive", strings.ToLower(name), strings.ToLower(name))
	}
	if path == "" {
		return direct, nil
	}
	value, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("config: reading %s_file: %w", strings.ToLower(name), err)
	}
	return strings.TrimSpace(string(value)), nil
}

func valueOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func durationValue(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("config: invalid duration for %s", strings.ToLower(name))
	}
	return parsed, nil
}

func int32Value(getenv func(string) string, name string, fallback int32) (int32, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("config: invalid integer for %s", strings.ToLower(name))
	}
	return int32(parsed), nil
}
