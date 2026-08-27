package customers

import (
	"errors"
	"math"
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
		{name: "hack duration exceeds storage bound", mutate: func(input *JobInput) {
			input.EstimatedHackMinutes = MaxJobDurationMinutes + 1
		}, wantErr: true},
		{name: "transport duration exceeds storage bound", mutate: func(input *JobInput) {
			input.JobType = JobTypeChippingWithTransport
			input.TransportMode = TransportUndecided
			input.EstimatedTransportMinutes = MaxJobDurationMinutes + 1
		}, wantErr: true},
		{name: "transport trips exceed storage bound", mutate: func(input *JobInput) {
			input.JobType = JobTypeChippingWithTransport
			input.TransportMode = TransportUndecided
			input.TransportTripCount = MaxTransportTrips + 1
		}, wantErr: true},
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

func TestJobPileLocationValidation(t *testing.T) {
	t.Parallel()
	validLatitude, validLongitude := 48.20849, 16.37208
	invalidLatitude := 91.0
	base := JobInput{JobType: JobTypeChippingOnly, VolumeM3: "80", EstimatedHackMinutes: 180, TransportMode: TransportNone, Urgency: UrgencyNormal, Source: SourcePhone}
	tests := []struct {
		name    string
		mutate  func(*JobInput)
		wantErr bool
	}{
		{name: "optional location"},
		{name: "valid map pin", mutate: func(input *JobInput) {
			input.PileLatitude, input.PileLongitude, input.PileLocationSource = &validLatitude, &validLongitude, PileSourceMapPin
		}},
		{name: "incomplete pair", mutate: func(input *JobInput) { input.PileLatitude = &validLatitude }, wantErr: true},
		{name: "source without coordinates", mutate: func(input *JobInput) { input.PileLocationSource = PileSourceCoordinates }, wantErr: true},
		{name: "coordinates without source", mutate: func(input *JobInput) {
			input.PileLatitude, input.PileLongitude = &validLatitude, &validLongitude
		}, wantErr: true},
		{name: "out of range", mutate: func(input *JobInput) {
			input.PileLatitude, input.PileLongitude, input.PileLocationSource = &invalidLatitude, &validLongitude, PileSourceCoordinates
		}, wantErr: true},
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

func TestMapsURLIsEmptyWithoutAddressOrCoordinates(t *testing.T) {
	t.Parallel()
	if link := MapsURL(CustomerInput{}); link != "" {
		t.Fatalf("MapsURL() = %q, want empty", link)
	}
}

func TestWaitlistFilterNormalizesWorkflowAndReviewFlags(t *testing.T) {
	t.Parallel()
	filter := WaitlistFilter{Workflow: "not-a-state", Sort: "customer", Direction: "desc", MissingLocation: true, DurationIssue: true}
	filter.Normalize()
	if filter.Workflow != "" || filter.Sort != "customer" || filter.Direction != "desc" || !filter.MissingLocation || !filter.DurationIssue {
		t.Fatalf("Normalize() = %#v", filter)
	}
	if !DurationNeedsReview(14) || DurationNeedsReview(15) || DurationNeedsReview(720) || !DurationNeedsReview(721) {
		t.Fatal("DurationNeedsReview() boundaries are wrong")
	}
}

func TestCustomerInputValidationCoversContactAndCoordinates(t *testing.T) {
	t.Parallel()
	latitude, longitude := 48.20849, 16.37208
	invalidLatitude := 91.0
	base := CustomerInput{FirstName: "Maria", CountryCode: "AT", NotificationPreference: NotifyBoth}
	tests := []struct {
		name    string
		mutate  func(*CustomerInput)
		wantErr bool
	}{
		{name: "minimal valid customer"},
		{name: "complete contacts and coordinates", mutate: func(input *CustomerInput) {
			input.Email, input.PhoneRaw = "maria@example.test", "0664 1234567"
			input.Latitude, input.Longitude = &latitude, &longitude
		}},
		{name: "name required", mutate: func(input *CustomerInput) { input.FirstName = "" }, wantErr: true},
		{name: "unsupported country", mutate: func(input *CustomerInput) { input.CountryCode = "DE" }, wantErr: true},
		{name: "invalid mail display name", mutate: func(input *CustomerInput) { input.Email = "Maria <maria@example.test>" }, wantErr: true},
		{name: "mail header injection", mutate: func(input *CustomerInput) { input.Email = "maria@example.test\r\nBcc: attacker@example.test" }, wantErr: true},
		{name: "invalid phone", mutate: func(input *CustomerInput) { input.PhoneRaw = "abc" }, wantErr: true},
		{name: "incomplete coordinates", mutate: func(input *CustomerInput) { input.Latitude = &latitude }, wantErr: true},
		{name: "coordinates outside range", mutate: func(input *CustomerInput) { input.Latitude, input.Longitude = &invalidLatitude, &longitude }, wantErr: true},
		{name: "unknown notification preference", mutate: func(input *CustomerInput) { input.NotificationPreference = "letter" }, wantErr: true},
		{name: "field is too long", mutate: func(input *CustomerInput) { input.Locality = strings.Repeat("x", 121) }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			if test.mutate != nil {
				test.mutate(&input)
			}
			err := input.Validate()
			if errors.Is(err, ErrValidation) != test.wantErr {
				t.Fatalf("Validate() error = %v, want validation=%t", err, test.wantErr)
			}
		})
	}
}

func TestDomainParsingAndMapsHelpersRejectUnsafeValues(t *testing.T) {
	t.Parallel()
	latitude, longitude := 48.20849, 16.37208
	if link := PointMapsURL(&latitude, &longitude); !strings.Contains(link, "48.208490%2C16.372080") {
		t.Fatalf("PointMapsURL() = %q", link)
	}
	if link := PointMapsURL(&latitude, nil); link != "" {
		t.Fatalf("PointMapsURL with incomplete point = %q", link)
	}
	for _, test := range []struct {
		value string
		want  string
		valid bool
	}{
		{value: "80,5", want: "80.50", valid: true},
		{value: " 1 ", want: "1.00", valid: true},
		{value: "0"},
		{value: "NaN"},
		{value: "+Inf"},
	} {
		actual, err := CanonicalVolume(test.value)
		if (err == nil) != test.valid || actual != test.want {
			t.Fatalf("CanonicalVolume(%q) = %q, %v", test.value, actual, err)
		}
	}
	for _, test := range []struct {
		value   string
		minutes int
		valid   bool
	}{
		{value: "3:30", minutes: 210, valid: true},
		{value: "45", minutes: 45, valid: true},
		{value: "1:60"},
		{value: "-1"},
		{value: "1:2:3"},
	} {
		actual, err := ParseDuration(test.value)
		if (err == nil) != test.valid || actual != test.minutes {
			t.Fatalf("ParseDuration(%q) = %d, %v", test.value, actual, err)
		}
	}
	input := validIntake().Job
	input.PileLatitude, input.PileLongitude, input.PileLocationSource = ptr(math.NaN()), ptr(longitude), PileSourceCoordinates
	if err := input.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("NaN pile location was accepted: %v", err)
	}
}

func TestCustomerAndWaitlistFiltersChooseSafeDefaults(t *testing.T) {
	t.Parallel()
	customerFilter := CustomerListFilter{Sort: "locality"}
	customerFilter.Normalize()
	if customerFilter.Direction != "asc" || customerFilter.Page != 1 || customerFilter.PageSize != 25 {
		t.Fatalf("CustomerListFilter.Normalize() = %#v", customerFilter)
	}
	customerFilter = CustomerListFilter{Sort: "jobs"}
	customerFilter.Normalize()
	if customerFilter.Direction != "desc" {
		t.Fatalf("jobs sort direction = %q", customerFilter.Direction)
	}
	filter := WaitlistFilter{JobType: string(JobTypeChippingOnly), Urgency: string(UrgencyUrgent), PreferredMonth: "2026-08", Workflow: "scheduled", Sort: "updated", Direction: "desc", Page: 2, PageSize: 50}
	filter.Normalize()
	if filter.JobType != string(JobTypeChippingOnly) || filter.Urgency != string(UrgencyUrgent) || filter.PreferredMonth != "2026-08" || filter.Workflow != "scheduled" || filter.Page != 2 || filter.PageSize != 50 {
		t.Fatalf("WaitlistFilter.Normalize() = %#v", filter)
	}
}

func ptr(value float64) *float64 { return &value }
