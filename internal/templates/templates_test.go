package templates_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-wiki/internal/document"
	"llm-wiki/internal/templates"
	"llm-wiki/internal/vault"
)

func TestPersonalTemplatesExposeObsidianProperties(t *testing.T) {
	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.1.1" {
		t.Fatalf("unexpected personal template version %s", manifest.Version)
	}
	agents, err := templates.ReadFile("personal", "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "`knowledge/` 中经过发布的 Markdown 是唯一最终事实源") ||
		!strings.Contains(string(agents), "| `raw add` | [[rules/capture|采集规则]]") {
		t.Fatalf("personal AGENTS.md omitted the fact boundary or operation routing: %s", agents)
	}
	if _, err := templates.ReadFile("personal", "rules/lifecycle.md"); err != nil {
		t.Fatalf("personal template omitted lifecycle routing target: %v", err)
	}
	for _, name := range []string{"concept", "guide", "reference", "decision", "project"} {
		item, err := templates.ReadContent(nil, "knowledge", name)
		if err != nil {
			t.Fatal(err)
		}
		meta, _, err := document.Parse([]byte(item.Content))
		if err != nil {
			t.Fatalf("parse %s template: %v", name, err)
		}
		for _, property := range []string{"description", "cssclasses", "related"} {
			if _, ok := meta.Extra[property]; !ok {
				t.Fatalf("%s template is missing %s", name, property)
			}
		}
		if meta.Tags == nil || meta.Aliases == nil {
			t.Fatalf("%s template must declare tags and aliases lists", name)
		}
	}
}

func TestTemplateUpgradePreservesUserChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "template-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(root, "AGENTS.md")
	userContent := []byte("# My custom policy\n")
	if err := os.WriteFile(agentsPath, userContent, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := templates.PlanUpgrade(initResult.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasConflicts {
		t.Fatal("expected modified managed file conflict")
	}
	if _, _, err := templates.ApplyUpgrade(initResult.Config, false, false); err == nil {
		t.Fatal("upgrade must require explicit conflict handling")
	}
	if _, _, err := templates.ApplyUpgrade(initResult.Config, true, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(agentsPath)
	if err != nil || string(b) != string(userContent) {
		t.Fatalf("user template modification was overwritten: %q %v", b, err)
	}
}
