package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	baseline := filepath.Join(root, ".llm-wiki", "template-base", "1.0.0", "AGENTS.md")
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
