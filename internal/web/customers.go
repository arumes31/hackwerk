package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerCustomerRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	service := dependencies.Customers
	csrfCookie := dependencies.Config.Auth.CSRFCookieName
	router.Get("/customers", customerList(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/search", customerSearch(service, page, csrfCookie, dependencies.Logger))
	router.Get("/customers/new", intakePage(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/new/search", intakeCustomerSearch(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers", createIntake(service, page, csrfCookie, dependencies.Logger))
	router.Get("/customers/{customerID}", customerDetail(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/{customerID}", updateCustomer(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/{customerID}/archive", archiveCustomer(service, dependencies.Logger))
	router.Get("/customers/{customerID}/jobs/new", jobForm(service, page, csrfCookie, dependencies.Logger))
	router.Get("/jobs/{jobID}/duplicate", duplicateJobForm(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/{customerID}/jobs", createJob(service, page, csrfCookie, dependencies.Logger))
	router.Post("/recent/customers/{customerID}", recordRecentCustomer(service, dependencies.Logger))
	router.Post("/recent/jobs/{jobID}", recordRecentJob(service, dependencies.Logger))
	router.Post("/jobs/{jobID}/notes", addJobNote(service, dependencies.Logger))
	router.Post("/jobs/{jobID}", updateJob(service, page, csrfCookie, dependencies.Logger))
	router.Post("/jobs/{jobID}/archive", archiveJob(service, dependencies.Logger))
	router.Get("/waitlist", waitlistPage(service, page, csrfCookie, dependencies.Logger))
	router.Post("/waitlist/{waitlistID}/priority", updateWaitlistPriority(service, dependencies.Logger))
	router.Post("/waitlist/{waitlistID}/remove", removeWaitlist(service, dependencies.Logger))
	router.Post("/waitlist/filter-favorites", saveWaitlistFilterFavorite(service, dependencies.Logger))
	router.Post("/waitlist/filter-favorites/{favoriteID}/delete", deleteWaitlistFilterFavorite(service, dependencies.Logger))
}

func updateCustomer(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		fieldErrors := customerEditFormErrors(values)
		if len(fieldErrors) > 0 {
			renderCustomerEditFailure(response, request, service, page, csrfCookie, logger, customers.ErrValidation, values, fieldErrors, "")
			return
		}
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.UpdateCustomer(request.Context(), session.Actor, customers.UpdateCustomerInput{
				ID: chi.URLParam(request, "customerID"), ExpectedVersion: version,
				RequestID: middleware.GetReqID(request.Context()), Customer: customerInputFromValues(values),
			})
		}
		if err != nil {
			renderCustomerEditFailure(response, request, service, page, csrfCookie, logger, err, values, fieldErrors, "")
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(chi.URLParam(request, "customerID")), http.StatusSeeOther)
	}
}

func jobForm(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		detail, err := service.CustomerDetail(request.Context(), session.Actor, chi.URLParam(request, "customerID"))
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Kundenakte nicht verfügbar")
			return
		}
		detail.PageRequestID = middleware.GetReqID(request.Context())
		if detail.Customer.ArchivedAt != nil {
			render(response, request, templates.Error(page, http.StatusConflict, "Kunde archiviert", "Für einen archivierten Kunden kann kein Auftrag angelegt werden."), http.StatusConflict, logger)
			return
		}
		render(response, request, templates.JobForm(templates.JobFormData{
			Shell: shell(request, page, csrfCookie), CustomerID: detail.Customer.ID,
			CustomerName: displayCustomerName(detail.Customer), Values: defaultIntakeValues(),
			CustomerRegion:   detail.Customer.Region,
			CustomerLatitude: floatFormValue(detail.Customer.Latitude), CustomerLongitude: floatFormValue(detail.Customer.Longitude),
		}), http.StatusOK, logger)
	}
}

func createJob(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		fieldErrors := intakeFormErrors(values, false)
		if len(fieldErrors) > 0 {
			render(response, request, templates.JobForm(templates.JobFormData{
				Shell:        shell(request, page, csrfCookie),
				CustomerID:   chi.URLParam(request, "customerID"),
				CustomerName: "bestehenden Kunden",
				Values:       values,
				Error:        "Bitte korrigieren Sie die markierten Felder.",
				FieldErrors:  fieldErrors,
			}), http.StatusUnprocessableEntity, logger)
			return
		}
		job, err := jobInput(values)
		if err == nil {
			session, _ := sessionFromContext(request.Context())
			_, err = service.CreateJob(request.Context(), session.Actor, customers.CreateJobInput{
				CustomerID: chi.URLParam(request, "customerID"), Job: job, InitialNote: values.Note,
				RequestID: middleware.GetReqID(request.Context()),
			})
		}
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, auth.ErrForbidden) {
				status = http.StatusForbidden
			}
			render(response, request, templates.JobForm(templates.JobFormData{
				Shell: shell(request, page, csrfCookie), CustomerID: chi.URLParam(request, "customerID"),
				CustomerName: "bestehenden Kunden", Values: values,
				Error: "Der Auftrag konnte nicht gespeichert werden. Prüfen Sie Menge, Dauer und Transportangaben.",
			}), status, logger)
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(chi.URLParam(request, "customerID")), http.StatusSeeOther)
	}
}

