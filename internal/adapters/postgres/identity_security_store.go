package postgres

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *IdentityStore) LoadProfile(ctx context.Context, userID string, now time.Time) (auth.Profile, error) {
	id, err := uuid(userID)
	if err != nil {
		return auth.Profile{}, auth.ErrNotFound
	}
	row, err := store.queries.GetOwnSecurityProfile(ctx, id)
	if err != nil {
		return auth.Profile{}, mapNotFound(err)
	}
	profile := auth.Profile{
		User: auth.User{
			ID: row.UID, Username: row.UUsername, DisplayName: row.DisplayName, Email: row.Email,
			Role: auth.Role(row.Role), Active: row.Active, Version: row.Version, DriverID: row.DriverID,
			Salutation: row.Salutation, WorkPhoneRaw: row.WorkPhoneRaw, WorkPhoneNormalized: row.WorkPhoneNormalized,
			EmailVerifiedAt: optionalTime(row.EmailVerifiedAt), TOTPEnabled: row.TotpEnabledAt.Valid, PasskeyEnabled: row.PasskeyCount > 0,
		},
		PendingEmailID: row.PendingEmailID, PendingEmail: row.PendingEmail,
		PendingEmailLastSentAt: optionalTime(row.PendingEmailLastSentAt), PendingEmailExpiresAt: optionalTime(row.PendingEmailExpiresAt),
		PendingEmailDeliveryStatus: row.PendingEmailDeliveryStatus,
		TOTPName:                   row.TotpName, TOTPEnabledAt: optionalTime(row.TotpEnabledAt), RecoveryCodeCount: row.RecoveryCodeCount,
	}
	credentialRows, err := store.queries.ListWebAuthnCredentials(ctx, id)
	if err != nil {
		return auth.Profile{}, err
	}
	profile.Passkeys = make([]auth.SecurityMethod, 0, len(credentialRows))
	for _, credential := range credentialRows {
		profile.Passkeys = append(profile.Passkeys, auth.SecurityMethod{
			ID: append([]byte(nil), credential.CredentialID...), EncodedID: base64.RawURLEncoding.EncodeToString(credential.CredentialID), Name: credential.Name,
			CreatedAt: credential.CreatedAt.Time, LastUsedAt: optionalTime(credential.LastUsedAt),
		})
	}
	sessionRows, err := store.queries.ListUserSessions(ctx, dbgen.ListUserSessionsParams{UserID: id, NowUtc: timestamp(now.UTC())})
	if err != nil {
		return auth.Profile{}, err
	}
	profile.Sessions = make([]auth.ActiveSession, 0, len(sessionRows))
	for _, session := range sessionRows {
		profile.Sessions = append(profile.Sessions, auth.ActiveSession{
			ID: session.ID, DeviceLabel: session.DeviceLabel, CreatedAt: session.CreatedAt.Time,
			LastUsedAt: session.LastUsedAt.Time, IdleExpiresAt: session.IdleExpiresAt.Time, AbsoluteExpiresAt: session.AbsoluteExpiresAt.Time,
		})
	}
	return profile, nil
}

func (store *IdentityStore) UpdateOwnProfile(ctx context.Context, actor auth.Actor, input auth.UpdateOwnProfileInput) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.UpdateOwnProfile(ctx, dbgen.UpdateOwnProfileParams{
			DisplayName: input.DisplayName, Salutation: input.Salutation, WorkPhoneRaw: input.WorkPhoneRaw,
			WorkPhoneNormalized: input.WorkPhoneNormalized, ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "user.profile_updated", "user", actor.UserID, input.RequestID, []string{"display_name", "salutation", "work_phone"})
	})
}

