package architecture_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var allowedRepositoryImports = map[string][]string{
	"llm-wiki/cmd/llm-wiki":           {"llm-wiki/internal/app"},
	"llm-wiki/internal/app":           {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/governance", "llm-wiki/internal/inbox", "llm-wiki/internal/index", "llm-wiki/internal/promote", "llm-wiki/internal/skill", "llm-wiki/internal/templates", "llm-wiki/internal/vault"},
	"llm-wiki/internal/config":        {"llm-wiki/internal/fsutil"},
	"llm-wiki/internal/document":      {"llm-wiki/internal/fsutil"},
	"llm-wiki/internal/fsutil":        {},
	"llm-wiki/internal/governance":    {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil"},
	"llm-wiki/internal/index":         {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/governance", "llm-wiki/internal/sqlite3simple", "llm-wiki/internal/vault"},
	"llm-wiki/internal/inbox":         {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/vault"},
	"llm-wiki/internal/promote":       {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/governance", "llm-wiki/internal/inbox", "llm-wiki/internal/vault"},
	"llm-wiki/internal/skill":         {"llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/resources"},
	"llm-wiki/internal/sqlite3simple": {},
	"llm-wiki/internal/templates":     {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/governance", "llm-wiki/resources"},
	"llm-wiki/internal/vault":         {"llm-wiki/internal/config", "llm-wiki/internal/document", "llm-wiki/internal/fsutil", "llm-wiki/internal/templates"},
}

func TestAgentInstructionTopology(t *testing.T) {
	root := repositoryRoot(t)
	want := []string{
		"AGENTS.md",
		"docs/template-design/personal-3.0.0/AGENTS.md",
		"resources/vault-templates/personal/AGENTS.md",
	}

	var got []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "AGENTS.md" {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("AGENTS.md topology mismatch\n got: %v\nwant: %v", got, want)
	}

	assertSameFile(t,
		filepath.Join(root, "resources/vault-templates/personal/AGENTS.md"),
		filepath.Join(root, "docs/template-design/personal-3.0.0/AGENTS.md"),
	)
}

func TestProductionRepositoryDependencies(t *testing.T) {
	root := repositoryRoot(t)
	packages := productionPackages(t, root)

	for packagePath, imports := range packages {
		allowed, ok := allowedRepositoryImports[packagePath]
		if !ok {
			t.Errorf("production package %q is missing from the AGENTS.md dependency policy", packagePath)
			continue
		}
		for _, imported := range imports {
			if strings.HasPrefix(imported, "llm-wiki/") && !slices.Contains(allowed, imported) {
				t.Errorf("%s imports disallowed repository package %s", packagePath, imported)
			}
		}
	}

	for packagePath := range allowedRepositoryImports {
		if _, ok := packages[packagePath]; !ok {
			t.Errorf("dependency policy contains missing production package %q", packagePath)
		}
	}
}

func productionPackages(t *testing.T, root string) map[string][]string {
	t.Helper()
	packages := make(map[string][]string)
	for _, base := range []string{"cmd/llm-wiki", "internal"} {
		start := filepath.Join(root, filepath.FromSlash(base))
		err := filepath.WalkDir(start, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}

			relDir, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			packagePath := "llm-wiki/" + filepath.ToSlash(relDir)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			if _, ok := packages[packagePath]; !ok {
				packages[packagePath] = nil
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if !slices.Contains(packages[packagePath], imported) {
					packages[packagePath] = append(packages[packagePath], imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return packages
}

func assertSameFile(t *testing.T, first, second string) {
	t.Helper()
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatalf("files must be byte-identical: %s, %s", first, second)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../.."))
}
