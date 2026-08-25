package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerDriverRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	drivers := dependencies.Drivers
	resources := dependencies.Resources
	csrfCookie := dependencies.Config.Auth.CSRFCookieName
	logger := dependencies.Logger

	router.Get("/availability", availabilityPage(drivers, page, csrfCookie, logger, false))
	router.Post("/availability/rules", createAvailabilityRule(drivers, logger, false))
	router.Post("/availability/rules/{ruleID}", updateAvailabilityRule(drivers, logger, false))
	router.Post("/availability/rules/{ruleID}/delete", deleteAvailabilityRule(drivers, logger, false))
	router.Post("/availability/exceptions", createAvailabilityException(drivers, logger, false))
	router.Post("/availability/exceptions/{exceptionID}", updateAvailabilityException(drivers, logger, false))
	router.Post("/availability/exceptions/{exceptionID}/delete", deleteAvailabilityException(drivers, logger, false))
	router.Get("/api/v1/me/availability", availabilityAPI(drivers, logger, false))
	router.Get("/api/v1/drivers/{driverID}/availability", availabilityAPI(drivers, logger, true))

	router.Route("/admin", func(admin chi.Router) {
		admin.Group(func(driverAdmin chi.Router) {
			driverAdmin.Use(requirePermission(auth.PermissionDriverManage, page, logger))
			driverAdmin.Get("/drivers", driversPage(drivers, dependencies.Identity, page, csrfCookie, logger))
			driverAdmin.Post("/drivers", createDriver(drivers, logger))
			driverAdmin.Post("/drivers/{driverID}", updateDriver(drivers, logger))
			driverAdmin.Post("/drivers/{driverID}/deactivate", deactivateDriver(drivers, logger))
			driverAdmin.Get("/drivers/{driverID}/availability", availabilityPage(drivers, page, csrfCookie, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/rules", createAvailabilityRule(drivers, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/rules/{ruleID}", updateAvailabilityRule(drivers, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/rules/{ruleID}/delete", deleteAvailabilityRule(drivers, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/exceptions", createAvailabilityException(drivers, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/exceptions/{exceptionID}", updateAvailabilityException(drivers, logger, true))
			driverAdmin.Post("/drivers/{driverID}/availability/exceptions/{exceptionID}/delete", deleteAvailabilityException(drivers, logger, true))
		})
		admin.Group(func(resourceAdmin chi.Router) {
			resourceAdmin.Use(requirePermission(auth.PermissionResourceManage, page, logger))
			resourceAdmin.Get("/resources", resourcesPage(resources, page, csrfCookie, logger))
			resourceAdmin.Post("/resources", createResource(resources, logger))
			resourceAdmin.Post("/resources/{resourceID}", updateResource(resources, logger))
			resourceAdmin.Post("/resources/{resourceID}/deactivate", deactivateResource(resources, logger))
		})
	})
}

func driversPage(service *driver.Service, identity *auth.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		profiles, err := service.ListProfiles(request.Context(), session.Actor)
		if err != nil {
			operationsPageError(response, request, page, logger, err, "Fahrerprofile nicht verfügbar")
			return
		}
		users, err := identity.ListUsers(request.Context(), session.Actor)
		if err != nil {
			operationsPageError(response, request, page, logger, err, "Fahrer-Logins nicht verfügbar")
			return
		}
		location, _ := time.LoadLocation("Europe/Vienna")
		now := time.Now().In(location)
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UTC()
		to := from.In(location).AddDate(0, 0, 7).UTC()
		overview := make(map[string][]driver.Interval, len(profiles))
		for _, profile := range profiles {
			intervals, resolveErr := service.ResolveAvailability(request.Context(), session.Actor, profile.ID, from, to)
			if resolveErr != nil {
				logger.WarnContext(request.Context(), "driver overview resolution rejected", slog.String("error_code", "availability_overview_invalid"))
				continue
			}
			overview[profile.ID] = intervals
		}
		render(response, request, templates.Drivers(templates.DriversData{Shell: shell(request, page, csrfCookie), Drivers: profiles, Users: users, Overview: overview}), http.StatusOK, logger)
	}
}

func createDriver(service *driver.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		_, err := service.CreateProfile(request.Context(), session.Actor, profileInput(request), middleware.GetReqID(request.Context()))
		if err != nil {
			operationsMutationError(response, request, logger, err, "driver_create_rejected")
			return
		}
		http.Redirect(response, request, "/admin/drivers", http.StatusSeeOther)
	}
}

func updateDriver(service *driver.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.UpdateProfile(request.Context(), session.Actor, chi.URLParam(request, "driverID"), version, profileInput(request), middleware.GetReqID(request.Context()))
		}
		if err != nil {
			operationsMutationError(response, request, logger, err, "driver_update_rejected")
			return
		}
		http.Redirect(response, request, "/admin/drivers", http.StatusSeeOther)
	}
}

