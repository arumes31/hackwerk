// Package customers implements customer, job, note, and waitlist business rules.
package customers

import (
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type JobType string
type TransportMode string
type Urgency string
type Source string
type NotificationPreference string
type PileLocationSource string
type PreferenceMode string

const (
	JobTypeChippingOnly          JobType                = "chipping_only"
	JobTypeChippingWithTransport JobType                = "chipping_with_transport"
	TransportNone                TransportMode          = "none"
	TransportInternal            TransportMode          = "internal"
	TransportExternal            TransportMode          = "external"
	TransportUndecided           TransportMode          = "undecided"
	UrgencyLow                   Urgency                = "low"
	UrgencyNormal                Urgency                = "normal"
	UrgencyHigh                  Urgency                = "high"
	UrgencyUrgent                Urgency                = "urgent"
	SourcePhone                  Source                 = "phone"
	SourceVoice                  Source                 = "voice"
	SourceEmail                  Source                 = "email"
	SourceInPerson               Source                 = "in_person"
	SourceOther                  Source                 = "other"
	NotifyEmail                  NotificationPreference = "email"
	NotifySMS                    NotificationPreference = "sms"
	NotifyBoth                   NotificationPreference = "both"
	NotifyNone                   NotificationPreference = "none"
	PileSourceMapPin             PileLocationSource     = "map_pin"
	PileSourceCustomerAddress    PileLocationSource     = "customer_address"
	PileSourceDeviceLocation     PileLocationSource     = "device_location"
	PileSourceCoordinates        PileLocationSource     = "coordinates"
	PreferenceFixed              PreferenceMode         = "fixed"
	PreferenceWindow             PreferenceMode         = "window"
	PreferenceFlexible           PreferenceMode         = "flexible"
)

var ErrValidation = errors.New("customers: validation failed")

type CustomerInput struct {
	FirstName, LastName, CompanyName                                   string
	Street, PostalCode, Locality, Region, CountryCode, AddressFreeform string
	PhoneRaw, Email                                                    string
	NotificationPreference                                             NotificationPreference
	Latitude, Longitude                                                *float64
}

type JobInput struct {
	JobType                              JobType
	VolumeM3                             string
	EstimatedHackMinutes                 int
	EstimatedTransportMinutes            int
	TransportTripCount                   int
	TransportMode                        TransportMode
	ExternalTransportConfirmed           bool
	PreferredStartDate, PreferredEndDate string
	PreferenceMode                       PreferenceMode
	PreferenceText                       string
	Urgency                              Urgency
	Region                               string
	Source                               Source
	PileLatitude, PileLongitude          *float64
	PileLocationSource                   PileLocationSource
}

const (
	MaxJobDurationMinutes = 7 * 24 * 60
	MaxTransportTrips     = 1000
)

type IntakeInput struct {
	Customer    CustomerInput
	Job         JobInput
	InitialNote string
}

func (input CustomerInput) Validate() error {
	if strings.TrimSpace(input.FirstName) == "" && strings.TrimSpace(input.LastName) == "" && strings.TrimSpace(input.CompanyName) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	if input.CountryCode != "" && input.CountryCode != "AT" {
		return fmt.Errorf("%w: only AT is supported", ErrValidation)
	}
	if input.Email != "" {
		parsed, err := mail.ParseAddress(input.Email)
		if err != nil || parsed.Address != input.Email || len(input.Email) > 320 || strings.ContainsAny(input.Email, "\r\n") {
			return fmt.Errorf("%w: invalid email", ErrValidation)
		}
	}
	if input.PhoneRaw != "" && NormalizePhone(input.PhoneRaw) == "" {
		return fmt.Errorf("%w: invalid phone", ErrValidation)
	}
	if (input.Latitude == nil) != (input.Longitude == nil) {
		return fmt.Errorf("%w: coordinates must be complete", ErrValidation)
	}
	if input.Latitude != nil && (*input.Latitude < -90 || *input.Latitude > 90 || *input.Longitude < -180 || *input.Longitude > 180) {
		return fmt.Errorf("%w: coordinates out of range", ErrValidation)
	}
	if !input.NotificationPreference.Valid() {
		return fmt.Errorf("%w: invalid notification preference", ErrValidation)
	}
	for _, field := range []struct {
		value string
		limit int
	}{
		{input.FirstName, 200}, {input.LastName, 200}, {input.CompanyName, 200},
		{input.Street, 250}, {input.PostalCode, 32}, {input.Locality, 120},
		{input.Region, 120}, {input.AddressFreeform, 1000}, {input.PhoneRaw, 64},
	} {
		if len([]rune(field.value)) > field.limit {
			return fmt.Errorf("%w: field is too long", ErrValidation)
		}
	}
	return nil
}

func (input JobInput) Validate() error {
	preferenceMode := input.PreferenceMode
	if preferenceMode == "" {
		preferenceMode = PreferenceWindow
	}
	volume, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(input.VolumeM3), ",", "."), 64)
	if err != nil || volume <= 0 || volume > 99999999 || math.IsNaN(volume) || math.IsInf(volume, 0) {
		return fmt.Errorf("%w: invalid volume", ErrValidation)
	}
	if input.EstimatedHackMinutes <= 0 || input.EstimatedHackMinutes > MaxJobDurationMinutes {
		return fmt.Errorf("%w: invalid hack duration", ErrValidation)
	}
	if !input.JobType.Valid() || !input.TransportMode.Valid() || !input.Urgency.Valid() || !input.Source.Valid() {
		return fmt.Errorf("%w: invalid selection", ErrValidation)
	}
	if input.JobType == JobTypeChippingOnly && (input.EstimatedTransportMinutes != 0 || input.TransportTripCount != 0 || input.TransportMode != TransportNone || input.ExternalTransportConfirmed) {
		return fmt.Errorf("%w: transport values on chipping-only job", ErrValidation)
	}
	if input.JobType == JobTypeChippingWithTransport &&
		(input.EstimatedTransportMinutes < 0 || input.EstimatedTransportMinutes > MaxJobDurationMinutes ||
			input.TransportTripCount < 0 || input.TransportTripCount > MaxTransportTrips || input.TransportMode == TransportNone) {
		return fmt.Errorf("%w: incomplete transport plan", ErrValidation)
	}
	if input.ExternalTransportConfirmed && (input.JobType != JobTypeChippingWithTransport || input.TransportMode != TransportExternal) {
		return fmt.Errorf("%w: external transport confirmation is inconsistent", ErrValidation)
	}
	if len([]rune(input.PreferenceText)) > 1000 || len([]rune(input.Region)) > 120 {
		return fmt.Errorf("%w: field is too long", ErrValidation)
	}
	if (input.PileLatitude == nil) != (input.PileLongitude == nil) {
		return fmt.Errorf("%w: pile coordinates must be complete", ErrValidation)
	}
	if input.PileLatitude == nil {
		if input.PileLocationSource != "" {
			return fmt.Errorf("%w: pile location source without coordinates", ErrValidation)
		}
	} else {
		latitude, longitude := *input.PileLatitude, *input.PileLongitude
		coordinatesInvalid := latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 ||
			math.IsNaN(latitude) || math.IsNaN(longitude) || math.IsInf(latitude, 0) || math.IsInf(longitude, 0)
		if coordinatesInvalid || !input.PileLocationSource.Valid() {
			return fmt.Errorf("%w: invalid pile location", ErrValidation)
		}
	}
	start, startErr := parseOptionalDate(input.PreferredStartDate)
	end, endErr := parseOptionalDate(input.PreferredEndDate)
	if startErr != nil || endErr != nil || (!start.IsZero() && !end.IsZero() && end.Before(start)) || !preferenceMode.Valid() {
		return fmt.Errorf("%w: invalid preferred date range", ErrValidation)
	}
	if preferenceMode == PreferenceFixed && (start.IsZero() || end.IsZero() || !start.Equal(end)) {
		return fmt.Errorf("%w: fixed preference requires exact date", ErrValidation)
	}
	return nil
}

