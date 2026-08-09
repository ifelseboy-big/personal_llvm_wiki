package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallUpdateAndUninstallOwnedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	result, err := Install("codex", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target != filepath.Join(root, "llm-wiki") {
		t.Fatalf("unexpected target %s", result.Target)
	}
	skillBytes, err := os.ReadFile(filepath.Join(result.Target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skillText := string(skillBytes)
	if !strings.Contains(skillText, "读取目标知识库根目录下的 `AGENTS.md`") ||
		!strings.Contains(skillText, "`knowledge/` 中已发布 Markdown 是唯一最终事实源") {
		t.Fatalf("installed skill omitted the Vault bootstrap or fact boundary: %s", skillText)
	}
	status, err := GetStatus("codex")
	if err != nil || !status.Installed || len(status.Modified) != 0 {
		t.Fatalf("bad status %#v err=%v", status, err)
	}
	if _, err := Install("codex", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall("codex", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "llm-wiki", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("owned file remains after uninstall: %v", err)
	}
}

func TestInstallRefusesUnmanagedConflictingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", root)
	target := filepath.Join(root, "llm-wiki")
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
	target := filepath.Join(root, "llm-wiki")
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
	if _, err := os.Stat(filepath.Join(root, "llm-wiki.lock")); !os.IsNotExist(err) {
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
	path := filepath.Join(result.Target, "SKILL.md")
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
