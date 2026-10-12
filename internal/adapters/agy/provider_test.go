package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type runnerStub struct {
	spec           ports.CommandSpec
	result         ports.ProcessResult
	stdout, stderr []byte
	err            error
}

func (r *runnerStub) Run(_ context.Context, s ports.CommandSpec, onStart func(domain.ProcessIdentity) error, onRecord func(ports.StreamRecord) error) (ports.ProcessResult, error) {
	r.spec = s
	if onStart != nil {
		if err := onStart(domain.ProcessIdentity{PID: 7, BootID: "boot", StartTicks: 4}); err != nil {
			return ports.ProcessResult{}, err
		}
	}
	if onRecord != nil {
		if len(r.stdout) > 0 {
			if err := onRecord(ports.StreamRecord{Kind: ports.StreamStdout, Data: r.stdout}); err != nil {
				return ports.ProcessResult{}, err
			}
		}
		if len(r.stderr) > 0 {
			if err := onRecord(ports.StreamRecord{Kind: ports.StreamStderr, Data: r.stderr}); err != nil {
				return ports.ProcessResult{}, err
			}
		}
	}
	return r.result, r.err
}

func TestStreamInput(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sentinel-secret")
	line := `{"event":"result","result":{"conversation_id":"conv-1","status":"SUCCESS"}}` + "\n"
	r := &runnerStub{result: ports.ProcessResult{ExitCode: 0, Reaped: true, Settled: true}, stdout: []byte(line)}
	ctx := domain.ExecutionContext{ExecutablePath: "/bin/agy", Cwd: t.TempDir()}
	p := New(r, ctx, ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true})
	prompt := "line one\nline \"two\""
	_, err := p.Start(context.Background(), ports.StartRequest{Prompt: prompt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.spec.Args, " "), prompt) || strings.Contains(strings.Join(r.spec.Args, " "), "-p") {
		t.Fatalf("prompt leaked into argv: %q", r.spec.Args)
	}
	if strings.Join(r.spec.Args, " ") != "--input-format stream-json --output-format stream-json" {
		t.Fatalf("args=%q", r.spec.Args)
	}
	if r.spec.Env == nil || strings.Contains(strings.Join(r.spec.Env, "\n"), "OPENAI_API_KEY") {
		t.Fatalf("provider received ambient secrets: %#v", r.spec.Env)
	}
	body, err := io.ReadAll(r.spec.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "user" || got.Message.Content != prompt {
		t.Fatalf("stdin event=%+v", got)
	}
}

func TestSoftDenialExitZero(t *testing.T) {
	r := &runnerStub{result: ports.ProcessResult{ExitCode: 0, Reaped: true, Settled: true}, stdout: []byte(`{"event":"result","result":{"conversation_id":"conv-1","status":"SUCCESS","response":"done"}}` + "\n"), stderr: []byte("Permission notice: command requires approval\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/agy", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true})
	out, err := p.Start(context.Background(), ports.StartRequest{Prompt: "task"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Evidence.PermissionBlocked || out.Evidence.TurnCompleted {
		t.Fatalf("soft denial was treated as success: %+v", out.Evidence)
	}
}

func TestResumeQuotaBeforeInit(t *testing.T) {
	r := &runnerStub{result: ports.ProcessResult{ExitCode: 0, Reaped: true, Settled: true}, stdout: []byte(`{"event":"result","result":{"status":"SUCCESS"}}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/agy", Cwd: t.TempDir()}, ports.Capabilities{CanResume: true, SupportsDenial: true, SupportsSettlement: true})
	out, err := p.Resume(context.Background(), ports.ResumeRequest{ProviderSessionID: "expected"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Evidence.ConfirmedSessionID != "" || out.Evidence.TurnCompleted || out.Evidence.Failure.Category != domain.MalformedOutput {
		t.Fatalf("unidentified result accepted: %+v", out.Evidence)
	}
	if !containsPair(r.spec.Args, "--conversation", "expected") {
		t.Fatalf("explicit resume ID missing from argv: %q", r.spec.Args)
	}
}

func TestUnknownVersionCapabilitiesClosed(t *testing.T) {
	p := New(&runnerStub{}, domain.ExecutionContext{ExecutablePath: "/bin/agy"}, ports.Capabilities{})
	if p.Capabilities().CanStart || p.Capabilities().CanResume || p.Capabilities().CanAutonomous {
		t.Fatalf("unknown capability enabled: %+v", p.Capabilities())
	}
}

func TestClassificationRequiresSettledMatchingIdentity(t *testing.T) {
	stdout := []byte(`{"event":"init","conversation_id":"expected"}` + "\n" + `{"event":"result","result":{"conversation_id":"expected","status":"SUCCESS"}}` + "\n")
	events, err := Parse(stdout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].ProviderSessionID != "expected" || !events[1].Evidence.TurnCompleted {
		t.Fatalf("events=%+v", events)
	}
	if _, err := Parse([]byte("not-json\n"), nil); err == nil {
		t.Fatal("malformed event accepted")
	}
	if _, err := Parse([]byte(`{"event":"future_event"}`+"\n"), nil); err == nil {
		t.Fatal("unknown event accepted")
	}
	if _, err := Parse(bytes.Repeat([]byte("x"), maxOutput+1), nil); err == nil {
		t.Fatal("oversized event accepted")
	}
}

func TestOversizeRecordFailsClosed(t *testing.T) {
	r := &runnerStub{result: ports.ProcessResult{Reaped: true, Settled: true}, stdout: bytes.Repeat([]byte("x"), maxOutput+1)}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/agy", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true})
	if _, err := p.Start(context.Background(), ports.StartRequest{Prompt: "x"}, nil); err == nil {
		t.Fatal("oversized record did not fail")
	}
}

func TestEventCountBounded(t *testing.T) {
	line := []byte(`{"event":"step_update"}` + "\n")
	stream := bytes.Repeat(line, maxEvents+1)
	if _, err := Parse(stream, nil); err == nil {
		t.Fatal("unbounded event count accepted")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"event":"init","conversation_id":"id"}`), []byte(nil))
	f.Add([]byte("{"), []byte("permission"))
	f.Fuzz(func(t *testing.T, stdout, stderr []byte) {
		if len(stdout) > maxOutput+1 || len(stderr) > maxOutput+1 {
			return
		}
		_, _ = Parse(stdout, stderr)
	})
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
