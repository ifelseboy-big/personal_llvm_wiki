package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"llm-wiki/internal/document"
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
	if len(result.Skills) != 4 {
		t.Fatalf("expected four independent skills, got %#v", result.Skills)
	}
	queryBytes, err := os.ReadFile(filepath.Join(result.Target, "llm-wiki-query", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	queryText := string(queryBytes)
	if !strings.Contains(queryText, "读取目标 Vault 的 `AGENTS.md`") ||
		!strings.Contains(queryText, "不得尝试搜索 `raw/`") ||
		!strings.Contains(queryText, "llm-wiki show <knowledge-id>") {
		t.Fatalf("query skill omitted Vault bootstrap or knowledge-only hydration: %s", queryText)
	}
	addBytes, err := os.ReadFile(filepath.Join(result.Target, "llm-wiki-add", "SKILL.md"))
	if err != nil || !strings.Contains(string(addBytes), "已采集、未发布") ||
		!strings.Contains(string(addBytes), "raw 不进入全文检索") {
		t.Fatalf("add skill blurred capture and retrieval boundaries: %s err=%v", addBytes, err)
	}
	publishBytes, err := os.ReadFile(filepath.Join(result.Target, "llm-wiki-publish", "SKILL.md"))
	if err != nil || !strings.Contains(string(publishBytes), "只有用户针对该 diff 明确批准") ||
		!strings.Contains(string(publishBytes), "唯一可信事实") {
		t.Fatalf("publish skill omitted approval or fact boundary: %s err=%v", publishBytes, err)
	}
	status, err := GetStatus("codex")
	if err != nil || !status.Installed || len(status.Modified) != 0 || len(status.Skills) != 4 {
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
