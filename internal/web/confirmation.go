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
		data := templates.ConfirmationData{Page: page, Token: token, Value: value, Invalid: err != nil,
			Expired: errors.Is(err, notification.ErrConfirmationExpired), Revoked: errors.Is(err, notification.ErrConfirmationRevoked)}
		data.Invalid = err != nil && !data.Expired && !data.Revoked
		if err == nil && value.Response != "" {
			data.AlreadyStored = true
			data.Result = confirmationResult(value.Response)
		}
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
		token := chi.URLParam(request, "confirmationToken")
		value, err := service.Respond(request.Context(), token, request.Form.Get("form_nonce"), notification.Response(request.Form.Get("action")), request.Form.Get("response_note"), middleware.GetReqID(request.Context()))
		if err != nil {
			switch {
			case errors.Is(err, notification.ErrConfirmationExpired):
				render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Expired: true}), http.StatusOK, logger)
			case errors.Is(err, notification.ErrConfirmationRevoked):
				render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Revoked: true}), http.StatusOK, logger)
			case errors.Is(err, notification.ErrConfirmationUnavailable):
				render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Invalid: true}), http.StatusOK, logger)
			case errors.Is(err, notification.ErrResponseLocked):
				stored, viewErr := service.View(request.Context(), token)
				if viewErr != nil {
					logger.WarnContext(request.Context(), "stored confirmation response unavailable", slog.String("error_code", "confirmation_locked_view_failed"))
					render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, AlreadyStored: true}), http.StatusOK, logger)
					return
				}
				render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, AlreadyStored: true, Result: confirmationResult(stored.Response)}), http.StatusOK, logger)
			default:
				logger.WarnContext(request.Context(), "confirmation response rejected", slog.String("error_code", "confirmation_response_failed"))
				response.Header().Set("Retry-After", "5")
				render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Token: token, Retryable: true}), http.StatusServiceUnavailable, logger)
			}
			return
		}
		result := confirmationResult(value.Response)
		if result == "" {
			result = "Ihre Rückmeldung wurde gespeichert."
		}
		render(response, request, templates.ConfirmationPage(templates.ConfirmationData{Page: page, Result: result}), http.StatusOK, logger)
	}
}

func confirmationResult(response notification.Response) string {
	switch response {
	case notification.ResponseConfirmed:
		return "Sie haben den Termin bestätigt."
	case notification.ResponseDeclined:
		return "Sie haben den Termin abgelehnt. Der Betrieb meldet sich bei Bedarf bei Ihnen."
	case notification.ResponseCallback:
		return "Ihr Rückrufwunsch wurde gespeichert."
	default:
		return ""
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
