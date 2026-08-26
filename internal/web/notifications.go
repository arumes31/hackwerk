package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerNotificationRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service := dependencies.Notifications
	router.Route("/admin/notifications", func(notificationRouter chi.Router) {
		notificationRouter.Use(requirePermission(auth.PermissionNotificationResend, page, dependencies.Logger))
		notificationRouter.Get("/", notificationFailures(service, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		notificationRouter.Get("/report.csv", notificationReport(service, dependencies.Logger))
		notificationRouter.Post("/{notificationID}/retry", retryNotification(service, page, dependencies.Logger))
		notificationRouter.Post("/{notificationID}/review", reviewNotification(service, page, dependencies.Logger))
	})
}

func notificationFailures(service *notification.AdminService, page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		filter := notification.ParseFailureFilter(request.URL.Query().Get("status"))
		values, err := service.Failed(request.Context(), session.Actor, filter, 100)
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Versandstatus nicht verfügbar", "Die fehlgeschlagenen Nachrichten können derzeit nicht geladen werden."), http.StatusInternalServerError, logger)
			return
		}
		callbacks, err := service.Callbacks(request.Context(), session.Actor, 100)
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Rückrufliste nicht verfügbar", "Die offenen Rückrufwünsche können derzeit nicht geladen werden."), http.StatusInternalServerError, logger)
			return
		}
		location, err := time.LoadLocation("Europe/Vienna")
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Vorschau nicht verfügbar", "Die Nachrichtenvorschau kann derzeit nicht erstellt werden."), http.StatusInternalServerError, logger)
			return
		}
		preview, err := notification.SyntheticPreview(location)
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Vorschau nicht verfügbar", "Die Nachrichtenvorschau kann derzeit nicht erstellt werden."), http.StatusInternalServerError, logger)
			return
		}
		render(response, request, templates.NotificationFailures(templates.NotificationFailuresData{
			Shell: shell(request, page, csrfCookieName), Notifications: values, Filter: filter, Callbacks: callbacks, Preview: preview,
		}), http.StatusOK, logger)
	}
}

func notificationReport(service *notification.AdminService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		filter := notification.ParseFailureFilter(request.URL.Query().Get("status"))
		contents, err := service.CSV(request.Context(), session.Actor, filter)
		if err != nil {
			logger.WarnContext(request.Context(), "notification report rejected", slog.String("error_code", "notification_report_failed"))
			http.Error(response, "Versandbericht nicht verfügbar", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/csv; charset=utf-8")
		response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="hackwerk-versand-%s.csv"`, time.Now().UTC().Format("20060102")))
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		// #nosec G705 -- this is a forced-download CSV; the service quotes fields and neutralizes formulas.
		if _, err := response.Write(contents); err != nil {
			logger.WarnContext(request.Context(), "notification report response failed", slog.String("error_code", "notification_report_write_failed"))
		}
	}
}

func retryNotification(service *notification.AdminService, page templates.PageData, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		err := service.Retry(request.Context(), session.Actor, chi.URLParam(request, "notificationID"), middleware.GetReqID(request.Context()))
		if err == nil {
			http.Redirect(response, request, "/admin/notifications", http.StatusSeeOther)
			return
		}
		if errors.Is(err, notification.ErrRetryUnavailable) {
			render(response, request, templates.Error(page, http.StatusConflict, "Erneuter Versand nicht möglich", "Die Nachricht ist nicht mehr fehlgeschlagen oder der Bestätigungslink ist nicht mehr aktiv."), http.StatusConflict, logger)
			return
		}
		logger.WarnContext(request.Context(), "notification retry rejected", slog.String("error_code", "notification_retry_failed"))
		render(response, request, templates.Error(page, http.StatusInternalServerError, "Erneuter Versand fehlgeschlagen", "Die Nachricht konnte nicht erneut eingereiht werden."), http.StatusInternalServerError, logger)
	}
}

func reviewNotification(service *notification.AdminService, page templates.PageData, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		err := service.Review(request.Context(), session.Actor, chi.URLParam(request, "notificationID"), middleware.GetReqID(request.Context()))
		if err == nil {
			http.Redirect(response, request, "/admin/notifications", http.StatusSeeOther)
			return
		}
		if errors.Is(err, notification.ErrAdminActionUnavailable) {
			render(response, request, templates.Error(page, http.StatusConflict, "Prüfung nicht möglich", "Die Nachricht ist nicht mehr offen oder wurde zwischenzeitlich erneut eingereiht."), http.StatusConflict, logger)
			return
		}
		logger.WarnContext(request.Context(), "notification review rejected", slog.String("error_code", "notification_review_failed"))
		render(response, request, templates.Error(page, http.StatusInternalServerError, "Prüfung fehlgeschlagen", "Der Versandhinweis konnte nicht als geprüft markiert werden."), http.StatusInternalServerError, logger)
	}
}
