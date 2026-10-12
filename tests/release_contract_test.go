package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseCLIContract(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "quota-guardian")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=release-contract-test", "-o", bin, "../cmd/quota-guardian")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	help, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run CLI help: %v %s", err, help)
	}
	for _, command := range []string{"run", "sessions", "status", "doctor", "daemon", "observe", "version"} {
		if !strings.Contains(string(help), command) || !strings.Contains(string(readme), command) {
			t.Errorf("CLI help or README missing command %q", command)
		}
	}
	version, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(version)) != "release-contract-test" {
		t.Fatalf("CLI version=%q err=%v", version, err)
	}
}

func TestReleaseFixturesContainNoCredentials(t *testing.T) {
	for _, path := range []string{"../internal/adapters/agy/testdata", "../internal/adapters/codex/testdata", "testdata/runtime"} {
		err := filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			b, e := os.ReadFile(name)
			if e != nil {
				return e
			}
			lower := strings.ToLower(string(b))
			for _, needle := range []string{"sk-", "ghp_", "xoxb-", "bearer ", "\"api_key\"", "\"password\""} {
				if strings.Contains(lower, needle) {
					return fmt.Errorf("credential-like fixture %s", name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestReleaseArtifactChecksums(t *testing.T) {
	dir := t.TempDir()
	artifacts := map[string][]byte{"quota-guardian-linux-amd64": []byte("linux"), "quota-guardian-darwin-arm64-runtime-unsupported": []byte("darwin")}
	for name, data := range artifacts {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("sh", "../scripts/package-artifacts.sh", dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("package artifacts: %v %s", err, output)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range artifacts {
		sum := sha256.Sum256(data)
		expected := hex.EncodeToString(sum[:]) + "  " + name
		if !strings.Contains(string(manifest), expected) {
			t.Errorf("checksum missing %q in %s", expected, manifest)
		}
	}
}
