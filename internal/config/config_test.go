package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
		{name: "invalid dashboard hours", values: map[string]string{"DASHBOARD_BUSINESS_OPEN": "18:00", "DASHBOARD_BUSINESS_CLOSE": "07:00"}, expectError: "dashboard business hours"},
		{name: "unbounded dashboard horizon", values: map[string]string{"DASHBOARD_HORIZON_DAYS": "90"}, expectError: "dashboard limits"},
		{name: "invalid calendar feed domain", values: map[string]string{"CALENDAR_UID_DOMAIN": "bad domain"}, expectError: "calendar feed"},
		{name: "invalid planning horizon", values: map[string]string{"PLANNING_HORIZON_DAYS": "91"}, expectError: "planning settings"},
		{name: "enabled voice needs transcriber", values: map[string]string{"VOICE_ENABLED": "true"}, expectError: "active transcriber"},
		{name: "OpenAI voice needs secret", values: map[string]string{"VOICE_ENABLED": "true", "VOICE_TRANSCRIBER": "openai"}, expectError: "API key"},
		{name: "local whisper accepts small", values: map[string]string{"VOICE_ENABLED": "true", "VOICE_TRANSCRIBER": "whisper-local"}, expectedEnv: EnvironmentDevelopment},
		{name: "tailscale whisper accepts fixed numeric endpoint", values: map[string]string{"VOICE_ENABLED": "true", "VOICE_TRANSCRIBER": "whisper-tailscale", "VOICE_WHISPER_URL": "http://100.115.58.99:8080"}, expectedEnv: EnvironmentDevelopment},
		{name: "tailscale whisper rejects arbitrary endpoint", values: map[string]string{"VOICE_TRANSCRIBER": "whisper-tailscale", "VOICE_WHISPER_URL": "http://10.0.0.1:8080"}, expectError: "Tailscale whisper"},
		{name: "local whisper rejects other model", values: map[string]string{"VOICE_TRANSCRIBER": "whisper-local", "VOICE_WHISPER_MODEL": "large"}, expectError: "small model"},
		{name: "voice timeout remains bounded", values: map[string]string{"VOICE_PROVIDER_TIMEOUT": "16m"}, expectError: "voice limits"},
		{name: "production rejects fake voice", values: map[string]string{"APP_ENV": "production", "APP_BASE_URL": "https://hackwerk.example", "SESSION_COOKIE_SECURE": "true", "DATABASE_URL": "postgres://secure@example/hackwerk", "CONFIRMATION_TOKEN_KEY_ID": "production", "CONFIRMATION_TOKEN_KEYS": `{"production":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`, "VOICE_TRANSCRIBER": "fake"}, expectError: "fake voice"},
		{name: "OSRM rejects request-controlled query", values: map[string]string{"PLANNING_ROUTER": "osrm", "PLANNING_ROUTING_URL": "https://router.example/table?target=internal"}, expectError: "static non-loopback HTTPS"},
		{name: "internal OSRM requires exact endpoint", values: map[string]string{"PLANNING_ROUTER": "osrm-internal", "PLANNING_ROUTING_URL": "http://router:5000"}, expectError: "exactly http://osrm:5000"},
		{name: "internal OSRM rejects path", values: map[string]string{"PLANNING_ROUTER": "osrm-internal", "PLANNING_ROUTING_URL": "http://osrm:5000/base"}, expectError: "exactly http://osrm:5000"},
		{name: "internal HTTP requires explicit mode", values: map[string]string{"PLANNING_ROUTER": "osrm", "PLANNING_ROUTING_URL": "http://osrm:5000"}, expectError: "static non-loopback HTTPS"},
		{name: "Tailscale OSRM requires numeric CGNAT endpoint", values: map[string]string{"PLANNING_ROUTER": "osrm-tailscale", "PLANNING_ROUTING_URL": "http://router:5000"}, expectError: "numeric http://100.64.0.0/10:5000"},
		{name: "Tailscale OSRM rejects wrong port", values: map[string]string{"PLANNING_ROUTER": "osrm-tailscale", "PLANNING_ROUTING_URL": "http://100.115.58.99:80"}, expectError: "numeric http://100.64.0.0/10:5000"},
		{name: "Tailscale OSRM rejects path", values: map[string]string{"PLANNING_ROUTER": "osrm-tailscale", "PLANNING_ROUTING_URL": "http://100.115.58.99:5000/base"}, expectError: "numeric http://100.64.0.0/10:5000"},
		{name: "map tiles reject loopback upstream", values: map[string]string{"MAP_TILE_URL": "https://127.0.0.1/{z}/{x}/{y}.png"}, expectError: "map tiles"},
		{name: "map tiles require all placeholders", values: map[string]string{"MAP_TILE_URL": "https://tiles.example/{z}/{x}.png"}, expectError: "z, x and y"},
		{name: "map tile token requires placeholder", values: map[string]string{"MAP_TILE_TOKEN": "secret-value"}, expectError: "configured together"},
		{name: "geocoding requires configured URL", values: map[string]string{"GEOCODING_ENABLED": "true"}, expectError: "static non-loopback HTTPS"},
		{name: "geocoding rejects loopback", values: map[string]string{"GEOCODING_ENABLED": "true", "GEOCODING_SEARCH_URL": "https://127.0.0.1/search"}, expectError: "static non-loopback HTTPS"},
		{name: "geocoding URL requires enable flag", values: map[string]string{"GEOCODING_SEARCH_URL": "https://geocoder.example/search"}, expectError: "requires geocoding to be enabled"},
		{name: "production requires https", values: map[string]string{"APP_ENV": "production"}, expectError: "https"},
		{name: "external SMTP rejects loopback", values: map[string]string{"MAIL_ENABLED": "true", "MAIL_SMTP_HOST": "127.0.0.1"}, expectError: "external SMTP"},
		{name: "SMS rejects loopback webhook", values: map[string]string{
			"SMS_ENABLED": "true", "SMS_PROVIDER": "webhook", "SMS_WEBHOOK_URL": "https://127.0.0.1/send", "SMS_WEBHOOK_HMAC_SECRET": "01234567890123456789012345678901",
		}, expectError: "non-loopback HTTPS"},
		{name: "Sendberry requires complete credentials", values: map[string]string{
			"SMS_ENABLED": "true", "SMS_PROVIDER": "sendberry", "SENDBERRY_API_URL": "https://api.sendberry.example/SMS/SEND",
			"SENDBERRY_API_KEY": "01234567890123456789012345678901", "SMS_SENDER": "SMS Inform",
		}, expectError: "access name"},
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
			if tt.name == "development defaults" && (cfg.SMS.Provider != "sendberry" || cfg.SMS.SendberryURL != "" || cfg.SMS.Sender != "") {
				t.Fatalf("default SMS provider and environment-only URL/sender = %q/%q/%q", cfg.SMS.Provider, cfg.SMS.SendberryURL, cfg.SMS.Sender)
			}
			if tt.name == "local whisper accepts small" && cfg.Voice.ProviderTimeout != 10*time.Minute {
				t.Fatalf("local whisper ProviderTimeout = %s, want 10m", cfg.Voice.ProviderTimeout)
			}
		})
	}
}

func TestLoadVoiceConfigurationFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{"VOICE_ENABLED": "true", "VOICE_TRANSCRIBER": "openai", "VOICE_EXTRACTOR": "openai", "VOICE_OPENAI_API_KEY": "01234567890123456789012345678901", "VOICE_OPENAI_MODEL": "speech-model", "VOICE_OPENAI_EXTRACTION_MODEL": "extract-model", "VOICE_MAX_DURATION": "75s", "VOICE_MAX_BYTES": "1048576", "VOICE_DRAFT_RETENTION": "12h", "VOICE_PROVIDER_TIMEOUT": "20s", "VOICE_MAX_RESPONSE_BYTES": "65536", "VOICE_TEMP_DIR": "/tmp/voice-test", "VOICE_RATE_LIMIT_PER_MINUTE": "7", "VOICE_CONCURRENT_PER_USER": "1"}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Voice.Enabled || cfg.Voice.Transcriber != "openai" || cfg.Voice.Extractor != "openai" || cfg.Voice.OpenAIAPIKey != values["VOICE_OPENAI_API_KEY"] || cfg.Voice.OpenAIModel != "speech-model" || cfg.Voice.OpenAIExtractionModel != "extract-model" || cfg.Voice.MaxDuration != 75*time.Second || cfg.Voice.MaxBytes != 1048576 || cfg.Voice.DraftRetention != 12*time.Hour || cfg.Voice.ProviderTimeout != 20*time.Second || cfg.Voice.MaxResponseBytes != 65536 || cfg.Voice.TempDir != "/tmp/voice-test" || cfg.Voice.RateLimitPerMinute != 7 || cfg.Voice.ConcurrentPerUser != 1 {
		t.Fatalf("voice config=%+v", cfg.Voice)
	}
}

func TestLoadPublicBusinessInformationFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"BUSINESS_NAME":                    "HackWerk Testbetrieb",
		"BUSINESS_ADDRESS":                 "Testweg 1, 4020 Linz",
		"BUSINESS_EMAIL":                   "datenschutz@example.test",
		"BUSINESS_PHONE":                   "+43 1 234567",
		"BUSINESS_LEGAL_FORM":              "Einzelunternehmen",
		"BUSINESS_REGISTRY_NUMBER":         "FN 123456a",
		"BUSINESS_REGISTRY_COURT":          "Landesgericht Linz",
		"BUSINESS_VAT_ID":                  "ATU12345678",
		"BUSINESS_SUPERVISORY_AUTHORITY":   "Bezirkshauptmannschaft Test",
		"BUSINESS_CHAMBER":                 "Wirtschaftskammer Test",
		"BUSINESS_TRADE_RULES":             "Gewerbeordnung",
		"BUSINESS_DATA_PROTECTION_OFFICER": "Datenschutz Testkontakt",
	}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) {
		return nil, errors.New("unexpected read")
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Business.Name != values["BUSINESS_NAME"] || cfg.Business.Address != values["BUSINESS_ADDRESS"] ||
		cfg.Business.Email != values["BUSINESS_EMAIL"] || cfg.Business.Phone != values["BUSINESS_PHONE"] ||
		cfg.Business.LegalForm != values["BUSINESS_LEGAL_FORM"] || cfg.Business.RegistryNumber != values["BUSINESS_REGISTRY_NUMBER"] ||
		cfg.Business.RegistryCourt != values["BUSINESS_REGISTRY_COURT"] || cfg.Business.VATID != values["BUSINESS_VAT_ID"] ||
		cfg.Business.SupervisoryAuthority != values["BUSINESS_SUPERVISORY_AUTHORITY"] || cfg.Business.Chamber != values["BUSINESS_CHAMBER"] ||
		cfg.Business.TradeRules != values["BUSINESS_TRADE_RULES"] || cfg.Business.DataProtectionOfficer != values["BUSINESS_DATA_PROTECTION_OFFICER"] {
		t.Fatalf("business config=%+v", cfg.Business)
	}
}

func TestLoadPlanningConfigurationFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{"PLANNING_ROUTER": "osrm", "PLANNING_ROUTING_URL": "https://router.example/base", "PLANNING_HORIZON_DAYS": "42", "PLANNING_SLOT_MINUTES": "20", "PLANNING_BUFFER_MINUTES": "25", "PLANNING_WEIGHT_TRAVEL": "30"}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Planning.Router != "osrm" || cfg.Planning.RoutingURL != values["PLANNING_ROUTING_URL"] || cfg.Planning.HorizonDays != 42 || cfg.Planning.SlotMinutes != 20 || cfg.Planning.BufferMinutes != 25 || cfg.Planning.WeightTravel != 30 {
		t.Fatalf("planning config=%+v", cfg.Planning)
	}
}

func TestLoadInternalPlanningRouterFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{"PLANNING_ROUTER": "osrm-internal", "PLANNING_ROUTING_URL": "http://osrm:5000"}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Planning.Router != "osrm-internal" || cfg.Planning.RoutingURL != "http://osrm:5000" {
		t.Fatalf("planning config=%+v", cfg.Planning)
	}
}

func TestLoadTailscalePlanningRouterFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{"PLANNING_ROUTER": "osrm-tailscale", "PLANNING_ROUTING_URL": "http://100.115.58.99:5000"}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Planning.Router != "osrm-tailscale" || cfg.Planning.RoutingURL != "http://100.115.58.99:5000" {
		t.Fatalf("planning config=%+v", cfg.Planning)
	}
}

func TestLoadMapConfigurationFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"MAP_TILE_URL": "https://tiles.example/{z}/{x}/{y}.png?key={token}", "MAP_TILE_TOKEN": "environment-secret",
		"MAP_TILE_ATTRIBUTION": "Beispieldaten", "MAP_TILE_TIMEOUT": "4s", "MAP_TILE_MAX_RESPONSE_BYTES": "65536", "MAP_TILE_MAX_ZOOM": "17",
	}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Map.TileURL != values["MAP_TILE_URL"] || cfg.Map.TileToken != values["MAP_TILE_TOKEN"] || cfg.Map.Attribution != "Beispieldaten" || cfg.Map.Timeout != 4*time.Second || cfg.Map.MaxResponseBytes != 65536 || cfg.Map.MaxZoom != 17 {
		t.Fatalf("map config=%+v", cfg.Map)
	}
}

