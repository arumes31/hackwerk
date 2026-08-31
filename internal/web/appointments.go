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
	router.Get("/calendar/appointments/{appointmentID}", appointmentDetailPage(service, page, csrfCookie, dependencies.Notifications != nil, appointmentDetailOptions{
		MailEnabled: dependencies.Config.Mail.Enabled, SMSEnabled: dependencies.Config.SMS.Enabled,
		BusinessName: dependencies.Config.Business.Name, BusinessAddress: dependencies.Config.Business.Address,
		BusinessPhone: dependencies.Config.Business.Phone,
	}, logger))
	router.Post("/calendar/appointments/{appointmentID}/assign", assignAppointmentPage(service, page, csrfCookie, dependencies.Notifications != nil, logger))
	if dependencies.Notifications != nil {
		router.Post("/calendar/appointments/{appointmentID}/confirmation/reissue", confirmationAdminPageAction(dependencies.Notifications, false, logger))
		router.Post("/calendar/appointments/{appointmentID}/confirmation/reset", confirmationAdminPageAction(dependencies.Notifications, true, logger))
	}
	router.Get("/api/v1/calendar", calendarEvents(service, logger))
	router.Get("/api/v1/calendar/conflicts", appointmentConflicts(service, logger))
	router.Post("/api/v1/calendar/plan", planFromWaitlist(service, page, csrfCookie, logger, true))
	router.Route("/api/v1/appointments/{appointmentID}", func(appointmentRouter chi.Router) {
		appointmentRouter.Get("/", appointmentDetail(service, dependencies.Notifications, appointmentDetailOptions{
			MailEnabled: dependencies.Config.Mail.Enabled, SMSEnabled: dependencies.Config.SMS.Enabled,
			BusinessName: dependencies.Config.Business.Name, BusinessAddress: dependencies.Config.Business.Address,
			BusinessPhone: dependencies.Config.Business.Phone, BusinessOpen: dependencies.Config.Planning.BusinessOpen,
			BusinessClose: dependencies.Config.Planning.BusinessClose,
		}, logger))
		appointmentRouter.Post("/preview", appointmentMutationPreview(service, dependencies.Config.Mail.Enabled, dependencies.Config.SMS.Enabled, dependencies.Config.Planning.BusinessOpen, dependencies.Config.Planning.BusinessClose, logger))
		appointmentRouter.Post("/assign", assignAppointment(service, logger))
		appointmentRouter.Post("/propose", proposeAppointment(service, logger))
		appointmentRouter.Post("/move", moveAppointment(service, logger, false))
		appointmentRouter.Post("/resize", moveAppointment(service, logger, true))
		appointmentRouter.Get("/alternatives", appointmentAlternatives(service, logger))
		appointmentRouter.Get("/swap-candidates", appointmentSwapCandidates(service, logger))
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

type appointmentDetailOptions struct {
	MailEnabled, SMSEnabled                      bool
	BusinessName, BusinessAddress, BusinessPhone string
	BusinessOpen, BusinessClose                  string
}

func appointmentSwapCandidates(service *appointment.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		location, err := time.LoadLocation("Europe/Vienna")
		if err != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_swap_candidates_rejected")
			return
		}
		date, err := time.ParseInLocation(time.DateOnly, request.URL.Query().Get("date"), location)
		if err != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_swap_candidates_rejected")
			return
		}
		events, err := service.SwapCandidates(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"), date.UTC(), date.AddDate(0, 0, 1).UTC())
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_swap_candidates_rejected")
			return
		}
		result := make([]map[string]any, 0, len(events))
		for _, event := range events {
			result = append(result, map[string]any{
				"id": event.ID, "title": event.CustomerName + " · " + event.JobNumber,
				"start": event.StartsAt, "end": event.EndsAt, "version": event.Version,
			})
		}
		writeJSON(response, http.StatusOK, map[string]any{"candidates": result})
	}
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

