package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/infrastructure"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestReadinessAfterReconcile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := application.Recover(ctx, store, "boot", now); err != nil {
		t.Fatal(err)
	}
	clock := infrastructure.NewClock()
	coordinator := application.NewCoordinator(store, nil, clock)
	scheduler := application.NewScheduler(store, coordinator, clock, nil, 1)
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-scheduler.Ready():
	case <-time.After(time.Second):
		t.Fatal("scheduler did not become ready")
	}
	if scheduler.ReadyError() != nil || scheduler.LastProgress().IsZero() {
		t.Fatalf("scheduler readiness error=%v progress=%v", scheduler.ReadyError(), scheduler.LastProgress())
	}
	cancel()
	<-done
}

type shutdownProvider struct{ started chan struct{} }

func (*shutdownProvider) Name() string    { return "agy" }
func (*shutdownProvider) Version() string { return "test" }
func (*shutdownProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *shutdownProvider) Start(ctx context.Context, _ ports.StartRequest, _ func(ports.StreamRecord) error) (ports.Outcome, error) {
	close(p.started)
	<-ctx.Done()
	return ports.Outcome{}, ctx.Err()
}
func (*shutdownProvider) Resume(context.Context, ports.ResumeRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	return ports.Outcome{}, errors.New("unused")
}

func TestShutdownBudget(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	execution := domain.ExecutionContext{ExecutablePath: "/bin/agy", Cwd: t.TempDir()}
	execution.Digest = domain.DigestContext(execution)
	session, err := store.Create(ctx, ports.NewSession{Provider: "agy", ContextID: "managed", Mode: domain.ModeManaged, Context: execution})
	if err != nil {
		t.Fatal(err)
	}
	provider := &shutdownProvider{started: make(chan struct{})}
	coordinator := application.NewCoordinator(store, map[string]ports.Provider{"agy": provider}, infrastructure.NewClock())
	startDone := make(chan error, 1)
	go func() { startDone <- coordinator.Start(ctx, ports.StartRequest{SessionID: session.ID}) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	if err := coordinator.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("shutdown exceeded child grace")
	}
	if err := <-startDone; !errors.Is(err, ports.ErrUncertain) {
		t.Fatalf("start result=%v", err)
	}
	final, err := store.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != domain.NeedsAttention {
		t.Fatalf("interrupted state=%s", final.State)
	}
}
