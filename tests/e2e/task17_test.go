//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestTask17PersonalProfileDesktopMobileNoJSAndKeyboard(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, _, driverUserID, _, _, driverPassword := task01Application(t, databaseURL)
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{
			SessionCookieName: "hackwerk_task17_session", CSRFCookieName: "hackwerk_task17_csrf", MFACookieName: "hackwerk_task17_mfa",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour,
		},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool,
		Build: buildinfo.Info{Version: "0.17.0-e2e", Commit: "task17"}, Identity: identity, Dashboard: e2eDashboard(t, pool),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Start()
	t.Cleanup(server.Close)
	webAuthnBaseURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	securityKeys, err := auth.NewSecurityKeyRing(map[string]string{
		"e2e-v1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32)),
	}, "e2e-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.ConfigureSecurity(auth.SecurityConfig{
		Keys: securityKeys, AppName: "HackWerk", BaseURL: webAuthnBaseURL,
		EmailVerificationTTL: 24 * time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: 5 * time.Minute, WebAuthnChallengeTTL: 5 * time.Minute, MailMaxAttempts: 6,
	}); err != nil {
		t.Fatal(err)
	}

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

	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/login"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "login and open profile",
		chromedp.SetValue("#username", "driver-task01", chromedp.ByQuery),
		chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/profile"),
		chromedp.WaitVisible("main.profile-page", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	var desktop struct {
		Cards            int    `json:"cards"`
		SessionRows      int    `json:"sessionRows"`
		CurrentSessions  int    `json:"currentSessions"`
		Build            string `json:"build"`
		InstallHidden    bool   `json:"installHidden"`
		InstallStatus    string `json:"installStatus"`
		PasswordStrength bool   `json:"passwordStrength"`
	}
	if err := chromedp.Run(browserContext, chromedp.Evaluate(`(()=>({
		cards:document.querySelectorAll('.profile-card').length,
		sessionRows:document.querySelectorAll('.session-row').length,
		currentSessions:[...document.querySelectorAll('.session-row')].filter(row=>row.textContent.includes('Aktuelle Sitzung')).length,
		build:[...document.querySelectorAll('.profile-list dt')].find(node=>node.textContent==='Build')?.nextElementSibling?.textContent||'',
		installHidden:document.querySelector('[data-profile-install]').hidden,
		installStatus:document.querySelector('[data-profile-install-status]').textContent,
		passwordStrength:!!document.querySelector("a[href='/password']")
	}))()`, &desktop)); err != nil {
		t.Fatal(err)
	}
	installStateConsistent := (desktop.InstallHidden && desktop.InstallStatus == "In diesem Browser nicht verfügbar") || (!desktop.InstallHidden && desktop.InstallStatus == "Installation unterstützt")
	if desktop.Cards < 6 || desktop.SessionRows != 1 || desktop.CurrentSessions != 1 || !strings.Contains(desktop.Build, "0.17.0-e2e") || !installStateConsistent || !desktop.PasswordStrength {
		t.Fatalf("desktop profile = %+v", desktop)
	}

	var mobile struct {
		Overflow     bool `json:"overflow"`
		Columns      int  `json:"columns"`
		SmallTargets int  `json:"smallTargets"`
	}
	var screenshot []byte
	if err := runBrowserStep(browserContext, "mobile profile",
		chromedp.EmulateViewport(360, 800),
		chromedp.Evaluate(`(()=>{
			const visible=node=>{const box=node.getBoundingClientRect();const style=getComputedStyle(node);return box.width>0&&box.height>0&&style.display!=='none'&&style.visibility!=='hidden'};
			const targets=[...document.querySelectorAll('.profile-page a,.profile-page button,.profile-page input:not([type="hidden"]):not([type="checkbox"]),.profile-page select,.profile-page .check-label')].filter(visible);
			return {overflow:document.documentElement.scrollWidth>window.innerWidth,columns:getComputedStyle(document.querySelector('.profile-grid')).gridTemplateColumns.split(' ').length,smallTargets:targets.filter(node=>{const box=node.getBoundingClientRect();return box.width<44||box.height<44}).length};
		})()`, &mobile),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if mobile.Overflow || mobile.Columns != 1 || mobile.SmallTargets != 0 {
		t.Fatalf("mobile profile = %+v", mobile)
	}
	artifact := filepath.Join(t.ArtifactDir(), "task17-mobile-profile.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile profile screenshot: %s", artifact)

	var focusedID string
	if err := runBrowserStep(browserContext, "keyboard order and strength feedback",
		chromedp.EmulateViewport(1280, 900),
		chromedp.Focus("#profile-display-name", chromedp.ByQuery),
		chromedp.KeyEvent("\t"),
		chromedp.Evaluate(`document.activeElement.id`, &focusedID),
		chromedp.Navigate(server.URL+"/password"),
		chromedp.WaitVisible("[data-password-strength-output]", chromedp.ByQuery),
		chromedp.SetValue("#new-password", "kurz", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-password-strength-output] small').textContent.includes('Noch 10 Zeichen')`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if focusedID != "profile-phone" {
		t.Fatalf("keyboard focus moved to %q", focusedID)
	}

	if err := runBrowserStep(browserContext, "no JavaScript profile mutation",
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Navigate(server.URL+"/profile"),
		chromedp.WaitVisible("form[action='/profile/details']", chromedp.ByQuery),
		chromedp.SetValue("#profile-display-name", "Fahrer NoJS", chromedp.ByQuery),
		chromedp.SetValue("#profile-phone", "0664 7654321", chromedp.ByQuery),
		chromedp.Click("form[action='/profile/details'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-profile-notice]", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
		chromedp.Reload(),
		chromedp.WaitVisible("main.profile-page", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var displayName, normalizedPhone string
	if err := pool.QueryRow(t.Context(), "SELECT display_name, work_phone_normalized FROM users WHERE id=$1", driverUserID).Scan(&displayName, &normalizedPhone); err != nil {
		t.Fatal(err)
	}
	if displayName != "Fahrer NoJS" || normalizedPhone != "+436647654321" {
		t.Fatalf("NoJS profile stored %q/%q", displayName, normalizedPhone)
	}

	var installFlow struct {
		Shown       bool   `json:"shown"`
		Status      string `json:"status"`
		PromptCalls int    `json:"promptCalls"`
	}
	if err := runBrowserStep(browserContext, "profile install capability and offline status",
		chromedp.Evaluate(`(()=>{window.__task17PromptCalls=0;const event=new Event('beforeinstallprompt',{cancelable:true});event.prompt=async()=>{window.__task17PromptCalls++};event.userChoice=Promise.resolve({outcome:'accepted'});window.dispatchEvent(event)})()`, nil),
		chromedp.Poll(`!document.querySelector('[data-profile-install]').hidden`, nil),
		chromedp.Evaluate(`(()=>({shown:!document.querySelector('[data-profile-install]').hidden,status:document.querySelector('[data-profile-install-status]').textContent,promptCalls:window.__task17PromptCalls}))()`, &installFlow),
		chromedp.Click("[data-profile-install]", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-profile-install]').hidden&&window.__task17PromptCalls===1`, nil),
		chromedp.Evaluate(`window.__task17PromptCalls`, &installFlow.PromptCalls),
		chromedp.Evaluate(`(()=>{window.__task17Online=false;Object.defineProperty(navigator,'onLine',{configurable:true,get:()=>window.__task17Online});window.dispatchEvent(new Event('offline'))})()`, nil),
		chromedp.Poll(`document.querySelector('[data-profile-connectivity]').textContent.startsWith('Offline')`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !installFlow.Shown || installFlow.Status != "Installation unterstützt" || installFlow.PromptCalls != 1 {
		t.Fatalf("profile install flow = %+v", installFlow)
	}

	var preferencesCleared, unrelatedPreserved bool
	if err := runBrowserStep(browserContext, "logout clears only allowlisted preferences",
		chromedp.Evaluate(`(()=>{window.__task17Online=true;window.dispatchEvent(new Event('online'));localStorage.setItem('hackwerk:density','comfortable');localStorage.setItem('hackwerk:outdoor','true');localStorage.setItem('hackwerk:install-dismissed','true');localStorage.setItem('hackwerk:privacy-notice:v1','read');localStorage.setItem('unrelated:keep','yes');document.querySelector('[data-clear-local-preferences]').checked=true})()`, nil),
		chromedp.Click("[data-logout-form] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.Evaluate(`['hackwerk:density','hackwerk:outdoor','hackwerk:install-dismissed','hackwerk:privacy-notice:v1'].every(key=>localStorage.getItem(key)===null)`, &preferencesCleared),
		chromedp.Evaluate(`localStorage.getItem('unrelated:keep')==='yes'`, &unrelatedPreserved),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !preferencesCleared || !unrelatedPreserved {
		t.Fatalf("logout storage allowlist cleared/preserved = %v/%v", preferencesCleared, unrelatedPreserved)
	}
}
