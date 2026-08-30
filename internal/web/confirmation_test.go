package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
)

type confirmationHTTPStore struct {
	value        notification.Confirmation
	lookupErr    error
	lookupCalls  int
	respondErr   error
	respondCalls int
}

func (store *confirmationHTTPStore) Lookup(context.Context, []byte) (notification.Confirmation, error) {
	store.lookupCalls++
	if store.lookupErr != nil {
		return notification.Confirmation{}, store.lookupErr
	}
	return store.value, nil
}

func (store *confirmationHTTPStore) Respond(_ context.Context, _, _ []byte, response notification.Response, _, _ string, _ time.Time) (notification.Confirmation, error) {
	store.respondCalls++
	if store.respondErr != nil {
		return notification.Confirmation{}, store.respondErr
	}
	store.value.Response = response
	store.value.ConfirmationStatus = string(response)
	return store.value, nil
}

func TestConfirmationHTTPHeadersNativeFormOriginAndRedactedLogs(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := notification.DevelopmentKeyRing()
	material, err := ring.Issue("request", "appointment", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationHTTPStore{value: notification.Confirmation{
		RequestID: "request", AppointmentID: "appointment", CustomerName: "Franz <script>", Locality: "Grieskirchen",
		JobNumber: "HW-1", JobType: "chipping_only", VolumeM3: "20", TokenKeyID: notification.DevelopmentKeyID,
		TokenVersion: 1, Status: "active", Lifecycle: "fixed", ConfirmationStatus: "pending",
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(3 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		TokenHash: material.Hash, FormNonceHash: material.NonceHash,
	}}
	service, err := notification.NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	page := templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"}
	limiter := newConfirmationRateLimiter(20, func() time.Time { return now })
	cfg := config.Config{BaseURL: "https://hackwerk.example"}
	router := chi.NewRouter()
	router.Get("/termin/{confirmationToken}", confirmationPage(service, limiter, page, logger))
	router.Post("/termin/{confirmationToken}/antwort", confirmationResponse(service, limiter, cfg, page, logger))

	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/termin/"+material.Raw, nil))
	if getResponse.Code != http.StatusOK || getResponse.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(getResponse.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("confirmation GET status/headers = %d/%v", getResponse.Code, getResponse.Header())
	}
	if strings.Contains(getResponse.Body.String(), "<script>") || strings.Contains(getResponse.Body.String(), "https://") {
		t.Fatalf("confirmation page contains unsafe customer markup or third-party URL: %s", getResponse.Body.String())
	}

	crossSite := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "confirmed")
	crossSite.Header.Set("Origin", "https://attacker.example")
	crossResponse := httptest.NewRecorder()
	router.ServeHTTP(crossResponse, crossSite)
	if crossResponse.Code != http.StatusOK || store.respondCalls != 0 || !strings.Contains(crossResponse.Body.String(), "Link nicht verfügbar") {
		t.Fatalf("cross-site response = %d calls=%d body=%q", crossResponse.Code, store.respondCalls, crossResponse.Body.String())
	}

	noteRequest := nativeConfirmationRequestWithNote(t, material.Raw, material.FormNonce, "confirmed", "Bitte vormittags")
	noteRequest.Header.Set("Origin", "null")
	noteResponse := httptest.NewRecorder()
	router.ServeHTTP(noteResponse, noteRequest)
	if noteResponse.Code != http.StatusUnprocessableEntity || store.respondCalls != 0 || !strings.Contains(noteResponse.Body.String(), "nur mit einer Ablehnung") || !strings.Contains(noteResponse.Body.String(), "Bitte vormittags") || !strings.Contains(noteResponse.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("confirmation note validation = %d calls=%d body=%q", noteResponse.Code, store.respondCalls, noteResponse.Body.String())
	}

	post := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "confirmed")
	post.Header.Set("Origin", "null")
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusOK || store.respondCalls != 1 || !strings.Contains(postResponse.Body.String(), "Termin bestätigt") {
		t.Fatalf("confirmation POST = %d calls=%d body=%q", postResponse.Code, store.respondCalls, postResponse.Body.String())
	}

	store.respondErr = notification.ErrConfirmationUnavailable
	unavailable := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "declined")
	unavailable.Header.Set("Origin", cfg.BaseURL)
	unavailableResponse := httptest.NewRecorder()
	router.ServeHTTP(unavailableResponse, unavailable)
	if unavailableResponse.Code != http.StatusOK || !strings.Contains(unavailableResponse.Body.String(), "Link nicht verfügbar") || strings.Contains(unavailableResponse.Body.String(), "erneut öffnen") {
		t.Fatalf("unavailable confirmation response = %d body=%q", unavailableResponse.Code, unavailableResponse.Body.String())
	}

	store.respondErr = errors.New("database details must stay private")
	failure := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "declined")
	failure.Header.Set("Origin", cfg.BaseURL)
	failureResponse := httptest.NewRecorder()
	router.ServeHTTP(failureResponse, failure)
	if failureResponse.Code != http.StatusServiceUnavailable || failureResponse.Header().Get("Retry-After") != "5" || !strings.Contains(failureResponse.Body.String(), "erneut öffnen") || strings.Contains(failureResponse.Body.String(), "Link nicht verfügbar") {
		t.Fatalf("transient confirmation response = %d body=%q", failureResponse.Code, failureResponse.Body.String())
	}
	for _, private := range []string{material.Raw, material.FormNonce, "Franz <script>", "database details"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("confirmation log leaked %q: %s", private, logs.String())
		}
	}
	for _, private := range []string{material.FormNonce, "Franz <script>", "database details"} {
		if strings.Contains(failureResponse.Body.String(), private) {
			t.Fatalf("confirmation failure page leaked %q: %s", private, failureResponse.Body.String())
		}
	}
}

