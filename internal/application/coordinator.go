package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

const attemptLease = 30 * time.Second

type Coordinator struct {
	store      ports.SessionStore
	providers  map[string]ports.Provider
	clock      ports.Clock
	ownerEpoch string
	mu         sync.Mutex
	active     map[string]context.CancelFunc
}

func NewCoordinator(store ports.SessionStore, providers map[string]ports.Provider, clock ports.Clock) *Coordinator {
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		copy(epoch[:], fmt.Sprintf("fallback-%d", time.Now().UnixNano()))
	}
	return &Coordinator{store: store, providers: providers, clock: clock, ownerEpoch: hex.EncodeToString(epoch[:]), active: make(map[string]context.CancelFunc)}
}

func (c *Coordinator) Start(ctx context.Context, req ports.StartRequest) error {
	c.mu.Lock()
	session, err := c.store.Get(ctx, req.SessionID)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	provider, err := c.provider(session.Provider)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	caps := provider.Capabilities()
	if session.Mode != domain.ModeManaged || !caps.CanStart || !caps.SupportsDenial || !caps.SupportsSettlement {
		c.mu.Unlock()
		return ports.ErrUnsupported
	}
	now := c.clock.Now()
	attempt, claimed, err := c.store.ClaimStart(ctx, ports.ClaimRequest{
		SessionID: session.ID, ExpectedRevision: session.Revision, OwnerEpoch: c.ownerEpoch,
		LeaseDuration: attemptLease, Now: now,
	})
	if err != nil || !claimed {
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return ports.ErrConflict
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.active[session.ID] = cancel
	c.mu.Unlock()
	defer c.finishRun(session.ID, cancel)

	if err := c.store.MarkLaunching(runCtx, attempt); err != nil {
		if runCtx.Err() != nil {
			return c.finishCancelled(attempt)
		}
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return err
	}
	if runCtx.Err() != nil {
		return c.finishCancelled(attempt)
	}
	req.Attempt, req.Context = attempt, session.Context
	req.OnStart = func(identity domain.ProcessIdentity) error { return c.store.RecordIdentity(runCtx, attempt, identity) }
	outcome, err := provider.Start(runCtx, req, nil)
	if runCtx.Err() != nil {
		return c.finishCancelled(attempt)
	}
	if err != nil {
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return errors.Join(ports.ErrUncertain, err)
	}
	return c.settle(ctx, session, attempt, domain.Running, outcome)
}

func (c *Coordinator) Resume(ctx context.Context, sessionID string, oneShot bool) error {
	c.mu.Lock()
	session, err := c.store.Get(ctx, sessionID)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	provider, err := c.provider(session.Provider)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	caps := provider.Capabilities()
	capsVerified := caps.CanResume && caps.SupportsDenial && caps.SupportsSettlement
	if err := domain.CanResume(session, c.clock.Now(), capsVerified, oneShot); err != nil {
		c.mu.Unlock()
		return err
	}
	if !oneShot && (!caps.CanAutonomous || !session.Consent.BackgroundAllowed) {
		c.mu.Unlock()
		return domain.ErrConsentRequired
	}
	if session.ProviderSessionID == nil || *session.ProviderSessionID == "" {
		c.mu.Unlock()
		return ports.ErrUncertain
	}
	now := c.clock.Now()
	attempt, claimed, err := c.store.ClaimDue(ctx, ports.ClaimRequest{
		SessionID: session.ID, ExpectedRevision: session.Revision, OwnerEpoch: c.ownerEpoch,
		LeaseDuration: attemptLease, Now: now,
	})
	if err != nil || !claimed {
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return ports.ErrConflict
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.active[session.ID] = cancel
	c.mu.Unlock()
	defer c.finishRun(session.ID, cancel)

	if err := c.store.MarkLaunching(runCtx, attempt); err != nil {
		if runCtx.Err() != nil {
			return c.finishCancelled(attempt)
		}
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return err
	}
	if runCtx.Err() != nil {
		return c.finishCancelled(attempt)
	}
	req := ports.ResumeRequest{
		SessionID: session.ID, ProviderSessionID: *session.ProviderSessionID,
		Attempt: attempt, Context: session.Context,
		OnStart: func(identity domain.ProcessIdentity) error { return c.store.RecordIdentity(runCtx, attempt, identity) },
	}
	outcome, err := provider.Resume(runCtx, req, nil)
	if runCtx.Err() != nil {
		return c.finishCancelled(attempt)
	}
	if err != nil {
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return errors.Join(ports.ErrUncertain, err)
	}
	outcome.Evidence.ExpectedSessionID = *session.ProviderSessionID
	if outcome.Evidence.ConfirmedSessionID != outcome.Evidence.ExpectedSessionID {
		outcome.Evidence.TurnCompleted = false
		outcome.Evidence.Failure = domain.Failure{Category: domain.MalformedOutput, Code: "resume_identity_mismatch"}
	}
	return c.settle(ctx, session, attempt, domain.Resuming, outcome)
}

func (c *Coordinator) Cancel(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, err := c.store.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, err := c.store.Cancel(ctx, sessionID, session.Revision); err != nil {
		return err
	}
	if cancel := c.active[sessionID]; cancel != nil {
		cancel()
	}
	return nil
}

func (c *Coordinator) provider(name string) (ports.Provider, error) {
	provider := c.providers[name]
	if provider == nil {
		return nil, ports.ErrUnsupported
	}
	return provider, nil
}

func (c *Coordinator) settle(ctx context.Context, session domain.Session, attempt domain.Attempt, state domain.State, outcome ports.Outcome) error {
	evidence := outcome.Evidence
	evidence.ObservedAt = c.clock.Now()
	if !evidence.Settled {
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, evidence)
		return ports.ErrUncertain
	}
	disposition, err := domain.Decide(state, evidence)
	if err != nil {
		return err
	}
	var nextRetryAt *time.Time
	if disposition.State == domain.QuotaExhausted {
		priorResumes := session.RetryCount
		if state == domain.Resuming && priorResumes > 0 {
			priorResumes--
		}
		deadline := time.Time{}
		if session.DeadlineAt != nil {
			deadline = *session.DeadlineAt
		}
		decision := domain.NextRetry(c.clock.Now(), evidence.Failure, priorResumes, session.RetryPolicy, 0, deadline)
		if decision.Blocked {
			disposition.State = domain.NeedsAttention
			disposition.Blocker = decision.Reason
		} else {
			nextRetryAt = &decision.EligibleAt
		}
	}
	transition := domain.Transition{
		ExpectedState: state, Destination: disposition.State, AttemptID: attempt.ID,
		Fence: attempt.Fence, Source: "coordinator", Evidence: evidence,
	}
	if err := c.store.FinishAttempt(ctx, ports.AttemptResult{
		AttemptID: attempt.ID, Fence: attempt.Fence, FinishedAt: evidence.ObservedAt,
		OutcomeCode: string(disposition.TurnOutcome), ProviderSessionID: evidence.ConfirmedSessionID,
		NextRetryAt: nextRetryAt, Transition: transition,
	}); err != nil {
		current, getErr := c.store.Get(context.WithoutCancel(ctx), session.ID)
		if getErr == nil && current.CancelRequested {
			return c.finishCancelled(attempt)
		}
		return err
	}
	if disposition.State == domain.QuotaExhausted {
		waitingEvidence := evidence
		waitingEvidence.ExpectedSessionID = ""
		_, err = c.store.Apply(ctx, domain.Transition{
			ExpectedState: domain.QuotaExhausted, Destination: domain.WaitingForReset,
			AttemptID: attempt.ID, Fence: attempt.Fence, Source: "coordinator", Evidence: waitingEvidence,
		})
	}
	return err
}

func (c *Coordinator) finishCancelled(attempt domain.Attempt) error {
	ctx := context.Background()
	session, err := c.store.Get(ctx, attempt.SessionID)
	if err != nil {
		return err
	}
	if domain.IsTerminal(session.State) {
		return c.store.FinishAttempt(ctx, ports.AttemptResult{AttemptID: attempt.ID, Fence: attempt.Fence, FinishedAt: c.clock.Now(), OutcomeCode: "cancelled"})
	}
	from := domain.Running
	if attempt.Kind == domain.AttemptKindResume {
		from = domain.Resuming
	}
	evidence := domain.Evidence{ObservedAt: c.clock.Now()}
	return c.store.FinishAttempt(ctx, ports.AttemptResult{
		AttemptID: attempt.ID, Fence: attempt.Fence, FinishedAt: evidence.ObservedAt, OutcomeCode: "cancelled",
		Transition: domain.Transition{ExpectedState: from, Destination: domain.Cancelled, AttemptID: attempt.ID, Fence: attempt.Fence, Source: "cancellation", Evidence: evidence},
	})
}

func (c *Coordinator) finishRun(sessionID string, cancel context.CancelFunc) {
	c.mu.Lock()
	delete(c.active, sessionID)
	c.mu.Unlock()
	cancel()
}
