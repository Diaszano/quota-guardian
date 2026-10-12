package application

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type startErrorStore struct {
	ports.SessionStore
	session domain.Session
	err     error
}

type launchBarrierStore struct {
	ports.SessionStore
	entered chan struct{}
	release chan struct{}
}

func (s launchBarrierStore) MarkLaunching(ctx context.Context, attempt domain.Attempt) error {
	close(s.entered)
	<-s.release
	return s.SessionStore.MarkLaunching(ctx, attempt)
}

func (s startErrorStore) Get(context.Context, string) (domain.Session, error) {
	return s.session, nil
}

func (s startErrorStore) ClaimStart(context.Context, ports.ClaimRequest) (domain.Attempt, bool, error) {
	return domain.Attempt{}, false, s.err
}

type startCountingProvider struct{ starts int }

func (*startCountingProvider) Name() string    { return "test" }
func (*startCountingProvider) Version() string { return "1" }
func (*startCountingProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *startCountingProvider) Start(context.Context, ports.StartRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	p.starts++
	return ports.Outcome{}, nil
}
func (*startCountingProvider) Resume(context.Context, ports.ResumeRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	return ports.Outcome{}, errors.New("unexpected resume")
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time                   { return c.now }
func (fixedClock) NewTimer(time.Duration) ports.Timer { return nil }

type testProvider struct {
	startOutcome  ports.Outcome
	resumeOutcome ports.Outcome
	startCalls    int
	resumeCalls   int
}

func (*testProvider) Name() string    { return "test" }
func (*testProvider) Version() string { return "1" }
func (*testProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, CanResume: true, CanAutonomous: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *testProvider) Start(_ context.Context, req ports.StartRequest, _ func(ports.StreamRecord) error) (ports.Outcome, error) {
	p.startCalls++
	if req.OnStart != nil {
		if err := req.OnStart(domain.ProcessIdentity{PID: 42, BootID: "boot", StartTicks: 1}); err != nil {
			return ports.Outcome{}, err
		}
	}
	return p.startOutcome, nil
}
func (p *testProvider) Resume(_ context.Context, req ports.ResumeRequest, _ func(ports.StreamRecord) error) (ports.Outcome, error) {
	p.resumeCalls++
	if req.OnStart != nil {
		if err := req.OnStart(domain.ProcessIdentity{PID: 43, BootID: "boot", StartTicks: 2}); err != nil {
			return ports.Outcome{}, err
		}
	}
	return p.resumeOutcome, nil
}

type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time                   { return c.now }
func (*mutableClock) NewTimer(time.Duration) ports.Timer { return nil }

func newTestSession(t *testing.T, store ports.SessionStore, now time.Time) domain.Session {
	t.Helper()
	ctx := domain.ExecutionContext{ExecutablePath: "/bin/agent", ProviderVersion: "1", Cwd: t.TempDir()}
	ctx.Digest = domain.DigestContext(ctx)
	session, err := store.Create(context.Background(), ports.NewSession{
		Provider: "test", ContextID: "workspace", Mode: domain.ModeManaged, Context: ctx,
		Consent: domain.Consent{Enabled: true, BackgroundAllowed: true, Revision: 1, ApprovedContextDigest: ctx.Digest, ApprovedAt: &now},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func newTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func quotaOutcome(id string) ports.Outcome {
	return ports.Outcome{Evidence: domain.Evidence{
		ConfirmedSessionID: id,
		Settled:            true,
		Failure:            domain.Failure{Category: domain.Quota},
	}}
}

func TestNoLaunchBeforeCommit(t *testing.T) {
	commitErr := errors.New("claim transaction failed")
	provider := &startCountingProvider{}
	session := domain.Session{ID: "session-1", Provider: "test", Mode: domain.ModeManaged, State: domain.Discovered, Revision: 1}
	coordinator := NewCoordinator(startErrorStore{session: session, err: commitErr}, map[string]ports.Provider{"test": provider}, fixedClock{now: time.Now()})

	err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "private prompt"})
	if !errors.Is(err, commitErr) {
		t.Fatalf("Start() error = %v, want %v", err, commitErr)
	}
	if provider.starts != 0 {
		t.Fatalf("provider started %d times after claim failed", provider.starts)
	}
}

func TestRepeatedQuotaBeforeRunning(t *testing.T) {
	now := time.Now().UTC()
	clock := &mutableClock{now: now}
	store := newTestStore(t)
	provider := &testProvider{startOutcome: quotaOutcome("thread-1"), resumeOutcome: quotaOutcome("thread-1")}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	if err := coordinator.Resume(context.Background(), session.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.WaitingForReset || got.RetryCount != 1 {
		t.Fatalf("after repeated quota: state=%s retry_count=%d", got.State, got.RetryCount)
	}
	if provider.resumeCalls != 1 {
		t.Fatalf("resume calls = %d, want 1", provider.resumeCalls)
	}
}

func TestMissingResumeIdentity(t *testing.T) {
	now := time.Now().UTC()
	clock := &mutableClock{now: now}
	store := newTestStore(t)
	provider := &testProvider{startOutcome: quotaOutcome("thread-1"), resumeOutcome: ports.Outcome{Evidence: domain.Evidence{Settled: true, TurnCompleted: true}}}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	if err := coordinator.Resume(context.Background(), session.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.NeedsAttention || got.TurnOutcome == domain.TurnOutcomeCompleted {
		t.Fatalf("unidentified resume outcome must require attention, got state=%s outcome=%s", got.State, got.TurnOutcome)
	}
}

func TestResumeCompletionRequiresMatchingIdentity(t *testing.T) {
	now := time.Now().UTC()
	clock := &mutableClock{now: now}
	store := newTestStore(t)
	provider := &testProvider{
		startOutcome:  quotaOutcome("thread-1"),
		resumeOutcome: ports.Outcome{Evidence: domain.Evidence{ConfirmedSessionID: "thread-1", Settled: true, TurnCompleted: true}},
	}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, clock)
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	if err := coordinator.Resume(context.Background(), session.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.Completed || got.TurnOutcome != domain.TurnOutcomeCompleted {
		t.Fatalf("matching settled resume: state=%s outcome=%s", got.State, got.TurnOutcome)
	}
}

func TestSoftDenialWins(t *testing.T) {
	now := time.Now().UTC()
	store := newTestStore(t)
	provider := &testProvider{startOutcome: ports.Outcome{Evidence: domain.Evidence{Settled: true, TurnCompleted: true, PermissionBlocked: true}}}
	session := newTestSession(t, store, now)
	coordinator := NewCoordinator(store, map[string]ports.Provider{"test": provider}, &mutableClock{now: now})
	if err := coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.NeedsAttention || got.TurnOutcome != domain.TurnOutcomePermissionBlocked {
		t.Fatalf("permission denial state=%s outcome=%s", got.State, got.TurnOutcome)
	}
}

func TestContextChangeBlocksResume(t *testing.T) {
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
	consent.Revision++
	consent.ApprovedContextDigest = "changed-context"
	if _, err := store.SetConsent(context.Background(), session.ID, waiting.Revision, consent); err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	if err := coordinator.Resume(context.Background(), session.ID, false); err == nil {
		t.Fatal("Resume() succeeded after the approved context changed")
	}
	if provider.resumeCalls != 0 {
		t.Fatalf("provider resumed %d times with changed context", provider.resumeCalls)
	}
}

func TestCancelledRequestCannotLaunch(t *testing.T) {
	now := time.Now().UTC()
	baseStore := newTestStore(t)
	session := newTestSession(t, baseStore, now)
	barrier := launchBarrierStore{SessionStore: baseStore, entered: make(chan struct{}), release: make(chan struct{})}
	provider := &testProvider{startOutcome: ports.Outcome{Evidence: domain.Evidence{Settled: true, TurnCompleted: true}}}
	coordinator := NewCoordinator(barrier, map[string]ports.Provider{"test": provider}, &mutableClock{now: now})
	startDone := make(chan error, 1)
	go func() {
		startDone <- coordinator.Start(context.Background(), ports.StartRequest{SessionID: session.ID, Prompt: "first"})
	}()
	<-barrier.entered
	if err := coordinator.Cancel(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	got, err := baseStore.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if provider.startCalls != 0 || got.State != domain.Cancelled {
		t.Fatalf("start_calls=%d state=%s, want 0/CANCELLED", provider.startCalls, got.State)
	}
}
