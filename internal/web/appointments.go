package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/notification"
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
	router.Get("/calendar/plan", calendarPlanPage(service, page, csrfCookie, logger))
	router.Post("/calendar/plan", planFromWaitlist(service, page, csrfCookie, logger, false))
	router.Get("/api/v1/calendar", calendarEvents(service, logger))
	router.Get("/api/v1/calendar/conflicts", appointmentConflicts(service, logger))
	router.Post("/api/v1/calendar/plan", planFromWaitlist(service, page, csrfCookie, logger, true))
	router.Route("/api/v1/appointments/{appointmentID}", func(appointmentRouter chi.Router) {
		appointmentRouter.Get("/", appointmentDetail(service, dependencies.Notifications, dependencies.Config.Mail.Enabled, dependencies.Config.SMS.Enabled, logger))
		appointmentRouter.Post("/assign", assignAppointment(service, logger))
		appointmentRouter.Post("/propose", proposeAppointment(service, logger))
		appointmentRouter.Post("/move", moveAppointment(service, logger, false))
		appointmentRouter.Post("/resize", moveAppointment(service, logger, true))
		appointmentRouter.Get("/alternatives", appointmentAlternatives(service, logger))
		appointmentRouter.Post("/swap", swapAppointments(service, logger))
		appointmentRouter.Post("/fix", fixAppointment(service, logger))
		appointmentRouter.Post("/cancel", cancelAppointment(service, logger))
		appointmentRouter.Post("/reopen", reopenAppointment(service, logger))
		appointmentRouter.Post("/complete", completeAppointment(service, logger))
		if dependencies.Notifications != nil {
			appointmentRouter.Post("/confirmation/reissue", reissueConfirmation(dependencies.Notifications, logger))
			appointmentRouter.Post("/confirmation/reset", resetConfirmationResponse(dependencies.Notifications, logger))
		}
	})
}

func appointmentAlternatives(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		startsAt, startErr := time.Parse(time.RFC3339, request.URL.Query().Get("starts_at"))
		endsAt, endErr := time.Parse(time.RFC3339, request.URL.Query().Get("ends_at"))
		if startErr != nil || endErr != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_alternatives_rejected")
			return
		}
		result, err := service.ConflictAlternatives(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"), startsAt.UTC(), endsAt.UTC())
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_alternatives_rejected")
			return
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func swapAppointments(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		firstVersion, firstErr := parseVersion(request.Form.Get("version"))
		secondVersion, secondErr := parseVersion(request.Form.Get("other_version"))
		if firstErr != nil || secondErr != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_swap_rejected")
			return
		}
		values, err := service.SwapAppointments(request.Context(), session.Actor, appointment.SwapInput{FirstID: chi.URLParam(request, "appointmentID"), SecondID: request.Form.Get("other_appointment_id"), FirstVersion: firstVersion, SecondVersion: secondVersion, RequestID: middleware.GetReqID(request.Context())})
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_swap_rejected")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"appointments": values})
	}
}

