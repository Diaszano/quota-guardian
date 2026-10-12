package configuration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Diaszano/quota-guardian/internal/domain"
)

type Provider struct {
	Executable  string `json:"executable"`
	Version     string `json:"version,omitempty"`
	SettingsRef string `json:"settings_ref,omitempty"`
}
type Config struct {
	StateDir   string              `json:"state_dir,omitempty"`
	RuntimeDir string              `json:"runtime_dir,omitempty"`
	Providers  map[string]Provider `json:"providers,omitempty"`
}

func Defaults() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = filepath.Join(home, ".cache", "quota-guardian", "run")
	}
	return Config{StateDir: filepath.Join(state, "quota-guardian"), RuntimeDir: runtime}, nil
}

func Load(path string) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, fmt.Errorf("config path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return Config{}, fmt.Errorf("unsafe config file permissions or type")
	}
	if !ownedByCurrentUser(info) {
		return Config{}, fmt.Errorf("config file owner mismatch")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var cfg Config
	if err = d.Decode(&cfg); err != nil {
		return Config{}, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("trailing config data")
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	for name, path := range map[string]string{"state_dir": c.StateDir, "runtime_dir": c.RuntimeDir} {
		if path != "" && !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be absolute", name)
		}
	}
	for name, p := range c.Providers {
		if strings.TrimSpace(name) == "" || !filepath.IsAbs(p.Executable) {
			return fmt.Errorf("provider %q executable must be absolute", name)
		}
		if p.SettingsRef != "" && !filepath.IsAbs(p.SettingsRef) {
			return fmt.Errorf("provider %q settings_ref must be absolute", name)
		}
		if p.SettingsRef != "" {
			info, err := os.Lstat(p.SettingsRef)
			if err != nil {
				return fmt.Errorf("provider %q settings_ref unavailable", name)
			}
			if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
				return fmt.Errorf("provider %q settings_ref is unsafe", name)
			}
			if !ownedByCurrentUser(info) {
				return fmt.Errorf("provider %q settings_ref owner mismatch", name)
			}
		}
	}
	return nil
}

func BuildExecutionContext(executable, version, cwd string) domain.ExecutionContext {
	c := domain.ExecutionContext{ExecutablePath: executable, ProviderVersion: version, Cwd: cwd}
	c.Digest = domain.DigestContext(c)
	return c
}
