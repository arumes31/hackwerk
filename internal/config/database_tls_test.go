package config

import (
	"errors"
	"strings"
	"testing"
)

func clearLibpqEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PGHOST", "PGPORT", "PGSSLMODE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGSSLSNI", "PGSSLNEGOTIATION", "PGSERVICE", "PGSERVICEFILE"} {
		t.Setenv(name, "")
	}
}

//nolint:gosec // Connection strings carry fake test-only passwords to prove redaction.
func TestValidateProductionDatabaseTLSRequiresVerifiedTLSForEveryTCPHost(t *testing.T) {
	clearLibpqEnvironment(t)
	tests := []struct {
		name      string
		url       string
		plaintext []string
		wantError string
	}{
		{name: "verify-full", url: "postgres://app:hunter2@db.example.net:5432/hackwerk?sslmode=verify-full"},
		{name: "system roots imply verify-full", url: "postgresql://app:hunter2@db.example.net/hackwerk?sslrootcert=system"},
		{name: "keyword conninfo verify-full", url: "host=db.example.net dbname=hackwerk sslmode=verify-full"},
		{name: "unix socket", url: "postgres://app@/hackwerk?host=/run/postgresql"},
		{name: "missing sslmode defaults to prefer", url: "postgres://app:hunter2@db.example.net/hackwerk", wantError: "verify-full"},
		{name: "disable", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=disable", wantError: "verify-full"},
		{name: "allow", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=allow", wantError: "verify-full"},
		{name: "require skips verification", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=require", wantError: "verify-full"},
		{name: "verify-ca skips host name", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=verify-ca", wantError: "verify-full"},
		{name: "verify-full keeps host name without sni", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=verify-full&sslsni=0"},
		{name: "multiple verify-full hosts", url: "postgres://app:hunter2@db1.example.net,db2.example.net/hackwerk?sslmode=verify-full"},
		{name: "unix primary with tcp prefer fallback", url: "host=/run/postgresql,db.example.net dbname=hackwerk", wantError: "verify-full"},
		{name: "keyword conninfo plaintext", url: "host=db.example.net dbname=hackwerk sslmode=disable", wantError: "verify-full"},
		{name: "opt-in service name", url: "postgres://app:hunter2@postgres:5432/hackwerk?sslmode=disable", plaintext: []string{"postgres"}},
		{name: "opt-in does not cover other hosts", url: "postgres://app:hunter2@postgres,db.example.net/hackwerk?sslmode=disable", plaintext: []string{"postgres"}, wantError: "verify-full"},
		{name: "opt-in rejects public names", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=disable", plaintext: []string{"db.example.net"}, wantError: "DATABASE_PLAINTEXT_HOSTS"},
		{name: "opt-in rejects public addresses", url: "postgres://app:hunter2@203.0.113.7/hackwerk?sslmode=disable", plaintext: []string{"203.0.113.7"}, wantError: "DATABASE_PLAINTEXT_HOSTS"},
		{name: "opt-in rejects wildcard", url: "postgres://app:hunter2@postgres/hackwerk?sslmode=disable", plaintext: []string{"*"}, wantError: "DATABASE_PLAINTEXT_HOSTS"},
		{name: "opt-in private address", url: "postgres://app:hunter2@10.20.0.5/hackwerk?sslmode=disable", plaintext: []string{"10.20.0.5"}},
		{name: "invalid url", url: "postgres://app:hunter2@db.example.net/hackwerk?sslmode=bogus", wantError: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateProductionDatabaseTLS(test.url, test.plaintext)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateProductionDatabaseTLS() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateProductionDatabaseTLS() error = %v, want %q", err, test.wantError)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error leaked credentials: %v", err)
			}
		})
	}
}

//nolint:gosec // The connection string carries a fake test-only password.
func TestProductionLoadRejectsPlaintextDatabaseUnlessHostOptedIn(t *testing.T) {
	clearLibpqEnvironment(t)
	values := map[string]string{
		"APP_ENV": "production", "APP_BASE_URL": "https://hackwerk.example", "APP_ALLOWED_HOSTS": "hackwerk.example",
		"APP_TRUSTED_PROXY_CIDRS": "10.0.0.0/8", "SESSION_COOKIE_SECURE": "true", "CALENDAR_UID_DOMAIN": "calendar.hackwerk.example",
		"DATABASE_URL":              "postgres://hackwerk_app:runtime@postgres:5432/hackwerk",
		"CONFIRMATION_TOKEN_KEY_ID": "production", "CONFIRMATION_TOKEN_KEYS": `{"production":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`,
		"AUTH_SECURITY_KEY_ID": "production", "AUTH_SECURITY_KEYS": `{"production":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`,
		"MAP_TILE_URL": "https://tiles.example/{z}/{x}/{y}.png", "MAP_TILE_ATTRIBUTION": "Tiles",
	}
	for name, value := range validBusinessEnvironment() {
		values[name] = value
	}
	get := func(name string) string { return values[name] }
	noRead := func(string) ([]byte, error) { return nil, errors.New("unexpected read") }
	if _, err := load(get, noRead); err == nil || !strings.Contains(err.Error(), "verify-full") {
		t.Fatalf("plaintext-capable production database error = %v", err)
	}
	values["DATABASE_PLAINTEXT_HOSTS"] = "postgres"
	cfg, err := load(get, noRead)
	if err != nil {
		t.Fatalf("opted-in private database host rejected: %v", err)
	}
	if len(cfg.Database.PlaintextHosts) != 1 || cfg.Database.PlaintextHosts[0] != "postgres" {
		t.Fatalf("PlaintextHosts = %q", cfg.Database.PlaintextHosts)
	}
}