func (value JobType) Valid() bool {
	return value == JobTypeChippingOnly || value == JobTypeChippingWithTransport
}
func (value TransportMode) Valid() bool {
	return value == TransportNone || value == TransportInternal || value == TransportExternal || value == TransportUndecided
}
func (value Urgency) Valid() bool {
	return value == UrgencyLow || value == UrgencyNormal || value == UrgencyHigh || value == UrgencyUrgent
}
func (value Source) Valid() bool {
	return value == SourcePhone || value == SourceVoice || value == SourceEmail || value == SourceInPerson || value == SourceOther
}
func (value NotificationPreference) Valid() bool {
	return value == NotifyEmail || value == NotifySMS || value == NotifyBoth || value == NotifyNone
}
func (value PileLocationSource) Valid() bool {
	return value == PileSourceMapPin || value == PileSourceCustomerAddress ||
		value == PileSourceDeviceLocation || value == PileSourceCoordinates
}
func (value PreferenceMode) Valid() bool {
	return value == PreferenceFixed || value == PreferenceWindow || value == PreferenceFlexible
}

func PointMapsURL(latitude, longitude *float64) string {
	if latitude == nil || longitude == nil {
		return ""
	}
	return MapsURL(CustomerInput{Latitude: latitude, Longitude: longitude})
}

