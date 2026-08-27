package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/routelocation"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerPlanningRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	router.With(requirePermission(auth.PermissionPlanningView, page, dependencies.Logger)).Get(
		"/planning",
		planningPage(dependencies.Planning, dependencies.Routes, dependencies.RouteLocations, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger),
	)
	router.Post("/planning/suggestions", createPlanningSuggestions(dependencies.Planning, dependencies.Logger, false))
	router.Post("/planning/suggestions/{suggestionID}/adopt", adoptPlanningSuggestion(dependencies.Planning, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger, false))
	router.Post("/api/v1/planning/suggestions", createPlanningSuggestions(dependencies.Planning, dependencies.Logger, true))
	router.Post("/api/v1/planning/suggestions/{suggestionID}/adopt", adoptPlanningSuggestion(dependencies.Planning, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger, true))
}

func planningPage(service *planning.Service, routes *planning.RouteService, routeLocations *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		data, err := planningViewData(
			request,
			service,
			page,
			csrfCookie,
			strings.TrimSpace(request.URL.Query().Get("job_id")),
			strings.TrimSpace(request.URL.Query().Get("run_id")),
		)
		if err == nil && routes != nil {
			session, _ := sessionFromContext(request.Context())
			data.Candidates, err = routes.Candidates(request.Context(), session.Actor)
			if err == nil {
				data.MissingLocations, err = routes.MissingLocations(request.Context(), session.Actor)
			}
		}
		if err == nil && routeLocations != nil {
			session, _ := sessionFromContext(request.Context())
			locations, locationErr := routeLocations.ListActive(request.Context(), session.Actor)
			if locationErr != nil {
				err = locationErr
			} else {
				for _, location := range locations {
					if location.DefaultStart {
						data.RadiusLatitude = strconv.FormatFloat(location.Latitude, 'f', 6, 64)
						data.RadiusLongitude = strconv.FormatFloat(location.Longitude, 'f', 6, 64)
						break
					}
				}
			}
		}
		data.ReturnTo = safePlanningReturn(request.URL.Query().Get("return_to"))
		data.Error = strings.TrimSpace(request.URL.Query().Get("error"))
		if err != nil {
			status, message := planningError(err)
			render(response, request, templates.Planning(data.WithError(message)), status, logger)
			return
		}
		render(response, request, templates.Planning(data), http.StatusOK, logger)
	}
}

func safePlanningReturn(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || parsed.IsAbs() || parsed.Host != "" {
		return "/waitlist"
	}
	return parsed.RequestURI()
}

func planningViewData(
	request *http.Request,
	service *planning.Service,
	page templates.PageData,
	csrfCookie, jobID, runID string,
) (templates.PlanningData, error) {
	session, _ := sessionFromContext(request.Context())
	data := templates.PlanningData{Shell: shell(request, page, csrfCookie), JobID: jobID}
	if hints, err := service.ClusterHints(request.Context(), session.Actor); err == nil {
		data.Clusters = hints
	}
	if runID == "" {
		return data, nil
	}
	run, err := service.ListRun(request.Context(), session.Actor, runID)
	if err != nil {
		return data, err
	}
	data.Run = &run
	data.JobID = run.JobID
	return data, nil
}

func createPlanningSuggestions(service *planning.Service, logger *slog.Logger, jsonResponse bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		returnTo := safePlanningReturn(request.Form.Get("return_to"))
		run, err := service.Suggest(request.Context(), session.Actor, request.Form.Get("job_id"))
		if err != nil {
			status, message := planningError(err)
			if jsonResponse {
				writeJSON(response, status, map[string]any{"error": map[string]string{"code": planningErrorCode(err), "message": message, "request_id": middleware.GetReqID(request.Context())}})
				return
			}
			logger.InfoContext(request.Context(), "planning suggestions rejected", slog.String("error_code", planningErrorCode(err)))
			http.Redirect(response, request, "/planning?job_id="+url.QueryEscape(request.Form.Get("job_id"))+"&return_to="+url.QueryEscape(returnTo)+"&error="+url.QueryEscape(message), http.StatusSeeOther)
			return
		}
		if jsonResponse {
			writeJSON(response, http.StatusCreated, run)
			return
		}
		http.Redirect(response, request, "/planning?run_id="+url.QueryEscape(run.ID)+"&return_to="+url.QueryEscape(returnTo), http.StatusSeeOther)
	}
}

