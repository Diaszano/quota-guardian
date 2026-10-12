package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/agy"
	"github.com/Diaszano/quota-guardian/internal/adapters/codex"
	"github.com/Diaszano/quota-guardian/internal/adapters/hooks"
	"github.com/Diaszano/quota-guardian/internal/adapters/ipc"
	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/adapters/process"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/infrastructure"
	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
	"github.com/Diaszano/quota-guardian/internal/infrastructure/observability"
	"github.com/Diaszano/quota-guardian/internal/infrastructure/systemd"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

const completionCopy = "Turn completed; objective completion not assessed."

type runPayload struct {
	Provider, Prompt string
	AutoResume       bool
	JSON             bool
}
type cliClient interface {
	Run(context.Context, ipc.RunRequest) (domain.Session, error)
	Inspect(context.Context, string) (domain.Session, error)
	Cancel(context.Context, string) error
	Output(context.Context, string, uint64) (ipc.OutputResponse, error)
}

type sessionView struct {
	ID                 string             `json:"id"`
	Provider           string             `json:"provider"`
	Mode               domain.SessionMode `json:"mode"`
	State              domain.State       `json:"state"`
	TurnOutcome        domain.TurnOutcome `json:"turn_outcome"`
	CapabilityEvidence string             `json:"capability_evidence"`
	RetryCount         int                `json:"retry_count"`
	NextRetryAt        *time.Time         `json:"next_retry_at,omitempty"`
	LastErrorCode      string             `json:"last_error_code,omitempty"`
}

func viewSession(s domain.Session) sessionView {
	v := sessionView{ID: s.ID, Provider: s.Provider, Mode: s.Mode, State: s.State, TurnOutcome: s.TurnOutcome, CapabilityEvidence: "unavailable", RetryCount: s.RetryCount, NextRetryAt: s.NextRetryAt}
	if s.LastError != nil {
		v.LastErrorCode = s.LastError.Code
	}
	return v
}

func main() {
	ctx, stop := signalContext()
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: quota-guardian <run|sessions|status|doctor|daemon>")
		return 2
	}
	if args[0] == "observe" {
		return observeCommand(ctx, args[1:], stdin, stdout)
	}
	configPath := os.Getenv("QUOTA_GUARDIAN_CONFIG")
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".config", "quota-guardian", "config.json")
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	if err = cfg.Validate(); err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	client := ipc.NewClient(filepath.Join(cfg.RuntimeDir, "quota-guardian", "control.sock"))
	switch args[0] {
	case "run":
		payload := runPayload{}
		if err = parseRunArgs(args[1:], stdin, &payload); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		key, err := newKey()
		if err != nil {
			fmt.Fprintln(stderr, "unable to create submission key")
			return 1
		}
		if payload.AutoResume {
			fmt.Fprintln(stderr, "Background recovery enabled for this session; the current execution-context digest is approved.")
		}
		session, err := client.Run(ctx, ipc.RunRequest{SubmissionKey: key, Provider: payload.Provider, ContextID: "default", Prompt: payload.Prompt, AutoResume: payload.AutoResume})
		if err != nil {
			fmt.Fprintln(stderr, "daemon unavailable or request failed; request was not resubmitted")
			return 1
		}
		if payload.JSON {
			_ = json.NewEncoder(stdout).Encode(viewSession(session))
			return 0
		}
		return watchRun(ctx, client, session.ID, 200*time.Millisecond, stdout, stderr)
	case "sessions":
		return sessionsCommand(ctx, args[1:], client, stdout, stderr)
	case "status":
		if err := client.Health(ctx); err != nil {
			fmt.Fprintln(stderr, "daemon unavailable")
			return 1
		}
		fmt.Fprintln(stdout, "Daemon ready.")
		return 0
	case "doctor":
		report, err := infrastructure.Doctor(ctx, cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if hasFlag(args[1:], "--json") {
			_ = json.NewEncoder(stdout).Encode(report)
		} else {
			for _, warning := range report.Warnings {
				fmt.Fprintln(stdout, "Warning:", warning)
			}
			for _, cap := range report.Capabilities {
				fmt.Fprintf(stdout, "%s: %s\n", cap.Provider, cap.Status)
			}
		}
		if !report.Ready {
			return 1
		}
		return 0
	case "daemon":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: daemon <run|install|uninstall|start|stop|status>")
			return 2
		}
		switch args[1] {
		case "run":
			return daemonRun(ctx, cfg, stdout, stderr)
		case "install":
			executable, e := os.Executable()
			if e != nil {
				fmt.Fprintln(stderr, "unable to locate executable")
				return 1
			}
			if e = systemd.Install(ctx, userUnitDir(), executable); e != nil {
				fmt.Fprintln(stderr, "service install failed:", e)
				return 1
			}
			fmt.Fprintln(stdout, "User service installed. Enable it with systemctl --user enable --now quota-guardian.service.")
			return 0
		case "uninstall":
			if e := systemd.Uninstall(ctx, userUnitDir()); e != nil {
				fmt.Fprintln(stderr, "service uninstall failed:", e)
				return 1
			}
			fmt.Fprintln(stdout, "User service unit removed.")
			return 0
		case "start", "stop", "status":
			if e := systemd.Control(ctx, args[1]); e != nil {
				fmt.Fprintln(stderr, "systemd user service command failed:", e)
				return 1
			}
			if args[1] == "status" {
				fmt.Fprintln(stdout, "User service is active.")
			} else {
				fmt.Fprintf(stdout, "User service %s requested.\n", args[1])
			}
			return 0
		default:
			fmt.Fprintln(stderr, "unknown daemon command:", args[1])
			return 2
		}
	default:
		fmt.Fprintln(stderr, "unknown command:", args[0])
		return 2
	}
}

