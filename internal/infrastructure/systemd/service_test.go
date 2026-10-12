package systemd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnit(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "test user", "qg%run")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	unit, err := Render(executable)
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{"Type=notify", "ExecStart=" + escape(executable) + " daemon run", "Restart=on-failure", "RestartSec=5s", "WatchdogSec=30s", "TimeoutStopSec=30s", "KillMode=control-group", "UMask=0077"} {
		if !strings.Contains(text, want) {
			t.Errorf("unit missing %q: %s", want, text)
		}
	}
	if analyzer, err := exec.LookPath("systemd-analyze"); err == nil {
		path := filepath.Join(t.TempDir(), unitName)
		if err := os.WriteFile(path, unit, 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(analyzer, "verify", path).CombinedOutput(); err != nil {
			t.Fatalf("systemd-analyze: %v: %s", err, output)
		}
	}
}

func TestInstallerPreservesExistingUnit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota-guardian.service")
	before := []byte("[Service]\nExecStart=/unowned\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), dir, "/usr/bin/quota-guardian"); err == nil {
		t.Fatal("existing unit overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("unit changed: %q", after)
	}
}

func TestInstallAndUninstallOwnedUnit(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "systemctl.log")
	tool := filepath.Join(bin, "systemctl")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+log+"\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	unitDir := filepath.Join(root, "units")
	if err := Install(context.Background(), unitDir, "/opt/quota guardian/bin/quota-guardian"); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(context.Background(), unitDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, unitName)); !os.IsNotExist(err) {
		t.Fatalf("owned unit remains: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "daemon-reload") != 2 {
		t.Fatalf("systemctl calls: %s", calls)
	}
}

func TestControlRejectsUnlistedActions(t *testing.T) {
	if err := Control(context.Background(), "enable"); err == nil {
		t.Fatal("unsupported systemctl action accepted")
	}
}