func adoptPlanningSuggestion(service *planning.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, jsonResponse bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		appointmentID, err := service.Adopt(request.Context(), session.Actor, chi.URLParam(request, "suggestionID"), middleware.GetReqID(request.Context()))
		if err != nil {
			status, message := planningError(err)
			if jsonResponse {
				writeJSON(response, status, map[string]any{"error": map[string]string{"code": planningErrorCode(err), "message": message, "request_id": middleware.GetReqID(request.Context())}})
				return
			}
			logger.InfoContext(request.Context(), "planning adoption rejected", slog.String("error_code", planningErrorCode(err)))
			data, viewErr := planningViewData(
				request,
				service,
				page,
				csrfCookie,
				strings.TrimSpace(request.Form.Get("job_id")),
				strings.TrimSpace(request.Form.Get("run_id")),
			)
			data.ReturnTo = safePlanningReturn(request.Form.Get("return_to"))
			if viewErr != nil {
				logger.WarnContext(request.Context(), "planning adoption context unavailable", slog.String("error_code", planningErrorCode(viewErr)))
			}
			if errors.Is(err, planning.ErrConflict) && data.Run != nil {
				for index := range data.Run.Suggestions {
					if data.Run.Suggestions[index].ID == chi.URLParam(request, "suggestionID") {
						data.Run.Suggestions[index].Status = "stale"
						break
					}
				}
			}
			data.Error = message
			render(response, request, templates.Planning(data), status, logger)
			return
		}
		if jsonResponse {
			writeJSON(response, http.StatusCreated, map[string]string{"appointment_id": appointmentID, "lifecycle": "proposal"})
			return
		}
		http.Redirect(response, request, "/calendar?appointment="+url.QueryEscape(appointmentID), http.StatusSeeOther)
	}
}

func planningError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "Für diese Aktion fehlt die Berechtigung."
	case errors.Is(err, planning.ErrConfiguration):
		return http.StatusServiceUnavailable, "Bitte zuerst unter Verwaltung → Routenorte einen Standard-Startort festlegen."
	case errors.Is(err, planning.ErrConflict):
		return http.StatusConflict, "Der Datenstand hat sich geändert. Bitte Vorschläge neu berechnen."
	case errors.Is(err, planning.ErrNoCapacity):
		return http.StatusUnprocessableEntity, "Im Suchzeitraum wurde kein konfliktfreier Slot mit verfügbaren Fahrern und Ressourcen gefunden."
	case errors.Is(err, planning.ErrValidation):
		return http.StatusUnprocessableEntity, "Die Planungsanfrage ist ungültig."
	case errors.Is(err, planning.ErrNotFound):
		return http.StatusNotFound, "Vorschlag oder Auftrag wurde nicht gefunden."
	default:
		return http.StatusInternalServerError, "Die Planung konnte derzeit nicht berechnet werden."
	}
}
func planningErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return "forbidden"
	case errors.Is(err, planning.ErrConfiguration):
		return "planning_configuration"
	case errors.Is(err, planning.ErrConflict):
		return "planning_stale"
	case errors.Is(err, planning.ErrNoCapacity):
		return "planning_no_capacity"
	case errors.Is(err, planning.ErrValidation):
		return "planning_invalid"
	case errors.Is(err, planning.ErrNotFound):
		return "planning_not_found"
	default:
		return "planning_failed"
	}
}
