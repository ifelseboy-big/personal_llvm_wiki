package templates_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

func TestPersonalTemplateMatchesDesignBaseline(t *testing.T) {
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
		baselinePath := filepath.Join(repositoryRoot, "docs", "template-design", manifest.Name, filepath.FromSlash(relative))
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
	if len(types) != 6 {
		t.Fatalf("personal content pack must expose six document types: %#v", types)
	}
	for _, name := range []string{"plan", "guide", "note", "config", "rule", "review"} {
		if !types[name] {
			t.Fatalf("personal content pack omitted type %s", name)
		}
		builtIn, err := templates.ReadContent(nil, "", name)
		if err != nil || builtIn.Kind != "knowledge" {
			t.Fatalf("built-in template name %s resolves ambiguously: %#v %v", name, builtIn, err)
		}
		installed, err := templates.ReadContent(initialized.Config, "", name)
		if err != nil || installed.Kind != builtIn.Kind || installed.Path != builtIn.Path {
			t.Fatalf("installed template name %s resolves differently: %#v %v", name, installed, err)
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
	if manifest.Version != "2.0.2" || manifest.ContentPack != "content-pack.json" {
		t.Fatalf("unexpected personal template version %s", manifest.Version)
	}
	agents, err := templates.ReadFile("personal", "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "`knowledge/` 中由 `promote apply` 写入的 Markdown 是唯一可信事实源") ||
		!strings.Contains(string(agents), "Inbox -> Promotion -> Knowledge") ||
		!strings.Contains(string(agents), "禁止直接创建、修改、移动或删除 `knowledge/` 文件") ||
		!strings.Contains(string(agents), "用户只要求检查、解释或预览时不得生成草稿") ||
		!strings.Contains(string(agents), "--wiki <vault-root> --json --no-interactive") {
		t.Fatalf("personal AGENTS.md omitted its management-only or retrieval boundary: %s", agents)
	}
	workflowRequirements := map[string][]string{
		"capture":  {"stdin 输入时必须提供", "--name <name>", "--summary", "index.md", "--batch-manifest", "相同 title", "docs/", "attachments/", "不得自动添加 `--allow-sensitive`"},
		"organize": {"规范 `payload_path`", "`content_hash` 和 `file_hash`", "`proposed_knowledge_id`", "Organize 不运行 `promote plan/apply`"},
		"publish":  {"plan hash 完全一致", "然后停止", "`transaction_state`", "不得重建一个“相似计划”沿用旧批准"},
		"maintain": {"检查阶段", "零写入", "通过 Capture 保存为 pending Inbox", "同一 Promotion"},
		"query":    {"没有足够的已发布证据", "Query 零写入", "`RECOVERY_REQUIRED`"},
	}
	for _, workflow := range []string{"capture", "organize", "publish", "maintain", "query"} {
		content, err := templates.ReadFile("personal", "workflows/"+workflow+".md")
		if err != nil {
			t.Fatalf("personal template omitted %s workflow: %v", workflow, err)
		}
		for _, required := range workflowRequirements[workflow] {
			if !strings.Contains(string(content), required) {
				t.Fatalf("%s workflow omitted executable contract %q: %s", workflow, required, content)
			}
		}
	}
	promoteRule, err := templates.ReadFile("personal", "rules/promote.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"\"operation\": \"create\"", "\"operation\": \"update\"", "\"base_content_hash\"", "\"base_file_hash\"", "`proposed_knowledge_id`"} {
		if !strings.Contains(string(promoteRule), required) {
			t.Fatalf("promotion rule omitted manifest contract %q: %s", required, promoteRule)
		}
	}
	for _, name := range []string{"plan", "guide", "note", "config", "rule", "review"} {
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
	configuration, err := templates.ReadContent(nil, "knowledge", "config")
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
		Kind: "knowledge", Name: "guide", Title: title, Output: output,
		Set: []string{"category=development", "description=Quoted title fixture", "applies_to=[macOS, LLVM]"},
		Now: time.Date(2026, 8, 9, 12, 34, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TemplateVersion != "2.0.2" || !strings.Contains(result.NextCommandHint, "promote plan") {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !document.ValidID("know", result.ProposedID) || !strings.Contains(result.NextCommandHint, result.ProposedID) {
		t.Fatalf("knowledge draft omitted its CLI-generated proposed id: %#v", result)
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
		Kind: "knowledge", Name: "guide", Title: "Override", Output: filepath.Join(t.TempDir(), "override.md"),
		Set: []string{"type=note"},
	}); err == nil {
		t.Fatal("template create allowed --set to override the content-pack type")
	}

	alias := filepath.Join(t.TempDir(), "drafts")
	if err := os.Symlink(cfg.KnowledgeDir(), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "knowledge", Name: "note", Title: "Unsafe", Output: filepath.Join(alias, "unsafe.md"),
	}); err == nil {
		t.Fatal("template create followed a parent symlink into knowledge")
	}
	inboxResult, err := templates.CreateDraft(cfg, templates.CreateOptions{
		Kind: "inbox", Name: "capture", Title: "Inbox", Output: filepath.Join(t.TempDir(), "note.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inboxResult.NextCommandHint, "inbox add") || !strings.Contains(inboxResult.NextCommandHint, "--note-file") {
		t.Fatalf("inbox template returned wrong next command: %s", inboxResult.NextCommandHint)
	}
	if inboxResult.ProposedID != "" {
		t.Fatalf("inbox draft unexpectedly received a knowledge id: %#v", inboxResult)
	}
	if _, err := templates.ReadContent(cfg, "knowledge", "../../note"); err == nil {
		t.Fatal("template name traversal was silently normalized")
	}
}

func TestPersonalKnowledgeDraftsRenderAndRequirePromptResolution(t *testing.T) {
	initialized, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "knowledge-drafts", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	policy, err := governance.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	promptPattern := regexp.MustCompile(`(?s)%% llm-wiki:prompt .*? %%`)
	for _, kind := range policy.Types {
		t.Run(kind.Name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "draft.md")
			set := []string{"category=development", "description=Template rendering fixture"}
			if kind.Name == "config" {
				set = append(set, "system=fixture", "environment=test")
			}
			opts := templates.CreateOptions{
				Kind: "knowledge", Name: kind.Name, Title: "正文：" + kind.Name, Output: output,
				Set: set, Now: now, DryRun: true,
			}
			preview, err := templates.CreateDraft(cfg, opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dry-run wrote a draft: %v", err)
			}
			opts.DryRun = false
			result, err := templates.CreateDraft(cfg, opts)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			meta, body, err := document.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.UnresolvedVars) != 0 || len(preview.UnresolvedVars) != 0 || strings.Contains(string(data), "{{") {
				t.Fatalf("template left unresolved variables: %#v", result.UnresolvedVars)
			}
			prompts := promptPattern.FindAll(body, -1)
			if len(prompts) == 0 || result.PromptCount != len(prompts) || preview.PromptCount != result.PromptCount {
				t.Fatalf("prompt blocks or preview counts differ: blocks=%d actual=%d preview=%d", len(prompts), result.PromptCount, preview.PromptCount)
			}
			meta.ID = result.ProposedID
			meta.GovernanceVersion = policy.GovernanceVersion
			doc := &document.Document{Metadata: meta, Body: body}
			if err := governance.ValidateForPromotion(cfg, doc, nil, now); err == nil {
				t.Fatal("unresolved template prompts passed promotion governance")
			}
			// Check the machine contract after prompt removal, not editorial completeness.
			doc.Body = promptPattern.ReplaceAll(body, nil)
			if err := governance.ValidateForPromotion(cfg, doc, nil, now); err != nil {
				t.Fatalf("rendered template has a non-prompt governance failure: %v", err)
			}
		})
	}
}

func TestPersonalContentPackRejectsRemovedTypesAndGovernance(t *testing.T) {
	initialized, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "removed-types", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	policy, err := governance.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	doc := &document.Document{
		Metadata: document.Metadata{
			Type: "note", Title: "Current note", GovernanceVersion: policy.GovernanceVersion,
			Extra: map[string]any{"category": "learning", "description": "Current contract", "lifecycle": "current"},
		},
		Body: []byte("# Current note\n\nSelf-contained test content.\n"),
	}
	if err := governance.ValidateForPromotion(cfg, doc, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"requirement", "design", "decision", "runbook", "business-process", "learning-note", "concept", "configuration", "business-rule", "retrospective"} {
		t.Run(name, func(t *testing.T) {
			if _, err := templates.ReadContent(nil, "knowledge", name); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed built-in type is still available: %v", err)
			}
			output := filepath.Join(t.TempDir(), "draft.md")
			if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
				Kind: "knowledge", Name: name, Title: "Removed type", Output: output, Now: now,
			}); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed type was not rejected: %v", err)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected type wrote a draft: %v", err)
			}
			removed := *doc
			removed.Metadata.Type = name
			if err := governance.ValidateForPromotion(cfg, &removed, nil, now); err == nil {
				t.Fatal("removed type passed promotion governance")
			}
		})
	}
	oldGovernance := *doc
	oldGovernance.Metadata.GovernanceVersion = "personal-1.0.0"
	if err := governance.ValidateStored(cfg, &oldGovernance, now); err == nil {
		t.Fatal("non-current governance version was accepted")
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

func TestTemplateUpgradeHandlesRemovedKnowledgeTemplates(t *testing.T) {
	for _, modified := range []bool{false, true} {
		name, wantAction := "unmodified", "remove"
		if modified {
			name, wantAction = "user-modified", "obsolete"
		}
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "wiki")
			initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "obsolete-template", Template: "personal"})
			if err != nil {
				t.Fatal(err)
			}
			relative := "templates/knowledge/concept.md"
			obsoletePath := filepath.Join(root, filepath.FromSlash(relative))
			obsoleteContent := []byte("# Previous template\n")
			currentContent := obsoleteContent
			if modified {
				currentContent = []byte("# User-customized template\n")
			}
			if err := os.WriteFile(obsoletePath, currentContent, 0o600); err != nil {
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
			state.Files = append(state.Files, templates.FileState{Path: relative, Hash: document.HashBytes(obsoleteContent)})
			stateBytes, err = json.MarshalIndent(state, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			stateBytes = append(stateBytes, '\n')
			if err := os.WriteFile(statePath, stateBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			preview, affected, err := templates.ApplyUpgrade(initialized.Config, false, true)
			if err != nil || len(affected) != 0 {
				t.Fatalf("upgrade preview failed or wrote files: %#v %v", affected, err)
			}
			for path, want := range map[string][]byte{obsoletePath: currentContent, statePath: stateBytes} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("upgrade preview changed %s: %v", path, err)
				}
			}
			plan, _, err := templates.ApplyUpgrade(initialized.Config, false, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []*templates.UpgradePlan{preview, plan} {
				found := false
				for _, action := range candidate.Actions {
					if action.Path == relative && action.Action == wantAction {
						found = true
					}
				}
				if !found {
					t.Fatalf("removed template did not plan %s: %#v", wantAction, candidate.Actions)
				}
			}
			if modified {
				got, err := os.ReadFile(obsoletePath)
				if err != nil || !bytes.Equal(got, currentContent) {
					t.Fatalf("user template was changed by upgrade: %v", err)
				}
			} else if _, err := os.Stat(obsoletePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unmodified removed template remains after upgrade: %v", err)
			}
			if _, err := templates.ReadContent(initialized.Config, "knowledge", "concept"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("obsolete file was treated as an active template: %v", err)
			}
		})
	}
}
