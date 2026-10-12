package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
	"github.com/Diaszano/quota-guardian/internal/testutil"
)

type fixedJitter float64

func (j fixedJitter) Float64() float64 { return float64(j) }

func waitSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func drainTimerEvents(clock *testutil.ManualClock) {
	for {
		select {
		case <-clock.TimerCreated():
		default:
			return
		}
	}
}

func TestNoEarlyRetry(t *testing.T) {
	now := time.Now().UTC()
	clock := testutil.NewManualClock(now)
	baseStore := newTestStore(t)
	provider := &testProvider{startOutcome: quotaOutcome("thread-1")}
	session := newTestSession(t, baseStore, now)
	coordinator := NewCoordinator(baseStore, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := baseStore.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(waiting.NextRetryAt.Add(-time.Second))
	store := listSignalStore{SessionStore: baseStore, listed: make(chan struct{}, 1)}
	scheduler := NewScheduler(store, coordinator, clock, fixedJitter(0), 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-store.listed:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not query due sessions")
	}
	_, resumes, _ := provider.counts()
	if resumes != 0 {
		t.Fatalf("resume called before retry became due: %d", resumes)
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
}

func TestSchedulerIndependentSessions(t *testing.T) {
	now := time.Now().UTC()
	clock := testutil.NewManualClock(now)
	store := newTestStore(t)
	release := make(chan struct{})
	provider := &testProvider{
		startOutcome: quotaOutcome("thread-1"),
		resumeOutcome: ports.Outcome{Evidence: domain.Evidence{
			ConfirmedSessionID: "thread-1", Settled: true, TurnCompleted: true,
		}},
		resumeEntered: make(chan struct{}, 8), resumeRelease: release,
	}
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	for range 6 {
		session := newTestSession(t, store, now)
		if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
			t.Fatal(err)
		}
	}
	drainTimerEvents(clock)
	clock.Advance(2 * time.Minute)
	mode := domain.ModeManaged
	candidates, err := store.List(context.Background(), ports.SessionFilter{Mode: &mode, DueOnly: true, Now: clock.Now()})
	if err != nil || len(candidates) != 6 {
		t.Fatalf("due candidates=%d, err=%v; want 6 candidates", len(candidates), err)
	}
	for i := 1; i < len(candidates); i++ {
		prev, current := candidates[i-1], candidates[i]
		ordered := prev.NextRetryAt.Before(*current.NextRetryAt) || prev.NextRetryAt.Equal(*current.NextRetryAt) &&
			(prev.CreatedAt.Before(current.CreatedAt) || prev.CreatedAt.Equal(current.CreatedAt) && prev.ID < current.ID)
		if !ordered {
			t.Fatalf("due candidate order is not retry/creation/ID order: %s before %s", prev.ID, current.ID)
		}
	}
	scheduler := NewScheduler(store, coordinator, clock, fixedJitter(0), 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	for range 4 {
		waitSignal(t, provider.resumeEntered, "four concurrent resumes")
	}
	_, resumes, maxActive := provider.counts()
	if resumes != 4 || maxActive != 4 {
		t.Fatalf("before release: resumes=%d max_active=%d, want 4/4", resumes, maxActive)
	}
	close(release)
	for range 2 {
		waitSignal(t, provider.resumeEntered, "remaining sessions")
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	_, resumes, maxActive = provider.counts()
	if resumes != 6 || maxActive > 4 {
		t.Fatalf("resumes=%d max_active=%d, want 6 and at most 4", resumes, maxActive)
	}
}

func TestClockJump(t *testing.T) {
	now := time.Now().UTC()
	clock := testutil.NewManualClock(now)
	store := newTestStore(t)
	provider := &testProvider{
		startOutcome: quotaOutcome("thread-1"),
		resumeOutcome: ports.Outcome{Evidence: domain.Evidence{
			ConfirmedSessionID: "thread-1", Settled: true, TurnCompleted: true,
		}},
		resumeEntered: make(chan struct{}, 1),
	}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	drainTimerEvents(clock)
	storeSignals := listSignalStore{SessionStore: store, listed: make(chan struct{}, 4)}
	scheduler := NewScheduler(storeSignals, coordinator, clock, fixedJitter(0), 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	waitSignal(t, storeSignals.listed, "initial scheduler pass")
	waitSignal(t, clock.TimerCreated(), "initial poll timer")
	clock.Advance(30 * time.Second)
	waitSignal(t, storeSignals.listed, "poll after first clock advance")
	waitSignal(t, clock.TimerCreated(), "second poll timer")
	clock.Set(*waiting.NextRetryAt)
	waitSignal(t, provider.resumeEntered, "resume after clock jump")
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	_, resumes, _ := provider.counts()
	if resumes != 1 {
		t.Fatalf("resumes after clock jump = %d, want 1", resumes)
	}
}

type leaseFailureStore struct {
	ports.SessionStore
	renewed   chan struct{}
	uncertain chan struct{}
	until     time.Time
}

func (s *leaseFailureStore) Renew(_ context.Context, _ domain.Attempt, until time.Time) error {
	s.until = until
	select {
	case s.renewed <- struct{}{}:
	default:
	}
	return errors.New("lease unavailable")
}

func (s *leaseFailureStore) MarkUncertain(ctx context.Context, attempt domain.Attempt, evidence domain.Evidence) error {
	err := s.SessionStore.MarkUncertain(ctx, attempt, evidence)
	select {
	case s.uncertain <- struct{}{}:
	default:
	}
	return err
}

func TestLeaseFailureCancelsWithoutReplacement(t *testing.T) {
	now := time.Now().UTC()
	clock := testutil.NewManualClock(now)
	store := newTestStore(t)
	provider := &testProvider{startOutcome: quotaOutcome("thread-1")}
	session := newTestSession(t, store, now)
	initial := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := initial.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	drainTimerEvents(clock)
	provider.resumeEntered = make(chan struct{}, 2)
	provider.resumeRelease = make(chan struct{})
	wrapped := leaseFailureStore{SessionStore: store, renewed: make(chan struct{}, 1), uncertain: make(chan struct{}, 1)}
	coordinator := NewCoordinator(&wrapped, map[string]ports.Provider{"test": provider}, clock)
	clock.Advance(2 * time.Minute)
	listStore := listSignalStore{SessionStore: &wrapped, listed: make(chan struct{}, 3)}
	scheduler := NewScheduler(listStore, coordinator, clock, fixedJitter(0), 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	waitSignal(t, listStore.listed, "due work query")
	waitSignal(t, provider.resumeEntered, "provider resume")
	waitSignal(t, clock.TimerCreated(), "scheduler poll timer")
	waitSignal(t, clock.TimerCreated(), "lease renewal timer")
	clock.Advance(10 * time.Second)
	waitSignal(t, wrapped.renewed, "lease renewal")
	if !wrapped.until.Equal(clock.Now().Add(30 * time.Second)) {
		t.Fatalf("renewed lease until %s, want %s", wrapped.until, clock.Now().Add(30*time.Second))
	}
	waitSignal(t, wrapped.uncertain, "uncertain attempt persistence")
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.NeedsAttention {
		t.Fatalf("session state after lease failure = %s, want NEEDS_ATTENTION", got.State)
	}
	clock.Advance(30 * time.Second)
	waitSignal(t, listStore.listed, "scheduler recheck after lease failure")
	_, resumes, _ := provider.counts()
	if resumes != 1 {
		t.Fatalf("resume calls after lease loss = %d, want 1", resumes)
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestConsentRevocation(t *testing.T) {
	now := time.Now().UTC()
	clock := &mutableClock{now: now}
	store := newTestStore(t)
	provider := &testProvider{startOutcome: quotaOutcome("thread-1")}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	consent := waiting.Consent
	consent.Enabled = false
	consent.Revision++
	if _, err := store.SetConsent(context.Background(), session.ID, waiting.Revision, consent); err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	if err := coordinator.Resume(context.Background(), session.ID, false); !errors.Is(err, domain.ErrConsentRequired) {
		t.Fatalf("Resume() error = %v, want consent required", err)
	}
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, resumes, _ := provider.counts()
	if got.State != domain.NeedsAttention || got.LastError == nil || got.LastError.Code != "recovery_consent_revoked" || resumes != 0 {
		t.Fatalf("state=%s last_error=%+v resumes=%d", got.State, got.LastError, resumes)
	}
}