func duplicateJobForm(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		draft, err := service.DuplicateJobDraft(request.Context(), session.Actor, chi.URLParam(request, "jobID"))
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Auftragsentwurf nicht verfügbar")
			return
		}
		detail, err := service.CustomerDetail(request.Context(), session.Actor, draft.CustomerID)
		if err != nil || detail.Customer.ArchivedAt != nil {
			renderCustomerError(response, request, page, logger, customers.ErrConflict, "Auftragsentwurf nicht verfügbar")
			return
		}
		render(response, request, templates.JobForm(templates.JobFormData{
			Shell: shell(request, page, csrfCookie), CustomerID: draft.CustomerID, CustomerName: draft.CustomerName,
			Values: jobDraftValues(draft.Job), CustomerLatitude: floatFormValue(detail.Customer.Latitude),
			CustomerLongitude: floatFormValue(detail.Customer.Longitude), CustomerRegion: detail.Customer.Region,
		}), http.StatusOK, logger)
	}
}

func customerList(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderCustomerList(response, request, service, page, csrfCookie, logger, customers.CustomerListFilter{
			Search: request.URL.Query().Get("q"), Sort: request.URL.Query().Get("sort"),
			Direction: request.URL.Query().Get("direction"), IncludeArchived: request.URL.Query().Get("archived") == "1",
			Page: queryPage(request), PageSize: 25,
		})
	}
}

func customerSearch(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		pageNumber, err := strconv.Atoi(request.Form.Get("page"))
		if err != nil || pageNumber < 1 {
			pageNumber = 1
		}
		renderCustomerList(response, request, service, page, csrfCookie, logger, customers.CustomerListFilter{
			Search: request.Form.Get("q"), Sort: request.Form.Get("sort"), Direction: request.Form.Get("direction"),
			IncludeArchived: request.Form.Get("archived") == "1", Page: pageNumber, PageSize: 25,
		})
	}
}

func renderCustomerList(response http.ResponseWriter, request *http.Request, service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, filter customers.CustomerListFilter) {
	response.Header().Set("Cache-Control", "no-store")
	session, _ := sessionFromContext(request.Context())
	filter.Normalize()
	result, err := service.ListCustomers(request.Context(), session.Actor, filter)
	if err != nil {
		renderCustomerError(response, request, page, logger, err, "Kunden nicht verfügbar")
		return
	}
	result.CustomerFilter = filter
	result.Recent, err = service.ListRecent(request.Context(), session.Actor)
	if err != nil {
		renderCustomerError(response, request, page, logger, err, "Kundenverlauf nicht verfügbar")
		return
	}
	render(response, request, templates.Customers(templates.CustomerListData{
		Shell: shell(request, page, csrfCookie), Page: result, Search: filter.Search,
	}), http.StatusOK, logger)
}

func intakePage(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderIntakePage(response, request, service, page, csrfCookie, logger, "", defaultIntakeValues(), "", nil, http.StatusOK)
	}
}

func intakeCustomerSearch(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderIntakePage(response, request, service, page, csrfCookie, logger, request.Form.Get("q"), defaultIntakeValues(), "", nil, http.StatusOK)
	}
}

