package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/sqlite3simple"
)

func TestCompleteCLIWorkflow(t *testing.T) {
	t.Setenv("LLM_WIKI_CONFIG", filepath.Join(t.TempDir(), "user-config.toml"))
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "workflow", "--json", "--no-interactive")
	rawResponse := runCLI(t, "# Source\n\n稳定 IR 解耦编译器组件。\n", "raw", "add", "-", "--name", "source.md", "--wiki", root, "--json", "--no-interactive")
	rawID := nestedString(t, rawResponse.Data, "items", 0, "id")
	indexAfterCapture := runCLI(t, "", "index", "status", "--wiki", root, "--json", "--no-interactive")
	if documentsValue, exists := indexAfterCapture.Data.(map[string]any)["documents"]; exists {
		documents := documentsValue.(map[string]any)
		if rawCount, exists := documents["raw"]; exists && rawCount.(float64) != 0 {
			t.Fatalf("raw add updated the searchable index: %#v", indexAfterCapture.Data)
		}
	}
	beforePublish := runCLI(t, "", "query", "稳定 IR", "--wiki", root, "--json", "--no-interactive")
	beforeData := beforePublish.Data.(map[string]any)
	if nestedFloat(t, beforePublish.Data, "count") != 0 {
		t.Fatalf("query returned unpublished raw content: %#v", beforePublish.Data)
	}
	if _, exists := beforeData["raw_evidence"]; exists {
		t.Fatalf("query exposed a raw retrieval channel: %#v", beforePublish.Data)
	}
	inbox := runCLI(t, "", "raw", "list", "--unreferenced", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, inbox.Data, "count") != 1 {
		t.Fatalf("new raw evidence was not visible in the unreferenced inbox: %#v", inbox.Data)
	}
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, governedDraft("稳定 IR", "稳定 IR 解耦编译器组件。", rawID), 0o600); err != nil {
		t.Fatal(err)
	}
	proposalResponse := runCLI(t, "", "publish", "propose", "--source", rawID, "--file", draft, "--wiki", root, "--json", "--no-interactive")
	changeID := nestedString(t, proposalResponse.Data, "change_id")
	knowledgeID := nestedString(t, proposalResponse.Data, "knowledge_id")
	runCLI(t, "", "publish", "diff", changeID, "--wiki", root, "--json", "--no-interactive")
	apply := runCLI(t, "", "publish", "apply", changeID, "--wiki", root, "--json", "--no-interactive")
	if _, ok := apply.Data.(map[string]any)["index"].(map[string]any); !ok {
		t.Fatalf("publish apply did not refresh the knowledge index: %#v", apply.Data)
	}
	inbox = runCLI(t, "", "raw", "list", "--unreferenced", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, inbox.Data, "count") != 0 {
		t.Fatalf("published raw evidence remained in the unreferenced inbox: %#v", inbox.Data)
	}
	query := runCLI(t, "", "query", "稳定 IR", "--wiki", root, "--json", "--no-interactive")
	if count := nestedFloat(t, query.Data, "count"); count < 1 {
		t.Fatalf("expected query evidence, got %#v", query.Data)
	}
	if normalized := nestedString(t, query.Data, "normalized_query"); normalized != "稳定 ir" {
		t.Fatalf("unexpected normalized query %q", normalized)
	}
	if factsFrom := nestedString(t, query.Data, "facts_from"); factsFrom != "knowledge_markdown" {
		t.Fatalf("query facts came from %q", factsFrom)
	}
	if mode := nestedString(t, query.Data, "evidence", 0, "retrieval_mode"); mode != "strict" {
		t.Fatalf("unexpected evidence retrieval mode %q", mode)
	}
	trace := runCLI(t, "", "trace", knowledgeID, "--wiki", root, "--json", "--no-interactive")
	if valid, ok := trace.Data.(map[string]any)["valid"].(bool); !ok || !valid {
		t.Fatalf("invalid trace %#v", trace.Data)
	}
	runCLI(t, "", "index", "rebuild", "--wiki", root, "--json", "--no-interactive")
	doctor := runCLI(t, "", "doctor", "--wiki", root, "--json", "--no-interactive")
	if healthy, ok := doctor.Data.(map[string]any)["healthy"].(bool); !ok || !healthy {
		t.Fatalf("doctor failed after complete workflow: %#v", doctor.Data)
	}
}

