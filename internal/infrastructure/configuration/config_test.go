package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsRelativeProviderAndUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":{"agy":{"executable":"agent"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative executable accepted: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"unsafe":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown config field accepted")
	}
}

func TestContextDigestChange(t *testing.T) {
	a := BuildExecutionContext("/usr/bin/agy", "1", t.TempDir())
	b := BuildExecutionContext("/usr/bin/agy", "1", filepath.Join(t.TempDir(), "other"))
	if a.Digest == b.Digest {
		t.Fatal("workspace change did not invalidate execution context")
	}
}

func TestConfigPathPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("shared config permissions accepted")
	}
}