func observeCommand(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) int {
	fs := flag.NewFlagSet("observe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "", "hook provider/event")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *provider == "" {
		return 2
	}
	configPath := os.Getenv("QUOTA_GUARDIAN_CONFIG")
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".config", "quota-guardian", "config.json")
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		_ = hooks.Handle(ctx, *provider, stdin, stdout, nil)
		return 0
	}
	if err = cfg.Validate(); err != nil {
		_ = hooks.Handle(ctx, *provider, stdin, stdout, nil)
		return 0
	}
	client := ipc.NewClient(filepath.Join(cfg.RuntimeDir, "quota-guardian", "control.sock"))
	_ = hooks.Handle(ctx, *provider, stdin, stdout, client)
	return 0
}

func loadConfig(path string) (configuration.Config, error) {
	if _, err := os.Lstat(path); err == nil {
		return configuration.Load(path)
	} else if !os.IsNotExist(err) {
		return configuration.Config{}, err
	}
	return configuration.Defaults()
}

func parseRunArgs(args []string, stdin io.Reader, out *runPayload) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&out.Provider, "provider", "", "provider name")
	fs.StringVar(&out.Prompt, "prompt", "", "prompt text or - for stdin")
	fs.BoolVar(&out.AutoResume, "auto-resume", false, "enable background recovery for this task")
	fs.BoolVar(&out.JSON, "json", false, "emit session as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected run argument")
	}
	if out.Provider == "" || out.Prompt == "" {
		return errors.New("--provider and --prompt are required")
	}
	if out.Prompt == "-" {
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20+1))
		if err != nil {
			return err
		}
		if len(b) > 1<<20 {
			return errors.New("prompt exceeds 1 MiB")
		}
		out.Prompt = string(b)
	}
	return nil
}

func watchRun(ctx context.Context, client cliClient, id string, interval time.Duration, stdout, stderr io.Writer) int {
	var cursor uint64
	for {
		if ctx.Err() != nil {
			_ = client.Cancel(context.Background(), id)
			return 130
		}
		output, err := client.Output(ctx, id, cursor)
		if err != nil {
			fmt.Fprintln(stderr, "output attachment unavailable")
			return 1
		}
		if output.Dropped > 0 {
			fmt.Fprintf(stderr, "[quota-guardian: %d output records dropped]\n", output.Dropped)
		}
		for _, event := range output.Records {
			cursor = event.Sequence
			dst := stdout
			if event.Record.Kind == ports.StreamStderr {
				dst = stderr
			}
			_, _ = dst.Write(event.Record.Data)
		}
		item, err := client.Inspect(ctx, id)
		if err != nil {
			fmt.Fprintln(stderr, "daemon connection lost; session was not resubmitted")
			return 1
		}
		if domain.IsTerminal(item.State) {
			fmt.Fprintln(stdout, formatSession(item))
			if item.State == domain.Completed {
				return 0
			}
			return 1
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = client.Cancel(context.Background(), id)
			return 130
		case <-timer.C:
		}
	}
}

