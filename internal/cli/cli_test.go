package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
)

func TestRunHelpAndVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		arguments    []string
		expectedCode int
		expectedText string
	}{
		{name: "help", arguments: []string{"help"}, expectedCode: ExitSuccess, expectedText: "Verwendung"},
		{name: "version", arguments: []string{"version"}, expectedCode: ExitSuccess, expectedText: "hackwerk"},
		{name: "missing command", arguments: []string{}, expectedCode: ExitUsage, expectedText: "Verwendung"},
		{name: "unknown command", arguments: []string{"unknown"}, expectedCode: ExitUsage, expectedText: "Unbekannter"},
		{name: "serve help", arguments: []string{"serve", "--help"}, expectedCode: ExitSuccess, expectedText: "HTTP-Webdienst"},
		{name: "migrate help", arguments: []string{"migrate", "--help"}, expectedCode: ExitSuccess, expectedText: "Datenbankschema"},
		{name: "healthcheck help", arguments: []string{"healthcheck", "--help"}, expectedCode: ExitSuccess, expectedText: "Readiness"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			var errorOutput bytes.Buffer
			code := Run(context.Background(), tt.arguments, IO{Output: &output, Error: &errorOutput})
			if code != tt.expectedCode {
				t.Fatalf("Run() = %d, want %d", code, tt.expectedCode)
			}
			combined := output.String() + errorOutput.String()
			if !strings.Contains(combined, tt.expectedText) {
				t.Fatalf("output = %q, want containing %q", combined, tt.expectedText)
			}
		})
	}
}

func TestCustomerSeedScenarios(t *testing.T) {
	t.Parallel()
	scenarios := customerSeedScenarios()
	if len(scenarios) != 3 {
		t.Fatalf("customerSeedScenarios() count = %d, want 3", len(scenarios))
	}
	want := []struct {
		lastName string
		volume   string
		minutes  int
		jobType  customers.JobType
	}{
		{lastName: "Huber", volume: "80", minutes: 180, jobType: customers.JobTypeChippingWithTransport},
		{lastName: "Maier", volume: "150", minutes: 360, jobType: customers.JobTypeChippingOnly},
		{lastName: "Berger", volume: "40", minutes: 120, jobType: customers.JobTypeChippingOnly},
	}
	for index, expected := range want {
		actual := scenarios[index]
		if actual.Customer.LastName != expected.lastName || actual.Job.VolumeM3 != expected.volume ||
			actual.Job.EstimatedHackMinutes != expected.minutes || actual.Job.JobType != expected.jobType {
			t.Errorf("scenario %d = %#v, want %#v", index, actual, expected)
		}
	}
}

func TestOperationSeedScenarios(t *testing.T) {
	t.Parallel()

	resources := resourceSeedInputs()
	if len(resources) < 2 || resources[0].Type != resource.TypeChipper || resources[1].Type != resource.TypeTransportVehicle {
		t.Fatalf("resourceSeedInputs() = %#v", resources)
	}
	accounts := driverSeedAccounts()
	if len(accounts) != 5 {
		t.Fatalf("driverSeedAccounts() count = %d, want 5", len(accounts))
	}
	if len(accounts[0].rules) != 3 || len(accounts[1].rules) != 5 || accounts[1].exception == nil || accounts[1].exception.Type != driver.ExceptionVacation {
		t.Fatalf("driverSeedAccounts() missing weekly or vacation scenario: %#v", accounts)
	}
	if len(accounts[4].rules) != 0 {
		t.Fatalf("Emil must demonstrate unavailable-by-default, rules = %#v", accounts[4].rules)
	}
}

func TestRunAdminPlaceholder(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	code := Run(context.Background(), []string{"admin", "--help"}, IO{Output: &output, Error: &errorOutput})
	if code != ExitSuccess {
		t.Fatalf("Run() = %d", code)
	}
	if !strings.Contains(output.String(), "stdin") {
		t.Fatalf("output = %q", output.String())
	}
}
