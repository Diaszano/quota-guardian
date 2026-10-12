package ports

import "context"

type TelemetryEvent string

const (
	TelemetrySessionObserved TelemetryEvent = "session_observed"
	TelemetryTurnSettled     TelemetryEvent = "turn_settled"
	TelemetryRetryScheduled  TelemetryEvent = "retry_scheduled"
)

type Telemetry interface {
	Record(context.Context, TelemetryEvent, string)
}
