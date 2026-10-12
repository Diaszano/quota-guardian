package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

const maxOutput = 1 << 20

type Provider struct {
	runner       ports.ProcessRunner
	context      domain.ExecutionContext
	capabilities ports.Capabilities
}

func New(runner ports.ProcessRunner, execution domain.ExecutionContext, evidence ports.Capabilities) *Provider {
	// Codex has no documented, version-stable quota/permission evidence parser here.
	evidence.SupportsDenial = false
	evidence.CanAutonomous = false
	return &Provider{runner: runner, context: execution, capabilities: evidence}
}
func (*Provider) Name() string                       { return "codex" }
func (p *Provider) Version() string                  { return p.capabilities.Version }
func (p *Provider) Capabilities() ports.Capabilities { return p.capabilities }

func (p *Provider) Start(ctx context.Context, req ports.StartRequest, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	if p.runner == nil || !p.capabilities.CanStart {
		return ports.Outcome{}, ports.ErrUnsupported
	}
	spec := ports.CommandSpec{Path: p.context.ExecutablePath, Args: []string{"exec", "--json", "-"}, Dir: p.context.Cwd, Env: safeEnv(), Stdin: bytes.NewReader([]byte(req.Prompt))}
	return p.invoke(ctx, spec, req.OnStart, "", onRecord)
}

func (p *Provider) Resume(ctx context.Context, req ports.ResumeRequest, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	if p.runner == nil || !p.capabilities.CanResume {
		return ports.Outcome{}, ports.ErrUnsupported
	}
	if req.ProviderSessionID == "" {
		return ports.Outcome{Evidence: domain.Evidence{Failure: domain.Failure{Category: domain.MalformedOutput, Code: "missing_resume_identity"}}}, nil
	}
	spec := ports.CommandSpec{Path: p.context.ExecutablePath, Args: []string{"exec", "resume", req.ProviderSessionID, "--json", "-"}, Dir: p.context.Cwd, Env: safeEnv(), Stdin: bytes.NewReader([]byte("Continue the current task."))}
	return p.invoke(ctx, spec, req.OnStart, req.ProviderSessionID, onRecord)
}

func (p *Provider) invoke(ctx context.Context, spec ports.CommandSpec, onStart func(domain.ProcessIdentity) error, expected string, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	var stdout, stderr []byte
	result, err := p.runner.Run(ctx, spec, onStart, func(record ports.StreamRecord) error {
		dst := &stdout
		if record.Kind == ports.StreamStderr {
			dst = &stderr
		}
		if len(record.Data) > maxOutput-len(*dst) {
			return fmt.Errorf("provider output exceeds limit")
		}
		*dst = append(*dst, record.Data...)
		if onRecord != nil {
			return onRecord(record)
		}
		return nil
	})
	if err != nil {
		return ports.Outcome{}, err
	}
	events, parseErr := Parse(stdout, stderr)
	settled := result.Reaped && result.Settled && result.DescendantsReaped && result.SettlementConfidence == ports.SettlementConfirmed
	evidence := domain.Evidence{ExpectedSessionID: expected, Settled: settled, ObservedAt: time.Now().UTC()}
	for _, event := range events {
		if event.ProviderSessionID != "" {
			if evidence.ConfirmedSessionID != "" && evidence.ConfirmedSessionID != event.ProviderSessionID {
				parseErr = errors.New("conflicting thread IDs")
				break
			}
			evidence.ConfirmedSessionID = event.ProviderSessionID
		}
		evidence.TurnCompleted = evidence.TurnCompleted || event.Evidence.TurnCompleted
	}
	if evidence.TurnCompleted && evidence.ConfirmedSessionID == "" {
		evidence.TurnCompleted = false
		parseErr = errors.New("completed turn omitted thread identity")
	}
	if expected != "" && evidence.ConfirmedSessionID != expected {
		evidence.TurnCompleted = false
		parseErr = errors.New("thread identity mismatch")
	}
	switch {
	case !settled:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "execution_unsettled"}
	case parseErr != nil:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.MalformedOutput, Code: "invalid_provider_output"}
	case result.ExitCode != 0:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "provider_exit"}
	case !hasCompletion(events):
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.MalformedOutput, Code: "turn_not_completed"}
	case !evidence.TurnCompleted:
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "turn_failed"}
	}
	return ports.Outcome{Evidence: evidence, ExitCode: result.ExitCode}, nil
}

func safeEnv() []string {
	env := make([]string, 0, 4)
	for _, name := range []string{"HOME", "PATH", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
func hasCompletion(events []ports.Event) bool {
	for _, e := range events {
		if e.Kind == "turn.completed" || e.Kind == "turn.failed" {
			return true
		}
	}
	return false
}
