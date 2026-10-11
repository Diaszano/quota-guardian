package domain

import (
	"errors"
	"fmt"
	"time"
)

type State string

const (
	Discovered      State = "DISCOVERED"
	Running         State = "RUNNING"
	QuotaExhausted  State = "QUOTA_EXHAUSTED"
	WaitingForReset State = "WAITING_FOR_RESET"
	ReadyToResume   State = "READY_TO_RESUME"
	Resuming        State = "RESUMING"
	NeedsAttention  State = "NEEDS_ATTENTION"
	Completed       State = "COMPLETED"
	Failed          State = "FAILED"
	Cancelled       State = "CANCELLED"
)

type TurnOutcome string

const (
	TurnOutcomeUnknown           TurnOutcome = "unknown"
	TurnOutcomeCompleted         TurnOutcome = "completed"
	TurnOutcomeQuota             TurnOutcome = "quota"
	TurnOutcomePermissionBlocked TurnOutcome = "permission_blocked"
	TurnOutcomeFailed            TurnOutcome = "failed"
)

type SessionMode string

const (
	ModeManaged  SessionMode = "managed"
	ModeObserved SessionMode = "observed"
)

type ExecutionContext struct {
	ExecutablePath  string
	ProviderVersion string
	Cwd             string
	Profile         string
	Digest          string
}

type Consent struct {
	Enabled               bool
	BackgroundAllowed     bool
	Revision              int64
	ApprovedContextDigest string
	ApprovedAt            *time.Time
	RevokedAt             *time.Time
}

type RetryPolicy struct {
	SafetyMargin   time.Duration
	BaseDelay      time.Duration
	CapDelay       time.Duration
	JitterFraction float64
	MaxRetries     int
	Deadline       time.Duration
}

type Session struct {
	ID                string
	SubmissionKey     *string
	Provider          string
	ProviderSessionID *string
	ContextID         string
	Mode              SessionMode
	State             State
	Revision          int64
	Fence             int64
	Context           ExecutionContext
	Consent           Consent
	RetryCount        int
	RetryPolicy       RetryPolicy
	NextRetryAt       *time.Time
	DeadlineAt        *time.Time
	CancelRequested   bool
	TurnOutcome       TurnOutcome
	LastError         *Failure
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastObservedAt    *time.Time
}

type Disposition struct {
	State       State
	TurnOutcome TurnOutcome
	Blocker     string
	NextRetryAt *time.Time
}

type Transition struct {
	ExpectedRevision int64
	ExpectedState    State
	Destination      State
	EventID          string
	Source           string
	AttemptID        string
	Fence            int64
	Evidence         Evidence
}

var (
	ErrInvalidTransition      = errors.New("invalid state transition")
	ErrTerminalStateImmutable = errors.New("terminal state is immutable")
)

var allowedTransitions = map[State]map[State]bool{
	Discovered: {
		Running:        true,
		NeedsAttention: true,
		Failed:         true,
		Cancelled:      true,
	},
	Running: {
		QuotaExhausted: true,
		Completed:      true,
		Failed:         true,
		NeedsAttention: true,
		Cancelled:      true,
	},
	QuotaExhausted: {
		WaitingForReset: true,
		NeedsAttention:  true,
		Cancelled:       true,
	},
	WaitingForReset: {
		ReadyToResume:  true,
		NeedsAttention: true,
		Cancelled:      true,
	},
	ReadyToResume: {
		Resuming:        true,
		WaitingForReset: true,
		NeedsAttention:  true,
		Cancelled:       true,
	},
	Resuming: {
		Running:        true,
		QuotaExhausted: true,
		NeedsAttention: true,
		Failed:         true,
		Cancelled:      true,
	},
	NeedsAttention: {
		ReadyToResume: true,
		Failed:        true,
		Cancelled:     true,
	},
	Completed: {},
	Failed:    {},
	Cancelled: {},
}

func IsTerminal(s State) bool {
	return s == Completed || s == Failed || s == Cancelled
}

func ValidateTransition(from, to State) error {
	if IsTerminal(from) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrTerminalStateImmutable, from, to)
	}
	if from == to {
		return fmt.Errorf("%w: cannot transition from %s to same state", ErrInvalidTransition, from)
	}
	destinations, ok := allowedTransitions[from]
	if !ok || !destinations[to] {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, from, to)
	}
	return nil
}
