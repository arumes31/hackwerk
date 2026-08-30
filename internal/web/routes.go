package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/routelocation"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerRouteRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service := dependencies.Routes
	csrfCookie := dependencies.Config.Auth.CSRFCookieName
	router.With(requirePermission(auth.PermissionRoutePlan, page, dependencies.Logger)).Get("/planning/routes", routePlannerPage(service, dependencies, page, csrfCookie))
	router.With(requirePermission(auth.PermissionRoutePlan, page, dependencies.Logger)).Post("/planning/routes", planRoute(service, dependencies, page, csrfCookie))
	router.With(requirePermission(auth.PermissionRouteAssign, page, dependencies.Logger)).Post("/planning/routes/{routeID}/assign", assignRoute(service, dependencies.Logger))
	router.With(requirePermission(auth.PermissionRoutePlan, page, dependencies.Logger)).Post("/planning/routes/{routeID}/move-stop", moveDraftStop(service, dependencies.Logger))
	router.With(requirePermission(auth.PermissionRouteViewOwn, page, dependencies.Logger)).Get("/my-route", ownRoutePage(service, dependencies, page, csrfCookie))
	router.With(requirePermission(auth.PermissionRouteReorderOwn, page, dependencies.Logger)).Post("/my-route/{routeID}/reorder", reorderOwnRoute(service, dependencies.Logger))
}

func routePlannerPage(service *planning.RouteService, dependencies Dependencies, page templates.PageData, csrfCookie string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		data, err := adminRouteViewData(request, service, dependencies, page, csrfCookie)
		if err != nil {
			status, message := routeError(err)
			data.Error = message
			render(response, request, templates.Routes(data), status, dependencies.Logger)
			return
		}
		data.Error = strings.TrimSpace(request.URL.Query().Get("error"))
		render(response, request, templates.Routes(data), http.StatusOK, dependencies.Logger)
	}
}

func adminRouteViewData(request *http.Request, service *planning.RouteService, dependencies Dependencies, page templates.PageData, csrfCookie string) (templates.RoutePageData, error) {
	session, _ := sessionFromContext(request.Context())
	data := templates.RoutePageData{
		Shell: shell(request, page, csrfCookie), Departure: defaultRouteDeparture(dependencies.Config.Planning.BusinessOpen),
	}
	data.SelectedJobIDs = append([]string(nil), request.URL.Query()["job_id"]...)
	data.SelectedDay = strings.TrimSpace(request.URL.Query().Get("date"))
	if data.SelectedDay == "" {
		data.SelectedDay = strings.Split(data.Departure, "T")[0]
	}
	var err error
	data.Candidates, err = service.Candidates(request.Context(), session.Actor)
	if err != nil {
		return data, err
	}
	data.MissingLocations, err = service.MissingLocations(request.Context(), session.Actor)
	if err != nil {
		return data, err
	}
	data.Options, err = service.Options(request.Context(), session.Actor)
	if err != nil {
		return data, err
	}
	if dependencies.RouteLocations != nil {
		locations, locationErr := dependencies.RouteLocations.ListActive(request.Context(), session.Actor)
		if locationErr != nil {
			return data, locationErr
		}
		for _, location := range locations {
			data.RouteLocations = append(data.RouteLocations, routeLocationOption(location))
			if location.DefaultStart {
				data.DefaultStartID = location.ID
			}
			if location.DefaultEnd {
				data.DefaultEndID = location.ID
			}
		}
	}
	data.ParallelRoutes, err = service.DraftsForDate(request.Context(), session.Actor, data.SelectedDay)
	if err != nil {
		return data, err
	}
	if routeID := strings.TrimSpace(request.URL.Query().Get("route_id")); routeID != "" {
		route, routeErr := service.Route(request.Context(), session.Actor, routeID)
		if routeErr != nil {
			return data, routeErr
		}
		data.Route = &route
		applyRouteComparisonQuery(&route, request.URL.Query())
	}
	return data, nil
}

