package assets

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestVoiceCaptureCancelsNavigationUploadAndUsesAmbiguousDeliveryMessage(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`Der Übertragungsstatus ist unbekannt.`,
		`[401, 403, 404].includes(response.status)`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("voice capture script does not preserve %q", contract)
		}
	}
	if strings.Contains(javascript, "Audio wurde nicht dauerhaft gespeichert") {
		t.Fatal("voice capture still claims ambiguous network failures were not stored")
	}
	if !regexp.MustCompile(`(?s)window\.addEventListener\("pagehide", \(\) => \{\s*cancelled = true;`).MatchString(javascript) {
		t.Fatal("voice capture does not cancel the pending recorder upload before navigation")
	}
	if !regexp.MustCompile(`(?s)window\.addEventListener\("pagehide", \(\) => \{.*if \(recorder && recorder\.state !== "inactive"\) recorder\.stop\(\);`).MatchString(javascript) {
		t.Fatal("voice capture page-hide cleanup does not guard a missing recorder")
	}
}

func TestLoginFooterControlsOverrideDisabledPointerEvents(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/login.css")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)\.login-meta nav a,\s*\.login-meta nav button\s*\{[^}]*pointer-events:\s*auto`).Match(stylesheet) {
		t.Fatal("login footer navigation controls do not restore pointer events")
	}
}

func TestLoadPathsReturnsContentVersionedURLs(t *testing.T) {
	t.Parallel()

	paths, err := LoadPaths()
	if err != nil {
		t.Fatal(err)
	}
	versioned := regexp.MustCompile(`^/assets/[^?]+\?v=[0-9a-f]{12}$`)
	for name, assetPath := range map[string]string{
		"application CSS":        paths.CSS,
		"mobile app CSS":         paths.MobileCSS,
		"control CSS":            paths.ControlFoundationCSS,
		"application JS":         paths.JavaScript,
		"presentation bootstrap": paths.PresentationBootstrapJS,
		"route location JS":      paths.RouteLocationsJavaScript,
		"calendar CSS":           paths.FullCalendarThemeCSS,
		"calendar JS":            paths.FullCalendarJavaScript,
		"login CSS":              paths.LoginCSS,
		"login loader":           paths.LoginLoaderJavaScript,
	} {
		if !versioned.MatchString(assetPath) {
			t.Errorf("%s path = %q, want content version", name, assetPath)
		}
	}
}

func TestPresentationBootstrapAppliesSafePreferencesBeforeStyles(t *testing.T) {
	t.Parallel()

	script, err := Files.ReadFile("static/presentation-bootstrap.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	for _, contract := range []string{"hackwerk:density", "density-comfortable", "hackwerk:outdoor", "outdoor-contrast", "display-mode: standalone"} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("presentation bootstrap is missing %q", contract)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "document.cookie", "sessionStorage"} {
		if strings.Contains(javascript, forbidden) {
			t.Errorf("presentation bootstrap must not access %q", forbidden)
		}
	}
}