func renderIntakePage(response http.ResponseWriter, request *http.Request, service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, search string, values templates.IntakeValues, formError string, fieldErrors []templates.FormFieldError, status int) {
	response.Header().Set("Cache-Control", "no-store")
	session, _ := sessionFromContext(request.Context())
	result, err := service.ListCustomers(request.Context(), session.Actor, customers.CustomerListFilter{
		Search: search, Sort: "recent", Direction: "desc", Page: 1, PageSize: 25,
	})
	if err != nil {
		renderCustomerError(response, request, page, logger, err, "Kundenauswahl nicht verfügbar")
		return
	}
	render(response, request, templates.Intake(templates.IntakeData{
		Shell: shell(request, page, csrfCookie), Customers: result, CustomerSearch: search,
		Values: values, Error: formError, FieldErrors: fieldErrors,
	}), status, logger)
}

func createIntake(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		fieldErrors := intakeFormErrors(values, true)
		if len(fieldErrors) > 0 {
			renderIntakePage(response, request, service, page, csrfCookie, logger, "", values, "Bitte korrigieren Sie die markierten Felder.", fieldErrors, http.StatusUnprocessableEntity)
			return
		}
		input, err := intakeInput(values)
		if err != nil {
			renderIntakePage(response, request, service, page, csrfCookie, logger, "", values, "Bitte prüfen Sie Menge, Dauer und Transportangaben.", nil, http.StatusUnprocessableEntity)
			return
		}
		session, _ := sessionFromContext(request.Context())
		created, err := service.CreateIntake(request.Context(), session.Actor, input, middleware.GetReqID(request.Context()))
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, auth.ErrForbidden) {
				status = http.StatusForbidden
			}
			renderIntakePage(response, request, service, page, csrfCookie, logger, "", values, "Der Auftrag konnte nicht gespeichert werden. Prüfen Sie die Pflichtfelder und Transportangaben.", nil, status)
			return
		}
		location := "/customers/" + created.CustomerID
		if len(created.Duplicates) > 0 {
			location += "?duplicate_warning=1"
		}
		http.Redirect(response, request, location, http.StatusSeeOther)
	}
}

func customerDetail(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		session, _ := sessionFromContext(request.Context())
		detail, err := service.CustomerDetail(request.Context(), session.Actor, chi.URLParam(request, "customerID"))
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Kundenakte nicht verfügbar")
			return
		}
		detail.PageRequestID = middleware.GetReqID(request.Context())
		message := ""
		if request.URL.Query().Get("duplicate_warning") == "1" {
			message = "Hinweis: Es gibt ähnlich wirkende Kundenakten. Bitte prüfen Sie diese vor einer späteren Zusammenführung. Es wurde nichts automatisch verbunden."
			detail.Duplicates, err = service.FindDuplicatesForCustomer(request.Context(), session.Actor, detail.Customer)
			if err != nil {
				renderCustomerError(response, request, page, logger, err, "Dublettenvergleich nicht verfügbar")
				return
			}
		}
		render(response, request, templates.CustomerDetail(templates.CustomerDetailData{
			Shell: shell(request, page, csrfCookie), Detail: detail, Error: message,
			CustomerValues: customerEditValues(detail.Customer), CustomerVersion: strconv.FormatInt(int64(detail.Customer.Version), 10),
		}), http.StatusOK, logger)
	}
}

func archiveCustomer(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.ArchiveCustomer(request.Context(), session.Actor, chi.URLParam(request, "customerID"), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			mutationError(response, err, logger, request, "customer_archive_rejected")
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(chi.URLParam(request, "customerID")), http.StatusSeeOther)
	}
}

func addJobNote(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		_, err := service.AddNote(request.Context(), session.Actor, chi.URLParam(request, "jobID"), request.Form.Get("body"), "", request.Form.Get("idempotency_key"), middleware.GetReqID(request.Context()))
		if err != nil {
			mutationError(response, err, logger, request, "job_note_rejected")
			return
		}
		customerID := request.Form.Get("customer_id")
		if !safeID(customerID) {
			http.Redirect(response, request, "/customers", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(customerID), http.StatusSeeOther)
	}
}

func updateJob(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		fieldErrors := intakeFormErrors(values, false)
		if len(fieldErrors) > 0 {
			renderCustomerEditFailure(response, request, service, page, csrfCookie, logger, customers.ErrValidation, values, fieldErrors, chi.URLParam(request, "jobID"))
			return
		}
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var job customers.JobInput
		if err == nil {
			job, err = jobInput(values)
		}
		if err == nil {
			err = service.UpdateJob(request.Context(), session.Actor, customers.UpdateJobInput{
				ID: chi.URLParam(request, "jobID"), ExpectedVersion: version, Job: job,
				RequestID: middleware.GetReqID(request.Context()),
			})
		}
		if err != nil {
			renderCustomerEditFailure(response, request, service, page, csrfCookie, logger, err, values, fieldErrors, chi.URLParam(request, "jobID"))
			return
		}
		redirectCustomer(response, request)
	}
}

