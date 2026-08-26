//go:build live

package notification

import (
	"os"
	"testing"
	"time"
)

func TestSendberryLive(t *testing.T) {
	apiKey := os.Getenv("SENDBERRY_API_KEY")
	accessName := os.Getenv("SENDBERRY_ACCESS_NAME")
	accessPassword := os.Getenv("SENDBERRY_ACCESS_PASSWORD")
	endpoint := os.Getenv("SENDBERRY_API_URL")
	recipient := os.Getenv("SENDBERRY_LIVE_TO")
	if endpoint == "" || apiKey == "" || accessName == "" || accessPassword == "" || recipient == "" {
		t.Skip("Sendberry live credentials are not configured")
	}
	sender := os.Getenv("SMS_SENDER")
	if sender == "" {
		t.Fatal("SMS_SENDER is required for the Sendberry live test")
	}
	provider, err := NewSendberryProvider(SendberryConfig{
		URL: endpoint, APIKey: apiKey, AccessName: accessName,
		AccessPassword: accessPassword, Sender: sender, Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal("Sendberry configuration was rejected")
	}
	providerID, err := provider.Send(t.Context(), Message{
		NotificationID: "hackwerk-live-check-20260825", IdempotencyKey: "hackwerk-live-check-20260825",
		Channel: ChannelSMS, Recipient: recipient,
		Text: "HackWerk Sendberry-Verbindungstest. Keine Antwort erforderlich.",
	})
	if err != nil {
		t.Fatalf("Sendberry live request failed: %v", err)
	}
	if providerID == "" {
		t.Fatal("Sendberry returned no provider ID")
	}
	t.Logf("Sendberry accepted the test SMS; provider ID: %s", providerID)
}