func TestManifestDefinesInstallableMobileEntryPoints(t *testing.T) {
	t.Parallel()

	manifest, err := Files.ReadFile("static/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	content := string(manifest)
	for _, contract := range []string{
		`"id": "/dashboard"`,
		`"start_url": "/dashboard"`,
		`"display": "standalone"`,
		`"url": "/calendar"`,
		`"url": "/customers/new"`,
		`"purpose": "any maskable"`,
	} {
		if !strings.Contains(content, contract) {
			t.Errorf("mobile application manifest is missing %q", contract)
		}
	}
}

func TestMobileAppStylesDefineIndependentShellContracts(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/mobile-app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	for name, contract := range map[string]string{
		"mobile breakpoint":            "@media (max-width: 1050px)",
		"standalone display mode":      "@media (display-mode: standalone)",
		"stable navigation":            ".mobile-bottom-nav",
		"separate primary action":      ".mobile-primary-action",
		"safe area":                    "env(safe-area-inset-bottom)",
		"context actions":              ".mobile-context-actions",
		"admin role-scoped workspaces": `body:has(.site-header[data-actor-role="admin"])`,
		"authenticated app canvas":     "body:has(.mobile-bottom-nav) .page",
		"calendar app surface":         ".calendar-page .calendar-board",
		"planning action sheet":        ".planning-page .planning-selection",
		"route field cards":            ".route-page .route-stop-card",
		"operations directory":         "[data-operation-page] .compact-list",
		"profile app cards":            ".profile-page .profile-card",
		"user directory":               ".users-page .user-card",
		"voice workflow":               ".voice-capture",
		"calendar feed management":     ".feed-layout",
		"notification mobile records":  ".table-card .responsive-table",
		"admin calendar first":         ".calendar-page .calendar-board",
		"admin saved locations first":  ".route-location-list",
		"admin notification actions":   `.notifications-page .responsive-table td[data-label="Aktion"]`,
		"admin appointment actions":    ".appointment-detail-page > .form-card:first-of-type > .action-row",
		"sticky action nav clearance":  "bottom: calc(var(--mobile-nav-height) + env(safe-area-inset-bottom) + .5625rem)",
		"sticky action scroll reserve": "scroll-margin-block-end: calc(var(--mobile-nav-height) + env(safe-area-inset-bottom) + .75rem)",
		"narrow phone guard":           "@media (max-width: 360px)",
	} {
		if !strings.Contains(css, contract) {
			t.Errorf("%s contract is missing from mobile app stylesheet", name)
		}
	}
	if !regexp.MustCompile(`(?s)body:has\(\.mobile-bottom-nav\)\s*\{[^}]*overflow-x:\s*clip`).MatchString(css) {
		t.Fatal("mobile app canvas does not guard narrow viewports against horizontal page overflow")
	}
	if strings.Contains(css, ".mobile-admin-nav") {
		t.Fatal("mobile stylesheet must not define a second admin navigation")
	}
	if strings.Contains(css, ".mobile-app-bar__title") {
		t.Fatal("mobile stylesheet must not keep a second visible page-title treatment in the utility bar")
	}
	for name, contract := range map[string]string{
		"compact app bar":             `--mobile-app-bar-height: 54px`,
		"compact section rhythm":      `--mobile-section-gap: .5rem`,
		"combined dashboard commands": `.admin-tour__commands`,
		"compact search placement":    `.customer-list-toolbar__search > input`,
		"unpadded disclosure shell":   `body:has(.mobile-bottom-nav) .compact-filter-panel`,
	} {
		if !strings.Contains(css, contract) {
			t.Errorf("%s contract is missing from mobile app stylesheet", name)
		}
	}
	if !regexp.MustCompile(`(?s)\.calendar-control-group--date\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)\s+auto`).MatchString(css) {
		t.Fatal("mobile calendar date and weekend controls must use the available width instead of a three-column arrow layout")
	}
	if !regexp.MustCompile(`(?s)\.calendar-control-group--utilities\s*\{[^}]*grid-template-columns:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)`).MatchString(css) {
		t.Fatal("mobile calendar utility actions must stay in one compact three-button row")
	}
	if !regexp.MustCompile(`(?s)\.calendar-page \.calendar-toolbar-button\s*,\s*\.calendar-page \.fc \.fc-button\s*\{[^}]*min-width:\s*44px[^}]*min-height:\s*44px`).MatchString(css) {
		t.Fatal("calendar library toolbar buttons must retain 44px touch targets across the full mobile shell")
	}
	if !regexp.MustCompile(`(?s)\.customer-page \.customer-name-link\s*\{[^}]*min-width:\s*44px[^}]*min-height:\s*44px`).MatchString(css) {
		t.Fatal("customer names must remain 44px touch targets even when their labels are short")
	}
	if !regexp.MustCompile(`(?s)\.notifications-page\s+\.notification-actions\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)`).MatchString(css) {
		t.Fatal("mobile notification actions must stack as full-width controls")
	}
	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(application), "bottom: calc(var(--mobile-nav-height) + .5625rem + env(safe-area-inset-bottom))") {
		t.Fatal("base responsive rules override the mobile sticky-action clearance")
	}
}

