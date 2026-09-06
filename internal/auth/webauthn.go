package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
)

type webAuthnUser struct {
	user        User
	credentials []webauthnlib.Credential
}

func (user webAuthnUser) WebAuthnID() []byte                            { return user.user.WebAuthnUserHandle }
func (user webAuthnUser) WebAuthnName() string                          { return user.user.Username }
func (user webAuthnUser) WebAuthnDisplayName() string                   { return user.user.DisplayName }
func (user webAuthnUser) WebAuthnCredentials() []webauthnlib.Credential { return user.credentials }

func (service *Service) BeginPasskeyRegistration(ctx context.Context, actor Actor, sessionID string) (*protocol.CredentialCreation, error) {
	if actor.UserID == "" || sessionID == "" || service.securityConfig == nil || service.securityConfig.webAuthn == nil {
		return nil, ErrForbidden
	}
	user, credentials, err := service.webAuthnUser(ctx, actor.UserID)
	if err != nil {
		return nil, err
	}
	creation, sessionData, err := service.securityConfig.webAuthn.BeginRegistration(
		user,
		webauthnlib.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthnlib.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthnlib.WithAuthenticatorSelection(protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired, ResidentKey: protocol.ResidentKeyRequirementPreferred}),
		webauthnlib.WithExclusions(credentialDescriptors(credentials)),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: beginning passkey registration: %w", err)
	}
	encoded, err := json.Marshal(sessionData)
	if err != nil {
		return nil, err
	}
	if err := service.security.SaveWebAuthnRegistrationChallenge(ctx, actor, sessionID, encoded, service.now().UTC().Add(service.securityConfig.WebAuthnChallengeTTL)); err != nil {
		return nil, err
	}
	return creation, nil
}

