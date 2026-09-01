package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

type profileTestStore struct {
	*identityTestStore
	profile               auth.Profile
	profileError          error
	mutationError         error
	pending               auth.EmailVerification
	totp                  auth.TOTPCredential
	credentials           []auth.StoredWebAuthnCredential
	registrationChallenge []byte
	registrationExpiresAt time.Time
	login                 auth.LoginChallenge
	completedLogin        auth.CompleteLoginInput
}

func (store *profileTestStore) LoadProfile(context.Context, string, time.Time) (auth.Profile, error) {
	return store.profile, store.profileError
}
func (store *profileTestStore) UpdateOwnProfile(context.Context, auth.Actor, auth.UpdateOwnProfileInput) error {
	return store.mutationError
}
func (store *profileTestStore) StartEmailVerification(_ context.Context, _ auth.Actor, verification auth.EmailVerification, _ int32, _ string) error {
	if store.mutationError == nil {
		store.pending = verification
	}
	return store.mutationError
}
func (store *profileTestStore) PendingEmailVerification(context.Context, string) (auth.EmailVerification, error) {
	if store.mutationError != nil {
		return auth.EmailVerification{}, store.mutationError
	}
	return store.pending, nil
}
func (store *profileTestStore) ResendEmailVerification(_ context.Context, _ auth.Actor, verification auth.EmailVerification, _ time.Time, _ int32, _ string) error {
	store.pending = verification
	return store.mutationError
}
func (store *profileTestStore) CancelEmailVerification(context.Context, auth.Actor, string) error {
	return store.mutationError
}
func (store *profileTestStore) VerifyEmail(context.Context, []byte, time.Time, string) error {
	return store.mutationError
}
func (store *profileTestStore) UpsertTOTPEnrollment(_ context.Context, _ auth.Actor, credential auth.TOTPCredential, _ string) error {
	store.totp = credential
	return store.mutationError
}
func (store *profileTestStore) TOTPCredential(context.Context, string) (auth.TOTPCredential, error) {
	if store.mutationError != nil {
		return auth.TOTPCredential{}, store.mutationError
	}
	return store.totp, nil
}
func (store *profileTestStore) EnableTOTP(context.Context, auth.Actor, [][]byte, string) error {
	return store.mutationError
}
func (store *profileTestStore) RenameTOTP(context.Context, auth.Actor, string, string) error {
	return store.mutationError
}
func (store *profileTestStore) DeleteTOTP(context.Context, auth.Actor, string) error {
	return store.mutationError
}
func (store *profileTestStore) WebAuthnCredentials(context.Context, string) ([]auth.StoredWebAuthnCredential, error) {
	return store.credentials, nil
}
func (store *profileTestStore) SaveWebAuthnRegistrationChallenge(_ context.Context, _ auth.Actor, _ string, challenge []byte, expiresAt time.Time) error {
	store.registrationChallenge, store.registrationExpiresAt = challenge, expiresAt
	return store.mutationError
}
func (store *profileTestStore) WebAuthnRegistrationChallenge(context.Context, auth.Actor, string) ([]byte, time.Time, error) {
	return store.registrationChallenge, store.registrationExpiresAt, store.mutationError
}
func (store *profileTestStore) AddWebAuthnCredential(context.Context, auth.Actor, string, auth.StoredWebAuthnCredential, [][]byte, string) error {
	return store.mutationError
}
func (store *profileTestStore) RenameWebAuthnCredential(context.Context, auth.Actor, []byte, string, string) error {
	return store.mutationError
}
func (store *profileTestStore) DeleteWebAuthnCredential(context.Context, auth.Actor, []byte, string) error {
	return store.mutationError
}
func (store *profileTestStore) ReplaceRecoveryCodes(context.Context, auth.Actor, [][]byte, string) error {
	return store.mutationError
}
func (store *profileTestStore) StartLoginChallenge(_ context.Context, user auth.User, _ []byte, expiresAt time.Time, _, _ []byte, _ string) error {
	store.login = auth.LoginChallenge{User: user, ExpiresAt: expiresAt}
	return store.mutationError
}
func (store *profileTestStore) LoginChallenge(context.Context, []byte) (auth.LoginChallenge, error) {
	if store.mutationError != nil {
		return auth.LoginChallenge{}, store.mutationError
	}
	return store.login, nil
}
func (store *profileTestStore) SetLoginWebAuthnSession(_ context.Context, _ []byte, session []byte) error {
	store.login.WebAuthnSession = session
	return store.mutationError
}
func (store *profileTestStore) RecordLoginChallengeFailure(context.Context, []byte) error { return nil }
func (store *profileTestStore) CompleteLogin(_ context.Context, input auth.CompleteLoginInput) error {
	store.completedLogin = input
	return store.mutationError
}
func (store *profileTestStore) RevokeOwnedSession(context.Context, auth.Actor, string, string) error {
	return store.mutationError
}
func (store *profileTestStore) RevokeAllSessions(context.Context, auth.Actor, string) error {
	return store.mutationError
}
func (store *profileTestStore) ResetUserSecurity(context.Context, auth.Actor, string, int32, string) error {
	return store.mutationError
}

