package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerAppointmentRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service := dependencies.Appointments
	csrfCookie := dependencies.Config.Auth.CSRFCookieName
	logger := dependencies.Logger
	router.Get("/calendar", calendarPage(service, page, csrfCookie, logger))
	router.Post("/calendar/plan", planFromWaitlist(service, logger, false))
	router.Get("/api/v1/calendar", calendarEvents(service, logger))
	router.Get("/api/v1/calendar/conflicts", appointmentConflicts(service, logger))
	router.Post("/api/v1/calendar/plan", planFromWaitlist(service, logger, true))
	router.Route("/api/v1/appointments/{appointmentID}", func(appointmentRouter chi.Router) {
		appointmentRouter.Post("/assign", assignAppointment(service, logger))
		appointmentRouter.Post("/propose", proposeAppointment(service, logger))
		appointmentRouter.Post("/move", moveAppointment(service, logger, false))
		appointmentRouter.Post("/resize", moveAppointment(service, logger, true))
		appointmentRouter.Post("/fix", fixAppointment(service, logger))
		appointmentRouter.Post("/cancel", cancelAppointment(service, logger))
		appointmentRouter.Post("/complete", completeAppointment(service, logger))
	})
}

func calendarPage(service *appointment.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		options := appointment.PlanningOptions{}
		var err error
		if session.Actor.Role == auth.RoleAdmin {
			options, err = service.PlanningOptions(request.Context(), session.Actor)
		}
		location, locationErr := time.LoadLocation("Europe/Vienna")
		if locationErr != nil {
			err = locationErr
		}
		now := time.Now().In(location)
		from := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, location).UTC()
		to := from.In(location).AddDate(0, 0, 9).UTC()
		var events []appointment.CalendarEvent
		if err == nil {
			events, err = service.ListCalendarRange(request.Context(), session.Actor, from, to)
		}
		if err != nil {
			appointmentPageError(response, request, page, logger, err)
			return
		}
		render(response, request, templates.Calendar(templates.CalendarData{
			Shell: shell(request, page, csrfCookie), Options: options, Events: events, Timezone: "Europe/Vienna",
		}), http.StatusOK, logger)
	}
}

func calendarEvents(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		from, to, err := parseCalendarRange(request)
		if err != nil {
			appointmentAPIError(response, request, logger, err, "calendar_range_rejected")
			return
		}
		events, err := service.ListCalendarRange(request.Context(), session.Actor, from, to)
		if err != nil {
			appointmentAPIError(response, request, logger, err, "calendar_list_rejected")
			return
		}
		type eventPayload struct {
			ID            string         `json:"id"`
			Title         string         `json:"title"`
			Start         time.Time      `json:"start"`
			End           time.Time      `json:"end"`
			Editable      bool           `json:"editable"`
			ClassName     string         `json:"className"`
			Color         string         `json:"color"`
			ContrastColor string         `json:"contrastColor"`
			ExtendedProps map[string]any `json:"extendedProps"`
		}
		payload := make([]eventPayload, 0, len(events))
		for _, event := range events {
			editable := session.Actor.Role == auth.RoleAdmin && event.Lifecycle.Editable()
			payload = append(payload, eventPayload{
				ID: event.ID, Title: event.CustomerName + " · " + event.JobNumber,
				Start: event.StartsAt, End: event.EndsAt, Editable: editable,
				ClassName: "calendar-event--" + string(event.Lifecycle) + " calendar-confirmation--" + string(event.Confirmation),
				Color:     appointmentColor(event.Lifecycle, event.Confirmation), ContrastColor: "#ffffff",
				ExtendedProps: map[string]any{
					"job_id": event.JobID, "job_number": event.JobNumber, "customer_id": event.CustomerID,
					"customer_name": event.CustomerName, "locality": event.Locality, "volume_m3": event.VolumeM3,
					"job_type": event.JobType, "lifecycle": event.Lifecycle, "confirmation": event.Confirmation,
					"status_label": templates.AppointmentStatusLabel(event.Lifecycle, event.Confirmation),
					"drivers":      event.Drivers, "resources": event.Resources, "version": event.Version,
					"maps_url": event.MapsURL, "can_fix": session.Actor.Role == auth.RoleAdmin && event.Lifecycle == appointment.LifecycleProposal,
					"can_cancel": session.Actor.Role == auth.RoleAdmin && event.Lifecycle.Editable(),
				},
			})
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func appointmentColor(lifecycle appointment.Lifecycle, confirmation appointment.Confirmation) string {
	if lifecycle == appointment.LifecycleCompleted {
		return "#46534a"
	}
	if lifecycle == appointment.LifecycleCancelled {
		return "#6b746d"
	}
	if lifecycle == appointment.LifecycleDraft || lifecycle == appointment.LifecycleProposal {
		return "#9a5b18"
	}
	if confirmation == appointment.ConfirmationConfirmed {
		return "#317347"
	}
	if confirmation == appointment.ConfirmationDeclined || confirmation == appointment.ConfirmationCallback {
		return "#9c342a"
	}
	return "#28659b"
}

func planFromWaitlist(service *appointment.Service, logger *slog.Logger, jsonResponse bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		start, duration, err := planningTime(request)
		var planned appointment.Appointment
		if err == nil {
			planned, err = service.CreateDraftFromWaitlist(request.Context(), session.Actor, appointment.CreateDraftInput{
				JobID: request.Form.Get("job_id"), RequestID: middleware.GetReqID(request.Context()),
				Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(duration)},
			})
		}
		if err == nil {
			planned, err = service.AssignDriversAndResources(request.Context(), session.Actor, appointment.AssignInput{
				MutateInput: appointment.MutateInput{ID: planned.ID, ExpectedVersion: planned.Version, RequestID: middleware.GetReqID(request.Context())},
				Assignments: planningAssignments(request),
			})
		}
		if err == nil {
			planned, err = service.ProposeAppointment(request.Context(), session.Actor, appointment.MutateInput{
				ID: planned.ID, ExpectedVersion: planned.Version, RequestID: middleware.GetReqID(request.Context()),
			}, request.Form.Get("override_reason"))
		}
		if err != nil {
			if planned.ID != "" && planned.Lifecycle.Editable() {
				if _, cleanupErr := service.CancelAppointment(request.Context(), session.Actor, appointment.CancelInput{
					MutateInput: appointment.MutateInput{ID: planned.ID, ExpectedVersion: planned.Version, RequestID: middleware.GetReqID(request.Context())},
					Reason:      "Einplanung verworfen",
				}); cleanupErr != nil {
					logger.WarnContext(request.Context(), "discarding failed planning draft failed", slog.String("error_code", "planning_cleanup_failed"))
				}
			}
			appointmentAPIError(response, request, logger, err, "appointment_plan_rejected")
			return
		}
		if jsonResponse {
			writeJSON(response, http.StatusCreated, map[string]any{"id": planned.ID, "version": planned.Version, "lifecycle": planned.Lifecycle})
			return
		}
		http.Redirect(response, request, "/calendar", http.StatusSeeOther)
	}
}

func assignAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.AssignDriversAndResources(request.Context(), session.Actor, appointment.AssignInput{
				MutateInput: appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				Assignments: planningAssignments(request),
			})
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_assign_rejected")
	}
}

func proposeAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.ProposeAppointment(request.Context(), session.Actor, appointment.MutateInput{
				ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context()),
			}, request.Form.Get("override_reason"))
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_propose_rejected")
	}
}

func moveAppointment(service *appointment.Service, logger *slog.Logger, resize bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		start, startErr := time.Parse(time.RFC3339, request.Form.Get("starts_at"))
		end, endErr := time.Parse(time.RFC3339, request.Form.Get("ends_at"))
		if startErr != nil || endErr != nil {
			err = appointment.ErrValidation
		}
		var value appointment.Appointment
		if err == nil {
			input := appointment.MoveInput{
				MutateInput: appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				StartsAt:    start.UTC(), EndsAt: end.UTC(), OverrideReason: request.Form.Get("override_reason"),
			}
			if resize {
				value, err = service.ResizeAppointment(request.Context(), session.Actor, input)
			} else {
				value, err = service.MoveAppointment(request.Context(), session.Actor, input)
			}
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_time_rejected")
	}
}

func fixAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.FixAppointment(request.Context(), session.Actor, appointment.MutateInput{
				ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context()),
			})
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_fix_rejected")
	}
}

func cancelAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.CancelAppointment(request.Context(), session.Actor, appointment.CancelInput{
				MutateInput: appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				Reason:      request.Form.Get("reason"),
			})
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_cancel_rejected")
	}
}

func completeAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.CompleteAppointment(request.Context(), session.Actor, appointment.CompleteInput{
				MutateInput:    appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				OverrideReason: request.Form.Get("override_reason"),
			})
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_complete_rejected")
	}
}

func appointmentConflicts(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		from, to, err := parseCalendarRange(request)
		var conflicts []appointment.Conflict
		if err == nil {
			conflicts, err = service.ListConflictsAndCapacity(request.Context(), session.Actor, from, to,
				request.URL.Query()["driver_id"], request.URL.Query()["resource_id"], request.URL.Query().Get("exclude_appointment_id"))
		}
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_conflicts_rejected")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"conflicts": conflicts})
	}
}

