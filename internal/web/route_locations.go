package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/routelocation"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerRouteLocationRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	router.Route("/settings/route-locations", func(settings chi.Router) {
		settings.Use(requirePermission(auth.PermissionSettingsManage, page, dependencies.Logger))
		settings.Get("/", routeLocationsPage(dependencies.RouteLocations, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		settings.Post("/", createRouteLocation(dependencies.RouteLocations, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		settings.Post("/{routeLocationID}", updateRouteLocation(dependencies.RouteLocations, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
		settings.Post("/{routeLocationID}/deactivate", deactivateRouteLocation(dependencies.RouteLocations, page, dependencies.Config.Auth.CSRFCookieName, dependencies.Logger))
	})
}

func routeLocationsPage(service *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		data, err := routeLocationSettingsData(request, service, page, csrfCookie)
		if err != nil {
			render(response, request, templates.Error(page, http.StatusInternalServerError, "Routenorte nicht verfügbar", "Die gespeicherten Start- und Endorte können derzeit nicht geladen werden."), http.StatusInternalServerError, logger)
			return
		}
		data.Error = strings.TrimSpace(request.URL.Query().Get("error"))
		render(response, request, templates.RouteLocationsSettings(data), http.StatusOK, logger)
	}
}

func createRouteLocation(service *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		input, err := routeLocationInput(request)
		if err == nil {
			_, err = service.Create(request.Context(), session.Actor, input, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			renderRouteLocationMutationError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		http.Redirect(response, request, "/settings/route-locations", http.StatusSeeOther)
	}
}

func updateRouteLocation(service *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err != nil {
			err = routelocation.ErrValidation
		}
		var input routelocation.Input
		if err == nil {
			input, err = routeLocationInput(request)
		}
		if err == nil {
			_, err = service.Update(request.Context(), session.Actor, chi.URLParam(request, "routeLocationID"), version, input, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			renderRouteLocationMutationError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		http.Redirect(response, request, "/settings/route-locations", http.StatusSeeOther)
	}
}

func deactivateRouteLocation(service *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err != nil {
			err = routelocation.ErrValidation
		}
		id := chi.URLParam(request, "routeLocationID")
		if err == nil {
			err = service.Deactivate(request.Context(), session.Actor, id, version, request.Form.Get("confirm_without_default") == "true", middleware.GetReqID(request.Context()))
		}
		if err != nil {
			renderRouteLocationMutationError(response, request, service, page, csrfCookie, logger, err)
			return
		}
		http.Redirect(response, request, "/settings/route-locations", http.StatusSeeOther)
	}
}

func routeLocationInput(request *http.Request) (routelocation.Input, error) {
	if request.Form.Get("confirmed") != "true" && request.Form.Get("confirmed_native") != "true" {
		return routelocation.Input{}, routelocation.ErrValidation
	}
	latitude, latitudeErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(request.Form.Get("latitude")), ",", "."), 64)
	longitude, longitudeErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(request.Form.Get("longitude")), ",", "."), 64)
	if latitudeErr != nil || longitudeErr != nil {
		return routelocation.Input{}, routelocation.ErrValidation
	}
	return routelocation.Input{
		Label: request.Form.Get("name"), Address: request.Form.Get("address"),
		Latitude: latitude, Longitude: longitude,
		DefaultStart: request.Form.Get("default_start") == "true",
		DefaultEnd:   request.Form.Get("default_end") == "true",
	}, nil
}

func routeLocationSettingsData(request *http.Request, service *routelocation.Service, page templates.PageData, csrfCookie string) (templates.RouteLocationsSettingsData, error) {
	session, _ := sessionFromContext(request.Context())
	locations, err := service.List(request.Context(), session.Actor)
	if err != nil {
		return templates.RouteLocationsSettingsData{}, err
	}
	data := templates.RouteLocationsSettingsData{Shell: shell(request, page, csrfCookie)}
	for _, location := range locations {
		option := routeLocationOption(location)
		data.Locations = append(data.Locations, option)
		if location.DefaultStart {
			data.DefaultStartID = location.ID
		}
		if location.DefaultEnd {
			data.DefaultEndID = location.ID
		}
	}
	return data, nil
}

func renderRouteLocationMutationError(response http.ResponseWriter, request *http.Request, service *routelocation.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, err error) {
	status, message := routeLocationError(err)
	data, listErr := routeLocationSettingsData(request, service, page, csrfCookie)
	if listErr != nil {
		render(response, request, templates.Error(page, http.StatusInternalServerError, "Routenorte nicht verfügbar", "Die Änderung konnte nicht gespeichert und die Liste nicht neu geladen werden."), http.StatusInternalServerError, logger)
		return
	}
	data.Error = message
	logger.WarnContext(request.Context(), "route location mutation rejected", slog.String("error_code", "route_location_mutation_rejected"))
	render(response, request, templates.RouteLocationsSettings(data), status, logger)
}

func routeLocationError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "Für diese Einstellung fehlt die Berechtigung."
	case errors.Is(err, routelocation.ErrConflict):
		return http.StatusConflict, "Der Routenort wurde zwischenzeitlich geändert. Bitte Seite neu laden."
	case errors.Is(err, routelocation.ErrNotFound):
		return http.StatusNotFound, "Der Routenort wurde nicht gefunden oder ist nicht mehr aktiv."
	case errors.Is(err, routelocation.ErrValidation):
		return http.StatusUnprocessableEntity, "Bitte Bezeichnung, bestätigte Adresse, Koordinaten und Standardauswahl prüfen."
	default:
		return http.StatusInternalServerError, "Der Routenort kann derzeit nicht gespeichert werden."
	}
}

func routeLocationOption(location routelocation.Location) templates.RouteLocationOption {
	return templates.RouteLocationOption{
		ID: location.ID, Name: location.Label, Address: location.Address,
		Latitude:  strconv.FormatFloat(location.Latitude, 'f', 6, 64),
		Longitude: strconv.FormatFloat(location.Longitude, 'f', 6, 64),
		Version:   location.Version, Active: location.Active,
		DefaultStart: location.DefaultStart, DefaultEnd: location.DefaultEnd,
	}
}
