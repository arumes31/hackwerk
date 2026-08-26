package web

import (
	"bytes"
	"context"
	"errors"
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
	respondErr   error
	respondCalls int
}

func (store *confirmationHTTPStore) Lookup(context.Context, []byte) (notification.Confirmation, error) {
	return store.value, nil
}

func (store *confirmationHTTPStore) Respond(_ context.Context, _, _ []byte, response notification.Response, _ string, _ time.Time) (notification.Confirmation, error) {
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

	post := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "confirmed")
	post.Header.Set("Origin", "null")
	postResponse := httptest.NewRecorder()
	router.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusOK || store.respondCalls != 1 || !strings.Contains(postResponse.Body.String(), "Termin bestätigt") {
		t.Fatalf("confirmation POST = %d calls=%d body=%q", postResponse.Code, store.respondCalls, postResponse.Body.String())
	}

	store.respondErr = errors.New("database details must stay private")
	failure := nativeConfirmationRequest(t, material.Raw, material.FormNonce, "declined")
	failure.Header.Set("Origin", cfg.BaseURL)
	router.ServeHTTP(httptest.NewRecorder(), failure)
	for _, private := range []string{material.Raw, material.FormNonce, "Franz <script>", "database details"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("confirmation log leaked %q: %s", private, logs.String())
		}
	}
}

func nativeConfirmationRequest(t *testing.T, rawToken, nonce, action string) *http.Request {
	t.Helper()
	body := url.Values{"form_nonce": {nonce}, "action": {action}}.Encode()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/termin/"+rawToken+"/antwort", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}
