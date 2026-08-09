package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/templates"
)

func TestInitConfiguresGitIgnoreAndKeepsChangesTracked(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	result, err := Init(InitOptions{Path: root, Name: "gitignore", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(result.CreatedFiles, gitIgnoreFileName) {
		t.Fatalf("created files do not include %s: %#v", gitIgnoreFileName, result.CreatedFiles)
	}
	b, err := os.ReadFile(filepath.Join(root, gitIgnoreFileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := gitIgnoreLines(b)
	for _, pattern := range gitIgnorePatterns {
		if !lines[pattern] {
			t.Errorf("missing gitignore pattern %q in:\n%s", pattern, b)
		}
	}
	if lines[".llm-wiki/"] || lines[".llm-wiki/changes/"] {
		t.Fatalf("changes audit records must remain tracked:\n%s", b)
	}
}

func TestInitMergesExistingGitIgnore(t *testing.T) {
	root := t.TempDir()
	existing := []byte("# user rules\n*.tmp\nllm-wiki/")
	path := filepath.Join(root, gitIgnoreFileName)
	if err := os.WriteFile(path, existing, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Init(InitOptions{Path: root, Name: "gitignore-merge", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(result.UpdatedFiles, gitIgnoreFileName) {
		t.Fatalf("updated files do not include %s: %#v", gitIgnoreFileName, result.UpdatedFiles)
	}
	if containsString(result.CreatedFiles, gitIgnoreFileName) {
		t.Fatalf("existing %s was reported as created", gitIgnoreFileName)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), string(existing)+"\n") {
		t.Fatalf("existing gitignore content was not preserved:\n%s", b)
	}
	if strings.Count(string(b), "llm-wiki/\n") != 1 {
		t.Fatalf("existing rule was duplicated:\n%s", b)
	}
	for _, pattern := range gitIgnorePatterns {
		if !gitIgnoreLines(b)[pattern] {
			t.Errorf("missing gitignore pattern %q in:\n%s", pattern, b)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("gitignore mode changed to %o", info.Mode().Perm())
	}
}

func TestInitDryRunReportsGitIgnoreWithoutWriting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	result, err := Init(InitOptions{Path: root, Name: "gitignore-dry-run", Template: "personal", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(result.CreatedFiles, gitIgnoreFileName) {
		t.Fatalf("dry-run did not report %s: %#v", gitIgnoreFileName, result.CreatedFiles)
	}
	if _, err := os.Stat(filepath.Join(root, gitIgnoreFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run wrote gitignore: %v", err)
	}
}

func TestInitExistingVaultRequiresExplicitConflictPolicy(t *testing.T) {
	root := t.TempDir()
	existing := []byte("# Existing vault policy\n")
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), existing, 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := Init(InitOptions{Path: root, Name: "existing", Template: "personal", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0] != "AGENTS.md" {
		t.Fatalf("unexpected conflicts %#v", preview.Conflicts)
	}
	if _, err := Init(InitOptions{Path: root, Name: "existing", Template: "personal"}); err == nil {
		t.Fatal("expected conflict without explicit policy")
	}
	result, err := Init(InitOptions{Path: root, Name: "existing", Template: "personal", KeepConflicts: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.InstanceID == "" {
		t.Fatal("wiki was not initialized")
	}
	b, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || string(b) != string(existing) {
		t.Fatalf("existing AGENTS.md was overwritten: %q %v", b, err)
	}
}

func TestWriteLockRejectsConcurrentWriter(t *testing.T) {
	result, err := Init(InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "lock-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := AcquireWrite(result.Config, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := AcquireWrite(result.Config, 100*time.Millisecond); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

func TestInitTemplatePreflightFailureLeavesNoInstanceConfig(t *testing.T) {
	root := t.TempDir()
	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(root, ".llm-wiki", "template-base", manifest.Version, "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(baseline), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseline, []byte("conflicting baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Init(InitOptions{Path: root, Name: "rollback", Template: "personal"})
	if err == nil || result != nil {
		t.Fatalf("expected initialization preflight failure, result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "llm-wiki.toml")); !os.IsNotExist(err) {
		t.Fatalf("failed initialization left an instance config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("failed initialization wrote template files: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, gitIgnoreFileName)); !os.IsNotExist(err) {
		t.Fatalf("failed initialization left gitignore changes: %v", err)
	}
}

func TestInitReturnsUsableWikiWhenOptionalRegistrationFails(t *testing.T) {
	registryPath := t.TempDir()
	t.Setenv("LLM_WIKI_CONFIG", registryPath)
	root := filepath.Join(t.TempDir(), "wiki")
	result, err := Init(InitOptions{Path: root, Name: "registration-warning", Template: "personal", Register: true})
	if err == nil || result == nil {
		t.Fatalf("expected partial registration warning with usable result: result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "llm-wiki.toml")); statErr != nil {
		t.Fatalf("registration failure discarded usable wiki: %v", statErr)
	}
	if result.Registered {
		t.Fatal("failed registration was reported as successful")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func gitIgnoreLines(content []byte) map[string]bool {
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		lines[strings.TrimSpace(strings.TrimSuffix(line, "\r"))] = true
	}
	return lines
}
