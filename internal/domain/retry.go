package domain

import (
	"math"
	"time"
)

type RetryDecision struct {
	EligibleAt time.Time
	Reason     string
	Blocked    bool
}

const (
	ReasonNotQuotaFailure        = "not_quota_failure"
	ReasonRetryBudgetExhausted   = "retry_budget_exhausted"
	ReasonDeadlineExceeded       = "deadline_exceeded"
	ReasonScheduledFallback      = "scheduled_fallback"
	ReasonScheduledExplicitReset = "scheduled_explicit_reset"
)

func NextRetry(now time.Time, failure Failure, priorResumes int, policy RetryPolicy, jitter float64, deadline time.Time) RetryDecision {
	// Only confirmed quota enters fallback; other failures (auth, network, etc.) do NOT enter quota retry fallback.
	if failure.Category != Quota {
		return RetryDecision{
			Blocked: true,
			Reason:  ReasonNotQuotaFailure,
		}
	}

	// Budget check: 5 max resumes. A 5th exhausted resume budget blocks a 6th.
	// If priorResumes >= MaxRetries, budget is exhausted.
	if priorResumes >= policy.MaxRetries || priorResumes < 0 {
		return RetryDecision{
			Blocked: true,
			Reason:  ReasonRetryBudgetExhausted,
		}
	}

	// Jitter clamping and NaN check: additive jitter in [0, 0.10]
	if math.IsNaN(jitter) || jitter < 0.0 {
		jitter = 0.0
	} else if jitter > 0.10 {
		jitter = 0.10
	}

	var eligibleAt time.Time
	reason := ReasonScheduledFallback

	if failure.ResetAt != nil {
		reason = ReasonScheduledExplicitReset
		reset := *failure.ResetAt
		// Clock rollback / reset in past: if reset is before now, adjust to now
		if reset.Before(now) {
			reset = now
		}
		eligibleAt = reset.Add(policy.SafetyMargin)
	} else {
		// Fallback exponential delay:
		// Base: policy.BaseDelay (e.g. 60s)
		// Growth by priorResumes: base * 2^priorResumes
		// Capped at policy.CapDelay (e.g. 1 hour)
		// Additive jitter: delay * jitter
		// Safety margin: policy.SafetyMargin (e.g. 30s)
		multiplier := 1.0
		if priorResumes > 0 {
			if priorResumes > 30 {
				multiplier = math.MaxFloat64
			} else {
				multiplier = math.Pow(2, float64(priorResumes))
			}
		}

		delaySec := float64(policy.BaseDelay/time.Second) * multiplier
		capSec := float64(policy.CapDelay / time.Second)
		if delaySec > capSec || math.IsInf(delaySec, 0) {
			delaySec = capSec
		}

		jitterSec := delaySec * jitter
		totalDelaySec := delaySec + jitterSec
		totalDelay := time.Duration(totalDelaySec * float64(time.Second))

		eligibleAt = now.Add(totalDelay).Add(policy.SafetyMargin)
	}

	// Deadline check: if eligible time > deadline, it blocks.
	if !deadline.IsZero() && eligibleAt.After(deadline) {
		return RetryDecision{
			Blocked: true,
			Reason:  ReasonDeadlineExceeded,
		}
	}

	return RetryDecision{
		EligibleAt: eligibleAt,
		Reason:     reason,
		Blocked:    false,
	}
}

// ScopesEqual checks whether two scopes can share a quota cooldown window.
// Unknown or incomplete scopes never combine or affect each other.
func ScopesEqual(a, b *Scope) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Provider == "" || b.Provider == "" || a.Provider != b.Provider {
		return false
	}
	if a.EvidenceVersion == "" || b.EvidenceVersion == "" || a.EvidenceVersion != b.EvidenceVersion {
		return false
	}
	if len(a.Dimensions) == 0 || len(b.Dimensions) == 0 || len(a.Dimensions) != len(b.Dimensions) {
		return false
	}
	for k, v := range a.Dimensions {
		if bv, ok := b.Dimensions[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