func appointmentDetail(service *appointment.Service, notifications *notification.AdminService, options appointmentDetailOptions, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		value, err := service.AppointmentDetail(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"))
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_detail_rejected")
			return
		}
		channels := notificationChannels(value, options.MailEnabled, options.SMSEnabled)
		assessment := notification.AssessChannels(value.NotificationPreference, value.Email, value.Phone, options.MailEnabled, options.SMSEnabled)
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
		payload := map[string]any{
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
			"can_assign":                 session.Actor.Role == auth.RoleAdmin && value.Lifecycle.Editable(),
			"can_reschedule":             session.Actor.Role == auth.RoleAdmin && value.Lifecycle.Editable(),
			"can_swap":                   session.Actor.Role == auth.RoleAdmin && (value.Lifecycle == appointment.LifecycleDraft || value.Lifecycle == appointment.LifecycleProposal),
			"can_reissue":                session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleFixed,
			"can_reset_confirmation":     session.Actor.Role == auth.RoleAdmin && value.Lifecycle == appointment.LifecycleFixed && value.Confirmation != appointment.ConfirmationPending && value.Confirmation != appointment.ConfirmationNotRequested,
			"working_minutes":            value.EstimatedHackMinutes, "transport_minutes": value.EstimatedTransportMinutes,
			"buffer_before_minutes": value.BufferBeforeMinutes, "buffer_after_minutes": value.BufferAfterMinutes,
		}
		if session.Actor.Role == auth.RoleAdmin {
			location, locationErr := time.LoadLocation("Europe/Vienna")
			if locationErr != nil {
				appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preview_rejected")
				return
			}
			preview, previewErr := notification.AppointmentPreview(notification.TemplateInput{
				CustomerName: value.CustomerName, JobType: value.JobType, VolumeM3: value.VolumeM3,
				StartsAt: value.StartsAt, EndsAt: value.EndsAt, BusinessName: options.BusinessName,
				BusinessAddress: options.BusinessAddress, BusinessPhone: options.BusinessPhone,
			}, location)
			if previewErr != nil {
				appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preview_rejected")
				return
			}
			payload["message_preview"] = preview
		}
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusOK, payload)
	}
}

