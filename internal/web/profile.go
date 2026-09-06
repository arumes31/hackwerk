package web

import (
	"errors"
	"log/slog"
	"net/http"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type profileRenderState struct {
	Error          string
	Notice         string
	Status         int
	TOTPEnrollment *auth.TOTPEnrollment
	RecoveryCodes  []string
}

func profilePage(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderProfile(response, request, identity, dependencies, page, profileRenderState{Status: http.StatusOK, Notice: profileNotice(request.URL.Query().Get("status"))})
	}
}

func renderProfile(response http.ResponseWriter, request *http.Request, identity *auth.Service, dependencies Dependencies, page templates.PageData, state profileRenderState) {
	response.Header().Set("Cache-Control", "no-store")
	session, _ := sessionFromContext(request.Context())
	profile, err := identity.Profile(request.Context(), session.Actor)
	if err != nil {
		render(response, request, templates.Error(page, http.StatusServiceUnavailable, "Profil nicht verfügbar", "Das persönliche Profil kann derzeit nicht geladen werden."), http.StatusServiceUnavailable, dependencies.Logger)
		return
	}
	if state.Status == 0 {
		state.Status = http.StatusOK
	}
	render(response, request, templates.Profile(templates.ProfileData{
		Shell: shell(request, page, dependencies.Config.Auth.CSRFCookieName), Profile: profile, CurrentSessionID: session.ID,
		Error: state.Error, Notice: state.Notice, TOTPEnrollment: state.TOTPEnrollment, RecoveryCodes: state.RecoveryCodes,
	}), state.Status, dependencies.Logger)
}

func updateOwnProfile(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = identity.UpdateOwnProfile(request.Context(), session.Actor, auth.UpdateOwnProfileInput{
				DisplayName: request.Form.Get("display_name"), Salutation: request.Form.Get("salutation"), WorkPhoneRaw: request.Form.Get("work_phone"),
				ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context()),
			})
		}
		if err != nil {
			message := "Anzeigename, Anrede oder Telefonnummer sind ungültig."
			status := http.StatusUnprocessableEntity
			if errors.Is(err, auth.ErrConflict) {
				message, status = "Das Profil wurde zwischenzeitlich geändert. Bitte laden Sie den aktuellen Stand neu.", http.StatusConflict
			}
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: message, Status: status})
			return
		}
		http.Redirect(response, request, "/profile?status=profile_saved", http.StatusSeeOther)
	}
}

func requestProfileEmail(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := identity.RequestEmailChange(request.Context(), session.Actor, request.Form.Get("email"), middleware.GetReqID(request.Context())); err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Geben Sie eine gültige, von der aktuellen Adresse abweichende E-Mail-Adresse ein.", Status: http.StatusUnprocessableEntity})
			return
		}
		http.Redirect(response, request, "/profile?status=email_requested", http.StatusSeeOther)
	}
}

func resendProfileEmail(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		err := identity.ResendEmailChange(request.Context(), session.Actor, middleware.GetReqID(request.Context()))
		if err != nil {
			message := "Die Bestätigung kann derzeit nicht erneut gesendet werden."
			status := http.StatusUnprocessableEntity
			if errors.Is(err, auth.ErrRateLimited) {
				message, status = "Bitte warten Sie kurz, bevor Sie die Bestätigung erneut senden.", http.StatusTooManyRequests
			}
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: message, Status: status})
			return
		}
		http.Redirect(response, request, "/profile?status=email_resent", http.StatusSeeOther)
	}
}

func cancelProfileEmail(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := identity.CancelEmailChange(request.Context(), session.Actor, middleware.GetReqID(request.Context())); err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Die ausstehende E-Mail-Änderung ist nicht mehr verfügbar.", Status: http.StatusConflict})
			return
		}
		http.Redirect(response, request, "/profile?status=email_cancelled", http.StatusSeeOther)
	}
}