type profileTestFixture struct {
	identity     *auth.Service
	store        *profileTestStore
	dependencies Dependencies
	page         templates.PageData
	session      auth.Session
	now          time.Time
}

func newProfileTestFixture(t *testing.T) profileTestFixture {
	t.Helper()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	user := auth.User{
		ID: "user-id", Username: "maria", DisplayName: "Maria Muster", Email: "maria@example.at",
		Role: auth.RoleDriver, Active: true, Version: 3, WebAuthnUserHandle: bytes.Repeat([]byte{0x44}, 32),
	}
	store := &profileTestStore{identityTestStore: &identityTestStore{user: user}}
	store.profile = auth.Profile{User: user, Sessions: []auth.ActiveSession{{ID: "session-id", DeviceLabel: "Chrome auf Windows"}}}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(store, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewSecurityKeyRing(map[string]string{
		"test-v1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 32)),
	}, "test-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.ConfigureSecurity(auth.SecurityConfig{
		Keys: keys, AppName: "HackWerk", BaseURL: "https://example.test",
		EmailVerificationTTL: 24 * time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: 5 * time.Minute, WebAuthnChallengeTTL: 5 * time.Minute, MailMaxAttempts: 6,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := configForWebTest()
	cfg.Auth.MFACookieName = "hackplan_mfa"
	return profileTestFixture{
		identity: identity, store: store,
		dependencies: Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		page:         templates.PageData{AppName: "HackWerk", CSSPath: "/assets/app.css"},
		session: auth.Session{ID: "session-id", Actor: auth.Actor{
			UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, UserVersion: user.Version,
		}},
		now: now,
	}
}

func (fixture profileTestFixture) request(t *testing.T, method, target string, form url.Values) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	request.Form = form
	request.PostForm = form
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, fixture.session))
	return request
}