func planningAssignments(request *http.Request) appointment.AssignmentInput {
	drivers := request.Form["driver_id"]
	primary := request.Form.Get("primary_driver_id")
	if primary == "" && len(drivers) > 0 {
		primary = drivers[0]
	}
	resources := make([]appointment.ResourceAssignment, 0)
	for _, id := range request.Form["chipper_resource_id"] {
		if id != "" {
			resources = append(resources, appointment.ResourceAssignment{ID: id, Purpose: appointment.PurposeChipping})
		}
	}
	for _, id := range request.Form["transport_resource_id"] {
		if id != "" {
			resources = append(resources, appointment.ResourceAssignment{ID: id, Purpose: appointment.PurposeTransport})
		}
	}
	for _, id := range request.Form["trailer_resource_id"] {
		if id != "" {
			resources = append(resources, appointment.ResourceAssignment{ID: id, Purpose: appointment.PurposeTrailer})
		}
	}
	return appointment.AssignmentInput{DriverIDs: drivers, PrimaryDriverID: primary, Resources: resources, OverrideReason: request.Form.Get("override_reason")}
}

func planningTime(request *http.Request) (time.Time, time.Duration, error) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return time.Time{}, 0, appointment.ErrValidation
	}
	start, err := driver.ParseLocalDateTime(request.Form.Get("starts_at"), location)
	minutes, minutesErr := strconv.ParseInt(request.Form.Get("duration_minutes"), 10, 32)
	if err != nil || minutesErr != nil || minutes < 15 || minutes > int64(appointment.MaxDuration/time.Minute) {
		return time.Time{}, 0, appointment.ErrValidation
	}
	return start, time.Duration(minutes) * time.Minute, nil
}

func parseCalendarRange(request *http.Request) (time.Time, time.Time, error) {
	from, fromErr := time.Parse(time.RFC3339, request.URL.Query().Get("from"))
	to, toErr := time.Parse(time.RFC3339, request.URL.Query().Get("to"))
	if fromErr != nil || toErr != nil {
		return time.Time{}, time.Time{}, appointment.ErrValidation
	}
	return from.UTC(), to.UTC(), nil
}

func appointmentMutationResult(response http.ResponseWriter, request *http.Request, logger *slog.Logger, value appointment.Appointment, err error, code string) {
	if err != nil {
		appointmentAPIError(response, request, logger, err, code)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"id": value.ID, "version": value.Version, "lifecycle": value.Lifecycle, "confirmation": value.Confirmation})
}

func appointmentAPIError(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error, code string) {
	status := http.StatusUnprocessableEntity
	errorCode := "validation_failed"
	message := "Die Eingaben sind fachlich nicht gültig."
	switch {
	case errors.Is(err, auth.ErrForbidden):
		status, errorCode, message = http.StatusForbidden, "forbidden", "Für diese Planungsaktion fehlt die Berechtigung."
	case errors.Is(err, appointment.ErrNotFound):
		status, errorCode, message = http.StatusNotFound, "not_found", "Termin oder Auftrag wurde nicht gefunden."
	case errors.Is(err, appointment.ErrConflict):
		status, errorCode, message = http.StatusConflict, "reservation_conflict", "Der Stand ist veraltet oder der Slot ist bereits belegt. Bitte Kalender neu laden."
	case errors.Is(err, appointment.ErrAvailability):
		errorCode, message = "driver_unavailable", "Mindestens ein Fahrer ist nicht verfügbar. Wählen Sie einen anderen Slot oder begründen Sie den Admin-Override."
	case errors.Is(err, appointment.ErrTransition):
		errorCode, message = "invalid_transition", "Dieser Statuswechsel ist nicht erlaubt."
	case errors.Is(err, driver.ErrLocalTime):
		errorCode, message = "invalid_local_time", "Diese Ortszeit existiert wegen der Zeitumstellung nicht oder ist mehrdeutig."
	case errors.Is(err, appointment.ErrValidation), errors.Is(err, resource.ErrValidation):
		errorCode = "validation_failed"
	default:
		status, errorCode, message = http.StatusInternalServerError, "internal_error", "Die Planung kann derzeit nicht gespeichert werden."
	}
	logger.WarnContext(request.Context(), "appointment request rejected", slog.String("error_code", code), slog.String("category", errorCode))
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": errorCode, "message": message}})
}

func appointmentPageError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	message := "Der Kalender kann derzeit nicht geladen werden."
	if errors.Is(err, auth.ErrForbidden) {
		status, message = http.StatusForbidden, "Für diese Ansicht fehlt die Berechtigung."
	}
	render(response, request, templates.Error(page, status, "Kalender nicht verfügbar", message), status, logger)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
