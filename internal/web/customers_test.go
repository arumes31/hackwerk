package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/customers"
)

const (
	testCustomerID = "10000000-0000-0000-0000-000000000001"
	testJobID      = "20000000-0000-0000-0000-000000000001"
	testWaitlistID = "30000000-0000-0000-0000-000000000001"
)

type customerHTTPStore struct {
	detail   customers.CustomerDetail
	waitlist customers.Page[customers.WaitlistItem]
	list     customers.Page[customers.CustomerSummary]

	created    customers.CreatedIntake
	input      customers.IntakeInput
	jobEdit    customers.UpdateJobInput
	listFilter customers.CustomerListFilter

	createCalls          int
	updateCustomerCalls  int
	updateJobCalls       int
	archiveCustomerCalls int
	archiveJobCalls      int
	priorityCalls        int
	removeCalls          int
	listCalls            int
	listSearch           string
	searchResults        []customers.SearchResult
	searchErr            error

	updateCustomerErr error
	updateJobErr      error
}

func (store *customerHTTPStore) FindDuplicates(context.Context, customers.CustomerInput) ([]customers.Duplicate, error) {
	return store.created.Duplicates, nil
}

func (store *customerHTTPStore) CreateIntake(_ context.Context, _ auth.Actor, input customers.IntakeInput, _ string) (customers.CreatedIntake, error) {
	store.createCalls++
	store.input = input
	return store.created, nil
}

func (store *customerHTTPStore) CreateJob(context.Context, auth.Actor, customers.CreateJobInput) (customers.CreatedIntake, error) {
	return store.created, nil
}

func (store *customerHTTPStore) UpdateJob(_ context.Context, _ auth.Actor, input customers.UpdateJobInput) error {
	store.updateJobCalls++
	store.jobEdit = input
	return store.updateJobErr
}

func (store *customerHTTPStore) ArchiveJob(context.Context, auth.Actor, string, int32, string) error {
	store.archiveJobCalls++
	return nil
}

func (store *customerHTTPStore) ListCustomers(_ context.Context, filter customers.CustomerListFilter) (customers.Page[customers.CustomerSummary], error) {
	store.listCalls++
	store.listSearch = filter.Search
	store.listFilter = filter
	if store.list.Page == 0 {
		store.list.Page = 1
		store.list.PageSize = 25
	}
	return store.list, nil
}

func (store *customerHTTPStore) CustomerDetail(context.Context, string) (customers.CustomerDetail, error) {
	return store.detail, nil
}
func (store *customerHTTPStore) DuplicateJobDraft(context.Context, string) (customers.JobDraft, error) {
	return customers.JobDraft{}, nil
}
func (store *customerHTTPStore) RecordRecentCustomer(context.Context, string, string) error {
	return nil
}
func (store *customerHTTPStore) RecordRecentJob(context.Context, string, string) (string, error) {
	return testCustomerID, nil
}
func (store *customerHTTPStore) ListRecent(context.Context, string, int) ([]customers.RecentRecord, error) {
	return nil, nil
}

func (store *customerHTTPStore) UpdateCustomer(context.Context, auth.Actor, customers.UpdateCustomerInput) error {
	store.updateCustomerCalls++
	return store.updateCustomerErr
}

func (store *customerHTTPStore) ArchiveCustomer(context.Context, auth.Actor, string, int32, string) error {
	store.archiveCustomerCalls++
	return nil
}

func (store *customerHTTPStore) ListWaitlist(context.Context, customers.WaitlistFilter) (customers.Page[customers.WaitlistItem], error) {
	return store.waitlist, nil
}
func (store *customerHTTPStore) ListWaitlistFilterFavorites(context.Context, string) ([]customers.WaitlistFilterFavorite, error) {
	return nil, nil
}
func (store *customerHTTPStore) SaveWaitlistFilterFavorite(context.Context, string, string, customers.WaitlistFilter) error {
	return nil
}
func (store *customerHTTPStore) DeleteWaitlistFilterFavorite(context.Context, string, string) error {
	return nil
}