func TestLoadGeocodingConfigurationFromEnvironment(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"GEOCODING_ENABLED": "true", "GEOCODING_SEARCH_URL": "https://geocoder.example/search", "GEOCODING_COUNTRY_CODES": "at,de",
		"GEOCODING_TIMEOUT": "4s", "GEOCODING_MAX_RESPONSE_BYTES": "65536", "GEOCODING_MAX_RESULTS": "4",
		"GEOCODING_RATE_LIMIT_PER_MINUTE": "20", "GEOCODING_MIN_INTERVAL": "500ms", "GEOCODING_CACHE_TTL": "12h", "GEOCODING_CACHE_ENTRIES": "128",
	}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) { return nil, errors.New("unexpected read") })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Geocoding.Enabled || cfg.Geocoding.SearchURL != values["GEOCODING_SEARCH_URL"] || strings.Join(cfg.Geocoding.CountryCodes, ",") != "at,de" ||
		cfg.Geocoding.Timeout != 4*time.Second || cfg.Geocoding.MaxResponseBytes != 65536 || cfg.Geocoding.MaxResults != 4 || cfg.Geocoding.RateLimit != 20 ||
		cfg.Geocoding.MinInterval != 500*time.Millisecond || cfg.Geocoding.CacheTTL != 12*time.Hour || cfg.Geocoding.CacheEntries != 128 {
		t.Fatalf("geocoding config=%+v", cfg.Geocoding)
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

func TestLoadSendberryConfigurationFromEnvironment(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"SMS_ENABLED": "true", "SMS_PROVIDER": "sendberry", "SENDBERRY_API_URL": "https://sms.example.test/SMS/SEND",
		"SENDBERRY_API_KEY": "01234567890123456789012345678901", "SENDBERRY_ACCESS_NAME": "environment-user",
		"SENDBERRY_ACCESS_PASSWORD": "environment-password", "SMS_SENDER": "HackWerk", "SMS_TIMEOUT": "9s", "SMS_MAX_ATTEMPTS": "4",
	}
	cfg, err := load(func(name string) string { return values[name] }, func(string) ([]byte, error) {
		return nil, errors.New("unexpected read")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SMS.Enabled || cfg.SMS.Provider != "sendberry" || cfg.SMS.SendberryURL != values["SENDBERRY_API_URL"] ||
		cfg.SMS.SendberryKey != values["SENDBERRY_API_KEY"] || cfg.SMS.SendberryName != values["SENDBERRY_ACCESS_NAME"] ||
		cfg.SMS.SendberryPassword != values["SENDBERRY_ACCESS_PASSWORD"] || cfg.SMS.Sender != "HackWerk" ||
		cfg.SMS.Timeout != 9*time.Second || cfg.SMS.MaxAttempts != 4 {
		t.Fatal("Sendberry configuration did not match the environment values")
	}
}

func TestLoadForCommandKeepsProviderSecretsOutOfWebProcess(t *testing.T) {
	for _, name := range []string{
		"SENDBERRY_API_URL", "SENDBERRY_API_KEY", "SENDBERRY_API_KEY_FILE",
		"SENDBERRY_ACCESS_NAME", "SENDBERRY_ACCESS_NAME_FILE",
		"SENDBERRY_ACCESS_PASSWORD", "SENDBERRY_ACCESS_PASSWORD_FILE", "SMS_SENDER",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("CONFIRMATION_TOKEN_KEY_ID", "development-v1")
	t.Setenv("CONFIRMATION_TOKEN_KEYS", "")
	t.Setenv("SMS_ENABLED", "true")
	t.Setenv("SMS_PROVIDER", "sendberry")
	webConfig, err := LoadForCommand("serve")
	if err != nil {
		t.Fatalf("serve config unexpectedly requires worker credentials: %v", err)
	}
	if !webConfig.SMS.Enabled {
		t.Fatal("serve config lost the SMS feature flag used by the UI")
	}
	if _, err := LoadForCommand("worker"); err == nil {
		t.Fatal("worker config accepted enabled Sendberry without credentials")
	}
}

func TestLoadForCommandDoesNotRequireWebMapSecretInWorker(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("MAP_TILE_URL", "https://tiles.example/{z}/{x}/{y}.png?key={token}")
	t.Setenv("MAP_TILE_TOKEN", "")
	t.Setenv("MAP_TILE_TOKEN_FILE", "")
	if _, err := LoadForCommand("worker"); err != nil {
		t.Fatalf("worker config unexpectedly requires web map secret: %v", err)
	}
	if _, err := LoadForCommand("serve"); err == nil {
		t.Fatal("serve config accepted tokenized map URL without its secret")
	}
}

func TestProductionRequiresTrustedProxyAndRejectsWildcardHost(t *testing.T) {
	base := map[string]string{
		"APP_ENV": "production", "APP_BASE_URL": "https://hackwerk.example", "APP_ALLOWED_HOSTS": "hackwerk.example",
		"SESSION_COOKIE_SECURE": "true", "DATABASE_URL": "postgres://secure@example/hackwerk?sslmode=require",
		"CONFIRMATION_TOKEN_KEY_ID": "production", "CONFIRMATION_TOKEN_KEYS": `{"production":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`,
	}
	get := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if _, err := load(get(base), func(string) ([]byte, error) { return nil, errors.New("unexpected read") }); err == nil || !strings.Contains(err.Error(), "trusted proxy") {
		t.Fatalf("missing proxy error=%v", err)
	}
	base["APP_TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8"
	base["CALENDAR_UID_DOMAIN"] = "calendar.hackwerk.example"
	base["APP_ALLOWED_HOSTS"] = "*"
	if _, err := load(get(base), func(string) ([]byte, error) { return nil, errors.New("unexpected read") }); err == nil || !strings.Contains(err.Error(), "allowed host") {
		t.Fatalf("wildcard host error=%v", err)
	}
	base["APP_ALLOWED_HOSTS"] = "hackwerk.example"
	if _, err := load(get(base), func(string) ([]byte, error) { return nil, errors.New("unexpected read") }); err != nil {
		t.Fatalf("secure production config error=%v", err)
	}
}

func TestDiagnosticDoesNotExposeSecrets(t *testing.T) {
	cfg := Config{Database: Database{URL: "database-canary"}, SMS: SMS{SendberryKey: "sms-canary", SendberryPassword: "password-canary"}, Voice: Voice{OpenAIAPIKey: "voice-canary"}}
	encoded, err := json.Marshal(cfg.Diagnostic())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"database-canary", "sms-canary", "password-canary", "voice-canary"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, encoded)
		}
	}
}