func deactivateDriver(service *driver.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.DeactivateProfile(request.Context(), session.Actor, chi.URLParam(request, "driverID"), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			operationsMutationError(response, request, logger, err, "driver_deactivate_rejected")
			return
		}
		http.Redirect(response, request, "/admin/drivers", http.StatusSeeOther)
	}
}

func profileInput(request *http.Request) driver.ProfileInput {
	return driver.ProfileInput{
		UserID: request.Form.Get("user_id"), DisplayName: request.Form.Get("display_name"),
		Phone: request.Form.Get("phone"), Email: request.Form.Get("email"),
		CanCompleteJobs: request.Form.Get("can_complete_jobs") == "true", InternalNote: request.Form.Get("internal_note"),
	}
}

func resourcesPage(service *resource.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		items, err := service.List(request.Context(), session.Actor)
		if err != nil {
			operationsPageError(response, request, page, logger, err, "Ressourcen nicht verfügbar")
			return
		}
		render(response, request, templates.Resources(templates.ResourcesData{Shell: shell(request, page, csrfCookie), Resources: items}), http.StatusOK, logger)
	}
}

func createResource(service *resource.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		input, err := resourceInput(request)
		if err == nil {
			_, err = service.Create(request.Context(), session.Actor, input, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			operationsMutationError(response, request, logger, err, "resource_create_rejected")
			return
		}
		http.Redirect(response, request, "/admin/resources", http.StatusSeeOther)
	}
}

func updateResource(service *resource.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var input resource.Input
		if err == nil {
			input, err = resourceInput(request)
		}
		if err == nil {
			err = service.Update(request.Context(), session.Actor, chi.URLParam(request, "resourceID"), version, input, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			operationsMutationError(response, request, logger, err, "resource_update_rejected")
			return
		}
		http.Redirect(response, request, "/admin/resources", http.StatusSeeOther)
	}
}

func deactivateResource(service *resource.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.Deactivate(request.Context(), session.Actor, chi.URLParam(request, "resourceID"), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			operationsMutationError(response, request, logger, err, "resource_deactivate_rejected")
			return
		}
		http.Redirect(response, request, "/admin/resources", http.StatusSeeOther)
	}
}

func resourceInput(request *http.Request) (resource.Input, error) {
	capacity := resource.Capacity{}
	volume, err := optionalFloat(request.Form.Get("volume_m3"))
	if err != nil {
		return resource.Input{}, resource.ErrValidation
	}
	payload, err := optionalInt32(request.Form.Get("payload_kg"))
	if err != nil {
		return resource.Input{}, resource.ErrValidation
	}
	seats, err := optionalInt32(request.Form.Get("seats"))
	if err != nil {
		return resource.Input{}, resource.ErrValidation
	}
	capacity.VolumeM3, capacity.PayloadKG, capacity.Seats = volume, payload, seats
	return resource.Input{Type: resource.Type(request.Form.Get("type")), Name: request.Form.Get("name"), IsExclusive: request.Form.Get("exclusive") == "true", Capacity: capacity, InternalNote: request.Form.Get("internal_note")}, nil
}