func appointmentDetail(service *appointment.Service, notifications *notification.AdminService, mailEnabled, smsEnabled bool, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		value, err := service.AppointmentDetail(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"))
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_detail_rejected")
			return
		}
		channels := notificationChannels(value, mailEnabled, smsEnabled)
		assessment := notification.AssessChannels(value.NotificationPreference, value.Email, value.Phone, mailEnabled, smsEnabled)
		targets := make([]map[string]string, 0, len(assessment.Targets))
		for _, target := range assessment.Targets {
			targets = append(targets, map[string]string{"channel": target.Label, "recipient": target.Recipient})
		}
		statuses := []notification.Status{}
		if notifications != nil {
			statuses, err = notifications.AppointmentStatuses(request.Context(), session.Actor, value.ID)
			if err != nil {
				appointmentAPIError(response, request, logger, err, "notification_status_rejected")
				return
			}
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"id": value.ID, "job_id": value.JobID, "customer_id": value.CustomerID,
			"title": value.CustomerName + " · " + value.JobNumber,
			"start": value.StartsAt, "end": value.EndsAt, "lifecycle": value.Lifecycle, "status_label": templates.AppointmentStatusLabel(value.Lifecycle, value.Confirmation),
			"locality": value.Locality, "volume_m3": value.VolumeM3, "drivers": value.Drivers, "resources": value.Resources,
			"notes":    value.Notes,
			"maps_url": value.MapsURL, "phone": value.Phone, "email": value.Email,
			"notification_preference": value.NotificationPreference, "notification_channels": channels,
			"notification_targets": targets, "notification_warning": assessment.Warning,
			"notification_suggestion": assessment.Suggestion, "notification_requires_override": assessment.RequiresOverrideReason,
			"notifications":              statuses,
			"version":                    value.Version,
			"can_complete":               value.CanComplete,
			"complete_requires_override": value.CompleteRequiresOverride,
			"can_fix":                    session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleProposal,
			"can_cancel":                 session.Actor.Role == auth.RoleAdmin && value.Lifecycle.Editable(),
			"can_reopen":                 session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleCancelled,
			"can_reschedule":             session.Actor.Role == auth.RoleAdmin && value.Lifecycle.Editable(),
			"can_swap":                   session.Actor.Role == auth.RoleAdmin && (value.Lifecycle == appointment.LifecycleDraft || value.Lifecycle == appointment.LifecycleProposal),
			"can_reissue":                session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleFixed,
			"can_reset_confirmation":     session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleFixed && value.Confirmation != appointment.ConfirmationPending && value.Confirmation != appointment.ConfirmationNotRequested,
		})
	}
}

func reissueConfirmation(service *notification.AdminService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.Reissue(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"), version, request.Form.Get("reason"), middleware.GetReqID(request.Context()))
		}
		notificationAdminResult(response, request, logger, err)
	}
}

func resetConfirmationResponse(service *notification.AdminService, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.ResetResponse(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"), version, request.Form.Get("reason"), middleware.GetReqID(request.Context()))
		}
		notificationAdminResult(response, request, logger, err)
	}
}

func notificationAdminResult(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error) {
	if err == nil {
		writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	status, code, message := http.StatusUnprocessableEntity, "notification_action_invalid", "Bitte geben Sie einen nachvollziehbaren Grund an."
	if errors.Is(err, auth.ErrForbidden) {
		status, code, message = http.StatusForbidden, "forbidden", "Für diese Aktion fehlt die Berechtigung."
	} else if errors.Is(err, notification.ErrAdminActionUnavailable) {
		status, code, message = http.StatusConflict, "notification_action_conflict", "Der Terminstand hat sich geändert oder die Aktion ist nicht mehr möglich."
	}
	logger.WarnContext(request.Context(), "notification admin action rejected", slog.String("error_code", code))
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func notificationChannels(value appointment.Detail, mailEnabled, smsEnabled bool) []string {
	channels := make([]string, 0, 2)
	if mailEnabled && (value.NotificationPreference == "email" || value.NotificationPreference == "both") && value.Email != "" {
		channels = append(channels, "E-Mail")
	}
	if smsEnabled && (value.NotificationPreference == "sms" || value.NotificationPreference == "both") && value.Phone != "" {
		channels = append(channels, "SMS")
	}
	return channels
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
			Notice: calendarNotice(request.URL.Query().Get("planned")),
		}), http.StatusOK, logger)
	}
}

func calendarNotice(value string) string {
	if value == "proposal" {
		return "Terminvorschlag gespeichert. Der Termin ist noch nicht fixiert und es wurde keine Nachricht versendet."
	}
	return ""
}

