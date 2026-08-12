package governance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
)

const (
	governanceInboxID = "inbox_01arz3ndektsv4rrffq69g5fav"
	governanceKnowID  = "know_01arz3ndektsv4rrffq69g5faw"
)

func TestValidateForPromotionUsesDeclarativeContentPack(t *testing.T) {
	cfg := testConfig(t)
	body := []byte("# Valid knowledge\n\nA self-contained conclusion without Inbox footnotes.\n")
	doc := governedDoc(governanceKnowID, "Valid knowledge", body)
	if err := ValidateForPromotion(cfg, doc, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	copyDoc := *doc
	copyDoc.Metadata.GovernanceVersion = "test-governance-0"
	if err := ValidateForPromotion(cfg, &copyDoc, nil, time.Now()); err == nil {
		t.Fatal("old governance version was accepted")
	}
	copyDoc = *doc
	copyDoc.Metadata.Extra = cloneExtra(doc.Metadata.Extra)
	copyDoc.Metadata.Extra["category"] = "undeclared-domain"
	if err := ValidateForPromotion(cfg, &copyDoc, nil, time.Now()); err == nil {
		t.Fatal("undeclared category was accepted")
	}
	copyDoc = *doc
	copyDoc.Body = []byte("# Different\n")
	if err := ValidateForPromotion(cfg, &copyDoc, nil, time.Now()); err == nil {
		t.Fatal("title/H1 mismatch was accepted")
	}
}

func TestContentPackAddsTypeCategoryAndFieldWithoutGoChanges(t *testing.T) {
	cfg := testConfig(t)
	doc := governedDoc(governanceKnowID, "Data only extension", []byte("# Data only extension\n\nValidated by test content-pack data.\n"))
	if err := ValidateStored(cfg, doc, time.Now()); err != nil {
		t.Fatalf("data-declared type/category/field was rejected: %v", err)
	}
	doc.Metadata.Extra["confidence"] = "guessed"
	if err := ValidateStored(cfg, doc, time.Now()); err == nil {
		t.Fatal("data-declared enum rule was not enforced")
	}
}

func TestContentPackIdentityMismatchIsRejected(t *testing.T) {
	cfg := testConfig(t)
	cfg.Template.Version = "1.0.1"
	if _, err := Load(cfg); err == nil {
		t.Fatal("content pack identity mismatch was accepted")
	}
}

func TestContentPackRejectsSymlinkedPolicyAndReferences(t *testing.T) {
	t.Run("policy", func(t *testing.T) {
		cfg := testConfig(t)
		data, err := os.ReadFile(cfg.ContentPackPath())
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "content-pack.json")
		if err := os.WriteFile(outside, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cfg.ContentPackPath()); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, cfg.ContentPackPath()); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(cfg); err == nil {
			t.Fatal("symlinked content pack was accepted")
		}
	})
	t.Run("template reference", func(t *testing.T) {
		cfg := testConfig(t)
		template := filepath.Join(cfg.Root, "templates", "knowledge", "record.md")
		if err := os.Remove(template); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "record.md")
		if err := os.WriteFile(outside, []byte("---\ntype: record\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, template); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(cfg); err == nil {
			t.Fatal("symlinked content template reference was accepted")
		}
	})
}

func TestRelationsUseStableKnowledgeIDs(t *testing.T) {
	cfg := testConfig(t)
	targetID := "know_01arz3ndektsv4rrffq69g5fax"
	target := governedDoc(targetID, "Target", []byte("# Target\n\nTarget body.\n"))
	writeKnowledge(t, cfg, target)
	source := governedDoc(governanceKnowID, "Source", []byte("# Source\n\nSource body.\n"))
	source.Metadata.Extra["related"] = []string{targetID}
	relations, err := ValidateRelations(cfg, source)
	if err != nil || len(relations) != 1 || relations[0].Target.Metadata.ID != targetID {
		t.Fatalf("stable relation failed: %#v %v", relations, err)
	}
	source.Metadata.Extra["related"] = []string{"[[knowledge/record/target--" + targetID + "|Target]]"}
	if _, err := ValidateRelations(cfg, source); err == nil || !strings.Contains(err.Error(), "knowledge id") {
		t.Fatalf("wikilink remained authoritative: %v", err)
	}
}

