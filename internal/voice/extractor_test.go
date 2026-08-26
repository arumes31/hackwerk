package voice

import (
	"context"
	"testing"
	"time"
)

func TestRuleExtractorExampleCreatesReviewableFields(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	fields, warnings, confidence := (RuleExtractor{}).Extract(context.Background(), "Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September", time.Date(2026, 8, 25, 10, 0, 0, 0, location), location)
	if fields.FirstName.Value != "Franz" || fields.LastName.Value != "Huber" || fields.AddressFreeform.Value != "Unterneukirchen 15" || fields.PhoneRaw.Value != "0664 1234567" || fields.VolumeM3.Value != "80" || fields.EstimatedHackMinutes.Value != "180" || fields.PreferenceText.Value != "Anfang September" {
		t.Fatalf("fields = %#v", fields)
	}
	if fields.PreferredStartDate.Value != "2026-09-01" || fields.PreferredEndDate.Value != "2026-09-10" || fields.PreferredStartDate.Confidence >= .75 || len(warnings) == 0 || confidence <= 0 || confidence > 1 {
		t.Fatalf("date/warnings/confidence = %#v/%#v/%v", fields.PreferredStartDate, warnings, confidence)
	}
}

func TestRuleExtractorDoesNotInventMissingFields(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	fields, warnings, _ := (RuleExtractor{}).Extract(context.Background(), "Unklare Aufnahme aus dem Wald", time.Now(), location)
	if fields.PhoneRaw.Value != "" || fields.VolumeM3.Value != "" || fields.EstimatedHackMinutes.Value != "" || fields.FirstName.Value != "" || fields.LastName.Value != "" {
		t.Fatalf("invented fields = %#v", fields)
	}
	if len(warnings) < 3 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestRuleExtractorTransportUrgencyAndFutureMonth(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	fields, _, _ := (RuleExtractor{}).Extract(context.Background(), "Maria Berger, Waldweg 7, 35 m³, zwei Stunden Hackzeit, mit Transport, drei Fahrten, dringend, Ende März", time.Date(2026, 10, 25, 2, 30, 0, 0, location), location)
	if fields.TransportMode.Value != "undecided" || fields.TransportTripCount.Value != "3" || fields.Urgency.Value != "urgent" || fields.PreferredStartDate.Value != "2027-03-21" || fields.PreferredEndDate.Value != "2027-03-31" {
		t.Fatalf("fields = %#v", fields)
	}
}

func FuzzRuleExtractorNeverPanics(f *testing.F) {
	f.Add("Franz Huber, 80 m³, drei Stunden")
	location, _ := time.LoadLocation("Europe/Vienna")
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 12000 {
			t.Skip()
		}
		fields, _, confidence := (RuleExtractor{}).Extract(context.Background(), value, time.Now(), location)
		if confidence < 0 || confidence > 1 {
			t.Fatalf("confidence = %v, fields = %#v", confidence, fields)
		}
	})
}
