package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"llm-wiki/internal/document"
	resourcebundle "llm-wiki/resources"
)

func TestInstallUpdateAndUninstallOwnedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	result, err := Install("codex", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != root {
		t.Fatalf("unexpected target %s", result.Target)
	}
	if len(result.Skills) != 2 {
		t.Fatalf("expected Add and Query skills, got %#v", result.Skills)
	}
	queryBytes, err := os.ReadFile(filepath.Join(result.Target, "llm-wiki-query", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	queryText := string(queryBytes)
	if !strings.Contains(queryText, "读取 Vault `AGENTS.md`") ||
		!strings.Contains(queryText, "禁止调用 `inbox list/show`") ||
		!strings.Contains(queryText, "llm-wiki show <id>") {
		t.Fatalf("query skill omitted Vault bootstrap or knowledge-only hydration: %s", queryText)
	}
	addBytes, err := os.ReadFile(filepath.Join(result.Target, "llm-wiki-add", "SKILL.md"))
	if err != nil || !strings.Contains(string(addBytes), "pending") ||
		!strings.Contains(string(addBytes), "不得用摘要替换用户输入") {
		t.Fatalf("add skill blurred capture and retrieval boundaries: %s err=%v", addBytes, err)
	}
	status, err := GetStatus("codex")
	if err != nil || !status.Installed || len(status.Modified) != 0 || len(status.Skills) != 2 {
		t.Fatalf("bad status %#v err=%v", status, err)
	}
	if _, err := Install("codex", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall("codex", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range SkillNames() {
		if _, err := os.Stat(filepath.Join(root, name, "SKILL.md")); !os.IsNotExist(err) {
			t.Fatalf("owned file remains after uninstall: %s: %v", name, err)
		}
	}
}

func TestUpdateLegacyFourSkillManifestPreservesModifiedObsoleteFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	current, err := sourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	owned := append([]OwnedFile(nil), current...)
	for _, file := range current {
		data, err := resourcebundle.FS.ReadFile("skills/" + file.Path)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"llm-wiki-maintain", "llm-wiki-publish"} {
		path := filepath.Join(root, name, "SKILL.md")
		data := []byte("legacy owned\n")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		owned = append(owned, OwnedFile{Path: filepath.ToSlash(filepath.Join(name, "SKILL.md")), Hash: document.HashBytes(data)})
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Path < owned[j].Path })
	manifest := InstallManifest{SchemaVersion: 2, Client: "codex", SkillVersion: "2.1.0", InstalledAt: "2026-08-09T00:00:00Z", Skills: legacySkillNames, Files: owned}
	manifestBytes, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, manifestName), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	modified := filepath.Join(root, "llm-wiki-maintain", "SKILL.md")
	if err := os.WriteFile(modified, []byte("user modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Install("codex", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Preserved) != 1 || result.Preserved[0] != "llm-wiki-maintain/SKILL.md" {
		t.Fatalf("modified obsolete skill was not reported: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "llm-wiki-publish", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("unmodified obsolete skill was not removed: %v", err)
	}
	if data, err := os.ReadFile(modified); err != nil || string(data) != "user modified\n" {
		t.Fatalf("modified obsolete skill was changed: %q %v", data, err)
	}
}

func TestInstallRefusesUnmanagedConflictingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	target := filepath.Join(root, "llm-wiki-query")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(path, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("codex", false, false); err == nil {
		t.Fatal("expected unmanaged conflict rejection")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "user-owned\n" {
		t.Fatalf("unmanaged file was changed: %q %v", b, err)
	}
}

func TestInstallRefusesNestedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	target := filepath.Join(root, "llm-wiki-query")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(target, "agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("codex", false, false); err == nil {
		t.Fatal("expected nested symlink rejection")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("skill write escaped target: entries=%v err=%v", entries, err)
	}
}

func TestSkillDryRunDoesNotCreateTargetOrLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-skills-root")
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	result, err := Install("codex", false, true)
	if err != nil || !result.DryRun {
		t.Fatalf("skill dry-run failed: result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("skill dry-run created target parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, installLockName)); !os.IsNotExist(err) {
		t.Fatalf("skill dry-run created lock: %v", err)
	}
}

func TestUninstallPreservesModifiedSkill(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	result, err := Install("codex", false, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(result.Target, "llm-wiki-query", "SKILL.md")
	if err := os.WriteFile(path, []byte("user change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall("codex", false); err == nil {
		t.Fatal("expected modified-file protection")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "user change\n" {
		t.Fatalf("modified file was not preserved: %q %v", b, err)
	}
}

func TestManifestCannotClaimUnrelatedSkillFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	unrelatedPath := filepath.Join(root, "unrelated-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(unrelatedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte("user owned\n")
	if err := os.WriteFile(unrelatedPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := InstallManifest{
		SchemaVersion: manifestSchema, Client: "codex", SkillVersion: SkillVersion,
		InstalledAt: "2026-08-10T00:00:00Z", Skills: SkillNames(),
		Files: []OwnedFile{{Path: "unrelated-skill/SKILL.md", Hash: document.HashBytes(data)}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, manifestName), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GetStatus("codex"); err == nil {
		t.Fatal("status accepted ownership outside the llm-wiki skill set")
	}
	if _, err := Uninstall("codex", false); err == nil {
		t.Fatal("uninstall accepted ownership outside the llm-wiki skill set")
	}
	if b, err := os.ReadFile(unrelatedPath); err != nil || string(b) != string(data) {
		t.Fatalf("unrelated skill file was changed: %q %v", b, err)
	}
}
