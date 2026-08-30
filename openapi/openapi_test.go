package openapi

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDocumentIsValidYAMLAndContainsCurrentContract(t *testing.T) {
	payload, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string `yaml:"openapi"`
		Info    struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(payload, &document); err != nil {
		t.Fatalf("invalid OpenAPI YAML: %v", err)
	}
	if document.OpenAPI != "3.1.0" || document.Info.Version != "0.10.0" {
		t.Fatalf("OpenAPI/version = %q/%q", document.OpenAPI, document.Info.Version)
	}
	for _, path := range []string{"/dashboard", "/search", "/login/mfa", "/login/mfa/passkey/options", "/profil/email/bestaetigen", "/profile", "/profile/details", "/profile/email", "/profile/security/passkeys/options", "/profile/sessions/{sessionID}/revoke", "/profile/sessions/revoke-all", "/admin/users/{userID}/reset-security", "/calendar/export.ics", "/api/v1/calendar-feeds", "/feeds/{calendarFeedToken}/calendar.ics", "/api/v1/geocoding/search", "/api/v1/voice/drafts", "/voice/upload", "/voice/drafts/{draftID}/retranscribe", "/api/v1/planning/suggestions", "/api/v1/planning/suggestions/{suggestionID}/adopt", "/api/v1/calendar", "/api/v1/calendar/plan", "/api/v1/appointments/{appointmentID}", "/api/v1/appointments/{appointmentID}/preview", "/api/v1/appointments/{appointmentID}/fix", "/api/v1/appointments/{appointmentID}/reopen", "/termin/{confirmationToken}", "/termin/{confirmationToken}/antwort", "/admin/notifications/{notificationID}/retry", "/admin/notifications/{notificationID}/review", "/admin/notifications/report.csv"} {
		if _, ok := document.Paths[path]; !ok {
			t.Errorf("missing path %s", path)
		}
	}
}
