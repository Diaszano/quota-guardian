package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Diaszano/quota-guardian/internal/adapters/persistence"
	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestPeerUIDRejected(t *testing.T) {
	if authorizedUID(os.Getuid()) == false || authorizedUID(os.Getuid()+1) {
		t.Fatal("peer UID policy did not accept only the daemon UID")
	}
	if _, err := Listen(context.Background(), t.TempDir(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })); err == nil {
		t.Fatal("Listen() accepted a runtime directory without owner-only permissions")
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	listenCtx, stopListen := context.WithCancel(context.Background())
	defer stopListen()
	listener, err := Listen(listenCtx, runtimeDir, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	path := filepath.Join(runtimeDir, "quota-guardian", "control.sock")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode=%o", info.Mode().Perm())
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case conn := <-accepted:
		defer conn.Close()
		if conn.RemoteAddr().String() != fmt.Sprintf("uid:%d", os.Getuid()) {
			t.Fatalf("peer=%s", conn.RemoteAddr())
		}
	case <-time.After(time.Second):
		t.Fatal("connection was not accepted")
	}
}

func TestOversizeRequest(t *testing.T) {
	handler := NewHandler(nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	request.RemoteAddr = fmt.Sprintf("uid:%d", os.Getuid())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", response.Code)
	}
}

func TestUnknownControlField(t *testing.T) {
	handler := NewHandler(nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"arbitrary_command":"/bin/sh"}`))
	request.RemoteAddr = fmt.Sprintf("uid:%d", os.Getuid())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", response.Code)
	}
}

type ipcProvider struct {
	ctx   domain.ExecutionContext
	calls atomic.Int32
	seen  chan struct{}
}

func (*ipcProvider) Name() string                                { return "test" }
func (*ipcProvider) Version() string                             { return "1" }
func (p *ipcProvider) ExecutionContext() domain.ExecutionContext { return p.ctx }
func (*ipcProvider) Capabilities() ports.Capabilities {
	return ports.Capabilities{CanStart: true, SupportsDenial: true, SupportsSettlement: true}
}
func (p *ipcProvider) Start(_ context.Context, req ports.StartRequest, _ func(ports.StreamRecord) error) (ports.Outcome, error) {
	p.calls.Add(1)
	if p.seen != nil {
		p.seen <- struct{}{}
	}
	if req.OnStart != nil {
		if err := req.OnStart(domain.ProcessIdentity{PID: 123, BootID: "boot", StartTicks: 1}); err != nil {
			return ports.Outcome{}, err
		}
	}
	return ports.Outcome{Evidence: domain.Evidence{ConfirmedSessionID: "provider-session", Settled: true, TurnCompleted: true}}, nil
}
func (*ipcProvider) Resume(context.Context, ports.ResumeRequest, func(ports.StreamRecord) error) (ports.Outcome, error) {
	return ports.Outcome{}, nil
}

func TestLostAckIdempotency(t *testing.T) {
	ctx := context.Background()
	stateDir := filepath.Join(t.TempDir(), "state")
	store, err := persistence.Open(ctx, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providerContext := domain.ExecutionContext{ExecutablePath: "/bin/agent", ProviderVersion: "1", Cwd: t.TempDir()}
	providerContext.Digest = domain.DigestContext(providerContext)
	provider := &ipcProvider{ctx: providerContext, seen: make(chan struct{}, 2)}
	coordinator := application.NewCoordinator(store, map[string]ports.Provider{"test": provider}, ipcClock{now: time.Now().UTC()})
	handler := NewHandler(coordinator, store)
	post := func(prompt string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"submission_key": "same-key", "provider": "test", "context_id": "workspace", "prompt": prompt})
		request := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(string(body)))
		request.RemoteAddr = fmt.Sprintf("uid:%d", os.Getuid())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := post("first prompt")
	second := post("different prompt")
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses=%d,%d; want 202,202", first.Code, second.Code)
	}
	select {
	case <-provider.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("managed provider was not started")
	}
	if calls := provider.calls.Load(); calls != 1 {
		t.Fatalf("provider starts=%d, want exactly 1 after lost-ack retry", calls)
	}
}

type ipcClock struct{ now time.Time }

func (c ipcClock) Now() time.Time                     { return c.now }
func (ipcClock) NewTimer(d time.Duration) ports.Timer { return ipcTimer{Timer: time.NewTimer(d)} }

type ipcTimer struct{ *time.Timer }

func (t ipcTimer) C() <-chan time.Time { return t.Timer.C }
