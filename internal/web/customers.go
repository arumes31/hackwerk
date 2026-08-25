package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	router.Get("/customers/new", intakePage(page, csrfCookie, dependencies.Logger))
	router.Post("/customers", createIntake(service, page, csrfCookie, dependencies.Logger))
	router.Get("/customers/{customerID}", customerDetail(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/{customerID}", updateCustomer(service, dependencies.Logger))
	router.Post("/customers/{customerID}/archive", archiveCustomer(service, dependencies.Logger))
	router.Get("/customers/{customerID}/jobs/new", jobForm(service, page, csrfCookie, dependencies.Logger))
	router.Post("/customers/{customerID}/jobs", createJob(service, page, csrfCookie, dependencies.Logger))
	router.Post("/jobs/{jobID}/notes", addJobNote(service, dependencies.Logger))
	router.Post("/jobs/{jobID}", updateJob(service, dependencies.Logger))
	router.Post("/jobs/{jobID}/archive", archiveJob(service, dependencies.Logger))
	router.Get("/waitlist", waitlistPage(service, page, csrfCookie, dependencies.Logger))
	router.Post("/waitlist/{waitlistID}/priority", updateWaitlistPriority(service, dependencies.Logger))
	router.Post("/waitlist/{waitlistID}/remove", removeWaitlist(service, dependencies.Logger))
}

func updateCustomer(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		if err == nil {
			err = service.UpdateCustomer(request.Context(), session.Actor, customers.UpdateCustomerInput{
				ID: chi.URLParam(request, "customerID"), ExpectedVersion: version,
				RequestID: middleware.GetReqID(request.Context()), Customer: customerInputFromForm(request),
			})
		}
		if err != nil {
			mutationError(response, err, logger, request, "customer_update_rejected")
			return
		}
		http.Redirect(response, request, "/customers/"+chi.URLParam(request, "customerID"), http.StatusSeeOther)
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
		if detail.Customer.ArchivedAt != nil {
			render(response, request, templates.Error(page, http.StatusConflict, "Kunde archiviert", "Für einen archivierten Kunden kann kein Auftrag angelegt werden."), http.StatusConflict, logger)
			return
		}
		render(response, request, templates.JobForm(templates.JobFormData{
			Shell: shell(request, page, csrfCookie), CustomerID: detail.Customer.ID,
			CustomerName: displayCustomerName(detail.Customer), Values: defaultIntakeValues(),
		}), http.StatusOK, logger)
	}
}

func createJob(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
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
		http.Redirect(response, request, "/customers/"+chi.URLParam(request, "customerID"), http.StatusSeeOther)
	}
}

func customerList(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		renderCustomerList(response, request, service, page, csrfCookie, logger, "", 1)
	}
}

func customerSearch(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		pageNumber, err := strconv.Atoi(request.Form.Get("page"))
		if err != nil || pageNumber < 1 {
			pageNumber = 1
		}
		renderCustomerList(response, request, service, page, csrfCookie, logger, request.Form.Get("q"), pageNumber)
	}
}

func renderCustomerList(response http.ResponseWriter, request *http.Request, service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger, search string, pageNumber int) {
	response.Header().Set("Cache-Control", "no-store")
	session, _ := sessionFromContext(request.Context())
	result, err := service.ListCustomers(request.Context(), session.Actor, search, pageNumber)
	if err != nil {
		renderCustomerError(response, request, page, logger, err, "Kunden nicht verfügbar")
		return
	}
	render(response, request, templates.Customers(templates.CustomerListData{
		Shell: shell(request, page, csrfCookie), Page: result, Search: search,
	}), http.StatusOK, logger)
}

func intakePage(page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.Intake(templates.IntakeData{
			Shell: shell(request, page, csrfCookie), Values: defaultIntakeValues(),
		}), http.StatusOK, logger)
	}
}

