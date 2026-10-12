package observability

import (
	"context"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type telemetry struct{ counter metric.Int64Counter }

func New(ctx context.Context, _ configuration.Config) (ports.Telemetry, func(context.Context) error, error) {
	if strings.TrimSpace(os.Getenv("OTEL_METRICS_EXPORTER")) != "otlp" {
		return telemetry{}, func(context.Context) error { return nil }, nil
	}
	exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithTimeout(2*time.Second))
	if err != nil {
		return nil, nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(15*time.Second))), sdkmetric.WithCardinalityLimit(32))
	counter, err := provider.Meter("quota-guardian").Int64Counter("quota_guardian.events")
	if err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, nil, err
	}
	return telemetry{counter: counter}, provider.Shutdown, nil
}

func (t telemetry) Record(ctx context.Context, event ports.TelemetryEvent, provider string) {
	attrs, ok := metricAttributes(event, provider)
	if !ok || t.counter == nil {
		return
	}
	t.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func metricAttributes(event ports.TelemetryEvent, provider string) ([]attribute.KeyValue, bool) {
	switch event {
	case ports.TelemetrySessionObserved, ports.TelemetryTurnSettled, ports.TelemetryRetryScheduled:
	default:
		return nil, false
	}
	switch provider {
	case "agy", "codex", "unknown":
	default:
		return nil, false
	}
	return []attribute.KeyValue{attribute.String("event", string(event)), attribute.String("provider", provider)}, true
}