func formatSession(s domain.Session) string {
	if s.State == domain.Completed && s.TurnOutcome == domain.TurnOutcomeCompleted {
		return completionCopy
	}
	result := fmt.Sprintf("Session %s: provider=%s mode=%s state=%s turn_outcome=%s retry_count=%d capability_evidence=unavailable", s.ID, s.Provider, s.Mode, s.State, s.TurnOutcome, s.RetryCount)
	if s.NextRetryAt != nil {
		result += fmt.Sprintf(" next_retry_at=%s", s.NextRetryAt.UTC().Format(time.RFC3339))
	}
	if s.LastError != nil && s.LastError.Code != "" {
		result += " blocker=" + s.LastError.Code
	}
	return result
}

func sessionsCommand(ctx context.Context, args []string, client *ipc.Client, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sessions <list|inspect|resume|cancel|auto-resume>")
		return 2
	}
	switch args[0] {
	case "list":
		items, err := client.List(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "daemon unavailable")
			return 1
		}
		for _, item := range items {
			fmt.Fprintln(stdout, formatSession(item))
		}
		return 0
	case "inspect":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "session ID required")
			return 2
		}
		item, err := client.Inspect(ctx, args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if hasFlag(args[2:], "--json") {
			_ = json.NewEncoder(stdout).Encode(viewSession(item))
		} else {
			fmt.Fprintln(stdout, formatSession(item))
		}
		return 0
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "session ID required")
			return 2
		}
		if err := client.Resume(ctx, args[1], true); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "cancel":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "session ID required")
			return 2
		}
		if err := client.Cancel(ctx, args[1]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "auto-resume":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "session ID required")
			return 2
		}
		item, err := client.Inspect(ctx, args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		enable := hasFlag(args[2:], "--enable")
		if !enable && !hasFlag(args[2:], "--disable") {
			fmt.Fprintln(stderr, "choose --enable or --disable")
			return 2
		}
		consent := item.Consent
		consent.Enabled = enable
		consent.BackgroundAllowed = enable
		if err := client.SetConsent(ctx, item.ID, consent); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, "unknown sessions command")
		return 2
	}
}
func hasFlag(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
func newKey() (string, error) {
	var b [24]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func daemonRun(ctx context.Context, cfg configuration.Config, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(cfg.RuntimeDir, 0700); err != nil {
		fmt.Fprintln(stderr, "runtime directory unavailable")
		return 1
	}
	info, err := os.Lstat(cfg.RuntimeDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		fmt.Fprintln(stderr, "unsafe runtime directory")
		return 1
	}
	if !ownedByCurrentUser(info) {
		fmt.Fprintln(stderr, "runtime directory owner mismatch")
		return 1
	}
	if err = os.MkdirAll(cfg.StateDir, 0700); err != nil {
		fmt.Fprintln(stderr, "state directory unavailable")
		return 1
	}
	stateInfo, err := os.Lstat(cfg.StateDir)
	if err != nil || !stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0 || stateInfo.Mode().Perm() != 0700 {
		fmt.Fprintln(stderr, "unsafe state directory")
		return 1
	}
	if !ownedByCurrentUser(stateInfo) {
		fmt.Fprintln(stderr, "state directory owner mismatch")
		return 1
	}
	lock, err := persistence.AcquireLock(cfg.StateDir)
	if err != nil {
		fmt.Fprintln(stderr, "daemon already owns state or lock unavailable")
		return 1
	}
	defer lock.Close()
	store, err := persistence.Open(ctx, cfg.StateDir)
	if err != nil {
		fmt.Fprintln(stderr, "state unavailable:", err)
		return 1
	}
	defer store.Close()
	telemetry, flush, telemetryErr := observability.New(ctx, cfg)
	if telemetryErr != nil {
		fmt.Fprintln(stderr, "telemetry disabled: exporter initialization failed")
		flush = func(context.Context) error { return nil }
	}
	clock := infrastructure.NewClock()
	epoch, err := newKey()
	if err != nil {
		return 1
	}
	if err = application.Recover(ctx, store, epoch, clock.Now()); err != nil {
		fmt.Fprintln(stderr, "recovery failed:", err)
		return 1
	}
	service := application.NewCoordinator(store, configuredProviders(cfg), clock)
	service.SetTelemetry(telemetry)
	scheduler := application.NewScheduler(store, service, clock, randomJitter{}, 4)
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	schedulerDone := make(chan struct{})
	var schedulerErr error
	go func() {
		schedulerErr = scheduler.Run(runCtx)
		close(schedulerDone)
	}()
	select {
	case <-scheduler.Ready():
		if scheduler.ReadyError() != nil {
			fmt.Fprintln(stderr, "scheduler readiness failed")
			_ = stopOwnedWork(service, cancelRun, schedulerDone, flush)
			return 1
		}
	case <-schedulerDone:
		fmt.Fprintln(stderr, "scheduler stopped before readiness")
		_ = stopOwnedWork(service, cancelRun, schedulerDone, flush)
		return 1
	case <-ctx.Done():
		_ = stopOwnedWork(service, cancelRun, schedulerDone, flush)
		return 0
	}
	listener, err := ipc.Listen(ctx, cfg.RuntimeDir, nil)
	if err != nil {
		_ = stopOwnedWork(service, cancelRun, schedulerDone, flush)
		fmt.Fprintln(stderr, "IPC unavailable:", err)
		return 1
	}
	handler := ipc.NewHandler(service, store)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	serveStarted := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		close(serveStarted)
		serverDone <- server.Serve(listener)
	}()
	<-serveStarted
	_ = systemd.Notify("READY=1\nSTATUS=Quota Guardian ready")
	fmt.Fprintln(stdout, "Daemon ready.")
	if systemd.WatchdogEnabled() {
		go watchdog(runCtx, scheduler, store)
	}
	var runErr error
	select {
	case <-ctx.Done():
	case <-schedulerDone:
		runErr = schedulerErr
		if runErr == nil {
			runErr = errors.New("scheduler stopped unexpectedly")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			fmt.Fprintln(stderr, "scheduler stopped unexpectedly")
		}
		cancelRun()
	case err := <-serverDone:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
			fmt.Fprintln(stderr, "daemon server failed")
		}
		cancelRun()
	}
	_ = systemd.Notify("STOPPING=1")
	serverShutdown, cancelServerShutdown := context.WithTimeout(context.Background(), 8*time.Second)
	if err := server.Shutdown(serverShutdown); err != nil {
		_ = server.Close()
	}
	cancelServerShutdown()
	shutdownErr := stopOwnedWork(service, cancelRun, schedulerDone, flush)
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return 1
	}
	if shutdownErr != nil {
		return 1
	}
	return 0
}