func moveDraftStop(service *planning.RouteService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		targetRouteID, targetVersion, targetErr := draftMoveTarget(request.Form)
		sourceVersion, sourceErr := parseVersion(request.Form.Get("source_version"))
		var err error
		if sourceErr != nil || targetErr != nil {
			err = planning.ErrValidation
		} else {
			_, err = service.MoveDraftStop(request.Context(), session.Actor, planning.MoveDraftStopInput{
				SourceRouteID: chi.URLParam(request, "routeID"), TargetRouteID: targetRouteID,
				StopID: request.Form.Get("stop_id"), SourceVersion: sourceVersion, TargetVersion: targetVersion,
				RequestID: middleware.GetReqID(request.Context()),
			})
		}
		day := strings.TrimSpace(request.Form.Get("date"))
		values := url.Values{"date": []string{day}}
		if err != nil {
			_, message := routeError(err)
			values.Set("error", message)
			logger.InfoContext(request.Context(), "draft route stop move rejected", slog.String("error_code", planningErrorCode(err)))
		}
		http.Redirect(response, request, "/planning/routes?"+values.Encode(), http.StatusSeeOther)
	}
}

func draftMoveTarget(form url.Values) (string, int32, error) {
	targetRouteID := strings.TrimSpace(form.Get("target_route_id"))
	targetVersionValue := form.Get("target_version")
	if combined := strings.TrimSpace(form.Get("target_route")); combined != "" {
		var found bool
		targetRouteID, targetVersionValue, found = strings.Cut(combined, "|")
		if !found {
			return "", 0, planning.ErrValidation
		}
	}
	targetVersion, err := parseVersion(targetVersionValue)
	if strings.TrimSpace(targetRouteID) == "" || err != nil {
		return "", 0, planning.ErrValidation
	}
	return strings.TrimSpace(targetRouteID), targetVersion, nil
}

func planRoute(service *planning.RouteService, dependencies Dependencies, page templates.PageData, csrfCookie string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		departureValue := strings.TrimSpace(request.Form.Get("departure"))
		if departureValue == "" {
			departureValue = strings.TrimSpace(request.Form.Get("departure_date")) + "T" + strings.TrimSpace(request.Form.Get("departure_time"))
		}
		departure, departureErr := time.ParseInLocation("2006-01-02T15:04", departureValue, routeLocation())
		start, startLabel, _, startErr := routeEndpoint(request, dependencies.RouteLocations, session.Actor, "start", false)
		end, endLabel, endAtLastStop, endErr := routeEndpoint(request, dependencies.RouteLocations, session.Actor, "end", true)
		if departureErr != nil || startErr != nil || endErr != nil {
			requestErr := startErr
			if requestErr == nil {
				requestErr = endErr
			}
			if requestErr == nil {
				requestErr = planning.ErrValidation
			}
			renderRoutePlanError(response, request, service, dependencies, page, csrfCookie, requestErr)
			return
		}
		route, err := service.Plan(request.Context(), session.Actor, planning.PlanRouteInput{
			Departure: departure, DriverID: request.Form.Get("driver_id"), ChipperResourceID: request.Form.Get("chipper_resource_id"),
			TransportResourceID: request.Form.Get("transport_resource_id"), StartLabel: startLabel, EndLabel: endLabel, Start: start, End: end,
			JobIDs: request.Form["job_id"], FixedJobIDs: request.Form["fixed_job_id"],
			Optimize: request.Form.Get("optimize") == "true", EndAtLastStop: endAtLastStop,
			RequestID: middleware.GetReqID(request.Context()),
		})
		if err != nil {
			renderRoutePlanError(response, request, service, dependencies, page, csrfCookie, err)
			return
		}
		values := url.Values{"route_id": []string{route.ID}}
		if route.Comparison.ManualDuration > 0 {
			values.Set("manual_distance", strconv.Itoa(route.Comparison.ManualDistanceMeters))
			values.Set("optimized_distance", strconv.Itoa(route.Comparison.OptimizedDistanceMeters))
			values.Set("manual_duration", strconv.FormatInt(int64(route.Comparison.ManualDuration/time.Second), 10))
			values.Set("optimized_duration", strconv.FormatInt(int64(route.Comparison.OptimizedDuration/time.Second), 10))
		}
		http.Redirect(response, request, "/planning/routes?"+values.Encode(), http.StatusSeeOther)
	}
}

