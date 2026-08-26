package voice

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const ruleParserVersion = "rules-de-at-v1"

type RuleExtractor struct{}

func (RuleExtractor) Version() string { return ruleParserVersion }

var (
	phonePattern    = regexp.MustCompile(`(?i)(?:telefon(?:nummer)?|tel\.?|handy)\s*[:,-]?\s*((?:\+|00)?\d[\d /-]{5,}\d)`)
	volumePattern   = regexp.MustCompile(`(?i)(\d+(?:[,.]\d+)?)\s*(?:kubikmeter|m3|m³)`)
	durationPattern = regexp.MustCompile(`(?i)(\d+|ein(?:e|en)?|zwei|drei|vier|fünf|sechs|sieben|acht|neun|zehn)\s*(stunden?|std\.?|minuten?|min\.?)\s*(?:hackzeit|hacken)?`)
	emailPattern    = regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+`)
	tripPattern     = regexp.MustCompile(`(?i)(\d+|ein(?:e|en)?|zwei|drei|vier|fünf)\s*(?:fahrten?|fuhren?)`)
	monthPattern    = regexp.MustCompile(`(?i)\b(anfang|mitte|ende)?\s*(jänner|januar|februar|märz|april|mai|juni|juli|august|september|oktober|november|dezember)\b`)
)

func (RuleExtractor) Extract(_ context.Context, transcript string, recordedAt time.Time, location *time.Location) (Fields, []string, float64) {
	text := strings.TrimSpace(transcript)
	parts := splitParts(text)
	fields := Fields{}
	warnings := make([]string, 0, 6)
	if len(parts) > 1 {
		name := strings.Fields(parts[0])
		if len(name) >= 2 && !containsDigit(parts[0]) {
			fields.FirstName = field(name[0], parts[0], .9)
			fields.LastName = field(strings.Join(name[1:], " "), parts[0], .9)
		} else if strings.Contains(strings.ToLower(parts[0]), "firma ") {
			fields.CompanyName = field(strings.TrimSpace(parts[0][len("Firma "):]), parts[0], .85)
		}
	}
	if len(parts) > 1 && containsDigit(parts[1]) && !phonePattern.MatchString(parts[1]) {
		fields.AddressFreeform = field(parts[1], parts[1], .73)
		fields.AddressFreeform.Warnings = []string{"Adresse ist unvollständig und muss geprüft werden"}
	}
	if match := phonePattern.FindStringSubmatch(text); len(match) == 2 {
		fields.PhoneRaw = field(strings.TrimSpace(match[1]), match[0], .92)
	}
	if match := emailPattern.FindString(text); match != "" {
		fields.Email = field(match, match, .96)
	}
	if match := volumePattern.FindStringSubmatch(text); len(match) == 2 {
		fields.VolumeM3 = field(strings.ReplaceAll(match[1], ",", "."), match[0], .98)
	}
	if match := durationPattern.FindStringSubmatch(text); len(match) == 3 {
		if amount, ok := spokenNumber(match[1]); ok {
			if strings.HasPrefix(strings.ToLower(match[2]), "st") {
				amount *= 60
			}
			fields.EstimatedHackMinutes = field(strconv.Itoa(amount), match[0], .96)
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "ohne transport") {
		fields.TransportMode = field("none", "ohne Transport", .97)
	} else if strings.Contains(lower, "externer transport") || strings.Contains(lower, "transport extern") {
		fields.TransportMode = field("external", "externer Transport", .9)
	} else if strings.Contains(lower, "mit transport") || strings.Contains(lower, "transport gewünscht") {
		fields.TransportMode = field("undecided", "mit Transport", .82)
		fields.TransportMode.Warnings = []string{"Transportart muss geprüft werden"}
	}
	if match := tripPattern.FindStringSubmatch(text); len(match) == 2 {
		if count, ok := spokenNumber(match[1]); ok {
			fields.TransportTripCount = field(strconv.Itoa(count), match[0], .9)
		}
	}
	if match := monthPattern.FindStringSubmatch(text); len(match) == 3 {
		preference := strings.TrimSpace(match[0])
		fields.PreferenceText = field(titleFirst(preference), match[0], .88)
		start, end := monthRange(recordedAt.In(location), match[1], match[2])
		warning := "Datumsableitung aus relativer Monatsangabe muss geprüft werden"
		fields.PreferredStartDate = field(start.Format("2006-01-02"), match[0], .68)
		fields.PreferredStartDate.Warnings = []string{warning}
		fields.PreferredEndDate = field(end.Format("2006-01-02"), match[0], .68)
		fields.PreferredEndDate.Warnings = []string{warning}
		warnings = append(warnings, warning)
	}
	if strings.Contains(lower, "dringend") || strings.Contains(lower, "sofort") {
		fields.Urgency = field("urgent", "dringend", .9)
	} else if strings.Contains(lower, "möglichst bald") {
		fields.Urgency = field("high", "möglichst bald", .85)
	} else {
		fields.Urgency = field("normal", "", .5)
		fields.Urgency.Warnings = []string{"Dringlichkeit wurde nicht ausdrücklich genannt"}
	}
	if fields.FirstName.Value == "" && fields.LastName.Value == "" && fields.CompanyName.Value == "" {
		warnings = append(warnings, "Name oder Firma fehlt")
	}
	if fields.VolumeM3.Value == "" {
		warnings = append(warnings, "Holzmenge fehlt")
	}
	if fields.EstimatedHackMinutes.Value == "" {
		warnings = append(warnings, "Hackdauer fehlt")
	}
	confidence := averageConfidence(fields)
	return fields, warnings, confidence
}

func field(value, source string, confidence float64) Field {
	return Field{Value: strings.TrimSpace(value), Source: strings.TrimSpace(source), Confidence: confidence}
}
func splitParts(text string) []string {
	raw := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func containsDigit(value string) bool { return strings.IndexFunc(value, unicode.IsDigit) >= 0 }
func spokenNumber(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil {
		return n, n > 0
	}
	n, ok := map[string]int{"ein": 1, "eine": 1, "einen": 1, "zwei": 2, "drei": 3, "vier": 4, "fünf": 5, "sechs": 6, "sieben": 7, "acht": 8, "neun": 9, "zehn": 10}[strings.ToLower(value)]
	return n, ok
}
func titleFirst(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
func monthRange(now time.Time, qualifier, monthName string) (time.Time, time.Time) {
	months := map[string]time.Month{"jänner": 1, "januar": 1, "februar": 2, "märz": 3, "april": 4, "mai": 5, "juni": 6, "juli": 7, "august": 8, "september": 9, "oktober": 10, "november": 11, "dezember": 12}
	month := months[strings.ToLower(monthName)]
	year := now.Year()
	if month < now.Month() {
		year++
	}
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, now.Location()).Day()
	startDay, endDay := 1, last
	switch strings.ToLower(qualifier) {
	case "anfang":
		endDay = min(10, last)
	case "mitte":
		startDay = min(11, last)
		endDay = min(20, last)
	case "ende":
		startDay = min(21, last)
	}
	return time.Date(year, month, startDay, 0, 0, 0, 0, now.Location()), time.Date(year, month, endDay, 0, 0, 0, 0, now.Location())
}
func averageConfidence(fields Fields) float64 {
	values := []Field{fields.FirstName, fields.LastName, fields.CompanyName, fields.AddressFreeform, fields.PhoneRaw, fields.Email, fields.VolumeM3, fields.EstimatedHackMinutes, fields.TransportMode, fields.PreferenceText, fields.Urgency}
	total, count := 0.0, 0.0
	for _, f := range values {
		if f.Value != "" {
			total += f.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / count
}
