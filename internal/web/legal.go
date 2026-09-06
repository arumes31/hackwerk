package web

import (
	"log/slog"
	"net/http"

	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
)

func registerLegalRoutes(router chi.Router, page templates.PageData, logger *slog.Logger) {
	pages := map[string]struct {
		title string
		kind  templates.LegalPageKind
	}{
		"/impressum":   {title: "Impressum", kind: templates.LegalPageImprint},
		"/datenschutz": {title: "Datenschutz", kind: templates.LegalPagePrivacy},
		"/cookies":     {title: "Cookies und Browser-Speicher", kind: templates.LegalPageCookies},
	}

	for path, legalPage := range pages {
		component := templates.LegalPage(page, legalPage.kind)
		router.Get(path, func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Cache-Control", "no-cache")
			if err := component.Render(request.Context(), response); err != nil {
				logger.ErrorContext(request.Context(), "rendering legal page", slog.Any("error", err))
			}
		})
	}
}