func availabilityPage(service *driver.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		data, err := service.Schedule(request.Context(), session.Actor, target)
		if err != nil {
			operationsPageError(response, request, page, logger, err, "Verfügbarkeit nicht verfügbar")
			return
		}
		location, _ := time.LoadLocation("Europe/Vienna")
		now := time.Now().In(location)
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UTC()
		to := from.In(location).AddDate(0, 0, 14).UTC()
		intervals, err := service.ResolveAvailability(request.Context(), session.Actor, target, from, to)
		if err != nil {
			operationsPageError(response, request, page, logger, err, "Verfügbarkeit kann wegen einer ungültigen Ortszeit nicht dargestellt werden")
			return
		}
		basePath := "/availability"
		if admin {
			basePath = "/admin/drivers/" + target + "/availability"
		}
		render(response, request, templates.Availability(templates.AvailabilityData{Shell: shell(request, page, csrfCookie), Data: data, Intervals: intervals, BasePath: basePath, Admin: admin}), http.StatusOK, logger)
	}
}

func createAvailabilityRule(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		_, err := service.CreateRule(request.Context(), session.Actor, target, ruleInput(request), middleware.GetReqID(request.Context()))
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_rule_create_rejected")
	}
}

func updateAvailabilityRule(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.UpdateRule(request.Context(), session.Actor, target, chi.URLParam(request, "ruleID"), version, ruleInput(request), middleware.GetReqID(request.Context()))
		}
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_rule_update_rejected")
	}
}

func deleteAvailabilityRule(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.DeleteRule(request.Context(), session.Actor, target, chi.URLParam(request, "ruleID"), version, middleware.GetReqID(request.Context()))
		}
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_rule_delete_rejected")
	}
}

func createAvailabilityException(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		input, err := exceptionInput(request)
		if err == nil {
			_, err = service.CreateException(request.Context(), session.Actor, target, input, middleware.GetReqID(request.Context()))
		}
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_exception_create_rejected")
	}
}

func updateAvailabilityException(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		version, err := parseVersion(request.Form.Get("version"))
		var input driver.ExceptionInput
		if err == nil {
			input, err = exceptionInput(request)
		}
		if err == nil {
			err = service.UpdateException(request.Context(), session.Actor, target, chi.URLParam(request, "exceptionID"), version, input, middleware.GetReqID(request.Context()))
		}
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_exception_update_rejected")
	}
}

func deleteAvailabilityException(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.DeleteException(request.Context(), session.Actor, target, chi.URLParam(request, "exceptionID"), version, middleware.GetReqID(request.Context()))
		}
		availabilityMutationResult(response, request, logger, err, target, admin, "availability_exception_delete_rejected")
	}
}

func availabilityAPI(service *driver.Service, logger *slog.Logger, admin bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		target := availabilityTarget(session.Actor, request, admin)
		from, fromErr := time.Parse(time.RFC3339, request.URL.Query().Get("from"))
		to, toErr := time.Parse(time.RFC3339, request.URL.Query().Get("to"))
		if fromErr != nil || toErr != nil {
			http.Error(response, "from und to müssen RFC3339-Zeitpunkte sein.", http.StatusBadRequest)
			return
		}
		intervals, err := service.ResolveAvailability(request.Context(), session.Actor, target, from.UTC(), to.UTC())
		if err != nil {
			operationsMutationError(response, request, logger, err, "availability_query_rejected")
			return
		}
		type overlayInterval struct {
			StartsAt   time.Time     `json:"starts_at"`
			EndsAt     time.Time     `json:"ends_at"`
			Status     driver.Status `json:"status"`
			Source     driver.Source `json:"source"`
			SourceType string        `json:"source_type,omitempty"`
			Reason     string        `json:"reason"`
		}
		minimal := make([]overlayInterval, 0, len(intervals))
		for _, interval := range intervals {
			minimal = append(minimal, overlayInterval{StartsAt: interval.StartsAt, EndsAt: interval.EndsAt, Status: interval.Status, Source: interval.Source, SourceType: interval.SourceType, Reason: interval.Reason})
		}
		writeJSON(response, http.StatusOK, map[string]any{"driver_id": target, "timezone": "Europe/Vienna", "intervals": minimal})
	}
}

