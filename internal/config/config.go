// Package config loads and validates HackWerk's startup configuration.
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	// Register the embedded timezone database for the scratch runtime image.
	_ "time/tzdata"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentTest        = "test"
	EnvironmentProduction  = "production"
	CurrentSchemaVersion   = int64(15)

	businessNamePlaceholder                  = "HackWerk – Betreiber noch nicht hinterlegt"
	businessAddressPlaceholder               = "Ladungsfähige Anschrift noch nicht hinterlegt"
	businessEmailPlaceholder                 = "E-Mail-Adresse noch nicht hinterlegt"
	businessPhonePlaceholder                 = "Telefonnummer noch nicht hinterlegt"
	businessLegalFormPlaceholder             = "Rechtsform noch nicht hinterlegt"
	businessRegistryPlaceholder              = "Nicht vorhanden oder noch nicht hinterlegt"
	businessSupervisoryAuthorityPlaceholder  = "Zuständige Behörde noch nicht hinterlegt"
	businessChamberPlaceholder               = "Kammer/Fachgruppe noch nicht hinterlegt"
	businessTradeRulesPlaceholder            = "Anwendbare gewerbe- oder berufsrechtliche Vorschriften noch nicht hinterlegt"
	businessDataProtectionOfficerPlaceholder = "Kein Datenschutzbeauftragter hinterlegt"
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
	Confirmation    Confirmation
	Worker          Worker
	Mail            Mail
	SMS             SMS
	Business        Business
	Waitlist        Waitlist
	Dashboard       Dashboard
	CalendarFeed    CalendarFeed
	Planning        Planning
	Map             Map
	Geocoding       Geocoding
	Voice           Voice
	Metrics         Metrics
	MaintenanceMode bool
}

// HTTP contains bounded server timeouts and header limits.
type HTTP struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
	AllowedHosts      []string
	TrustedProxyCIDRs []string
	InternalRateLimit int
}