func TestProfileHandlersRenderAndMutate(t *testing.T) {
	fixture := newProfileTestFixture(t)
	fixture.store.profile.Passkeys = []auth.SecurityMethod{{EncodedID: "credential-safe-id", Name: "Dienstschlüssel", CreatedAt: fixture.now}}
	pageResponse := httptest.NewRecorder()
	profilePage(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(pageResponse, fixture.request(t, http.MethodGet, "https://example.test/profile?status=profile_saved", nil))
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "Persönliche Daten gespeichert") ||
		!strings.Contains(pageResponse.Body.String(), `label class="visually-hidden" for="passkey-rename-credential-safe-id"`) ||
		!strings.Contains(pageResponse.Body.String(), `id="passkey-rename-credential-safe-id" name="name"`) {
		t.Fatalf("profile page = %d %q", pageResponse.Code, pageResponse.Body.String())
	}

	mutations := []struct {
		name    string
		handler http.Handler
		form    url.Values
		want    string
	}{
		{name: "profile", handler: updateOwnProfile(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"version": {"3"}, "display_name": {"Maria"}, "salutation": {"frau"}, "work_phone": {"06641234567"}}, want: "/profile?status=profile_saved"},
		{name: "email", handler: requestProfileEmail(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"email": {"neu@example.at"}}, want: "/profile?status=email_requested"},
		{name: "resend", handler: resendProfileEmail(fixture.identity, fixture.dependencies, fixture.page), want: "/profile?status=email_resent"},
		{name: "cancel", handler: cancelProfileEmail(fixture.identity, fixture.dependencies, fixture.page), want: "/profile?status=email_cancelled"},
		{name: "rename TOTP", handler: renameTOTP(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"name": {"Telefon"}}, want: "/profile?status=totp_renamed"},
		{name: "delete TOTP", handler: deleteTOTP(fixture.identity, fixture.dependencies, fixture.page), want: "/profile?status=totp_deleted"},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile", test.form))
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != test.want {
				t.Fatalf("response = %d, location %q", response.Code, response.Header().Get("Location"))
			}
		})
	}

	fixture.store.mutationError = auth.ErrConflict
	response := httptest.NewRecorder()
	updateOwnProfile(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile", url.Values{"version": {"3"}, "display_name": {"Maria"}}))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "zwischenzeitlich geändert") {
		t.Fatalf("profile conflict = %d %q", response.Code, response.Body.String())
	}
	fixture.store.profileError = errors.New("database unavailable")
	response = httptest.NewRecorder()
	profilePage(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodGet, "https://example.test/profile", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("profile load error status = %d", response.Code)
	}
}