func customerEditFormErrors(values templates.IntakeValues) []templates.FormFieldError {
	fieldErrors := make([]templates.FormFieldError, 0, 4)
	validateCustomerForm(values, func(field, label, message string) {
		fieldErrors = append(fieldErrors, templates.FormFieldError{Field: field, Label: label, Message: message})
	})
	return fieldErrors
}

func renderCustomerEditFailure(response http.ResponseWriter, request *http.Request, service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, mutationErr error, values templates.IntakeValues, fieldErrors []templates.FormFieldError, jobID string) {
	status := http.StatusUnprocessableEntity
	message := "Bitte korrigieren Sie die markierten Felder. Ihre Eingaben wurden beibehalten."
	conflict := errors.Is(mutationErr, customers.ErrConflict)
	if conflict {
		status = http.StatusConflict
		message = "Der Datensatz wurde zwischenzeitlich geändert. Ihre Eingaben bleiben zum Vergleichen sichtbar; laden Sie den aktuellen Stand neu, bevor Sie erneut speichern."
	} else if errors.Is(mutationErr, auth.ErrForbidden) {
		renderCustomerError(response, request, page, logger, mutationErr, "Änderung nicht erlaubt")
		return
	}
	logger.WarnContext(request.Context(), "customer edit rejected", slog.String("error_code", "customer_edit_rejected"), slog.Bool("version_conflict", conflict))
	session, _ := sessionFromContext(request.Context())
	customerID := chi.URLParam(request, "customerID")
	if jobID != "" {
		customerID = request.Form.Get("customer_id")
	}
	if !safeID(customerID) {
		renderCustomerError(response, request, page, logger, customers.ErrNotFound, "Kundenakte nicht verfügbar")
		return
	}
	detail, err := service.CustomerDetail(request.Context(), session.Actor, customerID)
	if err != nil {
		renderCustomerError(response, request, page, logger, err, "Kundenakte nicht verfügbar")
		return
	}
	detail.PageRequestID = middleware.GetReqID(request.Context())
	shellData := shell(request, page, csrfCookie)
	data := templates.CustomerDetailData{
		Shell: shellData, Detail: detail,
		CustomerValues: customerEditValues(detail.Customer), CustomerVersion: strconv.FormatInt(int64(detail.Customer.Version), 10),
	}
	if jobID == "" {
		data.OpenCustomerEdit = true
		data.CustomerValues = values
		data.CustomerVersion = request.Form.Get("version")
		data.CustomerEditError = message
		data.CustomerFieldErrors = fieldErrors
		data.CustomerConflict = conflict
	} else {
		data.JobEditID = jobID
		data.JobValues = values
		data.JobVersion = request.Form.Get("version")
		data.JobEditError = message
		data.JobFieldErrors = fieldErrors
		data.JobConflict = conflict
	}
	render(response, request, templates.CustomerDetail(data), status, logger)
}

func customerEditValues(customer customers.Customer) templates.IntakeValues {
	return templates.IntakeValues{
		FirstName: customer.FirstName, LastName: customer.LastName, CompanyName: customer.CompanyName,
		Street: customer.Street, PostalCode: customer.PostalCode, Locality: customer.Locality, Region: customer.Region,
		AddressFreeform: customer.AddressFreeform, Phone: customer.PhoneRaw, Email: customer.Email,
		Notification: string(customer.NotificationPreference),
	}
}

func archiveJob(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.ArchiveJob(request.Context(), session.Actor, chi.URLParam(request, "jobID"), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			mutationError(response, err, logger, request, "job_archive_rejected")
			return
		}
		redirectCustomer(response, request)
	}
}

func waitlistPage(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		filter := waitlistFilterFromRequest(request)
		filter.Normalize()
		session, _ := sessionFromContext(request.Context())
		result, err := service.ListWaitlist(request.Context(), session.Actor, filter)
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Warteliste nicht verfügbar")
			return
		}
		result.Favorites, err = service.ListWaitlistFilterFavorites(request.Context(), session.Actor)
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Filterfavoriten nicht verfügbar")
			return
		}
		render(response, request, templates.Waitlist(templates.WaitlistData{
			Shell: shell(request, page, csrfCookie), Page: result, Filter: filter,
		}), http.StatusOK, logger)
	}
}

