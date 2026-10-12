package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const unitName = "quota-guardian.service"
const managedMarker = "# Managed by quota-guardian; do not edit.\n"

func Render(executable string) ([]byte, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("executable path must be absolute")
	}
	return []byte(managedMarker + "[Unit]\nDescription=Quota Guardian\nAfter=default.target\n\n[Service]\nType=notify\nExecStart=" + escape(executable) + " daemon run\nRestart=on-failure\nRestartSec=5s\nWatchdogSec=30s\nTimeoutStopSec=30s\nKillMode=control-group\nUMask=0077\n\n[Install]\nWantedBy=default.target\n"), nil
}

func escape(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ' ':
			b.WriteString(`\s`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '"':
			b.WriteString(`\"`)
		case '%':
			b.WriteString(`%%`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Install(ctx context.Context, unitDir, executable string) error {
	if runtime.GOOS != "linux" {
		return errors.New("systemd user services are supported only on Linux")
	}
	if os.Geteuid() == 0 {
		return errors.New("user service installation cannot run as root")
	}
	if !filepath.IsAbs(unitDir) {
		return errors.New("unit directory must be absolute")
	}
	unit, err := Render(executable)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(unitDir, 0700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(unitDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe unit directory")
	}
	path := filepath.Join(unitDir, unitName)
	if info, e := os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe existing unit %s", path)
		}
		old, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Equal(old, unit) {
			return nil
		}
		return fmt.Errorf("%s already exists", path)
	} else if !os.IsNotExist(e) {
		return e
	}
	tmp, err := os.CreateTemp(unitDir, ".quota-guardian-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(unit)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmpName, path); err != nil {
		return fmt.Errorf("install unit: %w", err)
	}
	return exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
}

func Uninstall(ctx context.Context, unitDir string) error {
	if runtime.GOOS != "linux" {
		return errors.New("systemd user services are supported only on Linux")
	}
	if os.Geteuid() == 0 {
		return errors.New("user service removal cannot run as root")
	}
	if !filepath.IsAbs(unitDir) {
		return errors.New("unit directory must be absolute")
	}
	dirInfo, dirErr := os.Lstat(unitDir)
	if os.IsNotExist(dirErr) {
		return nil
	}
	if dirErr != nil {
		return dirErr
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return errors.New("unsafe unit directory")
	}
	path := filepath.Join(unitDir, unitName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe unit %s", path)
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(body, []byte(managedMarker)) {
		return fmt.Errorf("refusing to remove unowned unit %s", path)
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
}

func Control(ctx context.Context, action string) error {
	if runtime.GOOS != "linux" {
		return errors.New("systemd user services are supported only on Linux")
	}
	if os.Geteuid() == 0 {
		return errors.New("user service control cannot run as root")
	}
	switch action {
	case "start", "stop", "status":
	default:
		return errors.New("unsupported systemd action")
	}
	return exec.CommandContext(ctx, "systemctl", "--user", action, unitName).Run()
}
