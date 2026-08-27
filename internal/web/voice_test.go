package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/voice"
)

type webVoiceStore struct {
	draft   voice.Draft
	creates int
}

type webVoiceTranscriber struct {
	calls int
}

func (transcriber *webVoiceTranscriber) Transcribe(context.Context, voice.Audio, string, voice.Metadata) (voice.Transcript, error) {
	transcriber.calls++
	return voice.Transcript{Text: "fixture"}, nil
}

func (store *webVoiceStore) Create(_ context.Context, actor auth.Actor, expires time.Time) (voice.Draft, error) {
	store.creates++
	store.draft = voice.Draft{ID: "voice-draft", OwnerUserID: actor.UserID, Status: voice.StatusTranscribing, Version: 1, ExpiresAt: expires}
	return store.draft, nil
}
func (store *webVoiceStore) Complete(_ context.Context, _ auth.Actor, _ string, transcript voice.Transcript, fields voice.Fields, warnings []string, confidence float64, parser string) error {
	store.draft.Status = voice.StatusNeedsReview
	store.draft.Version = 2
	store.draft.Transcript = transcript.Text
	store.draft.Fields = fields
	store.draft.Warnings = warnings
	store.draft.OverallConfidence = confidence
	store.draft.ParserVersion = parser
	return nil
}
func (store *webVoiceStore) Fail(context.Context, auth.Actor, string, string) error { return nil }
func (store *webVoiceStore) Get(context.Context, auth.Actor, string) (voice.Draft, error) {
	return store.draft, nil
}
func (store *webVoiceStore) FindDuplicates(context.Context, customers.CustomerInput) ([]customers.Duplicate, error) {
	return nil, nil
}
func (store *webVoiceStore) Commit(context.Context, auth.Actor, voice.CommitInput) (customers.CreatedIntake, error) {
	return customers.CreatedIntake{}, nil
}
func (store *webVoiceStore) Discard(context.Context, auth.Actor, string) error { return nil }
func (store *webVoiceStore) Cleanup(context.Context) (int64, error)            { return 0, nil }

func TestReceiveVoiceUploadValidWebMUsesRestrictiveTempFile(t *testing.T) {
	cfg := config.Voice{TempDir: t.TempDir(), MaxBytes: 64 << 10, MaxDuration: 90 * time.Second}
	reader := voiceMultipart(t, webMOpusFixture(time.Second), "audio/webm", "1000")
	file, duration, mediaType, err := receiveVoiceUpload(reader, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	defer func() { _ = file.Close() }()
	info, _ := file.Stat()
	if (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) || duration != time.Second || mediaType != "audio/webm" {
		t.Fatalf("mode/duration/type = %v/%v/%s", info.Mode().Perm(), duration, mediaType)
	}
}

func TestVoiceUploadRejectsActualOverLimitBeforeCreatingDraft(t *testing.T) {
	store := &webVoiceStore{}
	transcriber := &webVoiceTranscriber{}
	router, tempDir := voiceHTTPRouterWithTranscriber(t, store, transcriber)
	body, contentType := voiceRequestBodyWithAudio(t, "csrf-token", "1", "audio/wav", wavFixture(91*time.Second))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/voice/upload", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", "https://example.test")
	request.AddCookie(secureVoiceTestCookie("hackplan_session", "session-token"))
	request.AddCookie(secureVoiceTestCookie("hackplan_csrf", "csrf-token"))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity || store.creates != 0 || transcriber.calls != 0 || !strings.Contains(response.Body.String(), "Aufnahmedauer") {
		t.Fatalf("response/creates/provider-calls = %d/%d/%d body=%q, want %d/0/0 with duration error", response.Code, store.creates, transcriber.calls, response.Body.String(), http.StatusUnprocessableEntity)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary upload files remain: %v, err=%v", entries, err)
	}
}