func recordRecentCustomer(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		id := chi.URLParam(request, "customerID")
		if err := service.RecordRecentCustomer(request.Context(), session.Actor, id); err != nil {
			mutationError(response, err, logger, request, "recent_customer_rejected")
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(id), http.StatusSeeOther)
	}
}

func recordRecentJob(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		jobID := chi.URLParam(request, "jobID")
		customerID, err := service.RecordRecentJob(request.Context(), session.Actor, jobID)
		if err != nil {
			mutationError(response, err, logger, request, "recent_job_rejected")
			return
		}
		http.Redirect(response, request, "/customers/"+url.PathEscape(customerID)+"#job-"+url.PathEscape(jobID), http.StatusSeeOther)
	}
}

func saveWaitlistFilterFavorite(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		filter := waitlistFilterFromRequest(request)
		if err := service.SaveWaitlistFilterFavorite(request.Context(), session.Actor, request.FormValue("name"), filter); err != nil {
			mutationError(response, err, logger, request, "waitlist_filter_favorite_rejected")
			return
		}
		http.Redirect(response, request, waitlistFilterLocation(filter), http.StatusSeeOther)
	}
}

func deleteWaitlistFilterFavorite(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := service.DeleteWaitlistFilterFavorite(request.Context(), session.Actor, chi.URLParam(request, "favoriteID")); err != nil {
			mutationError(response, err, logger, request, "waitlist_filter_favorite_delete_rejected")
			return
		}
		http.Redirect(response, request, "/waitlist", http.StatusSeeOther)
	}
}

func updateWaitlistPriority(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, versionErr := parseVersion(request.Form.Get("version"))
		priority, priorityErr := strconv.ParseInt(request.Form.Get("priority"), 10, 32)
		err := errors.Join(versionErr, priorityErr)
		if err == nil {
			err = service.UpdateWaitlistPriority(request.Context(), session.Actor, chi.URLParam(request, "waitlistID"), int32(priority), version, middleware.GetReqID(request.Context()))
		}
		if err != nil {
			mutationError(response, err, logger, request, "waitlist_priority_rejected")
			return
		}
		http.Redirect(response, request, "/waitlist", http.StatusSeeOther)
	}
}

func removeWaitlist(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.RemoveWaitlist(request.Context(), session.Actor, chi.URLParam(request, "waitlistID"), version, request.Form.Get("reason"), middleware.GetReqID(request.Context()))
		}
		if err != nil {
			mutationError(response, err, logger, request, "waitlist_remove_rejected")
			return
		}
		http.Redirect(response, request, "/waitlist", http.StatusSeeOther)
	}
}

func intakeValues(request *http.Request) templates.IntakeValues {
	return templates.IntakeValues{
		FirstName: request.Form.Get("first_name"), LastName: request.Form.Get("last_name"),
		CompanyName: request.Form.Get("company_name"), Street: request.Form.Get("street"),
		PostalCode: request.Form.Get("postal_code"), Locality: request.Form.Get("locality"),
		Region: request.Form.Get("region"), AddressFreeform: request.Form.Get("address_freeform"),
		Phone: request.Form.Get("phone"), Email: request.Form.Get("email"), Notification: request.Form.Get("notification"),
		JobType: request.Form.Get("job_type"), Volume: request.Form.Get("volume_m3"),
		HackDuration: request.Form.Get("hack_duration"), TransportDuration: request.Form.Get("transport_duration"),
		Trips: request.Form.Get("transport_trips"), TransportMode: request.Form.Get("transport_mode"),
		PreferredStart: request.Form.Get("preferred_start"), PreferredEnd: request.Form.Get("preferred_end"),
		PreferenceText: request.Form.Get("preference_text"), Urgency: request.Form.Get("urgency"),
		Source: request.Form.Get("source"), Note: request.Form.Get("note"),
		PileLatitude: request.Form.Get("pile_latitude"), PileLongitude: request.Form.Get("pile_longitude"),
		PileLocationSource: request.Form.Get("pile_location_source"),
		ExternalConfirmed:  request.Form.Get("external_confirmed") == "true",
	}
}

