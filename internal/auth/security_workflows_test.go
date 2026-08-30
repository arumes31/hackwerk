package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

type fakeSecurityStore struct {
	*fakeStore
	profile               Profile
	profileError          error
	updatedProfile        UpdateOwnProfileInput
	verification          EmailVerification
	pendingVerification   EmailVerification
	totp                  TOTPCredential
	recoveryHashes        [][]byte
	credentials           []StoredWebAuthnCredential
	registrationChallenge []byte
	registrationExpiresAt time.Time
	login                 LoginChallenge
	loginSession          []byte
	completedLogin        CompleteLoginInput
	loginFailures         int
	revokedSessionID      string
	revokedAll            bool
	resetUserID           string
}

func (store *fakeSecurityStore) LoadProfile(context.Context, string, time.Time) (Profile, error) {
	return store.profile, store.profileError
}

func (store *fakeSecurityStore) UpdateOwnProfile(_ context.Context, _ Actor, input UpdateOwnProfileInput) error {
	store.updatedProfile = input
	return nil
}

func (store *fakeSecurityStore) StartEmailVerification(_ context.Context, _ Actor, verification EmailVerification, _ int32, _ string) error {
	store.verification = verification
	store.pendingVerification = verification
	return nil
}

func (store *fakeSecurityStore) PendingEmailVerification(context.Context, string) (EmailVerification, error) {
	if store.pendingVerification.ID == "" {
		return EmailVerification{}, ErrNotFound
	}
	return store.pendingVerification, nil
}

func (store *fakeSecurityStore) ResendEmailVerification(_ context.Context, _ Actor, verification EmailVerification, _ time.Time, _ int32, _ string) error {
	store.pendingVerification = verification
	return nil
}

func (store *fakeSecurityStore) CancelEmailVerification(context.Context, Actor, string) error {
	store.pendingVerification = EmailVerification{}
	return nil
}

func (store *fakeSecurityStore) VerifyEmail(_ context.Context, hash []byte, _ time.Time, _ string) error {
	if !bytes.Equal(hash, store.pendingVerification.TokenHash) {
		return ErrNotFound
	}
	return nil
}

func (store *fakeSecurityStore) UpsertTOTPEnrollment(_ context.Context, _ Actor, credential TOTPCredential, _ string) error {
	store.totp = credential
	return nil
}

func (store *fakeSecurityStore) TOTPCredential(context.Context, string) (TOTPCredential, error) {
	if store.totp.Name == "" {
		return TOTPCredential{}, ErrNotFound
	}
	return store.totp, nil
}

func (store *fakeSecurityStore) EnableTOTP(_ context.Context, _ Actor, hashes [][]byte, _ string) error {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	store.totp.EnabledAt = &now
	store.recoveryHashes = hashes
	return nil
}

func (store *fakeSecurityStore) RenameTOTP(_ context.Context, _ Actor, name, _ string) error {
	store.totp.Name = name
	return nil
}

func (store *fakeSecurityStore) DeleteTOTP(context.Context, Actor, string) error {
	store.totp = TOTPCredential{}
	return nil
}

func (store *fakeSecurityStore) WebAuthnCredentials(context.Context, string) ([]StoredWebAuthnCredential, error) {
	return store.credentials, nil
}

func (store *fakeSecurityStore) SaveWebAuthnRegistrationChallenge(_ context.Context, _ Actor, _ string, challenge []byte, expiresAt time.Time) error {
	store.registrationChallenge = challenge
	store.registrationExpiresAt = expiresAt
	return nil
}

func (store *fakeSecurityStore) WebAuthnRegistrationChallenge(context.Context, Actor, string) ([]byte, time.Time, error) {
	return store.registrationChallenge, store.registrationExpiresAt, nil
}

func (store *fakeSecurityStore) AddWebAuthnCredential(_ context.Context, _ Actor, _ string, credential StoredWebAuthnCredential, hashes [][]byte, _ string) error {
	store.credentials = append(store.credentials, credential)
	store.recoveryHashes = hashes
	return nil
}

func (store *fakeSecurityStore) RenameWebAuthnCredential(_ context.Context, _ Actor, id []byte, name, _ string) error {
	for index := range store.credentials {
		if bytes.Equal(store.credentials[index].ID, id) {
			store.credentials[index].Name = name
		}
	}
	return nil
}

