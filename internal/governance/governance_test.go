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

const testRawID = "raw_01arz3ndektsv4rrffq69g5fav"

func TestAssessLifecycleUsesLocalCalendarDatesAndDSTSafeEndOfDay(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	meta := document.Metadata{ID: "know_01arz3ndektsv4rrffq69g5faw", Extra: map[string]any{
		"lifecycle": "current", "valid_from": "2026-08-09", "valid_until": "2026-08-09",
	}}
	assessment, err := AssessLifecycle(meta, time.Date(2026, 8, 9, 0, 30, 0, 0, shanghai))
	if err != nil {
		t.Fatal(err)
	}
	if assessment.NotYetValid || assessment.Expired {
		t.Fatalf("local calendar day was treated as a UTC instant: %#v", assessment)
	}

	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	meta.Extra["valid_from"] = nil
	meta.Extra["valid_until"] = "2026-03-08"
	assessment, err = AssessLifecycle(meta, time.Date(2026, 3, 8, 23, 30, 0, 0, losAngeles))
	if err != nil || assessment.Expired {
		t.Fatalf("DST transition shortened valid_until: %#v %v", assessment, err)
	}
	assessment, err = AssessLifecycle(meta, time.Date(2026, 3, 9, 0, 1, 0, 0, losAngeles))
	if err != nil || !assessment.Expired {
		t.Fatalf("valid_until did not expire on the next local day: %#v %v", assessment, err)
	}
	meta.Extra["valid_until"] = "2026-03-09T00:00:00Z"
	if _, err := AssessLifecycle(meta, time.Date(2026, 3, 9, 0, 1, 0, 0, losAngeles)); err == nil {
		t.Fatal("RFC3339 lifecycle timestamp was accepted even though the contract requires YYYY-MM-DD")
	}
}

func TestStoredLifecycleIgnoresPreV12AndLegacyCustomProperties(t *testing.T) {
	cfg := config.DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	meta := document.Metadata{ID: "know_01arz3ndektsv4rrffq69g5faw", Extra: map[string]any{
		"lifecycle": []any{"archived"}, "valid_until": "not-a-date",
	}}
	cfg.Template.Version = "1.1.1"
	assessment, err := AssessStoredLifecycle(cfg, meta, time.Now(), false)
	if err != nil || assessment.Lifecycle != "current" || len(assessment.Warnings) != 0 {
		t.Fatalf("pre-1.2 custom lifecycle changed behavior: %#v %v", assessment, err)
	}
	cfg.Template.Version = "1.2.0"
	assessment, err = AssessStoredLifecycle(cfg, meta, time.Now(), true)
	if err != nil || assessment.Lifecycle != "current" || len(assessment.Warnings) != 1 {
		t.Fatalf("upgrade-baselined legacy lifecycle was interpreted: %#v %v", assessment, err)
	}
}

func TestParsedStringListsAndCodeExamplesAreHandledMechanically(t *testing.T) {
	meta, _, err := document.Parse([]byte("---\nrelated: [one, two]\n---\n# Test\n"))
	if err != nil {
		t.Fatal(err)
	}
	items, exists, err := ExtraStringList(meta, "related")
	if err != nil || !exists || len(items) != 2 || items[0] != "one" || items[1] != "two" {
		t.Fatalf("parsed YAML list was lost: %#v %v", items, err)
	}

	doc := &document.Document{
		Metadata: document.Metadata{Sources: []document.SourceRef{{ID: testRawID}}},
		Body:     []byte("# Citation examples\n\nSupported fact.[^" + testRawID + "-1]\n\n`[^fake]`\n\n```md\n[^fake]: locator: code example\n```\n\n[^" + testRawID + "-1]: locator: section 2\n"),
	}
	if err := ValidateCitations(doc, true); err != nil {
		t.Fatalf("code examples were treated as real citations: %v", err)
	}
}

