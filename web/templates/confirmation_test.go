package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/notification"
)

func TestConfirmationDateTimeUsesGermanWeekdayAndViennaTime(t *testing.T) {
	t.Parallel()

	got := confirmationDateTime(time.Date(2026, time.September, 1, 6, 30, 0, 0, time.UTC))
	want := "Dienstag, 01.09.2026 · 08:30"
	if got != want {
		t.Fatalf("confirmationDateTime() = %q, want %q", got, want)
	}
}

func TestConfirmationNoteHasLiveFeedbackAndFieldRelationship(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	// #nosec G101 -- opaque request-only test values, not credentials.
	data := ConfirmationData{
		Page: PageData{AppName: "HackWerk"}, Token: "opaque-token",
		Value: notification.Confirmation{CustomerName: "Maria Muster", FormNonce: "nonce"},
	}
	if err := ConfirmationPage(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	for _, contract := range []string{
		`for="confirmation-response-note"`,
		`aria-describedby="confirmation-note-help confirmation-note-feedback"`,
		`data-confirmation-note-feedback role="status" aria-live="polite"`,
	} {
		if !strings.Contains(markup, contract) {
			t.Errorf("confirmation note markup does not preserve %q", contract)
		}
	}
}

func TestConfirmationRequiresExplicitChoiceBeforeSingleSubmit(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	// #nosec G101 -- opaque request-only test values, not credentials.
	data := ConfirmationData{
		Page: PageData{AppName: "HackWerk"}, Token: "opaque-token",
		Value: notification.Confirmation{CustomerName: "Maria Muster", FormNonce: "nonce"},
	}
	if err := ConfirmationPage(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	for _, action := range []string{"confirmed", "declined", "callback_requested"} {
		contract := `type="radio" name="action" value="` + action + `"`
		if !strings.Contains(markup, contract) {
			t.Errorf("confirmation choice does not preserve %q", contract)
		}
	}
	if got := strings.Count(markup, `type="submit"`); got != 1 {
		t.Fatalf("confirmation submit controls = %d, want 1", got)
	}
	for _, contract := range []string{
		`class="skip-link" href="#hauptinhalt"`,
		`id="hauptinhalt"`,
		`data-confirmation-submit`,
		`Antwort verbindlich speichern`,
		`href="/datenschutz"`,
		`href="/impressum"`,
	} {
		if !strings.Contains(markup, contract) {
			t.Errorf("confirmation page does not preserve %q", contract)
		}
	}
}