func (store *fakeSecurityStore) DeleteWebAuthnCredential(_ context.Context, _ Actor, id []byte, _ string) error {
	for index := range store.credentials {
		if bytes.Equal(store.credentials[index].ID, id) {
			store.credentials = append(store.credentials[:index], store.credentials[index+1:]...)
			break
		}
	}
	return nil
}

func (store *fakeSecurityStore) ReplaceRecoveryCodes(_ context.Context, _ Actor, hashes [][]byte, _ string) error {
	store.recoveryHashes = hashes
	return nil
}

func (store *fakeSecurityStore) StartLoginChallenge(_ context.Context, user User, hash []byte, expiresAt time.Time, _, _ []byte, _ string) error {
	store.login = LoginChallenge{ID: base64.RawURLEncoding.EncodeToString(hash), User: user, ExpiresAt: expiresAt}
	return nil
}

func (store *fakeSecurityStore) LoginChallenge(context.Context, []byte) (LoginChallenge, error) {
	if store.login.User.ID == "" {
		return LoginChallenge{}, ErrNotFound
	}
	return store.login, nil
}

func (store *fakeSecurityStore) SetLoginWebAuthnSession(_ context.Context, _ []byte, session []byte) error {
	store.loginSession = session
	store.login.WebAuthnSession = session
	return nil
}

func (store *fakeSecurityStore) RecordLoginChallengeFailure(context.Context, []byte) error {
	store.loginFailures++
	return nil
}

func (store *fakeSecurityStore) CompleteLogin(_ context.Context, input CompleteLoginInput) error {
	store.completedLogin = input
	return nil
}

func (store *fakeSecurityStore) RevokeOwnedSession(_ context.Context, _ Actor, sessionID, _ string) error {
	store.revokedSessionID = sessionID
	return nil
}

func (store *fakeSecurityStore) RevokeAllSessions(context.Context, Actor, string) error {
	store.revokedAll = true
	return nil
}

func (store *fakeSecurityStore) ResetUserSecurity(_ context.Context, _ Actor, userID string, _ int32, _ string) error {
	store.resetUserID = userID
	return nil
}

func newSecurityService(t *testing.T, now time.Time) (*Service, *fakeSecurityStore) {
	t.Helper()
	store := &fakeSecurityStore{fakeStore: &fakeStore{user: User{
		ID: "user-1", Username: "maria", DisplayName: "Maria Muster", Email: "maria@example.at",
		Role: RoleDriver, Active: true, Version: 3, WebAuthnUserHandle: bytes.Repeat([]byte{0x41}, 32),
	}}}
	hasher, err := NewPasswordHasher(PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewSecurityKeyRing(map[string]string{
		"test-v1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
	}, "test-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureSecurity(SecurityConfig{
		Keys: keys, AppName: "HackWerk", BaseURL: "https://hackwerk.example",
		EmailVerificationTTL: 24 * time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: 5 * time.Minute, WebAuthnChallengeTTL: 5 * time.Minute, MailMaxAttempts: 6,
	}); err != nil {
		t.Fatal(err)
	}
	counter := 0
	service.newToken = func() (string, error) {
		counter++
		return fmt.Sprintf("test-token-%d", counter), nil
	}
	return service, store
}

