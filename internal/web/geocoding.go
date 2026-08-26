package web

import (
	"errors"
	"log/slog"
	"net/http"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/geocode"
	"github.com/go-chi/chi/v5"
)

func registerGeocodingRoutes(router chi.Router, searcher geocode.Searcher, rateLimit int, logger *slog.Logger) {
	limiter := newConfirmationRateLimiter(rateLimit, nil)
	router.Post("/api/v1/geocoding/search", geocodingSearch(searcher, limiter, logger))
}

func geocodingSearch(searcher geocode.Searcher, limiter *confirmationRateLimiter, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := session.Actor.Require(auth.PermissionJobCreate); err != nil {
			writeJSON(response, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "geocoding_forbidden", "message": "Die Adresssuche ist für diesen Zugang nicht erlaubt."}})
			return
		}
		if !limiter.Allow(request.RemoteAddr) {
			response.Header().Set("Retry-After", "60")
			writeJSON(response, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"code": "geocoding_rate_limited", "message": "Zu viele Adresssuchen. Bitte kurz warten."}})
			return
		}
		results, err := searcher.Search(request.Context(), request.Form.Get("query"))
		if err != nil {
			status, code, message := http.StatusBadGateway, "geocoding_unavailable", "Die Adresssuche ist derzeit nicht verfügbar. Die Karte kann weiterhin manuell verwendet werden."
			if errors.Is(err, geocode.ErrInvalidQuery) {
				status, code, message = http.StatusUnprocessableEntity, "geocoding_invalid_query", "Bitte mindestens drei und höchstens 200 Zeichen eingeben."
			} else {
				logger.WarnContext(request.Context(), "geocoding search failed", slog.String("error_code", code))
			}
			writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, map[string]any{"results": results})
	}
}