func TestVoiceUploadRejectsUnverifiableMP4BeforeCreatingDraft(t *testing.T) {
	store := &webVoiceStore{}
	transcriber := &webVoiceTranscriber{}
	router, tempDir := voiceHTTPRouterWithTranscriber(t, store, transcriber)
	body, contentType := voiceRequestBodyWithAudio(t, "csrf-token", "1", "audio/mp4", mp4AACFixture(time.Second))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/voice/upload", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", "https://example.test")
	request.AddCookie(secureVoiceTestCookie("hackplan_session", "session-token"))
	request.AddCookie(secureVoiceTestCookie("hackplan_csrf", "csrf-token"))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType || store.creates != 0 || transcriber.calls != 0 || !strings.Contains(response.Body.String(), "Audioformat") {
		t.Fatalf("response/creates/provider-calls = %d/%d/%d body=%q, want %d/0/0 with format error", response.Code, store.creates, transcriber.calls, response.Body.String(), http.StatusUnsupportedMediaType)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary upload files remain: %v, err=%v", entries, err)
	}
}

func TestReceiveVoiceUploadRejectsEmptyWrongTypeLargeAndDuration(t *testing.T) {
	cfg := config.Voice{TempDir: t.TempDir(), MaxBytes: 8, MaxDuration: 2 * time.Second}
	tests := []struct {
		name              string
		audio             []byte
		content, duration string
		want              error
	}{{"empty", nil, "audio/webm", "1000", errVoiceEmpty}, {"type", []byte("nope"), "audio/webm", "1000", errVoiceType}, {"large", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 9)...), "audio/webm", "1000", errVoiceTooLarge}, {"duration", []byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm", "3000", errVoiceDuration}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _, _, err := receiveVoiceUpload(voiceMultipart(t, tt.audio, tt.content, tt.duration), cfg)
			if file != nil {
				_ = file.Close()
				_ = os.Remove(file.Name())
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			entries, _ := os.ReadDir(cfg.TempDir)
			if len(entries) != 0 {
				t.Fatalf("temporary files remain: %v", entries)
			}
		})
	}
}

func TestNativeVoiceUploadFallbackAndAPIRemainSecure(t *testing.T) {
	tests := []struct {
		name, path, csrfField, csrfHeader, duration, audioType string
		wantStatus, wantCreates                                int
		wantBody                                               string
	}{
		{name: "native success", path: "/voice/upload", csrfField: "csrf-token", duration: "3", audioType: "audio/wav", wantStatus: http.StatusSeeOther, wantCreates: 1},
		{name: "native missing csrf", path: "/voice/upload", duration: "3", audioType: "audio/wav", wantStatus: http.StatusForbidden, wantBody: "Sicherheitsmerkmal"},
		{name: "native invalid csrf", path: "/voice/upload", csrfField: "wrong", duration: "3", audioType: "audio/wav", wantStatus: http.StatusForbidden, wantBody: "Sicherheitsmerkmal"},
		{name: "native validation error", path: "/voice/upload", csrfField: "csrf-token", duration: "0", audioType: "audio/wav", wantStatus: http.StatusUnprocessableEntity, wantBody: "Aufnahmedauer"},
		{name: "api json success", path: "/api/v1/voice/drafts", csrfHeader: "csrf-token", duration: "3000", audioType: "audio/wav", wantStatus: http.StatusCreated, wantCreates: 1, wantBody: `"location":"/voice/drafts/voice-draft"`},
		{name: "api still requires csrf header", path: "/api/v1/voice/drafts", csrfField: "csrf-token", duration: "3000", audioType: "audio/wav", wantStatus: http.StatusForbidden, wantBody: "Request-Header"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &webVoiceStore{}
			router, tempDir := voiceHTTPRouter(t, store)
			body, contentType := voiceRequestBody(t, test.csrfField, test.duration, test.audioType)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test"+test.path, body)
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Origin", "https://example.test")
			if test.csrfHeader != "" {
				request.Header.Set("X-CSRF-Token", test.csrfHeader)
			}
			request.AddCookie(secureVoiceTestCookie("hackplan_session", "session-token"))
			request.AddCookie(secureVoiceTestCookie("hackplan_csrf", "csrf-token"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus || store.creates != test.wantCreates || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("response/creates = %d/%d body=%q, want %d/%d containing %q", response.Code, store.creates, response.Body.String(), test.wantStatus, test.wantCreates, test.wantBody)
			}
			if test.wantStatus == http.StatusSeeOther && response.Header().Get("Location") != "/voice/drafts/voice-draft" {
				t.Fatalf("native redirect = %q", response.Header().Get("Location"))
			}
			entries, err := os.ReadDir(tempDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary upload files remain: %v, err=%v", entries, err)
			}
		})
	}
}