func stopOwnedWork(service *application.Coordinator, cancel context.CancelFunc, schedulerDone <-chan struct{}, flush func(context.Context) error) error {
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := service.Shutdown(shutdown)
	cancelShutdown()
	cancel()
	schedulerCtx, cancelScheduler := context.WithTimeout(context.Background(), 8*time.Second)
	select {
	case <-schedulerDone:
	case <-schedulerCtx.Done():
		shutdownErr = errors.Join(shutdownErr, schedulerCtx.Err())
	}
	cancelScheduler()
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 2*time.Second)
	if flush != nil {
		_ = flush(flushCtx)
	}
	cancelFlush()
	return shutdownErr
}

func watchdog(ctx context.Context, scheduler *application.Scheduler, store ports.SessionStore) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			progress := scheduler.LastProgress()
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, checkErr := store.List(checkCtx, ports.SessionFilter{Limit: 1})
			cancel()
			if watchdogHealthy(progress, time.Now(), checkErr) {
				_ = systemd.Notify("WATCHDOG=1")
			}
		}
	}
}

func watchdogHealthy(progress, now time.Time, storageErr error) bool {
	age := now.Sub(progress)
	return storageErr == nil && !progress.IsZero() && age >= 0 && age < 30*time.Second
}

func userUnitDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user")
}

func configuredProviders(cfg configuration.Config) map[string]ports.Provider {
	providers := make(map[string]ports.Provider)
	runner := process.NewRunner()
	for name, provider := range cfg.Providers {
		execution := configuration.BuildExecutionContext(provider.Executable, provider.Version, provider.Workspace)
		execution.Profile = provider.SettingsRef
		execution.Digest = domain.DigestContext(execution)
		capabilities := ports.Capabilities{Version: provider.Version}
		switch name {
		case "agy":
			providers[name] = agy.New(runner, execution, capabilities)
		case "codex":
			providers[name] = codex.New(runner, execution, capabilities)
		}
	}
	return providers
}

type randomJitter struct{}

func (randomJitter) Float64() float64 { return mathrand.Float64() }
