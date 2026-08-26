package notification

import "strings"

type DeliveryTarget struct {
	Channel, Label, Recipient string
}

type ChannelAssessment struct {
	Targets                []DeliveryTarget
	Warning, Suggestion    string
	RequiresOverrideReason bool
}

func AssessChannels(preference, email, phone string, mailEnabled, smsEnabled bool) ChannelAssessment {
	preference = strings.TrimSpace(preference)
	email = strings.TrimSpace(email)
	phone = strings.TrimSpace(phone)
	wantsMail := preference == "email" || preference == "both"
	wantsSMS := preference == "sms" || preference == "both"
	result := ChannelAssessment{Targets: make([]DeliveryTarget, 0, 2)}
	if wantsMail && mailEnabled && email != "" {
		result.Targets = append(result.Targets, DeliveryTarget{Channel: "email", Label: "E-Mail", Recipient: MaskRecipient(email, ChannelEmail)})
	}
	if wantsSMS && smsEnabled && phone != "" {
		result.Targets = append(result.Targets, DeliveryTarget{Channel: "sms", Label: "SMS", Recipient: MaskRecipient(phone, ChannelSMS)})
	}
	if len(result.Targets) > 0 {
		return result
	}
	result.RequiresOverrideReason = true
	switch preference {
	case "none":
		result.Warning = "Der Kunde wünscht keine automatische Benachrichtigung. Eine Fixierung ohne Nachricht benötigt eine Begründung."
	case "email":
		result.Warning = unavailableChannelReason("E-Mail", email != "", mailEnabled)
	case "sms":
		result.Warning = unavailableChannelReason("SMS", phone != "", smsEnabled)
	case "both":
		result.Warning = "Keiner der gewünschten Kanäle ist derzeit erreichbar. Kontaktdaten und Providerkonfiguration prüfen."
	default:
		result.Warning = "Die Benachrichtigungspräferenz ist nicht gültig. Kundendaten vor der Fixierung prüfen."
	}
	available := make([]string, 0, 2)
	if !wantsMail && mailEnabled && email != "" {
		available = append(available, "E-Mail an "+MaskRecipient(email, ChannelEmail))
	}
	if !wantsSMS && smsEnabled && phone != "" {
		available = append(available, "SMS an "+MaskRecipient(phone, ChannelSMS))
	}
	if len(available) > 0 {
		result.Suggestion = strings.Join(available, " oder ") + " wäre technisch verfügbar. Kundenpräferenz zuerst ausdrücklich ändern; HackWerk wechselt den Kanal nicht automatisch."
	}
	return result
}

func unavailableChannelReason(label string, hasContact, enabled bool) string {
	if !hasContact && !enabled {
		return label + " ist gewünscht, aber Kontaktdaten fehlen und der Provider ist deaktiviert."
	}
	if !hasContact {
		return label + " ist gewünscht, aber die passende Kontaktangabe fehlt."
	}
	return label + " ist gewünscht, aber der Provider ist deaktiviert."
}
