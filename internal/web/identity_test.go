package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
)

type identityTestStore struct {
	user      auth.User
	session   auth.Session
	revoked   bool
	findError error
}

func (store *identityTestStore) FindUserByUsername(context.Context, string) (auth.User, error) {
	return store.user, store.findError
}
func (store *identityTestStore) FindUserByID(context.Context, string) (auth.User, error) {
	return store.user, store.findError
}
func (store *identityTestStore) RotateLogin(_ context.Context, user auth.User, session auth.NewSession, _ []byte, _ []byte, _ string) error {
	store.session = auth.Session{
		ID: "session-id", Actor: auth.Actor{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, UserVersion: user.Version},
		CSRFTokenHash: session.CSRFTokenHash, IdleExpiresAt: session.IdleExpiresAt, AbsoluteExpiresAt: session.AbsoluteExpiresAt, UserActive: true,
	}
	return nil
}
func (store *identityTestStore) FindSession(context.Context, []byte) (auth.Session, error) {
	if store.session.ID == "" {
		return auth.Session{}, auth.ErrNotFound
	}
	return store.session, nil
}
func (store *identityTestStore) TouchSession(context.Context, string, time.Time) error { return nil }
func (store *identityTestStore) RevokeSession(context.Context, []byte) error {
	store.revoked = true
	return nil
}
func (store *identityTestStore) LoginRate(context.Context, []byte) (auth.RateLimit, error) {
	return auth.RateLimit{}, nil
}
func (store *identityTestStore) RecordLoginFailure(context.Context, []byte) error { return nil }
func (store *identityTestStore) ListUsers(context.Context) ([]auth.UserSummary, error) {
	return []auth.UserSummary{{ID: store.user.ID, Username: store.user.Username, DisplayName: store.user.DisplayName, Role: store.user.Role, Active: true, Version: 1}}, nil
}
func (store *identityTestStore) CreateUser(context.Context, auth.Actor, auth.CreateUserInput, string) (string, error) {
	return "created", nil
}
func (store *identityTestStore) UpdateUserAccess(context.Context, auth.Actor, auth.UpdateAccessInput) error {
	return nil
}
func (store *identityTestStore) ResetPassword(context.Context, auth.Actor, auth.ResetPasswordInput, string) error {
	return nil
}
func (store *identityTestStore) ChangeOwnPassword(context.Context, auth.Actor, string, int32) error {
	return nil
}

func TestIdentityHTTPLoginCSRFAndDriverGate(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &identityTestStore{}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	password := "Ein sicheres Fahrerpasswort 2026"
	passwordHash, err := hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	store.user = auth.User{ID: "user-id", Username: "fahrer", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, PasswordHash: passwordHash, Active: true, Version: 1}
	identity, err := auth.NewService(store, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	cfg := configForWebTest()
	router, err := NewRouter(Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	loginPageRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/login", nil)
	loginPageResponse := httptest.NewRecorder()
	router.ServeHTTP(loginPageResponse, loginPageRequest)
	if loginPageResponse.Code != http.StatusOK || loginPageResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login page status = %d, cache-control = %q", loginPageResponse.Code, loginPageResponse.Header().Get("Cache-Control"))
	}

	loginForm := url.Values{"username": {"fahrer"}, "password": {password}}
	loginRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/login", strings.NewReader(loginForm.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRequest.Header.Set("Origin", "https://example.test")
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body = %q", loginResponse.Code, loginResponse.Body.String())
	}
	result := loginResponse.Result()
	cookies := result.Cookies()
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 2 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[1].HttpOnly {
		t.Fatalf("login cookies = %#v", cookies)
	}

	csrfMissing := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/logout", strings.NewReader(""))
	csrfMissing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	csrfMissing.Header.Set("Origin", "https://example.test")
	for _, cookie := range cookies {
		csrfMissing.AddCookie(cookie)
	}
	csrfResponse := httptest.NewRecorder()
	router.ServeHTTP(csrfResponse, csrfMissing)
	if csrfResponse.Code != http.StatusForbidden || store.revoked {
		t.Fatalf("missing CSRF status = %d, revoked = %v", csrfResponse.Code, store.revoked)
	}

	csrfToken := cookies[1].Value
	accessForm := url.Values{"csrf_token": {csrfToken}, "version": {"1"}, "role": {"admin"}, "active": {"true"}}
	accessRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/admin/users/other/access", strings.NewReader(accessForm.Encode()))
	accessRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	accessRequest.Header.Set("Origin", "https://example.test")
	for _, cookie := range cookies {
		accessRequest.AddCookie(cookie)
	}
	accessResponse := httptest.NewRecorder()
	router.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusForbidden {
		t.Fatalf("driver admin status = %d", accessResponse.Code)
	}
}

func TestIdentityHTTPGenericLoginError(t *testing.T) {
	store := &identityTestStore{findError: auth.ErrNotFound}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(store, hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	cfg := configForWebTest()
	router, err := NewRouter(Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"username": {"nicht-vorhanden"}, "password": {"Falsches Passwort 2026"}}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://example.test")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), genericLoginError) {
		t.Fatalf("generic login response = %d %q", response.Code, response.Body.String())
	}
}

func configForWebTest() config.Config {
	return config.Config{
		AppName: "HackWerk", BaseURL: "https://example.test",
		Database: config.Database{ReadinessTimeout: time.Second},
		Auth: config.Auth{
			SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour, CookieSecure: true,
		},
	}
}
