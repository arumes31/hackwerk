package web

import (
	"log/slog"
	"net/http"

	"example.invalid/hackplan/web/templates"
)

func onboardingPage(page templates.PageData, csrfCookieName string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.Onboarding(templates.OnboardingData{
			Shell: shell(request, page, csrfCookieName),
		}), http.StatusOK, logger)
	}
}