func TestRoleSpecificTourProjectionsAreMobileOnlyAndCountdownTargetsTheVisibleSchedule(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/mobile-app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	// The quoted role values are CSS selectors, not credentials.
	//nolint:gosec
	for name, contract := range map[string]string{
		"hidden driver projection":  `.driver-tour`,
		"hidden admin projection":   `.admin-tour`,
		"driver role scope":         `[data-dashboard-role="driver"]`,
		"admin role scope":          `[data-dashboard-role="admin"]`,
		"driver chronological tour": `[data-dashboard-projection="driver-tour"]`,
		"admin chronological tour":  `[data-dashboard-projection="admin-tour"]`,
		"grouped desktop schedule":  `[data-dashboard-projection="resource-groups"]`,
		"driver route spine":        `.driver-tour__list::before`,
		"driver route tooth":        `.driver-tour__item::before`,
		"admin route spine":         `.admin-tour__list::before`,
		"admin route tooth":         `.admin-tour__item::before`,
		"desktop moss token":        `var(--moss)`,
		"desktop surface token":     `var(--surface)`,
	} {
		if !strings.Contains(css, contract) {
			t.Errorf("%s contract is missing from mobile app stylesheet", name)
		}
	}
	if !regexp.MustCompile(`(?s)\.driver-tour\s*,\s*\.admin-tour\s*\{[^}]*display:\s*none`).MatchString(css) {
		t.Fatal("both role-specific tours must be hidden by default so desktop keeps its grouped schedule")
	}
	if !regexp.MustCompile(`(?s)@media \(max-width: 1050px\).*\[data-dashboard-role="driver"\].*\[data-dashboard-projection="driver-tour"\][^{]*\{[^}]*display:\s*grid`).MatchString(css) {
		t.Fatal("driver tour must only become visible with the mobile shell")
	}
	if !regexp.MustCompile(`(?s)@media \(max-width: 1050px\).*\[data-dashboard-role="admin"\].*\[data-dashboard-projection="admin-tour"\][^{]*\{[^}]*display:\s*grid`).MatchString(css) {
		t.Fatal("admin tour must only become visible with the mobile shell")
	}
	if !regexp.MustCompile(`(?s)@media \(max-width: 1050px\).*\.dashboard-page\[data-dashboard-role="admin"\]:has\(> \.admin-tour\)\s*>\s*\.dashboard-intro\s*\{[^}]*display:\s*contents`).MatchString(css) {
		t.Fatal("admin phone dashboard must keep its accessible h1 while replacing the desktop introduction with the tour")
	}
	if !regexp.MustCompile(`(?s)\.dashboard-page\[data-dashboard-role="admin"\]:has\(> \.admin-tour\).*\.dashboard-control-bar\s*\{[^}]*display:\s*none`).MatchString(css) {
		t.Fatal("admin phone dashboard must hide desktop controls only when its mobile tour exists")
	}
	if !regexp.MustCompile(`(?s)\.admin-tour__appointment-link\s*\{[^}]*grid-column:\s*1\s*/\s*-1`).MatchString(css) {
		t.Fatal("admin appointment management must remain the full-width primary card action")
	}
	if !regexp.MustCompile(`(?s)@media print.*\.dashboard-page\[data-dashboard-role="admin"\]\s*>\s*\.dashboard-intro h1\s*\{[^}]*position:\s*static[^}]*clip:\s*auto`).MatchString(css) {
		t.Fatal("printing from a narrow viewport must restore the dashboard title hidden by the mobile projection")
	}

	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(application), ".driver-tour") || strings.Contains(string(application), ".admin-tour") {
		t.Fatal("mobile tour styling must not alter the desktop application stylesheet")
	}

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`node.getClientRects().length > 0`,
		`window.addEventListener("resize", updateDashboardCountdown)`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("dashboard countdown does not preserve visible-projection contract %q", contract)
		}
	}
}

func TestEmbeddedStylesDefineFeedbackAndOverlayContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		file      string
		selectors []string
	}{
		{
			name: "application overlays",
			file: "static/app.css",
			selectors: []string{
				".planning-progress li > a",
				".planning-adoption-intro",
				".toast-stack",
				".toast--success",
				"dialog.modal",
				".nav-menu[open] .nav-menu__panel",
				"[popover]:popover-open",
				"[data-tooltip]::after",
				"[data-route-map] .maplibregl-popup-content",
				".route-map-legend .route-map-legend__line",
				".form-alert--warning",
				"@media (forced-colors: active)",
			},
		},
		{
			name:      "login feedback",
			file:      "static/login.css",
			selectors: []string{".login-alert", ".login-alert:focus"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			contents, err := Files.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			stylesheet := string(contents)
			for _, selector := range tt.selectors {
				if !strings.Contains(stylesheet, selector) {
					t.Errorf("%s does not define %q", tt.file, selector)
				}
			}
		})
	}
}

