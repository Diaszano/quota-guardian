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

var (
	errLeaseRenewal = errors.New("attempt lease renewal failed")
	errUserCancel   = errors.New("session cancellation requested")
)

type Coordinator struct {
	store       ports.SessionStore
	providers   map[string]ports.Provider
	clock       ports.Clock
	jitter      ports.Jitter
	ownerEpoch  string
	mu          sync.Mutex
	submitMu    sync.Mutex
	active      map[string]context.CancelCauseFunc
	outputMu    sync.Mutex
	outputs     map[string]*OutputBuffer
	outputOrder []string
}

// Submit creates an idempotent managed session and starts only its first accepted submission.
func (c *Coordinator) Submit(ctx context.Context, input ports.NewSession, prompt string) (domain.Session, error) {
	c.submitMu.Lock()
	defer c.submitMu.Unlock()
	provider, err := c.provider(input.Provider)
	if err != nil {
		return domain.Session{}, err
	}
	contextProvider, ok := provider.(interface {
		ExecutionContext() domain.ExecutionContext
	})
	if !ok {
		return domain.Session{}, ports.ErrUnsupported
	}
	input.Context = contextProvider.ExecutionContext()
	session, err := c.store.Create(ctx, input)
	if err != nil {
		return domain.Session{}, err
	}
	if session.State == domain.Discovered {
		claimed := make(chan struct{}, 1)
		finished := make(chan error, 1)
		go func() {
			finished <- c.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: prompt, OnClaim: func() { claimed <- struct{}{} }})
		}()
		select {
		case <-claimed:
		case startErr := <-finished:
			if startErr != nil && !errors.Is(startErr, ports.ErrConflict) {
				return session, startErr
			}
		case <-ctx.Done():
			latest, getErr := c.store.Get(context.Background(), session.ID)
			if getErr != nil {
				return session, getErr
			}
			if latest.State == domain.Discovered {
				return latest, ctx.Err()
			}
			session = latest
		}
		if session.State == domain.Discovered {
			session, err = c.store.Get(context.Background(), session.ID)
		}
	}
	return session, err
}

func NewCoordinator(store ports.SessionStore, providers map[string]ports.Provider, clock ports.Clock) *Coordinator {
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		copy(epoch[:], fmt.Sprintf("fallback-%d", time.Now().UnixNano()))
	}
	return &Coordinator{store: store, providers: providers, clock: clock, ownerEpoch: hex.EncodeToString(epoch[:]), active: make(map[string]context.CancelCauseFunc), outputs: make(map[string]*OutputBuffer)}
}

func (c *Coordinator) Output(sessionID string) *OutputBuffer {
	c.outputMu.Lock()
	defer c.outputMu.Unlock()
	return c.outputs[sessionID]
}

