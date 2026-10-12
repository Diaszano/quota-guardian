package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Diaszano/quota-guardian/internal/infrastructure/configuration"
)

type Capability struct {
	Provider      string `json:"provider"`
	Version       string `json:"version,omitempty"`
	CanStart      bool   `json:"can_start"`
	CanResume     bool   `json:"can_resume"`
	CanAutonomous bool   `json:"can_autonomous"`
	Status        string `json:"status"`
}
type Report struct {
	Ready        bool         `json:"ready"`
	ModelCalls   int          `json:"model_calls"`
	Warnings     []string     `json:"warnings,omitempty"`
	Capabilities []Capability `json:"capabilities"`
}

func Doctor(ctx context.Context, cfg configuration.Config) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	report := Report{Ready: true}
	for _, dir := range []string{cfg.StateDir, cfg.RuntimeDir} {
		if dir == "" {
			continue
		}
		info, err := os.Lstat(dir)
		if err != nil {
			report.Warnings = append(report.Warnings, "configured directory unavailable")
			report.Ready = false
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			report.Warnings = append(report.Warnings, "configured directory permissions unsafe")
			report.Ready = false
		}
	}
	if cfg.RuntimeDir != "" {
		path := filepath.Join(cfg.RuntimeDir, "quota-guardian", "control.sock")
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			report.Warnings = append(report.Warnings, "daemon control socket unavailable")
			report.Ready = false
		}
	}
	for name, p := range cfg.Providers {
		cap := Capability{Provider: name, Version: p.Version, Status: "recovery capability unavailable until runtime evidence is reviewed"}
		if strings.TrimSpace(p.Executable) == "" {
			report.Warnings = append(report.Warnings, "provider executable is not configured")
		} else if info, err := os.Lstat(p.Executable); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
			report.Warnings = append(report.Warnings, "provider executable unavailable or not executable")
			report.Ready = false
		}
		report.Capabilities = append(report.Capabilities, cap)
	}
	return report, nil
}
