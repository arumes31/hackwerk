//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	webassets "example.invalid/hackplan/web/assets"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestRouteDatePresetsUseViennaCalendar(t *testing.T) {
	script, err := webassets.Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(script)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><input data-route-day-filter><button data-route-date-preset="today"></button><button data-route-date-preset="tomorrow"></button><button data-route-date-preset="business-day"></button><script>const OriginalDate=Date;globalThis.Date=class extends OriginalDate{constructor(...args){super(...(args.length?args:[%q]))}static now(){return new OriginalDate(%q).getTime()}};</script><script src="/app.js"></script></body></html>`, r.URL.Query().Get("now"), r.URL.Query().Get("now"))
	}))
	t.Cleanup(server.Close)
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.UserDataDir(browserProfileDir(t)))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 60*time.Second)
	t.Cleanup(cancelTimeout)
	for _, tc := range []struct {
		name, now string
		want      []string
	}{
		{"CI midnight", "2026-11-01T23:32:00Z", []string{"2026-11-02", "2026-11-03", "2026-11-03"}},
		{"Friday UTC Saturday Vienna", "2026-11-06T23:30:00Z", []string{"2026-11-07", "2026-11-08", "2026-11-09"}},
		{"spring DST", "2026-03-28T23:30:00Z", []string{"2026-03-29", "2026-03-30", "2026-03-30"}},
		{"autumn DST", "2026-10-24T22:30:00Z", []string{"2026-10-25", "2026-10-26", "2026-10-26"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			if err := chromedp.Run(browser, emulation.SetTimezoneOverride("UTC"), chromedp.Navigate(server.URL+"/?now="+tc.now), chromedp.Evaluate(`['today','tomorrow','business-day'].map(preset=>{document.querySelector('[data-route-date-preset="'+preset+'"]').click();return document.querySelector('[data-route-day-filter]').value})`, &got)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("presets=%v, want %v", got, tc.want)
			}
		})
	}
}
