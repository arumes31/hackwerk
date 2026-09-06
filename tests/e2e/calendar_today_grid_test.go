//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.invalid/hackplan/web/assets"
	"github.com/chromedp/chromedp"
)

func TestCalendarTodayHighlightKeepsTimeGridDividersVisible(t *testing.T) {
	publicAssets, err := assets.PublicFS()
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(location).Format(time.DateOnly)

	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(publicAssets))))
	mux.HandleFunc("/", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(response, `<!doctype html>
<html lang="de"><head><meta charset="utf-8">
<link rel="stylesheet" href="/assets/app.css">
<link rel="stylesheet" href="/assets/fullcalendar-skeleton.css">
<link rel="stylesheet" href="/assets/fullcalendar-theme.css">
<link rel="stylesheet" href="/assets/fullcalendar-palette.css">
<script src="/assets/fullcalendar.min.js"></script>
<script src="/assets/fullcalendar-theme.min.js"></script>
</head><body><main class="calendar-page"><div id="calendar"></div></main>
<script>
const calendar=new FullCalendar.Calendar(document.querySelector('#calendar'),{
  themeSystem:'classic',locale:'de-AT',timeZone:'Europe/Vienna',
  initialView:'timeGridWeek',initialDate:%q,allDaySlot:false,
  slotMinTime:'06:00:00',slotMaxTime:'20:00:00',height:'auto'
});
calendar.render();
</script></body></html>`, today)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU,
			chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
			chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1200, 900),
		)...,
	)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 30*time.Second)
	t.Cleanup(cancelTimeout)

	var audit struct {
		TodayLaneCount       int
		TodayBackground      string
		TodayBackgroundAlpha float64
		SlotDivider          string
	}
	var screenshot []byte
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible(fmt.Sprintf(`[data-date=%q]`, today), chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf(`(() => {
			const todayLanes=[...document.querySelectorAll('[data-date=%q]')]
				.filter(node=>node.getBoundingClientRect().height>0)
				.sort((left,right)=>right.getBoundingClientRect().height-left.getBoundingClientRect().height);
			const lane=todayLanes[0];
			const background=lane ? getComputedStyle(lane).backgroundColor : '';
			const rgba=background.match(/^rgba?\(([^)]+)\)$/);
			const components=rgba ? rgba[1].split(',').map(value=>value.trim()) : [];
			const alpha=background==='transparent' ? 0 : components.length===4 ? Number(components[3]) : 1;
			const slot=[...document.querySelectorAll('[data-time]')].find(node=>node.getBoundingClientRect().height>0);
			const slotStyle=slot ? getComputedStyle(slot) : null;
			return {
				TodayLaneCount:todayLanes.length,
				TodayBackground:background,
				TodayBackgroundAlpha:alpha,
				SlotDivider:slotStyle ? slotStyle.borderBottomColor : ''
			};
		})()`, today), &audit),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if audit.TodayLaneCount == 0 || audit.TodayBackgroundAlpha >= 1 || audit.SlotDivider == "" || audit.SlotDivider == "rgba(0, 0, 0, 0)" {
		t.Fatalf("today time-grid lanes hide dividers: %+v", audit)
	}
	artifact := filepath.Join(t.ArtifactDir(), "calendar-today-grid.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("calendar today-grid screenshot: %s", artifact)
}