func availabilityTarget(actor auth.Actor, request *http.Request, admin bool) string {
	if admin {
		return chi.URLParam(request, "driverID")
	}
	return actor.DriverID
}

func availabilityMutationResult(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error, target string, admin bool, code string) {
	if err != nil {
		operationsMutationError(response, request, logger, err, code)
		return
	}
	location := "/availability"
	if admin {
		location = "/admin/drivers/" + target + "/availability"
	}
	http.Redirect(response, request, location, http.StatusSeeOther)
}

func ruleInput(request *http.Request) driver.RuleInput {
	weekday, _ := strconv.Atoi(request.Form.Get("weekday"))
	return driver.RuleInput{Weekday: weekday, LocalStart: request.Form.Get("local_start"), LocalEnd: request.Form.Get("local_end"), ValidFrom: request.Form.Get("valid_from"), ValidUntil: request.Form.Get("valid_until"), Status: driver.RuleStatus(request.Form.Get("status")), InternalNote: request.Form.Get("internal_note")}
}

func exceptionInput(request *http.Request) (driver.ExceptionInput, error) {
	input := driver.ExceptionInput{Type: driver.ExceptionType(request.Form.Get("type")), IsAllDay: request.Form.Get("all_day") == "true", InternalNote: request.Form.Get("internal_note")}
	if input.IsAllDay {
		input.LocalDate = request.Form.Get("local_date")
		return input, nil
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return driver.ExceptionInput{}, driver.ErrValidation
	}
	input.StartsAt, err = driver.ParseLocalDateTime(request.Form.Get("starts_at"), location)
	if err != nil {
		return driver.ExceptionInput{}, err
	}
	input.EndsAt, err = driver.ParseLocalDateTime(request.Form.Get("ends_at"), location)
	return input, err
}

func optionalFloat(value string) (*float64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return &parsed, err
}

func optionalInt32(value string) (*int32, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	converted := int32(parsed)
	return &converted, err
}

func operationsMutationError(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error, code string) {
	status := http.StatusUnprocessableEntity
	message := "Die Änderung wurde abgewiesen. Bitte prüfen Sie alle Eingaben."
	if errors.Is(err, auth.ErrForbidden) {
		status, message = http.StatusForbidden, "Für diese Änderung fehlt die Berechtigung."
	} else if errors.Is(err, driver.ErrConflict) || errors.Is(err, resource.ErrConflict) {
		status, message = http.StatusConflict, "Der Datensatz wurde zwischenzeitlich geändert. Bitte laden Sie die Seite neu."
	} else if errors.Is(err, driver.ErrNotFound) || errors.Is(err, resource.ErrNotFound) {
		status, message = http.StatusNotFound, "Der Datensatz wurde nicht gefunden."
	} else if errors.Is(err, driver.ErrLocalTime) {
		message = "Diese Ortszeit ist wegen der Zeitumstellung nicht eindeutig oder existiert nicht. Bitte wählen Sie eine andere Zeit."
	}
	logger.WarnContext(request.Context(), "operations mutation rejected", slog.String("error_code", code))
	http.Error(response, message, status)
}

func operationsPageError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error, title string) {
	status := http.StatusInternalServerError
	message := "Die Daten können derzeit nicht geladen werden."
	if errors.Is(err, auth.ErrForbidden) {
		status, message = http.StatusForbidden, "Für diese Ansicht fehlt die Berechtigung."
	} else if errors.Is(err, driver.ErrNotFound) || errors.Is(err, resource.ErrNotFound) {
		status, message = http.StatusNotFound, "Der Datensatz wurde nicht gefunden."
	} else if errors.Is(err, driver.ErrValidation) || errors.Is(err, driver.ErrLocalTime) {
		status, message = http.StatusUnprocessableEntity, "Die Verfügbarkeitsdaten enthalten eine ungültige Ortszeit. Bitte korrigieren Sie die Regel."
	}
	render(response, request, templates.Error(page, status, title, message), status, logger)
}