func verifyProfileEmail(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Referrer-Policy", "no-referrer")
		err := identity.VerifyEmail(request.Context(), request.URL.Query().Get("token"), middleware.GetReqID(request.Context()))
		if err != nil {
			status := http.StatusUnprocessableEntity
			message := "Der Bestätigungslink ist ungültig oder wurde bereits verwendet."
			if errors.Is(err, auth.ErrVerificationExpired) {
				status, message = http.StatusGone, "Der Bestätigungslink ist abgelaufen. Fordern Sie im Profil einen neuen Link an."
			}
			render(response, request, templates.Error(page, status, "E-Mail nicht bestätigt", message), status, dependencies.Logger)
			return
		}
		if _, ok := sessionFromContext(request.Context()); ok {
			http.Redirect(response, request, "/profile?status=email_verified", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/login?email_verified=1", http.StatusSeeOther)
	}
}

func beginTOTPEnrollment(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		enrollment, err := identity.BeginTOTPEnrollment(request.Context(), session.Actor, request.Form.Get("name"), middleware.GetReqID(request.Context()))
		if err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Die Authenticator-App konnte nicht vorbereitet werden.", Status: http.StatusUnprocessableEntity})
			return
		}
		renderProfile(response, request, identity, dependencies, page, profileRenderState{Status: http.StatusOK, TOTPEnrollment: &enrollment})
	}
}

func confirmTOTPEnrollment(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		codes, err := identity.ConfirmTOTPEnrollment(request.Context(), session.Actor, request.Form.Get("code"), middleware.GetReqID(request.Context()))
		if err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Der sechsstellige Code ist ungültig oder abgelaufen. Starten Sie die Einrichtung bei Bedarf neu.", Status: http.StatusUnprocessableEntity})
			return
		}
		renderProfile(response, request, identity, dependencies, page, profileRenderState{Status: http.StatusOK, Notice: "Authenticator-App aktiviert. Speichern Sie jetzt die einmalig angezeigten Recovery-Codes.", RecoveryCodes: codes})
	}
}

func renameTOTP(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return profileMutation(identity, dependencies, page, "totp_renamed", "Der Name der Authenticator-App konnte nicht gespeichert werden.", func(request *http.Request, actor auth.Actor) error {
		return identity.RenameTOTP(request.Context(), actor, request.Form.Get("name"), middleware.GetReqID(request.Context()))
	})
}

func deleteTOTP(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return profileMutation(identity, dependencies, page, "totp_deleted", "Die Authenticator-App konnte nicht entfernt werden.", func(request *http.Request, actor auth.Actor) error {
		return identity.DeleteTOTP(request.Context(), actor, middleware.GetReqID(request.Context()))
	})
}

func rotateRecoveryCodes(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		codes, err := identity.RotateRecoveryCodes(request.Context(), session.Actor, middleware.GetReqID(request.Context()))
		if err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Recovery-Codes können erst mit einer eingerichteten Sicherheitsmethode erzeugt werden.", Status: http.StatusUnprocessableEntity})
			return
		}
		renderProfile(response, request, identity, dependencies, page, profileRenderState{Status: http.StatusOK, Notice: "Neue Recovery-Codes erzeugt. Alle bisherigen Codes sind ungültig.", RecoveryCodes: codes})
	}
}

func beginPasskeyRegistration(identity *auth.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		options, err := identity.BeginPasskeyRegistration(request.Context(), session.Actor, session.ID)
		if err != nil {
			writeJSON(response, http.StatusUnprocessableEntity, map[string]string{"error": "passkey_registration_unavailable"})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, options)
	}
}

func finishPasskeyRegistration(identity *auth.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		codes, err := identity.FinishPasskeyRegistration(request.Context(), session.Actor, session.ID, request.URL.Query().Get("name"), middleware.GetReqID(request.Context()), request)
		if err != nil {
			writeJSON(response, http.StatusUnprocessableEntity, map[string]string{"error": "passkey_registration_failed"})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "recovery_codes": codes})
	}
}

