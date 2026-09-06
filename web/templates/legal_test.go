package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestLegalDurationUsesGermanWholeMinuteText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value time.Duration
		want  string
	}{
		{name: "nonpositive", want: "bis zum Sitzungsende"},
		{name: "one minute", value: time.Minute, want: "1 Minute"},
		{name: "whole minutes", value: 45 * time.Minute, want: "45 Minuten"},
		{name: "one hour", value: time.Hour, want: "1 Stunde"},
		{name: "whole hours", value: 8 * time.Hour, want: "8 Stunden"},
		{name: "partial minute", value: 90 * time.Second, want: "1m30s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := legalDuration(test.value); got != test.want {
				t.Fatalf("legalDuration(%s) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestSiteFooterHasOneNoJavaScriptCookieLinkAndNoBuildDiagnostics(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	page := PageData{AppName: "HackWerk", Version: "public-build-must-not-leak"}
	if err := siteFooter(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	navStart := strings.Index(body, `<nav class="site-footer__links"`)
	navEnd := strings.Index(body[navStart:], `</nav>`)
	if navStart < 0 || navEnd < 0 {
		t.Fatalf("footer legal navigation missing: %s", body)
	}
	legalNavigation := body[navStart : navStart+navEnd]
	if count := strings.Count(legalNavigation, `href="/cookies"`); count != 1 {
		t.Fatalf("footer cookie link count = %d, want 1: %s", count, legalNavigation)
	}
	if !strings.Contains(legalNavigation, `href="/cookies" data-privacy-notice-open`) {
		t.Fatalf("footer cookie control must remain a functional no-JavaScript link: %s", legalNavigation)
	}
	if strings.Contains(body, page.Version) || strings.Contains(body, "Version ") {
		t.Fatalf("public footer exposes build diagnostics: %s", body)
	}
}

func TestCookieTablesExposeMobileFactLabels(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	data := LegalData{SessionCookieName: "session", CSRFCookieName: "csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}
	if err := cookiesContent(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{
		`class="legal-table responsive-table"`,
		`<th scope="col">Name</th>`,
		`<td data-label="Name"><code>session</code></td>`,
		`<td data-label="Zweck">`,
		`<td data-label="Dauer">`,
		`<td data-label="Zugriff">`,
		`<th scope="col">Schlüssel</th>`,
		`<td data-label="Speicher">localStorage</td>`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("cookie facts do not contain %q", expected)
		}
	}
}