func calendarPlanPage(service *appointment.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		session, _ := sessionFromContext(request.Context())
		jobID := strings.TrimSpace(request.URL.Query().Get("job_id"))
		options, job, err := calendarPlanningOptions(request, service, session.Actor, jobID)
		if err != nil {
			calendarPlanPageError(response, request, page, logger, err)
			return
		}
		location, err := time.LoadLocation("Europe/Vienna")
		if err != nil {
			calendarPlanPageError(response, request, page, logger, err)
			return
		}
		now := time.Now().In(location)
		start := time.Date(now.Year(), now.Month(), now.Day()+1, 8, 0, 0, 0, location)
		data := templates.CalendarPlanData{
			Shell: shell(request, page, csrfCookie), Options: options, Job: job,
			Values: templates.PlanningFormValues{
				CSRFToken: shell(request, page, csrfCookie).CSRFToken,
				JobID:     job.JobID, StartsAt: start.Format("2006-01-02T15:04"),
				DurationMinutes: strconv.FormatInt(int64(job.EstimatedHackMinutes+job.EstimatedTransportMinutes), 10),
				TransportMode:   job.TransportMode, ExternalTransportConfirmed: job.ExternalTransportConfirmed,
			},
		}
		render(response, request, templates.CalendarPlan(data), http.StatusOK, logger)
	}
}

func calendarPlanningOptions(request *http.Request, service *appointment.Service, actor auth.Actor, jobID string) (appointment.PlanningOptions, appointment.WaitlistItem, error) {
	if jobID == "" {
		return appointment.PlanningOptions{}, appointment.WaitlistItem{}, appointment.ErrNotFound
	}
	options, err := service.PlanningOptions(request.Context(), actor)
	if err != nil {
		return appointment.PlanningOptions{}, appointment.WaitlistItem{}, err
	}
	for _, item := range options.Waitlist {
		if item.JobID == jobID {
			return options, item, nil
		}
	}
	return appointment.PlanningOptions{}, appointment.WaitlistItem{}, appointment.ErrNotFound
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

func planFromWaitlist(service *appointment.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, jsonResponse bool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		start, duration, err := planningTime(request)
		var planned appointment.Appointment
		if err == nil {
			assignments := planningAssignments(request)
			assignments.OverrideReason = request.Form.Get("override_reason")
			planned, err = service.PlanFromWaitlist(request.Context(), session.Actor, appointment.PlanInput{
				CreateDraftInput: appointment.CreateDraftInput{
					JobID: request.Form.Get("job_id"), RequestID: middleware.GetReqID(request.Context()),
					Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(duration)},
				},
				Assignments: assignments,
			})
		}
		if err != nil {
			if jsonResponse {
				appointmentAPIError(response, request, logger, err, "appointment_plan_rejected")
			} else {
				renderCalendarPlanError(response, request, service, page, csrfCookie, logger, err)
			}
			return
		}
		if jsonResponse {
			writeJSON(response, http.StatusCreated, map[string]any{"id": planned.ID, "version": planned.Version, "lifecycle": planned.Lifecycle})
			return
		}
		location, locationErr := time.LoadLocation("Europe/Vienna")
		if locationErr != nil {
			calendarPlanPageError(response, request, page, logger, locationErr)
			return
		}
		query := url.Values{
			"appointment": {planned.ID},
			"date":        {planned.StartsAt.In(location).Format(time.DateOnly)},
			"planned":     {"proposal"},
		}
		http.Redirect(response, request, "/calendar?"+query.Encode(), http.StatusSeeOther)
	}
}

func renderCalendarPlanError(response http.ResponseWriter, request *http.Request, service *appointment.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, planErr error) {
	response.Header().Set("Cache-Control", "no-store")
	session, _ := sessionFromContext(request.Context())
	jobID := strings.TrimSpace(request.Form.Get("job_id"))
	options, job, err := calendarPlanningOptions(request, service, session.Actor, jobID)
	if err != nil {
		calendarPlanPageError(response, request, page, logger, planErr)
		return
	}
	presentation := appointmentErrorPresentation(planErr)
	logger.WarnContext(request.Context(), "appointment request rejected", slog.String("error_code", "appointment_plan_rejected"), slog.String("category", presentation.Code))
	shellData := shell(request, page, csrfCookie)
	data := templates.CalendarPlanData{
		Shell: shellData, Options: options, Job: job,
		Values: templates.PlanningFormValues{
			CSRFToken: shellData.CSRFToken,
			JobID:     jobID, StartsAt: request.Form.Get("starts_at"), DurationMinutes: request.Form.Get("duration_minutes"),
			DriverIDs: append([]string(nil), request.Form["driver_id"]...), PrimaryDriverID: request.Form.Get("primary_driver_id"),
			ChipperResourceID: request.Form.Get("chipper_resource_id"), TransportResourceID: request.Form.Get("transport_resource_id"),
			TrailerResourceID: request.Form.Get("trailer_resource_id"), OverrideReason: request.Form.Get("override_reason"),
			TransportMode: job.TransportMode, ExternalTransportConfirmed: job.ExternalTransportConfirmed,
		},
		Error: templates.PlanningFormError{Message: presentation.Message, FieldID: planningErrorField(request, job, planErr)},
	}
	render(response, request, templates.CalendarPlan(data), presentation.Status, logger)
}