func renderRoutePlanError(response http.ResponseWriter, request *http.Request, service *planning.RouteService, dependencies Dependencies, page templates.PageData, csrfCookie string, err error) {
	data, viewErr := adminRouteViewData(request, service, dependencies, page, csrfCookie)
	if viewErr != nil {
		dependencies.Logger.WarnContext(request.Context(), "route planning context unavailable", slog.String("error_code", planningErrorCode(viewErr)))
	}
	status, message := routeError(err)
	if status == http.StatusUnprocessableEntity {
		message = routePlanValidationMessage(request, message)
	}
	data.Error = message
	data.Form = routeFormState(request)
	data.SelectedJobIDs = append([]string(nil), request.Form["job_id"]...)
	if value := strings.TrimSpace(request.Form.Get("departure")); value != "" {
		data.Departure = value
	} else if date, clock := strings.TrimSpace(request.Form.Get("departure_date")), strings.TrimSpace(request.Form.Get("departure_time")); date != "" && clock != "" {
		data.Departure = date + "T" + clock
	}
	render(response, request, templates.Routes(data), status, dependencies.Logger)
}

func routeFormState(request *http.Request) templates.RouteFormState {
	return templates.RouteFormState{
		Submitted:         true,
		DriverID:          strings.TrimSpace(request.Form.Get("driver_id")),
		ChipperResourceID: strings.TrimSpace(request.Form.Get("chipper_resource_id")),
		TransportID:       strings.TrimSpace(request.Form.Get("transport_resource_id")),
		Start:             routeEndpointFormState(request, "start"),
		End:               routeEndpointFormState(request, "end"),
		Optimize:          request.Form.Get("optimize") == "true",
	}
}

func routeEndpointFormState(request *http.Request, prefix string) templates.RouteEndpointFormState {
	return templates.RouteEndpointFormState{
		Selection: strings.TrimSpace(request.Form.Get(prefix + "_selection")),
		Label:     strings.TrimSpace(request.Form.Get(prefix + "_custom_label")),
		Address:   strings.TrimSpace(request.Form.Get(prefix + "_custom_address")),
		Latitude:  strings.TrimSpace(request.Form.Get(prefix + "_latitude")),
		Longitude: strings.TrimSpace(request.Form.Get(prefix + "_longitude")),
		Confirmed: request.Form.Get(prefix+"_custom_confirmed") == "true" ||
			request.Form.Get(prefix+"_custom_confirmed_native") == "true",
	}
}

func routePlanValidationMessage(request *http.Request, fallback string) string {
	missing := make([]string, 0, 8)
	if len(request.Form["job_id"]) == 0 {
		missing = append(missing, "mindestens einen Auftrag auswählen")
	}
	missing = append(missing, routeEndpointValidationProblems(request, "start", "Startort", false)...)
	missing = append(missing, routeEndpointValidationProblems(request, "end", "Endort", true)...)
	if strings.TrimSpace(request.Form.Get("driver_id")) == "" {
		missing = append(missing, "Fahrer auswählen")
	}
	if strings.TrimSpace(request.Form.Get("chipper_resource_id")) == "" {
		missing = append(missing, "Hackmaschine auswählen")
	}
	departure := strings.TrimSpace(request.Form.Get("departure"))
	if departure == "" {
		departure = strings.TrimSpace(request.Form.Get("departure_date")) + "T" + strings.TrimSpace(request.Form.Get("departure_time"))
	}
	if _, err := time.ParseInLocation("2006-01-02T15:04", departure, routeLocation()); err != nil {
		missing = append(missing, "gültiges Abfahrtsdatum und gültige Abfahrtszeit eingeben")
	}
	if len(missing) == 0 {
		return fallback
	}
	return "Bitte ergänzen: " + strings.Join(missing, "; ") + "."
}