func TestConfirmationResponseLockedRendersStoredAnswer(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := notification.DevelopmentKeyRing()
	material, err := ring.Issue("locked-request", "locked-appointment", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationHTTPStore{
		value: notification.Confirmation{
			RequestID: "locked-request", AppointmentID: "locked-appointment", CustomerName: "Nicht ausgeben",
			TokenKeyID: notification.DevelopmentKeyID, TokenVersion: 1, Status: "active", Lifecycle: "fixed",
			ConfirmationStatus: "confirmed", Response: notification.ResponseConfirmed, ExpiresAt: now.Add(24 * time.Hour),
			TokenHash: material.Hash, FormNonceHash: material.NonceHash,
		},
		respondErr: notification.ErrResponseLocked,
	}
	service, err := notification.NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	router := chi.NewRouter()
	router.Post("/termin/{confirmationToken}/antwort", confirmationResponse(service, newConfirmationRateLimiter(20, func() time.Time { return now }), config.Config{BaseURL: "https://hackwerk.example"}, templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"}, slog.New(slog.NewTextHandler(&logs, nil))))

	request := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "declined")
	request.Header.Set("Origin", "https://hackwerk.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || store.respondCalls != 1 || store.lookupCalls != 1 {
		t.Fatalf("locked response = %d respond=%d lookup=%d", response.Code, store.respondCalls, store.lookupCalls)
	}
	if !strings.Contains(body, "Antwort bereits gespeichert") || !strings.Contains(body, "Termin bestätigt") || strings.Contains(body, "Termin abgelehnt") || strings.Contains(body, "Link nicht verfügbar") {
		t.Fatalf("locked response did not render stored answer: %q", body)
	}
	for _, private := range []string{material.Raw, material.FormNonce, "Nicht ausgeben"} {
		if strings.Contains(logs.String(), private) || strings.Contains(body, private) {
			t.Fatalf("locked response leaked %q: log=%s body=%s", private, logs.String(), body)
		}
	}
}

func TestConfirmationUnavailableExpiredAndRevokedLookIdentical(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := notification.DevelopmentKeyRing()
	material, err := ring.Issue("oracle-request", "oracle-appointment", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationHTTPStore{value: notification.Confirmation{
		RequestID: "oracle-request", AppointmentID: "oracle-appointment", TokenKeyID: notification.DevelopmentKeyID,
		TokenVersion: 1, Status: "active", Lifecycle: "fixed", ExpiresAt: now.Add(time.Hour), TokenHash: material.Hash, FormNonceHash: material.NonceHash,
	}}
	service, err := notification.NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/termin/{confirmationToken}", confirmationPage(service, newConfirmationRateLimiter(50, func() time.Time { return now }), templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"}, slog.New(slog.NewTextHandler(io.Discard, nil))))

	renderPage := func(token string) string {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/termin/"+token, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("confirmation status = %d", response.Code)
		}
		return response.Body.String()
	}
	invalid := renderPage("invalid")
	store.value.Status = "active"
	store.value.ExpiresAt = now
	expired := renderPage(material.Raw)
	store.value.Status = "revoked"
	store.value.ExpiresAt = now.Add(time.Hour)
	revoked := renderPage(material.Raw)
	if invalid != expired || invalid != revoked || !strings.Contains(invalid, "Link nicht verfügbar") {
		t.Fatalf("public token states differ: invalid=%q expired=%q revoked=%q", invalid, expired, revoked)
	}
}

