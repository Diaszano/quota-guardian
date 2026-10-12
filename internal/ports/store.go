package ports

import (
	"context"
	"errors"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

var (
	ErrConflict    = errors.New("conflict: state revision or constraint violation")
	ErrNotFound    = errors.New("entity not found")
	ErrUnsupported = errors.New("unsupported operation or configuration")
	ErrUncertain   = errors.New("uncertain attempt: requires operator resolution")
	ErrBusy        = errors.New("storage busy or locked")
)

type NewSession struct {
	SubmissionKey *string
	Provider      string
	ContextID     string
	Mode          domain.SessionMode
	Context       domain.ExecutionContext
	RetryPolicy   domain.RetryPolicy
	Consent       domain.Consent
}

type SessionFilter struct {
	State    *domain.State
	Provider *string
	Mode     *domain.SessionMode
	DueOnly  bool
	Now      time.Time
	Limit    int
}

type ClaimRequest struct {
	SessionID        string
	ExpectedRevision int64
	OwnerEpoch       string
	LeaseDuration    time.Duration
	Now              time.Time
}

type AttemptResult struct {
	AttemptID         string
	Fence             int64
	FinishedAt        time.Time
	OutcomeCode       string
	ProviderSessionID string
	NextRetryAt       *time.Time
	Transition        domain.Transition
}

type UncertainResolution string

const (
	ResolutionAbandon         UncertainResolution = "abandon"
	ResolutionMarkFailed      UncertainResolution = "mark_failed"
	ResolutionVerifiedSettled UncertainResolution = "verified_settled"
)

type ResolveUncertainRequest struct {
	SessionID   string
	AttemptID   string
	Resolution  UncertainResolution
	Evidence    domain.Evidence
	Operator    string
	Reason      string
	Destination domain.State
}

type SessionStore interface {
	Create(context.Context, NewSession) (domain.Session, error)
	Get(context.Context, string) (domain.Session, error)
	List(context.Context, SessionFilter) ([]domain.Session, error)
	Apply(context.Context, domain.Transition) (domain.Session, error)
	ClaimStart(context.Context, ClaimRequest) (domain.Attempt, bool, error)
	ClaimDue(context.Context, ClaimRequest) (domain.Attempt, bool, error)
	MarkLaunching(context.Context, domain.Attempt) error
	RecordIdentity(context.Context, domain.Attempt, domain.ProcessIdentity) error
	Renew(context.Context, domain.Attempt, time.Time) error
	FinishAttempt(context.Context, AttemptResult) error
	MarkUncertain(context.Context, domain.Attempt, domain.Evidence) error
	Reconcile(context.Context, string, time.Time) error
	ResolveUncertain(context.Context, ResolveUncertainRequest) error
	Backup(context.Context, string) error
	Prune(context.Context, time.Time) error
	Cancel(context.Context, string, int64) (domain.Session, error)
	SetConsent(context.Context, string, int64, domain.Consent) (domain.Session, error)
}