func (c *Coordinator) output(sessionID string) *OutputBuffer {
	c.outputMu.Lock()
	defer c.outputMu.Unlock()
	if c.outputs[sessionID] == nil {
		c.outputs[sessionID] = NewOutputBuffer()
		c.outputOrder = append(c.outputOrder, sessionID)
		// shortcut: retain output for only the latest 64 sessions, increase when persistent output attachment is added.
		if len(c.outputOrder) > 64 {
			delete(c.outputs, c.outputOrder[0])
			c.outputOrder = c.outputOrder[1:]
		}
	}
	return c.outputs[sessionID]
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
	if req.OnClaim != nil {
		req.OnClaim()
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	c.active[session.ID] = cancel
	c.mu.Unlock()
	defer c.finishRun(session.ID, cancel)
	stopRenewal := c.renewLease(runCtx, cancel, attempt)
	defer stopRenewal()

	if err := c.store.MarkLaunching(runCtx, attempt); err != nil {
		if runCtx.Err() != nil {
			return c.finishInterrupted(ctx, attempt, runCtx)
		}
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return err
	}
	if runCtx.Err() != nil {
		return c.finishInterrupted(ctx, attempt, runCtx)
	}
	req.Attempt, req.Context = attempt, session.Context
	req.OnStart = func(identity domain.ProcessIdentity) error { return c.store.RecordIdentity(runCtx, attempt, identity) }
	output := c.output(session.ID)
	outcome, err := provider.Start(runCtx, req, func(record ports.StreamRecord) error { output.Publish(record); return nil })
	if runCtx.Err() != nil {
		return c.finishInterrupted(ctx, attempt, runCtx)
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
		c.blockResume(ctx, session, err)
		return err
	}
	caps := provider.Capabilities()
	capsVerified := caps.CanResume && caps.SupportsDenial && caps.SupportsSettlement
	if err := domain.CanResume(session, c.clock.Now(), capsVerified, oneShot); err != nil {
		c.mu.Unlock()
		c.blockResume(ctx, session, err)
		return err
	}
	if !oneShot && !caps.CanAutonomous {
		c.mu.Unlock()
		c.blockResume(ctx, session, domain.ErrCapabilitiesUnverified)
		return domain.ErrCapabilitiesUnverified
	}
	if !oneShot && !session.Consent.BackgroundAllowed {
		c.mu.Unlock()
		c.blockResume(ctx, session, domain.ErrConsentRequired)
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
	runCtx, cancel := context.WithCancelCause(ctx)
	c.active[session.ID] = cancel
	c.mu.Unlock()
	defer c.finishRun(session.ID, cancel)
	stopRenewal := c.renewLease(runCtx, cancel, attempt)
	defer stopRenewal()

	if err := c.store.MarkLaunching(runCtx, attempt); err != nil {
		if runCtx.Err() != nil {
			return c.finishInterrupted(ctx, attempt, runCtx)
		}
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, domain.Evidence{ObservedAt: c.clock.Now()})
		return err
	}
	if runCtx.Err() != nil {
		return c.finishInterrupted(ctx, attempt, runCtx)
	}
	req := ports.ResumeRequest{
		SessionID: session.ID, ProviderSessionID: *session.ProviderSessionID,
		Attempt: attempt, Context: session.Context,
		OnStart: func(identity domain.ProcessIdentity) error { return c.store.RecordIdentity(runCtx, attempt, identity) },
	}
	output := c.output(session.ID)
	outcome, err := provider.Resume(runCtx, req, func(record ports.StreamRecord) error { output.Publish(record); return nil })
	if runCtx.Err() != nil {
		return c.finishInterrupted(ctx, attempt, runCtx)
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
		cancel(errUserCancel)
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

func (c *Coordinator) blockResume(ctx context.Context, session domain.Session, reason error) {
	code := ""
	switch {
	case errors.Is(reason, domain.ErrConsentRequired):
		code = "recovery_consent_revoked"
	case errors.Is(reason, domain.ErrContextMismatch):
		code = "execution_context_changed"
	case errors.Is(reason, domain.ErrCapabilitiesUnverified):
		code = "provider_capability_unverified"
	case errors.Is(reason, ports.ErrUnsupported):
		code = "provider_unavailable"
	default:
		return
	}
	if session.State != domain.WaitingForReset && session.State != domain.ReadyToResume {
		return
	}
	_, _ = c.store.Apply(context.WithoutCancel(ctx), domain.Transition{
		ExpectedState: session.State, Destination: domain.NeedsAttention, Source: "coordinator",
		Evidence: domain.Evidence{ExpectedSessionID: session.ID, Failure: domain.Failure{Category: domain.Unknown, Code: code}, ObservedAt: c.clock.Now()},
	})
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
		decision := domain.NextRetry(c.clock.Now(), evidence.Failure, priorResumes, session.RetryPolicy, c.jitterValue(), deadline)
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

func (c *Coordinator) finishInterrupted(ctx context.Context, attempt domain.Attempt, runCtx context.Context) error {
	if errors.Is(context.Cause(runCtx), errLeaseRenewal) {
		evidence := domain.Evidence{ObservedAt: c.clock.Now()}
		_ = c.store.MarkUncertain(context.WithoutCancel(ctx), attempt, evidence)
		return ports.ErrUncertain
	}
	return c.finishCancelled(attempt)
}

func (c *Coordinator) renewLease(ctx context.Context, cancel context.CancelCauseFunc, attempt domain.Attempt) context.CancelFunc {
	renewCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			timer := c.clock.NewTimer(10 * time.Second)
			select {
			case <-renewCtx.Done():
				timer.Stop()
				return
			case <-timer.C():
				if err := c.store.Renew(renewCtx, attempt, c.clock.Now().Add(attemptLease)); err != nil && renewCtx.Err() == nil {
					cancel(errors.Join(errLeaseRenewal, err))
					return
				}
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}

func (c *Coordinator) jitterValue() float64 {
	c.mu.Lock()
	jitter := c.jitter
	c.mu.Unlock()
	if jitter == nil {
		return 0
	}
	return jitter.Float64()
}

func (c *Coordinator) setJitter(jitter ports.Jitter) {
	c.mu.Lock()
	c.jitter = jitter
	c.mu.Unlock()
}

func (c *Coordinator) finishRun(sessionID string, cancel context.CancelCauseFunc) {
	c.mu.Lock()
	delete(c.active, sessionID)
	c.mu.Unlock()
	cancel(context.Canceled)
}
