package tests_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitecture(t *testing.T) {
	domainDir := filepath.Join("..", "internal", "domain")
	entries, err := os.ReadDir(domainDir)
	if err != nil {
		t.Fatalf("failed to read domain directory: %v", err)
	}

	inspectedFiles := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only inspect production Go files, not test files
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		filePath := filepath.Join(domainDir, name)
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", filePath, err)
		}

		inspectedFiles++

		for _, imp := range node.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			// Core boundary rules:
			// 1. Domain imports ONLY Go standard library.
			// 2. Reject internal/adapters, external modules, database/sql, os/exec.
			firstSegment := strings.Split(importPath, "/")[0]
			if strings.Contains(firstSegment, ".") {
				t.Errorf("file %s imports non-standard package %s", name, importPath)
			}
			if strings.HasPrefix(importPath, "internal/adapters") {
				t.Errorf("file %s imports adapter package %s", name, importPath)
			}
			if importPath == "database/sql" {
				t.Errorf("file %s imports database/sql, forbidden in core domain", name)
			}
			if importPath == "os/exec" {
				t.Errorf("file %s imports os/exec, forbidden in core domain", name)
			}
		}
	}

	if inspectedFiles == 0 {
		t.Fatalf("expected to inspect production files in %s, but found 0", domainDir)
	}
}