func defaultIntakeValues() templates.IntakeValues {
	return templates.IntakeValues{Notification: "none", JobType: "chipping_only", TransportMode: "none", Urgency: "normal", Source: "phone"}
}

func jobDraftValues(job customers.JobInput) templates.IntakeValues {
	values := templates.IntakeValues{
		JobType: string(job.JobType), Volume: job.VolumeM3, HackDuration: strconv.Itoa(job.EstimatedHackMinutes),
		TransportMode: string(job.TransportMode), PreferredStart: job.PreferredStartDate,
		PreferredEnd: job.PreferredEndDate, PreferenceText: job.PreferenceText, Urgency: string(job.Urgency),
		Region: job.Region, Source: string(job.Source), ExternalConfirmed: job.ExternalTransportConfirmed,
		PileLocationSource: string(job.PileLocationSource),
	}
	if job.EstimatedTransportMinutes > 0 {
		values.TransportDuration = strconv.Itoa(job.EstimatedTransportMinutes)
	}
	if job.TransportTripCount > 0 {
		values.Trips = strconv.Itoa(job.TransportTripCount)
	}
	values.PileLatitude = floatFormValue(job.PileLatitude)
	values.PileLongitude = floatFormValue(job.PileLongitude)
	return values
}

func waitlistFilterFromRequest(request *http.Request) customers.WaitlistFilter {
	filter := customers.WaitlistFilter{
		Query: request.FormValue("q"), JobType: request.FormValue("type"), Region: request.FormValue("region"),
		Urgency: request.FormValue("urgency"), PreferredMonth: request.FormValue("month"),
		Workflow: request.FormValue("workflow"), Sort: request.FormValue("sort"), Direction: request.FormValue("direction"),
		MissingLocation: request.FormValue("missing_location") == "1",
		DurationIssue:   request.FormValue("duration_issue") == "1", Overdue: request.FormValue("overdue") == "1",
		Unassigned: request.FormValue("unassigned") == "1", TransportPending: request.FormValue("transport_pending") == "1",
		DurationGroup: request.FormValue("duration_group"), Page: queryPage(request), PageSize: 25,
	}
	filter.Normalize()
	return filter
}

