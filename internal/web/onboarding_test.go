package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

func TestOnboardingRequiresAuthentication(t *testing.T) {
	router := identityRouterForMutationTest(t, &identityTestStore{}, auth.RoleDriver)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/hilfe/erste-schritte", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated onboarding = %d, location = %q", response.Code, response.Header().Get("Location"))
	}
}

func TestOnboardingContentByRole(t *testing.T) {
	tests := []struct {
		name      string
		role      auth.Role
		want      []string
		doNotWant []string
	}{
		{
			name: "driver sees only driver guide",
			role: auth.RoleDriver,
			want: []string{
				`id="fuer-fahrer"`,
				"Gemeinsamen Kalender öffnen",
				"Kontrollieren Sie Abfahrtszeit, Startort, Endort, Reihenfolge und Kundendaten",
				"Eine Spracheingabe legt niemals automatisch einen Kunden, Auftrag oder Termin an.",
				"Fahrer können Termine ansehen, aber nicht planen, verschieben, fixieren, absagen oder neu öffnen.",
			},
			doNotWant: []string{`id="fuer-administratoren"`, `href="/admin/`, `href="/settings/route-locations"`, "Zweiten Administrator anlegen"},
		},
		{
			name: "admin sees both complete guides",
			role: auth.RoleAdmin,
			want: []string{
				`href="#fuer-administratoren"`,
				`href="#fuer-fahrer"`,
				`id="fuer-administratoren"`,
				`id="fuer-fahrer"`,
				"Zweiten Administrator anlegen",
				"Routenorte und Standards einrichten",
				"In der Routenplanung können Sie immer einen anderen Start- oder Endort bestätigen",
				"Durchgeführten Auftrag abschließen",
				`href="/settings/route-locations"`,
				`href="/admin/notifications"`,
				"Ein Planungsvorschlag ist noch kein verbindlicher Termin und fixiert sich niemals selbst.",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := identityRouterForMutationTest(t, &identityTestStore{}, test.role)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/hilfe/erste-schritte", nil)
			// #nosec G124 -- request-only fixture; no cookie is emitted to a browser.
			request.AddCookie(&http.Cookie{Name: "hackplan_session", Value: "session"})
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("onboarding status = %d; body = %q", response.Code, body)
			}
			for _, expected := range test.want {
				if !strings.Contains(body, expected) {
					t.Errorf("body does not contain %q", expected)
				}
			}
			for _, unexpected := range test.doNotWant {
				if strings.Contains(body, unexpected) {
					t.Errorf("body unexpectedly contains %q", unexpected)
				}
			}
			if !strings.Contains(body, `class="onboarding-phase"`) || !strings.Contains(body, `data-onboarding-phase`) {
				t.Errorf("onboarding guide is not grouped into semantic phases")
			}
			if strings.Contains(body, `type="checkbox"`) || strings.Contains(body, `data-onboarding-checklist`) {
				t.Errorf("onboarding unexpectedly renders a persistent checklist")
			}
		})
	}
}

func TestOnboardingIsLinkedFromDesktopAndMobileNavigation(t *testing.T) {
	router := identityRouterForMutationTest(t, &identityTestStore{}, auth.RoleAdmin)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/hilfe/erste-schritte", nil)
	// #nosec G124 -- request-only fixture; no cookie is emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "hackplan_session", Value: "session"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	want := `href="/hilfe/erste-schritte" class="nav-link nav-link--active"`
	if count := strings.Count(response.Body.String(), want); count != 3 {
		t.Fatalf("active onboarding navigation link count = %d, want desktop, modal-mobile, and no-JavaScript fallback links", count)
	}
}