func routeEndpointValidationProblems(request *http.Request, prefix, heading string, allowLastStop bool) []string {
	selection := strings.TrimSpace(request.Form.Get(prefix + "_selection"))
	if strings.HasPrefix(selection, "saved:") || (selection == "last_stop" && allowLastStop) {
		return nil
	}
	if selection != "custom" {
		return []string{heading + " auswählen"}
	}
	problems := make([]string, 0, 4)
	confirmed := request.Form.Get(prefix+"_custom_confirmed") == "true" ||
		request.Form.Get(prefix+"_custom_confirmed_native") == "true"
	if !confirmed {
		problems = append(problems, "individuellen "+heading+" ausdrücklich mit „Standort übernehmen“ bestätigen")
	}
	if strings.TrimSpace(request.Form.Get(prefix+"_custom_label")) == "" {
		problems = append(problems, "Bezeichnung für den "+heading+" eingeben")
	}
	if strings.TrimSpace(request.Form.Get(prefix+"_custom_address")) == "" {
		problems = append(problems, "geprüfte Adresse für den "+heading+" eingeben")
	}
	if _, err := routePoint(request.Form.Get(prefix+"_latitude"), request.Form.Get(prefix+"_longitude")); err != nil {
		problems = append(problems, "gültige Koordinaten für den "+heading+" eingeben")
	}
	return problems
}