func TestSecurityProfileEmailAndSessionWorkflows(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	store.profile = Profile{User: store.user, Sessions: []ActiveSession{{ID: "session-1", DeviceLabel: "\x00"}}}

	profile, err := service.Profile(t.Context(), actor)
	if err != nil || profile.Sessions[0].DeviceLabel != "Unbekanntes Gerät" {
		t.Fatalf("profile = %#v, %v", profile, err)
	}
	if err := service.UpdateOwnProfile(t.Context(), actor, UpdateOwnProfileInput{
		DisplayName: " Maria Beispiel ", Salutation: " FRAU ", WorkPhoneRaw: "0664 / 123 45 67", ExpectedVersion: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if store.updatedProfile.DisplayName != "Maria Beispiel" || store.updatedProfile.WorkPhoneNormalized != "+436641234567" {
		t.Fatalf("normalized profile = %#v", store.updatedProfile)
	}
	if err := service.UpdateOwnProfile(t.Context(), actor, UpdateOwnProfileInput{DisplayName: "Maria", Salutation: "invalid", ExpectedVersion: 3}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid salutation error = %v", err)
	}

	if err := service.RequestEmailChange(t.Context(), actor, "maria.neu@EXAMPLE.AT", "request-1"); err != nil {
		t.Fatal(err)
	}
	if store.verification.Email != "maria.neu@example.at" || store.verification.TokenVersion != 1 {
		t.Fatalf("verification = %#v", store.verification)
	}
	if err := service.ResendEmailChange(t.Context(), actor, "request-2"); err != nil {
		t.Fatal(err)
	}
	if store.pendingVerification.TokenVersion != 2 {
		t.Fatalf("resent token version = %d", store.pendingVerification.TokenVersion)
	}
	rawToken, err := service.securityConfig.Keys.ReconstructEmailToken(
		store.pendingVerification.TokenKeyID, store.pendingVerification.ID, actor.UserID, store.pendingVerification.TokenVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyEmail(t.Context(), rawToken, "request-3"); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelEmailChange(t.Context(), actor, "request-4"); err != nil {
		t.Fatal(err)
	}

	if err := service.RevokeSession(t.Context(), actor, "session-1", "request-5"); err != nil || store.revokedSessionID != "session-1" {
		t.Fatalf("revoke session = %q, %v", store.revokedSessionID, err)
	}
	if err := service.RevokeAllSessions(t.Context(), actor, "request-6"); err != nil || !store.revokedAll {
		t.Fatalf("revoke all = %v, %v", store.revokedAll, err)
	}
	admin := Actor{UserID: "admin", Role: RoleAdmin}
	if err := service.ResetUserSecurity(t.Context(), admin, "user-1", 3, "request-7"); err != nil || store.resetUserID != "user-1" {
		t.Fatalf("reset security = %q, %v", store.resetUserID, err)
	}
}

func TestSecurityTOTPRecoveryAndLoginWorkflows(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), actor, "Telefon", "request-1")
	if err != nil || enrollment.Secret == "" || store.totp.SecretKeyID != "test-v1" || !bytes.HasPrefix([]byte(enrollment.QRCodeDataURI), []byte("data:image/png;base64,")) {
		t.Fatalf("enrollment = %#v, %v", enrollment, err)
	}
	code, err := totp.GenerateCodeCustom(enrollment.Secret, now, totp.ValidateOpts{
		Period: totpPeriodSeconds, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	codes, err := service.ConfirmTOTPEnrollment(t.Context(), actor, code, "request-2")
	if err != nil || len(codes) != recoveryCodeCount || len(store.recoveryHashes) != recoveryCodeCount {
		t.Fatalf("confirm TOTP = %d codes, %v", len(codes), err)
	}
	if err := service.RenameTOTP(t.Context(), actor, " Diensthandy ", "request-3"); err != nil || store.totp.Name != "Diensthandy" {
		t.Fatalf("rename TOTP = %q, %v", store.totp.Name, err)
	}
	store.profile.TOTPEnabledAt = store.totp.EnabledAt
	rotated, err := service.RotateRecoveryCodes(t.Context(), actor, "request-4")
	if err != nil || len(rotated) != recoveryCodeCount {
		t.Fatalf("rotate recovery codes = %d, %v", len(rotated), err)
	}

	store.user.TOTPEnabled = true
	store.login = LoginChallenge{User: store.user, ExpiresAt: now.Add(time.Minute)}
	options, err := service.MFAOptions(t.Context(), "challenge")
	if err != nil || !options.TOTPEnabled {
		t.Fatalf("MFA options = %#v, %v", options, err)
	}
	tokens, err := service.CompleteTOTPLogin(t.Context(), "challenge", code, "Chrome auf Windows", "request-5")
	if err != nil || tokens.SessionToken == "" || store.completedLogin.Factor != "totp" || store.completedLogin.TOTPStep == nil {
		t.Fatalf("TOTP login = %#v, %#v, %v", tokens, store.completedLogin, err)
	}
	if _, err := service.CompleteRecoveryLogin(t.Context(), "challenge", "kurz", "Firefox", "request-6"); !errors.Is(err, ErrInvalidMFA) || store.loginFailures != 1 {
		t.Fatalf("invalid recovery login = failures %d, %v", store.loginFailures, err)
	}
	if _, err := service.CompleteRecoveryLogin(t.Context(), "challenge", codes[0], "Firefox", "request-7"); err != nil || store.completedLogin.Factor != "recovery" {
		t.Fatalf("recovery login = %#v, %v", store.completedLogin, err)
	}
	if err := service.DeleteTOTP(t.Context(), actor, "request-8"); err != nil || store.totp.Name != "" {
		t.Fatalf("delete TOTP = %#v, %v", store.totp, err)
	}
}

func TestSecurityPasskeyChallengeAndCredentialWorkflows(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}

	creation, err := service.BeginPasskeyRegistration(t.Context(), actor, "session-1")
	if err != nil || creation == nil || len(store.registrationChallenge) == 0 || !store.registrationExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("registration challenge = %#v, %v", creation, err)
	}
	credential := &webauthnlib.Credential{ID: bytes.Repeat([]byte{0x51}, 32)}
	stored, err := service.encryptWebAuthnCredential(actor.UserID, "Laptop", credential)
	if err != nil {
		t.Fatal(err)
	}
	store.credentials = []StoredWebAuthnCredential{stored}
	user, credentials, err := service.webAuthnUser(t.Context(), actor.UserID)
	if err != nil || len(credentials) != 1 || len(user.WebAuthnCredentials()) != 1 || user.WebAuthnName() != "maria" || len(user.WebAuthnID()) != 32 {
		t.Fatalf("WebAuthn user = %#v, %d, %v", user, len(credentials), err)
	}
	encodedID := credentialID(credential.ID)
	if err := service.RenamePasskey(t.Context(), actor, encodedID, "Werkstatt-Laptop", "request-1"); err != nil || store.credentials[0].Name != "Werkstatt-Laptop" {
		t.Fatalf("rename passkey = %#v, %v", store.credentials, err)
	}

	store.user.PasskeyEnabled = true
	store.login = LoginChallenge{User: store.user, ExpiresAt: now.Add(time.Minute)}
	assertion, err := service.BeginPasskeyLogin(t.Context(), "challenge")
	if err != nil || assertion == nil || len(store.loginSession) == 0 {
		t.Fatalf("login challenge = %#v, %v", assertion, err)
	}
	if _, err := service.FinishPasskeyLogin(t.Context(), "challenge", "Laptop", "request-2", nil); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("nil passkey response error = %v", err)
	}
	if err := service.DeletePasskey(t.Context(), actor, encodedID, "request-3"); err != nil || len(store.credentials) != 0 {
		t.Fatalf("delete passkey = %#v, %v", store.credentials, err)
	}
}