func renamePasskey(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return profileMutation(identity, dependencies, page, "passkey_renamed", "Der Passkey-Name konnte nicht gespeichert werden.", func(request *http.Request, actor auth.Actor) error {
		return identity.RenamePasskey(request.Context(), actor, chi.URLParam(request, "credentialID"), request.Form.Get("name"), middleware.GetReqID(request.Context()))
	})
}

func deletePasskey(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return profileMutation(identity, dependencies, page, "passkey_deleted", "Der Passkey konnte nicht entfernt werden.", func(request *http.Request, actor auth.Actor) error {
		return identity.DeletePasskey(request.Context(), actor, chi.URLParam(request, "credentialID"), middleware.GetReqID(request.Context()))
	})
}

func revokeProfileSession(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := chi.URLParam(request, "sessionID")
		if err := identity.RevokeSession(request.Context(), session.Actor, target, middleware.GetReqID(request.Context())); err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Die Sitzung ist nicht mehr aktiv oder gehört nicht zu diesem Konto.", Status: http.StatusNotFound})
			return
		}
		if target == session.ID {
			clearAuthCookies(response, dependencies)
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/profile?status=session_revoked", http.StatusSeeOther)
	}
}

func revokeAllProfileSessions(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := identity.RevokeAllSessions(request.Context(), session.Actor, middleware.GetReqID(request.Context())); err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: "Die Sitzungen konnten derzeit nicht widerrufen werden.", Status: http.StatusServiceUnavailable})
			return
		}
		clearAuthCookies(response, dependencies)
		http.Redirect(response, request, "/login", http.StatusSeeOther)
	}
}

func profileMutation(identity *auth.Service, dependencies Dependencies, page templates.PageData, success, failure string, mutation func(*http.Request, auth.Actor) error) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := mutation(request, session.Actor); err != nil {
			renderProfile(response, request, identity, dependencies, page, profileRenderState{Error: failure, Status: http.StatusUnprocessableEntity})
			return
		}
		http.Redirect(response, request, "/profile?status="+success, http.StatusSeeOther)
	}
}

func mfaPage(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		cookie, err := request.Cookie(dependencies.Config.Auth.MFACookieName)
		if err != nil {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		options, err := identity.MFAOptions(request.Context(), cookie.Value)
		if err != nil {
			clearMFACookie(response, dependencies)
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		render(response, request, templates.MFA(templates.MFAData{Page: page, TOTPEnabled: options.TOTPEnabled, PasskeyEnabled: options.PasskeyEnabled}), http.StatusOK, dependencies.Logger)
	}
}

func completeMFALogin(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !sameOrigin(request) {
			renderMFAError(response, request, identity, dependencies, page, http.StatusForbidden)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maxFormBytes)
		if err := request.ParseForm(); err != nil {
			renderMFAError(response, request, identity, dependencies, page, http.StatusBadRequest)
			return
		}
		cookie, err := request.Cookie(dependencies.Config.Auth.MFACookieName)
		if err != nil {
			renderMFAError(response, request, identity, dependencies, page, http.StatusUnauthorized)
			return
		}
		var tokens auth.SessionTokens
		switch request.Form.Get("method") {
		case "totp":
			tokens, err = identity.CompleteTOTPLogin(request.Context(), cookie.Value, request.Form.Get("code"), auth.DeviceLabel(request.UserAgent()), middleware.GetReqID(request.Context()))
		case "recovery":
			tokens, err = identity.CompleteRecoveryLogin(request.Context(), cookie.Value, request.Form.Get("code"), auth.DeviceLabel(request.UserAgent()), middleware.GetReqID(request.Context()))
		default:
			err = auth.ErrInvalidMFA
		}
		if err != nil {
			renderMFAError(response, request, identity, dependencies, page, http.StatusUnauthorized)
			return
		}
		finishMFAResponse(response, request, dependencies, tokens)
	}
}

