package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

func TestCompletionRequiresEvidence(t *testing.T) {
	// 1. Settled:true without TurnCompleted:true must NOT be Completed
	got, err := domain.Decide(domain.Running, domain.Evidence{Settled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State == domain.Completed {
		t.Fatalf("expected State != Completed when TurnCompleted is false, got %v", got.State)
	}

	// 2. Completed turn + settled + no blocker => Completed
	got, err = domain.Decide(domain.Running, domain.Evidence{
		Settled:       true,
		TurnCompleted: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State != domain.Completed {
		t.Fatalf("expected Completed, got %v", got.State)
	}
	if got.TurnOutcome != domain.TurnOutcomeCompleted {
		t.Fatalf("expected TurnOutcomeCompleted, got %v", got.TurnOutcome)
	}

	// 3. Same evidence + PermissionBlocked => NeedsAttention
	got, err = domain.Decide(domain.Running, domain.Evidence{
		Settled:           true,
		TurnCompleted:     true,
		PermissionBlocked: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State != domain.NeedsAttention {
		t.Fatalf("expected NeedsAttention when PermissionBlocked is true, got %v", got.State)
	}
}

func TestPermissionBlockerWins(t *testing.T) {
	// A permission denial followed by a nominal success/exit zero must remain attention, never success
	got, err := domain.Decide(domain.Running, domain.Evidence{
		Settled:           true,
		TurnCompleted:     true,
		PermissionBlocked: true,
		Failure: domain.Failure{
			Category: domain.Permission,
			Code:     "PERMISSION_DENIED",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.State != domain.NeedsAttention {
		t.Fatalf("permission blocker must yield NeedsAttention, got %v", got.State)
	}
	if got.TurnOutcome != domain.TurnOutcomePermissionBlocked {
		t.Fatalf("expected TurnOutcomePermissionBlocked, got %v", got.TurnOutcome)
	}
}

func TestQuotaBeforeRunning(t *testing.T) {
	// Resuming + matching nonempty IDs + settled quota => QuotaExhausted
	got, err := domain.Decide(domain.Resuming, domain.Evidence{
		ExpectedSessionID:  "session-a",
		ConfirmedSessionID: "session-a",
		Settled:            true,
		Failure:            domain.Failure{Category: domain.Quota},
	})
	if err != nil || got.State != domain.QuotaExhausted {
		t.Fatalf("settled repeated quota: got %+v, err %v", got, err)
	}
	if got.TurnOutcome != domain.TurnOutcomeQuota {
		t.Fatalf("expected TurnOutcomeQuota, got %v", got.TurnOutcome)
	}

	// Missing expected ID => NeedsAttention
	got, err = domain.Decide(domain.Resuming, domain.Evidence{
		ExpectedSessionID:  "",
		ConfirmedSessionID: "session-a",
		Settled:            true,
		Failure:            domain.Failure{Category: domain.Quota},
	})
	if err != nil || got.State != domain.NeedsAttention {
		t.Fatalf("missing expected ID must yield NeedsAttention, got %+v, err %v", got, err)
	}

	// Missing confirmed ID => NeedsAttention
	got, err = domain.Decide(domain.Resuming, domain.Evidence{
		ExpectedSessionID:  "session-a",
		ConfirmedSessionID: "",
		Settled:            true,
		Failure:            domain.Failure{Category: domain.Quota},
	})
	if err != nil || got.State != domain.NeedsAttention {
		t.Fatalf("missing confirmed ID must yield NeedsAttention, got %+v, err %v", got, err)
	}

	// Mismatched ID => NeedsAttention
	got, err = domain.Decide(domain.Resuming, domain.Evidence{
		ExpectedSessionID:  "session-a",
		ConfirmedSessionID: "session-b",
		Settled:            true,
		Failure:            domain.Failure{Category: domain.Quota},
	})
	if err != nil || got.State != domain.NeedsAttention {
		t.Fatalf("mismatched ID must yield NeedsAttention, got %+v, err %v", got, err)
	}

	// Unsettled quota => NeedsAttention
	got, err = domain.Decide(domain.Resuming, domain.Evidence{
		ExpectedSessionID:  "session-a",
		ConfirmedSessionID: "session-a",
		Settled:            false,
		Failure:            domain.Failure{Category: domain.Quota},
	})
	if err != nil || got.State != domain.NeedsAttention {
		t.Fatalf("unsettled quota must yield NeedsAttention, got %+v, err %v", got, err)
	}
}

func TestTransitionTable(t *testing.T) {
	allStates := []domain.State{
		domain.Discovered,
		domain.Running,
		domain.QuotaExhausted,
		domain.WaitingForReset,
		domain.ReadyToResume,
		domain.Resuming,
		domain.NeedsAttention,
		domain.Completed,
		domain.Failed,
		domain.Cancelled,
	}

	allowed := map[domain.State]map[domain.State]bool{
		domain.Discovered: {
			domain.Running:        true,
			domain.NeedsAttention: true,
			domain.Failed:         true,
			domain.Cancelled:      true,
		},
		domain.Running: {
			domain.QuotaExhausted: true,
			domain.Completed:      true,
			domain.Failed:         true,
			domain.NeedsAttention: true,
			domain.Cancelled:      true,
		},
		domain.QuotaExhausted: {
			domain.WaitingForReset: true,
			domain.NeedsAttention:  true,
			domain.Cancelled:       true,
		},
		domain.WaitingForReset: {
			domain.ReadyToResume:  true,
			domain.NeedsAttention: true,
			domain.Cancelled:      true,
		},
		domain.ReadyToResume: {
			domain.Resuming:        true,
			domain.WaitingForReset: true,
			domain.NeedsAttention:  true,
			domain.Cancelled:       true,
		},
		domain.Resuming: {
			domain.Running:        true,
			domain.QuotaExhausted: true,
			domain.Completed:      true,
			domain.NeedsAttention: true,
			domain.Failed:         true,
			domain.Cancelled:      true,
		},
		domain.NeedsAttention: {
			domain.ReadyToResume:   true,
			domain.WaitingForReset: true,
			domain.Failed:          true,
			domain.Cancelled:       true,
		},
		domain.Completed: {},
		domain.Failed:    {},
		domain.Cancelled: {},
	}

	for _, from := range allStates {
		for _, to := range allStates {
			isAllowed := allowed[from][to]
			err := domain.ValidateTransition(from, to)

			if isAllowed && err != nil {
				t.Errorf("transition %s -> %s should be allowed, got error: %v", from, to, err)
			}
			if !isAllowed && err == nil {
				t.Errorf("transition %s -> %s should NOT be allowed, but err was nil", from, to)
			}

			// Explicit check for terminal states immutability
			if from == domain.Completed || from == domain.Failed || from == domain.Cancelled {
				if err == nil {
					t.Errorf("terminal state %s must be immutable, but transition to %s succeeded", from, to)
				}
			}
		}
	}
}

func TestNoObjectiveSuccessField(t *testing.T) {
	// Rule: Assert no field reports objective success.
	typesToCheck := []interface{}{
		domain.Session{},
		domain.Evidence{},
		domain.Disposition{},
		domain.Transition{},
	}

	forbiddenSubstrings := []string{"success", "objective"}

	for _, instance := range typesToCheck {
		val := reflect.TypeOf(instance)
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			nameLower := strings.ToLower(field.Name)
			for _, forbidden := range forbiddenSubstrings {
				if strings.Contains(nameLower, forbidden) {
					t.Fatalf("type %s contains forbidden field %s (matches %q)", val.Name(), field.Name, forbidden)
				}
			}
		}
	}
}

func TestTerminalStateImmutability(t *testing.T) {
	terminalStates := []domain.State{
		domain.Completed,
		domain.Failed,
		domain.Cancelled,
	}

	for _, state := range terminalStates {
		if !domain.IsTerminal(state) {
			t.Fatalf("expected state %s to be terminal", state)
		}

		_, err := domain.Decide(state, domain.Evidence{
			Settled:       true,
			TurnCompleted: true,
		})
		if err == nil {
			t.Fatalf("expected Decide on terminal state %s to fail", state)
		}
		if !errors.Is(err, domain.ErrTerminalStateImmutable) {
			t.Fatalf("expected ErrTerminalStateImmutable on terminal state %s, got: %v", state, err)
		}
	}
}