func (store *customerHTTPStore) SearchWorkspace(context.Context, string) ([]customers.SearchResult, error) {
	return store.searchResults, store.searchErr
}

func (store *customerHTTPStore) UpdateWaitlistPriority(context.Context, auth.Actor, string, int32, string, int32, string) error {
	store.priorityCalls++
	return nil
}

func (store *customerHTTPStore) RemoveWaitlist(context.Context, auth.Actor, string, int32, string, string) error {
	store.removeCalls++
	return nil
}

func (store *customerHTTPStore) AddNote(context.Context, auth.Actor, string, string, string, string, string) (string, error) {
	return "note-id", nil
}

func TestCustomerHTTPDriverIntakeUsesCSRFAndPRG(t *testing.T) {
	store := &customerHTTPStore{created: customers.CreatedIntake{
		CustomerID: testCustomerID, JobID: testJobID, WaitlistID: testWaitlistID, JobNumber: "HA-2026-0001",
		Duplicates: []customers.Duplicate{{ID: "existing"}},
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := validCustomerHTTPForm(csrfToken)
	request := authenticatedCustomerRequest(t, http.MethodPost, "/customers", form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/customers/"+testCustomerID+"?duplicate_warning=1" {
		t.Fatalf("Location = %q", location)
	}
	if store.createCalls != 1 || store.input.Customer.LastName != "Huber" || store.input.Job.EstimatedHackMinutes != 210 {
		t.Fatalf("CreateIntake calls = %d, input = %#v", store.createCalls, store.input)
	}

	missingCSRF := validCustomerHTTPForm("")
	request = authenticatedCustomerRequest(t, http.MethodPost, "/customers", missingCSRF, sessionToken, csrfToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || store.createCalls != 1 {
		t.Fatalf("missing CSRF status = %d, create calls = %d", response.Code, store.createCalls)
	}
}

func TestWorkspaceSearchUsesAuthenticatedPostNoStoreAndBoundedResults(t *testing.T) {
	store := &customerHTTPStore{searchResults: []customers.SearchResult{{Kind: "job", ID: testJobID, ParentID: testCustomerID, Title: "HA-2026-0001", Subtitle: "Franz Huber", Href: "/customers/" + testCustomerID + "#job-" + testJobID}}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := url.Values{"csrf_token": {csrfToken}, "q": {"HA-2026"}}
	request := authenticatedCustomerRequest(t, http.MethodPost, "/search", form, sessionToken, csrfToken)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "HA-2026-0001") {
		t.Fatalf("search response = %d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if strings.Contains(response.Header().Get("Location"), "HA-2026") {
		t.Fatalf("search query leaked into redirect: %q", response.Header().Get("Location"))
	}

	invalid := authenticatedCustomerRequest(t, http.MethodPost, "/search", url.Values{"csrf_token": {csrfToken}, "q": {"x"}}, sessionToken, csrfToken)
	invalid.Header.Set("Accept", "application/json")
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusUnprocessableEntity || invalidResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid search = %d cache=%q", invalidResponse.Code, invalidResponse.Header().Get("Cache-Control"))
	}

	store.searchErr = errors.New("database unavailable")
	unavailable := authenticatedCustomerRequest(t, http.MethodPost, "/search", form, sessionToken, csrfToken)
	unavailable.Header.Set("Accept", "application/json")
	unavailableResponse := httptest.NewRecorder()
	router.ServeHTTP(unavailableResponse, unavailable)
	if unavailableResponse.Code != http.StatusServiceUnavailable || !strings.Contains(unavailableResponse.Body.String(), `"code":"search_unavailable"`) || strings.Contains(unavailableResponse.Body.String(), "database unavailable") {
		t.Fatalf("unavailable search = %d body=%q", unavailableResponse.Code, unavailableResponse.Body.String())
	}
}

func TestCustomerHTTPNewJobOffersExistingCustomerWithoutLeakingSearchInURL(t *testing.T) {
	store := &customerHTTPStore{list: customers.Page[customers.CustomerSummary]{
		Items: []customers.CustomerSummary{{
			ID: testCustomerID, FirstName: "Maria", LastName: "Maier",
			Locality: "Linz", Region: "Oberösterreich", JobCount: 2,
		}},
		Page: 1, PageSize: 25, Total: 1, TotalPages: 1,
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)

	request := authenticatedCustomerRequest(t, http.MethodGet, "/customers/new", nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("new job status = %d, body = %q", response.Code, body)
	}
	for _, expected := range []string{
		`action="/customers/new/search"`, `data-existing-customer-job`,
		`href="/customers/` + testCustomerID + `/jobs/new"`, `data-new-customer-panel`,
		`data-date-range-preset data-start-offset="7" data-day-span="7">Nächste Woche`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("new job customer picker is missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, `data-new-customer-panel open`) {
		t.Fatalf("new-customer form is open by default: %s", body)
	}
	newCustomerPosition := strings.Index(body, `data-new-customer-panel`)
	existingCustomerPosition := strings.Index(body, `data-existing-customer-job`)
	if newCustomerPosition < 0 || existingCustomerPosition < 0 || newCustomerPosition >= existingCustomerPosition {
		t.Fatalf("new-customer action must appear before existing customer rows: new=%d existing=%d", newCustomerPosition, existingCustomerPosition)
	}

	searchValue := "+43 660 123 45 67"
	form := url.Values{"csrf_token": {csrfToken}, "q": {searchValue}}
	request = authenticatedCustomerRequest(t, http.MethodPost, "/customers/new/search", form, sessionToken, csrfToken)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.listSearch != searchValue {
		t.Fatalf("customer picker search status/search = %d/%q", response.Code, store.listSearch)
	}
	if request.URL.RawQuery != "" || strings.Contains(response.Body.String(), "?q=") {
		t.Fatalf("customer picker leaked search into URL: request=%q", request.URL.String())
	}
}

func TestCustomerHTTPInvalidIntakeAssociatesAndRetainsFieldErrors(t *testing.T) {
	store := &customerHTTPStore{}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := validCustomerHTTPForm(csrfToken)
	form.Set("first_name", "")
	form.Set("last_name", "")
	form.Set("volume_m3", "0")
	form.Set("hack_duration", "ungueltig")

	request := authenticatedCustomerRequest(t, http.MethodPost, "/customers", form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity || store.createCalls != 0 {
		t.Fatalf("invalid intake status = %d, create calls = %d", response.Code, store.createCalls)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`data-error-summary`, `href="#first_name"`, `id="volume_m3"`,
		`aria-invalid="true"`, `aria-describedby="volume_m3-error"`, `value="ungueltig"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("invalid intake response is missing %q: %s", expected, body)
		}
	}
}

func TestCustomerHTTPStaleEditReturnsConflict(t *testing.T) {
	store := &customerHTTPStore{updateCustomerErr: customers.ErrConflict, detail: customers.CustomerDetail{Customer: customers.Customer{ID: testCustomerID, FirstName: "Maria", LastName: "Maier", CountryCode: "AT", NotificationPreference: customers.NotifyNone, Version: 2}}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := url.Values{
		"csrf_token": {csrfToken}, "version": {"1"}, "first_name": {"Maria"},
		"last_name": {"Maier"}, "notification": {"none"},
	}
	request := authenticatedCustomerRequest(t, http.MethodPost, "/customers/"+testCustomerID, form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusConflict || !strings.Contains(body, "zwischenzeitlich geändert") ||
		!strings.Contains(body, `value="Maria"`) || !strings.Contains(body, `href="/customers/`+testCustomerID+`"`) ||
		!strings.Contains(body, `<details class="edit-card" open`) {
		t.Fatalf("stale response = %d %q", response.Code, response.Body.String())
	}
	if store.updateCustomerCalls != 1 {
		t.Fatalf("update customer calls = %d", store.updateCustomerCalls)
	}
}

func TestCustomerHTTPEditValidationKeepsValuesAndFieldErrors(t *testing.T) {
	store := &customerHTTPStore{detail: customers.CustomerDetail{Customer: customers.Customer{ID: testCustomerID, FirstName: "Alt", LastName: "Stand", CountryCode: "AT", NotificationPreference: customers.NotifyNone, Version: 3}}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := url.Values{
		"csrf_token": {csrfToken}, "version": {"3"}, "first_name": {""}, "last_name": {""}, "company_name": {""},
		"phone": {"abc"}, "email": {"ungueltig"}, "notification": {"none"}, "locality": {"Eigener Wert"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/customers/"+testCustomerID, form, sessionToken, csrfToken))
	body := response.Body.String()
	for _, expected := range []string{`value="Eigener Wert"`, `value="ungueltig"`, `aria-invalid="true"`, `id="email-error"`, "Ihre Eingaben wurden beibehalten"} {
		if !strings.Contains(body, expected) {
			t.Errorf("edit validation missing %q in %q", expected, body)
		}
	}
	if response.Code != http.StatusUnprocessableEntity || store.updateCustomerCalls != 0 {
		t.Fatalf("edit validation status/calls=%d/%d", response.Code, store.updateCustomerCalls)
	}
}

func TestCustomerHTTPJobEditValidationAndConflictRenderStructuredForm(t *testing.T) {
	job := customers.Job{ID: testJobID, JobNumber: "HA-2026-0001", JobType: customers.JobTypeChippingOnly, VolumeM3: "80.00", EstimatedHackMinutes: 180, TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal, Source: customers.SourcePhone, WorkflowStatus: "waitlist", Version: 7}
	detail := customers.CustomerDetail{Customer: customers.Customer{ID: testCustomerID, FirstName: "Maria", LastName: "Maier", CountryCode: "AT", NotificationPreference: customers.NotifyNone, Version: 1}, Jobs: []customers.Job{job}, Notes: map[string][]customers.Note{}}

	t.Run("validation", func(t *testing.T) {
		store := &customerHTTPStore{detail: detail}
		router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
		form := validCustomerHTTPForm(csrfToken)
		form.Set("customer_id", testCustomerID)
		form.Set("version", "7")
		form.Set("volume_m3", "kaputt")
		form.Set("hack_duration", "falsch")
		form.Set("transport_duration", "30")
		form.Set("external_confirmed", "true")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/jobs/"+testJobID, form, sessionToken, csrfToken))
		body := response.Body.String()
		if response.Code != http.StatusUnprocessableEntity || store.updateJobCalls != 0 || !strings.Contains(body, `value="kaputt"`) || !strings.Contains(body, `value="falsch"`) || !strings.Contains(body, `id="job-edit-error-`+testJobID+`"`) || !strings.Contains(body, `aria-describedby="job_type-error"`) || !strings.Contains(body, `id="job_type-error"`) || !strings.Contains(body, `aria-describedby="external_confirmed-error"`) || !strings.Contains(body, `id="external_confirmed-error"`) {
			t.Fatalf("job validation status/calls/body=%d/%d/%q", response.Code, store.updateJobCalls, body)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		store := &customerHTTPStore{detail: detail, updateJobErr: customers.ErrConflict}
		router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
		form := validCustomerHTTPForm(csrfToken)
		form.Set("customer_id", testCustomerID)
		form.Set("version", "7")
		form.Set("volume_m3", "91")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/jobs/"+testJobID, form, sessionToken, csrfToken))
		body := response.Body.String()
		if response.Code != http.StatusConflict || store.updateJobCalls != 1 || !strings.Contains(body, `value="91"`) || !strings.Contains(body, "Aktuellen Stand neu laden") {
			t.Fatalf("job conflict status/calls/body=%d/%d/%q", response.Code, store.updateJobCalls, body)
		}
	})
}

func TestCustomerHTTPArchivedFlagSurvivesSortAndPagination(t *testing.T) {
	store := &customerHTTPStore{list: customers.Page[customers.CustomerSummary]{Page: 2, PageSize: 25, Total: 75, TotalPages: 3}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleAdmin, store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/customers?archived=1&sort=name&direction=asc&page=2&q=Maier", nil, sessionToken, csrfToken))
	body := response.Body.String()
	if response.Code != http.StatusOK || !store.listFilter.IncludeArchived || store.listSearch != "Maier" || !strings.Contains(body, `href="/customers?archived=1&amp;direction=desc&amp;q=Maier&amp;sort=jobs"`) || strings.Count(body, `name="archived" value="1"`) < 2 {
		t.Fatalf("archived list status/filter/body=%d/%#v/%q", response.Code, store.listFilter, body)
	}
}

func TestCustomerHTTPTemplatesEscapeAllFreeText(t *testing.T) {
	payload := `<script>alert("xss")</script>`
	store := &customerHTTPStore{detail: customers.CustomerDetail{
		Customer: customers.Customer{
			ID: testCustomerID, FirstName: payload, LastName: "Huber", Street: payload,
			CountryCode: "AT", NotificationPreference: customers.NotifyNone, GeocodingStatus: payload, Version: 1,
		},
		Jobs: []customers.Job{{
			ID: testJobID, JobNumber: "HA-2026-0001", JobType: customers.JobTypeChippingOnly,
			VolumeM3: "80.00", EstimatedHackMinutes: 180, TransportMode: customers.TransportNone,
			Urgency: customers.UrgencyNormal, Source: customers.SourcePhone, WorkflowStatus: "waitlist",
			PreferenceText: payload, Region: payload, Version: 1,
		}},
		Notes: map[string][]customers.Note{testJobID: {{
			ID: "note", JobID: testJobID, AuthorName: payload, Body: payload, CreatedAt: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC),
		}}},
		MapsURL: "https://www.google.com/maps/search/?api=1&query=Unterneukirchen",
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	request := authenticatedCustomerRequest(t, http.MethodGet, "/customers/"+testCustomerID, nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, body)
	}
	if strings.Contains(body, payload) || strings.Contains(body, "<script>") {
		t.Fatalf("response contains executable payload: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "https://www.google.com/maps/search/") {
		t.Fatalf("response does not contain escaped text or safe maps link: %s", body)
	}
}

func TestCustomerHTTPDetailProvidesSafeContactActions(t *testing.T) {
	store := &customerHTTPStore{detail: customers.CustomerDetail{
		Customer: customers.Customer{
			ID: testCustomerID, FirstName: "Maria", LastName: "Maier", PhoneRaw: "+43 660 123 45 67",
			Email: "maria.maier@example.test", CountryCode: "AT", NotificationPreference: customers.NotifyBoth,
			GeocodingStatus: "resolved", Version: 1,
		},
		MapsURL: "https://www.google.com/maps/search/?api=1&query=Unterneukirchen",
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	request := authenticatedCustomerRequest(t, http.MethodGet, "/customers/"+testCustomerID, nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, body)
	}
	for _, link := range []string{`href="tel:+436601234567"`, `href="mailto:maria.maier@example.test"`} {
		if !strings.Contains(body, link) {
			t.Fatalf("customer detail is missing safe contact link %q: %s", link, body)
		}
	}
}

func TestCustomerHTTPDetailUsesOneIndependentMapEditorPerEditableJob(t *testing.T) {
	latitude, longitude := 48.20849, 16.37208
	jobIDs := []string{
		"20000000-0000-0000-0000-000000000001",
		"20000000-0000-0000-0000-000000000002",
	}
	jobs := make([]customers.Job, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		jobs = append(jobs, customers.Job{
			ID: jobID, JobNumber: "HA-2026-0001",
			JobType: customers.JobTypeChippingOnly, VolumeM3: "80.00",
			EstimatedHackMinutes: 180, TransportMode: customers.TransportNone,
			Urgency: customers.UrgencyNormal, Source: customers.SourcePhone,
			WorkflowStatus: "waitlist", Version: 1,
			PileLatitude: &latitude, PileLongitude: &longitude,
		})
	}
	store := &customerHTTPStore{detail: customers.CustomerDetail{
		Customer: customers.Customer{
			ID: testCustomerID, FirstName: "Maria", LastName: "Maier",
			CountryCode: "AT", NotificationPreference: customers.NotifyNone,
			GeocodingStatus: "resolved", Version: 1,
		},
		Jobs: jobs,
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	request := authenticatedCustomerRequest(t, http.MethodGet, "/customers/"+testCustomerID, nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, body)
	}
	if count := strings.Count(body, "data-job-location-editor"); count != len(jobIDs) {
		t.Fatalf("map editor count = %d, want %d", count, len(jobIDs))
	}
	if count := strings.Count(body, "data-map-preview"); count != 0 {
		t.Fatalf("editable jobs render %d redundant map previews, want none", count)
	}
	if strings.Contains(body, `role="application"`) {
		t.Fatal("location map must not claim application semantics without keyboard map controls")
	}
	if count := strings.Count(body, `data-map-canvas role="region"`); count != len(jobIDs) {
		t.Fatalf("accessible location map region count = %d, want %d", count, len(jobIDs))
	}
	for _, jobID := range jobIDs {
		for _, id := range []string{
			"edit-" + jobID + "-pile-map",
			"edit-" + jobID + "-pile-map-heading",
			"edit-" + jobID + "-pile-map-hint",
			"edit-" + jobID + "-pile-latitude",
		} {
			if count := strings.Count(body, `id="`+id+`"`); count != 1 {
				t.Fatalf("id %q occurs %d times, want once", id, count)
			}
		}
	}
}

func TestCustomerHTTPDriverCannotCallAdminMutations(t *testing.T) {
	store := &customerHTTPStore{}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	tests := []struct {
		name string
		path string
		form url.Values
	}{
		{name: "archive customer", path: "/customers/" + testCustomerID + "/archive", form: url.Values{"version": {"1"}}},
		{name: "archive job", path: "/jobs/" + testJobID + "/archive", form: url.Values{"version": {"1"}, "customer_id": {testCustomerID}}},
		{name: "change priority", path: "/waitlist/" + testWaitlistID + "/priority", form: url.Values{"version": {"1"}, "priority": {"10"}}},
		{name: "remove waitlist", path: "/waitlist/" + testWaitlistID + "/remove", form: url.Values{"version": {"1"}, "reason": {"other"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.form.Set("csrf_token", csrfToken)
			request := authenticatedCustomerRequest(t, http.MethodPost, test.path, test.form, sessionToken, csrfToken)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
		})
	}
	if store.archiveCustomerCalls != 0 || store.archiveJobCalls != 0 || store.priorityCalls != 0 || store.removeCalls != 0 {
		t.Fatalf("admin store calls = archive customer %d, archive job %d, priority %d, remove %d",
			store.archiveCustomerCalls, store.archiveJobCalls, store.priorityCalls, store.removeCalls)
	}
}

func TestCustomerHTTPDriverWaitlistIsNotCachedAndHidesAdminPlanningControls(t *testing.T) {
	store := &customerHTTPStore{waitlist: customers.Page[customers.WaitlistItem]{
		Page: 1, PageSize: 25, Total: 1, TotalPages: 1,
		Items: []customers.WaitlistItem{{
			WaitlistID: testWaitlistID, JobID: testJobID, CustomerID: testCustomerID,
			JobNumber: "HW-2026-000001", FirstName: "Franz", LastName: "Huber",
			VolumeM3: "80.00", EstimatedHackMinutes: 180, JobType: customers.JobTypeChippingOnly,
			TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal,
		}},
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	request := authenticatedCustomerRequest(t, http.MethodGet, "/waitlist", nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("waitlist status = %d, cache-control = %q", response.Code, response.Header().Get("Cache-Control"))
	}
	body := response.Body.String()
	if !strings.Contains(body, `class="compact-table responsive-table waitlist-table"`) || !strings.Contains(body, `Auftrag bearbeiten`) {
		t.Fatalf("driver waitlist misses compact table or existing-job action: %s", body)
	}
	if strings.Contains(body, ">Einplanen<") || strings.Contains(body, "data-drag-source") || strings.Contains(body, "/priority") || strings.Contains(body, "/remove") {
		t.Fatalf("driver waitlist contains admin-only planning controls: %s", response.Body.String())
	}
}

func TestCustomerHTTPDetailMakesScheduledJobsEditableWithoutChangingAppointment(t *testing.T) {
	store := &customerHTTPStore{detail: customers.CustomerDetail{
		Customer: customers.Customer{ID: testCustomerID, FirstName: "Maria", LastName: "Maier", CountryCode: "AT", NotificationPreference: customers.NotifyNone, Version: 1},
		Jobs:     []customers.Job{{ID: testJobID, JobNumber: "HW-2026-0042", JobType: customers.JobTypeChippingOnly, VolumeM3: "80.00", EstimatedHackMinutes: 180, TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal, Source: customers.SourcePhone, WorkflowStatus: "scheduled", Version: 2}},
	}}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleAdmin, store)
	request := authenticatedCustomerRequest(t, http.MethodGet, "/customers/"+testCustomerID, nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "Termin bleibt unverändert") || !strings.Contains(body, "Öffnen &amp; bearbeiten") {
		t.Fatalf("scheduled job is not clearly editable: %d %s", response.Code, body)
	}
	if !strings.Contains(body, `action="/jobs/`+testJobID+`"`) || strings.Contains(body, `action="/jobs/`+testJobID+`/archive"`) || !strings.Contains(body, `data-job-location-editor`) {
		t.Fatalf("scheduled job must expose all edit fields including location, but no archive action: %s", body)
	}
}

func TestCustomerHTTPUpdatesScheduledJobIncludingPileLocation(t *testing.T) {
	store := &customerHTTPStore{}
	router, sessionToken, csrfToken := customerTestRouter(t, auth.RoleDriver, store)
	form := validCustomerHTTPForm(csrfToken)
	form.Set("customer_id", testCustomerID)
	form.Set("version", "7")
	form.Set("job_type", "chipping_with_transport")
	form.Set("transport_mode", "external")
	form.Set("transport_duration", "1:15")
	form.Set("transport_trips", "3")
	form.Set("external_confirmed", "true")
	form.Set("pile_latitude", "46.712345")
	form.Set("pile_longitude", "15.567890")
	form.Set("pile_location_source", "map_pin")
	request := authenticatedCustomerRequest(t, http.MethodPost, "/jobs/"+testJobID, form, sessionToken, csrfToken)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/customers/"+testCustomerID {
		t.Fatalf("status/location = %d/%q, body = %s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if store.updateJobCalls != 1 || store.jobEdit.ExpectedVersion != 7 || store.jobEdit.Job.JobType != customers.JobTypeChippingWithTransport ||
		store.jobEdit.Job.EstimatedTransportMinutes != 75 || store.jobEdit.Job.TransportTripCount != 3 || !store.jobEdit.Job.ExternalTransportConfirmed ||
		store.jobEdit.Job.PileLatitude == nil || *store.jobEdit.Job.PileLatitude != 46.712345 || store.jobEdit.Job.PileLongitude == nil || *store.jobEdit.Job.PileLongitude != 15.56789 ||
		store.jobEdit.Job.PileLocationSource != customers.PileSourceMapPin {
		t.Fatalf("scheduled job update = calls %d, input %#v", store.updateJobCalls, store.jobEdit)
	}
}

func customerTestRouter(t *testing.T, role auth.Role, customerStore *customerHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	sessionToken := "test-session-token"
	// #nosec G101 -- deterministic non-secret test fixture token.
	csrfToken := "test-csrf-token"
	identityStore := &identityTestStore{
		user: auth.User{ID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, Active: true, Version: 1},
		session: auth.Session{
			ID: "session-id", Actor: auth.Actor{UserID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, UserVersion: 1},
			CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true,
		},
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	customerService, err := customers.NewService(customerStore)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Dependencies{
		Config: configForWebTest(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Customers: customerService,
	})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}

func authenticatedCustomerRequest(t *testing.T, method string, path string, form url.Values, sessionToken string, csrfToken string) *http.Request {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequestWithContext(t.Context(), method, "https://example.test"+path, body)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "https://example.test")
	}
	// #nosec G124 -- request-only test fixtures; no cookies are emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "hackplan_session", Value: sessionToken})
	// #nosec G124 -- request-only test fixtures; no cookies are emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "hackplan_csrf", Value: csrfToken})
	return request
}

func validCustomerHTTPForm(csrfToken string) url.Values {
	return url.Values{
		"csrf_token": {csrfToken}, "first_name": {"Franz"}, "last_name": {"Huber"},
		"street": {"Unterneukirchen 15"}, "locality": {"Unterneukirchen"}, "region": {"Unterneukirchen"},
		"phone": {"0664 1234567"}, "email": {"franz.huber@example.test"}, "notification": {"none"},
		"job_type": {"chipping_only"}, "volume_m3": {"80"}, "hack_duration": {"3:30"},
		"transport_mode": {"none"}, "preference_mode": {"window"}, "urgency": {"normal"}, "source": {"phone"}, "note": {"Interne Bemerkung"},
	}
}

func TestCustomerPresentationHelpers(t *testing.T) {
	latitude, longitude := 48.234567, 14.345678
	values := jobDraftValues(customers.JobInput{
		JobType: customers.JobTypeChippingWithTransport, VolumeM3: "80", EstimatedHackMinutes: 90,
		EstimatedTransportMinutes: 45, TransportTripCount: 2, TransportMode: customers.TransportExternal,
		PreferredStartDate: "2026-09-01", PreferredEndDate: "2026-09-30", PreferenceText: "vormittags",
		Urgency: customers.UrgencyUrgent, Region: "Nord", Source: customers.SourcePhone,
		ExternalTransportConfirmed: true, PileLatitude: &latitude, PileLongitude: &longitude,
		PileLocationSource: customers.PileSourceMapPin,
	})
	if values.HackDuration != "90" || values.TransportDuration != "45" || values.Trips != "2" ||
		values.PileLatitude != "48.234567" || values.PileLongitude != "14.345678" || !values.ExternalConfirmed {
		t.Fatalf("job draft values = %#v", values)
	}
	if empty := jobDraftValues(customers.JobInput{}); empty.TransportDuration != "" || empty.Trips != "" || empty.PileLatitude != "" || empty.PileLongitude != "" {
		t.Fatalf("empty job draft values = %#v", empty)
	}

	filter := customers.WaitlistFilter{JobType: "chipping_only", Region: "Nord", Urgency: "urgent", PreferredMonth: "2026-09", Workflow: "open", Sort: "priority", Direction: "desc", MissingLocation: true, DurationIssue: true}
	location := waitlistFilterLocation(filter)
	for _, fragment := range []string{"type=chipping_only", "region=Nord", "missing_location=1", "duration_issue=1"} {
		if !strings.Contains(location, fragment) {
			t.Fatalf("filter location %q misses %q", location, fragment)
		}
	}
	if location := waitlistFilterLocation(customers.WaitlistFilter{}); location != "/waitlist" {
		t.Fatalf("empty filter location = %q", location)
	}

	if name := displayCustomerName(customers.Customer{FirstName: "Franz", LastName: "Huber"}); name != "Franz Huber" {
		t.Fatalf("personal customer name = %q", name)
	}
	if name := displayCustomerName(customers.Customer{FirstName: "Franz", LastName: "Huber", CompanyName: "Forst GmbH"}); name != "Forst GmbH · Franz Huber" {
		t.Fatalf("company customer name = %q", name)
	}
}
