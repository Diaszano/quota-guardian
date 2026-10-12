package tests

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPluginContracts(t *testing.T) {
	for _, path := range []string{"../plugins/agy/plugin.json", "../plugins/agy/hooks.json", "../plugins/codex/.codex-plugin/plugin.json", "../plugins/codex/hooks/hooks.json", "../.agents/plugins/marketplace.json"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	var agy map[string]any
	readJSON(t, "../plugins/agy/hooks.json", &agy)
	for _, event := range []string{"Stop", "PreInvocation", "PostInvocation"} {
		if _, ok := agy["quota-guardian-observer"].(map[string]any)[event]; !ok {
			t.Errorf("Antigravity missing %s", event)
		}
	}
	var codex map[string]any
	readJSON(t, "../plugins/codex/hooks/hooks.json", &codex)
	if _, ok := codex["hooks"].(map[string]any)["SessionStart"]; !ok {
		t.Fatal("Codex SessionStart hook missing")
	}
	for _, file := range []string{"../plugins/agy/hooks.json", "../plugins/codex/hooks/hooks.json"} {
		b, _ := os.ReadFile(file)
		content := string(b)
		if strings.Contains(content, "consent") || strings.Contains(content, "resume") || strings.Contains(content, "cancel") {
			t.Fatalf("control action in %s", file)
		}
	}
	for _, file := range []string{"../plugins/agy/README.md", "../plugins/codex/README.md"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"$PWD`) {
			t.Errorf("install path in %s is not quoted", file)
		}
	}
}

func TestPluginUninstallPreservesUnrelatedConfig(t *testing.T) {
	for _, file := range []string{"../plugins/agy/README.md", "../plugins/codex/README.md"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "does not rewrite") {
			t.Errorf("%s must explain that vendor uninstall preserves unrelated config", file)
		}
	}
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
