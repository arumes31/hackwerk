package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func enrollTOTPForTest(t *testing.T, service *Service, now time.Time, actor Actor, sessionID string) string {
	t.Helper()
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), actor, sessionID, "Telefon", "request-begin")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCodeCustom(enrollment.Secret, now, totp.ValidateOpts{
		Period: totpPeriodSeconds, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestConfirmTOTPEnrollmentRequiresStartingSession(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, _ := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	code := enrollTOTPForTest(t, service, now, actor, "session-1")
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-2", code, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("other session confirm error = %v, want invalid input", err)
	}
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "", code, "request"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing session confirm error = %v, want forbidden", err)
	}
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", code, "request"); err != nil {
		t.Fatalf("starting session confirm error = %v", err)
	}
}

func TestConfirmTOTPEnrollmentExpiresPendingEnrollment(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	code := enrollTOTPForTest(t, service, now, actor, "session-1")
	store.totp.UpdatedAt = now.Add(-totpEnrollmentTTL - time.Second)
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", code, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expired confirm error = %v, want invalid input", err)
	}
	if store.totp.EnabledAt != nil {
		t.Fatal("expired enrollment was enabled")
	}
}

func TestConfirmTOTPEnrollmentLimitsGuessesPerAccount(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	store.rate = RateLimit{WindowStartedAt: now}
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	code := enrollTOTPForTest(t, service, now, actor, "session-1")
	for attempt := 1; attempt <= totpEnrollmentAttemptLimit; attempt++ {
		if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", "abcdef", "request"); !errors.Is(err, ErrInvalidMFA) {
			t.Fatalf("guess %d error = %v, want invalid MFA", attempt, err)
		}
	}
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", code, "request"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over-budget confirm error = %v, want rate limited", err)
	}
	if store.totp.EnabledAt != nil {
		t.Fatal("over-budget guess enabled TOTP")
	}
}

func TestSecondFactorBudgetSurvivesChallengeRenewal(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	store.rate = RateLimit{WindowStartedAt: now}
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	code := enrollTOTPForTest(t, service, now, actor, "session-1")
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", code, "request"); err != nil {
		t.Fatal(err)
	}
	store.user.TOTPEnabled = true
	for attempt := 1; attempt <= secondFactorAttemptLimit; attempt++ {
		// Every guess uses a fresh challenge, as a caller holding the password could.
		store.login = LoginChallenge{User: store.user, ExpiresAt: now.Add(time.Minute)}
		_, err := service.CompleteRecoveryLogin(t.Context(), "challenge", "kurz", "Firefox", "request")
		if attempt%2 == 0 {
			_, err = service.CompleteTOTPLogin(t.Context(), "challenge", "abcdef", "Firefox", "request")
		}
		if !errors.Is(err, ErrInvalidMFA) && !errors.Is(err, ErrRateLimited) {
			t.Fatalf("guess %d error = %v", attempt, err)
		}
	}
	store.login = LoginChallenge{User: store.user, ExpiresAt: now.Add(time.Minute)}
	if _, err := service.CompleteTOTPLogin(t.Context(), "challenge", code, "Firefox", "request"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("renewed challenge error = %v, want rate limited", err)
	}
	if store.completedLogin.Factor != "" {
		t.Fatalf("over-budget login completed: %#v", store.completedLogin)
	}
}

func TestCompletedSecondFactorClearsLoginBudgets(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	actor := Actor{UserID: "user-1", Role: RoleDriver}
	code := enrollTOTPForTest(t, service, now, actor, "session-1")
	if _, err := service.ConfirmTOTPEnrollment(t.Context(), actor, "session-1", code, "request"); err != nil {
		t.Fatal(err)
	}
	store.user.TOTPEnabled = true
	store.login = LoginChallenge{User: store.user, ExpiresAt: now.Add(time.Minute)}
	if _, err := service.CompleteTOTPLogin(t.Context(), "challenge", code, "Firefox", "request"); err != nil {
		t.Fatal(err)
	}
	keys := store.completedLogin.ClearRateKeys
	if len(keys) != 2 || !bytes.Equal(keys[0], loginAccountKey(" Maria ")) || !bytes.Equal(keys[1], secondFactorAccountKey("user-1")) {
		t.Fatalf("cleared rate keys = %x", keys)
	}
}

func TestPasswordStageDoesNotClearAccountBudgetWhenSecondFactorRequired(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service, store := newSecurityService(t, now)
	const password = "Sicheres Testpasswort 2026" //nolint:gosec // Test-only password fixture.
	hash, err := service.hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	store.user.PasswordHash = hash
	store.user.TOTPEnabled = true
	tokens, err := service.LoginWithDevice(t.Context(), "maria", password, "client", "Firefox", "request")
	if err != nil || !tokens.MFARequired {
		t.Fatalf("login = %#v, %v", tokens, err)
	}
	if store.attempts[string(loginAccountKey("maria"))] != 1 {
		t.Fatalf("account budget after password stage = %d, want retained attempt", store.attempts[string(loginAccountKey("maria"))])
	}
}