func TestInstallPromptIsHiddenUntilBrowserOffersInstallation(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stylesheet), ":where([hidden])") || !strings.Contains(string(stylesheet), "display: none !important;") {
		t.Fatal("global hidden attribute must override component display rules")
	}

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`typeof event.prompt !== "function"`,
		`window.addEventListener("beforeinstallprompt"`,
		`window.addEventListener("appinstalled"`,
		`hideInstallPrompt();`,
		`announce("HackWerk kann auf diesem Gerät installiert werden.")`,
		`privacyNoticeVisible()`,
		`window.addEventListener("hackwerk:privacy-notice"`,
		`else offerInstallPrompt();`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("install prompt script does not preserve %q", contract)
		}
	}
	dismissStart := strings.Index(javascript, `querySelector("[data-install-dismiss]")`)
	installedStart := strings.Index(javascript, `window.addEventListener("appinstalled"`)
	if dismissStart < 0 || installedStart <= dismissStart {
		t.Fatal("install dismissal and installed-event handlers are missing")
	}
	if strings.Contains(javascript[dismissStart:installedStart], "installEvent = undefined") {
		t.Fatal("dismissing the promotional install notice also disables the explicit profile installation")
	}
}

func TestTasteRefinementsPreserveFocusAndSafeAreas(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	for name, contract := range map[string]*regexp.Regexp{
		"appointment preflight focus": regexp.MustCompile(`(?s)\.appointment-preflight:focus\s*\{[^}]*outline:\s*3px solid var\(--focus-ring\)`),
		"install prompt right inset":  regexp.MustCompile(`(?s)\.install-prompt\s*\{[^}]*padding:[^;]*max\([^;]*env\(safe-area-inset-right\)\)`),
		"install prompt left inset":   regexp.MustCompile(`(?s)\.install-prompt\s*\{[^}]*padding:[^;]*max\([^;]*env\(safe-area-inset-left\)\)`),
	} {
		if !contract.MatchString(css) {
			t.Errorf("%s contract is missing", name)
		}
	}
}

func TestAccessibilityFoundationSupportsForcedColorsAndTextSafeAccent(t *testing.T) {
	t.Parallel()

	foundation, err := Files.ReadFile("static/control-foundation.css")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)@media \(forced-colors: active\).*select\s*\{[^}]*appearance:\s*auto`).Match(foundation) {
		t.Fatal("native select affordance is not restored in forced-colors mode")
	}

	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(application)
	for _, contract := range []string{"--ochre-text:", "color: var(--ochre-text)", "100dvh"} {
		if !strings.Contains(css, contract) {
			t.Errorf("application accessibility foundation is missing %q", contract)
		}
	}
	if strings.Contains(css, ") .mobile-bottom-nav {\n    display: none;") {
		t.Fatal("workflow pages must not remove the stable mobile navigation")
	}
}

func TestLowPriorityUIContractsRemainAccessibleAndRaceSafe(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth"`,
		`if (!/^\d{7,15}$/.test(digits)) return "";`,
		`Bitte 7 bis 15 Ziffern als gültige Telefonnummer eingeben.`,
		`note.setAttribute("aria-invalid", "true")`,
		`data-confirmation-note-feedback`,
		`searchController = new AbortController()`,
		`if (sequence !== searchSequence) return;`,
		`[data-user-directory]`,
		`toLocaleLowerCase("de-AT")`,
		`event.submitter?.dataset.confirmMessage || form.dataset.confirmMessage`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("application script does not preserve %q", contract)
		}
	}

	routeLocations, err := Files.ReadFile("static/route-locations.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{`searchController = new AbortController()`, `signal: searchController.signal`, `sequence !== searchSequence`} {
		if !strings.Contains(string(routeLocations), contract) {
			t.Errorf("route location script does not preserve %q", contract)
		}
	}
}

func TestConfirmationSubmitUsesOnlyTheGlobalDuplicateGuard(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	confirmationStart := strings.Index(javascript, `document.querySelectorAll("[data-confirmation-form]")`)
	confirmationEnd := strings.Index(javascript, `document.querySelectorAll("[data-planning-results]")`)
	if confirmationStart < 0 || confirmationEnd <= confirmationStart {
		t.Fatal("confirmation submit handler boundaries are missing")
	}
	confirmationHandler := javascript[confirmationStart:confirmationEnd]
	if strings.Contains(confirmationHandler, "dataset.submitting") {
		t.Fatal("confirmation handler must not claim the shared duplicate-submit guard before the global handler runs")
	}

	for _, contract := range []string{`form.dataset.submitting === "true"`, `form.dataset.submitting = "true"`} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("global duplicate-submit handler does not preserve %q", contract)
		}
	}
}