type unreadVoiceBody struct {
	read bool
}

func (body *unreadVoiceBody) Read([]byte) (int, error) {
	body.read = true
	return 0, errors.New("request body must not be read")
}

func TestNativeVoiceUploadRejectsCrossSiteBeforeReadingBody(t *testing.T) {
	store := &webVoiceStore{}
	router, tempDir := voiceHTTPRouter(t, store)
	body := &unreadVoiceBody{}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/voice/upload", body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=not-read")
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(secureVoiceTestCookie("hackplan_session", "session-token"))
	request.AddCookie(secureVoiceTestCookie("hackplan_csrf", "csrf-token"))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || body.read || store.creates != 0 {
		t.Fatalf("status/body-read/creates = %d/%v/%d, want %d/false/0", response.Code, body.read, store.creates, http.StatusForbidden)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary upload files remain: %v, err=%v", entries, err)
	}
}

func secureVoiceTestCookie(name, value string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func voiceHTTPRouter(t *testing.T, store *webVoiceStore) (http.Handler, string) {
	return voiceHTTPRouterWithTranscriber(t, store, voice.FakeTranscriber{Text: "Franz Huber, 80 m³, drei Stunden"})
}

func voiceHTTPRouterWithTranscriber(t *testing.T, store *webVoiceStore, transcriber voice.Transcriber) (http.Handler, string) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	identityStore := &identityTestStore{session: auth.Session{
		ID: "session", Actor: auth.Actor{UserID: "voice-user", Username: "voice", DisplayName: "Voice Driver", Role: auth.RoleDriver, UserVersion: 1},
		CSRFTokenHash: auth.TokenHash("csrf-token"), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true,
	}}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	voiceService, err := voice.New(store, transcriber, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 1, Timezone: location}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	cfg := configForWebTest()
	cfg.Voice = config.Voice{Enabled: true, Transcriber: "fake", MaxDuration: 90 * time.Second, MaxBytes: 1 << 20, ProviderTimeout: time.Second, TempDir: tempDir}
	router, err := NewRouter(Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{},
		Build: buildinfo.Info{Version: "test"}, Identity: identity, Voice: voiceService,
	})
	if err != nil {
		t.Fatal(err)
	}
	return router, tempDir
}

func voiceRequestBody(t *testing.T, csrfToken, duration, audioType string) (*bytes.Reader, string) {
	return voiceRequestBodyWithAudio(t, csrfToken, duration, audioType, wavFixture(3*time.Second))
}

func voiceRequestBodyWithAudio(t *testing.T, csrfToken, duration, audioType string, audio []byte) (*bytes.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if csrfToken != "" {
		if err := writer.WriteField("csrf_token", csrfToken); err != nil {
			t.Fatal(err)
		}
	}
	durationField := "duration_seconds"
	if duration == "3000" {
		durationField = "duration_ms"
	}
	if err := writer.WriteField(durationField, duration); err != nil {
		t.Fatal(err)
	}
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="audio"; filename="input.wav"`}
	header["Content-Type"] = []string{audioType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}
func voiceMultipart(t *testing.T, audio []byte, contentType, duration string) *multipart.Reader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("duration_ms", duration)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="audio"; filename="input.bin"`}
	header["Content-Type"] = []string{contentType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(audio)
	_ = writer.Close()
	return multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary())
}
