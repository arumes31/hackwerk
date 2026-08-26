package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"example.invalid/hackplan/internal/auth"
	dashboarddomain "example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/web/templates"
	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const maxFormBytes = 1 << 20

type sessionContextKey struct{}

func registerIdentityRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	identity := dependencies.Identity
	router.Use(optionalAuthentication(identity, dependencies.Config.Auth.SessionCookieName))

	router.Get("/", func(response http.ResponseWriter, request *http.Request) {
		if _, ok := sessionFromContext(request.Context()); ok {
			http.Redirect(response, request, "/dashboard", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/login", http.StatusSeeOther)
	})
	router.Get("/login", loginPage(page, dependencies.Logger))
	router.Post("/login", login(identity, dependencies, page))
	if dependencies.Confirmations != nil {
		registerConfirmationRoutes(router, dependencies, page)
	}
	if dependencies.CalendarFeeds != nil {
		registerPublicCalendarFeedRoute(router, dependencies)
	}

	router.Group(func(protected chi.Router) {
		protected.Use(requireAuthentication(page, dependencies.Logger))
		protected.Use(requirePasswordChange(page, dependencies.Logger))
		protected.Use(csrfProtection(identity, dependencies.Config.Auth.CSRFCookieName, page, dependencies.Logger))
		protected.Get("/dashboard", dashboardPage(dependencies.Dashboard, dependencies.Notifications, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		protected.Get("/profile", profile(page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		protected.Get("/password", passwordPage(page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		protected.Post("/password", changePassword(identity, dependencies, page))
		protected.Post("/logout", logout(identity, dependencies))
		if dependencies.MapTiles != nil {
			registerMapTileRoutes(protected, dependencies.MapTiles)
		}
		if dependencies.Geocoder != nil {
			registerGeocodingRoutes(protected, dependencies.Geocoder, dependencies.Config.Geocoding.RateLimit, dependencies.Logger)
		}
		if dependencies.Customers != nil {
			registerCustomerRoutes(protected, dependencies, page)
		}
		if dependencies.Drivers != nil && dependencies.Resources != nil {
			registerDriverRoutes(protected, dependencies, page)
		}
		if dependencies.Appointments != nil {
			registerAppointmentRoutes(protected, dependencies, page)
		}
		if dependencies.Notifications != nil {
			registerNotificationRoutes(protected, dependencies, page)
		}
		if dependencies.CalendarFeeds != nil {
			registerCalendarFeedRoutes(protected, dependencies, page)
		}
		if dependencies.Planning != nil {
			registerPlanningRoutes(protected, dependencies, page)
		}
		if dependencies.Routes != nil {
			registerRouteRoutes(protected, dependencies, page)
		}
		if dependencies.Voice != nil {
			registerVoiceRoutes(protected, dependencies, page)
		}

		protected.Route("/admin/users", func(adminRouter chi.Router) {
			adminRouter.Use(requirePermission(auth.PermissionUserManage, page, dependencies.Logger))
			adminRouter.Get("/", usersPage(identity, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
			adminRouter.Post("/", createUser(identity, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
			adminRouter.Post("/{userID}/details", updateUserDetails(identity, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
			adminRouter.Post("/{userID}/access", updateAccess(identity, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
			adminRouter.Post("/{userID}/reset-password", resetPassword(identity, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		})
	})
}

func optionalAuthentication(identity *auth.Service, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			cookie, err := request.Cookie(cookieName)
			if err != nil {
				next.ServeHTTP(response, request)
				return
			}
			session, err := identity.Authenticate(request.Context(), cookie.Value)
			if err != nil {
				next.ServeHTTP(response, request)
				return
			}
			ctx := context.WithValue(request.Context(), sessionContextKey{}, session)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}

func requireAuthentication(page templates.PageData, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Cache-Control", "no-store")
			if _, ok := sessionFromContext(request.Context()); !ok {
				if request.Method == http.MethodGet {
					http.Redirect(response, request, "/login", http.StatusSeeOther)
					return
				}
				render(response, request, templates.Error(page, http.StatusUnauthorized, "Anmeldung erforderlich", "Bitte melden Sie sich erneut an."), http.StatusUnauthorized, logger)
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}

func requirePasswordChange(page templates.PageData, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			session, ok := sessionFromContext(request.Context())
			if ok && session.Actor.MustChangePassword && request.URL.Path != "/password" && request.URL.Path != "/logout" {
				if request.Method == http.MethodGet {
					http.Redirect(response, request, "/password", http.StatusSeeOther)
					return
				}
				render(response, request, templates.Error(page, http.StatusForbidden, "Passwortänderung erforderlich", "Ändern Sie zuerst Ihr temporäres Passwort."), http.StatusForbidden, logger)
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}

func requirePermission(permission auth.Permission, page templates.PageData, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			session, _ := sessionFromContext(request.Context())
			if err := session.Actor.Require(permission); err != nil {
				render(response, request, templates.Error(page, http.StatusForbidden, "Zugriff verweigert", "Für diese Aktion fehlt die Berechtigung."), http.StatusForbidden, logger)
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}

func csrfProtection(identity *auth.Service, csrfCookieName string, page templates.PageData, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
				next.ServeHTTP(response, request)
				return
			}
			if !sameOrigin(request) {
				render(response, request, templates.Error(page, http.StatusForbidden, "Anfrage abgewiesen", "Die Sicherheitsprüfung der Anfrage ist fehlgeschlagen."), http.StatusForbidden, logger)
				return
			}
			presented := request.Header.Get("X-CSRF-Token")
			if strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "multipart/form-data") {
				if presented == "" {
					render(response, request, templates.Error(page, http.StatusForbidden, "Anfrage abgewiesen", "Audio-Uploads benötigen das Sicherheitsmerkmal im Request-Header."), http.StatusForbidden, logger)
					return
				}
			} else {
				request.Body = http.MaxBytesReader(response, request.Body, maxFormBytes)
				if err := request.ParseForm(); err != nil {
					http.Error(response, "Formular ist zu groß oder ungültig.", http.StatusBadRequest)
					return
				}
				if presented == "" {
					presented = request.Form.Get("csrf_token")
				}
			}
			csrfCookie, cookieErr := request.Cookie(csrfCookieName)
			session, _ := sessionFromContext(request.Context())
			cookieMatches := cookieErr == nil && subtle.ConstantTimeCompare(auth.TokenHash(csrfCookie.Value), auth.TokenHash(presented)) == 1
			if !cookieMatches || !identity.ValidateCSRF(session, presented) {
				render(response, request, templates.Error(page, http.StatusForbidden, "Anfrage abgewiesen", "Das Sicherheitsmerkmal ist ungültig oder abgelaufen."), http.StatusForbidden, logger)
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}

func loginPage(page templates.PageData, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		if _, ok := sessionFromContext(request.Context()); ok {
			http.Redirect(response, request, "/dashboard", http.StatusSeeOther)
			return
		}
		render(response, request, templates.Login(templates.LoginData{Page: page}), http.StatusOK, logger)
	}
}

func login(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !sameOrigin(request) {
			render(response, request, templates.Login(templates.LoginData{Page: page, Error: genericLoginError}), http.StatusForbidden, dependencies.Logger)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maxFormBytes)
		if err := request.ParseForm(); err != nil {
			render(response, request, templates.Login(templates.LoginData{Page: page, Error: genericLoginError}), http.StatusBadRequest, dependencies.Logger)
			return
		}
		username := strings.TrimSpace(request.Form.Get("username"))
		tokens, err := identity.Login(request.Context(), username, request.Form.Get("password"), request.RemoteAddr, middleware.GetReqID(request.Context()))
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, auth.ErrRateLimited) {
				status = http.StatusTooManyRequests
			}
			render(response, request, templates.Login(templates.LoginData{Page: page, Username: username, Error: genericLoginError}), status, dependencies.Logger)
			return
		}
		setAuthCookies(response, dependencies, tokens)
		if tokens.Actor.MustChangePassword {
			http.Redirect(response, request, "/password", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/dashboard", http.StatusSeeOther)
	}
}

const genericLoginError = "Benutzername oder Passwort ist ungültig. Bitte versuchen Sie es später erneut."

func dashboardPage(service *dashboarddomain.Service, notifications *notification.AdminService, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if service == nil {
			render(response, request, templates.Error(page, http.StatusServiceUnavailable, "Übersicht nicht verfügbar", "Der Tagesüberblick kann derzeit nicht geladen werden."), http.StatusServiceUnavailable, logger)
			return
		}
		session, _ := sessionFromContext(request.Context())
		value, err := service.View(request.Context(), session.Actor, request.URL.Query().Get("date"))
		if err != nil {
			status := http.StatusInternalServerError
			message := "Der Tagesüberblick kann derzeit nicht geladen werden."
			if errors.Is(err, dashboarddomain.ErrInvalidDate) {
				status = http.StatusUnprocessableEntity
				message = "Das gewählte Datum ist ungültig oder liegt außerhalb des erlaubten Bereichs."
			}
			render(response, request, templates.Dashboard(templates.DashboardData{Shell: shell(request, page, csrfCookieName), Error: message}), status, logger)
			return
		}
		data := templates.DashboardData{Shell: shell(request, page, csrfCookieName), View: value}
		if value.Admin && notifications != nil && value.Counts.NotificationIssues > 0 {
			data.NotificationIssues, err = notifications.Failed(request.Context(), session.Actor, notification.FailureAll, 5)
			if err != nil {
				logger.WarnContext(request.Context(), "dashboard notification issues unavailable", slog.String("error_code", "dashboard_notification_issues_unavailable"))
				data.NotificationIssues = nil
			}
		}
		render(response, request, templates.Dashboard(data), http.StatusOK, logger)
	}
}

func profile(page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.Profile(shell(request, page, csrfCookieName)), http.StatusOK, logger)
	}
}

func passwordPage(page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.Password(templates.PasswordData{Shell: shell(request, page, csrfCookieName)}), http.StatusOK, logger)
	}
}

func changePassword(identity *auth.Service, dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		password := request.Form.Get("password")
		if password != request.Form.Get("confirmation") {
			render(response, request, templates.Password(templates.PasswordData{Shell: shell(request, page, dependencies.Config.Auth.CSRFCookieName), Error: "Die Passwörter stimmen nicht überein."}), http.StatusUnprocessableEntity, dependencies.Logger)
			return
		}
		if err := identity.ChangeOwnPassword(request.Context(), session.Actor, password, session.Actor.UserVersion); err != nil {
			render(response, request, templates.Password(templates.PasswordData{Shell: shell(request, page, dependencies.Config.Auth.CSRFCookieName), Error: "Das Passwort erfüllt die Vorgaben nicht oder der Zugang wurde inzwischen geändert."}), http.StatusUnprocessableEntity, dependencies.Logger)
			return
		}
		clearAuthCookies(response, dependencies)
		http.Redirect(response, request, "/login", http.StatusSeeOther)
	}
}

func logout(identity *auth.Service, dependencies Dependencies) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if cookie, err := request.Cookie(dependencies.Config.Auth.SessionCookieName); err == nil {
			if logoutErr := identity.Logout(request.Context(), cookie.Value); logoutErr != nil {
				dependencies.Logger.WarnContext(request.Context(), "logout revocation failed", slog.String("error_code", "logout_revocation_failed"))
			}
		}
		clearAuthCookies(response, dependencies)
		http.Redirect(response, request, "/login", http.StatusSeeOther)
	}
}

func usersPage(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{Status: http.StatusOK})
	}
}

func createUser(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		values := templates.CreateUserValues{Username: strings.TrimSpace(request.Form.Get("username")), DisplayName: strings.TrimSpace(request.Form.Get("display_name")), Email: strings.TrimSpace(request.Form.Get("email")), Role: request.Form.Get("role"), CreateDriver: request.Form.Get("create_driver") == "true"}
		_, err := identity.CreateUser(request.Context(), session.Actor, auth.CreateUserInput{Username: values.Username, DisplayName: values.DisplayName, Email: values.Email, Role: auth.Role(values.Role), Password: request.Form.Get("password"), CreateDriver: values.CreateDriver, RequestID: middleware.GetReqID(request.Context())})
		if err != nil {
			renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{
				Error:        "Der Zugang konnte nicht angelegt werden. Prüfen Sie eindeutigen Benutzernamen und Passwortvorgaben.",
				CreateValues: values,
				Status:       http.StatusUnprocessableEntity,
			})
			return
		}
		http.Redirect(response, request, "/admin/users", http.StatusSeeOther)
	}
}

func updateUserDetails(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		input := auth.UpdateUserDetailsInput{
			UserID:      chi.URLParam(request, "userID"),
			Username:    strings.TrimSpace(request.Form.Get("username")),
			DisplayName: strings.TrimSpace(request.Form.Get("display_name")),
			Email:       strings.TrimSpace(request.Form.Get("email")),
			RequestID:   middleware.GetReqID(request.Context()),
		}
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			input.ExpectedVersion = version
			err = identity.UpdateUserDetails(request.Context(), session.Actor, input)
		}
		if err != nil {
			logger.WarnContext(request.Context(), "user details update rejected", slog.String("error_code", "user_details_rejected"))
			status := http.StatusUnprocessableEntity
			message := "Benutzername, Anzeigename oder E-Mail sind ungültig."
			if errors.Is(err, auth.ErrConflict) {
				status = http.StatusConflict
				message = "Der Benutzer wurde zwischenzeitlich geändert oder der Benutzername ist bereits vergeben. Bitte laden Sie die Seite neu."
			} else if !errors.Is(err, auth.ErrInvalidInput) {
				status = http.StatusInternalServerError
				message = "Die Benutzerdaten konnten derzeit nicht gespeichert werden."
			}
			renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{
				Error: message, EditedDetails: &input, EditedUserID: input.UserID, Status: status,
			})
			return
		}
		http.Redirect(response, request, "/admin/users", http.StatusSeeOther)
	}
}

func updateAccess(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = identity.UpdateUserAccess(request.Context(), session.Actor, auth.UpdateAccessInput{UserID: chi.URLParam(request, "userID"), Role: auth.Role(request.Form.Get("role")), Active: request.Form.Get("active") == "true", ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())})
		}
		if err != nil {
			logger.WarnContext(request.Context(), "user access update rejected", slog.String("error_code", "user_access_rejected"))
			message := "Die Zugriffsänderung konnte nicht gespeichert werden. Bitte laden Sie die Seite neu."
			if errors.Is(err, auth.ErrLastAdmin) {
				message = "Änderung abgewiesen. Mindestens ein aktiver Administrator muss erhalten bleiben."
			}
			renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{
				Error: message, EditedUserID: chi.URLParam(request, "userID"), Status: http.StatusConflict,
			})
			return
		}
		http.Redirect(response, request, "/admin/users", http.StatusSeeOther)
	}
}