func TestQueryRejectsWrapperOnlyChinese(t *testing.T) {
	t.Setenv("LLM_WIKI_CONFIG", filepath.Join(t.TempDir(), "user-config.toml"))
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "empty-query", "--json", "--no-interactive")
	for _, query := range []string{"的", "是什么"} {
		response := runCLIFailure(t, "", "query", query, "--wiki", root, "--json", "--no-interactive")
		if response.Error == nil || response.Error.Code != "QUERY_INVALID" {
			t.Fatalf("query %q returned %#v", query, response)
		}
	}
}

func TestPublishApplyConflictUsesStableMachineCode(t *testing.T) {
	t.Setenv("LLM_WIKI_CONFIG", filepath.Join(t.TempDir(), "user-config.toml"))
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "apply-conflict", "--json", "--no-interactive")
	rawResponse := runCLI(t, "# Source\n\nOriginal evidence.\n", "raw", "add", "-", "--name", "source.md", "--wiki", root, "--json", "--no-interactive")
	rawID := nestedString(t, rawResponse.Data, "items", 0, "id")
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, governedDraft("Conflict", "Original evidence.", rawID), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal := runCLI(t, "", "publish", "propose", "--source", rawID, "--file", draft, "--wiki", root, "--json", "--no-interactive")
	changeID := nestedString(t, proposal.Data, "change_id")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	rawDoc, err := document.FindByID(cfg.RawDir(), rawID)
	if err != nil {
		t.Fatal(err)
	}
	rawDoc.Body = append(rawDoc.Body, []byte("\nChanged after proposal.\n")...)
	rawDoc.Metadata.ContentHash = document.HashBytes(document.NormalizeMarkdownBody(rawDoc.Body))
	if err := document.Write(rawDoc.Path, rawDoc.Metadata, rawDoc.Body); err != nil {
		t.Fatal(err)
	}

	response := runCLIFailure(t, "", "publish", "apply", changeID, "--wiki", root, "--json", "--no-interactive")
	if response.Error.Code != "PUBLISH_BASE_CHANGED" {
		t.Fatalf("apply conflict returned unstable protocol error: %#v", response.Error)
	}
}