func appointmentMutationPreview(service *appointment.Service, mailEnabled, smsEnabled bool, businessOpen, businessClose string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preflight_rejected")
			return
		}
		input := appointment.PreflightInput{
			AppointmentID: chi.URLParam(request, "appointmentID"), Action: request.Form.Get("action"), ExpectedVersion: version,
		}
		if input.Action != "assign" && input.Action != "move" && input.Action != "resize" && input.Action != "fix" {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preflight_rejected")
			return
		}
		if startsAt := request.Form.Get("starts_at"); startsAt != "" {
			input.StartsAt, err = time.Parse(time.RFC3339, startsAt)
			if err == nil {
				input.EndsAt, err = time.Parse(time.RFC3339, request.Form.Get("ends_at"))
			}
			input.StartsAt, input.EndsAt = input.StartsAt.UTC(), input.EndsAt.UTC()
		}
		if input.Action == "assign" {
			assignments := planningAssignments(request)
			input.Assignments = &assignments
		}
		if err != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preflight_rejected")
			return
		}
		preview, err := service.PreviewMutation(request.Context(), session.Actor, input)
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_preflight_rejected")
			return
		}
		detail, err := service.AppointmentDetail(request.Context(), session.Actor, input.AppointmentID)
		if err != nil {
			appointmentAPIError(response, request, logger, err, "appointment_preflight_rejected")
			return
		}
		assessment := notification.AssessChannels(detail.NotificationPreference, detail.Email, detail.Phone, mailEnabled, smsEnabled)
		notificationPassed := len(assessment.Targets) > 0 || strings.TrimSpace(request.Form.Get("without_notification_reason")) != ""
		preview.Checks = append(preview.Checks, appointment.PreflightCheck{
			Key: "notification", Label: "Benachrichtigung", Passed: notificationPassed,
			Detail: firstNonEmpty(assessment.Warning, "Kanal und maskiertes Ziel sind verfügbar."),
		})
		location, locationErr := time.LoadLocation("Europe/Vienna")
		openAt, openErr := time.Parse("15:04", businessOpen)
		closeAt, closeErr := time.Parse("15:04", businessClose)
		if locationErr != nil || openErr != nil || closeErr != nil {
			appointmentAPIError(response, request, logger, appointment.ErrValidation, "appointment_preflight_rejected")
			return
		}
		localStart, localEnd := preview.ProposedStartsAt.In(location), preview.ProposedEndsAt.In(location)
		insideOperatingDay := localStart.Year() == localEnd.Year() && localStart.YearDay() == localEnd.YearDay() &&
			localStart.Hour()*60+localStart.Minute() >= openAt.Hour()*60+openAt.Minute() &&
			localEnd.Hour()*60+localEnd.Minute() <= closeAt.Hour()*60+closeAt.Minute()
		preview.Checks = append(preview.Checks, appointment.PreflightCheck{
			Key: "overtime", Label: "Überstundenrisiko", Passed: insideOperatingDay,
			Detail: "Hinweis aus konfigurierter Betriebszeit; keine arbeitsrechtliche Freigabe.",
		})
		writeJSON(response, http.StatusOK, preview)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appointmentDetailPage(service *appointment.Service, page templates.PageData, csrfCookie string, confirmationsEnabled bool, detailOptions appointmentDetailOptions, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		session, _ := sessionFromContext(request.Context())
		detail, err := service.AppointmentDetail(request.Context(), session.Actor, chi.URLParam(request, "appointmentID"))
		if err != nil {
			calendarPlanPageError(response, request, page, logger, err)
			return
		}
		options, err := service.PlanningOptions(request.Context(), session.Actor)
		if err != nil {
			calendarPlanPageError(response, request, page, logger, err)
			return
		}
		shellData := shell(request, page, csrfCookie)
		var messagePreview *notification.TemplatePreview
		location, locationErr := time.LoadLocation("Europe/Vienna")
		if session.Actor.Role == auth.RoleAdmin && locationErr == nil {
			preview, previewErr := notification.AppointmentPreview(notification.TemplateInput{
				CustomerName: detail.CustomerName, JobType: detail.JobType, VolumeM3: detail.VolumeM3,
				StartsAt: detail.StartsAt, EndsAt: detail.EndsAt, BusinessName: detailOptions.BusinessName,
				BusinessAddress: detailOptions.BusinessAddress, BusinessPhone: detailOptions.BusinessPhone,
			}, location)
			if previewErr == nil {
				messagePreview = &preview
			}
		}
		notice := ""
		if request.URL.Query().Get("assigned") == "1" {
			notice = "Fahrer und Ressourcen wurden gespeichert."
		}
		switch request.URL.Query().Get("confirmation_action") {
		case "reissued":
			notice = "Ein neuer Bestätigungslink wurde für den Versand vorgemerkt."
		case "reset":
			notice = "Die Kundenantwort wurde einschließlich ihrer Notiz zurückgesetzt."
		}
		actionError := ""
		switch request.URL.Query().Get("confirmation_error") {
		case "forbidden":
			actionError = "Für diese Bestätigungsaktion fehlt die Berechtigung."
		case "conflict":
			actionError = "Der Terminstand hat sich geändert oder die Aktion ist nicht mehr möglich. Bitte prüfen Sie den aktuellen Stand."
		case "invalid":
			actionError = "Bitte geben Sie einen nachvollziehbaren Grund an."
		}
		render(response, request, templates.AppointmentDetailPage(templates.AppointmentDetailData{
			Shell: shellData, Options: options, Detail: detail,
			MessagePreview: messagePreview,
			Values:         appointmentAssignmentValues(detail, shellData.CSRFToken), Notice: notice, ConfirmationActionError: actionError,
			CanReissueConfirmation: confirmationsEnabled && detail.Lifecycle == appointment.LifecycleFixed,
			CanResetConfirmation:   confirmationsEnabled && detail.Lifecycle == appointment.LifecycleFixed && detail.Confirmation != appointment.ConfirmationPending && detail.Confirmation != appointment.ConfirmationNotRequested,
		}), http.StatusOK, logger)
	}
}

func confirmationAdminPageAction(service *notification.AdminService, reset bool, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		appointmentID := chi.URLParam(request, "appointmentID")
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			if reset {
				err = service.ResetResponse(request.Context(), session.Actor, appointmentID, version, request.Form.Get("reason"), middleware.GetReqID(request.Context()))
			} else {
				err = service.Reissue(request.Context(), session.Actor, appointmentID, version, request.Form.Get("reason"), middleware.GetReqID(request.Context()))
			}
		}
		values := url.Values{}
		if err == nil {
			if reset {
				values.Set("confirmation_action", "reset")
			} else {
				values.Set("confirmation_action", "reissued")
			}
		} else {
			code := "invalid"
			if errors.Is(err, auth.ErrForbidden) {
				code = "forbidden"
			} else if errors.Is(err, notification.ErrAdminActionUnavailable) {
				code = "conflict"
			}
			values.Set("confirmation_error", code)
			logger.WarnContext(request.Context(), "notification admin page action rejected", slog.String("error_code", code))
		}
		http.Redirect(response, request, "/calendar/appointments/"+url.PathEscape(appointmentID)+"?"+values.Encode(), http.StatusSeeOther)
	}
}

