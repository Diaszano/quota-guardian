package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

const maxEvents = 4096

func Parse(stdout, stderr []byte) ([]ports.Event, error) {
	if len(stdout) > maxOutput || len(stderr) > maxOutput {
		return nil, fmt.Errorf("provider output exceeds limit")
	}
	var events []ports.Event
	terminal := 0
	for _, line := range bytes.Split(stdout, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if len(line) > maxOutput {
			return nil, fmt.Errorf("provider record exceeds limit")
		}
		if len(events) >= maxEvents {
			return nil, fmt.Errorf("provider event count exceeds limit")
		}
		var raw struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, fmt.Errorf("malformed provider event")
		}
		if raw.Type == "" {
			return nil, fmt.Errorf("provider event missing type")
		}
		switch raw.Type {
		case "thread.started", "turn.started", "turn.completed", "turn.failed", "item.started", "item.completed", "item.updated", "error":
		default:
			return nil, fmt.Errorf("unknown provider event")
		}
		if raw.Type == "turn.completed" || raw.Type == "turn.failed" {
			terminal++
			if terminal > 1 {
				return nil, fmt.Errorf("multiple terminal turn events")
			}
		}
		evidence := domain.Evidence{ConfirmedSessionID: raw.ThreadID, ObservedAt: time.Now().UTC()}
		if raw.Type == "turn.completed" {
			evidence.TurnCompleted = true
		}
		events = append(events, ports.Event{Kind: raw.Type, Source: "jsonl", Provider: "codex", ProviderSessionID: raw.ThreadID, Evidence: evidence, ObservedAt: evidence.ObservedAt})
	}
	if terminal != 1 {
		return nil, fmt.Errorf("terminal turn event missing")
	}
	return events, nil
}
