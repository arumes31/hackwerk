package notification

import "testing"

func TestAssessChannelsExplainsTargetsMissingPreferenceAndFallback(t *testing.T) {
	tests := []struct {
		name, preference, email, phone string
		mailEnabled, smsEnabled        bool
		targets                        int
		warning, suggestion, override  bool
	}{
		{name: "email target", preference: "email", email: "private@example.test", mailEnabled: true, targets: 1},
		{name: "missing email with SMS fallback", preference: "email", phone: "+43 664 1234567", mailEnabled: true, smsEnabled: true, warning: true, suggestion: true, override: true},
		{name: "preference none", preference: "none", email: "private@example.test", mailEnabled: true, warning: true, suggestion: true, override: true},
		{name: "SMS provider disabled", preference: "sms", phone: "+43 664 1234567", warning: true, override: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := AssessChannels(test.preference, test.email, test.phone, test.mailEnabled, test.smsEnabled)
			if len(result.Targets) != test.targets || (result.Warning != "") != test.warning || (result.Suggestion != "") != test.suggestion || result.RequiresOverrideReason != test.override {
				t.Fatalf("assessment = %+v", result)
			}
			for _, target := range result.Targets {
				if target.Recipient == test.email || target.Recipient == test.phone {
					t.Fatalf("target was not masked: %+v", target)
				}
			}
		})
	}
}
