package web

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerPublicCalendarFeedRoute(router chi.Router, dependencies Dependencies) {
	limiter := newConfirmationRateLimiter(dependencies.Config.CalendarFeed.RateLimit, time.Now)
	router.Get("/feeds/{calendarFeedToken}/calendar.ics", publicCalendarFeed(dependencies.CalendarFeeds, limiter, dependencies.Logger))
}

func registerCalendarFeedRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service, csrfCookie, logger := dependencies.CalendarFeeds, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger
	router.Get("/calendar/export.ics", calendarExport(service, dependencies.Config.Timezone, logger))
	router.Get("/calendar/feeds", calendarFeedPage(service, page, csrfCookie, logger, nil))
	router.Post("/calendar/feeds", createCalendarFeed(service, page, csrfCookie, logger))
	router.Post("/calendar/feeds/{calendarFeedID}/rotate", rotateCalendarFeed(service, page, csrfCookie, logger))
	router.Post("/calendar/feeds/{calendarFeedID}/revoke", revokeCalendarFeed(service, page, csrfCookie, logger))
	router.Get("/api/v1/calendar-feeds", listCalendarFeedsAPI(service, logger))
	router.Post("/api/v1/calendar-feeds", createCalendarFeedAPI(service, logger))
	router.Post("/api/v1/calendar-feeds/{calendarFeedID}/rotate", rotateCalendarFeedAPI(service, logger))
	router.Delete("/api/v1/calendar-feeds/{calendarFeedID}", revokeCalendarFeedAPI(service, logger))
}

func calendarFeedPage(service *calendarfeed.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, material *calendarfeed.Material) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		feeds, err := service.List(request.Context(), session.Actor)
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Kalenderfeeds nicht verfügbar", "Die Feedliste kann derzeit nicht geladen werden."), http.StatusInternalServerError, logger)
			return
		}
		render(response, request, templates.CalendarFeeds(templates.CalendarFeedsData{Shell: shell(request, page, csrfCookie), Feeds: feeds, Material: material}), http.StatusOK, logger)
	}
}

func createCalendarFeed(service *calendarfeed.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		material, err := service.Create(request.Context(), session.Actor, feedInput(request))
		if err != nil {
			feedFormError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		calendarFeedPage(service, page, csrfCookie, logger, &material).ServeHTTP(response, request)
	}
}

func rotateCalendarFeed(service *calendarfeed.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var material calendarfeed.Material
		if err != nil {
			err = calendarfeed.ErrInvalid
		}
		if err == nil {
			material, err = service.Rotate(request.Context(), session.Actor, chi.URLParam(request, "calendarFeedID"), version)
		}
		if err != nil {
			feedFormError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		calendarFeedPage(service, page, csrfCookie, logger, &material).ServeHTTP(response, request)
	}
}

func revokeCalendarFeed(service *calendarfeed.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err != nil {
			err = calendarfeed.ErrInvalid
		}
		if err == nil {
			err = service.Revoke(request.Context(), session.Actor, chi.URLParam(request, "calendarFeedID"), version)
		}
		if err != nil {
			feedFormError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		http.Redirect(response, request, "/calendar/feeds", http.StatusSeeOther)
	}
}

func calendarExport(service *calendarfeed.Service, timezone string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		location, err := time.LoadLocation(timezone)
		if err != nil {
			http.Error(response, "Export nicht verfügbar.", http.StatusInternalServerError)
			return
		}
		from, to, err := exportDates(request, location)
		if err != nil {
			http.Error(response, "Datumsbereich ungültig.", http.StatusUnprocessableEntity)
			return
		}
		session, _ := sessionFromContext(request.Context())
		calendar, err := service.Export(request.Context(), session.Actor, from, to, request.URL.Query().Get("detail"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, calendarfeed.ErrInvalid) {
				status = http.StatusUnprocessableEntity
			}
			logger.WarnContext(request.Context(), "calendar export rejected", slog.String("error_code", "calendar_export_rejected"))
			http.Error(response, "Kalenderexport nicht möglich.", status)
			return
		}
		writeCalendar(response, request, calendar, true)
	}
}

func publicCalendarFeed(service *calendarfeed.Service, limiter *confirmationRateLimiter, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Cache-Control", "private, no-cache, max-age=0")
		if !limiter.Allow(request.RemoteAddr) {
			response.Header().Set("Retry-After", "60")
			http.Error(response, "Kalenderfeed nicht verfügbar.", http.StatusTooManyRequests)
			return
		}
		calendar, err := service.Public(request.Context(), chi.URLParam(request, "calendarFeedToken"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, calendarfeed.ErrNotFound) {
				status = http.StatusNotFound
			}
			logger.WarnContext(request.Context(), "calendar feed rejected", slog.String("error_code", "calendar_feed_rejected"))
			http.Error(response, "Kalenderfeed nicht verfügbar.", status)
			return
		}
		writeCalendar(response, request, calendar, false)
	}
}