func createIntake(service *customers.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		input, err := intakeInput(values)
		if err != nil {
			render(response, request, templates.Intake(templates.IntakeData{
				Shell: shell(request, page, csrfCookie), Values: values,
				Error: "Bitte prüfen Sie Menge, Dauer und Transportangaben.",
			}), http.StatusUnprocessableEntity, logger)
			return
		}
		session, _ := sessionFromContext(request.Context())
		created, err := service.CreateIntake(request.Context(), session.Actor, input, middleware.GetReqID(request.Context()))
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, auth.ErrForbidden) {
				status = http.StatusForbidden
			}
			render(response, request, templates.Intake(templates.IntakeData{
				Shell: shell(request, page, csrfCookie), Values: values,
				Error: "Der Auftrag konnte nicht gespeichert werden. Prüfen Sie die Pflichtfelder und Transportangaben.",
			}), status, logger)
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
		message := ""
		if request.URL.Query().Get("duplicate_warning") == "1" {
			message = "Hinweis: Es gibt ähnlich wirkende Kundenakten. Bitte prüfen Sie diese vor einer späteren Zusammenführung. Es wurde nichts automatisch verbunden."
		}
		render(response, request, templates.CustomerDetail(templates.CustomerDetailData{
			Shell: shell(request, page, csrfCookie), Detail: detail, Error: message,
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
		http.Redirect(response, request, "/customers/"+chi.URLParam(request, "customerID"), http.StatusSeeOther)
	}
}

func addJobNote(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		_, err := service.AddNote(request.Context(), session.Actor, chi.URLParam(request, "jobID"), request.Form.Get("body"), "", middleware.GetReqID(request.Context()))
		if err != nil {
			mutationError(response, err, logger, request, "job_note_rejected")
			return
		}
		customerID := request.Form.Get("customer_id")
		if !safeID(customerID) {
			http.Redirect(response, request, "/customers", http.StatusSeeOther)
			return
		}
		http.Redirect(response, request, "/customers/"+customerID, http.StatusSeeOther)
	}
}

func updateJob(service *customers.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		version, err := parseVersion(request.Form.Get("version"))
		var job customers.JobInput
		if err == nil {
			job, err = jobInput(intakeValues(request))
		}
		if err == nil {
			err = service.UpdateJob(request.Context(), session.Actor, customers.UpdateJobInput{
				ID: chi.URLParam(request, "jobID"), ExpectedVersion: version, Job: job,
				RequestID: middleware.GetReqID(request.Context()),
			})
		}
		if err != nil {
			mutationError(response, err, logger, request, "job_update_rejected")
			return
		}
		redirectCustomer(response, request)
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
		filter := customers.WaitlistFilter{
			Query: request.URL.Query().Get("q"), JobType: request.URL.Query().Get("type"),
			Region: request.URL.Query().Get("region"), Urgency: request.URL.Query().Get("urgency"),
			PreferredMonth: request.URL.Query().Get("month"), Sort: request.URL.Query().Get("sort"),
			Direction: request.URL.Query().Get("direction"), Page: queryPage(request), PageSize: 25,
		}
		filter.Normalize()
		session, _ := sessionFromContext(request.Context())
		result, err := service.ListWaitlist(request.Context(), session.Actor, filter)
		if err != nil {
			renderCustomerError(response, request, page, logger, err, "Warteliste nicht verfügbar")
			return
		}
		render(response, request, templates.Waitlist(templates.WaitlistData{
			Shell: shell(request, page, csrfCookie), Page: result, Filter: filter,
		}), http.StatusOK, logger)
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
		ExternalConfirmed: request.Form.Get("external_confirmed") == "true",
	}
}

func defaultIntakeValues() templates.IntakeValues {
	return templates.IntakeValues{Notification: "none", JobType: "chipping_only", TransportMode: "none", Urgency: "normal", Source: "phone"}
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
	return customers.JobInput{
		JobType: customers.JobType(values.JobType), VolumeM3: values.Volume, EstimatedHackMinutes: hackMinutes,
		EstimatedTransportMinutes: transportMinutes, TransportTripCount: trips,
		TransportMode: customers.TransportMode(values.TransportMode), PreferredStartDate: values.PreferredStart,
		PreferredEndDate: values.PreferredEnd, PreferenceText: values.PreferenceText,
		Urgency: customers.Urgency(values.Urgency), Region: values.Region, Source: customers.Source(values.Source),
		ExternalTransportConfirmed: values.ExternalConfirmed,
	}, nil
}

func customerInputFromValues(values templates.IntakeValues) customers.CustomerInput {
	return customers.CustomerInput{
		FirstName: values.FirstName, LastName: values.LastName, CompanyName: values.CompanyName,
		Street: values.Street, PostalCode: values.PostalCode, Locality: values.Locality, Region: values.Region,
		CountryCode: "AT", AddressFreeform: values.AddressFreeform, PhoneRaw: values.Phone,
		Email: values.Email, NotificationPreference: customers.NotificationPreference(values.Notification),
	}
}

func customerInputFromForm(request *http.Request) customers.CustomerInput {
	return customerInputFromValues(templates.IntakeValues{
		FirstName: request.Form.Get("first_name"), LastName: request.Form.Get("last_name"),
		CompanyName: request.Form.Get("company_name"), Street: request.Form.Get("street"),
		PostalCode: request.Form.Get("postal_code"), Locality: request.Form.Get("locality"),
		Region: request.Form.Get("region"), AddressFreeform: request.Form.Get("address_freeform"),
		Phone: request.Form.Get("phone"), Email: request.Form.Get("email"), Notification: request.Form.Get("notification"),
	})
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
		http.Redirect(response, request, "/customers/"+customerID, http.StatusSeeOther)
		return
	}
	http.Redirect(response, request, "/customers", http.StatusSeeOther)
}
