//go:build linux

package persistence

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestLockHelperProcess(t *testing.T) {
	dir := os.Getenv("QG_LOCK_HELPER_DIR")
	if dir == "" {
		return
	}
	lock, err := AcquireLock(dir)
	if errors.Is(err, ports.ErrBusy) {
		count := 0
		entries, _ := os.ReadDir("/proc/self/fd")
		for _, entry := range entries {
			target, _ := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
			if target == filepath.Join(dir, "daemon.lock") {
				count++
			}
		}
		_, _ = fmt.Fprintf(os.Stdout, "busy:%d", count)
		os.Exit(0)
	}
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	_ = lock.Close()
	os.Exit(3)
}

func TestSecondDaemonCannotStart(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	cmd.Env = append(os.Environ(), "QG_LOCK_HELPER_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "busy:0" {
		t.Fatalf("second daemon result: output=%q err=%v", out, err)
	}
}

func TestLockUsesStateDirectory(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	info, err := os.Stat(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o, want 600", info.Mode().Perm())
	}
}
