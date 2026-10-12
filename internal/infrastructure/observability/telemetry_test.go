package observability

import (
	"context"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestMetricAttributesBounded(t *testing.T) {
	attrs, ok := metricAttributes(ports.TelemetrySessionObserved, "codex")
	if !ok || len(attrs) != 2 || attrs[0].Value.AsString() != "session_observed" || attrs[1].Value.AsString() != "codex" {
		t.Fatalf("attrs=%v ok=%v", attrs, ok)
	}
	if _, ok := metricAttributes("secret prompt", "codex"); ok {
		t.Fatal("arbitrary event accepted")
	}
	if _, ok := metricAttributes(ports.TelemetrySessionObserved, "secret-provider"); ok {
		t.Fatal("arbitrary provider accepted")
	}
}

func TestSecretsNeverLogged(t *testing.T) {
	for _, secret := range []string{"prompt text", "/private/workspace", "token=secret"} {
		if _, ok := metricAttributes(ports.TelemetryEvent(secret), "codex"); ok {
			t.Fatalf("sensitive value accepted as event attribute: %q", secret)
		}
		if _, ok := metricAttributes(ports.TelemetrySessionObserved, secret); ok {
			t.Fatalf("sensitive value accepted as provider attribute: %q", secret)
		}
	}
}

func TestExporterDisabled(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	_, shutdown, err := New(context.Background(), configuration.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
