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
	"net/url"
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
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
)

type webVoiceStore struct {
	draft          voice.Draft
	recordingAudio voice.RecordingAudio
	recordings     []voice.Recording
	listLimit      int32
	listOffset     int32
	creates        int
	uploadKeyHash  []byte
}

func (store *webVoiceStore) FindRecordingByUploadKey(_ context.Context, actor auth.Actor, uploadKeyHash []byte) (voice.Draft, bool, error) {
	if bytes.Equal(store.uploadKeyHash, uploadKeyHash) && store.draft.ID != "" && store.draft.OwnerUserID == actor.UserID {
		return store.draft, true, nil
	}
	return voice.Draft{}, false, nil
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
func (store *webVoiceStore) CreateRecording(_ context.Context, actor auth.Actor, uploadKeyHash []byte, _ []byte, _ string, _ voice.Metadata, expires, _ time.Time) (voice.Draft, error) {
	store.creates++
	store.uploadKeyHash = append([]byte(nil), uploadKeyHash...)
	store.draft = voice.Draft{ID: "voice-draft", OwnerUserID: actor.UserID, Status: voice.StatusRecorded, Version: 1, ExpiresAt: expires}
	return store.draft, nil
}
func (store *webVoiceStore) RetryRecording(_ context.Context, actor auth.Actor, _ string, expectedVersion int32, _ time.Time) (voice.Draft, error) {
	if store.draft.OwnerUserID != actor.UserID || store.draft.Status != voice.StatusFailed || store.draft.Version != expectedVersion || store.draft.ManualRetryCount >= 1 {
		return voice.Draft{}, voice.ErrConflict
	}
	store.draft.Status = voice.StatusRecorded
	store.draft.ManualRetryCount++
	store.draft.Version++
	return store.draft, nil
}
func (*webVoiceStore) ClaimRecording(context.Context, string, time.Time, time.Time) (voice.ClaimedRecording, bool, error) {
	return voice.ClaimedRecording{}, false, nil
}
func (*webVoiceStore) CompleteRecording(context.Context, string, voice.ClaimedRecording, voice.Transcript, voice.Fields, []string, float64, string, time.Time) error {
	return nil
}
func (*webVoiceStore) FailRecording(context.Context, string, voice.ClaimedRecording, string, bool, time.Time, time.Time) error {
	return nil
}
func (store *webVoiceStore) ListRecordings(_ context.Context, limit, offset int32) ([]voice.Recording, error) {
	store.listLimit, store.listOffset = limit, offset
	return store.recordings, nil
}
func (store *webVoiceStore) GetRecordingAudio(context.Context, string) (voice.RecordingAudio, error) {
	if store.recordingAudio.ID == "" {
		return voice.RecordingAudio{}, voice.ErrNotFound
	}
	return store.recordingAudio, nil
}
func (*webVoiceStore) CleanupRecordings(context.Context, time.Time) (int64, error) { return 0, nil }
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
		{name: "api json success", path: "/api/v1/voice/drafts", csrfHeader: "csrf-token", duration: "3000", audioType: "audio/wav", wantStatus: http.StatusAccepted, wantCreates: 1, wantBody: `"location":"/voice/drafts/voice-draft"`},
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

func TestVoiceRecordingPlaybackIsAdminOnlyAndNotCacheable(t *testing.T) {
	store := &webVoiceStore{recordingAudio: voice.RecordingAudio{
		ID: "recording", ContentType: "audio/wav", Bytes: wavFixture(time.Second),
		RecordedAt: time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC),
	}}
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := voice.New(store, voice.FakeTranscriber{Text: "fixture"}, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RecordingRetention: 30 * 24 * time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 1, Timezone: location}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler := voiceRecordingAudio(service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		role       auth.Role
		wantStatus int
	}{{auth.RoleDriver, http.StatusForbidden}, {auth.RoleAdmin, http.StatusOK}} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/admin/voice-recordings/recording/audio", nil)
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("recordingID", "recording")
		ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
		ctx = context.WithValue(ctx, sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "user", Role: test.role}})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request.WithContext(ctx))
		if response.Code != test.wantStatus {
			t.Fatalf("role %s status = %d, want %d", test.role, response.Code, test.wantStatus)
		}
		if test.role == auth.RoleAdmin && (response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Type") != "audio/wav") {
			t.Fatalf("playback headers = %#v", response.Header())
		}
	}
}