func (store *IdentityStore) StartEmailVerification(ctx context.Context, actor auth.Actor, verification auth.EmailVerification, maxAttempts int32, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		userID, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		verificationID, err := uuid(verification.ID)
		if err != nil {
			return auth.ErrInvalidInput
		}
		if _, err := queries.CancelPendingEmailVerification(ctx, userID); err != nil {
			return err
		}
		if err := queries.InsertEmailVerification(ctx, dbgen.InsertEmailVerificationParams{
			ID: verificationID, UserID: userID, Email: verification.Email, TokenHash: verification.TokenHash,
			TokenKeyID: verification.TokenKeyID, TokenVersion: verification.TokenVersion, ExpiresAt: timestamp(verification.ExpiresAt),
		}); err != nil {
			return mapConflict(err)
		}
		if err := queries.InsertEmailVerificationOutbox(ctx, dbgen.InsertEmailVerificationOutboxParams{
			VerificationID: verificationID, TokenVersion: strconv.Itoa(int(verification.TokenVersion)), MaxAttempts: maxAttempts,
		}); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.email_change_requested", "user", actor.UserID, requestID, []string{"pending_email"})
	})
}

func (store *IdentityStore) PendingEmailVerification(ctx context.Context, userID string) (auth.EmailVerification, error) {
	id, err := uuid(userID)
	if err != nil {
		return auth.EmailVerification{}, auth.ErrNotFound
	}
	row, err := store.queries.GetPendingEmailVerification(ctx, id)
	if err != nil {
		return auth.EmailVerification{}, mapNotFound(err)
	}
	return auth.EmailVerification{
		ID: row.ID, UserID: row.UserID, Email: row.Email, TokenKeyID: row.TokenKeyID, TokenVersion: row.TokenVersion,
		SendCount: row.SendCount, LastSentAt: row.LastSentAt.Time, ExpiresAt: row.ExpiresAt.Time,
	}, nil
}

func (store *IdentityStore) ResendEmailVerification(ctx context.Context, actor auth.Actor, verification auth.EmailVerification, resendBefore time.Time, maxAttempts int32, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		userID, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		verificationID, err := uuid(verification.ID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.UpdateEmailVerificationForResend(ctx, dbgen.UpdateEmailVerificationForResendParams{
			TokenHash: verification.TokenHash, TokenKeyID: verification.TokenKeyID, TokenVersion: verification.TokenVersion,
			ExpiresAt: timestamp(verification.ExpiresAt), ID: verificationID, UserID: userID, ResendBefore: timestamp(resendBefore),
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrRateLimited
		}
		if err := queries.InsertEmailVerificationOutbox(ctx, dbgen.InsertEmailVerificationOutboxParams{
			VerificationID: verificationID, TokenVersion: strconv.Itoa(int(verification.TokenVersion)), MaxAttempts: maxAttempts,
		}); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.email_verification_resent", "user", actor.UserID, requestID, []string{"pending_email_delivery"})
	})
}

func (store *IdentityStore) CancelEmailVerification(ctx context.Context, actor auth.Actor, requestID string) error {
	id, err := uuid(actor.UserID)
	if err != nil {
		return auth.ErrNotFound
	}
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		rows, err := queries.CancelPendingEmailVerification(ctx, id)
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrNotFound
		}
		return insertAudit(ctx, queries, actor, "user.email_change_cancelled", "user", actor.UserID, requestID, []string{"pending_email"})
	})
}

func (store *IdentityStore) VerifyEmail(ctx context.Context, tokenHash []byte, now time.Time, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		row, err := queries.GetEmailVerificationForUpdate(ctx, tokenHash)
		if err != nil {
			return mapNotFound(err)
		}
		verificationID, err := uuid(row.ID)
		if err != nil {
			return auth.ErrNotFound
		}
		if row.Status != "pending" {
			return auth.ErrNotFound
		}
		if !now.Before(row.ExpiresAt.Time) {
			return auth.ErrVerificationExpired
		}
		userID, err := uuid(row.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		if err := queries.ApplyVerifiedEmail(ctx, dbgen.ApplyVerifiedEmailParams{Email: row.Email, UserID: userID}); err != nil {
			return mapConflict(err)
		}
		rows, err := queries.MarkEmailVerificationVerified(ctx, verificationID)
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		return insertPublicAudit(ctx, queries, "user.email_verified", "user", row.UserID, requestID, []string{"email", "email_verified_at"})
	})
}