func TestMobileMoreUsesNativeModalSheetContracts(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`[data-mobile-menu-open]`,
		`mobileMenu.showModal()`,
		`event.target === mobileMenu`,
		`mobileMenu.addEventListener("close"`,
		`mobileMenuTrigger.focus()`,
		`event.key !== "Tab"`,
		`document.activeElement === last`,
		`classList.add("mobile-menu-dialog-ready")`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("mobile More modal script is missing %q", contract)
		}
	}

	stylesheet, err := Files.ReadFile("static/mobile-app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	for _, contract := range []string{
		`.mobile-more__dialog::backdrop`,
		`env(safe-area-inset-bottom) + .25rem`,
		`calc(100dvh - var(--mobile-nav-height)`,
		`calc(var(--visual-viewport-height, 100dvh) - var(--mobile-nav-height)`,
	} {
		if !strings.Contains(css, contract) {
			t.Errorf("mobile More modal stylesheet is missing %q", contract)
		}
	}
	if strings.Contains(css, `.mobile-primary-action > span`) {
		t.Fatal("narrow-phone primary action must keep a visible text label")
	}
	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(application), `.mobile-menu-dialog-ready`) {
		t.Fatal("base application stylesheet does not progressively replace the no-JavaScript mobile More fallback")
	}
}

func TestCommandSearchProgressivelyEnhancesAndNarrowNavigationLabelsFit(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `classList.add("command-dialog-ready")`) {
		t.Fatal("command dialog does not announce successful progressive enhancement")
	}

	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(application)
	for _, contract := range []string{`html:not(.command-dialog-ready) [data-command-open]`, `.command-dialog-ready .command-search-fallback`} {
		if !strings.Contains(css, contract) {
			t.Errorf("command search fallback stylesheet is missing %q", contract)
		}
	}

	mobile, err := Files.ReadFile("static/mobile-app.css")
	if err != nil {
		t.Fatal(err)
	}
	mobileCSS := string(mobile)
	for _, contract := range []string{
		`.mobile-bottom-nav .mobile-nav-item__label`, `font-size: .66rem`, `text-overflow: clip`,
		`.command-search-fallback--mobile .command-search-fallback__panel`, `position: fixed`,
		`max(.5rem, env(safe-area-inset-left))`,
	} {
		if !strings.Contains(mobileCSS, contract) {
			t.Errorf("narrow mobile fallback contract is missing %q", contract)
		}
	}
}

func TestListSortControlsRemainVisibleAcrossBreakpoints(t *testing.T) {
	t.Parallel()

	application, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(application)
	if !regexp.MustCompile(`(?s)\.list-sort-controls\s*\{[^}]*display:\s*flex`).MatchString(css) {
		t.Fatal("desktop list sorting controls are not visible")
	}
	if regexp.MustCompile(`(?s)\.list-sort-controls\s*\{[^}]*display:\s*none`).MatchString(css) {
		t.Fatal("list sorting controls are hidden at a breakpoint")
	}

	mobile, err := Files.ReadFile("static/mobile-app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mobile), `.customer-list-toolbar > .list-sort-controls`) {
		t.Fatal("tablet mobile-app layout does not allocate a row for list sorting")
	}
}

func TestRouteReorderingMarksTheFormDirtyAndRevealsPersistenceState(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`dirtyForms.add(order)`,
		`order.dataset.routeOrderDirty = "true"`,
		`[data-route-order-save-button]`,
		`[data-route-order-save-status]`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("route reordering script is missing %q", contract)
		}
	}
}

func TestVoiceUploadUsesStableOwnerScopedIdempotencyKey(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`[data-voice-idempotency-key]`,
		`form.append("idempotency_key", pendingUploadKey)`,
		`crypto.randomUUID()`,
		`data-voice-retry`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("voice idempotency script does not preserve %q", contract)
		}
	}
}