func TestVoiceRecordingsPageLabelsCurrentPageCount(t *testing.T) {
	store := &webVoiceStore{recordings: make([]voice.Recording, 51)}
	for index := range store.recordings {
		store.recordings[index] = voice.Recording{
			ID:               "recording",
			OwnerDisplayName: "Test Admin",
			RecordedAt:       time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC),
			ExpiresAt:        time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
		}
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := voice.New(store, voice.FakeTranscriber{Text: "fixture"}, voice.RuleExtractor{}, voice.Config{
		Enabled: true, Retention: time.Hour, RecordingRetention: 30 * 24 * time.Hour,
		RateLimitPerMinute: 10, ConcurrentPerUser: 1, Timezone: location,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler := voiceRecordingsPage(service, templates.PageData{AppName: "HackWerk", Version: "test"}, "csrf", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/admin/voice-recordings", nil)
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{
		Actor: auth.Actor{UserID: "admin", Role: auth.RoleAdmin},
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || store.listLimit != 51 || store.listOffset != 0 ||
		!strings.Contains(body, "50 auf dieser Seite") || !strings.Contains(body, "Ältere Aufnahmen") || strings.Contains(body, "50 aktiv") {
		t.Fatalf("status/limit/offset = %d/%d/%d body=%q", response.Code, store.listLimit, store.listOffset, body)
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

func TestFailedVoiceDraftOffersOneNoJavaScriptRetranscription(t *testing.T) {
	store := &webVoiceStore{draft: voice.Draft{
		ID: "voice-draft", OwnerUserID: "voice-user", Status: voice.StatusFailed, Version: 4,
		ExpiresAt: time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC),
	}}
	router, _ := voiceHTTPRouter(t, store)
	page := httptest.NewRecorder()
	router.ServeHTTP(page, authenticatedCustomerRequest(t, http.MethodGet, "/voice/drafts/voice-draft", nil, "session-token", "csrf-token"))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "/voice/drafts/voice-draft/retranscribe") || !strings.Contains(page.Body.String(), `name="version" value="4"`) {
		t.Fatalf("failed draft page = %d %s", page.Code, page.Body.String())
	}
	for _, form := range []url.Values{
		{"csrf_token": {"csrf-token"}},
		{"csrf_token": {"csrf-token"}, "version": {"not-a-version"}},
	} {
		invalid := httptest.NewRecorder()
		router.ServeHTTP(invalid, authenticatedCustomerRequest(t, http.MethodPost, "/voice/drafts/voice-draft/retranscribe", form, "session-token", "csrf-token"))
		if invalid.Code != http.StatusUnprocessableEntity || store.draft.Status != voice.StatusFailed || store.draft.ManualRetryCount != 0 {
			t.Fatalf("invalid retry response/draft = %d/%#v", invalid.Code, store.draft)
		}
	}

	retry := httptest.NewRecorder()
	form := url.Values{"csrf_token": {"csrf-token"}, "version": {"4"}}
	router.ServeHTTP(retry, authenticatedCustomerRequest(t, http.MethodPost, "/voice/drafts/voice-draft/retranscribe", form, "session-token", "csrf-token"))
	if retry.Code != http.StatusSeeOther || store.draft.Status != voice.StatusRecorded || store.draft.ManualRetryCount != 1 {
		t.Fatalf("retry response/draft = %d/%#v", retry.Code, store.draft)
	}
}

func TestVoicePageRendersFreshUploadIdempotencyKey(t *testing.T) {
	store := &webVoiceStore{}
	router, _ := voiceHTTPRouter(t, store)
	page := httptest.NewRecorder()
	router.ServeHTTP(page, authenticatedCustomerRequest(t, http.MethodGet, "/voice", nil, "session-token", "csrf-token"))
	if page.Code != http.StatusOK {
		t.Fatalf("voice page status = %d", page.Code)
	}
	markup := page.Body.String()
	marker := `name="idempotency_key" value="`
	start := strings.Index(markup, marker)
	if start < 0 {
		t.Fatal("voice page is missing the upload idempotency key")
	}
	value := markup[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end != 43 {
		t.Fatalf("voice upload idempotency key length = %d, want 43", end)
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
	if err := writer.WriteField("idempotency_key", "web-test-upload-key-0001"); err != nil {
		t.Fatal(err)
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