func beginPasskeyLogin(identity *auth.Service, dependencies Dependencies) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !sameOrigin(request) {
			writeJSON(response, http.StatusForbidden, map[string]string{"error": "mfa_failed"})
			return
		}
		cookie, err := request.Cookie(dependencies.Config.Auth.MFACookieName)
		if err != nil {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "mfa_failed"})
			return
		}
		options, err := identity.BeginPasskeyLogin(request.Context(), cookie.Value)
		if err != nil {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "mfa_failed"})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, options)
	}
}

func finishPasskeyLogin(identity *auth.Service, dependencies Dependencies) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !sameOrigin(request) {
			writeJSON(response, http.StatusForbidden, map[string]string{"error": "mfa_failed"})
			return
		}
		cookie, err := request.Cookie(dependencies.Config.Auth.MFACookieName)
		if err != nil {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "mfa_failed"})
			return
		}
		tokens, err := identity.FinishPasskeyLogin(request.Context(), cookie.Value, auth.DeviceLabel(request.UserAgent()), middleware.GetReqID(request.Context()), request)
		if err != nil {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "mfa_failed"})
			return
		}
		setAuthCookies(response, dependencies, tokens)
		clearMFACookie(response, dependencies)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "redirect": mfaRedirect(tokens)})
	}
}

func renderMFAError(response http.ResponseWriter, request *http.Request, identity *auth.Service, dependencies Dependencies, page templates.PageData, status int) {
	data := templates.MFAData{Page: page, Error: "Die Sicherheitsprüfung ist fehlgeschlagen. Prüfen Sie den Code oder verwenden Sie eine andere Methode."}
	if cookie, err := request.Cookie(dependencies.Config.Auth.MFACookieName); err == nil {
		if options, optionErr := identity.MFAOptions(request.Context(), cookie.Value); optionErr == nil {
			data.TOTPEnabled, data.PasskeyEnabled = options.TOTPEnabled, options.PasskeyEnabled
		}
	}
	render(response, request, templates.MFA(data), status, dependencies.Logger)
}

func finishMFAResponse(response http.ResponseWriter, request *http.Request, dependencies Dependencies, tokens auth.SessionTokens) {
	setAuthCookies(response, dependencies, tokens)
	clearMFACookie(response, dependencies)
	http.Redirect(response, request, mfaRedirect(tokens), http.StatusSeeOther)
}

func mfaRedirect(tokens auth.SessionTokens) string {
	if tokens.Actor.MustChangePassword {
		return "/password"
	}
	return "/dashboard"
}

func resetUserSecurity(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = identity.ResetUserSecurity(request.Context(), session.Actor, chi.URLParam(request, "userID"), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{Error: "Die Sicherheitsmethoden konnten nicht zurückgesetzt werden. Laden Sie den aktuellen Stand neu.", EditedUserID: chi.URLParam(request, "userID"), Status: http.StatusConflict})
			return
		}
		http.Redirect(response, request, "/admin/users", http.StatusSeeOther)
	}
}

func profileNotice(status string) string {
	// #nosec G101 -- these are UI status identifiers and localized notices, not credentials.
	return map[string]string{
		"profile_saved": "Persönliche Daten gespeichert.", "email_requested": "Bestätigungsmail vorgemerkt. Die bisherige Adresse bleibt aktiv.",
		"email_resent": "Bestätigungsmail erneut vorgemerkt.", "email_cancelled": "Ausstehende E-Mail-Änderung abgebrochen.",
		"email_verified": "Neue E-Mail-Adresse bestätigt.", "totp_renamed": "Authenticator-App umbenannt.", "totp_deleted": "Authenticator-App entfernt.",
		"passkey_added": "Passkey hinzugefügt.", "passkey_renamed": "Passkey umbenannt.", "passkey_deleted": "Passkey entfernt.", "session_revoked": "Sitzung abgemeldet.",
	}[status]
}