func TestSecurityRejectsInvalidConfigurationAndInputs(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}

	if err := service.ConfigureSecurity(SecurityConfig{}); err == nil {
		t.Fatal("empty security configuration accepted")
	}
	if err := service.ConfigureSecurity(SecurityConfig{
		Keys: service.securityConfig.Keys, AppName: "HackWerk", BaseURL: "://invalid",
		EmailVerificationTTL: time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: time.Minute, WebAuthnChallengeTTL: time.Minute, MailMaxAttempts: 1,
	}); err == nil {
		t.Fatal("invalid WebAuthn URL accepted")
	}
	if err := service.RequestEmailChange(t.Context(), actor, store.user.Email, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unchanged email error = %v", err)
	}
	if err := service.VerifyEmail(t.Context(), "", "request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty verification token error = %v", err)
	}
	store.profile = Profile{}
	if _, err := service.RotateRecoveryCodes(t.Context(), actor, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("rotation without factor error = %v", err)
	}
	if err := service.ResetUserSecurity(t.Context(), actor, "user-1", 3, "request"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("driver reset security error = %v", err)
	}
	if _, err := service.BeginPasskeyRegistration(t.Context(), Actor{}, "session"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous passkey registration error = %v", err)
	}
	if err := service.RenamePasskey(t.Context(), actor, "invalid", "Passkey", "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid credential ID error = %v", err)
	}
}
