package domain_test

import (
	"math"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

func TestRetryPolicy(t *testing.T) {
	policy := domain.RetryPolicy{
		SafetyMargin:   30 * time.Second,
		BaseDelay:      60 * time.Second,
		CapDelay:       1 * time.Hour,
		JitterFraction: 0.10,
		MaxRetries:     5,
		Deadline:       7 * 24 * time.Hour,
	}

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(policy.Deadline)

	// 1. No-reset first delay = 90s at jitter 0 (60s base + 30s margin)
	quotaFail := domain.Failure{
		Category: domain.Quota,
		Code:     "RESOURCE_EXHAUSTED",
	}

	decision1 := domain.NextRetry(now, quotaFail, 0, policy, 0.0, deadline)
	if decision1.Blocked {
		t.Fatalf("first retry should not be blocked: %s", decision1.Reason)
	}
	expectedFirst := now.Add(90 * time.Second)
	if !decision1.EligibleAt.Equal(expectedFirst) {
		t.Fatalf("expected first retry eligible at %v, got %v", expectedFirst, decision1.EligibleAt)
	}

	// 2. Second delay = 150s at jitter 0 (120s exponential fallback + 30s margin)
	decision2 := domain.NextRetry(now, quotaFail, 1, policy, 0.0, deadline)
	if decision2.Blocked {
		t.Fatalf("second retry should not be blocked: %s", decision2.Reason)
	}
	expectedSecond := now.Add(150 * time.Second)
	if !decision2.EligibleAt.Equal(expectedSecond) {
		t.Fatalf("expected second retry eligible at %v, got %v", expectedSecond, decision2.EligibleAt)
	}

	// 3. Capped fallback = 3630s (3600s cap + 30s margin)
	cappedPolicy := policy
	cappedPolicy.MaxRetries = 20
	decisionCapped := domain.NextRetry(now, quotaFail, 10, cappedPolicy, 0.0, deadline)
	if decisionCapped.Blocked {
		t.Fatalf("capped retry should not be blocked: %s", decisionCapped.Reason)
	}
	expectedCapped := now.Add(3630 * time.Second)
	if !decisionCapped.EligibleAt.Equal(expectedCapped) {
		t.Fatalf("expected capped retry eligible at %v, got %v", expectedCapped, decisionCapped.EligibleAt)
	}

	// 4. Explicit reset 8h away remains reset + 30s
	reset8h := now.Add(8 * time.Hour)
	quotaWithReset := domain.Failure{
		Category: domain.Quota,
		Code:     "RESOURCE_EXHAUSTED",
		ResetAt:  &reset8h,
	}
	decisionReset := domain.NextRetry(now, quotaWithReset, 0, policy, 0.0, deadline)
	if decisionReset.Blocked {
		t.Fatalf("explicit reset should not be blocked: %s", decisionReset.Reason)
	}
	expectedReset := reset8h.Add(30 * time.Second)
	if !decisionReset.EligibleAt.Equal(expectedReset) {
		t.Fatalf("expected explicit reset eligible at %v, got %v", expectedReset, decisionReset.EligibleAt)
	}

	// 5. Assert fifth exhausted resume budget blocks a sixth (priorResumes = 5 with MaxRetries = 5)
	decisionExhausted := domain.NextRetry(now, quotaFail, 5, policy, 0.0, deadline)
	if !decisionExhausted.Blocked {
		t.Fatalf("expected 5th exhausted resume budget to block 6th attempt, got eligible at %v", decisionExhausted.EligibleAt)
	}
	if decisionExhausted.Reason != "retry_budget_exhausted" {
		t.Fatalf("expected reason retry_budget_exhausted, got %q", decisionExhausted.Reason)
	}

	// 6. Reset after seven-day deadline blocks
	pastDeadlineReset := deadline.Add(1 * time.Hour)
	quotaPastDeadline := domain.Failure{
		Category: domain.Quota,
		Code:     "RESOURCE_EXHAUSTED",
		ResetAt:  &pastDeadlineReset,
	}
	decisionPastDeadline := domain.NextRetry(now, quotaPastDeadline, 0, policy, 0.0, deadline)
	if !decisionPastDeadline.Blocked {
		t.Fatalf("expected reset after deadline to block, got eligible at %v", decisionPastDeadline.EligibleAt)
	}
	if decisionPastDeadline.Reason != "deadline_exceeded" {
		t.Fatalf("expected reason deadline_exceeded, got %q", decisionPastDeadline.Reason)
	}

	// Also fallback exceeding deadline blocks
	shortDeadline := now.Add(60 * time.Second)
	decisionFallbackPastDeadline := domain.NextRetry(now, quotaFail, 0, policy, 0.0, shortDeadline)
	if !decisionFallbackPastDeadline.Blocked {
		t.Fatalf("expected fallback exceeding deadline to block")
	}
	if decisionFallbackPastDeadline.Reason != "deadline_exceeded" {
		t.Fatalf("expected reason deadline_exceeded, got %q", decisionFallbackPastDeadline.Reason)
	}

	// 7. Non-quota failure does NOT enter quota retry fallback
	authFail := domain.Failure{
		Category: domain.Auth,
		Code:     "UNAUTHORIZED",
	}
	decisionAuth := domain.NextRetry(now, authFail, 0, policy, 0.0, deadline)
	if !decisionAuth.Blocked {
		t.Fatalf("non-quota failure must be blocked from quota retry")
	}
	if decisionAuth.Reason != "not_quota_failure" {
		t.Fatalf("expected reason not_quota_failure, got %q", decisionAuth.Reason)
	}

	// 8. Jitter bounds and clamping (additive jitter in [0, 0.10])
	// At jitter = 0.05, fallback delay = 60s, jitter delay = 60s * 0.05 = 3s. Total = 60s + 3s + 30s margin = 93s.
	decisionJitter := domain.NextRetry(now, quotaFail, 0, policy, 0.05, deadline)
	expectedJitter := now.Add(93 * time.Second)
	if !decisionJitter.EligibleAt.Equal(expectedJitter) {
		t.Fatalf("expected jittered eligible at %v, got %v", expectedJitter, decisionJitter.EligibleAt)
	}

	// Jitter clamped when > 0.10: jitter = 0.5 should clamp to 0.10 -> 60s * 0.10 = 6s. Total = 60 + 6 + 30 = 96s.
	decisionJitterOver := domain.NextRetry(now, quotaFail, 0, policy, 0.5, deadline)
	expectedJitterOver := now.Add(96 * time.Second)
	if !decisionJitterOver.EligibleAt.Equal(expectedJitterOver) {
		t.Fatalf("expected clamped jitter eligible at %v, got %v", expectedJitterOver, decisionJitterOver.EligibleAt)
	}

	// Jitter negative clamps to 0.0 -> 90s
	decisionJitterNeg := domain.NextRetry(now, quotaFail, 0, policy, -0.2, deadline)
	if !decisionJitterNeg.EligibleAt.Equal(expectedFirst) {
		t.Fatalf("expected negative jitter clamped to 0 eligible at %v, got %v", expectedFirst, decisionJitterNeg.EligibleAt)
	}

	// NaN jitter clamps to 0.0 -> 90s
	decisionJitterNaN := domain.NextRetry(now, quotaFail, 0, policy, math.NaN(), deadline)
	if !decisionJitterNaN.EligibleAt.Equal(expectedFirst) {
		t.Fatalf("expected NaN jitter clamped to 0 eligible at %v, got %v", expectedFirst, decisionJitterNaN.EligibleAt)
	}

	// Inf jitter clamps to 0.10
	decisionJitterInf := domain.NextRetry(now, quotaFail, 0, policy, math.Inf(1), deadline)
	if !decisionJitterInf.EligibleAt.Equal(expectedJitterOver) {
		t.Fatalf("expected Inf jitter clamped to 0.10, got %v", decisionJitterInf.EligibleAt)
	}

	// 9. Clock rollback / reset in past: if reset is before now, treat reset as now (now + margin)
	pastReset := now.Add(-10 * time.Minute)
	quotaPastReset := domain.Failure{
		Category: domain.Quota,
		Code:     "RESOURCE_EXHAUSTED",
		ResetAt:  &pastReset,
	}
	decisionPastReset := domain.NextRetry(now, quotaPastReset, 0, policy, 0.0, deadline)
	expectedPastReset := now.Add(30 * time.Second)
	if !decisionPastReset.EligibleAt.Equal(expectedPastReset) {
		t.Fatalf("expected past reset to adjust to now+margin %v, got %v", expectedPastReset, decisionPastReset.EligibleAt)
	}

	// 10. Large priorResumes overflow protection
	decisionOverflow := domain.NextRetry(now, quotaFail, 1000, policy, 0.0, deadline)
	if decisionOverflow.Blocked && decisionOverflow.Reason != "retry_budget_exhausted" {
		t.Fatalf("overflow should be handled safely: %+v", decisionOverflow)
	}
}

func TestUnknownScopesStayIndependent(t *testing.T) {
	// Different sessions with nil/unknown scope never combine or affect each other's quota tracking
	scopeA := (*domain.Scope)(nil)
	scopeB := (*domain.Scope)(nil)

	if domain.ScopesEqual(scopeA, scopeB) {
		// Nil scopes should NOT be considered equal or shared
		t.Fatalf("nil scopes must never be considered equal or shareable")
	}

	// Partial / dimension-missing scopes are also not equal to complete scopes
	partialScope := &domain.Scope{
		Provider:   "agy",
		Dimensions: nil,
	}
	completeScope := &domain.Scope{
		Provider: "agy",
		Dimensions: map[string]string{
			"account": "acc-1",
			"model":   "gemini",
		},
		EvidenceVersion: "v1",
	}
	if domain.ScopesEqual(partialScope, completeScope) {
		t.Fatalf("partial scope must not equal complete scope")
	}
	if domain.ScopesEqual(partialScope, partialScope) {
		t.Fatalf("unverified/partial scope must not equal itself for sharing")
	}
}
