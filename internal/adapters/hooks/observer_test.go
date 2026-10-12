package hooks

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

type observationClient func(context.Context, ports.Event) error

func (f observationClient) Observe(ctx context.Context, event ports.Event) error {
	return f(ctx, event)
}

func TestObserverDaemonDown(t *testing.T) {
	var out bytes.Buffer
	input := `{"session_id":"s1","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup","transcript_path":"secret"}`
	if err := Handle(context.Background(), "codex/SessionStart", strings.NewReader(input), &out, observationClient(func(context.Context, ports.Event) error { return errors.New("offline") })); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{}\n" {
		t.Fatalf("response %q", out.String())
	}
}

func TestObserverDeadline(t *testing.T) {
	var out bytes.Buffer
	input := `{"session_id":"s1","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup"}`
	started := time.Now()
	_ = Handle(context.Background(), "codex/SessionStart", strings.NewReader(input), &out, observationClient(func(ctx context.Context, _ ports.Event) error { <-ctx.Done(); return ctx.Err() }))
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("hook exceeded deadline: %s", elapsed)
	}
	if out.String() != "{}\n" {
		t.Fatalf("response %q", out.String())
	}
}

func TestObserverIgnoresTranscriptAndUsesNeutralProviderResponse(t *testing.T) {
	var out bytes.Buffer
	input := `{"conversationId":"c1","workspacePaths":["/tmp"],"fullyIdle":true,"terminationReason":"complete","transcript":"private"}`
	var got ports.Event
	_ = Handle(context.Background(), "agy/Stop", strings.NewReader(input), &out, observationClient(func(_ context.Context, event ports.Event) error { got = event; return nil }))
	if got.ProviderSessionID != "c1" || got.Workspace != "/tmp" || got.Source != "hook" {
		t.Fatalf("event %+v", got)
	}
	if out.String() != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("response %q", out.String())
	}
}

func TestObserverRejectsOversizedInput(t *testing.T) {
	var out bytes.Buffer
	payload := `{"session_id":"s1","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup","ignored":"` + strings.Repeat("x", maxInput) + `"}`
	called := false
	_ = Handle(context.Background(), "codex/SessionStart", strings.NewReader(payload), &out, observationClient(func(context.Context, ports.Event) error { called = true; return nil }))
	if called {
		t.Fatal("oversized input was sent")
	}
	if out.String() != "{}\n" {
		t.Fatalf("response %q", out.String())
	}
}
