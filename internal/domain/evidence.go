package domain

import (
	"fmt"
	"time"
)

type FailureCategory string

const (
	Quota              FailureCategory = "quota"
	Permission         FailureCategory = "permission"
	Auth               FailureCategory = "authentication"
	Network            FailureCategory = "network"
	ServiceUnavailable FailureCategory = "service_unavailable"
	MalformedOutput    FailureCategory = "malformed_output"
	InvalidSession     FailureCategory = "invalid_session"
	Unknown            FailureCategory = "unknown"
)

type Scope struct {
	Provider        string
	Dimensions      map[string]string
	EvidenceVersion string
}

type Failure struct {
	Category FailureCategory
	Code     string
	ResetAt  *time.Time
	Scope    *Scope
}

type Evidence struct {
	ExpectedSessionID  string
	ConfirmedSessionID string
	Settled            bool
	TurnCompleted      bool
	PermissionBlocked  bool
	Failure            Failure
	ObservedAt         time.Time
}

func Decide(state State, evidence Evidence) (Disposition, error) {
	if IsTerminal(state) {
		return Disposition{}, fmt.Errorf("%w: cannot decide from terminal state %s", ErrTerminalStateImmutable, state)
	}

	// 1. Permission denial always wins: remains attention, never success
	if evidence.PermissionBlocked {
		disp := Disposition{
			State:       NeedsAttention,
			TurnOutcome: TurnOutcomePermissionBlocked,
			Blocker:     "permission_blocked",
		}
		if evidence.Failure.Code != "" {
			disp.Blocker = evidence.Failure.Code
		}
		return disp, nil
	}

	switch state {
	case Running:
		// Quota failure
		if evidence.Failure.Category == Quota {
			if evidence.Settled {
				return Disposition{
					State:       QuotaExhausted,
					TurnOutcome: TurnOutcomeQuota,
					NextRetryAt: evidence.Failure.ResetAt,
				}, nil
			}
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeUnknown,
				Blocker:     "unsettled_quota_failure",
			}, nil
		}

		// Definitive fatal failures
		if evidence.Failure.Category == Auth || evidence.Failure.Category == InvalidSession {
			return Disposition{
				State:       Failed,
				TurnOutcome: TurnOutcomeFailed,
				Blocker:     string(evidence.Failure.Category),
			}, nil
		}

		// Other errors requiring attention
		if evidence.Failure.Category != "" {
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeFailed,
				Blocker:     string(evidence.Failure.Category),
			}, nil
		}

		// Turn completed
		if evidence.TurnCompleted {
			if evidence.Settled {
				return Disposition{
					State:       Completed,
					TurnOutcome: TurnOutcomeCompleted,
				}, nil
			}
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeUnknown,
				Blocker:     "unsettled_turn_completion",
			}, nil
		}

		// Settled without turn completion is an uncertain/incomplete turn
		if evidence.Settled {
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeUnknown,
				Blocker:     "incomplete_turn_settled",
			}, nil
		}

		// Default running state if not settled, no failure, not completed
		return Disposition{
			State:       Running,
			TurnOutcome: TurnOutcomeUnknown,
		}, nil

	case Resuming:
		// Resumed invocation failing with quota
		if evidence.Failure.Category == Quota {
			// Must confirm expected saved session ID and execution settled
			hasMatchingNonEmptyIDs := evidence.ExpectedSessionID != "" &&
				evidence.ConfirmedSessionID != "" &&
				evidence.ExpectedSessionID == evidence.ConfirmedSessionID

			if hasMatchingNonEmptyIDs && evidence.Settled {
				return Disposition{
					State:       QuotaExhausted,
					TurnOutcome: TurnOutcomeQuota,
					NextRetryAt: evidence.Failure.ResetAt,
				}, nil
			}
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeUnknown,
				Blocker:     "unsettled_or_unconfirmed_quota_failure",
			}, nil
		}

		// Definitive resume rejection
		if evidence.Failure.Category == Auth || evidence.Failure.Category == InvalidSession {
			return Disposition{
				State:       Failed,
				TurnOutcome: TurnOutcomeFailed,
				Blocker:     string(evidence.Failure.Category),
			}, nil
		}

		// Resumed invocation established running
		hasMatchingNonEmptyIDs := evidence.ExpectedSessionID != "" &&
			evidence.ConfirmedSessionID != "" &&
			evidence.ExpectedSessionID == evidence.ConfirmedSessionID
		if hasMatchingNonEmptyIDs && evidence.Settled && evidence.TurnCompleted {
			return Disposition{State: Completed, TurnOutcome: TurnOutcomeCompleted}, nil
		}

		if hasMatchingNonEmptyIDs && !evidence.Settled && evidence.Failure.Category == "" {
			return Disposition{
				State:       Running,
				TurnOutcome: TurnOutcomeUnknown,
			}, nil
		}

		// Ambiguous launch or outcome
		return Disposition{
			State:       NeedsAttention,
			TurnOutcome: TurnOutcomeUnknown,
			Blocker:     "ambiguous_resumed_outcome",
		}, nil

	case Discovered:
		if evidence.Failure.Category == Auth || evidence.Failure.Category == InvalidSession {
			return Disposition{
				State:       Failed,
				TurnOutcome: TurnOutcomeFailed,
				Blocker:     string(evidence.Failure.Category),
			}, nil
		}
		if evidence.Failure.Category != "" {
			return Disposition{
				State:       NeedsAttention,
				TurnOutcome: TurnOutcomeFailed,
				Blocker:     string(evidence.Failure.Category),
			}, nil
		}
		return Disposition{
			State:       Running,
			TurnOutcome: TurnOutcomeUnknown,
		}, nil

	case QuotaExhausted:
		return Disposition{
			State:       WaitingForReset,
			TurnOutcome: TurnOutcomeQuota,
			NextRetryAt: evidence.Failure.ResetAt,
		}, nil

	case NeedsAttention:
		return Disposition{
			State:       NeedsAttention,
			TurnOutcome: TurnOutcomeUnknown,
			Blocker:     "requires_operator_resolution",
		}, nil

	default:
		return Disposition{
			State:       NeedsAttention,
			TurnOutcome: TurnOutcomeUnknown,
			Blocker:     fmt.Sprintf("unhandled_state_%s", state),
		}, nil
	}
}