func TestPromotionRelationsCanResolveProspectiveTargets(t *testing.T) {
	cfg := testConfig(t)
	leftID := governanceKnowID
	rightID := "know_01arz3ndektsv4rrffq69g5fax"
	left := governedDoc(leftID, "Left", []byte("# Left\n\nLeft.\n"))
	right := governedDoc(rightID, "Right", []byte("# Right\n\nRight.\n"))
	left.Metadata.Extra["related"] = []string{rightID}
	prospective := map[string]*document.Document{leftID: left, rightID: right}
	if err := ValidateForPromotion(cfg, left, prospective, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleWarningsUseDeclaredFieldsAndValues(t *testing.T) {
	cfg := testConfig(t)
	meta := governedDoc(governanceKnowID, "Fact", []byte("# Fact\n")).Metadata
	meta.Extra["state"] = "contested"
	meta.Extra["review_on"] = "2026-01-01"
	assessment, err := AssessStoredLifecycle(cfg, meta, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	if err != nil || assessment.Field != "state" || assessment.Lifecycle != "contested" || !assessment.Disputed || !assessment.ReviewDue || len(assessment.Warnings) != 2 {
		t.Fatalf("unexpected assessment %#v %v", assessment, err)
	}
}

func testConfig(t *testing.T) *config.Instance {
	t.Helper()
	root := t.TempDir()
	cfg := config.DefaultInstance("governance", "wiki_01arz3ndektsv4rrffq69g5faz", time.Now())
	cfg.Root = root
	cfg.Template.Name = "test-pack"
	cfg.Template.Version = "1.0.0"
	for _, dir := range []string{cfg.KnowledgeDir(), filepath.Join(root, "templates", "knowledge"), filepath.Join(root, "workflows")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "knowledge", "record.md"), []byte("---\ntype: record\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "query.md"), []byte("# Query\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := Policy{
		SchemaVersion: PolicySchemaVersion, Name: "test-pack", Version: "1.0.0", GovernanceVersion: "test-governance-1",
		Categories: []NamedDefinition{{Name: "experimental-domain", Description: "A category added only in test data"}},
		Types:      []TypeRule{{Name: "record", Description: "A type added only in test data", Template: "templates/knowledge/record.md", Fields: []FieldRule{{Name: "confidence", Kind: "enum", Required: true, Values: []string{"verified", "estimated"}}}}},
		Knowledge: KnowledgeRules{
			Fields: []FieldRule{
				{Name: "category", Kind: "enum", Required: true, ValuesFrom: "categories"},
				{Name: "description", Kind: "string", Required: true},
				{Name: "state", Kind: "enum", Required: true, Values: []string{"current", "contested", "retired"}},
				{Name: "review_on", Kind: "date"},
			},
			Relations: []RelationRule{{Field: "related"}, {Field: "replaces", Reciprocal: "replaced_by"}, {Field: "replaced_by", Reciprocal: "replaces"}},
			Lifecycle: &LifecycleRule{Field: "state", InactiveValues: []string{"retired"}, DisputedValues: []string{"contested"}, ReviewAfterField: "review_on"},
			Quality:   QualityRules{RequireH1Title: true, RejectTemplateVariables: true, RejectPromptComments: true, RequireCompleteFootnotes: true},
		},
		Workflows: []WorkflowDefinition{{Name: "query", Path: "workflows/query.md"}},
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ContentPackPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func governedDoc(id, title string, body []byte) *document.Document {
	return &document.Document{Metadata: document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Type: "record", Title: title, Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: document.HashBytes(body),
		GovernanceVersion: "test-governance-1",
		Lineage:           []document.LineageRef{{InboxID: governanceInboxID, PayloadHash: document.HashBytes([]byte("payload")), Source: "test", CapturedAt: "2026-08-08T00:00:00Z"}},
		Extra: map[string]any{
			"category": "experimental-domain", "description": "Description", "state": "current", "confidence": "verified",
		},
	}, Body: body}
}

func writeKnowledge(t *testing.T, cfg *config.Instance, doc *document.Document) {
	t.Helper()
	dir := filepath.Join(cfg.KnowledgeDir(), doc.Metadata.Type)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, document.Slug(doc.Metadata.Title)+"--"+doc.Metadata.ID+".md")
	if err := document.Write(path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	doc.Path = path
}

func cloneExtra(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