func (store *IdentityStore) UpsertTOTPEnrollment(ctx context.Context, actor auth.Actor, credential auth.TOTPCredential, requestID string) error {
	id, err := uuid(actor.UserID)
	if err != nil {
		return auth.ErrNotFound
	}
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		rows, err := queries.UpsertTOTPEnrollment(ctx, dbgen.UpsertTOTPEnrollmentParams{
			UserID: id, Name: credential.Name, SecretKeyID: credential.SecretKeyID, SecretCiphertext: credential.SecretCiphertext,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "user.totp_enrollment_started", "user", actor.UserID, requestID, []string{"totp_pending"})
	})
}

func (store *IdentityStore) TOTPCredential(ctx context.Context, userID string) (auth.TOTPCredential, error) {
	id, err := uuid(userID)
	if err != nil {
		return auth.TOTPCredential{}, auth.ErrNotFound
	}
	row, err := store.queries.GetTOTPCredential(ctx, id)
	if err != nil {
		return auth.TOTPCredential{}, mapNotFound(err)
	}
	return auth.TOTPCredential{
		Name: row.Name, SecretKeyID: row.SecretKeyID, SecretCiphertext: append([]byte(nil), row.SecretCiphertext...),
		EnabledAt: optionalTime(row.EnabledAt), LastUsedStep: row.LastUsedStep,
	}, nil
}

func (store *IdentityStore) EnableTOTP(ctx context.Context, actor auth.Actor, recoveryHashes [][]byte, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.EnableTOTPCredential(ctx, id)
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		if err := insertRecoveryHashesIfEmpty(ctx, queries, id, recoveryHashes); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.totp_enabled", "user", actor.UserID, requestID, []string{"totp", "recovery_codes"})
	})
}

func (store *IdentityStore) RenameTOTP(ctx context.Context, actor auth.Actor, name, requestID string) error {
	return store.securityRowsMutation(ctx, actor, requestID, "user.totp_renamed", []string{"totp_name"}, func(queries *dbgen.Queries, id pgtype.UUID) (int64, error) {
		return queries.RenameTOTPCredential(ctx, dbgen.RenameTOTPCredentialParams{Name: name, UserID: id})
	})
}

func (store *IdentityStore) DeleteTOTP(ctx context.Context, actor auth.Actor, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.DeleteTOTPCredential(ctx, id)
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrNotFound
		}
		if err := deleteRecoveryCodesWithoutFactor(ctx, queries, id); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.totp_deleted", "user", actor.UserID, requestID, []string{"totp", "recovery_codes"})
	})
}

func (store *IdentityStore) WebAuthnCredentials(ctx context.Context, userID string) ([]auth.StoredWebAuthnCredential, error) {
	id, err := uuid(userID)
	if err != nil {
		return nil, auth.ErrNotFound
	}
	rows, err := store.queries.ListWebAuthnCredentials(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]auth.StoredWebAuthnCredential, 0, len(rows))
	for _, row := range rows {
		result = append(result, auth.StoredWebAuthnCredential{
			ID: append([]byte(nil), row.CredentialID...), Name: row.Name, CredentialKeyID: row.CredentialKeyID,
			CredentialCiphertext: append([]byte(nil), row.CredentialCiphertext...), CreatedAt: row.CreatedAt.Time, LastUsedAt: optionalTime(row.LastUsedAt),
		})
	}
	return result, nil
}

func (store *IdentityStore) SaveWebAuthnRegistrationChallenge(ctx context.Context, actor auth.Actor, sessionID string, data []byte, expiresAt time.Time) error {
	session, err := uuid(sessionID)
	if err != nil {
		return auth.ErrInvalidSession
	}
	user, err := uuid(actor.UserID)
	if err != nil {
		return auth.ErrNotFound
	}
	return store.queries.UpsertWebAuthnRegistrationChallenge(ctx, dbgen.UpsertWebAuthnRegistrationChallengeParams{
		SessionID: session, UserID: user, SessionData: data, ExpiresAt: timestamp(expiresAt),
	})
}

