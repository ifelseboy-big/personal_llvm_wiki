package templates_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

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

func TestPersonalContentPackDeclaresOrthogonalDomainsTypesAndWorkflows(t *testing.T) {
	initialized, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "policy-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := governance.Load(initialized.Config)
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]bool{}
	for _, item := range policy.Categories {
		categories[item.Name] = true
	}
	for _, name := range []string{"development", "learning", "configuration", "business"} {
		if !categories[name] {
			t.Fatalf("personal content pack omitted category %s", name)
		}
	}
	types := map[string]bool{}
	for _, item := range policy.Types {
		types[item.Name] = true
	}
	for _, name := range []string{"requirement", "design", "decision", "runbook", "retrospective", "learning-note", "concept", "configuration", "business-rule", "business-process"} {
		if !types[name] {
			t.Fatalf("personal content pack omitted type %s", name)
		}
	}
	if len(policy.Workflows) != 5 {
		t.Fatalf("personal content pack must route five workflows: %#v", policy.Workflows)
	}
}

func TestPersonalTemplatesExposeInboxPromotionAndOptionalViews(t *testing.T) {
	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "3.0.0" || manifest.ContentPack != "content-pack.json" {
		t.Fatalf("unexpected personal template version %s", manifest.Version)
	}
	agents, err := templates.ReadFile("personal", "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "`knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源") ||
		!strings.Contains(string(agents), "Inbox -> Promotion -> Knowledge") ||
		!strings.Contains(string(agents), "禁止直接创建、修改或移动 `knowledge/` 文件") {
		t.Fatalf("personal AGENTS.md omitted its management-only or retrieval boundary: %s", agents)
	}
	for _, workflow := range []string{"capture", "organize", "publish", "maintain", "query"} {
		if _, err := templates.ReadFile("personal", "workflows/"+workflow+".md"); err != nil {
			t.Fatalf("personal template omitted %s workflow: %v", workflow, err)
		}
	}
	for _, name := range []string{"requirement", "design", "decision", "runbook", "retrospective", "learning-note", "concept", "configuration", "business-rule", "business-process"} {
		item, err := templates.ReadContent(nil, "knowledge", name)
		if err != nil {
			t.Fatal(err)
		}
		meta, _, err := document.Parse([]byte(item.Content))
		if err != nil {
			t.Fatalf("parse %s template: %v", name, err)
		}
		if meta.Type != name {
			t.Fatalf("%s template declares type %q", name, meta.Type)
		}
		if category, ok := meta.Extra["category"].(string); !ok || category != "" {
			t.Fatalf("%s template preselects category instead of keeping category/type orthogonal: %#v", name, meta.Extra["category"])
		}
		for _, property := range []string{"category", "description", "lifecycle", "related"} {
			if _, ok := meta.Extra[property]; !ok {
				t.Fatalf("%s template is missing %s", name, property)
			}
		}
		if meta.Tags == nil || meta.Aliases == nil {
			t.Fatalf("%s template must declare tags and aliases lists", name)
		}
	}
	configuration, err := templates.ReadContent(nil, "knowledge", "configuration")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"密码", "Token", "私钥", "秘密"} {
		if !strings.Contains(configuration.Content, forbidden) {
			t.Fatalf("configuration template omitted secret prohibition %q", forbidden)
		}
	}
	for _, name := range []string{"knowledge.base", "inbox.base"} {
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
	title := `Use "foo" \\ path`
	output := filepath.Join(t.TempDir(), "draft.md")
	result, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "runbook", Title: title, Output: output,
		Set: []string{"category=development", "description=Quoted title fixture", "applies_to=[macOS, LLVM]"},
		Now: time.Date(2026, 8, 9, 12, 34, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TemplateVersion != "3.0.0" || !strings.Contains(result.NextCommandHint, "promote plan") {
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
	if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "runbook", Title: "Override", Output: filepath.Join(t.TempDir(), "override.md"),
		Set: []string{"type=concept"},
	}); err == nil {
		t.Fatal("template create allowed --set to override the content-pack type")
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
	inboxResult, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "inbox", Name: "note", Title: "Inbox", Output: filepath.Join(t.TempDir(), "note.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inboxResult.NextCommandHint, "inbox add") || !strings.Contains(inboxResult.NextCommandHint, "--note-file") {
		t.Fatalf("inbox template returned wrong next command: %s", inboxResult.NextCommandHint)
	}
	if _, err := templates.ReadContent(cfg, "knowledge", "../../concept"); err == nil {
		t.Fatal("template name traversal was silently normalized")
	}
}

func TestWikiContentPackAddsCategoryTypeAndTemplateUsingDataOnly(t *testing.T) {
	initialized, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "data-extension", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	policy, err := governance.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy.Categories = append(policy.Categories, governance.NamedDefinition{Name: "research-domain", Description: "Declared only by test content data"})
	policy.Types = append(policy.Types, governance.TypeRule{
		Name: "field-note", Description: "Declared only by test content data", Template: "templates/knowledge/field-note.md",
		Fields: []governance.FieldRule{{Name: "confidence", Kind: "enum", Required: true, Values: []string{"observed", "estimated"}}},
	})
	policy.Knowledge.Relations[0].Field = "connections"
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ContentPackPath(), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(cfg.TemplatesDir(), "knowledge", "field-note.md")
	template := "---\ntype: field-note\ncategory: \"\"\ntitle: \"{{title}}\"\ndescription: \"\"\nlifecycle: current\nconfidence: observed\naliases: []\ntags: []\nconnections: []\nsupersedes: []\nsuperseded_by: []\n---\n# {{title}}\n\n%% llm-wiki:prompt Record observed evidence and boundaries. %%\n"
	if err := os.WriteFile(templatePath, []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := templates.ListContent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		found = found || (item.Kind == "knowledge" && item.Name == "field-note" && item.Path == "templates/knowledge/field-note.md")
	}
	if !found {
		t.Fatalf("data-declared template was not discovered: %#v", items)
	}
	output := filepath.Join(t.TempDir(), "field-note.md")
	relatedID := "know_01arz3ndektsv4rrffq69g5faa"
	relatedBody := []byte("# Existing\n")
	relatedMeta := document.Metadata{
		ID: relatedID, Type: "field-note", Title: "Existing", Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: document.HashBytes(relatedBody),
		GovernanceVersion: policy.GovernanceVersion,
		Lineage:           []document.LineageRef{{InboxID: "inbox_01arz3ndektsv4rrffq69g5fav", PayloadHash: document.HashBytes([]byte("payload")), Source: "test", CapturedAt: "2026-08-08T00:00:00Z"}},
		Extra:             map[string]any{"category": "research-domain", "description": "Existing", "lifecycle": "current", "confidence": "observed"},
	}
	relatedPath := filepath.Join(cfg.KnowledgeDir(), "field-note", "existing--"+relatedID+".md")
	if err := document.Write(relatedPath, relatedMeta, relatedBody); err != nil {
		t.Fatal(err)
	}
	if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "field-note", Title: "Test observation", Output: output,
		Set: []string{"category=research-domain", "description=Observed only in test data", "confidence=observed"}, Related: []string{relatedID},
	}); err != nil {
		t.Fatalf("data-declared template could not create a draft: %v", err)
	}
	draftBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	draftMeta, _, err := document.Parse(draftBytes)
	if err != nil {
		t.Fatal(err)
	}
	connections, ok := draftMeta.Extra["connections"].([]any)
	if !ok || len(connections) != 1 || connections[0] != relatedID {
		t.Fatalf("--related did not use the data-declared default relation: %#v", draftMeta.Extra["connections"])
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

func TestTemplateUpgradeRemovesUnmodifiedObsoleteFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "obsolete-template", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	obsoletePath := filepath.Join(root, "rules", "derived.md")
	obsoleteContent := []byte("# Legacy derived rule\n")
	if err := os.WriteFile(obsoletePath, obsoleteContent, 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(initialized.Config.RuntimeDir(), "template-state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state templates.InstallState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	state.Files = append(state.Files, templates.FileState{Path: "rules/derived.md", Hash: document.HashBytes(obsoleteContent)})
	stateBytes, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, append(stateBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, _, err := templates.ApplyUpgrade(initialized.Config, false, false)
	if err != nil {
		t.Fatal(err)
	}
	foundRemoval := false
	for _, action := range plan.Actions {
		if action.Path == "rules/derived.md" && action.Action == "remove" {
			foundRemoval = true
		}
	}
	if !foundRemoval {
		t.Fatalf("obsolete unmodified file was not planned for removal: %#v", plan.Actions)
	}
	if _, err := os.Stat(obsoletePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("obsolete unmodified file remains after upgrade: %v", err)
	}
}
