package hooks

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func parseAGY(provider string, data []byte) (ports.Event, error) {
	kind := strings.TrimPrefix(provider, "agy/")
	if kind != "Stop" && kind != "PreInvocation" && kind != "PostInvocation" {
		return ports.Event{}, ports.ErrUnsupported
	}
	var input struct {
		ConversationID    string   `json:"conversationId"`
		WorkspacePaths    []string `json:"workspacePaths"`
		InvocationNum     *int     `json:"invocationNum"`
		InitialNumSteps   *int     `json:"initialNumSteps"`
		FullyIdle         *bool    `json:"fullyIdle"`
		TerminationReason string   `json:"terminationReason"`
	}
	if err := decodePayload(data, &input); err != nil {
		return ports.Event{}, fmt.Errorf("invalid hook payload")
	}
	if input.ConversationID == "" || len(input.WorkspacePaths) == 0 || !filepath.IsAbs(input.WorkspacePaths[0]) {
		return ports.Event{}, ports.ErrUnsupported
	}
	if kind == "Stop" && (input.FullyIdle == nil || input.TerminationReason == "") {
		return ports.Event{}, ports.ErrUnsupported
	}
	if kind != "Stop" && (input.InvocationNum == nil || input.InitialNumSteps == nil) {
		return ports.Event{}, ports.ErrUnsupported
	}
	workspace := filepath.Clean(input.WorkspacePaths[0])
	return ports.Event{Kind: kind, Source: "hook", Provider: "agy", ProviderSessionID: input.ConversationID, ContextID: workspace, Workspace: workspace, ObservedAt: time.Now().UTC()}, nil
}