func (store *IdentityStore) WebAuthnRegistrationChallenge(ctx context.Context, actor auth.Actor, sessionID string) ([]byte, time.Time, error) {
	session, err := uuid(sessionID)
	if err != nil {
		return nil, time.Time{}, auth.ErrNotFound
	}
	user, err := uuid(actor.UserID)
	if err != nil {
		return nil, time.Time{}, auth.ErrNotFound
	}
	row, err := store.queries.GetWebAuthnRegistrationChallenge(ctx, dbgen.GetWebAuthnRegistrationChallengeParams{SessionID: session, UserID: user})
	if err != nil {
		return nil, time.Time{}, mapNotFound(err)
	}
	return append([]byte(nil), row.SessionData...), row.ExpiresAt.Time, nil
}

func (store *IdentityStore) AddWebAuthnCredential(ctx context.Context, actor auth.Actor, sessionID string, credential auth.StoredWebAuthnCredential, recoveryHashes [][]byte, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		userID, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		session, err := uuid(sessionID)
		if err != nil {
			return auth.ErrInvalidSession
		}
		rows, err := queries.DeleteWebAuthnRegistrationChallenge(ctx, dbgen.DeleteWebAuthnRegistrationChallengeParams{SessionID: session, UserID: userID})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		if err := queries.InsertWebAuthnCredential(ctx, dbgen.InsertWebAuthnCredentialParams{
			CredentialID: credential.ID, UserID: userID, Name: credential.Name, CredentialKeyID: credential.CredentialKeyID,
			CredentialCiphertext: credential.CredentialCiphertext,
		}); err != nil {
			return mapConflict(err)
		}
		if err := insertRecoveryHashesIfEmpty(ctx, queries, userID, recoveryHashes); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.passkey_added", "user", actor.UserID, requestID, []string{"passkeys", "recovery_codes"})
	})
}

func (store *IdentityStore) RenameWebAuthnCredential(ctx context.Context, actor auth.Actor, credentialID []byte, name, requestID string) error {
	return store.securityRowsMutation(ctx, actor, requestID, "user.passkey_renamed", []string{"passkey_name"}, func(queries *dbgen.Queries, id pgtype.UUID) (int64, error) {
		return queries.RenameWebAuthnCredential(ctx, dbgen.RenameWebAuthnCredentialParams{Name: name, CredentialID: credentialID, UserID: id})
	})
}

func (store *IdentityStore) DeleteWebAuthnCredential(ctx context.Context, actor auth.Actor, credentialID []byte, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.DeleteWebAuthnCredential(ctx, dbgen.DeleteWebAuthnCredentialParams{CredentialID: credentialID, UserID: id})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrNotFound
		}
		if err := deleteRecoveryCodesWithoutFactor(ctx, queries, id); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.passkey_deleted", "user", actor.UserID, requestID, []string{"passkeys", "recovery_codes"})
	})
}

func (store *IdentityStore) ReplaceRecoveryCodes(ctx context.Context, actor auth.Actor, hashes [][]byte, requestID string) error {
	if len(hashes) == 0 {
		return auth.ErrInvalidInput
	}
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		if err := queries.DeleteRecoveryCodes(ctx, id); err != nil {
			return err
		}
		if err := insertRecoveryHashes(ctx, queries, id, hashes); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.recovery_codes_rotated", "user", actor.UserID, requestID, []string{"recovery_codes"})
	})
}

func (store *IdentityStore) StartLoginChallenge(ctx context.Context, user auth.User, tokenHash []byte, expiresAt time.Time, replacementHash, rateKey []byte, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(user.ID)
		if err != nil {
			return auth.ErrNotFound
		}
		if len(replacementHash) > 0 {
			rows, err := queries.UpdatePassword(ctx, dbgen.UpdatePasswordParams{PasswordHash: string(replacementHash), MustChangePassword: user.MustChangePassword, ID: id, ExpectedVersion: user.Version})
			if err != nil {
				return err
			}
			if rows != 1 {
				return auth.ErrConflict
			}
		}
		if err := queries.InsertLoginChallenge(ctx, dbgen.InsertLoginChallengeParams{UserID: id, TokenHash: tokenHash, ExpiresAt: timestamp(expiresAt)}); err != nil {
			return err
		}
		if err := queries.ClearLoginFailures(ctx, rateKey); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actorForUser(user), "auth.second_factor_started", "user", user.ID, requestID, []string{"login_challenge"})
	})
}