func planningErrorField(request *http.Request, job appointment.WaitlistItem, err error) string {
	location, locationErr := time.LoadLocation("Europe/Vienna")
	if locationErr != nil {
		return ""
	}
	if _, parseErr := driver.ParseLocalDateTime(request.Form.Get("starts_at"), location); parseErr != nil {
		return "planning-start"
	}
	minutes, parseErr := strconv.ParseInt(request.Form.Get("duration_minutes"), 10, 32)
	if parseErr != nil || minutes < 15 || minutes > int64(appointment.MaxDuration/time.Minute) || minutes < int64(job.EstimatedHackMinutes+job.EstimatedTransportMinutes) {
		return "planning-duration"
	}
	primary := request.Form.Get("primary_driver_id")
	primarySelected := false
	for _, driverID := range request.Form["driver_id"] {
		if driverID == primary {
			primarySelected = true
			break
		}
	}
	if len(request.Form["driver_id"]) == 0 || primary == "" || !primarySelected {
		return "planning-primary-driver"
	}
	if request.Form.Get("chipper_resource_id") == "" {
		return "planning-chipper-resource"
	}
	if errors.Is(err, appointment.ErrValidation) {
		return "planning-transport-resource"
	}
	if errors.Is(err, appointment.ErrConflict) {
		return "planning-start"
	}
	if errors.Is(err, appointment.ErrAvailability) {
		return "planning-primary-driver"
	}
	return ""
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
		start, end, timeErr := appointmentMutationTime(request)
		if timeErr != nil {
			err = timeErr
		}
		var value appointment.Appointment
		if err == nil {
			input := appointment.MoveInput{
				MutateInput: appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				StartsAt:    start.UTC(), EndsAt: end.UTC(), OverrideReason: request.Form.Get("override_reason"),
				WithoutNotificationReason: request.Form.Get("without_notification_reason"),
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

func appointmentMutationTime(request *http.Request) (time.Time, time.Time, error) {
	if request.Form.Get("starts_at_local") != "" || request.Form.Get("duration_minutes") != "" {
		location, err := time.LoadLocation("Europe/Vienna")
		if err != nil {
			return time.Time{}, time.Time{}, appointment.ErrValidation
		}
		start, err := driver.ParseLocalDateTime(request.Form.Get("starts_at_local"), location)
		minutes, minutesErr := strconv.ParseInt(request.Form.Get("duration_minutes"), 10, 32)
		if err != nil || minutesErr != nil || minutes < 15 || minutes > int64(appointment.MaxDuration/time.Minute) {
			return time.Time{}, time.Time{}, appointment.ErrValidation
		}
		return start.UTC(), start.Add(time.Duration(minutes) * time.Minute).UTC(), nil
	}
	start, startErr := time.Parse(time.RFC3339, request.Form.Get("starts_at"))
	end, endErr := time.Parse(time.RFC3339, request.Form.Get("ends_at"))
	if startErr != nil || endErr != nil {
		return time.Time{}, time.Time{}, appointment.ErrValidation
	}
	return start.UTC(), end.UTC(), nil
}

func fixAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.FixAppointment(request.Context(), session.Actor, appointment.FixInput{
				MutateInput:               appointment.MutateInput{ID: chi.URLParam(request, "appointmentID"), ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				WithoutNotificationReason: request.Form.Get("without_notification_reason"),
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

func reopenAppointment(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var value appointment.Appointment
		if err == nil {
			value, err = service.ReopenAppointment(request.Context(), session.Actor, appointment.ReopenInput{
				MutateInput: appointment.MutateInput{
					ID:              chi.URLParam(request, "appointmentID"),
					ExpectedVersion: version,
					RequestID:       middleware.GetReqID(request.Context()),
				},
				Reason:         request.Form.Get("reason"),
				OverrideReason: request.Form.Get("override_reason"),
			})
		}
		appointmentMutationResult(response, request, logger, value, err, "appointment_reopen_rejected")
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

type appointmentErrorView struct {
	Status  int
	Code    string
	Message string
}

func appointmentErrorPresentation(err error) appointmentErrorView {
	result := appointmentErrorView{
		Status: http.StatusUnprocessableEntity, Code: "validation_failed",
		Message: "Die Eingaben sind fachlich nicht gültig.",
	}
	switch {
	case errors.Is(err, auth.ErrForbidden):
		result = appointmentErrorView{Status: http.StatusForbidden, Code: "forbidden", Message: "Für diese Planungsaktion fehlt die Berechtigung."}
	case errors.Is(err, appointment.ErrNotFound):
		result = appointmentErrorView{Status: http.StatusNotFound, Code: "not_found", Message: "Termin oder Auftrag wurde nicht gefunden."}
	case errors.Is(err, appointment.ErrConflict):
		result = appointmentErrorView{Status: http.StatusConflict, Code: "reservation_conflict", Message: "Der Stand ist veraltet oder der Slot ist bereits belegt. Bitte Kalender neu laden."}
	case errors.Is(err, appointment.ErrAvailability):
		result.Code, result.Message = "driver_unavailable", "Mindestens ein Fahrer ist nicht verfügbar. Wählen Sie einen anderen Slot oder begründen Sie den Admin-Override."
	case errors.Is(err, appointment.ErrNotification):
		result.Code, result.Message = "notification_channel_missing", "Für diesen Kunden ist kein erreichbarer Benachrichtigungskanal vorhanden. Nur mit begründeter Ausnahme ohne Nachricht fixieren."
	case errors.Is(err, appointment.ErrTransition):
		result.Code, result.Message = "invalid_transition", "Dieser Statuswechsel ist nicht erlaubt."
	case errors.Is(err, driver.ErrLocalTime):
		result.Code, result.Message = "invalid_local_time", "Diese Ortszeit existiert wegen der Zeitumstellung nicht oder ist mehrdeutig."
	case errors.Is(err, appointment.ErrValidation), errors.Is(err, resource.ErrValidation):
	default:
		result = appointmentErrorView{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Die Planung kann derzeit nicht gespeichert werden."}
	}
	return result
}

func appointmentAPIError(response http.ResponseWriter, request *http.Request, logger *slog.Logger, err error, code string) {
	presentation := appointmentErrorPresentation(err)
	logger.WarnContext(request.Context(), "appointment request rejected", slog.String("error_code", code), slog.String("category", presentation.Code))
	writeJSON(response, presentation.Status, map[string]any{"error": map[string]string{"code": presentation.Code, "message": presentation.Message, "request_id": middleware.GetReqID(request.Context())}})
}

func calendarPlanPageError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error) {
	presentation := appointmentErrorPresentation(err)
	logger.WarnContext(request.Context(), "calendar planning page rejected", slog.String("error_code", presentation.Code))
	render(response, request, templates.Error(page, presentation.Status, "Einplanung nicht verfügbar", presentation.Message), presentation.Status, logger)
}

func appointmentPageError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	message := "Der Kalender kann derzeit nicht geladen werden."
	if errors.Is(err, auth.ErrForbidden) {
		status, message = http.StatusForbidden, "Für diese Ansicht fehlt die Berechtigung."
	}
	render(response, request, templates.Error(page, status, "Kalender nicht verfügbar", message), status, logger)
}
