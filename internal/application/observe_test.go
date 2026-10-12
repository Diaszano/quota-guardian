package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type observeProvider struct{ calls atomic.Int32 }

func (*observeProvider) Name() string    { return "agy" }
func (*observeProvider) Version() string { return "1" }
func (*observeProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, CanResume: true, CanAutonomous: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *observeProvider) Start(context.Context, ports.StartRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	p.calls.Add(1)
	return ports.Outcome{}, nil
}
func (*observeProvider) Resume(context.Context, ports.ResumeRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	return ports.Outcome{}, nil
}

func TestManualSessionDiscovered(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	workspace := t.TempDir()
	coordinator := application.NewCoordinator(store, nil, testClock{now})
	event := ports.Event{Kind: "SessionStart", Source: "hook", Provider: "codex", ProviderSessionID: "thread-1", ContextID: workspace, Workspace: workspace, ObservedAt: now, DeliveryID: "delivery-1"}
	session, err := coordinator.Observe(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if session.Mode != domain.ModeObserved || session.State != domain.Discovered || session.ProviderSessionID == nil || *session.ProviderSessionID != "thread-1" {
		t.Fatalf("session=%+v", session)
	}
	if session.Consent.Enabled || session.Consent.BackgroundAllowed {
		t.Fatalf("observation granted consent: %+v", session.Consent)
	}
}

func TestObservationCannotTakeOver(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := &observeProvider{}
	coordinator := application.NewCoordinator(store, map[string]ports.Provider{"agy": p}, testClock{time.Now().UTC()})
	workspace := t.TempDir()
	session, err := coordinator.Observe(ctx, ports.Event{Kind: "Stop", Source: "hook", Provider: "agy", ProviderSessionID: "conversation-1", ContextID: workspace, Workspace: workspace, ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Start(ctx, ports.StartRequest{SessionID: session.ID, Prompt: "must not run"}); !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("takeover err=%v", err)
	}
	if p.calls.Load() != 0 {
		t.Fatal("provider was called for an observed session")
	}
}

func TestStaleHookCannotReopen(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	coordinator := application.NewCoordinator(store, nil, testClock{time.Now().UTC()})
	workspace := t.TempDir()
	event := ports.Event{Kind: "SessionStart", Source: "hook", Provider: "codex", ProviderSessionID: "thread-stale", ContextID: workspace, Workspace: workspace, ObservedAt: time.Now().UTC()}
	session, err := coordinator.Observe(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.Apply(ctx, domain.Transition{ExpectedRevision: session.Revision, ExpectedState: domain.Discovered, Destination: domain.NeedsAttention, EventID: "manual-check", Source: "operator", Evidence: domain.Evidence{ExpectedSessionID: session.ID}})
	if err != nil {
		t.Fatal(err)
	}
	event.ObservedAt = event.ObservedAt.Add(-time.Hour)
	again, err := coordinator.Observe(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != session.ID || again.State != changed.State {
		t.Fatalf("stale observation changed session: %+v", again)
	}
	if again.LastObservedAt == nil || !again.LastObservedAt.Equal(session.LastObservedAt.UTC()) {
		t.Fatalf("stale timestamp moved backwards: %v", again.LastObservedAt)
	}
}

func TestObservationCannotGrantConsent(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	coordinator := application.NewCoordinator(store, nil, testClock{time.Now().UTC()})
	workspace := t.TempDir()
	session, err := coordinator.Observe(ctx, ports.Event{Kind: "Stop", Source: "hook", Provider: "agy", ProviderSessionID: "conversation-permission", ContextID: workspace, Workspace: workspace, Evidence: domain.Evidence{PermissionBlocked: true}, ObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if session.Consent.Enabled || session.Consent.BackgroundAllowed {
		t.Fatalf("hook granted consent: %+v", session.Consent)
	}
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time                   { return c.now }
func (testClock) NewTimer(time.Duration) ports.Timer { return nil }