func assignRoute(service *planning.RouteService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			_, err = service.Assign(request.Context(), session.Actor, planning.AssignRouteInput{ID: chi.URLParam(request, "routeID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())})
		}
		if err != nil {
			status, message := routeError(err)
			if status >= 500 {
				logger.ErrorContext(request.Context(), "route assignment failed", slog.String("error_code", planningErrorCode(err)))
			}
			http.Redirect(response, request, "/planning/routes?route_id="+url.QueryEscape(chi.URLParam(request, "routeID"))+"&error="+url.QueryEscape(message), http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/planning/routes?route_id="+url.QueryEscape(chi.URLParam(request, "routeID")), http.StatusSeeOther)
	}
}

func ownRoutePage(service *planning.RouteService, dependencies Dependencies, page templates.PageData, csrfCookie string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		day := strings.TrimSpace(request.URL.Query().Get("date"))
		if day == "" {
			day = time.Now().In(routeLocation()).Format(time.DateOnly)
		}
		data := templates.RoutePageData{Shell: shell(request, page, csrfCookie), Own: true, SelectedDay: day, RetrievedAt: time.Now().UTC()}
		route, err := service.OwnRouteForDate(request.Context(), session.Actor, day)
		if err == nil {
			data.Route = &route
		} else if !errors.Is(err, planning.ErrNotFound) {
			status, message := routeError(err)
			data.Error = message
			render(response, request, templates.Routes(data), status, dependencies.Logger)
			return
		}
		data.Error = strings.TrimSpace(request.URL.Query().Get("error"))
		render(response, request, templates.Routes(data), http.StatusOK, dependencies.Logger)
	}
}

func applyRouteComparisonQuery(route *planning.RouteDraft, values url.Values) {
	manualDistance, errA := strconv.Atoi(values.Get("manual_distance"))
	optimizedDistance, errB := strconv.Atoi(values.Get("optimized_distance"))
	manualDuration, errC := strconv.ParseInt(values.Get("manual_duration"), 10, 64)
	optimizedDuration, errD := strconv.ParseInt(values.Get("optimized_duration"), 10, 64)
	const (
		maxComparisonDistance = 10_000_000
		maxComparisonSeconds  = int64((7 * 24 * time.Hour) / time.Second)
	)
	if errA != nil || errB != nil || errC != nil || errD != nil ||
		manualDistance < 0 || manualDistance > maxComparisonDistance ||
		optimizedDistance < 0 || optimizedDistance > maxComparisonDistance ||
		manualDuration <= 0 || manualDuration > maxComparisonSeconds ||
		optimizedDuration <= 0 || optimizedDuration > maxComparisonSeconds {
		return
	}
	route.Comparison = planning.RouteComparison{
		ManualDistanceMeters: manualDistance, OptimizedDistanceMeters: optimizedDistance,
		ManualDuration: time.Duration(manualDuration) * time.Second, OptimizedDuration: time.Duration(optimizedDuration) * time.Second,
	}
}

func reorderOwnRoute(service *planning.RouteService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		stopIDs := append([]string(nil), request.Form["stop_id"]...)
		if err == nil {
			stopIDs, err = applyOwnRouteStep(stopIDs, request.Form.Get("move_up"), request.Form.Get("move_down"))
		}
		var route planning.RouteDraft
		if err == nil {
			route, err = service.ReorderOwn(request.Context(), session.Actor, planning.ReorderOwnRouteInput{
				ID: chi.URLParam(request, "routeID"), ExpectedVersion: version, StopIDs: stopIDs, RequestID: middleware.GetReqID(request.Context()),
			})
		}
		if err != nil {
			_, message := routeError(err)
			logger.InfoContext(request.Context(), "own route reorder rejected", slog.String("error_code", planningErrorCode(err)))
			http.Redirect(response, request, "/my-route?error="+url.QueryEscape(message), http.StatusSeeOther)
			return
		}
		day := route.Departure.In(routeLocation()).Format(time.DateOnly)
		http.Redirect(response, request, "/my-route?date="+url.QueryEscape(day), http.StatusSeeOther)
	}
}

func applyOwnRouteStep(stopIDs []string, moveUp, moveDown string) ([]string, error) {
	moveUp = strings.TrimSpace(moveUp)
	moveDown = strings.TrimSpace(moveDown)
	if moveUp == "" && moveDown == "" {
		return stopIDs, nil
	}
	if moveUp != "" && moveDown != "" {
		return nil, planning.ErrValidation
	}
	targetID := moveUp
	offset := -1
	if moveDown != "" {
		targetID = moveDown
		offset = 1
	}
	for index, stopID := range stopIDs {
		if strings.TrimSpace(stopID) != targetID {
			continue
		}
		other := index + offset
		if other < 0 || other >= len(stopIDs) {
			return nil, planning.ErrValidation
		}
		result := append([]string(nil), stopIDs...)
		result[index], result[other] = result[other], result[index]
		return result, nil
	}
	return nil, planning.ErrValidation
}

func routePoint(latitude, longitude string) (planning.Point, error) {
	lat, latErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(latitude), ",", "."), 64)
	lon, lonErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(longitude), ",", "."), 64)
	point := planning.Point{Latitude: lat, Longitude: lon}
	if latErr != nil || lonErr != nil || !point.Valid() {
		return planning.Point{}, planning.ErrValidation
	}
	return point, nil
}

