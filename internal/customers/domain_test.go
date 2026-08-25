package customers

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	tests := map[string]string{"0664 1234567": "+436641234567", "+43 (664) 123": "+43664123", "0043 664 123456": "+43664123456", "abc": ""}
	for input, expected := range tests {
		if actual := NormalizePhone(input); actual != expected {
			t.Errorf("NormalizePhone(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestJobTransportValidation(t *testing.T) {
	t.Parallel()
	base := JobInput{JobType: JobTypeChippingOnly, VolumeM3: "80,5", EstimatedHackMinutes: 180, TransportMode: TransportNone, Urgency: UrgencyNormal, Source: SourcePhone}
	tests := []struct {
		name    string
		mutate  func(*JobInput)
		wantErr bool
	}{
		{name: "chipping only"},
		{name: "transport undecided", mutate: func(input *JobInput) {
			input.JobType = JobTypeChippingWithTransport
			input.TransportMode = TransportUndecided
		}},
		{name: "chipping only with trips", mutate: func(input *JobInput) { input.TransportTripCount = 1 }, wantErr: true},
		{name: "transport without mode", mutate: func(input *JobInput) { input.JobType = JobTypeChippingWithTransport }, wantErr: true},
		{name: "external confirmed with internal mode", mutate: func(input *JobInput) {
			input.JobType = JobTypeChippingWithTransport
			input.TransportMode = TransportInternal
			input.ExternalTransportConfirmed = true
		}, wantErr: true},
		{name: "external confirmed", mutate: func(input *JobInput) {
			input.JobType = JobTypeChippingWithTransport
			input.TransportMode = TransportExternal
			input.ExternalTransportConfirmed = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			if test.mutate != nil {
				test.mutate(&input)
			}
			err := input.Validate()
			if test.wantErr != errors.Is(err, ErrValidation) {
				t.Fatalf("Validate() error = %v, want validation error %v", err, test.wantErr)
			}
		})
	}
}

func TestDurationAndFilter(t *testing.T) {
	t.Parallel()
	minutes, err := ParseDuration("3:30")
	if err != nil || minutes != 210 {
		t.Fatalf("ParseDuration() = %d, %v", minutes, err)
	}
	filter := WaitlistFilter{JobType: "invalid", Urgency: "invalid", PreferredMonth: "2026-99", Sort: "DROP TABLE", Direction: "sideways", Page: -1, PageSize: 1000}
	filter.Normalize()
	if filter.JobType != "" || filter.Urgency != "" || filter.PreferredMonth != "" || filter.Sort != "entered" || filter.Direction != "asc" || filter.Page != 1 || filter.PageSize != 25 {
		t.Fatalf("Normalize() = %#v", filter)
	}
}

func TestMapsURLDoesNotAcceptArbitraryURL(t *testing.T) {
	t.Parallel()
	link := MapsURL(CustomerInput{Street: "Waldstraße 9", PostalCode: "4710", Locality: "Test", CountryCode: "AT", AddressFreeform: "https://evil.invalid"})
	if !strings.HasPrefix(link, "https://www.google.com/maps/search/?") || strings.Contains(link, "evil.invalid") {
		t.Fatalf("MapsURL() = %q", link)
	}
}
