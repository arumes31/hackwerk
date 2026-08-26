package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestFullCalendarAssetsArePageSpecific(t *testing.T) {
	t.Parallel()

	page := PageData{
		AppName:                     "HackWerk",
		CSSPath:                     "/assets/app.css?v=app",
		ControlFoundationCSSPath:    "/assets/control-foundation.css?v=control",
		JSPath:                      "/assets/app.js?v=app",
		FullCalendarJSPath:          "/assets/fullcalendar.min.js?v=calendar",
		FullCalendarThemeJSPath:     "/assets/fullcalendar-theme.min.js?v=calendar",
		FullCalendarSkeletonCSSPath: "/assets/fullcalendar-skeleton.css?v=calendar",
		FullCalendarThemeCSSPath:    "/assets/fullcalendar-theme.css?v=calendar",
		FullCalendarPaletteCSSPath:  "/assets/fullcalendar-palette.css?v=calendar",
	}

	render := func(calendar bool) string {
		t.Helper()
		var output bytes.Buffer
		component := documentHead(page, "Test")
		if calendar {
			component = calendarDocumentHead(page, "Kalender")
		}
		if err := component.Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	regular := render(false)
	if strings.Contains(regular, "fullcalendar") {
		t.Fatalf("regular page loads calendar assets: %s", regular)
	}
	calendar := render(true)
	for _, assetName := range []string{"fullcalendar.min.js", "fullcalendar-theme.min.js", "fullcalendar-skeleton.css", "fullcalendar-theme.css", "fullcalendar-palette.css"} {
		if !strings.Contains(calendar, assetName) {
			t.Errorf("calendar head does not load %s", assetName)
		}
	}
	if strings.Index(calendar, "control-foundation.css") > strings.Index(calendar, "app.css") {
		t.Fatal("control foundation must load before application CSS")
	}
}
