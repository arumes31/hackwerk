package notification

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode/utf16"
)

const TemplateVersion = 1

type TemplateInput struct {
	CustomerName, JobType, VolumeM3 string
	StartsAt, EndsAt                time.Time
	ConfirmationURL                 string
	BusinessName, BusinessAddress   string
	BusinessPhone                   string
}

type Rendered struct {
	Subject, Text, HTML, SMS string
}

type TemplatePreview struct {
	Subject, Text, SMS, SMSEncoding string
	SMSSegments                     int
}

var confirmationHTML = template.Must(template.New("confirmation").Parse(`<!doctype html><html lang="de"><body><h1>Termin von {{.BusinessName}}</h1><p>Guten Tag {{.CustomerName}},</p><p>Ihr Termin ist am <strong>{{.When}}</strong>. Geplant sind {{.Duration}} für {{.VolumeM3}} m³ ({{.JobType}}).</p><p><a href="{{.ConfirmationURL}}">Termin bestätigen, ablehnen oder Rückruf wünschen</a></p><p>{{.BusinessAddress}} · {{.BusinessPhone}}</p></body></html>`))

func Render(input TemplateInput, location *time.Location) (Rendered, error) {
	if location == nil || input.CustomerName == "" || input.ConfirmationURL == "" || input.StartsAt.IsZero() || !input.EndsAt.After(input.StartsAt) {
		return Rendered{}, errors.New("notification: invalid template input")
	}
	localStart := input.StartsAt.In(location)
	weekdays := [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
	when := weekdays[localStart.Weekday()] + localStart.Format(", 02.01.2006 um 15:04 Uhr")
	totalMinutes := int(input.EndsAt.Sub(input.StartsAt).Round(time.Minute) / time.Minute)
	duration := fmt.Sprintf("%d Minuten", totalMinutes)
	if totalMinutes%60 == 0 {
		duration = fmt.Sprintf("%d Stunden", totalMinutes/60)
	} else if totalMinutes > 60 {
		duration = fmt.Sprintf("%d Stunden %d Minuten", totalMinutes/60, totalMinutes%60)
	}
	jobType := "Hacken"
	if input.JobType == "chipping_with_transport" {
		jobType = "Hacken mit Transport"
	}
	data := struct {
		TemplateInput
		When, Duration string
	}{input, when, duration}
	var html bytes.Buffer
	if err := confirmationHTML.Execute(&html, data); err != nil {
		return Rendered{}, fmt.Errorf("notification: rendering html: %w", err)
	}
	text := fmt.Sprintf("Guten Tag %s,\n\nIhr Termin ist am %s. Geplant sind %s für %s m³ (%s).\n\nBestätigen, ablehnen oder Rückruf wünschen: %s\n\n%s · %s · %s",
		input.CustomerName, when, duration, input.VolumeM3, jobType, input.ConfirmationURL, input.BusinessName, input.BusinessAddress, input.BusinessPhone)
	sms := fmt.Sprintf("%s: Termin %s, %s m³. Antwort: %s. Kontakt: %s", input.BusinessName, when, input.VolumeM3, input.ConfirmationURL, input.BusinessPhone)
	return Rendered{Subject: "Ihr HackWerk-Termin", Text: text, HTML: html.String(), SMS: strings.TrimSpace(sms)}, nil
}

func SyntheticPreview(location *time.Location) (TemplatePreview, error) {
	if location == nil {
		return TemplatePreview{}, errors.New("notification: preview location is required")
	}
	start := time.Date(2026, time.September, 8, 8, 30, 0, 0, location)
	rendered, err := Render(TemplateInput{
		CustomerName: "Maria Muster", JobType: "chipping_with_transport", VolumeM3: "35",
		StartsAt: start.UTC(), EndsAt: start.Add(2*time.Hour + 30*time.Minute).UTC(),
		ConfirmationURL: "https://hackwerk.example/termin/BEISPIELTOKEN-OHNE-FUNKTION",
		BusinessName:    "HackWerk", BusinessAddress: "Musterstraße 1, 4710 Musterort", BusinessPhone: "+43 000 000000",
	}, location)
	if err != nil {
		return TemplatePreview{}, err
	}
	segments, encoding := SMSSegmentCount(rendered.SMS)
	return TemplatePreview{Subject: rendered.Subject, Text: rendered.Text, SMS: rendered.SMS, SMSSegments: segments, SMSEncoding: encoding}, nil
}

func SMSSegmentCount(value string) (int, string) {
	septets, gsm := gsmSeptetCount(value)
	if gsm {
		if septets == 0 {
			return 0, "GSM-7"
		}
		if septets <= 160 {
			return 1, "GSM-7"
		}
		return (septets + 152) / 153, "GSM-7"
	}
	units := len(utf16.Encode([]rune(value)))
	if units == 0 {
		return 0, "UCS-2"
	}
	if units <= 70 {
		return 1, "UCS-2"
	}
	return (units + 66) / 67, "UCS-2"
}

func gsmSeptetCount(value string) (int, bool) {
	const basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	const extended = "^{}\\[~]|€"
	count := 0
	for _, valueRune := range value {
		switch {
		case strings.ContainsRune(basic, valueRune):
			count++
		case strings.ContainsRune(extended, valueRune):
			count += 2
		default:
			return 0, false
		}
	}
	return count, true
}
