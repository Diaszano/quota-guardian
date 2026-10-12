package hooks

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func parseCodex(provider string, data []byte) (ports.Event, error) {
	if provider != "codex/SessionStart" {
		return ports.Event{}, ports.ErrUnsupported
	}
	var input struct {
		SessionID     string `json:"session_id"`
		Cwd           string `json:"cwd"`
		HookEventName string `json:"hook_event_name"`
		Source        string `json:"source"`
	}
	if err := decodePayload(data, &input); err != nil {
		return ports.Event{}, fmt.Errorf("invalid hook payload")
	}
	if input.HookEventName != "SessionStart" || input.SessionID == "" || !filepath.IsAbs(input.Cwd) {
		return ports.Event{}, ports.ErrUnsupported
	}
	switch input.Source {
	case "startup", "resume", "clear", "compact":
	default:
		return ports.Event{}, ports.ErrUnsupported
	}
	workspace := filepath.Clean(input.Cwd)
	return ports.Event{Kind: "SessionStart", Source: "hook", Provider: "codex", ProviderSessionID: input.SessionID, ContextID: workspace, Workspace: workspace, ObservedAt: time.Now().UTC()}, nil
}

func normalize(provider string, data []byte) (ports.Event, error) {
	switch {
	case provider == "codex/SessionStart":
		return parseCodex(provider, data)
	case len(provider) > 4 && provider[:4] == "agy/":
		return parseAGY(provider, data)
	default:
		return ports.Event{}, ports.ErrUnsupported
	}
}
