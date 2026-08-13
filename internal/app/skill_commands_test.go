package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeCodeSkillCLIInstallStatusAndUninstall(t *testing.T) {
	target := t.TempDir()
	t.Setenv("LLM_WIKI_CLAUDE_CODE_SKILLS_DIR", target)

	installed := runAppCommand(t, "skill", "install", "claude-code", "--yes", "--json", "--no-interactive")
	if !installed.OK || installed.Command != "skill.install" || len(installed.AffectedFiles) != 2 {
		t.Fatalf("unexpected install response %#v", installed)
	}
	for _, name := range []string{"llm-wiki-add", "llm-wiki-query"} {
		if _, err := os.Stat(filepath.Join(target, name, "SKILL.md")); err != nil {
			t.Fatalf("missing Claude Code skill %s: %v", name, err)
		}
	}

	status := runAppCommand(t, "skill", "status", "claude-code", "--json", "--no-interactive")
	if !status.OK || status.Command != "skill.status" {
		t.Fatalf("unexpected status response %#v", status)
	}

	uninstalled := runAppCommand(t, "skill", "uninstall", "claude-code", "--yes", "--json", "--no-interactive")
	if !uninstalled.OK || uninstalled.Command != "skill.uninstall" || len(uninstalled.AffectedFiles) != 2 {
		t.Fatalf("unexpected uninstall response %#v", uninstalled)
	}
}

func TestInitCanInstallClaudeCodeSkills(t *testing.T) {
	skillTarget := filepath.Join(t.TempDir(), "claude-skills")
	t.Setenv("LLM_WIKI_CLAUDE_CODE_SKILLS_DIR", skillTarget)
	vaultRoot := filepath.Join(t.TempDir(), "wiki")
	response := runAppCommand(t,
		"init", vaultRoot, "--name", "claude-wiki", "--install-skill", "--skill-client", "claude-code", "--yes", "--json", "--no-interactive",
	)
	if !response.OK || response.Command != "init" {
		t.Fatalf("unexpected init response %#v", response)
	}
	for _, name := range []string{"llm-wiki-add", "llm-wiki-query"} {
		if _, err := os.Stat(filepath.Join(skillTarget, name, "SKILL.md")); err != nil {
			t.Fatalf("init omitted Claude Code skill %s: %v", name, err)
		}
	}
}

func runAppCommand(t *testing.T, args ...string) Response {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		RenderFailure(root, err)
		t.Fatalf("execute %v: %v stderr=%s stdout=%s", args, err, stderr.String(), stdout.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	return response
}
