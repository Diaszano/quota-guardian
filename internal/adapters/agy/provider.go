package agy

import (
	"bytes"
	"context"
	"encoding/json"
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
	return &Provider{runner: runner, context: execution, capabilities: evidence}
}
func (*Provider) Name() string                       { return "agy" }
func (p *Provider) Version() string                  { return p.capabilities.Version }
func (p *Provider) Capabilities() ports.Capabilities { return p.capabilities }

func (p *Provider) Start(ctx context.Context, req ports.StartRequest, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	if p.runner == nil || !p.capabilities.CanStart {
		return ports.Outcome{}, ports.ErrUnsupported
	}
	body, err := json.Marshal(struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}{Event: "user", Message: struct {
		Content string `json:"content"`
	}{Content: req.Prompt}})
	if err != nil {
		return ports.Outcome{}, err
	}
	body = append(body, '\n')
	spec := ports.CommandSpec{Path: p.context.ExecutablePath, Args: []string{"--input-format", "stream-json", "--output-format", "stream-json"}, Dir: p.context.Cwd, Env: safeEnv(), Stdin: bytes.NewReader(body)}
	return p.invoke(ctx, spec, req.OnStart, "", onRecord)
}

func (p *Provider) Resume(ctx context.Context, req ports.ResumeRequest, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	if p.runner == nil || !p.capabilities.CanResume {
		return ports.Outcome{}, ports.ErrUnsupported
	}
	if req.ProviderSessionID == "" {
		return ports.Outcome{Evidence: domain.Evidence{Failure: domain.Failure{Category: domain.MalformedOutput, Code: "missing_resume_identity"}}}, nil
	}
	spec := ports.CommandSpec{Path: p.context.ExecutablePath, Args: []string{"-p", "Continue the current task.", "--conversation", req.ProviderSessionID, "--output-format", "stream-json"}, Dir: p.context.Cwd, Env: safeEnv()}
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
	evidence := domain.Evidence{ExpectedSessionID: expected, Settled: result.Reaped && result.Settled, ObservedAt: time.Now().UTC()}
	for _, event := range events {
		if event.ProviderSessionID != "" {
			if evidence.ConfirmedSessionID != "" && evidence.ConfirmedSessionID != event.ProviderSessionID {
				parseErr = errors.New("conflicting provider session IDs")
				break
			}
			evidence.ConfirmedSessionID = event.ProviderSessionID
		}
		evidence.TurnCompleted = evidence.TurnCompleted || event.Evidence.TurnCompleted
	}
	if evidence.TurnCompleted && evidence.ConfirmedSessionID == "" {
		evidence.TurnCompleted = false
		parseErr = errors.New("successful turn omitted conversation identity")
	}
	if expected != "" && evidence.ConfirmedSessionID != expected {
		evidence.TurnCompleted = false
		parseErr = errors.New("resume identity mismatch")
	}
	switch {
	case !evidence.Settled:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "execution_unsettled"}
	case parseErr != nil:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.MalformedOutput, Code: "invalid_provider_output"}
	case result.ExitCode != 0:
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "provider_exit"}
	case !hasResult(events):
		evidence.TurnCompleted = false
		evidence.Failure = domain.Failure{Category: domain.MalformedOutput, Code: "missing_result"}
	case evidence.TurnCompleted && len(bytes.TrimSpace(stderr)) > 0:
		// Antigravity documents permission notices on stderr but no stable denial code. Any stderr with nominal success fails closed.
		evidence.TurnCompleted = false
		evidence.PermissionBlocked = true
		evidence.Failure = domain.Failure{Category: domain.Permission, Code: "stderr_with_success"}
	case !evidence.TurnCompleted:
		evidence.Failure = domain.Failure{Category: domain.Unknown, Code: "provider_turn_failed"}
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
func hasResult(events []ports.Event) bool {
	for _, event := range events {
		if event.Kind == "result" {
			return true
		}
	}
	return false
}
