//go:build systemd

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/infrastructure/systemd"
)

func TestSystemdContractTemplate(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "packaging", "systemd", "quota-guardian.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := systemd.Render("/usr/bin/quota-guardian")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Type=notify", "WatchdogSec=30s", "TimeoutStopSec=30s", "KillMode=control-group"} {
		if !strings.Contains(string(template), field) || !strings.Contains(string(rendered), field) {
			t.Errorf("unit field %s missing", field)
		}
	}
}

func TestSystemdLifecycle(t *testing.T) {
	if os.Getenv("QG_SYSTEMD_AUTHORIZED") != "1" {
		t.Skip("requires a disposable authorized systemd user manager")
	}
	t.Skip("runtime lifecycle harness is enabled only after a disposable user-manager fixture is configured")
}