func waitlistFilterLocation(filter customers.WaitlistFilter) string {
	values := url.Values{}
	for key, value := range map[string]string{
		"type": filter.JobType, "region": filter.Region, "urgency": filter.Urgency, "month": filter.PreferredMonth,
		"workflow": filter.Workflow, "duration_group": filter.DurationGroup, "sort": filter.Sort, "direction": filter.Direction,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if filter.MissingLocation {
		values.Set("missing_location", "1")
	}
	if filter.DurationIssue {
		values.Set("duration_issue", "1")
	}
	if filter.Overdue {
		values.Set("overdue", "1")
	}
	if filter.Unassigned {
		values.Set("unassigned", "1")
	}
	if filter.TransportPending {
		values.Set("transport_pending", "1")
	}
	if encoded := values.Encode(); encoded != "" {
		return "/waitlist?" + encoded
	}
	return "/waitlist"
}

func intakeFormErrors(values templates.IntakeValues, includeCustomer bool) []templates.FormFieldError {
	fieldErrors := make([]templates.FormFieldError, 0, 8)
	add := func(field, label, message string) {
		fieldErrors = append(fieldErrors, templates.FormFieldError{Field: field, Label: label, Message: message})
	}

	if includeCustomer {
		validateCustomerForm(values, add)
	}
	validateJobForm(values, add)

	return fieldErrors
}

func validateCustomerForm(values templates.IntakeValues, add func(string, string, string)) {
	noName := strings.TrimSpace(values.FirstName) == "" &&
		strings.TrimSpace(values.LastName) == "" &&
		strings.TrimSpace(values.CompanyName) == ""
	if noName {
		add("first_name", "Kundenname", "Vorname, Nachname oder Firma angeben.")
	}
	if values.Email != "" {
		parsed, err := mail.ParseAddress(values.Email)
		invalidEmail := err != nil || parsed.Address != values.Email || strings.ContainsAny(values.Email, "\r\n")
		if invalidEmail {
			add("email", "E-Mail", "Eine gültige E-Mail-Adresse eingeben.")
		}
	}
	if values.Phone != "" && customers.NormalizePhone(values.Phone) == "" {
		add("phone", "Telefon", "Eine gültige österreichische oder internationale Telefonnummer eingeben.")
	}
	if !customers.NotificationPreference(values.Notification).Valid() {
		add("notification", "Benachrichtigung", "Einen gültigen Benachrichtigungskanal auswählen.")
	}
}

func validateJobForm(values templates.IntakeValues, add func(string, string, string)) {
	jobType := customers.JobType(values.JobType)
	transportMode := customers.TransportMode(values.TransportMode)
	if !jobType.Valid() {
		add("job_type", "Auftragstyp", "Einen gültigen Auftragstyp auswählen.")
	}
	if _, err := customers.CanonicalVolume(values.Volume); err != nil {
		add("volume_m3", "Menge", "Eine positive Menge in m³ eingeben.")
	}
	if minutes, err := customers.ParseDuration(values.HackDuration); err != nil || minutes > customers.MaxJobDurationMinutes {
		add("hack_duration", "Hackdauer", "Eine Dauer als Stunden:Minuten oder positive Gesamtminuten eingeben.")
	}
	if strings.TrimSpace(values.TransportDuration) != "" {
		if minutes, err := customers.ParseDuration(values.TransportDuration); err != nil || minutes > customers.MaxJobDurationMinutes {
			add("transport_duration", "Transportdauer", "Eine Dauer als Stunden:Minuten oder positive Gesamtminuten eingeben.")
		}
	}
	if strings.TrimSpace(values.Trips) != "" {
		trips, err := strconv.Atoi(values.Trips)
		if err != nil || trips < 0 || trips > customers.MaxTransportTrips {
			add("transport_trips", "Transportfahrten", "Eine ganze Zahl zwischen 0 und 1000 eingeben.")
		}
	}
	if !transportMode.Valid() || (jobType == customers.JobTypeChippingWithTransport && transportMode == customers.TransportNone) {
		add("transport_mode", "Transportmodus", "Für einen Transportauftrag einen Transportmodus auswählen.")
	}
	if jobType == customers.JobTypeChippingOnly &&
		(strings.TrimSpace(values.TransportDuration) != "" || strings.TrimSpace(values.Trips) != "" || transportMode != customers.TransportNone) {
		add("job_type", "Auftragstyp", "Transportangaben sind nur bei einem Transportauftrag zulässig.")
	}
	if values.ExternalConfirmed && (jobType != customers.JobTypeChippingWithTransport || transportMode != customers.TransportExternal) {
		add("external_confirmed", "Transportbestätigung", "Nur externen Transport ausdrücklich bestätigen.")
	}
	validatePreferredDates(values, add)
	if _, _, _, err := pileLocation(values); err != nil {
		add("pile_latitude", "Haufenstandort", "Gültige, vollständige Koordinaten übernehmen oder den Standort entfernen.")
	}
}

func validatePreferredDates(values templates.IntakeValues, add func(string, string, string)) {
	start, startErr := parseFormDate(values.PreferredStart)
	end, endErr := parseFormDate(values.PreferredEnd)
	if startErr != nil {
		add("preferred_start", "Frühestes Datum", "Ein gültiges Datum eingeben.")
	}
	if endErr != nil || (startErr == nil && !start.IsZero() && !end.IsZero() && end.Before(start)) {
		add("preferred_end", "Spätestes Datum", "Ein gültiges Datum wählen, das nicht vor dem frühesten Datum liegt.")
	}
}

func parseFormDate(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.DateOnly, value)
}

func intakeInput(values templates.IntakeValues) (customers.IntakeInput, error) {
	job, err := jobInput(values)
	if err != nil {
		return customers.IntakeInput{}, err
	}
	return customers.IntakeInput{Customer: customerInputFromValues(values), Job: job, InitialNote: values.Note}, nil
}

