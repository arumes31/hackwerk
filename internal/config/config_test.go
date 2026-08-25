package config

import (
	"errors"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		values      map[string]string
		expectedEnv string
		expectError string
	}{
		{name: "development defaults", values: map[string]string{}, expectedEnv: EnvironmentDevelopment},
		{name: "test environment", values: map[string]string{"APP_ENV": "test"}, expectedEnv: EnvironmentTest},
		{name: "invalid pool", values: map[string]string{"DATABASE_MIN_CONNS": "5", "DATABASE_MAX_CONNS": "2"}, expectError: "pool"},
		{name: "invalid duration", values: map[string]string{"APP_SHUTDOWN_TIMEOUT": "later"}, expectError: "duration"},
		{name: "production requires https", values: map[string]string{"APP_ENV": "production"}, expectError: "https"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(name string) string { return tt.values[name] }
			cfg, err := load(getenv, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
			if tt.expectError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.expectError) {
					t.Fatalf("load() error = %v, want containing %q", err, tt.expectError)
				}
				return
			}
			if err != nil {
				t.Fatalf("load() error = %v", err)
			}
			if cfg.Environment != tt.expectedEnv {
				t.Fatalf("Environment = %q, want %q", cfg.Environment, tt.expectedEnv)
			}
			if cfg.Database.URL == "" {
				t.Fatal("Database.URL is empty")
			}
			if tt.name == "development defaults" && cfg.AppName != "HackWerk" {
				t.Fatalf("AppName = %q, want HackWerk", cfg.AppName)
			}
		})
	}
}

func TestLoadSecretFile(t *testing.T) {
	t.Parallel()

	getenv := func(name string) string {
		if name == "DATABASE_URL_FILE" {
			return "/run/secrets/database_url"
		}
		return ""
	}
	cfg, err := load(getenv, func(path string) ([]byte, error) {
		if path != "/run/secrets/database_url" {
			t.Fatalf("path = %q", path)
		}
		return []byte("postgres://secret\n"), nil
	})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Database.URL != "postgres://secret" {
		t.Fatalf("Database.URL = %q", cfg.Database.URL)
	}
}
