package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerConfirmationRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service := dependencies.Confirmations
	limiter := newConfirmationRateLimiter(dependencies.Config.Confirmation.RateLimit, nil)
	router.Get("/termin/{confirmationToken}", confirmationPage(service, limiter, page, dependencies.Logger))
	router.Post("/termin/{confirmationToken}/antwort", confirmationResponse(service, limiter, dependencies.Config, page, dependencies.Logger))
}

func confirmationPage(service *notification.ConfirmationService, limiter *confirmationRateLimiter, page templates.PageData, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		confirmationHeaders(response)
		if !limiter.Allow(request.RemoteAddr) {
			render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Invalid: true}), http.StatusOK, logger)
			return
		}
		token := chi.URLParam(request, "confirmationToken")
		value, err := service.View(request.Context(), token)
		data := templates.ConfirmationData{Page: page, Token: token, Value: value, Invalid: err != nil}
		render(response, request, templates.ConfirmationPage(data), http.StatusOK, logger)
	}
}

func confirmationResponse(service *notification.ConfirmationService, limiter *confirmationRateLimiter, cfg config.Config, page templates.PageData, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		confirmationHeaders(response)
		if !limiter.Allow(request.RemoteAddr) {
			render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Invalid: true}), http.StatusOK, logger)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
		if !validConfirmationOrigin(request, cfg.BaseURL) || request.ParseForm() != nil {
			render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Invalid: true}), http.StatusOK, logger)
			return
		}
		value, err := service.Respond(request.Context(), chi.URLParam(request, "confirmationToken"), request.Form.Get("form_nonce"), notification.Response(request.Form.Get("action")), middleware.GetReqID(request.Context()))
		if err != nil {
			if !errors.Is(err, notification.ErrConfirmationUnavailable) && !errors.Is(err, notification.ErrResponseLocked) {
				logger.WarnContext(request.Context(), "confirmation response rejected", slog.String("error_code", "confirmation_response_failed"))
			}
			render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Invalid: true}), http.StatusOK, logger)
			return
		}
		result := "Ihre Rückmeldung wurde gespeichert."
		switch value.Response {
		case notification.ResponseConfirmed:
			result = "Sie haben den Termin bestätigt."
		case notification.ResponseDeclined:
			result = "Sie haben den Termin abgelehnt. Der Betrieb meldet sich bei Bedarf bei Ihnen."
		case notification.ResponseCallback:
			result = "Ihr Rückrufwunsch wurde gespeichert."
		}
		render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Result: result}), http.StatusOK, logger)
	}
}

func confirmationHeaders(response http.ResponseWriter) {
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Cache-Control", "no-store, private")
	response.Header().Set("Pragma", "no-cache")
	response.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func validConfirmationOrigin(request *http.Request, baseURL string) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" || origin == "null" {
		// A native form submission from the no-referrer capability page can have an
		// opaque origin. The link-bound form nonce remains mandatory; explicit
		// foreign origins are still rejected below.
		return true
	}
	parsedOrigin, originErr := url.Parse(origin)
	parsedBase, baseErr := url.Parse(baseURL)
	return originErr == nil && baseErr == nil && strings.EqualFold(parsedOrigin.Scheme, parsedBase.Scheme) && strings.EqualFold(parsedOrigin.Host, parsedBase.Host)
}