func resetPassword(identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = identity.ResetPassword(request.Context(), session.Actor, auth.ResetPasswordInput{UserID: chi.URLParam(request, "userID"), Password: request.Form.Get("password"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())})
		}
		if err != nil {
			logger.WarnContext(request.Context(), "password reset rejected", slog.String("error_code", "password_reset_rejected"))
			renderUsers(response, request, identity, page, csrfCookieName, logger, usersRenderState{
				Error:        "Das Passwort konnte nicht zurückgesetzt werden. Verwenden Sie mindestens 14 Zeichen.",
				EditedUserID: chi.URLParam(request, "userID"), Status: http.StatusConflict,
			})
			return
		}
		http.Redirect(response, request, "/admin/users", http.StatusSeeOther)
	}
}

type usersRenderState struct {
	Error         string
	CreateValues  templates.CreateUserValues
	EditedDetails *auth.UpdateUserDetailsInput
	EditedUserID  string
	Status        int
}

func renderUsers(response http.ResponseWriter, request *http.Request, identity *auth.Service, page templates.PageData, csrfCookieName string, logger *slog.Logger, state usersRenderState) {
	session, _ := sessionFromContext(request.Context())
	users, err := identity.ListUsers(request.Context(), session.Actor)
	if err != nil {
		render(response, request, templates.Error(page, http.StatusInternalServerError, "Benutzer nicht verfügbar", "Die Liste kann derzeit nicht geladen werden."), http.StatusInternalServerError, logger)
		return
	}
	editedUserID := state.EditedUserID
	if state.EditedDetails != nil {
		for index := range users {
			if users[index].ID != state.EditedDetails.UserID {
				continue
			}
			users[index].Username = state.EditedDetails.Username
			users[index].DisplayName = state.EditedDetails.DisplayName
			users[index].Email = state.EditedDetails.Email
		}
	}
	render(response, request, templates.Users(templates.UsersData{
		Shell: shell(request, page, csrfCookieName), Users: users,
		Error: state.Error, Values: state.CreateValues, EditedUserID: editedUserID,
	}), state.Status, logger)
}