func jobInput(values templates.IntakeValues) (customers.JobInput, error) {
	hackMinutes, err := customers.ParseDuration(values.HackDuration)
	if err != nil {
		return customers.JobInput{}, err
	}
	transportMinutes := 0
	if strings.TrimSpace(values.TransportDuration) != "" {
		transportMinutes, err = customers.ParseDuration(values.TransportDuration)
		if err != nil {
			return customers.JobInput{}, err
		}
	}
	trips := 0
	if strings.TrimSpace(values.Trips) != "" {
		trips, err = strconv.Atoi(values.Trips)
		if err != nil {
			return customers.JobInput{}, err
		}
	}
	pileLatitude, pileLongitude, pileSource, err := pileLocation(values)
	if err != nil {
		return customers.JobInput{}, err
	}
	return customers.JobInput{
		JobType: customers.JobType(values.JobType), VolumeM3: values.Volume, EstimatedHackMinutes: hackMinutes,
		EstimatedTransportMinutes: transportMinutes, TransportTripCount: trips,
		TransportMode: customers.TransportMode(values.TransportMode), PreferredStartDate: values.PreferredStart,
		PreferredEndDate: values.PreferredEnd, PreferenceText: values.PreferenceText,
		Urgency: customers.Urgency(values.Urgency), Region: values.Region, Source: customers.Source(values.Source),
		ExternalTransportConfirmed: values.ExternalConfirmed,
		PileLatitude:               pileLatitude, PileLongitude: pileLongitude, PileLocationSource: pileSource,
	}, nil
}

func pileLocation(values templates.IntakeValues) (*float64, *float64, customers.PileLocationSource, error) {
	latitudeValue := strings.TrimSpace(values.PileLatitude)
	longitudeValue := strings.TrimSpace(values.PileLongitude)
	source := customers.PileLocationSource(strings.TrimSpace(values.PileLocationSource))
	if latitudeValue == "" && longitudeValue == "" && source == "" {
		return nil, nil, "", nil
	}
	if latitudeValue == "" || longitudeValue == "" || !source.Valid() {
		return nil, nil, "", customers.ErrValidation
	}
	latitude, latitudeErr := strconv.ParseFloat(strings.ReplaceAll(latitudeValue, ",", "."), 64)
	longitude, longitudeErr := strconv.ParseFloat(strings.ReplaceAll(longitudeValue, ",", "."), 64)
	if latitudeErr != nil || longitudeErr != nil {
		return nil, nil, "", customers.ErrValidation
	}
	return &latitude, &longitude, source, nil
}

func floatFormValue(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', 6, 64)
}

func customerInputFromValues(values templates.IntakeValues) customers.CustomerInput {
	return customers.CustomerInput{
		FirstName: values.FirstName, LastName: values.LastName, CompanyName: values.CompanyName,
		Street: values.Street, PostalCode: values.PostalCode, Locality: values.Locality, Region: values.Region,
		CountryCode: "AT", AddressFreeform: values.AddressFreeform, PhoneRaw: values.Phone,
		Email: values.Email, NotificationPreference: customers.NotificationPreference(values.Notification),
	}
}

func displayCustomerName(customer customers.Customer) string {
	name := strings.TrimSpace(customer.FirstName + " " + customer.LastName)
	if customer.CompanyName != "" {
		return customer.CompanyName + " · " + name
	}
	return name
}

func queryPage(request *http.Request) int {
	page, err := strconv.Atoi(request.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func mutationError(response http.ResponseWriter, err error, logger *slog.Logger, request *http.Request, code string) {
	status := http.StatusUnprocessableEntity
	message := "Die Änderung wurde abgewiesen. Bitte prüfen Sie die Eingaben."
	if errors.Is(err, auth.ErrForbidden) {
		status = http.StatusForbidden
		message = "Für diese Änderung fehlt die Berechtigung."
	} else if errors.Is(err, customers.ErrConflict) {
		status = http.StatusConflict
		message = "Der Datensatz wurde zwischenzeitlich geändert. Bitte laden Sie die Seite neu."
	}
	logger.WarnContext(request.Context(), "customer mutation rejected", slog.String("error_code", code))
	http.Error(response, message, status)
}

func renderCustomerError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error, title string) {
	status := http.StatusInternalServerError
	message := "Die Daten können derzeit nicht geladen werden."
	if errors.Is(err, customers.ErrNotFound) {
		status = http.StatusNotFound
		message = "Der Datensatz wurde nicht gefunden."
	} else if errors.Is(err, auth.ErrForbidden) {
		status = http.StatusForbidden
		message = "Für diese Ansicht fehlt die Berechtigung."
	}
	render(response, request, templates.Error(page, status, title, message), status, logger)
}

func safeID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("-0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func redirectCustomer(response http.ResponseWriter, request *http.Request) {
	customerID := request.Form.Get("customer_id")
	if safeID(customerID) {
		http.Redirect(response, request, "/customers/"+url.PathEscape(customerID), http.StatusSeeOther)
		return
	}
	http.Redirect(response, request, "/customers", http.StatusSeeOther)
}
