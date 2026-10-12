package tests

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Diaszano/quota-guardian/internal/adapters/agy"
	"github.com/Diaszano/quota-guardian/internal/adapters/codex"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

func TestRuntimeGateDefaultsClosed(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "invoked")
	executable := filepath.Join(root, "fake-provider")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionContext{ExecutablePath: executable, Cwd: root}
	for name, provider := range map[string]ports.Provider{
		"agy":   agy.New(nil, execution, ports.Capabilities{Version: "synthetic-only"}),
		"codex": codex.New(nil, execution, ports.Capabilities{Version: "synthetic-only"}),
	} {
		cap := provider.Capabilities()
		if cap.CanObserve || cap.CanStart || cap.CanResume || cap.CanAutonomous || cap.SupportsDenial || cap.SupportsSettlement {
			t.Fatalf("%s runtime capability unexpectedly open: %+v", name, cap)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("runtime gate invoked provider: %v", err)
	}
	for _, path := range []string{"../internal/adapters/agy/testdata/contracts.json", "../internal/adapters/codex/testdata/contracts.json"} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			RuntimeVerified    bool   `json:"runtime_verified"`
			AutonomousRecovery bool   `json:"autonomous_recovery"`
			RuntimeModelTurn   string `json:"runtime_model_turn"`
		}
		err = json.NewDecoder(io.LimitReader(file, 1<<16)).Decode(&record)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if record.RuntimeVerified || record.AutonomousRecovery || record.RuntimeModelTurn == "" {
			t.Fatalf("unsafe or incomplete runtime record in %s: %+v", path, record)
		}
	}
}
