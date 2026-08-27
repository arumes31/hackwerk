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
	user               auth.User
	users              []auth.UserSummary
	session            auth.Session
	revoked            bool
	findError          error
	accessError        error
	updatedAccess      auth.UpdateAccessInput
	updatedDetails     auth.UpdateUserDetailsInput
	updateDetailsError error
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
	if store.users != nil {
		return store.users, nil
	}
	return []auth.UserSummary{{ID: store.user.ID, Username: store.user.Username, DisplayName: store.user.DisplayName, Role: store.user.Role, Active: true, Version: 1}}, nil
}
func (store *identityTestStore) CreateUser(context.Context, auth.Actor, auth.CreateUserInput, string) (string, error) {
	return "created", nil
}
func (store *identityTestStore) UpdateUserDetails(_ context.Context, _ auth.Actor, input auth.UpdateUserDetailsInput) error {
	store.updatedDetails = input
	return store.updateDetailsError
}
func (store *identityTestStore) UpdateUserAccess(_ context.Context, _ auth.Actor, input auth.UpdateAccessInput) error {
	store.updatedAccess = input
	return store.accessError
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
	// #nosec G101 -- deterministic non-secret test fixture password.
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
	rootRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/", nil)
	rootResponse := httptest.NewRecorder()
	router.ServeHTTP(rootResponse, rootRequest)
	if rootResponse.Code != http.StatusSeeOther || rootResponse.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated root = %d, location = %q", rootResponse.Code, rootResponse.Header().Get("Location"))
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
	if len(cookies) != 2 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode ||
		!cookies[1].HttpOnly || cookies[1].SameSite != http.SameSiteStrictMode {
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
	router, err := NewRouter(Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "0.1.23", Commit: "11f91120aeba15b59f7c99d805a4ba08a8906672"}, Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	loginRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test/login", nil)
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, loginRequest)
	loginBody := loginResponse.Body.String()
	if loginResponse.Code != http.StatusOK ||
		!strings.Contains(loginBody, `href="/assets/login.css?v=`) ||
		strings.Contains(loginBody, `href="/assets/login-original.css?v=`) ||
		strings.Contains(loginBody, `src="/assets/login-background-loader.js?v=`) ||
		strings.Contains(loginBody, `class="scene"`) ||
		!strings.Contains(loginBody, `class="login-panel"`) ||
		!strings.Contains(loginBody, `HWK-SYS // V 0.1.23`) ||
		!strings.Contains(loginBody, `ID: 11f9112`) {
		t.Fatalf("login page response = %d %q", loginResponse.Code, loginBody)
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
	missingOrigin := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/login", strings.NewReader(form.Encode()))
	missingOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingOriginResponse := httptest.NewRecorder()
	router.ServeHTTP(missingOriginResponse, missingOrigin)
	if missingOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("missing-origin login response = %d", missingOriginResponse.Code)
	}
}

func TestIdentityHTTPUpdateUserDetails(t *testing.T) {
	tests := []struct {
		name             string
		role             auth.Role
		storeError       error
		expectedStatus   int
		expectedBody     string
		expectedUsername string
	}{
		{
			name: "admin updates details", role: auth.RoleAdmin,
			expectedStatus: http.StatusSeeOther, expectedUsername: "neuer-name",
		},
		{
			name: "driver is forbidden", role: auth.RoleDriver,
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "conflict is visible and retains values", role: auth.RoleAdmin,
			storeError: auth.ErrConflict, expectedStatus: http.StatusConflict,
			expectedBody: "bereits vergeben", expectedUsername: "neuer-name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &identityTestStore{
				updateDetailsError: test.storeError,
				users: []auth.UserSummary{{
					ID: "target", Username: "alt", DisplayName: "Alter Name", Email: "alt@example.test",
					Role: auth.RoleDriver, Active: true, Version: 4, DriverID: "driver-profile",
				}},
			}
			router := identityRouterForMutationTest(t, store, test.role)
			form := url.Values{
				"csrf_token": {"csrf"}, "version": {"4"}, "username": {" neuer-name "},
				"display_name": {" Neuer Anzeigename "}, "email": {" neu@example.test "},
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/admin/users/target/details", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://example.test")
			// #nosec G124 -- request-only test fixture; no cookie is emitted to a browser.
			request.AddCookie(&http.Cookie{Name: "hackplan_session", Value: "session"})
			// #nosec G124 -- request-only test fixture; no cookie is emitted to a browser.
			request.AddCookie(&http.Cookie{Name: "hackplan_csrf", Value: "csrf"})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.expectedStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, test.expectedStatus, response.Body.String())
			}
			if test.expectedBody != "" && !strings.Contains(response.Body.String(), test.expectedBody) {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.expectedBody)
			}
			if test.expectedUsername != "" && store.updatedDetails.Username != test.expectedUsername {
				t.Fatalf("stored username = %q, want %q", store.updatedDetails.Username, test.expectedUsername)
			}
			if test.storeError != nil && !strings.Contains(response.Body.String(), `value="neuer-name"`) {
				t.Fatalf("submitted username was not retained: %q", response.Body.String())
			}
			if test.role == auth.RoleDriver && store.updatedDetails.UserID != "" {
				t.Fatalf("forbidden update reached store: %#v", store.updatedDetails)
			}
		})
	}
}

func TestIdentityHTTPLastAdminErrorIsVisible(t *testing.T) {
	store := &identityTestStore{
		accessError: auth.ErrLastAdmin,
		users: []auth.UserSummary{{
			ID: "admin", Username: "admin", DisplayName: "Anna Admin",
			Role: auth.RoleAdmin, Active: true, Version: 2,
		}},
	}
	router := identityRouterForMutationTest(t, store, auth.RoleAdmin)
	form := url.Values{"csrf_token": {"csrf"}, "version": {"2"}, "role": {"driver"}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/admin/users/admin/access", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://example.test")
	// #nosec G124 -- request-only test fixtures; no cookies are emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "hackplan_session", Value: "session"})
	// #nosec G124 -- request-only test fixtures; no cookies are emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "hackplan_csrf", Value: "csrf"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Mindestens ein aktiver Administrator") ||
		!strings.Contains(response.Body.String(), `role="alert"`) {
		t.Fatalf("last-admin response = %d %q", response.Code, response.Body.String())
	}
}

func identityRouterForMutationTest(t *testing.T, store *identityTestStore, role auth.Role) http.Handler {
	t.Helper()
	now := time.Now()
	store.session = auth.Session{
		ID: "session", Actor: auth.Actor{UserID: "actor", Username: "actor", DisplayName: "Actor", Role: role, UserVersion: 1},
		CSRFTokenHash: auth.TokenHash("csrf"), IdleExpiresAt: now.Add(time.Hour),
		AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true,
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{
		MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(store, hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Dependencies{
		Config: configForWebTest(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
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