func writeCalendar(response http.ResponseWriter, request *http.Request, calendar calendarfeed.Calendar, attachment bool) {
	response.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	response.Header().Set("ETag", calendar.ETag)
	if !calendar.LastModified.IsZero() {
		response.Header().Set("Last-Modified", calendar.LastModified.UTC().Format(http.TimeFormat))
	}
	if attachment {
		response.Header().Set("Content-Disposition", `attachment; filename="hackwerk-termine.ics"`)
	}
	if request.Header.Get("If-None-Match") == calendar.ETag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(calendar.Bytes)
}

func feedInput(request *http.Request) calendarfeed.CreateInput {
	return calendarfeed.CreateInput{Name: request.Form.Get("name"), Scope: request.Form.Get("scope"), Detail: request.Form.Get("detail"), ResourceTypes: request.Form["resource_type"]}
}

func exportDates(request *http.Request, location *time.Location) (time.Time, time.Time, error) {
	from, err := time.ParseInLocation(time.DateOnly, request.URL.Query().Get("from"), location)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := time.ParseInLocation(time.DateOnly, request.URL.Query().Get("to"), location)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from.UTC(), to.AddDate(0, 0, 1).UTC(), nil
}

func feedFormError(response http.ResponseWriter, request *http.Request, service *calendarfeed.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, err error) {
	status, message := http.StatusUnprocessableEntity, "Die Feedangaben sind ungültig oder veraltet."
	if !errors.Is(err, calendarfeed.ErrInvalid) && !errors.Is(err, calendarfeed.ErrConflict) {
		status, message = http.StatusInternalServerError, "Der Kalenderfeed kann derzeit nicht gespeichert werden."
	}
	session, _ := sessionFromContext(request.Context())
	feeds, _ := service.List(request.Context(), session.Actor)
	logger.WarnContext(request.Context(), "calendar feed mutation rejected", slog.String("error_code", "calendar_feed_mutation_rejected"))
	render(response, request, templates.CalendarFeeds(templates.CalendarFeedsData{Shell: shell(request, page, csrfCookie), Feeds: feeds, Error: message}), status, logger)
}

func listCalendarFeedsAPI(service *calendarfeed.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		feeds, err := service.List(request.Context(), session.Actor)
		if err != nil {
			calendarFeedAPIError(response, request, logger, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"feeds": feeds})
	}
}

func createCalendarFeedAPI(service *calendarfeed.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		material, err := service.Create(request.Context(), session.Actor, feedInput(request))
		if err != nil {
			calendarFeedAPIError(response, request, logger, err)
			return
		}
		writeJSON(response, http.StatusCreated, map[string]any{"id": material.Feed.ID, "url": material.URL, "token_version": material.Feed.TokenVersion, "version": material.Feed.Version})
	}
}

func rotateCalendarFeedAPI(service *calendarfeed.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var material calendarfeed.Material
		if err != nil {
			err = calendarfeed.ErrInvalid
		}
		if err == nil {
			material, err = service.Rotate(request.Context(), session.Actor, chi.URLParam(request, "calendarFeedID"), version)
		}
		if err != nil {
			calendarFeedAPIError(response, request, logger, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"id": material.Feed.ID, "url": material.URL, "token_version": material.Feed.TokenVersion, "version": material.Feed.Version})
	}
}

func revokeCalendarFeedAPI(service *calendarfeed.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err != nil {
			err = calendarfeed.ErrInvalid
		}
		if err == nil {
			err = service.Revoke(request.Context(), session.Actor, chi.URLParam(request, "calendarFeedID"), version)
		}
		if err != nil {
			calendarFeedAPIError(response, request, logger, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func calendarFeedAPIError(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "Der Kalenderfeed kann derzeit nicht verarbeitet werden."
	if errors.Is(err, calendarfeed.ErrInvalid) {
		status, code, message = http.StatusUnprocessableEntity, "validation_failed", "Die Feedangaben sind ungültig."
	}
	if errors.Is(err, calendarfeed.ErrConflict) {
		status, code, message = http.StatusConflict, "version_conflict", "Der Feed wurde zwischenzeitlich geändert."
	}
	logger.WarnContext(request.Context(), "calendar feed API rejected", slog.String("error_code", code))
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": middleware.GetReqID(request.Context())}})
}
