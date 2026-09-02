//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/cdproto/emulation"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTask01UserDetailsBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, adminID, driverUserID, driverID, adminPassword, driverPassword := task01Application(t, databaseURL)
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{
			SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour,
		},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool,
		Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Dashboard: e2eDashboard(t, pool),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU,
		chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 120*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	var mobileLayout struct {
		Overflow      bool    `json:"overflow"`
		CardLeft      float64 `json:"cardLeft"`
		CardRight     float64 `json:"cardRight"`
		ViewportWidth float64 `json:"viewportWidth"`
		SmallTargets  int     `json:"smallTargets"`
		Grid          bool    `json:"grid"`
		InsetBorder   bool    `json:"insetBorder"`
		LegacyScene   bool    `json:"legacyScene"`
	}
	var loginMobileScreenshot []byte
	if err := chromedp.Run(
		browserContext,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-reduced-motion", Value: "no-preference"}}).Do(ctx)
		}),
		chromedp.EmulateViewport(360, 800),
		chromedp.Navigate(server.URL+"/login"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{
			const panel=document.querySelector('.login-panel');
			const card=panel.getBoundingClientRect();
			const targets=[...document.querySelectorAll('.login-page input,.login-page button')];
			return {overflow:document.documentElement.scrollWidth>window.innerWidth,
				cardLeft:card.left,cardRight:card.right,viewportWidth:window.innerWidth,
				smallTargets:targets.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<44||rect.height<44}).length,
				grid:getComputedStyle(document.body).backgroundImage.includes('linear-gradient'),
				insetBorder:getComputedStyle(panel,'::before').borderTopStyle==='solid',
				legacyScene:!!document.querySelector('.scene,#vehicles')};
		})()`, &mobileLayout),
		chromedp.FullScreenshot(&loginMobileScreenshot, 90),
	); err != nil {
		t.Fatalf("mobile login layout: %s", browserDiagnostics(browserContext, err))
	}
	cardOutsideViewport := mobileLayout.CardLeft < 0 || mobileLayout.CardRight > mobileLayout.ViewportWidth
	if mobileLayout.Overflow || cardOutsideViewport || mobileLayout.SmallTargets != 0 || !mobileLayout.Grid || !mobileLayout.InsetBorder || mobileLayout.LegacyScene {
		t.Fatalf("mobile login overflow/card/small targets/grid/inset/legacy = %v/%v/%d/%v/%v/%v: %+v",
			mobileLayout.Overflow,
			cardOutsideViewport,
			mobileLayout.SmallTargets,
			mobileLayout.Grid,
			mobileLayout.InsetBorder,
			mobileLayout.LegacyScene,
			mobileLayout,
		)
	}

	var usersDesktopScreenshot []byte
	var loginDesktopScreenshot []byte
	var installPromptFlow struct {
		HiddenStateHonored      bool `json:"hiddenStateHonored"`
		HiddenBehindPrivacy     bool `json:"hiddenBehindPrivacy"`
		ShownAfterPrivacyClosed bool `json:"shownAfterPrivacyClosed"`
		HiddenAfterUse          bool `json:"hiddenAfterUse"`
		PromptCalls             int  `json:"promptCalls"`
	}
	var desktopLogin struct {
		HasStyles          bool    `json:"hasStyles"`
		HasLegacyAsset     bool    `json:"hasLegacyAsset"`
		Grid               bool    `json:"grid"`
		PanelWidth         float64 `json:"panelWidth"`
		CenterDelta        float64 `json:"centerDelta"`
		HasBuildMeta       bool    `json:"hasBuildMeta"`
		PasswordIconOnly   bool    `json:"passwordIconOnly"`
		PasswordIconInline bool    `json:"passwordIconInline"`
		HasScrollTop       bool    `json:"hasScrollTop"`
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(1280, 900),
		chromedp.Navigate(server.URL+"/login"),
		chromedp.WaitVisible(".login-panel", chromedp.ByQuery),
		chromedp.WaitVisible(".password-reveal", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{
			const panel=document.querySelector('.login-panel').getBoundingClientRect();
			const meta=document.querySelector('.login-meta').textContent;
			const password=document.querySelector('#password').getBoundingClientRect();
			const reveal=document.querySelector('.password-reveal');
			const revealBox=reveal.getBoundingClientRect();
			return {
				hasStyles:!!document.querySelector('link[href^="/assets/login.css?v="]'),
				hasLegacyAsset:!!document.querySelector('link[href^="/assets/login-original.css?v="],script[src^="/assets/login-background-loader.js?v="],.scene,#vehicles'),
				grid:getComputedStyle(document.body).backgroundImage.includes('linear-gradient'),
				panelWidth:panel.width,
				centerDelta:Math.abs(panel.left+panel.width/2-window.innerWidth/2),
				hasBuildMeta:meta.includes('HWK-SYS // V')&&meta.includes('ID:'),
				passwordIconOnly:reveal.textContent.trim()===''&&!!reveal.querySelector('svg')&&reveal.getAttribute('aria-label')==='Passwort anzeigen',
				passwordIconInline:revealBox.left>=password.left&&revealBox.right<=password.right&&revealBox.top===password.top&&revealBox.bottom===password.bottom,
				hasScrollTop:!!document.querySelector('.scroll-top')
			};
		})()`, &desktopLogin),
		chromedp.FullScreenshot(&loginDesktopScreenshot, 90),
		chromedp.SetValue("#username", "admin-task01", chromedp.ByQuery),
		chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-admin-menu] summary", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('.scroll-top') !== null`, nil),
		chromedp.Evaluate(`(()=>{const prompt=document.querySelector('[data-install-prompt]');prompt.hidden=true;return getComputedStyle(prompt).display==='none'})()`, &installPromptFlow.HiddenStateHonored),
		chromedp.Evaluate(`(()=>{
			window.__hackwerkInstallPromptCalls=0;
			const event=new Event('beforeinstallprompt',{cancelable:true});
			event.prompt=async()=>{window.__hackwerkInstallPromptCalls+=1;};
			event.userChoice=Promise.resolve({outcome:'accepted'});
			window.dispatchEvent(event);
		})()`, nil),
		chromedp.Evaluate(`(()=>{const notice=document.querySelector('[data-privacy-notice]');const prompt=document.querySelector('[data-install-prompt]');return !notice.hidden&&prompt.hidden&&getComputedStyle(prompt).display==='none'})()`, &installPromptFlow.HiddenBehindPrivacy),
		chromedp.Click("[data-privacy-notice-dismiss]", chromedp.ByQuery),
		chromedp.Poll(`(()=>{const prompt=document.querySelector('[data-install-prompt]');return !prompt.hidden&&getComputedStyle(prompt).display!=='none'})()`, nil),
		chromedp.Evaluate(`(()=>{const prompt=document.querySelector('[data-install-prompt]');return !prompt.hidden&&getComputedStyle(prompt).display!=='none'})()`, &installPromptFlow.ShownAfterPrivacyClosed),
		chromedp.Click("[data-install-accept]", chromedp.ByQuery),
		chromedp.Poll(`(()=>{const prompt=document.querySelector('[data-install-prompt]');return prompt.hidden&&window.__hackwerkInstallPromptCalls===1})()`, nil),
		chromedp.Evaluate(`(()=>{const prompt=document.querySelector('[data-install-prompt]');return prompt.hidden&&getComputedStyle(prompt).display==='none'})()`, &installPromptFlow.HiddenAfterUse),
		chromedp.Evaluate(`window.__hackwerkInstallPromptCalls`, &installPromptFlow.PromptCalls),
		chromedp.Click("[data-admin-menu] summary", chromedp.ByQuery),
		chromedp.WaitVisible("a[href='/admin/users']", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/admin/users"),
		chromedp.WaitVisible("main.users-page", chromedp.ByQuery),
		chromedp.FullScreenshot(&usersDesktopScreenshot, 90),
	); err != nil {
		t.Fatalf("admin login: %s", browserDiagnostics(browserContext, err))
	}
	if !desktopLogin.HasStyles || desktopLogin.HasLegacyAsset || !desktopLogin.Grid || desktopLogin.PanelWidth < 400 || desktopLogin.PanelWidth > 440 || desktopLogin.CenterDelta > 2 || !desktopLogin.HasBuildMeta || !desktopLogin.PasswordIconOnly || !desktopLogin.PasswordIconInline || desktopLogin.HasScrollTop {
		t.Fatalf("desktop login field-manual layout = %+v", desktopLogin)
	}
	if !installPromptFlow.HiddenStateHonored || !installPromptFlow.HiddenBehindPrivacy || !installPromptFlow.ShownAfterPrivacyClosed || !installPromptFlow.HiddenAfterUse || installPromptFlow.PromptCalls != 1 {
		t.Fatalf("install prompt flow = %+v", installPromptFlow)
	}
	detailsForm := "form[action='/admin/users/" + driverUserID + "/details']"
	if err := runBrowserStep(browserContext, "submit user details",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(detailsForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(detailsForm+" [name='username']", "driver-task01-neu", chromedp.ByQuery),
		chromedp.SetValue(detailsForm+" [name='display_name']", "Neuer Kontoname", chromedp.ByQuery),
		chromedp.SetValue(detailsForm+" [name='email']", "konto@example.test", chromedp.ByQuery),
		chromedp.Click(detailsForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitReady(detailsForm+" [name='username'][value='driver-task01-neu']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("update user details: %s", browserDiagnostics(browserContext, err))
	}

	var username, displayName, email, driverDisplayName, driverEmail string
	if err := pool.QueryRow(t.Context(), `SELECT username::text, display_name, COALESCE(email::text, '') FROM users WHERE id = $1`, driverUserID).Scan(&username, &displayName, &email); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT display_name, COALESCE(email::text, '') FROM drivers WHERE id = $1`, driverID).Scan(&driverDisplayName, &driverEmail); err != nil {
		t.Fatal(err)
	}
	if username != "driver-task01-neu" || displayName != "Neuer Kontoname" || email != "konto@example.test" {
		t.Fatalf("updated account = %q %q %q", username, displayName, email)
	}
	if driverDisplayName != "Fahrerprofil bleibt" || driverEmail != "fahrerprofil@example.test" {
		t.Fatalf("driver profile changed = %q %q", driverDisplayName, driverEmail)
	}

	accessForm := "form[action='/admin/users/" + adminID + "/access']"
	var lastAdminError string
	if err := runBrowserStep(browserContext, "protect last admin",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(accessForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(accessForm+" select[name='role']", "driver", chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>true`, nil),
		chromedp.Click(accessForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[role='alert']", chromedp.ByQuery),
		chromedp.Text("[role='alert']", &lastAdminError, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("last-admin protection: %s", browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(lastAdminError, "Mindestens ein aktiver Administrator") {
		t.Fatalf("last-admin error = %q", lastAdminError)
	}

	var usersLayout struct {
		Overflow            bool    `json:"overflow"`
		Cards               int     `json:"cards"`
		Tables              int     `json:"tables"`
		SmallTargets        int     `json:"smallTargets"`
		HeadingSize         float64 `json:"headingSize"`
		DirectCalendarLinks int     `json:"directCalendarLinks"`
		NavigationFeedLinks int     `json:"navigationFeedLinks"`
		OpenManagementRows  int     `json:"openManagementRows"`
	}
	var usersScreenshot []byte
	if err := runBrowserStep(browserContext, "mobile user administration",
		chromedp.EmulateViewport(360, 800),
		chromedp.WaitVisible("article.user-card", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{
			const visible=node=>{const rect=node.getBoundingClientRect();const style=getComputedStyle(node);return rect.width>0&&rect.height>0&&style.visibility!=='hidden'&&style.display!=='none'};
			const targets=[...document.querySelectorAll('.users-page a,.users-page button,.users-page input:not([type="hidden"]):not([type="checkbox"]),.users-page select,.users-page summary,.users-page .check-label')].filter(visible);
			return {overflow:document.documentElement.scrollWidth>window.innerWidth,
				cards:document.querySelectorAll('article.user-card').length,
				tables:document.querySelectorAll('.users-page table').length,
				smallTargets:targets.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<44||rect.height<44}).length,
				headingSize:document.querySelector('.users-page h1').getBoundingClientRect().height,
				directCalendarLinks:document.querySelectorAll(".primary-nav > a[href='/calendar']").length,
				navigationFeedLinks:document.querySelectorAll(".site-header a[href='/calendar/feeds'],.mobile-bottom-nav a[href='/calendar/feeds']").length,
				openManagementRows:document.querySelectorAll('details.user-manage[open]').length};
		})()`, &usersLayout),
		chromedp.FullScreenshot(&usersScreenshot, 90),
		chromedp.EmulateViewport(1280, 900),
	); err != nil {
		t.Fatalf("mobile user administration: %s", browserDiagnostics(browserContext, err))
	}
	artifact := filepath.Join(t.ArtifactDir(), "task01-mobile-users.png")
	if err := os.WriteFile(artifact, usersScreenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile user administration screenshot: %s", artifact)
	loginMobileArtifact := filepath.Join(t.ArtifactDir(), "task01-mobile-login.png")
	if err := os.WriteFile(loginMobileArtifact, loginMobileScreenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile login screenshot: %s", loginMobileArtifact)
	if screenshotDir := os.Getenv("E2E_SCREENSHOT_DIR"); screenshotDir != "" {
		if err := os.MkdirAll(screenshotDir, 0o700); err != nil {
			t.Fatal(err)
		}
		persistentArtifact := filepath.Join(screenshotDir, "task01-mobile-users.png")
		if err := os.WriteFile(persistentArtifact, usersScreenshot, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(screenshotDir, "task01-desktop-users.png"), usersDesktopScreenshot, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(screenshotDir, "task01-desktop-login.png"), loginDesktopScreenshot, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(screenshotDir, "task01-mobile-login.png"), loginMobileScreenshot, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("persistent mobile screenshot: %s", persistentArtifact)
	}
	if usersLayout.Overflow || usersLayout.Cards != 2 || usersLayout.Tables != 0 || usersLayout.SmallTargets != 0 || usersLayout.HeadingSize > 150 || usersLayout.DirectCalendarLinks != 1 || usersLayout.NavigationFeedLinks != 0 || usersLayout.OpenManagementRows != 1 {
		t.Fatalf("mobile users overflow/cards/tables/small targets/heading/direct calendar/navigation feed/open = %v/%d/%d/%d/%.1f/%d/%d/%d", usersLayout.Overflow, usersLayout.Cards, usersLayout.Tables, usersLayout.SmallTargets, usersLayout.HeadingSize, usersLayout.DirectCalendarLinks, usersLayout.NavigationFeedLinks, usersLayout.OpenManagementRows)
	}

	createdPassword := randomE2EPassword(t)
	if err := runBrowserStep(browserContext, "create user",
		chromedp.SetValue("#create-username", "new-driver-task01", chromedp.ByQuery),
		chromedp.SetValue("#create-name", "Neuer Fahrerzugang", chromedp.ByQuery),
		chromedp.SetValue("#create-email", "neu@example.test", chromedp.ByQuery),
		chromedp.SetValue("#create-role", "driver", chromedp.ByQuery),
		chromedp.SetValue("#create-password", createdPassword, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("form[action='/admin/users'] [name='create_driver']").checked=true`, nil),
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click("form[action='/admin/users'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("main.users-page", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("create user: %s", browserDiagnostics(browserContext, err))
	}
	var createdUserID, createdPasswordHash string
	var createdActive, createdMustChange bool
	var createdDriverProfiles int
	if err := pool.QueryRow(t.Context(), `
		SELECT id::text, password_hash, active, must_change_password
		FROM users
		WHERE username = 'new-driver-task01'
	`).Scan(&createdUserID, &createdPasswordHash, &createdActive, &createdMustChange); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM drivers WHERE user_id = $1`, createdUserID).Scan(&createdDriverProfiles); err != nil {
		t.Fatal(err)
	}
	if !createdActive || !createdMustChange || createdDriverProfiles != 1 {
		t.Fatalf("created user active/must-change/driver-profile = %v/%v/%d", createdActive, createdMustChange, createdDriverProfiles)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE users SET must_change_password = false WHERE id = $1`, createdUserID); err != nil {
		t.Fatal(err)
	}
	resetPassword := randomE2EPassword(t)
	if resetPassword == createdPassword {
		t.Fatal("generated reset password unexpectedly equals initial password")
	}
	resetForm := "form[action='/admin/users/" + createdUserID + "/reset-password']"
	var resetCancelConfirmCalls int
	if err := runBrowserStep(browserContext, "cancel user password reset",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(resetForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(resetForm+" [name='password']", resetPassword, chromedp.ByQuery),
		chromedp.Evaluate(`window.__e2eConfirmCalls=0;window.confirm=()=>{window.__e2eConfirmCalls++;return false}`, nil),
		chromedp.Click(resetForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.Evaluate(`window.__e2eConfirmCalls`, &resetCancelConfirmCalls),
	); err != nil {
		t.Fatalf("cancel user password reset: %s", browserDiagnostics(browserContext, err))
	}
	var passwordHashAfterCancel string
	if err := pool.QueryRow(t.Context(), `SELECT password_hash, must_change_password FROM users WHERE id = $1`, createdUserID).Scan(&passwordHashAfterCancel, &createdMustChange); err != nil {
		t.Fatal(err)
	}
	if resetCancelConfirmCalls != 1 || passwordHashAfterCancel != createdPasswordHash || createdMustChange {
		t.Fatalf("cancelled reset confirmations/hash-changed/must-change = %d/%v/%v", resetCancelConfirmCalls, passwordHashAfterCancel != createdPasswordHash, createdMustChange)
	}
	if err := runBrowserStep(browserContext, "confirm user password reset",
		chromedp.Evaluate(`window.confirm=()=>true;document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click(resetForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("main.users-page", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("confirm user password reset: %s", browserDiagnostics(browserContext, err))
	}
	var resetPasswordHash string
	if err := pool.QueryRow(t.Context(), `SELECT password_hash, must_change_password FROM users WHERE id = $1`, createdUserID).Scan(&resetPasswordHash, &createdMustChange); err != nil {
		t.Fatal(err)
	}
	if resetPasswordHash == createdPasswordHash || !createdMustChange {
		t.Fatalf("confirmed reset hash-changed/must-change = %v/%v", resetPasswordHash != createdPasswordHash, createdMustChange)
	}

	createdAccessForm := "form.user-access-status[action='/admin/users/" + createdUserID + "/access']"
	var deactivateCancelConfirmCalls int
	if err := runBrowserStep(browserContext, "cancel user deactivation",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(createdAccessForm)+`).closest('details').open=true`, nil),
		chromedp.Evaluate(`window.__e2eConfirmCalls=0;window.confirm=()=>{window.__e2eConfirmCalls++;return false}`, nil),
		chromedp.Click(createdAccessForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.Evaluate(`window.__e2eConfirmCalls`, &deactivateCancelConfirmCalls),
	); err != nil {
		t.Fatalf("cancel user deactivation: %s", browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), `SELECT active FROM users WHERE id = $1`, createdUserID).Scan(&createdActive); err != nil {
		t.Fatal(err)
	}
	if deactivateCancelConfirmCalls != 1 || !createdActive {
		t.Fatalf("cancelled deactivation confirmations/active = %d/%v", deactivateCancelConfirmCalls, createdActive)
	}
	if err := runBrowserStep(browserContext, "confirm user deactivation",
		chromedp.Evaluate(`window.confirm=()=>true;document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click(createdAccessForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("main.users-page", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("confirm user deactivation: %s", browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), `SELECT active FROM users WHERE id = $1`, createdUserID).Scan(&createdActive); err != nil {
		t.Fatal(err)
	}
	if createdActive {
		t.Fatal("confirmed user deactivation left account active")
	}

	if err := runBrowserStep(browserContext, "login driver",
		chromedp.WaitVisible("[data-account-menu] summary", chromedp.ByQuery),
		chromedp.Click("[data-account-menu] summary", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/logout'] button[type='submit']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("[data-account-menu] form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task01-neu", chromedp.ByQuery),
		chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-account-menu] summary", chromedp.ByQuery),
		chromedp.Click("[data-account-menu] summary", chromedp.ByQuery),
		chromedp.WaitVisible("a[href='/profile']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("driver login: %s", browserDiagnostics(browserContext, err))
	}
	var forbiddenStatus int
	expression := `fetch('/admin/users/` + adminID + `/details',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector("input[name=csrf_token]").value,version:'1',username:'attack',display_name:'Attack'})}).then(response=>response.status)`
	if err := chromedp.Run(browserContext, chromedp.Evaluate(expression, &forbiddenStatus, func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	})); err != nil {
		t.Fatal(err)
	}
	if forbiddenStatus != 403 {
		t.Fatalf("driver direct update status = %d, want 403", forbiddenStatus)
	}
}

func TestTask01ForcedPasswordChangeBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, _, driverUserID, _, _, currentPassword := task01Application(t, databaseURL)
	if _, err := pool.Exec(t.Context(), "UPDATE users SET must_change_password=true WHERE id=$1", driverUserID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{
			SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour,
		},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool,
		Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Dashboard: e2eDashboard(t, pool),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU,
		chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(390, 844),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 120*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	newPassword := randomE2EPassword(t)
	if newPassword == currentPassword {
		t.Fatal("generated passwords unexpectedly equal")
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+"/login"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var forcedLocation, mismatchMessage string
	var mismatchPasswordsCleared bool
	var mobileState struct {
		Overflow     bool `json:"overflow"`
		SmallTargets int  `json:"smallTargets"`
	}
	if err := runBrowserStep(browserContext, "forced password login",
		chromedp.SetValue("#username", "driver-task01", chromedp.ByQuery),
		chromedp.SetValue("#password", currentPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/password']", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/profile"),
		chromedp.WaitVisible("form[action='/password']", chromedp.ByQuery),
		chromedp.Location(&forcedLocation),
		chromedp.Evaluate(`(()=>{const visible=node=>{const box=node.getBoundingClientRect();const style=getComputedStyle(node);return box.width>0&&box.height>0&&style.display!=='none'&&style.visibility!=='hidden'};const targets=[...document.querySelectorAll('main input,main button')].filter(visible);return {overflow:document.documentElement.scrollWidth>window.innerWidth,smallTargets:targets.filter(node=>{const box=node.getBoundingClientRect();return box.width<44||box.height<44}).length}})()`, &mobileState),
		chromedp.SetValue("#new-password", newPassword, chromedp.ByQuery),
		chromedp.SetValue("#confirm-password", newPassword+"x", chromedp.ByQuery),
		chromedp.Click("form[action='/password'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[role='alert']", chromedp.ByQuery),
		chromedp.Text("[role='alert']", &mismatchMessage, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#new-password').value===''&&document.querySelector('#confirm-password').value===''`, &mismatchPasswordsCleared),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.HasSuffix(forcedLocation, "/password") || mobileState.Overflow || mobileState.SmallTargets != 0 || !strings.Contains(mismatchMessage, "stimmen nicht überein") || !mismatchPasswordsCleared {
		t.Fatalf("forced password location/mobile/error = %q/%+v/%q", forcedLocation, mobileState, mismatchMessage)
	}
	if err := runBrowserStep(browserContext, "change forced password",
		chromedp.SetValue("#new-password", newPassword, chromedp.ByQuery),
		chromedp.SetValue("#confirm-password", newPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/password'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task01", chromedp.ByQuery),
		chromedp.SetValue("#password", currentPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[role='alert']", chromedp.ByQuery),
		chromedp.SetValue("#password", newPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var mustChange bool
	if err := pool.QueryRow(t.Context(), "SELECT must_change_password FROM users WHERE id=$1", driverUserID).Scan(&mustChange); err != nil {
		t.Fatal(err)
	}
	if mustChange {
		t.Fatal("password change flag remained set")
	}
}

func task01Application(t *testing.T, databaseURL string) (*pgxpool.Pool, *auth.Service, string, string, string, string, string) {
	t.Helper()
	if err := migrate.Run(t.Context(), databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(t.Context(), config.Database{
		URL: databaseURL, MaxConnections: 10, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), "TRUNCATE audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{
		MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(postgres.NewIdentityStore(pool), hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	adminPassword := randomE2EPassword(t)
	driverPassword := randomE2EPassword(t)
	system := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "E2E Setup"}
	adminID, err := identity.CreateUser(t.Context(), system, auth.CreateUserInput{
		Username: "admin-task01", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: adminPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	driverUserID, err := identity.CreateUser(t.Context(), system, auth.CreateUserInput{
		Username: "driver-task01", DisplayName: "Fahrerprofil bleibt", Email: "fahrerprofil@example.test",
		Role: auth.RoleDriver, Password: driverPassword, CreateDriver: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE users SET must_change_password = false"); err != nil {
		t.Fatal(err)
	}
	var driverID string
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM drivers WHERE user_id = $1", driverUserID).Scan(&driverID); err != nil {
		t.Fatal(err)
	}
	return pool, identity, adminID, driverUserID, driverID, adminPassword, driverPassword
}

func quoteJS(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `\'`) + "'"
}
