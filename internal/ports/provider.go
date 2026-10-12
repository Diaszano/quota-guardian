package ports

import (
	"context"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

// Capabilities describes verified runtime capabilities of a provider adapter.
type Capabilities struct {
	Version            string
	CanObserve         bool
	CanStart           bool
	CanResume          bool
	CanAutonomous      bool
	SupportsDenial     bool
	SupportsSettlement bool
}

// Event represents an observation or lifecycle signal emitted by a provider.
type Event struct {
	Kind              string
	Source            string
	Provider          string
	ProviderSessionID string
	Evidence          domain.Evidence
	ObservedAt        time.Time
	DeliveryID        string
}

// Outcome captures the terminal result and evidence from an invocation attempt.
type Outcome struct {
	Evidence domain.Evidence
	ExitCode int
}

// StartRequest contains parameters to launch a managed initial turn.
type StartRequest struct {
	SessionID string
	Attempt   domain.Attempt
	Context   domain.ExecutionContext
	Prompt    string // Ephemeral prompt passed into stdin / invocation
	OnStart   func(domain.ProcessIdentity) error
}

// ResumeRequest contains parameters to resume an existing managed turn.
type ResumeRequest struct {
	SessionID         string
	ProviderSessionID string
	Attempt           domain.Attempt
	Context           domain.ExecutionContext
	OnStart           func(domain.ProcessIdentity) error
}

// Provider defines the interface for interacting with underlying AI agent CLI tools.
type Provider interface {
	Name() string
	Version() string
	Capabilities() Capabilities
	Start(ctx context.Context, req StartRequest, onRecord func(StreamRecord) error) (Outcome, error)
	Resume(ctx context.Context, req ResumeRequest, onRecord func(StreamRecord) error) (Outcome, error)
}
