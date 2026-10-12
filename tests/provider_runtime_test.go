//go:build provider_runtime

package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProviderRuntime(t *testing.T) {
	if os.Getenv("QG_RUNTIME_AUTHORIZED") != "1" {
		t.Skip("requires explicit QG_RUNTIME_AUTHORIZED=1")
	}
	workspace := os.Getenv("QG_RUNTIME_WORKSPACE")
	if !filepath.IsAbs(workspace) {
		t.Fatal("QG_RUNTIME_WORKSPACE must name an approved absolute workspace")
	}
	info, err := os.Lstat(workspace)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("runtime workspace is missing or unsafe")
	}
	for _, provider := range []struct{ name, env, versionArg string }{{"agy", "QG_RUNTIME_AGY_BIN", "--version"}, {"codex", "QG_RUNTIME_CODEX_BIN", "--version"}} {
		path := os.Getenv(provider.env)
		if !filepath.IsAbs(path) {
			t.Fatalf("%s must be an absolute executable path", provider.env)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, err := exec.CommandContext(ctx, path, provider.versionArg).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s version check failed: %v", provider.name, err)
		}
		t.Logf("%s version output: %s", provider.name, output)
		t.Logf("%s model-turn, resume, denial, quota, plugin install/remove, and settlement checks remain unverified; no capability is enabled", provider.name)
	}
}