func TestValidateForPublishEnforcesPersonalV12Structure(t *testing.T) {
	cfg := config.DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	cfg.Root = t.TempDir()
	body := []byte("# Valid knowledge\n\nSupported fact.[^" + testRawID + "-1]\n\n```md\n{{example}} llm-wiki:prompt [^fake]\n```\n\n[^" + testRawID + "-1]: locator: section 2\n")
	valid := &document.Document{Metadata: document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5faw", Type: "concept", Title: "Valid knowledge",
		GovernanceVersion: PersonalV12Version,
		Sources:           []document.SourceRef{{ID: testRawID}},
		Extra:             map[string]any{"description": "A complete fixture", "lifecycle": "current"},
	}, Body: body}
	if err := ValidateForPublish(cfg, valid, time.Now()); err != nil {
		t.Fatalf("valid personal 1.2 knowledge was rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*document.Document)
	}{
		{"missing marker", func(doc *document.Document) { doc.Metadata.GovernanceVersion = "" }},
		{"missing description", func(doc *document.Document) { delete(doc.Metadata.Extra, "description") }},
		{"noncanonical lifecycle", func(doc *document.Document) { doc.Metadata.Extra["lifecycle"] = " current " }},
		{"title mismatch", func(doc *document.Document) { doc.Metadata.Title = "Different" }},
		{"metadata variable", func(doc *document.Document) { doc.Metadata.Extra["as_of"] = "{{date:YYYY-MM-DD}}" }},
		{"missing citation", func(doc *document.Document) { doc.Body = []byte("# Valid knowledge\n\nUnsupported.\n") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyDoc := *valid
			copyDoc.Metadata = valid.Metadata
			copyDoc.Metadata.Extra = map[string]any{}
			for key, value := range valid.Metadata.Extra {
				copyDoc.Metadata.Extra[key] = value
			}
			copyDoc.Body = append([]byte(nil), valid.Body...)
			test.mutate(&copyDoc)
			if err := ValidateForPublish(cfg, &copyDoc, time.Now()); err == nil {
				t.Fatal("invalid knowledge passed governance validation")
			}
		})
	}
}

func TestGovernanceModeRequiresMarkerOrExactUpgradeBaseline(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	cfg.Root = root
	if err := os.MkdirAll(cfg.KnowledgeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("# Legacy\n")
	doc := &document.Document{Path: filepath.Join(cfg.KnowledgeDir(), "legacy.md"), Metadata: document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5faw", Type: "concept", Title: "Legacy", Status: "published",
		PublishedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
		ContentHash: document.HashBytes(body), Sources: []document.SourceRef{{ID: testRawID, ContentHash: document.HashBytes([]byte("raw"))}},
	}, Body: body}
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Read(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := GovernanceMode(cfg, doc); err == nil {
		t.Fatal("markerless 1.2 document was accepted without an upgrade baseline")
	}
	b, err := os.ReadFile(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLegacyBaseline(cfg, map[string]string{doc.Metadata.ID: document.HashBytes(b)}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLegacyBaseline(cfg, map[string]string{doc.Metadata.ID: document.HashBytes([]byte("different"))}); err == nil {
		t.Fatal("a different pre-existing governance baseline was overwritten")
	}
	strict, legacy, err := GovernanceMode(cfg, doc)
	if err != nil || strict || !legacy {
		t.Fatalf("exact legacy baseline was not recognized: strict=%v legacy=%v err=%v", strict, legacy, err)
	}
	doc.Metadata.Extra = map[string]any{"description": "tampered metadata"}
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	doc, err = document.Read(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := GovernanceMode(cfg, doc); err == nil {
		t.Fatal("changed legacy metadata still matched the upgrade baseline")
	}
}

func TestCanonicalKnowledgeLinkEscapesTitleSeparators(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	cfg.Root = root
	title := `A | B]`
	body := []byte("# " + title + "\n")
	meta := document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5faw", Type: "concept", Title: title, Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: document.HashBytes(body),
		Sources: []document.SourceRef{{ID: testRawID, ContentHash: document.HashBytes([]byte("raw"))}},
	}
	target := filepath.Join(cfg.KnowledgeDir(), "concept", "escaped--"+meta.ID+".md")
	if err := document.Write(target, meta, body); err != nil {
		t.Fatal(err)
	}
	link, err := CanonicalKnowledgeLink(cfg, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if link == "" || !strings.Contains(link, `\|`) || !strings.Contains(link, `\]`) {
		t.Fatalf("canonical link did not escape title separators: %q", link)
	}
	source := &document.Document{Metadata: document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5fax", Extra: map[string]any{"related": []any{link}},
	}}
	if _, err := ValidateRelations(cfg, source); err != nil {
		t.Fatalf("escaped canonical link could not be resolved: %v", err)
	}
}
