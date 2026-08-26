package calendarfeed

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func Generate(name, uidDomain, baseURL, detail string, events []Event, fallbackModified time.Time) (Calendar, error) {
	var output bytes.Buffer
	writeLine(&output, "BEGIN:VCALENDAR")
	writeLine(&output, "VERSION:2.0")
	writeLine(&output, "PRODID:-//HackWerk//Kalender 1.0//DE")
	writeLine(&output, "CALSCALE:GREGORIAN")
	writeLine(&output, "METHOD:PUBLISH")
	writeLine(&output, "X-WR-CALNAME:"+escapeText(name))
	lastModified := fallbackModified.UTC()
	for _, event := range events {
		if err := validateEvent(event); err != nil {
			return Calendar{}, err
		}
		modified := event.LastModified.UTC()
		if modified.After(lastModified) {
			lastModified = modified
		}
		writeLine(&output, "BEGIN:VEVENT")
		writeLine(&output, "UID:"+event.ID+"@"+uidDomain)
		writeLine(&output, "DTSTAMP:"+icalTime(event.CreatedAt))
		writeLine(&output, "DTSTART:"+icalTime(event.StartsAt))
		writeLine(&output, "DTEND:"+icalTime(event.EndsAt))
		writeLine(&output, "LAST-MODIFIED:"+icalTime(modified))
		writeLine(&output, "SEQUENCE:"+strconv.FormatInt(event.Sequence, 10))
		status := "CONFIRMED"
		if event.Lifecycle == "cancelled" {
			status = "CANCELLED"
		}
		writeLine(&output, "STATUS:"+status)
		summary := "HackWerk-Termin"
		if detail == DetailInternal {
			summary = strings.TrimSpace(customerName(event) + " · " + event.JobNumber)
		}
		writeLine(&output, "SUMMARY:"+escapeText(summary))
		description := "Menge: " + event.VolumeM3 + " m³\nAuftrag: " + jobTypeLabel(event.JobType) + "\nStatus: " + statusLabel(event.Lifecycle)
		if detail == DetailInternal {
			description += "\nHackWerk: " + strings.TrimRight(baseURL, "/") + "/calendar"
		}
		writeLine(&output, "DESCRIPTION:"+escapeText(description))
		if detail == DetailInternal {
			address := strings.TrimSpace(strings.Join([]string{event.Street, event.PostalCode, event.Locality, event.CountryCode}, " "))
			if address != "" {
				writeLine(&output, "LOCATION:"+escapeText(address))
			}
			if event.Latitude != "" && event.Longitude != "" {
				writeLine(&output, "GEO:"+event.Latitude+";"+event.Longitude)
			}
		}
		writeLine(&output, "END:VEVENT")
	}
	writeLine(&output, "END:VCALENDAR")
	payload := output.Bytes()
	return Calendar{Bytes: append([]byte(nil), payload...), ETag: etag(payload), LastModified: lastModified}, nil
}

func icalTime(value time.Time) string { return value.UTC().Format("20060102T150405Z") }

func escapeText(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, ";", "\\;")
	return strings.ReplaceAll(value, ",", "\\,")
}

func writeLine(output *bytes.Buffer, line string) {
	for len(line) > 75 {
		cut := 75
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if cut == 0 {
			cut = 75
		}
		_, _ = fmt.Fprintf(output, "%s\r\n", line[:cut])
		line = " " + line[cut:]
	}
	_, _ = fmt.Fprintf(output, "%s\r\n", line)
}