func sessionFromContext(ctx context.Context) (auth.Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(auth.Session)
	return session, ok
}

func shell(request *http.Request, page templates.PageData, csrfCookieName string) templates.ShellData {
	session, _ := sessionFromContext(request.Context())
	csrf := ""
	if cookie, err := request.Cookie(csrfCookieName); err == nil {
		csrf = cookie.Value
	}
	return templates.ShellData{Page: page, Actor: session.Actor, CSRFToken: csrf, ActivePath: request.URL.Path}
}

func setAuthCookies(response http.ResponseWriter, dependencies Dependencies, tokens auth.SessionTokens) {
	secure := dependencies.Config.Auth.CookieSecure
	maxAge := int(dependencies.Config.Auth.SessionAbsoluteTTL.Seconds())
	// #nosec G124 -- production validation requires Secure=true; local HTTP development is explicit.
	http.SetCookie(response, &http.Cookie{Name: dependencies.Config.Auth.SessionCookieName, Value: tokens.SessionToken, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	// #nosec G124 -- production validation requires Secure=true; CSRF values are rendered server-side.
	http.SetCookie(response, &http.Cookie{Name: dependencies.Config.Auth.CSRFCookieName, Value: tokens.CSRFToken, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
}

func clearAuthCookies(response http.ResponseWriter, dependencies Dependencies) {
	for _, name := range []string{dependencies.Config.Auth.SessionCookieName, dependencies.Config.Auth.CSRFCookieName} {
		// #nosec G124 -- production validation requires Secure=true; this expires both cookies.
		http.SetCookie(response, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: dependencies.Config.Auth.CookieSecure, SameSite: http.SameSiteStrictMode})
	}
}

func sameOrigin(request *http.Request) bool {
	if strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, request.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func parseVersion(value string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 1 {
		return 0, errors.New("invalid version")
	}
	return int32(parsed), nil
}

func render(response http.ResponseWriter, request *http.Request, component templ.Component, status int, logger *slog.Logger) {
	response.WriteHeader(status)
	if err := component.Render(request.Context(), response); err != nil {
		logger.ErrorContext(request.Context(), "rendering identity page", slog.String("error_type", "template_render"))
	}
}
