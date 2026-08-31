package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	recoveryCodeCount = 8
	totpPeriodSeconds = 30
)

var digitsOnly = regexp.MustCompile(`^[0-9]{6}$`)

type SecurityConfig struct {
	Keys                 *SecurityKeyRing
	AppName              string
	BaseURL              string
	EmailVerificationTTL time.Duration
	EmailResendInterval  time.Duration
	MFAChallengeTTL      time.Duration
	WebAuthnChallengeTTL time.Duration
	MailMaxAttempts      int32
	webAuthn             *webauthnlib.WebAuthn
}

type SecurityKeyRing struct {
	current string
	keys    map[string][]byte
	random  io.Reader
}

func NewSecurityKeyRing(encoded map[string]string, current string) (*SecurityKeyRing, error) {
	if strings.TrimSpace(current) == "" || len(encoded) == 0 {
		return nil, errors.New("auth: invalid security key ring")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) < 32 || strings.TrimSpace(id) == "" {
			return nil, errors.New("auth: invalid security key")
		}
		keys[id] = append([]byte(nil), decoded...)
	}
	if _, ok := keys[current]; !ok {
		return nil, errors.New("auth: current security key missing")
	}
	return &SecurityKeyRing{current: current, keys: keys, random: rand.Reader}, nil
}

func (ring *SecurityKeyRing) CurrentID() string { return ring.current }

func (ring *SecurityKeyRing) emailToken(verificationID, userID string, version int32) (string, []byte, error) {
	key, ok := ring.keys[ring.current]
	if !ok || version < 1 {
		return "", nil, ErrInvalidInput
	}
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "hackwerk:email-verification:v1\x00")
	_, _ = io.WriteString(mac, verificationID)
	_, _ = io.WriteString(mac, "\x00")
	_, _ = io.WriteString(mac, userID)
	_, _ = io.WriteString(mac, fmt.Sprintf("\x00%d", version))
	raw := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return raw, TokenHash(raw), nil
}

func (ring *SecurityKeyRing) reconstructEmailToken(keyID, verificationID, userID string, version int32) (string, error) {
	key, ok := ring.keys[keyID]
	if !ok || version < 1 {
		return "", ErrNotFound
	}
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "hackwerk:email-verification:v1\x00")
	_, _ = io.WriteString(mac, verificationID)
	_, _ = io.WriteString(mac, "\x00")
	_, _ = io.WriteString(mac, userID)
	_, _ = io.WriteString(mac, fmt.Sprintf("\x00%d", version))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ReconstructEmailToken rebuilds a verification link token inside the trusted worker boundary.
func (ring *SecurityKeyRing) ReconstructEmailToken(keyID, verificationID, userID string, version int32) (string, error) {
	return ring.reconstructEmailToken(keyID, verificationID, userID, version)
}

func (ring *SecurityKeyRing) encrypt(domain string, plaintext []byte) (string, []byte, error) {
	key := ring.keys[ring.current]
	derived := sha256.Sum256(key)
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return "", nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(ring.random, nonce); err != nil {
		return "", nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(domain))
	return ring.current, append(nonce, sealed...), nil
}

func (ring *SecurityKeyRing) decrypt(keyID, domain string, ciphertext []byte) ([]byte, error) {
	key, ok := ring.keys[keyID]
	if !ok {
		return nil, ErrNotFound
	}
	derived := sha256.Sum256(key)
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrInvalidInput
	}
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], []byte(domain))
	if err != nil {
		return nil, ErrInvalidInput
	}
	return plain, nil
}

type Profile struct {
	User
	PendingEmail               string
	PendingEmailID             string
	PendingEmailLastSentAt     *time.Time
	PendingEmailExpiresAt      *time.Time
	PendingEmailDeliveryStatus string
	TOTPName                   string
	TOTPEnabledAt              *time.Time
	RecoveryCodeCount          int32
	Passkeys                   []SecurityMethod
	Sessions                   []ActiveSession
}

