package application

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func (c *Coordinator) Observe(ctx context.Context, event ports.Event) (domain.Session, error) {
	if event.Source != "hook" || event.ProviderSessionID == "" || !filepath.IsAbs(event.Workspace) || !allowedObservation(event.Provider, event.Kind) {
		return domain.Session{}, ports.ErrUnsupported
	}
	contextID := event.ContextID
	if contextID == "" {
		contextID = event.Workspace
	}
	if strings.TrimSpace(contextID) == "" {
		return domain.Session{}, ports.ErrUnsupported
	}
	now := event.ObservedAt.UTC()
	if event.ObservedAt.IsZero() {
		now = c.clock.Now().UTC()
	}
	providerSessionID := event.ProviderSessionID
	execution := domain.ExecutionContext{Cwd: filepath.Clean(event.Workspace)}
	execution.Digest = domain.DigestContext(execution)
	session, err := c.store.Create(ctx, ports.NewSession{Provider: event.Provider, ProviderSessionID: &providerSessionID, ContextID: contextID, Mode: domain.ModeObserved, Context: execution, LastObservedAt: &now})
	if err != nil {
		return domain.Session{}, err
	}
	if session.Mode == domain.ModeObserved {
		if err := c.store.RecordObserved(ctx, session.ID, now); err != nil {
			return domain.Session{}, err
		}
		return c.store.Get(ctx, session.ID)
	}
	return session, nil
}

func allowedObservation(provider, kind string) bool {
	switch provider {
	case "agy":
		return kind == "Stop" || kind == "PreInvocation" || kind == "PostInvocation"
	case "codex":
		return kind == "SessionStart"
	default:
		return false
	}
}