func TestProfileEmailVerificationTOTPAndRecoveryHandlers(t *testing.T) {
	fixture := newProfileTestFixture(t)
	fixture.store.pending = auth.EmailVerification{ID: "verification", UserID: "user-id", TokenVersion: 1}
	response := httptest.NewRecorder()
	verifyProfileEmail(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodGet, "https://example.test/profile/email/verify?token=opaque", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/profile?status=email_verified" {
		t.Fatalf("verify email = %d %q", response.Code, response.Header().Get("Location"))
	}

	anonymous := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/profile/email/verify?token=opaque", nil)
	response = httptest.NewRecorder()
	verifyProfileEmail(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, anonymous)
	if response.Header().Get("Location") != "/login?email_verified=1" {
		t.Fatalf("anonymous verification location = %q", response.Header().Get("Location"))
	}
	fixture.store.mutationError = auth.ErrVerificationExpired
	response = httptest.NewRecorder()
	verifyProfileEmail(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, anonymous)
	if response.Code != http.StatusGone || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("expired verification = %d", response.Code)
	}
	fixture.store.mutationError = nil

	response = httptest.NewRecorder()
	beginTOTPEnrollment(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/totp", url.Values{"name": {"Telefon"}}))
	if response.Code != http.StatusOK || fixture.store.totp.Name != "Telefon" {
		t.Fatalf("begin TOTP = %d, %#v", response.Code, fixture.store.totp)
	}
	enrollment, err := fixture.identity.BeginTOTPEnrollment(t.Context(), fixture.session.Actor, "Telefon", "request")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCodeCustom(enrollment.Secret, fixture.now, totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	confirmTOTPEnrollment(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/totp/confirm", url.Values{"code": {code}}))
	if response.Code != http.StatusOK || !containsAll(response.Body.String(), "Recovery-Codes", "Profilbereiche", `href="#personal"`, `href="#security"`, `href="#sessions"`, "Codes kopieren", "Codes drucken") {
		t.Fatalf("confirm TOTP = %d %q", response.Code, response.Body.String())
	}
	enabled := fixture.now
	fixture.store.profile.TOTPEnabledAt = &enabled
	response = httptest.NewRecorder()
	rotateRecoveryCodes(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/recovery", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Neue Recovery-Codes") {
		t.Fatalf("rotate recovery = %d", response.Code)
	}
}

func TestProfilePasskeySessionAndMFAHandlers(t *testing.T) {
	fixture := newProfileTestFixture(t)
	response := httptest.NewRecorder()
	beginPasskeyRegistration(fixture.identity).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/passkeys/begin", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("begin passkey = %d %q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	finishPasskeyRegistration(fixture.identity).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/passkeys/finish", nil))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid passkey finish = %d", response.Code)
	}

	validID := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32))
	for _, test := range []struct {
		name    string
		handler http.Handler
		param   string
		want    string
	}{
		{name: "rename", handler: renamePasskey(fixture.identity, fixture.dependencies, fixture.page), param: validID, want: "/profile?status=passkey_renamed"},
		{name: "delete", handler: deletePasskey(fixture.identity, fixture.dependencies, fixture.page), param: validID, want: "/profile?status=passkey_deleted"},
		{name: "revoke other", handler: revokeProfileSession(fixture.identity, fixture.dependencies, fixture.page), param: "other-session", want: "/profile?status=session_revoked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request(t, http.MethodPost, "https://example.test/profile", url.Values{"name": {"Laptop"}})
			routeContext := chi.NewRouteContext()
			routeContext.URLParams.Add("credentialID", test.param)
			routeContext.URLParams.Add("sessionID", test.param)
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, request)
			if response.Header().Get("Location") != test.want {
				t.Fatalf("location = %q", response.Header().Get("Location"))
			}
		})
	}

	request := fixture.request(t, http.MethodPost, "https://example.test/profile/sessions/session-id", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("sessionID", "session-id")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response = httptest.NewRecorder()
	revokeProfileSession(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, request)
	if response.Header().Get("Location") != "/login" {
		t.Fatalf("current session revoke = %q", response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	revokeAllProfileSessions(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile/sessions", nil))
	if response.Header().Get("Location") != "/login" {
		t.Fatalf("revoke all location = %q", response.Header().Get("Location"))
	}

	fixture.store.user.TOTPEnabled = true
	fixture.store.login = auth.LoginChallenge{User: fixture.store.user, ExpiresAt: fixture.now.Add(time.Minute)}
	mfaRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/mfa", nil)
	mfaRequest.AddCookie(&http.Cookie{Name: fixture.dependencies.Config.Auth.MFACookieName, Value: "challenge", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	response = httptest.NewRecorder()
	mfaPage(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, mfaRequest)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Sicherheitsprüfung") ||
		!strings.Contains(response.Body.String(), `href="/cookies"`) ||
		!strings.Contains(response.Body.String(), `href="/cookies" data-privacy-notice-open`) ||
		!strings.Contains(response.Body.String(), `data-privacy-notice`) {
		t.Fatalf("MFA page = %d %q", response.Code, response.Body.String())
	}

	form := url.Values{"method": {"recovery"}, "code": {"ABCD-EFGH-JKLM"}}
	mfaRequest = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/mfa", strings.NewReader(form.Encode()))
	mfaRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mfaRequest.Header.Set("Origin", "https://example.test")
	mfaRequest.AddCookie(&http.Cookie{Name: fixture.dependencies.Config.Auth.MFACookieName, Value: "challenge", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	response = httptest.NewRecorder()
	completeMFALogin(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, mfaRequest)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
		t.Fatalf("complete MFA = %d %q", response.Code, response.Header().Get("Location"))
	}

	fixture.store.mutationError = errors.New("unavailable")
	response = httptest.NewRecorder()
	beginPasskeyLogin(fixture.identity, fixture.dependencies).ServeHTTP(response, mfaRequest)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("failed passkey login = %d", response.Code)
	}
}

func TestProfileHandlerFailureStatesAndAdminReset(t *testing.T) {
	fixture := newProfileTestFixture(t)
	fixture.store.mutationError = auth.ErrRateLimited
	for _, test := range []struct {
		name    string
		handler http.Handler
		form    url.Values
		status  int
	}{
		{name: "email request", handler: requestProfileEmail(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"email": {"invalid"}}, status: http.StatusUnprocessableEntity},
		{name: "email resend", handler: resendProfileEmail(fixture.identity, fixture.dependencies, fixture.page), status: http.StatusTooManyRequests},
		{name: "email cancel", handler: cancelProfileEmail(fixture.identity, fixture.dependencies, fixture.page), status: http.StatusConflict},
		{name: "TOTP begin", handler: beginTOTPEnrollment(fixture.identity, fixture.dependencies, fixture.page), status: http.StatusUnprocessableEntity},
		{name: "TOTP confirm", handler: confirmTOTPEnrollment(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"code": {"000000"}}, status: http.StatusUnprocessableEntity},
		{name: "recovery rotation", handler: rotateRecoveryCodes(fixture.identity, fixture.dependencies, fixture.page), status: http.StatusUnprocessableEntity},
		{name: "profile mutation", handler: renameTOTP(fixture.identity, fixture.dependencies, fixture.page), form: url.Values{"name": {"Telefon"}}, status: http.StatusUnprocessableEntity},
		{name: "session revoke all", handler: revokeAllProfileSessions(fixture.identity, fixture.dependencies, fixture.page), status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, fixture.request(t, http.MethodPost, "https://example.test/profile", test.form))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}

	request := fixture.request(t, http.MethodPost, "https://example.test/profile/sessions/missing", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("sessionID", "missing")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response := httptest.NewRecorder()
	revokeProfileSession(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing session status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	mfaPage(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/login/mfa", nil))
	if response.Header().Get("Location") != "/login" {
		t.Fatalf("MFA without cookie location = %q", response.Header().Get("Location"))
	}
	crossSite := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/login/mfa", nil)
	crossSite.Header.Set("Origin", "https://attacker.example")
	response = httptest.NewRecorder()
	completeMFALogin(fixture.identity, fixture.dependencies, fixture.page).ServeHTTP(response, crossSite)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Sicherheitsprüfung ist fehlgeschlagen") {
		t.Fatalf("cross-site MFA = %d %q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	beginPasskeyLogin(fixture.identity, fixture.dependencies).ServeHTTP(response, crossSite)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site passkey begin = %d", response.Code)
	}
	response = httptest.NewRecorder()
	finishPasskeyLogin(fixture.identity, fixture.dependencies).ServeHTTP(response, crossSite)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site passkey finish = %d", response.Code)
	}

	fixture.store.mutationError = nil
	adminSession := fixture.session
	adminSession.Actor = auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	request = fixture.request(t, http.MethodPost, "https://example.test/admin/users/user-id/security/reset", url.Values{"version": {"3"}})
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, adminSession))
	routeContext = chi.NewRouteContext()
	routeContext.URLParams.Add("userID", "user-id")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response = httptest.NewRecorder()
	resetUserSecurity(fixture.identity, fixture.page, fixture.dependencies.Config.Auth.CSRFCookieName, fixture.dependencies.Logger).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/users" {
		t.Fatalf("admin reset = %d %q", response.Code, response.Header().Get("Location"))
	}

	request.Form.Set("version", "invalid")
	response = httptest.NewRecorder()
	resetUserSecurity(fixture.identity, fixture.page, fixture.dependencies.Config.Auth.CSRFCookieName, fixture.dependencies.Logger).ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("invalid admin reset = %d", response.Code)
	}
}

func TestProfileRedirectAndNoticeHelpers(t *testing.T) {
	if mfaRedirect(auth.SessionTokens{Actor: auth.Actor{MustChangePassword: true}}) != "/password" ||
		mfaRedirect(auth.SessionTokens{}) != "/dashboard" {
		t.Fatal("unexpected MFA redirect")
	}
	if profileNotice("email_verified") == "" || profileNotice("unknown") != "" {
		t.Fatal("unexpected profile notice")
	}
}
