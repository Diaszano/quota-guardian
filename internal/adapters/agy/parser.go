package agy

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
	results := 0
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
			Event          string `json:"event"`
			ConversationID string `json:"conversation_id"`
			Result         struct {
				ConversationID string `json:"conversation_id"`
				Status         string `json:"status"`
			} `json:"result"`
			Step struct {
				ConversationID string `json:"conversation_id"`
			} `json:"step_update"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, fmt.Errorf("malformed provider event")
		}
		if raw.Event == "" {
			return nil, fmt.Errorf("provider event missing type")
		}
		switch raw.Event {
		case "init", "step_update":
		case "result":
			results++
			if results > 1 {
				return nil, fmt.Errorf("multiple provider results")
			}
		default:
			return nil, fmt.Errorf("unknown provider event")
		}
		id := raw.ConversationID
		if id == "" {
			id = raw.Result.ConversationID
		}
		if id == "" {
			id = raw.Step.ConversationID
		}
		evidence := domain.Evidence{ConfirmedSessionID: id, ObservedAt: time.Now().UTC()}
		if raw.Event == "result" && raw.Result.Status == "SUCCESS" {
			evidence.TurnCompleted = true
		}
		events = append(events, ports.Event{Kind: raw.Event, Source: "stream-json", Provider: "agy", ProviderSessionID: id, Evidence: evidence, ObservedAt: evidence.ObservedAt})
	}
	if results != 1 {
		return nil, fmt.Errorf("provider result missing")
	}
	return events, nil
}