func (store *IdentityStore) LoginChallenge(ctx context.Context, tokenHash []byte) (auth.LoginChallenge, error) {
	row, err := store.queries.GetLoginChallenge(ctx, tokenHash)
	if err != nil {
		return auth.LoginChallenge{}, mapNotFound(err)
	}
	return auth.LoginChallenge{
		ID: row.CID, ExpiresAt: row.ExpiresAt.Time, AttemptCount: row.AttemptCount, WebAuthnSession: append([]byte(nil), row.WebauthnSession...),
		User: auth.User{
			ID: row.CUserID, Username: row.UUsername, DisplayName: row.DisplayName, Email: row.Email, Role: auth.Role(row.Role),
			PasswordHash: row.PasswordHash, MustChangePassword: row.MustChangePassword, Active: row.Active, Version: row.Version,
			DriverID: row.DriverID, WebAuthnUserHandle: append([]byte(nil), row.WebauthnUserHandle...),
			TOTPEnabled: row.TotpEnabled, PasskeyEnabled: row.PasskeyEnabled,
		},
	}, nil
}

func (store *IdentityStore) SetLoginWebAuthnSession(ctx context.Context, tokenHash, data []byte) error {
	rows, err := store.queries.SetLoginWebAuthnSession(ctx, dbgen.SetLoginWebAuthnSessionParams{SessionData: data, TokenHash: tokenHash})
	if err != nil {
		return err
	}
	if rows != 1 {
		return auth.ErrNotFound
	}
	return nil
}

func (store *IdentityStore) RecordLoginChallengeFailure(ctx context.Context, tokenHash []byte) error {
	_, err := store.queries.RecordLoginChallengeFailure(ctx, tokenHash)
	return err
}

func (store *IdentityStore) CompleteLogin(ctx context.Context, input auth.CompleteLoginInput) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		challenge, err := queries.GetLoginChallenge(ctx, input.ChallengeTokenHash)
		if err != nil || challenge.CUserID != input.NewSession.UserID || !challenge.Active {
			return auth.ErrInvalidMFA
		}
		rows, err := queries.ConsumeLoginChallenge(ctx, dbgen.ConsumeLoginChallengeParams{TokenHash: input.ChallengeTokenHash, NowUtc: timestamp(input.Now)})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrInvalidMFA
		}
		userID, err := uuid(input.NewSession.UserID)
		if err != nil {
			return auth.ErrInvalidMFA
		}
		switch input.Factor {
		case "totp":
			if input.TOTPStep == nil {
				return auth.ErrInvalidMFA
			}
			rows, err = queries.RecordTOTPStep(ctx, dbgen.RecordTOTPStepParams{Step: input.TOTPStep, UserID: userID})
		case "recovery":
			rows, err = queries.ConsumeRecoveryCode(ctx, dbgen.ConsumeRecoveryCodeParams{UserID: userID, CodeHash: input.RecoveryCodeHash})
		case "webauthn":
			if input.Credential == nil {
				return auth.ErrInvalidMFA
			}
			rows, err = queries.UpdateWebAuthnCredential(ctx, dbgen.UpdateWebAuthnCredentialParams{
				CredentialKeyID: input.Credential.CredentialKeyID, CredentialCiphertext: input.Credential.CredentialCiphertext,
				CredentialID: input.Credential.ID, UserID: userID,
			})
		default:
			return auth.ErrInvalidMFA
		}
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrInvalidMFA
		}
		if _, err := queries.InsertSession(ctx, dbgen.InsertSessionParams{
			UserID: userID, TokenHash: input.NewSession.TokenHash, CsrfTokenHash: input.NewSession.CSRFTokenHash,
			IdleExpiresAt: timestamp(input.NewSession.IdleExpiresAt), AbsoluteExpiresAt: timestamp(input.NewSession.AbsoluteExpiresAt), DeviceLabel: input.NewSession.DeviceLabel,
		}); err != nil {
			return err
		}
		if err := queries.MarkLogin(ctx, userID); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actorForUser(auth.User{ID: input.NewSession.UserID, Username: challenge.UUsername, DisplayName: challenge.DisplayName, Role: auth.Role(challenge.Role)}), "auth.login", "user", input.NewSession.UserID, input.RequestID, []string{"last_login_at", "session", "second_factor"})
	})
}