func (service *Service) FinishPasskeyRegistration(ctx context.Context, actor Actor, sessionID, name, requestID string, request *http.Request) ([]string, error) {
	if actor.UserID == "" || sessionID == "" || request == nil || service.securityConfig == nil {
		return nil, ErrForbidden
	}
	name, err := normalizeSecurityMethodName(name, "Passkey")
	if err != nil {
		return nil, err
	}
	encoded, expiresAt, err := service.security.WebAuthnRegistrationChallenge(ctx, actor, sessionID)
	if err != nil || !service.now().UTC().Before(expiresAt) {
		return nil, ErrVerificationExpired
	}
	var sessionData webauthnlib.SessionData
	if err := json.Unmarshal(encoded, &sessionData); err != nil {
		return nil, ErrInvalidInput
	}
	user, _, err := service.webAuthnUser(ctx, actor.UserID)
	if err != nil {
		return nil, err
	}
	credential, err := service.securityConfig.webAuthn.FinishRegistration(user, sessionData, request)
	if err != nil {
		return nil, ErrInvalidMFA
	}
	stored, err := service.encryptWebAuthnCredential(actor.UserID, name, credential)
	if err != nil {
		return nil, err
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
	if err := service.security.AddWebAuthnCredential(ctx, actor, sessionID, stored, hashes, requestID); err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) RenamePasskey(ctx context.Context, actor Actor, encodedID, name, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	id, err := decodeCredentialID(encodedID)
	if err != nil {
		return err
	}
	name, err = normalizeSecurityMethodName(name, "Passkey")
	if err != nil {
		return err
	}
	return service.security.RenameWebAuthnCredential(ctx, actor, id, name, requestID)
}

func (service *Service) DeletePasskey(ctx context.Context, actor Actor, encodedID, requestID string) error {
	if actor.UserID == "" || service.security == nil {
		return ErrForbidden
	}
	id, err := decodeCredentialID(encodedID)
	if err != nil {
		return err
	}
	return service.security.DeleteWebAuthnCredential(ctx, actor, id, requestID)
}

func (service *Service) BeginPasskeyLogin(ctx context.Context, rawChallenge string) (*protocol.CredentialAssertion, error) {
	challenge, err := service.loginChallenge(ctx, rawChallenge)
	if err != nil || !challenge.User.PasskeyEnabled {
		return nil, ErrInvalidMFA
	}
	user, _, err := service.webAuthnUser(ctx, challenge.User.ID)
	if err != nil {
		return nil, ErrInvalidMFA
	}
	assertion, sessionData, err := service.securityConfig.webAuthn.BeginLogin(user, webauthnlib.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, ErrInvalidMFA
	}
	encoded, err := json.Marshal(sessionData)
	if err != nil {
		return nil, err
	}
	if err := service.security.SetLoginWebAuthnSession(ctx, TokenHash(rawChallenge), encoded); err != nil {
		return nil, ErrInvalidMFA
	}
	return assertion, nil
}

func (service *Service) FinishPasskeyLogin(ctx context.Context, rawChallenge, deviceLabel, requestID string, request *http.Request) (SessionTokens, error) {
	challenge, err := service.loginChallenge(ctx, rawChallenge)
	if err != nil || !challenge.User.PasskeyEnabled || len(challenge.WebAuthnSession) == 0 || request == nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	var sessionData webauthnlib.SessionData
	if err := json.Unmarshal(challenge.WebAuthnSession, &sessionData); err != nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	user, _, err := service.webAuthnUser(ctx, challenge.User.ID)
	if err != nil {
		return SessionTokens{}, ErrInvalidMFA
	}
	credential, err := service.securityConfig.webAuthn.FinishLogin(user, sessionData, request)
	if err != nil {
		_ = service.security.RecordLoginChallengeFailure(ctx, TokenHash(rawChallenge))
		return SessionTokens{}, ErrInvalidMFA
	}
	stored, err := service.encryptWebAuthnCredential(challenge.User.ID, "", credential)
	if err != nil {
		return SessionTokens{}, err
	}
	return service.completeLogin(ctx, rawChallenge, challenge.User, deviceLabel, CompleteLoginInput{Factor: "webauthn", Credential: &stored, RequestID: requestID})
}

func (service *Service) webAuthnUser(ctx context.Context, userID string) (webAuthnUser, []StoredWebAuthnCredential, error) {
	user, err := service.store.FindUserByID(ctx, userID)
	if err != nil {
		return webAuthnUser{}, nil, err
	}
	if len(user.WebAuthnUserHandle) < 16 {
		return webAuthnUser{}, nil, ErrInvalidInput
	}
	stored, err := service.security.WebAuthnCredentials(ctx, userID)
	if err != nil {
		return webAuthnUser{}, nil, err
	}
	credentials := make([]webauthnlib.Credential, 0, len(stored))
	for _, item := range stored {
		plain, decryptErr := service.securityConfig.Keys.decrypt(item.CredentialKeyID, webAuthnCredentialDomain(userID, item.ID), item.CredentialCiphertext)
		if decryptErr != nil {
			return webAuthnUser{}, nil, decryptErr
		}
		var credential webauthnlib.Credential
		if err := json.Unmarshal(plain, &credential); err != nil {
			return webAuthnUser{}, nil, ErrInvalidInput
		}
		credentials = append(credentials, credential)
	}
	return webAuthnUser{user: user, credentials: credentials}, stored, nil
}

func (service *Service) encryptWebAuthnCredential(userID, name string, credential *webauthnlib.Credential) (StoredWebAuthnCredential, error) {
	if credential == nil || len(credential.ID) < 16 {
		return StoredWebAuthnCredential{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return StoredWebAuthnCredential{}, err
	}
	keyID, ciphertext, err := service.securityConfig.Keys.encrypt(webAuthnCredentialDomain(userID, credential.ID), encoded)
	if err != nil {
		return StoredWebAuthnCredential{}, err
	}
	return StoredWebAuthnCredential{ID: append([]byte(nil), credential.ID...), Name: name, CredentialKeyID: keyID, CredentialCiphertext: ciphertext}, nil
}

func webAuthnCredentialDomain(userID string, credentialID []byte) string {
	return "webauthn:" + userID + ":" + base64.RawURLEncoding.EncodeToString(credentialID)
}

func credentialDescriptors(credentials []StoredWebAuthnCredential) []protocol.CredentialDescriptor {
	result := make([]protocol.CredentialDescriptor, 0, len(credentials))
	for _, credential := range credentials {
		result = append(result, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: credential.ID})
	}
	return result
}

func decodeCredentialID(value string) ([]byte, error) {
	id, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(id) < 16 || len(id) > 1024 {
		return nil, ErrInvalidInput
	}
	return id, nil
}

func credentialID(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

var _ webauthnlib.User = webAuthnUser{}