func CanonicalVolume(value string) (string, error) {
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(value), ",", "."), 64)
	if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return "", ErrValidation
	}
	return strconv.FormatFloat(parsed, 'f', 2, 64), nil
}

func ParseDuration(value string) (int, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return 0, ErrValidation
		}
		hours, hourErr := strconv.Atoi(parts[0])
		minutes, minuteErr := strconv.Atoi(parts[1])
		if hourErr != nil || minuteErr != nil || hours < 0 || minutes < 0 || minutes > 59 || hours*60+minutes <= 0 {
			return 0, ErrValidation
		}
		return hours*60 + minutes, nil
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes <= 0 {
		return 0, ErrValidation
	}
	return minutes, nil
}

var nonPhone = regexp.MustCompile(`[^0-9+]`)

func NormalizePhone(value string) string {
	compact := nonPhone.ReplaceAllString(strings.TrimSpace(value), "")
	if strings.HasPrefix(compact, "00") {
		compact = "+" + compact[2:]
	}
	if strings.HasPrefix(compact, "0") {
		compact = "+43" + compact[1:]
	}
	if strings.Count(compact, "+") > 1 || (strings.Contains(compact, "+") && !strings.HasPrefix(compact, "+")) {
		return ""
	}
	digits := strings.TrimPrefix(compact, "+")
	if len(digits) < 7 || len(digits) > 15 {
		return ""
	}
	for _, digit := range digits {
		if !unicode.IsDigit(digit) {
			return ""
		}
	}
	return "+" + digits
}

func MapsURL(customer CustomerInput) string {
	query := ""
	if customer.Latitude != nil && customer.Longitude != nil {
		query = strconv.FormatFloat(*customer.Latitude, 'f', 6, 64) + "," + strconv.FormatFloat(*customer.Longitude, 'f', 6, 64)
	} else {
		parts := []string{customer.Street, customer.PostalCode, customer.Locality, customer.Region, customer.CountryCode}
		clean := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				clean = append(clean, value)
			}
		}
		if len(clean) == 0 {
			query = strings.TrimSpace(customer.AddressFreeform)
		} else {
			query = strings.Join(clean, ", ")
		}
	}
	if strings.TrimSpace(query) == "" {
		return ""
	}
	values := url.Values{"api": {"1"}, "query": {query}}
	return "https://www.google.com/maps/search/?" + values.Encode()
}

type WaitlistFilter struct {
	Query, JobType, Region, Urgency, PreferredMonth, Workflow, DurationGroup, Sort, Direction string
	MissingLocation, DurationIssue, Overdue, Unassigned, TransportPending, Incomplete         bool
	Page, PageSize                                                                            int
	DurationReviewMinMinutes, DurationReviewMaxMinutes                                        int32
}

func (filter *WaitlistFilter) Normalize() {
	allowedSort := map[string]bool{
		"entered": true, "preferred": true, "urgency": true, "volume": true,
		"region": true, "customer": true, "workflow": true, "updated": true, "duration": true,
	}
	if !allowedSort[filter.Sort] {
		filter.Sort = "entered"
	}
	if filter.Direction != "desc" {
		filter.Direction = "asc"
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 25
	}
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Region = strings.TrimSpace(filter.Region)
	if !JobType(filter.JobType).Valid() {
		filter.JobType = ""
	}
	if filter.Urgency != "" && !Urgency(filter.Urgency).Valid() {
		filter.Urgency = ""
	}
	if filter.PreferredMonth != "" {
		if _, err := time.Parse("2006-01", filter.PreferredMonth); err != nil {
			filter.PreferredMonth = ""
		}
	}
	if filter.Workflow != "unplanned" && filter.Workflow != "proposal" && filter.Workflow != "scheduled" {
		filter.Workflow = ""
	}
	if filter.DurationGroup != "short" && filter.DurationGroup != "medium" && filter.DurationGroup != "long" {
		filter.DurationGroup = ""
	}
}

// DurationNeedsReview centralizes the intentionally conservative duration
// signal used by list filters. Values remain valid domain data; the flag only
// asks a human to check unusually short or long estimates.
func DurationNeedsReview(minutes int32) bool {
	return DurationNeedsReviewWithin(minutes, 15, 12*60)
}

func DurationNeedsReviewWithin(minutes, minimum, maximum int32) bool {
	return minutes < minimum || minutes > maximum
}

func parseOptionalDate(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.DateOnly, value)
}