// Database contains connection details. URL must never be logged.
type Database struct {
	URL              string
	MaxConnections   int32
	MinConnections   int32
	ConnectTimeout   time.Duration
	ReadinessTimeout time.Duration
	ExpectedSchema   int64
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

type Confirmation struct {
	TokenTTL     time.Duration
	CurrentKeyID string
	TokenKeys    map[string]string
	RateLimit    int
}

type Worker struct {
	InstanceID   string
	PollInterval time.Duration
	Lease        time.Duration
	BatchSize    int
}

type Mail struct {
	Enabled        bool
	Host           string
	Port           int
	TLSMode        string
	Username       string
	Password       string
	FromAddress    string
	FromName       string
	ReplyTo        string
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
	MaxAttempts    int
}

type SMS struct {
	Enabled           bool
	Provider          string
	Sender            string
	Timeout           time.Duration
	MaxAttempts       int
	SendberryURL      string
	SendberryKey      string
	SendberryName     string
	SendberryPassword string
	WebhookURL        string
	HMACSecret        string
}

type Business struct {
	Name                  string
	Address               string
	Email                 string
	Phone                 string
	LegalForm             string
	RegistryNumber        string
	RegistryCourt         string
	VATID                 string
	SupervisoryAuthority  string
	Chamber               string
	TradeRules            string
	DataProtectionOfficer string
}

type Dashboard struct {
	HorizonDays   int
	PendingAfter  time.Duration
	BusinessOpen  string
	BusinessClose string
}

// Waitlist configures advisory duration warnings without changing which
// estimates are valid business data.
type Waitlist struct {
	DurationReviewMinMinutes int32
	DurationReviewMaxMinutes int32
}

type CalendarFeed struct {
	Enabled       bool
	UIDDomain     string
	Name          string
	ExportMaxDays int
	HistoryDays   int
	FutureDays    int
	RateLimit     int
}

// Metrics configures the separately bound internal Prometheus endpoint.
type Metrics struct {
	Enabled           bool
	ListenAddr        string
	CollectionTimeout time.Duration
	WorkerStaleAfter  time.Duration
}

type Planning struct {
	Router                  string
	RoutingURL              string
	RoutingTimeout          time.Duration
	RoutingMaxResponseBytes int
	RoutingBackoff          time.Duration
	RoutingCacheTTL         time.Duration
	RoutingCacheEntries     int
	HorizonDays             int
	SlotMinutes             int
	BufferMinutes           int
	CandidateLimit          int
	SuggestionTTL           time.Duration
	BusinessOpen            string
	BusinessClose           string
	HaversineRoadFactor     float64
	HaversineSpeedKMH       float64
	WeightPreference        float64
	WeightTravel            float64
	WeightDriver            float64
	WeightResource          float64
	WeightUtilization       float64
	WeightUrgency           float64
	WeightRegion            float64
}

// Map configures the same-origin raster tile proxy. The upstream URL is
// startup configuration only and is never influenced by request data.
type Map struct {
	TileURL          string
	TileToken        string
	Attribution      string
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxZoom          int
}

// Geocoding configures optional address search. SearchURL is startup-only and
// receives user queries only after authentication, CSRF, and input validation.
type Geocoding struct {
	Enabled          bool
	SearchURL        string
	CountryCodes     []string
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxResults       int
	RateLimit        int
	MinInterval      time.Duration
	CacheTTL         time.Duration
	CacheEntries     int
}

type Voice struct {
	Enabled               bool
	Transcriber           string
	Extractor             string
	WhisperModel          string
	WhisperURL            string
	OpenAIAPIKey          string
	OpenAIModel           string
	OpenAIExtractionModel string
	FakeTranscript        string
	MaxDuration           time.Duration
	MaxBytes              int
	DraftRetention        time.Duration
	RecordingRetention    time.Duration
	ProviderTimeout       time.Duration
	MaxResponseBytes      int
	TempDir               string
	RateLimitPerMinute    int
	ConcurrentPerUser     int
	ExternalProviderNote  string
}

// Load reads process environment variables and validates the complete result.
func Load() (Config, error) {
	return load(os.Getenv, os.ReadFile)
}

// LoadForCommand validates only provider dependencies used by the selected
// process while retaining feature flags needed by the web UI.
func LoadForCommand(command string) (Config, error) {
	cfg, err := loadConfig(os.Getenv, os.ReadFile, false)
	if err != nil {
		return Config{}, err
	}
	validation := cfg
	switch command {
	case "serve":
		validation.Mail.Enabled = false
		validation.SMS.Enabled = false
	case "worker":
		validation.Map = Map{TileURL: "https://tile.openstreetmap.org/{z}/{x}/{y}.png", Attribution: "unused", Timeout: time.Second, MaxResponseBytes: 16 << 10, MaxZoom: 1}
	case "migrate", "seed-dev", "admin", "healthcheck":
		validation.Mail.Enabled = false
		validation.SMS.Enabled = false
		validation.Voice.Enabled = false
		validation.Map = Map{TileURL: "https://tile.openstreetmap.org/{z}/{x}/{y}.png", Attribution: "unused", Timeout: time.Second, MaxResponseBytes: 16 << 10, MaxZoom: 1}
	}
	if err := validation.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type readFileFunc func(string) ([]byte, error)

func load(getenv func(string) string, readFile readFileFunc) (Config, error) {
	return loadConfig(getenv, readFile, true)
}

func loadConfig(getenv func(string) string, readFile readFileFunc, validate bool) (Config, error) {
	databaseURL, err := secretValue(getenv, readFile, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	if databaseURL == "" {
		// #nosec G101 -- public development-only default; production validation rejects it.
		databaseURL = "postgres://hackplan_app:development-only@postgres:5432/hackplan?sslmode=disable"
	}
	confirmationKeys, err := secretValue(getenv, readFile, "CONFIRMATION_TOKEN_KEYS")
	if err != nil {
		return Config{}, err
	}
	mailUsername, err := secretValue(getenv, readFile, "MAIL_SMTP_USERNAME")
	if err != nil {
		return Config{}, err
	}
	mailPassword, err := secretValue(getenv, readFile, "MAIL_SMTP_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	smsSecret, err := secretValue(getenv, readFile, "SMS_WEBHOOK_HMAC_SECRET")
	if err != nil {
		return Config{}, err
	}
	sendberryKey, err := secretValue(getenv, readFile, "SENDBERRY_API_KEY")
	if err != nil {
		return Config{}, err
	}
	sendberryName, err := secretValue(getenv, readFile, "SENDBERRY_ACCESS_NAME")
	if err != nil {
		return Config{}, err
	}
	sendberryPassword, err := secretValue(getenv, readFile, "SENDBERRY_ACCESS_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	openAIKey, err := secretValue(getenv, readFile, "VOICE_OPENAI_API_KEY")
	if err != nil {
		return Config{}, err
	}
	mapTileToken, err := secretValue(getenv, readFile, "MAP_TILE_TOKEN")
	if err != nil {
		return Config{}, err
	}
	keys := map[string]string{"development-v1": "ZGV2ZWxvcG1lbnQtb25seS1oYWNrd2Vyay10b2tlbi1rZXk="}
	if confirmationKeys != "" {
		keys = make(map[string]string)
		if err := json.Unmarshal([]byte(confirmationKeys), &keys); err != nil {
			return Config{}, errors.New("config: invalid confirmation token keys JSON")
		}
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
			MaxBodyBytes:      16 << 20,
			AllowedHosts:      splitCSV(getenv("APP_ALLOWED_HOSTS")),
			TrustedProxyCIDRs: splitCSV(getenv("APP_TRUSTED_PROXY_CIDRS")),
			InternalRateLimit: 600,
		},
		Database: Database{
			URL:              databaseURL,
			MaxConnections:   20,
			MinConnections:   2,
			ConnectTimeout:   5 * time.Second,
			ReadinessTimeout: 2 * time.Second,
			ExpectedSchema:   CurrentSchemaVersion,
		},
		Auth: Auth{
			SessionCookieName:   valueOrDefault(getenv("SESSION_COOKIE_NAME"), "hackwerk_session"),
			CSRFCookieName:      valueOrDefault(getenv("CSRF_COOKIE_NAME"), "hackwerk_csrf"),
			SessionIdleTTL:      8 * time.Hour,
			SessionAbsoluteTTL:  24 * time.Hour,
			CookieSecure:        false,
			PasswordMinLength:   14,
			Argon2MemoryKiB:     64 * 1024,
			Argon2Iterations:    3,
			Argon2Parallelism:   2,
			LoginLimitPerMinute: 10,
		},
		Confirmation: Confirmation{TokenTTL: 14 * 24 * time.Hour, CurrentKeyID: valueOrDefault(getenv("CONFIRMATION_TOKEN_KEY_ID"), "development-v1"), TokenKeys: keys, RateLimit: 30},
		Worker:       Worker{InstanceID: strings.TrimSpace(getenv("WORKER_INSTANCE_ID")), PollInterval: time.Second, Lease: 30 * time.Second, BatchSize: 20},
		Mail: Mail{
			Host: valueOrDefault(getenv("MAIL_SMTP_HOST"), "smtp.example.invalid"), Port: 587, TLSMode: valueOrDefault(getenv("MAIL_SMTP_TLS"), "starttls"),
			Username: mailUsername, Password: mailPassword, FromAddress: valueOrDefault(getenv("MAIL_FROM_ADDRESS"), "hackwerk@example.invalid"),
			FromName: valueOrDefault(getenv("MAIL_FROM_NAME"), "HackWerk"), ReplyTo: strings.TrimSpace(getenv("MAIL_REPLY_TO")),
			ConnectTimeout: 8 * time.Second, CommandTimeout: 12 * time.Second, MaxAttempts: 6,
		},
		SMS: SMS{
			Provider: valueOrDefault(getenv("SMS_PROVIDER"), "sendberry"), Sender: strings.TrimSpace(getenv("SMS_SENDER")), Timeout: 8 * time.Second, MaxAttempts: 6,
			SendberryURL: strings.TrimSpace(getenv("SENDBERRY_API_URL")),
			SendberryKey: sendberryKey, SendberryName: sendberryName, SendberryPassword: sendberryPassword,
			WebhookURL: strings.TrimSpace(getenv("SMS_WEBHOOK_URL")), HMACSecret: smsSecret,
		},
		Business: Business{
			Name:                  valueOrDefault(getenv("BUSINESS_NAME"), businessNamePlaceholder),
			Address:               valueOrDefault(getenv("BUSINESS_ADDRESS"), businessAddressPlaceholder),
			Email:                 valueOrDefault(getenv("BUSINESS_EMAIL"), businessEmailPlaceholder),
			Phone:                 valueOrDefault(getenv("BUSINESS_PHONE"), businessPhonePlaceholder),
			LegalForm:             valueOrDefault(getenv("BUSINESS_LEGAL_FORM"), businessLegalFormPlaceholder),
			RegistryNumber:        valueOrDefault(getenv("BUSINESS_REGISTRY_NUMBER"), businessRegistryPlaceholder),
			RegistryCourt:         valueOrDefault(getenv("BUSINESS_REGISTRY_COURT"), businessRegistryPlaceholder),
			VATID:                 valueOrDefault(getenv("BUSINESS_VAT_ID"), businessRegistryPlaceholder),
			SupervisoryAuthority:  valueOrDefault(getenv("BUSINESS_SUPERVISORY_AUTHORITY"), businessSupervisoryAuthorityPlaceholder),
			Chamber:               valueOrDefault(getenv("BUSINESS_CHAMBER"), businessChamberPlaceholder),
			TradeRules:            valueOrDefault(getenv("BUSINESS_TRADE_RULES"), businessTradeRulesPlaceholder),
			DataProtectionOfficer: valueOrDefault(getenv("BUSINESS_DATA_PROTECTION_OFFICER"), businessDataProtectionOfficerPlaceholder),
		},
		Waitlist:  Waitlist{DurationReviewMinMinutes: 15, DurationReviewMaxMinutes: 12 * 60},
		Dashboard: Dashboard{HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: valueOrDefault(getenv("DASHBOARD_BUSINESS_OPEN"), "07:00"), BusinessClose: valueOrDefault(getenv("DASHBOARD_BUSINESS_CLOSE"), "17:00")},
		CalendarFeed: CalendarFeed{
			Enabled:   true,
			UIDDomain: valueOrDefault(getenv("CALENDAR_UID_DOMAIN"), "hackwerk.local"), Name: valueOrDefault(getenv("CALENDAR_NAME"), "HackWerk Termine"),
			ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366, RateLimit: 120,
		},
		Planning: Planning{
			Router: valueOrDefault(getenv("PLANNING_ROUTER"), "haversine"), RoutingURL: strings.TrimSpace(getenv("PLANNING_ROUTING_URL")),
			RoutingTimeout: 5 * time.Second, RoutingMaxResponseBytes: 1 << 20, RoutingBackoff: 30 * time.Second, RoutingCacheTTL: time.Hour, RoutingCacheEntries: 512,
			HorizonDays: 56, SlotMinutes: 15, BufferMinutes: 15, CandidateLimit: 2500, SuggestionTTL: 30 * time.Minute,
			BusinessOpen: valueOrDefault(getenv("PLANNING_BUSINESS_OPEN"), "07:00"), BusinessClose: valueOrDefault(getenv("PLANNING_BUSINESS_CLOSE"), "17:00"),
			HaversineRoadFactor: 1.3, HaversineSpeedKMH: 55,
			WeightPreference: 25, WeightTravel: 25, WeightDriver: 15, WeightResource: 10, WeightUtilization: 10, WeightUrgency: 10, WeightRegion: 5,
		},
		Map: Map{
			TileURL:   valueOrDefault(getenv("MAP_TILE_URL"), "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
			TileToken: mapTileToken, Attribution: valueOrDefault(getenv("MAP_TILE_ATTRIBUTION"), "© OpenStreetMap-Mitwirkende"),
			Timeout: 8 * time.Second, MaxResponseBytes: 2 << 20, MaxZoom: 19,
		},
		Geocoding: Geocoding{
			SearchURL: strings.TrimSpace(getenv("GEOCODING_SEARCH_URL")), CountryCodes: splitCSV(valueOrDefault(getenv("GEOCODING_COUNTRY_CODES"), "at")),
			Timeout: 5 * time.Second, MaxResponseBytes: 256 << 10, MaxResults: 5, RateLimit: 30,
			MinInterval: time.Second, CacheTTL: 24 * time.Hour, CacheEntries: 512,
		},
		Voice: Voice{
			Transcriber: valueOrDefault(getenv("VOICE_TRANSCRIBER"), "disabled"), Extractor: valueOrDefault(getenv("VOICE_EXTRACTOR"), "rules"),
			WhisperModel: valueOrDefault(getenv("VOICE_WHISPER_MODEL"), "small"),
			WhisperURL:   strings.TrimSpace(getenv("VOICE_WHISPER_URL")),
			OpenAIAPIKey: openAIKey, OpenAIModel: valueOrDefault(getenv("VOICE_OPENAI_MODEL"), "gpt-4o-mini-transcribe"), OpenAIExtractionModel: valueOrDefault(getenv("VOICE_OPENAI_EXTRACTION_MODEL"), "gpt-5-mini"), FakeTranscript: strings.TrimSpace(getenv("VOICE_FAKE_TRANSCRIPT")),
			MaxDuration: 90 * time.Second, MaxBytes: 15 << 20, DraftRetention: 24 * time.Hour, RecordingRetention: 30 * 24 * time.Hour, ProviderTimeout: 30 * time.Second, MaxResponseBytes: 1 << 20,
			TempDir: valueOrDefault(getenv("VOICE_TEMP_DIR"), "/tmp/hackwerk-voice"), RateLimitPerMinute: 10, ConcurrentPerUser: 2,
			ExternalProviderNote: valueOrDefault(getenv("VOICE_EXTERNAL_PROVIDER_NOTICE"), "Bei aktivem externem Sprachdienst verlassen Audio und/oder Transkript zur Verarbeitung die HackWerk-Infrastruktur."),
		},
		Metrics: Metrics{Enabled: true, ListenAddr: valueOrDefault(getenv("METRICS_LISTEN_ADDR"), "127.0.0.1:19090"), CollectionTimeout: 2 * time.Second, WorkerStaleAfter: 2 * time.Minute},
	}
	if len(cfg.HTTP.AllowedHosts) == 0 {
		if cfg.Environment != EnvironmentProduction {
			cfg.HTTP.AllowedHosts = []string{"*"}
		} else if parsedBase, parseErr := url.Parse(cfg.BaseURL); parseErr == nil && parsedBase.Hostname() != "" {
			cfg.HTTP.AllowedHosts = []string{parsedBase.Hostname()}
		}
	}

	if err := applyOverrides(&cfg, getenv); err != nil {
		return Config{}, err
	}
	if (cfg.Voice.Transcriber == "whisper-local" || cfg.Voice.Transcriber == "whisper-tailscale") && strings.TrimSpace(getenv("VOICE_PROVIDER_TIMEOUT")) == "" {
		cfg.Voice.ProviderTimeout = 10 * time.Minute
	}
	if validate {
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func applyOverrides(cfg *Config, getenv func(string) string) error {
	var err error
	if cfg.ShutdownTimeout, err = durationValue(getenv, "APP_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	for name, target := range map[string]*time.Duration{
		"HTTP_READ_HEADER_TIMEOUT": &cfg.HTTP.ReadHeaderTimeout, "HTTP_READ_TIMEOUT": &cfg.HTTP.ReadTimeout,
		"HTTP_WRITE_TIMEOUT": &cfg.HTTP.WriteTimeout, "HTTP_IDLE_TIMEOUT": &cfg.HTTP.IdleTimeout,
	} {
		if *target, err = durationValue(getenv, name, *target); err != nil {
			return err
		}
	}
	if cfg.Map.Timeout, err = durationValue(getenv, "MAP_TILE_TIMEOUT", cfg.Map.Timeout); err != nil {
		return err
	}
	if cfg.Map.MaxResponseBytes, err = int64Value(getenv, "MAP_TILE_MAX_RESPONSE_BYTES", cfg.Map.MaxResponseBytes); err != nil {
		return err
	}
	if cfg.Map.MaxZoom, err = intValue(getenv, "MAP_TILE_MAX_ZOOM", cfg.Map.MaxZoom); err != nil {
		return err
	}
	if cfg.Geocoding.Timeout, err = durationValue(getenv, "GEOCODING_TIMEOUT", cfg.Geocoding.Timeout); err != nil {
		return err
	}
	if cfg.Geocoding.MaxResponseBytes, err = int64Value(getenv, "GEOCODING_MAX_RESPONSE_BYTES", cfg.Geocoding.MaxResponseBytes); err != nil {
		return err
	}
	if cfg.Geocoding.MinInterval, err = durationValue(getenv, "GEOCODING_MIN_INTERVAL", cfg.Geocoding.MinInterval); err != nil {
		return err
	}
	if cfg.Geocoding.CacheTTL, err = durationValue(getenv, "GEOCODING_CACHE_TTL", cfg.Geocoding.CacheTTL); err != nil {
		return err
	}
	for name, target := range map[string]*int{
		"GEOCODING_MAX_RESULTS": &cfg.Geocoding.MaxResults, "GEOCODING_RATE_LIMIT_PER_MINUTE": &cfg.Geocoding.RateLimit,
		"GEOCODING_CACHE_ENTRIES": &cfg.Geocoding.CacheEntries,
	} {
		if *target, err = intValue(getenv, name, *target); err != nil {
			return err
		}
	}
	if cfg.HTTP.MaxHeaderBytes, err = intValue(getenv, "HTTP_MAX_HEADER_BYTES", cfg.HTTP.MaxHeaderBytes); err != nil {
		return err
	}
	if cfg.Database.ConnectTimeout, err = durationValue(getenv, "DATABASE_CONNECT_TIMEOUT", cfg.Database.ConnectTimeout); err != nil {
		return err
	}
	if cfg.Database.ReadinessTimeout, err = durationValue(getenv, "DATABASE_READINESS_TIMEOUT", cfg.Database.ReadinessTimeout); err != nil {
		return err
	}
	if cfg.HTTP.MaxBodyBytes, err = int64Value(getenv, "HTTP_MAX_BODY_BYTES", cfg.HTTP.MaxBodyBytes); err != nil {
		return err
	}
	if cfg.HTTP.InternalRateLimit, err = intValue(getenv, "INTERNAL_RATE_LIMIT_PER_MINUTE", cfg.HTTP.InternalRateLimit); err != nil {
		return err
	}
	if cfg.Metrics.CollectionTimeout, err = durationValue(getenv, "METRICS_COLLECTION_TIMEOUT", cfg.Metrics.CollectionTimeout); err != nil {
		return err
	}
	if cfg.Metrics.WorkerStaleAfter, err = durationValue(getenv, "WORKER_HEARTBEAT_STALE_AFTER", cfg.Metrics.WorkerStaleAfter); err != nil {
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
	if cfg.Confirmation.TokenTTL, err = durationValue(getenv, "CONFIRMATION_TOKEN_TTL", cfg.Confirmation.TokenTTL); err != nil {
		return err
	}
	if cfg.Confirmation.RateLimit, err = intValue(getenv, "CONFIRMATION_RATE_LIMIT_PER_MINUTE", cfg.Confirmation.RateLimit); err != nil {
		return err
	}
	if cfg.Worker.PollInterval, err = durationValue(getenv, "WORKER_POLL_INTERVAL", cfg.Worker.PollInterval); err != nil {
		return err
	}
	if cfg.Worker.Lease, err = durationValue(getenv, "WORKER_LEASE", cfg.Worker.Lease); err != nil {
		return err
	}
	if cfg.Worker.BatchSize, err = intValue(getenv, "WORKER_BATCH_SIZE", cfg.Worker.BatchSize); err != nil {
		return err
	}
	if cfg.Mail.Port, err = intValue(getenv, "MAIL_SMTP_PORT", cfg.Mail.Port); err != nil {
		return err
	}
	if cfg.Mail.ConnectTimeout, err = durationValue(getenv, "MAIL_CONNECT_TIMEOUT", cfg.Mail.ConnectTimeout); err != nil {
		return err
	}
	if cfg.Mail.CommandTimeout, err = durationValue(getenv, "MAIL_COMMAND_TIMEOUT", cfg.Mail.CommandTimeout); err != nil {
		return err
	}
	if cfg.Mail.MaxAttempts, err = intValue(getenv, "MAIL_MAX_ATTEMPTS", cfg.Mail.MaxAttempts); err != nil {
		return err
	}
	if cfg.SMS.Timeout, err = durationValue(getenv, "SMS_TIMEOUT", cfg.SMS.Timeout); err != nil {
		return err
	}
	if cfg.SMS.MaxAttempts, err = intValue(getenv, "SMS_MAX_ATTEMPTS", cfg.SMS.MaxAttempts); err != nil {
		return err
	}
	if cfg.Dashboard.HorizonDays, err = intValue(getenv, "DASHBOARD_HORIZON_DAYS", cfg.Dashboard.HorizonDays); err != nil {
		return err
	}
	if cfg.Dashboard.PendingAfter, err = durationValue(getenv, "DASHBOARD_PENDING_AFTER", cfg.Dashboard.PendingAfter); err != nil {
		return err
	}
	if cfg.Waitlist.DurationReviewMinMinutes, err = int32Value(getenv, "WAITLIST_DURATION_REVIEW_MIN_MINUTES", cfg.Waitlist.DurationReviewMinMinutes); err != nil {
		return err
	}
	if cfg.Waitlist.DurationReviewMaxMinutes, err = int32Value(getenv, "WAITLIST_DURATION_REVIEW_MAX_MINUTES", cfg.Waitlist.DurationReviewMaxMinutes); err != nil {
		return err
	}
	if cfg.CalendarFeed.ExportMaxDays, err = intValue(getenv, "CALENDAR_EXPORT_MAX_DAYS", cfg.CalendarFeed.ExportMaxDays); err != nil {
		return err
	}
	if cfg.CalendarFeed.HistoryDays, err = intValue(getenv, "CALENDAR_FEED_HISTORY_DAYS", cfg.CalendarFeed.HistoryDays); err != nil {
		return err
	}
	if cfg.CalendarFeed.FutureDays, err = intValue(getenv, "CALENDAR_FEED_FUTURE_DAYS", cfg.CalendarFeed.FutureDays); err != nil {
		return err
	}
	if cfg.CalendarFeed.RateLimit, err = intValue(getenv, "CALENDAR_FEED_RATE_LIMIT", cfg.CalendarFeed.RateLimit); err != nil {
		return err
	}
	for name, target := range map[string]*int{"VOICE_MAX_BYTES": &cfg.Voice.MaxBytes, "VOICE_MAX_RESPONSE_BYTES": &cfg.Voice.MaxResponseBytes, "VOICE_RATE_LIMIT_PER_MINUTE": &cfg.Voice.RateLimitPerMinute, "VOICE_CONCURRENT_PER_USER": &cfg.Voice.ConcurrentPerUser} {
		if *target, err = intValue(getenv, name, *target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*time.Duration{"VOICE_MAX_DURATION": &cfg.Voice.MaxDuration, "VOICE_DRAFT_RETENTION": &cfg.Voice.DraftRetention, "VOICE_RECORDING_RETENTION": &cfg.Voice.RecordingRetention, "VOICE_PROVIDER_TIMEOUT": &cfg.Voice.ProviderTimeout} {
		if *target, err = durationValue(getenv, name, *target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*int{
		"PLANNING_HORIZON_DAYS": &cfg.Planning.HorizonDays, "PLANNING_SLOT_MINUTES": &cfg.Planning.SlotMinutes,
		"PLANNING_BUFFER_MINUTES": &cfg.Planning.BufferMinutes, "PLANNING_CANDIDATE_LIMIT": &cfg.Planning.CandidateLimit,
		"PLANNING_ROUTING_MAX_RESPONSE_BYTES": &cfg.Planning.RoutingMaxResponseBytes,
		"PLANNING_ROUTING_CACHE_ENTRIES":      &cfg.Planning.RoutingCacheEntries,
	} {
		if *target, err = intValue(getenv, name, *target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*time.Duration{
		"PLANNING_SUGGESTION_TTL": &cfg.Planning.SuggestionTTL, "PLANNING_ROUTING_TIMEOUT": &cfg.Planning.RoutingTimeout,
		"PLANNING_ROUTING_BACKOFF":   &cfg.Planning.RoutingBackoff,
		"PLANNING_ROUTING_CACHE_TTL": &cfg.Planning.RoutingCacheTTL,
	} {
		if *target, err = durationValue(getenv, name, *target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*float64{
		"PLANNING_HAVERSINE_ROAD_FACTOR": &cfg.Planning.HaversineRoadFactor, "PLANNING_HAVERSINE_SPEED_KMH": &cfg.Planning.HaversineSpeedKMH,
		"PLANNING_WEIGHT_PREFERENCE": &cfg.Planning.WeightPreference, "PLANNING_WEIGHT_TRAVEL": &cfg.Planning.WeightTravel,
		"PLANNING_WEIGHT_DRIVER": &cfg.Planning.WeightDriver, "PLANNING_WEIGHT_RESOURCE": &cfg.Planning.WeightResource,
		"PLANNING_WEIGHT_UTILIZATION": &cfg.Planning.WeightUtilization, "PLANNING_WEIGHT_URGENCY": &cfg.Planning.WeightUrgency,
		"PLANNING_WEIGHT_REGION": &cfg.Planning.WeightRegion,
	} {
		if *target, err = floatValue(getenv, name, *target); err != nil {
			return err
		}
	}
	for name, target := range map[string]*bool{"MAIL_ENABLED": &cfg.Mail.Enabled, "SMS_ENABLED": &cfg.SMS.Enabled, "VOICE_ENABLED": &cfg.Voice.Enabled, "CALENDAR_ENABLED": &cfg.CalendarFeed.Enabled, "GEOCODING_ENABLED": &cfg.Geocoding.Enabled, "METRICS_ENABLED": &cfg.Metrics.Enabled, "MAINTENANCE_MODE": &cfg.MaintenanceMode} {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			*target, err = strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("config: invalid boolean for %s", strings.ToLower(name))
			}
		}
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
	if cfg.HTTP.ReadHeaderTimeout < time.Second || cfg.HTTP.ReadTimeout < time.Second || cfg.HTTP.WriteTimeout < time.Second || cfg.HTTP.IdleTimeout < time.Second || cfg.HTTP.MaxHeaderBytes < 16<<10 || cfg.HTTP.MaxHeaderBytes > 2<<20 {
		return errors.New("config: invalid HTTP server settings")
	}
	if cfg.Database.ExpectedSchema != CurrentSchemaVersion {
		return errors.New("config: binary schema version is inconsistent")
	}
	if cfg.HTTP.MaxBodyBytes < 1<<20 || cfg.HTTP.MaxBodyBytes > 64<<20 || cfg.HTTP.InternalRateLimit < 10 || cfg.HTTP.InternalRateLimit > 10000 {
		return errors.New("config: invalid HTTP limits")
	}
	if len(cfg.HTTP.AllowedHosts) == 0 {
		return errors.New("config: at least one allowed host is required")
	}
	for _, host := range cfg.HTTP.AllowedHosts {
		if !validAllowedHost(host) {
			return errors.New("config: invalid allowed host")
		}
		if cfg.Environment == EnvironmentProduction && strings.TrimSpace(host) == "*" {
			return errors.New("config: production allowed hosts must not contain wildcard")
		}
	}
	for _, cidr := range cfg.HTTP.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return errors.New("config: invalid trusted proxy CIDR")
		}
	}
	if cfg.Metrics.Enabled {
		if _, _, err := net.SplitHostPort(cfg.Metrics.ListenAddr); err != nil || cfg.Metrics.CollectionTimeout < 100*time.Millisecond || cfg.Metrics.CollectionTimeout > 30*time.Second || cfg.Metrics.WorkerStaleAfter < 10*time.Second || cfg.Metrics.WorkerStaleAfter > time.Hour {
			return errors.New("config: invalid metrics settings")
		}
	}
	if strings.TrimSpace(cfg.Database.URL) == "" {
		return errors.New("config: database url is required")
	}
	if cfg.Auth.SessionIdleTTL > cfg.Auth.SessionAbsoluteTTL {
		return errors.New("config: session idle ttl must not exceed absolute ttl")
	}
	if !validCookieName(cfg.Auth.SessionCookieName) || !validCookieName(cfg.Auth.CSRFCookieName) || cfg.Auth.SessionCookieName == cfg.Auth.CSRFCookieName {
		return errors.New("config: invalid auth cookie names")
	}
	if cfg.Auth.PasswordMinLength < 12 || cfg.Auth.PasswordMinLength > 256 {
		return errors.New("config: password minimum length must be between 12 and 256")
	}
	if cfg.Auth.LoginLimitPerMinute < 1 || cfg.Auth.LoginLimitPerMinute > 1000 {
		return errors.New("config: invalid login rate limit")
	}
	if cfg.Confirmation.TokenTTL < time.Hour || cfg.Confirmation.TokenTTL > 90*24*time.Hour || cfg.Confirmation.RateLimit < 1 || cfg.Confirmation.RateLimit > 1000 {
		return errors.New("config: invalid confirmation limits")
	}
	encodedKey, ok := cfg.Confirmation.TokenKeys[cfg.Confirmation.CurrentKeyID]
	decodedKey, decodeErr := base64.StdEncoding.DecodeString(encodedKey)
	if !ok || decodeErr != nil || len(decodedKey) < 32 {
		return errors.New("config: current confirmation token key is missing or too short")
	}
	if cfg.Worker.PollInterval < 100*time.Millisecond || cfg.Worker.Lease < time.Second || cfg.Worker.BatchSize < 1 || cfg.Worker.BatchSize > 500 ||
		len(cfg.Worker.InstanceID) > 128 || strings.ContainsAny(cfg.Worker.InstanceID, "\r\n\t") {
		return errors.New("config: invalid worker settings")
	}
	if cfg.Mail.MaxAttempts < 1 || cfg.Mail.MaxAttempts > 50 || cfg.Mail.Port < 1 || cfg.Mail.Port > 65535 ||
		cfg.Mail.ConnectTimeout < time.Second || cfg.Mail.CommandTimeout < time.Second {
		return errors.New("config: invalid mail settings")
	}
	if cfg.Mail.TLSMode != "starttls" && cfg.Mail.TLSMode != "implicit" {
		return errors.New("config: mail TLS mode must be starttls or implicit")
	}
	if cfg.Mail.Enabled {
		if strings.TrimSpace(cfg.Mail.Host) == "" || strings.TrimSpace(cfg.Mail.FromAddress) == "" {
			return errors.New("config: enabled mail requires host and from address")
		}
		if isLoopbackHost(cfg.Mail.Host) {
			return errors.New("config: external SMTP host must not be loopback")
		}
	}
	if cfg.SMS.MaxAttempts < 1 || cfg.SMS.MaxAttempts > 50 || cfg.SMS.Timeout < time.Second {
		return errors.New("config: invalid SMS settings")
	}
	minimumWorkerLease := time.Second
	if cfg.Mail.Enabled {
		minimumWorkerLease = max(minimumWorkerLease, cfg.Mail.ConnectTimeout+cfg.Mail.CommandTimeout)
	}
	if cfg.SMS.Enabled {
		minimumWorkerLease = max(minimumWorkerLease, cfg.SMS.Timeout)
	}
	if cfg.Worker.Lease <= minimumWorkerLease {
		return errors.New("config: worker lease must exceed the longest provider timeout")
	}
	if cfg.Dashboard.HorizonDays < 1 || cfg.Dashboard.HorizonDays > 31 || cfg.Dashboard.PendingAfter < time.Minute || cfg.Dashboard.PendingAfter > 7*24*time.Hour {
		return errors.New("config: invalid dashboard limits")
	}
	if cfg.Waitlist.DurationReviewMinMinutes < 1 || cfg.Waitlist.DurationReviewMaxMinutes <= cfg.Waitlist.DurationReviewMinMinutes || cfg.Waitlist.DurationReviewMaxMinutes > 7*24*60 {
		return errors.New("config: invalid waitlist duration review limits")
	}
	openAt, openErr := time.Parse("15:04", cfg.Dashboard.BusinessOpen)
	closeAt, closeErr := time.Parse("15:04", cfg.Dashboard.BusinessClose)
	if openErr != nil || closeErr != nil || !closeAt.After(openAt) {
		return errors.New("config: invalid dashboard business hours")
	}
	if strings.TrimSpace(cfg.CalendarFeed.UIDDomain) == "" || strings.ContainsAny(cfg.CalendarFeed.UIDDomain, " \t\r\n@") ||
		strings.TrimSpace(cfg.CalendarFeed.Name) == "" || len(cfg.CalendarFeed.Name) > 100 || cfg.CalendarFeed.ExportMaxDays < 1 || cfg.CalendarFeed.ExportMaxDays > 366 ||
		cfg.CalendarFeed.HistoryDays < 0 || cfg.CalendarFeed.HistoryDays > 366 || cfg.CalendarFeed.FutureDays < 1 || cfg.CalendarFeed.FutureDays > 730 ||
		cfg.CalendarFeed.RateLimit < 1 || cfg.CalendarFeed.RateLimit > 1000 {
		return errors.New("config: invalid calendar feed limits")
	}
	if cfg.Planning.Router != "haversine" && cfg.Planning.Router != "osrm" && cfg.Planning.Router != "osrm-internal" && cfg.Planning.Router != "osrm-tailscale" {
		return errors.New("config: planning router must be haversine, osrm, osrm-internal or osrm-tailscale")
	}
	if err := validateMapConfig(cfg.Map); err != nil {
		return err
	}
	if err := validateGeocodingConfig(cfg.Geocoding); err != nil {
		return err
	}
	if cfg.Planning.Router == "osrm" {
		routingURL, parseErr := url.Parse(cfg.Planning.RoutingURL)
		if parseErr != nil || routingURL.Scheme != "https" || routingURL.Host == "" || routingURL.User != nil || isLoopbackHost(routingURL.Hostname()) || routingURL.RawQuery != "" || routingURL.Fragment != "" {
			return errors.New("config: OSRM requires a static non-loopback HTTPS URL")
		}
	}
	if cfg.Planning.Router == "osrm-internal" && cfg.Planning.RoutingURL != "http://osrm:5000" {
		return errors.New("config: internal OSRM requires exactly http://osrm:5000")
	}
	if cfg.Planning.Router == "osrm-tailscale" && !validTailscaleRoutingURL(cfg.Planning.RoutingURL) {
		return errors.New("config: Tailscale OSRM requires a numeric http://100.64.0.0/10:5000 endpoint")
	}
	openPlanning, openPlanningErr := time.Parse("15:04", cfg.Planning.BusinessOpen)
	closePlanning, closePlanningErr := time.Parse("15:04", cfg.Planning.BusinessClose)
	weightTotal := cfg.Planning.WeightPreference + cfg.Planning.WeightTravel + cfg.Planning.WeightDriver + cfg.Planning.WeightResource + cfg.Planning.WeightUtilization + cfg.Planning.WeightUrgency + cfg.Planning.WeightRegion
	if openPlanningErr != nil || closePlanningErr != nil || !closePlanning.After(openPlanning) || cfg.Planning.HorizonDays < 1 || cfg.Planning.HorizonDays > 90 ||
		cfg.Planning.SlotMinutes < 5 || cfg.Planning.SlotMinutes > 60 || 60%cfg.Planning.SlotMinutes != 0 || cfg.Planning.BufferMinutes < 0 || cfg.Planning.BufferMinutes > 240 ||
		cfg.Planning.CandidateLimit < 10 || cfg.Planning.CandidateLimit > 10000 || cfg.Planning.SuggestionTTL < time.Minute || cfg.Planning.SuggestionTTL > 24*time.Hour ||
		cfg.Planning.RoutingMaxResponseBytes < 1024 || cfg.Planning.RoutingMaxResponseBytes > 8<<20 || cfg.Planning.RoutingCacheEntries < 1 || cfg.Planning.RoutingCacheEntries > 10000 || cfg.Planning.RoutingCacheTTL < time.Minute || cfg.Planning.RoutingCacheTTL > 30*24*time.Hour || cfg.Planning.HaversineRoadFactor < 1 || cfg.Planning.HaversineRoadFactor > 3 ||
		cfg.Planning.HaversineSpeedKMH < 5 || cfg.Planning.HaversineSpeedKMH > 150 || weightTotal <= 0 {
		return errors.New("config: invalid planning settings")
	}
	for _, weight := range []float64{cfg.Planning.WeightPreference, cfg.Planning.WeightTravel, cfg.Planning.WeightDriver, cfg.Planning.WeightResource, cfg.Planning.WeightUtilization, cfg.Planning.WeightUrgency, cfg.Planning.WeightRegion} {
		if weight < 0 || weight > 100 {
			return errors.New("config: invalid planning weights")
		}
	}
	if cfg.Voice.Transcriber != "disabled" && cfg.Voice.Transcriber != "fake" && cfg.Voice.Transcriber != "openai" && cfg.Voice.Transcriber != "whisper-local" && cfg.Voice.Transcriber != "whisper-tailscale" {
		return errors.New("config: voice transcriber must be disabled, fake, openai, whisper-local or whisper-tailscale")
	}
	if cfg.Voice.Extractor != "rules" && cfg.Voice.Extractor != "openai" {
		return errors.New("config: voice extractor must be rules or openai")
	}
	if cfg.Voice.MaxDuration < time.Second || cfg.Voice.MaxDuration > 5*time.Minute || cfg.Voice.MaxBytes < 1024 || cfg.Voice.MaxBytes > 15<<20 || cfg.Voice.DraftRetention < 5*time.Minute || cfg.Voice.DraftRetention > 7*24*time.Hour || cfg.Voice.RecordingRetention < 24*time.Hour || cfg.Voice.RecordingRetention > 30*24*time.Hour || cfg.Voice.ProviderTimeout < time.Second || cfg.Voice.ProviderTimeout > 15*time.Minute || cfg.Voice.MaxResponseBytes < 1024 || cfg.Voice.MaxResponseBytes > 4<<20 || strings.TrimSpace(cfg.Voice.TempDir) == "" || cfg.Voice.RateLimitPerMinute < 1 || cfg.Voice.RateLimitPerMinute > 100 || cfg.Voice.ConcurrentPerUser < 1 || cfg.Voice.ConcurrentPerUser > 5 {
		return errors.New("config: invalid voice limits")
	}
	if cfg.Voice.Enabled && cfg.Voice.Transcriber == "disabled" {
		return errors.New("config: enabled voice input requires an active transcriber")
	}
	if cfg.Voice.Transcriber == "openai" && (len(cfg.Voice.OpenAIAPIKey) < 16 || strings.TrimSpace(cfg.Voice.OpenAIModel) == "") {
		return errors.New("config: OpenAI voice transcriber requires API key and model")
	}
	if (cfg.Voice.Transcriber == "whisper-local" || cfg.Voice.Transcriber == "whisper-tailscale") && cfg.Voice.WhisperModel != "small" {
		return errors.New("config: local whisper transcriber requires the small model")
	}
	if cfg.Voice.Transcriber == "whisper-tailscale" && !validTailscaleVoiceURL(cfg.Voice.WhisperURL) {
		return errors.New("config: Tailscale whisper requires a numeric http://100.64.0.0/10:8080 endpoint")
	}
	if cfg.Voice.Extractor == "openai" && (len(cfg.Voice.OpenAIAPIKey) < 16 || strings.TrimSpace(cfg.Voice.OpenAIExtractionModel) == "") {
		return errors.New("config: OpenAI voice extractor requires API key and model")
	}
	if cfg.Voice.Transcriber == "fake" && cfg.Environment == EnvironmentProduction {
		return errors.New("config: fake voice transcriber is not allowed in production")
	}
	if cfg.SMS.Provider != "sendberry" && cfg.SMS.Provider != "webhook" && cfg.SMS.Provider != "disabled" {
		return errors.New("config: SMS provider must be sendberry, webhook or disabled")
	}
	if cfg.SMS.Enabled {
		if cfg.SMS.Provider == "disabled" {
			return errors.New("config: enabled SMS requires an active provider")
		}
		providerURL := cfg.SMS.WebhookURL
		if cfg.SMS.Provider == "sendberry" {
			providerURL = cfg.SMS.SendberryURL
		}
		smsURL, parseErr := url.Parse(providerURL)
		if parseErr != nil || smsURL.Scheme != "https" || smsURL.Host == "" || isLoopbackHost(smsURL.Hostname()) {
			return errors.New("config: enabled SMS requires a non-loopback HTTPS provider URL")
		}
		if cfg.SMS.Provider == "webhook" && len(cfg.SMS.HMACSecret) < 32 {
			return errors.New("config: enabled SMS webhook requires an HMAC secret")
		}
		if cfg.SMS.Provider == "sendberry" && (len(cfg.SMS.SendberryKey) < 16 || cfg.SMS.SendberryName == "" || cfg.SMS.SendberryPassword == "" || cfg.SMS.Sender == "") {
			return errors.New("config: enabled Sendberry requires API key, access name, access password and sender")
		}
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
		if cfg.Confirmation.CurrentKeyID == "development-v1" {
			return errors.New("config: production confirmation token key is required")
		}
		if len(cfg.HTTP.TrustedProxyCIDRs) == 0 {
			return errors.New("config: production trusted proxy CIDRs are required")
		}
		if len(splitCSV(strings.Join(cfg.HTTP.AllowedHosts, ","))) == 0 || !hostAllowed(baseURL.Hostname(), cfg.HTTP.AllowedHosts) {
			return errors.New("config: production base host must be explicitly allowed")
		}
		if cfg.LogFormat != "json" || cfg.LogLevel == "debug" {
			return errors.New("config: production logging must use non-debug JSON")
		}
		if err := validateProductionBusiness(cfg.Business); err != nil {
			return err
		}
		if cfg.Mail.Enabled && (strings.HasSuffix(strings.ToLower(cfg.Mail.Host), ".invalid") || strings.HasSuffix(strings.ToLower(cfg.Mail.FromAddress), ".invalid")) {
			return errors.New("config: production mail configuration must not use example defaults")
		}
		if cfg.CalendarFeed.Enabled && strings.EqualFold(cfg.CalendarFeed.UIDDomain, "hackwerk.local") {
			return errors.New("config: production calendar UID domain is required")
		}
	}
	return nil
}

func validateProductionBusiness(business Business) error {
	fields := []struct {
		name        string
		value       string
		placeholder string
	}{
		{name: "name", value: business.Name, placeholder: businessNamePlaceholder},
		{name: "address", value: business.Address, placeholder: businessAddressPlaceholder},
		{name: "email", value: business.Email, placeholder: businessEmailPlaceholder},
		{name: "phone", value: business.Phone, placeholder: businessPhonePlaceholder},
		{name: "legal form", value: business.LegalForm, placeholder: businessLegalFormPlaceholder},
		{name: "registry number", value: business.RegistryNumber, placeholder: businessRegistryPlaceholder},
		{name: "registry court", value: business.RegistryCourt, placeholder: businessRegistryPlaceholder},
		{name: "VAT ID", value: business.VATID, placeholder: businessRegistryPlaceholder},
		{name: "supervisory authority", value: business.SupervisoryAuthority, placeholder: businessSupervisoryAuthorityPlaceholder},
		{name: "chamber", value: business.Chamber, placeholder: businessChamberPlaceholder},
		{name: "trade rules", value: business.TradeRules, placeholder: businessTradeRulesPlaceholder},
		{name: "data protection officer", value: business.DataProtectionOfficer, placeholder: businessDataProtectionOfficerPlaceholder},
	}
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" || strings.EqualFold(value, field.placeholder) {
			return fmt.Errorf("config: production business %s is required", field.name)
		}
	}
	return nil
}

func validateMapConfig(cfg Map) error {
	if strings.TrimSpace(cfg.Attribution) == "" || cfg.Timeout < time.Second || cfg.Timeout > 30*time.Second ||
		cfg.MaxResponseBytes < 16<<10 || cfg.MaxResponseBytes > 8<<20 || cfg.MaxZoom < 1 || cfg.MaxZoom > 22 {
		return errors.New("config: invalid map tile settings")
	}
	template := strings.TrimSpace(cfg.TileURL)
	parsed, err := url.Parse(template)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || isLoopbackHost(parsed.Hostname()) {
		return errors.New("config: map tiles require a static non-loopback HTTPS URL")
	}
	for _, placeholder := range []string{"{z}", "{x}", "{y}"} {
		if strings.Count(template, placeholder) != 1 {
			return errors.New("config: map tile URL requires z, x and y placeholders")
		}
	}
	if strings.Contains(template, "{token}") != (strings.TrimSpace(cfg.TileToken) != "") {
		return errors.New("config: map tile token and URL placeholder must be configured together")
	}
	return nil
}

func validateGeocodingConfig(cfg Geocoding) error {
	if cfg.Timeout < time.Second || cfg.Timeout > 30*time.Second || cfg.MaxResponseBytes < 16<<10 || cfg.MaxResponseBytes > 2<<20 ||
		cfg.MaxResults < 1 || cfg.MaxResults > 10 || cfg.RateLimit < 1 || cfg.RateLimit > 120 || cfg.MinInterval < 100*time.Millisecond || cfg.MinInterval > time.Minute ||
		cfg.CacheTTL < time.Minute || cfg.CacheTTL > 30*24*time.Hour || cfg.CacheEntries < 1 || cfg.CacheEntries > 4096 || len(cfg.CountryCodes) == 0 || len(cfg.CountryCodes) > 8 {
		return errors.New("config: invalid geocoding settings")
	}
	for _, code := range cfg.CountryCodes {
		if len(code) != 2 || code[0] < 'a' || code[0] > 'z' || code[1] < 'a' || code[1] > 'z' {
			return errors.New("config: geocoding country codes must be lowercase ISO alpha-2")
		}
	}
	searchURL := strings.TrimSpace(cfg.SearchURL)
	if !cfg.Enabled && searchURL == "" {
		return nil
	}
	parsed, err := url.Parse(searchURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || isLoopbackHost(parsed.Hostname()) {
		return errors.New("config: geocoding requires a static non-loopback HTTPS search URL")
	}
	if !cfg.Enabled {
		return errors.New("config: geocoding URL requires geocoding to be enabled")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func validTailscaleRoutingURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" || parsed.Path != "" || parsed.Scheme != "http" {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || !address.Is4() || !netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
		return false
	}
	return parsed.Host == net.JoinHostPort(address.String(), "5000")
}

func validTailscaleVoiceURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" || parsed.Path != "" || parsed.Scheme != "http" {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || !address.Is4() || !netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
		return false
	}
	return parsed.Host == net.JoinHostPort(address.String(), "8080")
}

func validAllowedHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "*" {
		return true
	}
	if host == "" || strings.ContainsAny(host, "/\\?#@\r\n\t ") {
		return false
	}
	if parsed := net.ParseIP(strings.Trim(host, "[]")); parsed != nil {
		return true
	}
	return !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".") && !strings.Contains(host, "..")
}

func validCookieName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == "*" {
			return true
		}
		if host == strings.Trim(strings.ToLower(strings.TrimSpace(candidate)), "[]") {
			return true
		}
	}
	return false
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, part)
	}
	return result
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

func int64Value(getenv func(string) string, name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: invalid integer for %s", strings.ToLower(name))
	}
	return parsed, nil
}

func floatValue(getenv func(string) string, name string, fallback float64) (float64, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("config: invalid number for %s", strings.ToLower(name))
	}
	return parsed, nil
}

// Diagnostic returns an operator-readable allowlist without secret values.
func (cfg Config) Diagnostic() map[string]any {
	configured := func(value string) string {
		if strings.TrimSpace(value) == "" {
			return "not_set"
		}
		return "set_redacted"
	}
	return map[string]any{
		"app_env": cfg.Environment, "app_name": cfg.AppName, "listen_addr": cfg.ListenAddr,
		"allowed_hosts": append([]string(nil), cfg.HTTP.AllowedHosts...), "trusted_proxy_cidrs": append([]string(nil), cfg.HTTP.TrustedProxyCIDRs...),
		"database_url": configured(cfg.Database.URL), "schema_version": cfg.Database.ExpectedSchema,
		"metrics_enabled": cfg.Metrics.Enabled, "metrics_listen_addr": cfg.Metrics.ListenAddr,
		"mail_enabled": cfg.Mail.Enabled, "mail_username": configured(cfg.Mail.Username), "mail_password": configured(cfg.Mail.Password),
		"sms_enabled": cfg.SMS.Enabled, "sms_provider": cfg.SMS.Provider, "sendberry_api_url": configured(cfg.SMS.SendberryURL),
		"sendberry_api_key": configured(cfg.SMS.SendberryKey), "sendberry_access_name": configured(cfg.SMS.SendberryName), "sendberry_access_password": configured(cfg.SMS.SendberryPassword),
		"voice_enabled": cfg.Voice.Enabled, "voice_transcriber": cfg.Voice.Transcriber, "voice_whisper_url": configured(cfg.Voice.WhisperURL),
		"voice_recording_retention": cfg.Voice.RecordingRetention.String(), "voice_provider_key": configured(cfg.Voice.OpenAIAPIKey), "calendar_enabled": cfg.CalendarFeed.Enabled,
		"routing_provider": cfg.Planning.Router, "map_tile_url": configured(cfg.Map.TileURL), "map_tile_token": configured(cfg.Map.TileToken),
		"geocoding_enabled": cfg.Geocoding.Enabled, "geocoding_search_url": configured(cfg.Geocoding.SearchURL),
		"confirmation_key_count": len(cfg.Confirmation.TokenKeys), "maintenance_mode": cfg.MaintenanceMode,
	}
}