type SecurityMethod struct {
	ID         []byte
	EncodedID  string
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type MFAOptions struct {
	TOTPEnabled    bool
	PasskeyEnabled bool
}

func (service *Service) MFAOptions(ctx context.Context, rawChallenge string) (MFAOptions, error) {
	challenge, err := service.loginChallenge(ctx, rawChallenge)
	if err != nil {
		return MFAOptions{}, err
	}
	return MFAOptions{TOTPEnabled: challenge.User.TOTPEnabled, PasskeyEnabled: challenge.User.PasskeyEnabled}, nil
}

type ActiveSession struct {
	ID                string
	DeviceLabel       string
	CreatedAt         time.Time
	LastUsedAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type UpdateOwnProfileInput struct {
	DisplayName         string
	Salutation          string
	WorkPhoneRaw        string
	WorkPhoneNormalized string
	ExpectedVersion     int32
	RequestID           string
}

type EmailVerification struct {
	ID           string
	UserID       string
	Email        string
	TokenHash    []byte
	TokenKeyID   string
	TokenVersion int32
	SendCount    int32
	LastSentAt   time.Time
	ExpiresAt    time.Time
}

type TOTPCredential struct {
	Name             string
	SecretKeyID      string
	SecretCiphertext []byte
	EnabledAt        *time.Time
	LastUsedStep     *int64
}

type StoredWebAuthnCredential struct {
	ID                   []byte
	Name                 string
	CredentialKeyID      string
	CredentialCiphertext []byte
	CreatedAt            time.Time
	LastUsedAt           *time.Time
}

type LoginChallenge struct {
	ID              string
	User            User
	ExpiresAt       time.Time
	AttemptCount    int32
	WebAuthnSession []byte
}

type CompleteLoginInput struct {
	ChallengeTokenHash []byte
	Now                time.Time
	NewSession         NewSession
	Factor             string
	TOTPStep           *int64
	RecoveryCodeHash   []byte
	Credential         *StoredWebAuthnCredential
	RequestID          string
}

type SecurityStore interface {
	LoadProfile(context.Context, string, time.Time) (Profile, error)
	UpdateOwnProfile(context.Context, Actor, UpdateOwnProfileInput) error
	StartEmailVerification(context.Context, Actor, EmailVerification, int32, string) error
	PendingEmailVerification(context.Context, string) (EmailVerification, error)
	ResendEmailVerification(context.Context, Actor, EmailVerification, time.Time, int32, string) error
	CancelEmailVerification(context.Context, Actor, string) error
	VerifyEmail(context.Context, []byte, time.Time, string) error

	UpsertTOTPEnrollment(context.Context, Actor, TOTPCredential, string) error
	TOTPCredential(context.Context, string) (TOTPCredential, error)
	EnableTOTP(context.Context, Actor, [][]byte, string) error
	RenameTOTP(context.Context, Actor, string, string) error
	DeleteTOTP(context.Context, Actor, string) error

	WebAuthnCredentials(context.Context, string) ([]StoredWebAuthnCredential, error)
	SaveWebAuthnRegistrationChallenge(context.Context, Actor, string, []byte, time.Time) error
	WebAuthnRegistrationChallenge(context.Context, Actor, string) ([]byte, time.Time, error)
	AddWebAuthnCredential(context.Context, Actor, string, StoredWebAuthnCredential, [][]byte, string) error
	RenameWebAuthnCredential(context.Context, Actor, []byte, string, string) error
	DeleteWebAuthnCredential(context.Context, Actor, []byte, string) error

	ReplaceRecoveryCodes(context.Context, Actor, [][]byte, string) error
	StartLoginChallenge(context.Context, User, []byte, time.Time, []byte, []byte, string) error
	LoginChallenge(context.Context, []byte) (LoginChallenge, error)
	SetLoginWebAuthnSession(context.Context, []byte, []byte) error
	RecordLoginChallengeFailure(context.Context, []byte) error
	CompleteLogin(context.Context, CompleteLoginInput) error

	RevokeOwnedSession(context.Context, Actor, string, string) error
	RevokeAllSessions(context.Context, Actor, string) error
	ResetUserSecurity(context.Context, Actor, string, int32, string) error
}

func (service *Service) ConfigureSecurity(cfg SecurityConfig) error {
	store, ok := service.store.(SecurityStore)
	if !ok || cfg.Keys == nil || cfg.EmailVerificationTTL <= 0 || cfg.EmailResendInterval <= 0 ||
		cfg.MFAChallengeTTL <= 0 || cfg.WebAuthnChallengeTTL <= 0 || cfg.MailMaxAttempts < 1 {
		return errors.New("auth: invalid security dependencies")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return errors.New("auth: invalid WebAuthn base URL")
	}
	origin := base.Scheme + "://" + base.Host
	wa, err := webauthnlib.New(&webauthnlib.Config{
		RPID: base.Hostname(), RPDisplayName: cfg.AppName, RPOrigins: []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return fmt.Errorf("auth: configuring WebAuthn: %w", err)
	}
	service.security = store
	service.securityConfig = &cfg
	service.securityConfig.webAuthn = wa
	return nil
}

func (service *Service) Profile(ctx context.Context, actor Actor) (Profile, error) {
	if actor.UserID == "" || service.security == nil {
		return Profile{}, ErrForbidden
	}
	profile, err := service.security.LoadProfile(ctx, actor.UserID, service.now().UTC())
	if err != nil {
		return Profile{}, err
	}
	for i := range profile.Sessions {
		profile.Sessions[i].DeviceLabel = normalizeDeviceLabel(profile.Sessions[i].DeviceLabel)
	}
	return profile, nil
}

func (service *Service) UpdateOwnProfile(ctx context.Context, actor Actor, input UpdateOwnProfileInput) error {
	if actor.UserID == "" || service.security == nil || input.ExpectedVersion < 1 {
		return ErrForbidden
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Salutation = strings.ToLower(strings.TrimSpace(input.Salutation))
	input.WorkPhoneRaw = strings.TrimSpace(input.WorkPhoneRaw)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 200 || strings.ContainsAny(input.DisplayName, "\r\n") ||
		!validSalutation(input.Salutation) || len([]rune(input.WorkPhoneRaw)) > 80 || strings.ContainsAny(input.WorkPhoneRaw, "\r\n") {
		return ErrInvalidInput
	}
	phone, err := normalizePhone(input.WorkPhoneRaw)
	if err != nil {
		return ErrInvalidInput
	}
	input.WorkPhoneNormalized = phone
	return service.security.UpdateOwnProfile(ctx, actor, input)
}

func (service *Service) RequestEmailChange(ctx context.Context, actor Actor, rawEmail, requestID string) error {
	if actor.UserID == "" || service.securityConfig == nil {
		return ErrForbidden
	}
	email, err := normalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	user, err := service.store.FindUserByID(ctx, actor.UserID)
	if err != nil {
		return err
	}
	if strings.EqualFold(email, user.Email) {
		return ErrInvalidInput
	}
	id, err := service.newToken()
	if err != nil {
		return err
	}
	// Convert the random token to an opaque UUID-shaped identifier at the store boundary.
	idHash := sha256.Sum256([]byte(id))
	verificationID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", idHash[:4], idHash[4:6], idHash[6:8], idHash[8:10], idHash[10:16])
	raw, tokenHash, err := service.securityConfig.Keys.emailToken(verificationID, actor.UserID, 1)
	_ = raw
	if err != nil {
		return err
	}
	verification := EmailVerification{
		ID: verificationID, UserID: actor.UserID, Email: email, TokenHash: tokenHash,
		TokenKeyID: service.securityConfig.Keys.CurrentID(), TokenVersion: 1,
		ExpiresAt: service.now().UTC().Add(service.securityConfig.EmailVerificationTTL),
	}
	return service.security.StartEmailVerification(ctx, actor, verification, service.securityConfig.MailMaxAttempts, requestID)
}

func (service *Service) ResendEmailChange(ctx context.Context, actor Actor, requestID string) error {
	if actor.UserID == "" || service.securityConfig == nil {
		return ErrForbidden
	}
	pending, err := service.security.PendingEmailVerification(ctx, actor.UserID)
	if err != nil {
		return err
	}
	pending.TokenVersion++
	_, pending.TokenHash, err = service.securityConfig.Keys.emailToken(pending.ID, actor.UserID, pending.TokenVersion)
	if err != nil {
		return err
	}
	pending.TokenKeyID = service.securityConfig.Keys.CurrentID()
	pending.ExpiresAt = service.now().UTC().Add(service.securityConfig.EmailVerificationTTL)
	return service.security.ResendEmailVerification(ctx, actor, pending, service.now().UTC().Add(-service.securityConfig.EmailResendInterval), service.securityConfig.MailMaxAttempts, requestID)
}

func (service *Service) CancelEmailChange(ctx context.Context, actor Actor, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	return service.security.CancelEmailVerification(ctx, actor, requestID)
}

func (service *Service) VerifyEmail(ctx context.Context, rawToken, requestID string) error {
	if rawToken == "" || len(rawToken) > 256 || service.security == nil {
		return ErrNotFound
	}
	return service.security.VerifyEmail(ctx, TokenHash(rawToken), service.now().UTC(), requestID)
}

type TOTPEnrollment struct {
	Name            string
	Secret          string
	ProvisioningURI string
	QRCodeDataURI   string
}

func (service *Service) BeginTOTPEnrollment(ctx context.Context, actor Actor, name, requestID string) (TOTPEnrollment, error) {
	if actor.UserID == "" || service.securityConfig == nil {
		return TOTPEnrollment{}, ErrForbidden
	}
	name, err := normalizeSecurityMethodName(name, "Authenticator-App")
	if err != nil {
		return TOTPEnrollment{}, err
	}
	user, err := service.store.FindUserByID(ctx, actor.UserID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	account := user.Username
	if user.Email != "" {
		account = user.Email
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: service.securityConfig.AppName, AccountName: account, Period: totpPeriodSeconds,
		SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("auth: generating TOTP: %w", err)
	}
	keyID, ciphertext, err := service.securityConfig.Keys.encrypt("totp:"+actor.UserID, []byte(key.Secret()))
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if err := service.security.UpsertTOTPEnrollment(ctx, actor, TOTPCredential{Name: name, SecretKeyID: keyID, SecretCiphertext: ciphertext}, requestID); err != nil {
		return TOTPEnrollment{}, err
	}
	image, err := key.Image(220, 220)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	var encoded strings.Builder
	writer := base64.NewEncoder(base64.StdEncoding, &encoded)
	if err := png.Encode(writer, image); err != nil {
		return TOTPEnrollment{}, err
	}
	if err := writer.Close(); err != nil {
		return TOTPEnrollment{}, err
	}
	return TOTPEnrollment{Name: name, Secret: key.Secret(), ProvisioningURI: key.URL(), QRCodeDataURI: "data:image/png;base64," + encoded.String()}, nil
}

func (service *Service) ConfirmTOTPEnrollment(ctx context.Context, actor Actor, code, requestID string) ([]string, error) {
	if actor.UserID == "" || service.securityConfig == nil {
		return nil, ErrForbidden
	}
	credential, err := service.security.TOTPCredential(ctx, actor.UserID)
	if err != nil || credential.EnabledAt != nil {
		return nil, ErrInvalidInput
	}
	secret, err := service.securityConfig.Keys.decrypt(credential.SecretKeyID, "totp:"+actor.UserID, credential.SecretCiphertext)
	if err != nil {
		return nil, err
	}
	if _, ok := validateTOTP(string(secret), code, service.now().UTC()); !ok {
		return nil, ErrInvalidMFA
	}
	profile, err := service.security.LoadProfile(ctx, actor.UserID, service.now().UTC())
	if err != nil {
		return nil, err
	}
	var codes []string
	var hashes [][]byte
	if profile.RecoveryCodeCount == 0 {
		codes, hashes, err = newRecoveryCodes()
		if err != nil {
			return nil, err
		}
	}
	if err := service.security.EnableTOTP(ctx, actor, hashes, requestID); err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) RenameTOTP(ctx context.Context, actor Actor, name, requestID string) error {
	name, err := normalizeSecurityMethodName(name, "Authenticator-App")
	if err != nil || service.security == nil {
		return ErrInvalidInput
	}
	return service.security.RenameTOTP(ctx, actor, name, requestID)
}

func (service *Service) DeleteTOTP(ctx context.Context, actor Actor, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	return service.security.DeleteTOTP(ctx, actor, requestID)
}

func (service *Service) RotateRecoveryCodes(ctx context.Context, actor Actor, requestID string) ([]string, error) {
	if actor.UserID == "" || service.security == nil {
		return nil, ErrForbidden
	}
	profile, err := service.security.LoadProfile(ctx, actor.UserID, service.now().UTC())
	if err != nil {
		return nil, err
	}
	if profile.TOTPEnabledAt == nil && len(profile.Passkeys) == 0 {
		return nil, ErrInvalidInput
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := service.security.ReplaceRecoveryCodes(ctx, actor, hashes, requestID); err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) CompleteTOTPLogin(ctx context.Context, rawChallenge, code, deviceLabel, requestID string) (SessionTokens, error) {
	challenge, err := service.loginChallenge(ctx, rawChallenge)
	if err != nil || !challenge.User.TOTPEnabled {
		return SessionTokens{}, ErrInvalidMFA
	}
	credential, err := service.security.TOTPCredential(ctx, challenge.User.ID)
	if err != nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	secret, err := service.securityConfig.Keys.decrypt(credential.SecretKeyID, "totp:"+challenge.User.ID, credential.SecretCiphertext)
	if err != nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	step, valid := validateTOTP(string(secret), code, service.now().UTC())
	if !valid || (credential.LastUsedStep != nil && step <= *credential.LastUsedStep) {
		_ = service.security.RecordLoginChallengeFailure(ctx, TokenHash(rawChallenge))
		return SessionTokens{}, ErrInvalidMFA
	}
	return service.completeLogin(ctx, rawChallenge, challenge.User, deviceLabel, CompleteLoginInput{Factor: "totp", TOTPStep: &step, RequestID: requestID})
}

func (service *Service) CompleteRecoveryLogin(ctx context.Context, rawChallenge, code, deviceLabel, requestID string) (SessionTokens, error) {
	challenge, err := service.loginChallenge(ctx, rawChallenge)
	if err != nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	normalized := normalizeRecoveryCode(code)
	if len(normalized) != 12 {
		_ = service.security.RecordLoginChallengeFailure(ctx, TokenHash(rawChallenge))
		return SessionTokens{}, ErrInvalidMFA
	}
	hash := TokenHash("recovery\x00" + normalized)
	return service.completeLogin(ctx, rawChallenge, challenge.User, deviceLabel, CompleteLoginInput{Factor: "recovery", RecoveryCodeHash: hash, RequestID: requestID})
}

func (service *Service) RevokeSession(ctx context.Context, actor Actor, sessionID, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	return service.security.RevokeOwnedSession(ctx, actor, sessionID, requestID)
}

func (service *Service) RevokeAllSessions(ctx context.Context, actor Actor, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	return service.security.RevokeAllSessions(ctx, actor, requestID)
}

func (service *Service) ResetUserSecurity(ctx context.Context, actor Actor, userID string, expectedVersion int32, requestID string) error {
	if err := actor.Require(PermissionUserManage); err != nil || service.security == nil || expectedVersion < 1 {
		return ErrForbidden
	}
	return service.security.ResetUserSecurity(ctx, actor, userID, expectedVersion, requestID)
}

func (service *Service) loginChallenge(ctx context.Context, raw string) (LoginChallenge, error) {
	if raw == "" || service.security == nil {
		return LoginChallenge{}, ErrInvalidMFA
	}
	challenge, err := service.security.LoginChallenge(ctx, TokenHash(raw))
	if err != nil || !challenge.User.Active || !service.now().UTC().Before(challenge.ExpiresAt) || challenge.AttemptCount >= 10 {
		return LoginChallenge{}, ErrInvalidMFA
	}
	return challenge, nil
}

func (service *Service) completeLogin(ctx context.Context, rawChallenge string, user User, deviceLabel string, input CompleteLoginInput) (SessionTokens, error) {
	tokens, session, err := service.newSessionForUser(user, deviceLabel)
	if err != nil {
		return SessionTokens{}, err
	}
	input.ChallengeTokenHash = TokenHash(rawChallenge)
	input.Now = service.now().UTC()
	input.NewSession = session
	if err := service.security.CompleteLogin(ctx, input); err != nil {
		_ = service.security.RecordLoginChallengeFailure(ctx, input.ChallengeTokenHash)
		return SessionTokens{}, ErrInvalidMFA
	}
	return tokens, nil
}

func (service *Service) newSessionForUser(user User, deviceLabel string) (SessionTokens, NewSession, error) {
	now := service.now().UTC()
	sessionToken, err := service.newToken()
	if err != nil {
		return SessionTokens{}, NewSession{}, err
	}
	csrfToken, err := service.newToken()
	if err != nil {
		return SessionTokens{}, NewSession{}, err
	}
	return SessionTokens{SessionToken: sessionToken, CSRFToken: csrfToken, Actor: actorFromUser(user)}, NewSession{
		UserID: user.ID, TokenHash: TokenHash(sessionToken), CSRFTokenHash: TokenHash(csrfToken),
		IdleExpiresAt: now.Add(service.idleTTL), AbsoluteExpiresAt: now.Add(service.absoluteTTL), DeviceLabel: normalizeDeviceLabel(deviceLabel),
	}, nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 320 || strings.ContainsAny(value, "\r\n") {
		return "", ErrInvalidInput
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return "", ErrInvalidInput
	}
	return parts[0] + "@" + strings.ToLower(parts[1]), nil
}

func normalizePhone(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	compact := strings.NewReplacer(" ", "", "(", "", ")", "", ".", "", "/", "", "-", "").Replace(value)
	if strings.HasPrefix(compact, "00") {
		compact = "+" + compact[2:]
	} else if strings.HasPrefix(compact, "0") {
		compact = "+43" + compact[1:]
	}
	if !strings.HasPrefix(compact, "+") || len(compact) < 8 || len(compact) > 16 {
		return "", ErrInvalidInput
	}
	for _, r := range compact[1:] {
		if r < '0' || r > '9' {
			return "", ErrInvalidInput
		}
	}
	if compact[1] == '0' {
		return "", ErrInvalidInput
	}
	return compact, nil
}

func normalizeDeviceLabel(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	if value == "" {
		return "Unbekanntes Gerät"
	}
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	return value
}

func DeviceLabel(userAgent string) string {
	ua := strings.ToLower(userAgent)
	browser := "Browser"
	switch {
	case strings.Contains(ua, "edg/"):
		browser = "Edge"
	case strings.Contains(ua, "firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "chrome/") || strings.Contains(ua, "crios/"):
		browser = "Chrome"
	case strings.Contains(ua, "safari/"):
		browser = "Safari"
	}
	device := "Desktop"
	switch {
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad"):
		device = "iPhone/iPad"
	case strings.Contains(ua, "android"):
		device = "Android"
	case strings.Contains(ua, "windows"):
		device = "Windows"
	case strings.Contains(ua, "mac os") || strings.Contains(ua, "macintosh"):
		device = "macOS"
	case strings.Contains(ua, "linux"):
		device = "Linux"
	}
	return normalizeDeviceLabel(browser + " auf " + device)
}

func validSalutation(value string) bool {
	return value == "" || value == "frau" || value == "herr" || value == "divers"
}

func normalizeSecurityMethodName(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len([]rune(value)) > 100 || strings.ContainsAny(value, "\r\n") {
		return "", ErrInvalidInput
	}
	return value, nil
}

func validateTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if !digitsOnly.MatchString(code) {
		return 0, false
	}
	opts := totp.ValidateOpts{Period: totpPeriodSeconds, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	for _, offset := range []int{-1, 0, 1} {
		candidate := now.Add(time.Duration(offset*totpPeriodSeconds) * time.Second)
		valid, err := totp.ValidateCustom(code, secret, candidate, opts)
		if err == nil && valid {
			return candidate.Unix() / totpPeriodSeconds, true
		}
	}
	return 0, false
}

func newRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([][]byte, 0, recoveryCodeCount)
	encoding := base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)
	for len(codes) < recoveryCodeCount {
		raw := make([]byte, 8)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, nil, err
		}
		value := encoding.EncodeToString(raw)[:12]
		formatted := value[:4] + "-" + value[4:8] + "-" + value[8:]
		codes = append(codes, formatted)
		hashes = append(hashes, TokenHash("recovery\x00"+value))
	}
	return codes, hashes, nil
}

func normalizeRecoveryCode(value string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(value)))
}
