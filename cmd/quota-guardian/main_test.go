package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/ipc"
	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/infrastructure"
	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestCLICompletionCopy(t *testing.T) {
	got := formatSession(domain.Session{State: domain.Completed, TurnOutcome: domain.TurnOutcomeCompleted})
	if got != "Turn completed; objective completion not assessed." {
		t.Fatalf("completion copy=%q", got)
	}
	if strings.Contains(strings.ToLower(got), "objective completed") {
		t.Fatalf("objective claim in %q", got)
	}
}

type fakeCLIClient struct {
	canceled bool
	item     domain.Session
}

func (f *fakeCLIClient) Run(context.Context, ipc.RunRequest) (domain.Session, error) {
	return f.item, nil
}
func (f *fakeCLIClient) Inspect(context.Context, string) (domain.Session, error) { return f.item, nil }
func (f *fakeCLIClient) Cancel(context.Context, string) error                    { f.canceled = true; return nil }
func (f *fakeCLIClient) Output(context.Context, string, uint64) (ipc.OutputResponse, error) {
	return ipc.OutputResponse{}, nil
}

func TestCLIConsentDefaultOff(t *testing.T) {
	var payload runPayload
	if err := parseRunArgs([]string{"--provider", "test", "--prompt", "hello"}, strings.NewReader(""), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AutoResume {
		t.Fatal("background recovery consent defaulted on")
	}
}

func TestPromptMayComeFromStdin(t *testing.T) {
	var payload runPayload
	if err := parseRunArgs([]string{"--provider", "test", "--prompt", "-"}, strings.NewReader("private prompt"), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Prompt != "private prompt" {
		t.Fatalf("stdin prompt=%q", payload.Prompt)
	}
}

func TestCLICtrlCCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeCLIClient{item: domain.Session{ID: "s", State: domain.Running}}
	done := make(chan struct{})
	go func() { _ = watchRun(ctx, client, "s", time.Millisecond, io.Discard, io.Discard); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch did not exit")
	}
	if !client.canceled {
		t.Fatal("Ctrl-C path did not request cancellation")
	}
}

type cliProvider struct {
	ctx  domain.ExecutionContext
	once sync.Once
	done chan struct{}
}

func (*cliProvider) Name() string                                { return "fake" }
func (*cliProvider) Version() string                             { return "1" }
func (p *cliProvider) ExecutionContext() domain.ExecutionContext { return p.ctx }
func (*cliProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *cliProvider) Start(_ context.Context, req ports.StartRequest, onRecord func(ports.StreamRecord) error) (ports.Outcome, error) {
	if req.OnStart != nil {
		if err := req.OnStart(domain.ProcessIdentity{PID: 22, BootID: "test", StartTicks: 1}); err != nil {
			return ports.Outcome{}, err
		}
	}
	if onRecord != nil {
		_ = onRecord(ports.StreamRecord{Kind: ports.StreamStdout, Data: []byte("simulated\n")})
	}
	p.once.Do(func() { close(p.done) })
	return ports.Outcome{Evidence: domain.Evidence{ConfirmedSessionID: "fake-session", Settled: true, TurnCompleted: true}}, nil
}
func (*cliProvider) Resume(context.Context, ports.ResumeRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	return ports.Outcome{}, ports.ErrUnsupported
}

func TestCLIEndToEndWithFakeProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := t.TempDir()
	if err := os.Chmod(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	store, err := persistence.Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providerContext := domain.ExecutionContext{ExecutablePath: "/bin/fake", ProviderVersion: "1", Cwd: t.TempDir()}
	providerContext.Digest = domain.DigestContext(providerContext)
	provider := &cliProvider{ctx: providerContext, done: make(chan struct{})}
	coordinator := application.NewCoordinator(store, map[string]ports.Provider{"fake": provider}, infrastructure.NewClock())
	listener, err := ipc.Listen(ctx, runtime, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: ipc.NewHandler(coordinator, store)}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.json")
	workspace := t.TempDir()
	config, _ := json.Marshal(map[string]any{"runtime_dir": runtime, "state_dir": state, "providers": map[string]any{"fake": map[string]string{"executable": "/bin/fake", "workspace": workspace}}})
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUOTA_GUARDIAN_CONFIG", configPath)
	var stdout, stderr bytes.Buffer
	code := run(ctx, []string{"run", "--provider", "fake", "--prompt", "hello", "--json"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"turn_outcome"`) {
		t.Fatalf("unstable/missing JSON response: %s", stdout.String())
	}
	select {
	case <-provider.done:
	case <-time.After(time.Second):
		t.Fatal("fake provider did not execute")
	}
}

func TestCompositionKeepsProviderCapabilitiesClosed(t *testing.T) {
	providers := configuredProviders(configuration.Config{Providers: map[string]configuration.Provider{"agy": {Executable: "/bin/agy", Workspace: t.TempDir()}, "codex": {Executable: "/bin/codex", Workspace: t.TempDir()}}})
	for name, provider := range providers {
		caps := provider.Capabilities()
		if caps.CanStart || caps.CanResume || caps.CanAutonomous || caps.SupportsDenial {
			t.Fatalf("%s capability unexpectedly enabled: %+v", name, caps)
		}
	}
}
