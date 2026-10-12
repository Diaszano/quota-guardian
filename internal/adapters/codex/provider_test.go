package codex

import (
	"bytes"
	"context"
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
		if err := onStart(domain.ProcessIdentity{PID: 8, BootID: "boot", StartTicks: 2}); err != nil {
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

func settled() ports.ProcessResult {
	return ports.ProcessResult{ExitCode: 0, Reaped: true, Settled: true, DescendantsReaped: true, SettlementConfidence: ports.SettlementConfirmed}
}
func TestThreadIdentity(t *testing.T) {
	r := &runnerStub{result: settled(), stdout: []byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n" + `{"type":"turn.completed","usage":{"output_tokens":2}}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true})
	out, err := p.Start(context.Background(), ports.StartRequest{Prompt: "do work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Evidence.ConfirmedSessionID != "thread-1" || !out.Evidence.TurnCompleted || !out.Evidence.Settled {
		t.Fatalf("outcome=%+v", out)
	}
}

func TestTurnCompletionNotObjective(t *testing.T) {
	r := &runnerStub{result: settled(), stdout: []byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n" + `{"type":"turn.completed"}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true})
	out, err := p.Start(context.Background(), ports.StartRequest{Prompt: "do work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Evidence.TurnCompleted {
		t.Fatal("provider-confirmed turn completion was lost")
	}
}

func TestPromptUsesStdinAndNotArgs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "private-sentinel")
	r := &runnerStub{result: settled(), stdout: []byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n" + `{"type":"turn.completed"}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true})
	prompt := "line one\nline \"two\""
	if _, err := p.Start(context.Background(), ports.StartRequest{Prompt: prompt}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.spec.Args, " ") != "exec --json -" || strings.Contains(strings.Join(r.spec.Args, " "), prompt) {
		t.Fatalf("prompt exposed in args: %q", r.spec.Args)
	}
	stdin, err := io.ReadAll(r.spec.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != prompt {
		t.Fatalf("stdin=%q", stdin)
	}
	if r.spec.Env == nil || strings.Contains(strings.Join(r.spec.Env, "\n"), "OPENAI_API_KEY") {
		t.Fatalf("ambient credential passed: %#v", r.spec.Env)
	}
}

func TestResumeExplicitID(t *testing.T) {
	r := &runnerStub{result: settled(), stdout: []byte(`{"type":"thread.started","thread_id":"thread-expected"}` + "\n" + `{"type":"turn.completed"}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanResume: true, SupportsDenial: true, SupportsSettlement: true})
	_, err := p.Resume(context.Background(), ports.ResumeRequest{ProviderSessionID: "thread-expected"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.spec.Args, " ") != "exec resume thread-expected --json -" {
		t.Fatalf("resume args=%q", r.spec.Args)
	}
	if strings.Contains(strings.Join(r.spec.Args, " "), "--last") || strings.Contains(strings.Join(r.spec.Args, " "), "--ephemeral") || strings.Contains(strings.Join(r.spec.Args, " "), "dangerously") {
		t.Fatalf("unsafe resume flags: %q", r.spec.Args)
	}
	input, err := io.ReadAll(r.spec.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(input) != "Continue the current task." {
		t.Fatalf("continuation stdin=%q", input)
	}
}

func TestUnknownVersionFailsClosed(t *testing.T) {
	p := New(&runnerStub{}, domain.ExecutionContext{ExecutablePath: "/bin/codex"}, ports.Capabilities{})
	if p.Capabilities().CanStart || p.Capabilities().CanResume || p.Capabilities().CanAutonomous {
		t.Fatalf("unknown version enabled: %+v", p.Capabilities())
	}
	if _, err := p.Start(context.Background(), ports.StartRequest{Prompt: "must not run"}, nil); err == nil {
		t.Fatal("unverified provider started")
	}
}

func TestDenialAndAutonomyRemainClosed(t *testing.T) {
	p := New(&runnerStub{}, domain.ExecutionContext{}, ports.Capabilities{CanStart: true, CanResume: true, CanAutonomous: true, SupportsDenial: true, SupportsSettlement: true})
	caps := p.Capabilities()
	if caps.SupportsDenial || caps.CanAutonomous {
		t.Fatalf("unsupported evidence gates opened: %+v", caps)
	}
}

func TestExternalExecutionUnsettled(t *testing.T) {
	r := &runnerStub{result: ports.ProcessResult{ExitCode: 0, Reaped: true, Settled: false, DescendantsReaped: false, SettlementConfidence: ports.SettlementUncertain}, stdout: []byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n" + `{"type":"turn.completed"}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanStart: true, SupportsSettlement: true})
	out, err := p.Start(context.Background(), ports.StartRequest{Prompt: "do work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Evidence.Settled || out.Evidence.TurnCompleted || out.Evidence.Failure.Code != "execution_unsettled" {
		t.Fatalf("unsettled execution accepted: %+v", out.Evidence)
	}
}

func TestMismatchedResumeIdentity(t *testing.T) {
	r := &runnerStub{result: settled(), stdout: []byte(`{"type":"thread.started","thread_id":"different"}` + "\n" + `{"type":"turn.completed"}` + "\n")}
	p := New(r, domain.ExecutionContext{ExecutablePath: "/bin/codex", Cwd: t.TempDir()}, ports.Capabilities{CanResume: true, SupportsSettlement: true})
	out, err := p.Resume(context.Background(), ports.ResumeRequest{ProviderSessionID: "expected"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Evidence.TurnCompleted || out.Evidence.ConfirmedSessionID != "different" || out.Evidence.Failure.Category != domain.MalformedOutput {
		t.Fatalf("identity mismatch accepted: %+v", out.Evidence)
	}
}

func TestOversizedOrMalformedStreamRejected(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte("x"), maxOutput+1), nil); err == nil {
		t.Fatal("oversized stream accepted")
	}
	if _, err := Parse([]byte("{"), nil); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestUnknownAndContradictoryEventsRejected(t *testing.T) {
	if _, err := Parse([]byte(`{"type":"future.event"}`+"\n"), nil); err == nil {
		t.Fatal("unknown event accepted")
	}
	if _, err := Parse([]byte(`{"type":"turn.completed"}`+"\n"+`{"type":"turn.failed"}`+"\n"), nil); err == nil {
		t.Fatal("contradictory terminal events accepted")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"type":"thread.started","thread_id":"id"}`), []byte(nil))
	f.Add([]byte("{"), []byte("error"))
	f.Fuzz(func(t *testing.T, stdout, stderr []byte) {
		if len(stdout) > maxOutput+1 || len(stderr) > maxOutput+1 {
			return
		}
		_, _ = Parse(stdout, stderr)
	})
}
