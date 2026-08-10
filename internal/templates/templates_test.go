package templates_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/templates"
	"llm-wiki/internal/vault"
)

func TestPersonalTemplateMatchesVersionedDesignBaseline(t *testing.T) {
	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate template test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	for _, relative := range append([]string{"template.toml"}, manifest.ManagedFiles...) {
		embedded, err := templates.ReadFile("personal", relative)
		if err != nil {
			t.Fatalf("read embedded %s: %v", relative, err)
		}
		baselinePath := filepath.Join(repositoryRoot, "docs", "template-design", "personal-"+manifest.Version, filepath.FromSlash(relative))
		baseline, err := os.ReadFile(baselinePath)
		if err != nil {
			t.Fatalf("read design baseline %s: %v", baselinePath, err)
		}
		if !bytes.Equal(embedded, baseline) {
			t.Fatalf("embedded template %s differs from %s", relative, baselinePath)
		}
	}
}

func TestPersonalTemplatesExposeObsidianProperties(t *testing.T) {
	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.3.0" {
		t.Fatalf("unexpected personal template version %s", manifest.Version)
	}
	agents, err := templates.ReadFile("personal", "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "`knowledge/` 中经过人工确认发布的 Markdown 是唯一可信事实源") ||
		!strings.Contains(string(agents), "它不是 CLI 使用手册") ||
		!strings.Contains(string(agents), "不进入全文检索") {
		t.Fatalf("personal AGENTS.md omitted its management-only or retrieval boundary: %s", agents)
	}
	if _, err := templates.ReadFile("personal", "rules/lifecycle.md"); err != nil {
		t.Fatalf("personal template omitted lifecycle routing target: %v", err)
	}
	for _, name := range []string{"claim", "concept", "guide", "tutorial", "reference", "decision", "project"} {
		item, err := templates.ReadContent(nil, "knowledge", name)
		if err != nil {
			t.Fatal(err)
		}
		meta, _, err := document.Parse([]byte(item.Content))
		if err != nil {
			t.Fatalf("parse %s template: %v", name, err)
		}
		for _, property := range []string{"description", "lifecycle", "cssclasses", "related"} {
			if _, ok := meta.Extra[property]; !ok {
				t.Fatalf("%s template is missing %s", name, property)
			}
		}
		if meta.Tags == nil || meta.Aliases == nil {
			t.Fatalf("%s template must declare tags and aliases lists", name)
		}
	}
	for _, name := range []string{"knowledge.base", "review.base", "raw.base"} {
		content, err := templates.ReadFile("personal", "views/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "\t") {
			t.Fatalf("%s contains a YAML tab", name)
		}
		var parsed any
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}
}

func TestCreateDraftRendersSafelyAndProtectsManagedPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "create-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	if err := os.RemoveAll(cfg.DerivedDir()); err != nil {
		t.Fatal(err)
	}
	title := `Use "foo" \\ path`
	output := filepath.Join(t.TempDir(), "draft.md")
	result, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "guide", Title: title, Output: output,
		Set: []string{"description=Quoted title fixture", "applies_to=[macOS, LLVM]"},
		Now: time.Date(2026, 8, 9, 12, 34, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TemplateVersion != "1.3.0" || !strings.Contains(result.NextCommandHint, "publish propose") {
		t.Fatalf("unexpected result: %#v", result)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	meta, body, err := document.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != title || !strings.Contains(string(body), "# "+title) {
		t.Fatalf("title was not rendered safely: %#v %q", meta.Title, body)
	}
	if got, ok := meta.Extra["applies_to"].([]any); !ok || len(got) != 2 {
		t.Fatalf("YAML list --set was not preserved: %#v", meta.Extra["applies_to"])
	}

	alias := filepath.Join(t.TempDir(), "drafts")
	if err := os.Symlink(cfg.KnowledgeDir(), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "concept", Title: "Unsafe", Output: filepath.Join(alias, "unsafe.md"),
	}); err == nil {
		t.Fatal("template create followed a parent symlink into knowledge")
	}
	rawResult, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "raw", Name: "source", Title: "Raw", Output: filepath.Join(t.TempDir(), "source.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawResult.NextCommandHint, "raw add") {
		t.Fatalf("raw template returned wrong next command: %s", rawResult.NextCommandHint)
	}
	if _, err := templates.ReadContent(cfg, "knowledge", "../../concept"); err == nil {
		t.Fatal("template name traversal was silently normalized")
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

func TestTemplateUpgradeBaselinesLegacyKnowledgeWithLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "legacy-upgrade", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	body := []byte("# Legacy with lifecycle\n\nLegacy body.\n")
	legacy := document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5faw", Type: "concept", Title: "Legacy with lifecycle", Status: "published",
		PublishedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", ContentHash: document.HashBytes(body),
		Sources: []document.SourceRef{{ID: "raw_01arz3ndektsv4rrffq69g5fav", ContentHash: document.HashBytes([]byte("raw"))}},
		Extra:   map[string]any{"lifecycle": "archived", "valid_until": "legacy-custom-value"},
	}
	legacyPath := filepath.Join(cfg.KnowledgeDir(), "concept", "legacy--"+legacy.ID+".md")
	if err := document.Write(legacyPath, legacy, body); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cfg.RuntimeDir(), "template-state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state templates.InstallState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	state.TemplateVersion = "1.1.1"
	stateBytes, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, append(stateBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Template.Version = "1.1.1"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	_, affected, err := templates.ApplyUpgrade(cfg, false, false)
	if err != nil {
		t.Fatal(err)
	}
	foundBaseline := false
	for _, path := range affected {
		foundBaseline = foundBaseline || path == governance.StateFileName
	}
	if !foundBaseline {
		t.Fatalf("upgrade did not report the durable governance baseline: %#v", affected)
	}
	legacyDoc, err := document.Read(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	strict, isLegacy, err := governance.GovernanceMode(cfg, legacyDoc)
	if err != nil || strict || !isLegacy {
		t.Fatalf("legacy document with lifecycle was not recognized: strict=%v legacy=%v err=%v", strict, isLegacy, err)
	}
	assessment, err := governance.AssessStoredLifecycle(cfg, legacyDoc.Metadata, time.Now(), isLegacy)
	if err != nil || assessment.Lifecycle != "current" || len(assessment.Warnings) != 1 {
		t.Fatalf("legacy custom lifecycle was interpreted: %#v %v", assessment, err)
	}
	if _, err := os.Stat(filepath.Join(root, governance.StateFileName)); err != nil {
		t.Fatalf("durable governance baseline is missing: %v", err)
	}
}