func (store *IdentityStore) RevokeOwnedSession(ctx context.Context, actor auth.Actor, sessionID, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(sessionID)
		if err != nil {
			return auth.ErrNotFound
		}
		userID, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.RevokeOwnedSessionByID(ctx, dbgen.RevokeOwnedSessionByIDParams{ID: id, UserID: userID})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrNotFound
		}
		return insertAudit(ctx, queries, actor, "user.session_revoked", "user", actor.UserID, requestID, []string{"session"})
	})
}

func (store *IdentityStore) RevokeAllSessions(ctx context.Context, actor auth.Actor, requestID string) error {
	id, err := uuid(actor.UserID)
	if err != nil {
		return auth.ErrNotFound
	}
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		if err := queries.RevokeUserSessions(ctx, id); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.sessions_revoked", "user", actor.UserID, requestID, []string{"sessions"})
	})
}

func (store *IdentityStore) ResetUserSecurity(ctx context.Context, actor auth.Actor, userID string, expectedVersion int32, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(userID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := queries.ForceOwnSecurityRecovery(ctx, dbgen.ForceOwnSecurityRecoveryParams{ID: id, ExpectedVersion: expectedVersion})
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrConflict
		}
		if _, err := queries.DeleteTOTPCredential(ctx, id); err != nil {
			return err
		}
		if err := queries.DeleteWebAuthnCredentialsForUser(ctx, id); err != nil {
			return err
		}
		if err := queries.DeleteRecoveryCodes(ctx, id); err != nil {
			return err
		}
		if err := queries.DeleteLoginChallengesForUser(ctx, id); err != nil {
			return err
		}
		if err := queries.DeleteWebAuthnRegistrationChallengesForUser(ctx, id); err != nil {
			return err
		}
		if err := queries.RevokeUserSessions(ctx, id); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "user.security_recovered", "user", userID, requestID, []string{"totp", "passkeys", "recovery_codes", "sessions", "must_change_password"})
	})
}

func (store *IdentityStore) securityRowsMutation(ctx context.Context, actor auth.Actor, requestID, action string, changed []string, mutate func(*dbgen.Queries, pgtype.UUID) (int64, error)) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(actor.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		rows, err := mutate(queries, id)
		if err != nil {
			return err
		}
		if rows != 1 {
			return auth.ErrNotFound
		}
		return insertAudit(ctx, queries, actor, action, "user", actor.UserID, requestID, changed)
	})
}

func insertRecoveryHashesIfEmpty(ctx context.Context, queries *dbgen.Queries, userID pgtype.UUID, hashes [][]byte) error {
	count, err := queries.CountRecoveryCodes(ctx, userID)
	if err != nil || count > 0 || len(hashes) == 0 {
		return err
	}
	return insertRecoveryHashes(ctx, queries, userID, hashes)
}

func deleteRecoveryCodesWithoutFactor(ctx context.Context, queries *dbgen.Queries, userID pgtype.UUID) error {
	factorCount, err := queries.CountEnabledSecurityFactors(ctx, userID)
	if err != nil || factorCount > 0 {
		return err
	}
	return queries.DeleteRecoveryCodes(ctx, userID)
}

func insertRecoveryHashes(ctx context.Context, queries *dbgen.Queries, userID pgtype.UUID, hashes [][]byte) error {
	for _, hash := range hashes {
		if err := queries.InsertRecoveryCode(ctx, dbgen.InsertRecoveryCodeParams{UserID: userID, CodeHash: hash}); err != nil {
			return err
		}
	}
	return nil
}

var _ auth.SecurityStore = (*IdentityStore)(nil)