func TestAuxiliaryCLICommandSurface(t *testing.T) {
	t.Setenv("LLM_WIKI_CONFIG", filepath.Join(t.TempDir(), "user-config.toml"))
	skillsRoot := filepath.Join(t.TempDir(), "skills")
	t.Setenv("LLM_WIKI_CODEX_SKILLS_DIR", skillsRoot)
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "surface", "--json", "--no-interactive")
	runCLI(t, "", "locate", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "status", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "index", "status", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "index", "update", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "template", "list", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "template", "show", "concept", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "template", "upgrade", "--plan", "--wiki", root, "--json", "--no-interactive")
	templateOutput := filepath.Join(t.TempDir(), "source-template.md")
	createdTemplate := runCLI(t, "", "template", "create", "source", "--kind", "raw", "--title", `A "quoted" source`,
		"--output", templateOutput, "--set", "origin=web", "--set", "authors=[Alice, Bob]", "--wiki", root, "--json", "--no-interactive")
	if version := nestedString(t, createdTemplate.Data, "template_version"); version != "1.4.0" {
		t.Fatalf("template create returned version %q", version)
	}
	templateRaw := runCLI(t, "", "raw", "add", templateOutput, "--wiki", root, "--json", "--no-interactive")
	templateRawID := nestedString(t, templateRaw.Data, "items", 0, "id")
	templateRawShow := runCLI(t, "", "raw", "show", templateRawID, "--wiki", root, "--json", "--no-interactive")
	if origin := nestedString(t, templateRawShow.Data, "metadata", "origin"); origin != "web" {
		t.Fatalf("raw add replaced template origin with %q", origin)
	}

	rawResponse := runCLI(t, "# Reject source\n", "raw", "add", "-", "--name", "reject.md", "--wiki", root, "--json", "--no-interactive")
	rawID := nestedString(t, rawResponse.Data, "items", 0, "id")
	runCLI(t, "", "raw", "list", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "raw", "show", rawID, "--wiki", root, "--json", "--no-interactive")
	draft := filepath.Join(t.TempDir(), "reject-draft.md")
	if err := os.WriteFile(draft, governedDraft("Rejected knowledge", "Rejected knowledge remains reviewable.", rawID), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal := runCLI(t, "", "publish", "propose", "--source", rawID, "--file", draft, "--wiki", root, "--json", "--no-interactive")
	changeID := nestedString(t, proposal.Data, "change_id")
	runCLI(t, "", "publish", "reject", changeID, "--reason", "test rejection", "--wiki", root, "--json", "--no-interactive")

	runCLI(t, "", "skill", "status", "codex", "--json", "--no-interactive")
	installedSkills := runCLI(t, "", "skill", "install", "codex", "--yes", "--json", "--no-interactive")
	if skills, ok := installedSkills.Data.(map[string]any)["skills"].([]any); !ok || len(skills) != 4 {
		t.Fatalf("skill install did not return four independent skills: %#v", installedSkills.Data)
	}
	if _, err := os.Stat(filepath.Join(skillsRoot, "llm-wiki-query", "SKILL.md")); err != nil {
		t.Fatalf("query skill was not installed as a sibling directory: %v", err)
	}
	runCLI(t, "", "skill", "update", "codex", "--yes", "--json", "--no-interactive")
	runCLI(t, "", "skill", "uninstall", "codex", "--yes", "--json", "--no-interactive")
	if _, err := os.Stat(filepath.Join(skillsRoot, "llm-wiki-query", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("query skill remains after uninstall: %v", err)
	}
}

func TestQueryUsesIndexForCandidatesAndKnowledgeForFacts(t *testing.T) {
	t.Setenv("LLM_WIKI_CONFIG", filepath.Join(t.TempDir(), "user-config.toml"))
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "facts", "--json", "--no-interactive")
	rawResponse := runCLI(t, "# Source\n\nOriginal evidence.\n", "raw", "add", "-", "--name", "source.md", "--wiki", root, "--json", "--no-interactive")
	rawID := nestedString(t, rawResponse.Data, "items", 0, "id")
	rawPath := nestedString(t, rawResponse.Data, "items", 0, "path")
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, governedDraft("File facts", "Original evidence.", rawID), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal := runCLI(t, "", "publish", "propose", "--source", rawID, "--file", draft, "--wiki", root, "--json", "--no-interactive")
	changeID := nestedString(t, proposal.Data, "change_id")
	knowledgeID := nestedString(t, proposal.Data, "knowledge_id")
	runCLI(t, "", "publish", "apply", changeID, "--wiki", root, "--json", "--no-interactive")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(sqlite3simple.DriverName, indexstore.DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE chunks SET body='Poisoned SQLite body' WHERE document_id=?`, knowledgeID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE documents SET title='Poisoned SQLite title',metadata_json='{}' WHERE id=?`, knowledgeID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE chunks_fts SET body=body || ' Poisoned SQLite FTS body',title='Poisoned SQLite FTS title' WHERE document_id=?`, knowledgeID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	query := runCLI(t, "", "query", "Original evidence", "--wiki", root, "--json", "--no-interactive")
	bodyBeforeRebuild := nestedString(t, query.Data, "evidence", 0, "body")
	if !strings.Contains(bodyBeforeRebuild, "Original evidence.") || strings.Contains(bodyBeforeRebuild, "Poisoned") {
		t.Fatalf("query returned cached SQLite body instead of published Markdown: %q", bodyBeforeRebuild)
	}
	titleBeforeRebuild := nestedString(t, query.Data, "evidence", 0, "title")
	if titleBeforeRebuild != "File facts" {
		t.Fatalf("query returned cached SQLite metadata instead of published Markdown: %q", titleBeforeRebuild)
	}
	if err := os.Remove(indexstore.DBPath(cfg)); err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	rebuiltQuery := runCLI(t, "", "query", "Original evidence", "--wiki", root, "--json", "--no-interactive")
	if body := nestedString(t, rebuiltQuery.Data, "evidence", 0, "body"); body != bodyBeforeRebuild {
		t.Fatalf("evidence changed after deleting and rebuilding SQLite: before=%q after=%q", bodyBeforeRebuild, body)
	}
	if title := nestedString(t, rebuiltQuery.Data, "evidence", 0, "title"); title != titleBeforeRebuild {
		t.Fatalf("title changed after deleting and rebuilding SQLite: before=%q after=%q", titleBeforeRebuild, title)
	}

	knowledge.Body = append(knowledge.Body, []byte("\nUniqueFileFactToken\n")...)
	knowledge.Metadata.ContentHash = document.HashBytes(document.NormalizeMarkdownBody(knowledge.Body))
	if err := document.Write(knowledge.Path, knowledge.Metadata, knowledge.Body); err != nil {
		t.Fatal(err)
	}
	stale := runCLIFailure(t, "", "query", "Original evidence", "--wiki", root, "--json", "--no-interactive")
	if stale.Error == nil || stale.Error.Code != "INDEX_STALE" {
		t.Fatalf("query trusted a stale index: %#v", stale)
	}
	runCLI(t, "", "index", "update", "--wiki", root, "--json", "--no-interactive")
	query = runCLI(t, "", "query", "UniqueFileFactToken", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, query.Data, "count") < 1 {
		t.Fatalf("query did not use the explicitly updated index: %#v", query)
	}

	path := filepath.Join(root, filepath.FromSlash(rawPath))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("Original evidence."), []byte("Tampered evidence."), 1)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	query = runCLI(t, "", "query", "UniqueFileFactToken", "--wiki", root, "--json", "--no-interactive")
	if body := nestedString(t, query.Data, "evidence", 0, "body"); !strings.Contains(body, "UniqueFileFactToken") {
		t.Fatalf("raw drift incorrectly prevented reading the published knowledge fact: %#v", query)
	}
	trace := runCLI(t, "", "trace", knowledgeID, "--wiki", root, "--json", "--no-interactive")
	data := trace.Data.(map[string]any)
	if valid, ok := data["valid"].(bool); !ok || valid {
		t.Fatalf("trace trusted recorded metadata instead of actual raw bytes: %#v", trace.Data)
	}
}

func governedDraft(title, fact, rawID string) []byte {
	return []byte(fmt.Sprintf("---\ntype: concept\ntitle: %q\ndescription: Test knowledge for %s\nlifecycle: current\n---\n# %s\n\n%s[^%s-1]\n\n[^%s-1]: locator: test fixture\n",
		title, title, title, fact, rawID, rawID))
}

func runCLI(t *testing.T, stdin string, args ...string) Response {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(stdin), &stdout, &stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("command %v: %v stderr=%s stdout=%s", args, err, stderr.String(), stdout.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %v output %q: %v", args, stdout.String(), err)
	}
	if !response.OK {
		t.Fatalf("command %v returned failure %#v", args, response)
	}
	var generic any
	b, _ := json.Marshal(response.Data)
	_ = json.Unmarshal(b, &generic)
	response.Data = generic
	return response
}

func runCLIFailure(t *testing.T, stdin string, args ...string) Response {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(stdin), &stdout, &stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		t.Fatalf("command %v unexpectedly succeeded stdout=%s", args, stdout.String())
	}
	if code := RenderFailure(root, err); code == ExitOK {
		t.Fatalf("command %v returned a zero failure code", args)
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %v failure output %q: %v stderr=%s", args, stdout.String(), err, stderr.String())
	}
	if response.OK || response.Error == nil {
		t.Fatalf("command %v returned invalid failure %#v", args, response)
	}
	return response
}

func nestedString(t *testing.T, value any, path ...any) string {
	t.Helper()
	current := value
	for _, part := range path {
		switch key := part.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				t.Fatalf("%v is not an object at %s", current, key)
			}
			current = m[key]
		case int:
			a, ok := current.([]any)
			if !ok || key >= len(a) {
				t.Fatalf("%v is not an array at %d", current, key)
			}
			current = a[key]
		default:
			t.Fatal(fmt.Sprintf("unsupported path component %T", part))
		}
	}
	out, ok := current.(string)
	if !ok {
		t.Fatalf("%v is not a string", current)
	}
	return out
}

func nestedFloat(t *testing.T, value any, key string) float64 {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object", value)
	}
	out, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%v is not a number", m[key])
	}
	return out
}