func TestConfirmationResponseLockedUsesSafeFallbackWhenViewFails(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := notification.DevelopmentKeyRing()
	material, err := ring.Issue("fallback-request", "fallback-appointment", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationHTTPStore{
		respondErr: notification.ErrResponseLocked,
		lookupErr:  errors.New("private lookup failure"),
	}
	service, err := notification.NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	router := chi.NewRouter()
	router.Post("/termin/{confirmationToken}/antwort", confirmationResponse(service, newConfirmationRateLimiter(20, func() time.Time { return now }), config.Config{BaseURL: "https://hackwerk.example"}, templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"}, slog.New(slog.NewTextHandler(&logs, nil))))

	request := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "declined")
	request.Header.Set("Origin", "https://hackwerk.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || store.respondCalls != 1 || store.lookupCalls != 1 || !strings.Contains(body, "Antwort bereits gespeichert") || !strings.Contains(body, "bereits eine Rückmeldung") {
		t.Fatalf("locked fallback = %d respond=%d lookup=%d body=%q", response.Code, store.respondCalls, store.lookupCalls, body)
	}
	for _, private := range []string{material.Raw, material.FormNonce, "private lookup failure"} {
		if strings.Contains(logs.String(), private) || strings.Contains(body, private) {
			t.Fatalf("locked fallback leaked %q: log=%s body=%s", private, logs.String(), body)
		}
	}
}

func TestConfirmationResponseIdempotentSameAnswer(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := notification.DevelopmentKeyRing()
	material, err := ring.Issue("same-request", "same-appointment", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationHTTPStore{value: notification.Confirmation{
		RequestID: "same-request", AppointmentID: "same-appointment", TokenKeyID: notification.DevelopmentKeyID,
		TokenVersion: 1, Status: "active", Lifecycle: "fixed", ConfirmationStatus: "confirmed",
		Response: notification.ResponseConfirmed, ExpiresAt: now.Add(24 * time.Hour), TokenHash: material.Hash, FormNonceHash: material.NonceHash,
	}}
	service, err := notification.NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/termin/{confirmationToken}/antwort", confirmationResponse(service, newConfirmationRateLimiter(20, func() time.Time { return now }), config.Config{BaseURL: "https://hackwerk.example"}, templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))

	request := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "confirmed")
	request.Header.Set("Origin", "https://hackwerk.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.respondCalls != 1 || store.lookupCalls != 0 || !strings.Contains(response.Body.String(), "Termin bestätigt") || strings.Contains(response.Body.String(), "Link nicht verfügbar") {
		t.Fatalf("idempotent response = %d respond=%d lookup=%d body=%q", response.Code, store.respondCalls, store.lookupCalls, response.Body.String())
	}
}

func nativeConfirmationRequest(t *testing.T, rawToken, nonce, action string) *http.Request {
	return nativeConfirmationRequestWithNote(t, rawToken, nonce, action, "")
}

func nativeConfirmationRequestWithNote(t *testing.T, rawToken, nonce, action, note string) *http.Request {
	t.Helper()
	body := url.Values{"form_nonce": {nonce}, "action": {action}, "response_note": {note}}.Encode()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/termin/"+rawToken+"/antwort", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}
