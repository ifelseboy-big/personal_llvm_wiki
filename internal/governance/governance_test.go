package governance

import (
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

func TestValidateForPromotionUsesSelfContainedV2Governance(t *testing.T) {
	cfg := testConfig(t)
	body := []byte("# Valid knowledge\n\nA self-contained conclusion without Inbox footnotes.\n")
	doc := governedDoc(governanceKnowID, "Valid knowledge", body)
	if err := ValidateForPromotion(cfg, doc, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	copyDoc := *doc
	copyDoc.Metadata.GovernanceVersion = "personal-1.3"
	if err := ValidateForPromotion(cfg, &copyDoc, nil, time.Now()); err == nil {
		t.Fatal("old governance version was accepted")
	}
	copyDoc = *doc
	copyDoc.Body = []byte("# Different\n")
	if err := ValidateForPromotion(cfg, &copyDoc, nil, time.Now()); err == nil {
		t.Fatal("title/H1 mismatch was accepted")
	}
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
	source.Metadata.Extra["related"] = []string{"[[knowledge/concept/target--" + targetID + "|Target]]"}
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

func TestLifecycleWarningsRemainStable(t *testing.T) {
	meta := governedDoc(governanceKnowID, "Fact", []byte("# Fact\n")).Metadata
	meta.Extra["lifecycle"] = "disputed"
	meta.Extra["review_after"] = "2026-01-01"
	assessment, err := AssessLifecycle(meta, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	if err != nil || !assessment.Disputed || !assessment.ReviewDue || len(assessment.Warnings) != 2 {
		t.Fatalf("unexpected assessment %#v %v", assessment, err)
	}
}

func testConfig(t *testing.T) *config.Instance {
	t.Helper()
	root := t.TempDir()
	cfg := config.DefaultInstance("governance", "wiki_01arz3ndektsv4rrffq69g5faz", time.Now())
	cfg.Root = root
	if err := os.MkdirAll(cfg.KnowledgeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func governedDoc(id, title string, body []byte) *document.Document {
	return &document.Document{Metadata: document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Type: "concept", Title: title, Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: document.HashBytes(body),
		GovernanceVersion: PersonalGovernanceVersion,
		Lineage:           []document.LineageRef{{InboxID: governanceInboxID, PayloadHash: document.HashBytes([]byte("payload")), Source: "test", CapturedAt: "2026-08-08T00:00:00Z"}},
		Extra:             map[string]any{"description": "Description", "lifecycle": "current"},
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
