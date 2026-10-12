package infrastructure

import (
	"context"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
)

func TestDoctorNoModelCalls(t *testing.T) {
	report, err := Doctor(context.Background(), configuration.Config{Providers: map[string]configuration.Provider{"test": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ModelCalls != 0 || len(report.Capabilities) != 1 || report.Capabilities[0].CanStart || report.Capabilities[0].CanResume || report.Capabilities[0].CanAutonomous {
		t.Fatalf("doctor report=%+v", report)
	}
}