func TestAppointmentMutationsAndCopyFallbackReportTruthfulState(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`dialog.dataset.actionPending = "true"`,
		`dialog.querySelectorAll("button, input, select, textarea")`,
		`control.disabled = true`,
		`dialog.inert = true`,
		`if (dialog.dataset.actionPending === "true") return;`,
		`event.currentTarget.dataset.actionPending === "true"`,
		`function calendarFailure(payload, status)`,
		`failure.code = payload?.error?.code`,
		`showAppointmentFailure(calendarFailure(payload, response.status))`,
		`document.execCommand?.("copy")`,
		`if (await copyText(value))`,
		`bitte mit Strg+C kopieren`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("application script does not preserve %q", contract)
		}
	}
}

func TestPlanningFiltersAndRouteEndpointsSynchronizeTheMap(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`new CustomEvent("planningfilterchange"`,
		`context.closest("[data-planning-workbench]") || context`,
		`context.querySelector("[data-route-map-count]")`,
		`const filteredCandidates = () =>`,
		`features: filteredCandidates().map`,
		`context.addEventListener("route-location-status"`,
		`renderEndpointMarkers(true)`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("route map synchronization does not preserve %q", contract)
		}
	}
}

func TestRouteLocationCardsAndFooterKeepStableLayoutContracts(t *testing.T) {
	t.Parallel()

	contents, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	stylesheet := string(contents)
	for _, contract := range []string{
		".site-footer__inner { min-height: 60px; padding-block: .5rem;",
		".site-footer__meta { display: flex;",
		".route-location-card { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr);",
		".route-location-card > .form-grid { min-width: 0; width: 100%;",
		".route-location-card > .form-grid > .route-location-native-confirm,",
		".route-location-card__actions { width: 100%;",
	} {
		if !strings.Contains(stylesheet, contract) {
			t.Errorf("application stylesheet does not preserve %q", contract)
		}
	}
}

func TestRouteMapMarkersRemainCompactPointedAndTouchSized(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(stylesheet)
	for _, contract := range []string{
		".route-map-marker::before",
		".route-map-marker::after",
		`[data-route-marker-scale="overview"]`,
		`[data-route-marker-scale="detail"]`,
	} {
		if !strings.Contains(styles, contract) {
			t.Errorf("route marker styles do not define %q", contract)
		}
	}

	script, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	for _, contract := range []string{`dataset.markerLabel`, `dataset.routeMarkerScale`, `anchor:"bottom"`} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("route marker script does not define %q", contract)
		}
	}
}

func TestWaitlistFilterActionsRemainTouchSized(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(stylesheet)
	if !strings.Contains(css, ".waitlist-filter-chips .button, .waitlist-regions .button, .waitlist-favorites .button { min-height: 44px;") {
		t.Fatal("waitlist filter and favorite actions must remain at least 44px high")
	}
}

func TestMapLibreCSPWorkerUsesSupportedConfigurationAPI(t *testing.T) {
	t.Parallel()

	script, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	if !strings.Contains(javascript, `.setWorkerUrl(`) {
		t.Fatal("application script does not configure the MapLibre CSP worker with setWorkerUrl")
	}
	if strings.Contains(javascript, `.workerUrl=`) {
		t.Fatal("application script assigns unsupported MapLibre workerUrl property")
	}
}

func TestRouteLocationSaveConfirmsValidDraftAndKeepsNativeFallback(t *testing.T) {
	t.Parallel()

	script, err := Files.ReadFile("static/route-locations.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	for _, expected := range []string{
		`editor.querySelector("[data-route-location-native-confirmed]")`,
		`const custom = editor.closest("[data-route-location-custom]") || editor;`,
		`const latitude = editor.querySelector("[data-route-location-latitude]");`,
		`if (confirmLocation()) return;`,
		`address.value = text;`,
		`data-route-location-selected-result`,
		`Adresse ausgewählt. Bitte noch eine Bezeichnung eingeben`,
		`data-route-form-feedback`,
		`Bitte mindestens einen Auftrag auswählen.`,
		`dataset.routeLocationsReady = "true"`,
		`const setActive = (choice, selectionChanged = false) => {`,
		`setActive(choice, true)`,
		`Die Bezeichnung darf höchstens 120 Zeichen lang sein.`,
	} {
		if !strings.Contains(javascript, expected) {
			t.Fatalf("route-location save script missing %q", expected)
		}
	}
	if strings.Contains(javascript, `label.value = text;`) {
		t.Fatal("address search overwrites the editable route-location label")
	}
}