func routeEndpoint(request *http.Request, service *routelocation.Service, actor auth.Actor, prefix string, allowLastStop bool) (planning.Point, string, bool, error) {
	selection := strings.TrimSpace(request.Form.Get(prefix + "_selection"))
	if selection == "last_stop" && allowLastStop {
		return planning.Point{}, "Letzter Stopp", true, nil
	}
	if strings.HasPrefix(selection, "saved:") {
		if service == nil {
			return planning.Point{}, "", false, routelocation.ErrNotFound
		}
		value := strings.TrimSpace(strings.TrimPrefix(selection, "saved:"))
		id := value
		embeddedVersion := ""
		if separator := strings.LastIndex(value, ":"); separator > 0 {
			id = strings.TrimSpace(value[:separator])
			embeddedVersion = strings.TrimSpace(value[separator+1:])
		}
		versionValue := strings.TrimSpace(request.Form.Get(prefix + "_location_version"))
		if versionValue == "" {
			versionValue = embeddedVersion
		} else if embeddedVersion != "" && versionValue != embeddedVersion {
			return planning.Point{}, "", false, routelocation.ErrConflict
		}
		version, err := parseVersion(versionValue)
		if err != nil || id == "" {
			return planning.Point{}, "", false, routelocation.ErrValidation
		}
		if submittedID := strings.TrimSpace(request.Form.Get(prefix + "_location_id")); submittedID != "" && submittedID != id {
			return planning.Point{}, "", false, routelocation.ErrConflict
		}
		location, err := service.Resolve(request.Context(), actor, id, version)
		if err != nil {
			return planning.Point{}, "", false, err
		}
		return planning.Point{Latitude: location.Latitude, Longitude: location.Longitude}, location.Label, false, nil
	}
	confirmed := request.Form.Get(prefix+"_custom_confirmed") == "true" || request.Form.Get(prefix+"_custom_confirmed_native") == "true"
	if selection != "custom" || !confirmed {
		return planning.Point{}, "", false, routelocation.ErrValidation
	}
	label := strings.TrimSpace(request.Form.Get(prefix + "_custom_label"))
	address := strings.TrimSpace(request.Form.Get(prefix + "_custom_address"))
	if label == "" || address == "" || len([]rune(label)) > 120 || len([]rune(address)) > 500 {
		return planning.Point{}, "", false, routelocation.ErrValidation
	}
	point, err := routePoint(request.Form.Get(prefix+"_latitude"), request.Form.Get(prefix+"_longitude"))
	if err != nil {
		return planning.Point{}, "", false, routelocation.ErrValidation
	}
	return point, label, false, nil
}

func defaultRouteDeparture(open string) string {
	now := time.Now().In(routeLocation())
	hourMinute, err := time.Parse("15:04", open)
	if err != nil {
		hourMinute = time.Date(0, 1, 1, 7, 0, 0, 0, time.UTC)
	}
	value := time.Date(now.Year(), now.Month(), now.Day(), hourMinute.Hour(), hourMinute.Minute(), 0, 0, routeLocation())
	if !value.After(now) {
		value = value.AddDate(0, 0, 1)
	}
	return value.Format("2006-01-02T15:04")
}

func routeLocation() *time.Location {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return time.FixedZone("Europe/Vienna", 60*60)
	}
	return location
}

func routeError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "Für diese Route fehlt die Berechtigung."
	case errors.Is(err, planning.ErrConflict):
		return http.StatusConflict, "Auftrag, Route oder Belegung hat sich geändert. Bitte Route neu laden und erneut prüfen."
	case errors.Is(err, planning.ErrNoCapacity):
		return http.StatusUnprocessableEntity, "Fahrer oder Ressourcen sind für mindestens einen Stopp nicht verfügbar."
	case errors.Is(err, planning.ErrValidation):
		return http.StatusUnprocessableEntity, "Bitte Aufträge, Fahrer, Ressource und Abfahrtszeit vollständig prüfen."
	case errors.Is(err, planning.ErrNotFound):
		return http.StatusNotFound, "Die Route wurde nicht gefunden."
	case errors.Is(err, routelocation.ErrConflict):
		return http.StatusConflict, "Der gewählte Start- oder Endort wurde geändert. Bitte Auswahl neu laden."
	case errors.Is(err, routelocation.ErrNotFound):
		return http.StatusNotFound, "Der gewählte Start- oder Endort ist nicht mehr verfügbar."
	case errors.Is(err, routelocation.ErrValidation):
		return http.StatusUnprocessableEntity, "Bitte Start- und Endort auswählen und individuelle Orte ausdrücklich übernehmen."
	default:
		return http.StatusInternalServerError, "Die Route konnte derzeit nicht verarbeitet werden."
	}
}