func assignAppointmentPage(service *appointment.Service, page templates.PageData, csrfCookie string, confirmationsEnabled bool, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		session, _ := sessionFromContext(request.Context())
		appointmentID := chi.URLParam(request, "appointmentID")
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			_, err = service.AssignDriversAndResources(request.Context(), session.Actor, appointment.AssignInput{
				MutateInput: appointment.MutateInput{ID: appointmentID, ExpectedVersion: version, RequestID: middleware.GetReqID(request.Context())},
				Assignments: planningAssignments(request),
			})
		}
		if err == nil {
			http.Redirect(response, request, "/calendar/appointments/"+url.PathEscape(appointmentID)+"?assigned=1", http.StatusSeeOther)
			return
		}
		presentation := appointmentErrorPresentation(err)
		logger.WarnContext(request.Context(), "appointment assignment page rejected", slog.String("error_code", presentation.Code))
		detail, detailErr := service.AppointmentDetail(request.Context(), session.Actor, appointmentID)
		options, optionsErr := service.PlanningOptions(request.Context(), session.Actor)
		if detailErr != nil || optionsErr != nil {
			calendarPlanPageError(response, request, page, logger, err)
			return
		}
		shellData := shell(request, page, csrfCookie)
		values := templates.AppointmentAssignmentValues{
			CSRFToken: shellData.CSRFToken, Version: request.Form.Get("version"),
			DriverIDs: request.Form["driver_id"], PrimaryDriverID: request.Form.Get("primary_driver_id"),
			ChipperResourceID: request.Form.Get("chipper_resource_id"), TransportResourceID: request.Form.Get("transport_resource_id"),
			TrailerResourceID: request.Form.Get("trailer_resource_id"), OtherResourceIDs: append([]string(nil), request.Form["other_resource_id"]...),
			OverrideReason: request.Form.Get("override_reason"),
		}
		render(response, request, templates.AppointmentDetailPage(templates.AppointmentDetailData{
			Shell: shellData, Options: options, Detail: detail, Values: values,
			Error:                  templates.PlanningFormError{Message: presentation.Message},
			CanReissueConfirmation: confirmationsEnabled && detail.Lifecycle == appointment.LifecycleFixed,
			CanResetConfirmation:   confirmationsEnabled && detail.Lifecycle == appointment.LifecycleFixed && detail.Confirmation != appointment.ConfirmationPending && detail.Confirmation != appointment.ConfirmationNotRequested,
		}), presentation.Status, logger)
	}
}

func appointmentAssignmentValues(detail appointment.Detail, csrf string) templates.AppointmentAssignmentValues {
	values := templates.AppointmentAssignmentValues{CSRFToken: csrf, Version: strconv.FormatInt(int64(detail.Version), 10)}
	values.DriverIDs = make([]string, 0, len(detail.Drivers))
	for _, assigned := range detail.Drivers {
		values.DriverIDs = append(values.DriverIDs, assigned.ID)
		if assigned.Primary {
			values.PrimaryDriverID = assigned.ID
		}
	}
	for _, assigned := range detail.Resources {
		switch assigned.Purpose {
		case appointment.PurposeChipping:
			values.ChipperResourceID = assigned.ID
		case appointment.PurposeTransport:
			values.TransportResourceID = assigned.ID
		case appointment.PurposeTrailer:
			values.TrailerResourceID = assigned.ID
		case appointment.PurposeOther:
			values.OtherResourceIDs = append(values.OtherResourceIDs, assigned.ID)
		}
	}
	return values
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
			TrailerResourceID: request.Form.Get("trailer_resource_id"), OtherResourceIDs: append([]string(nil), request.Form["other_resource_id"]...), OverrideReason: request.Form.Get("override_reason"),
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
	for _, id := range request.Form["other_resource_id"] {
		if id != "" {
			resources = append(resources, appointment.ResourceAssignment{ID: id, Purpose: appointment.PurposeOther})
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
	case errors.Is(err, appointment.ErrVersionConflict):
		result = appointmentErrorView{Status: http.StatusConflict, Code: "appointment_version_conflict", Message: "Der Termin wurde zwischenzeitlich geändert. Bitte laden Sie den aktuellen Stand neu."}
	case errors.Is(err, appointment.ErrConflict):
		result = appointmentErrorView{Status: http.StatusConflict, Code: "reservation_conflict", Message: "Der Slot konnte wegen einer gleichzeitigen Änderung nicht reserviert werden. Bitte versuchen Sie die Aktion erneut; bleibt der Slot belegt, wählen Sie eine andere Belegung oder Zeit."}
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
