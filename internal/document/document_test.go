package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testInboxID     = "inbox_01arz3ndektsv4rrffq69g5fav"
	testKnowledgeID = "know_01arz3ndektsv4rrffq69g5faw"
)

func TestMarkdownHashNormalizesLineEndingsOnly(t *testing.T) {
	a := HashBytes(NormalizeMarkdownBody([]byte("a\r\nb\r\n")))
	b := HashBytes(NormalizeMarkdownBody([]byte("a\nb\n")))
	if a != b {
		t.Fatalf("line ending normalization differs: %s != %s", a, b)
	}
	if a == HashBytes(NormalizeMarkdownBody([]byte("a\nb"))) {
		t.Fatal("final newline must remain hash-significant")
	}
}

func TestKnowledgeAttachmentMarkdownIsNotScannedAsDocument(t *testing.T) {
	root := t.TempDir()
	assetDir := filepath.Join(root, "note--"+testKnowledgeID+".assets", testInboxID)
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "source.md"), []byte("# raw source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, problems := ScanMarkdown(root)
	if len(docs) != 0 || len(problems) != 0 {
		t.Fatalf("attachment was scanned as Knowledge: docs=%d problems=%v", len(docs), problems)
	}
}

func TestSlugIsReadableAndBounded(t *testing.T) {
	got := Slug(" LLVM 的模块化架构 / IR ")
	if got != "llvm-的模块化架构-ir" {
		t.Fatalf("unexpected slug %q", got)
	}
	if len([]rune(Slug(strings.Repeat("x", 200)))) > 80 {
		t.Fatal("slug exceeded 80 runes")
	}
}

func TestKnowledgeRoundTrip(t *testing.T) {
	body := []byte("# Stable fact\n\nSelf-contained fact.\n")
	meta := Metadata{
		SchemaVersion: CurrentSchema, ID: testKnowledgeID, Type: "note", Title: "Stable fact",
		Status: "published", PublishedAt: "2026-08-08T10:00:00Z", UpdatedAt: "2026-08-08T10:00:00Z",
		ContentHash: HashBytes(body), GovernanceVersion: "personal-2.0.0",
		Lineage: []LineageRef{{InboxID: testInboxID, PayloadHash: HashBytes([]byte("payload")), Source: "test", CapturedAt: "2026-08-08T09:00:00Z"}},
		Extra:   map[string]any{"description": "kept", "lifecycle": "current", "future": "round-trip"},
	}
	path := filepath.Join(t.TempDir(), "fact.md")
	if err := Write(path, meta, body); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate("knowledge", true); err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Extra["future"] != "round-trip" || doc.Metadata.Lineage[0].InboxID != testInboxID {
		t.Fatalf("metadata was not preserved: %#v", doc.Metadata)
	}
}

func TestRejectNonCurrentFrontmatterSchema(t *testing.T) {
	body := []byte("# Old\n")
	doc := &Document{Metadata: Metadata{
		SchemaVersion: CurrentSchema + 1, ID: testKnowledgeID, Type: "any-declared-type", Title: "Non-current", Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: HashBytes(body),
	}, Body: body}
	if err := doc.Validate("knowledge", false); err == nil {
		t.Fatal("non-current frontmatter schema was accepted")
	}
}

func TestInboxMetadataDoesNotFreezeEditableBody(t *testing.T) {
	meta := Metadata{SchemaVersion: CurrentSchema, ID: testInboxID, Title: "Note", Status: "pending", Source: "user",
		CapturedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), MediaType: "text/plain", OriginalName: "note.txt", Extra: map[string]any{"custom": "preserved"},
		ContentHash: HashBytes([]byte("original")), PayloadHash: HashBytes([]byte("old attachment")), PayloadBytes: 14}
	path := filepath.Join(t.TempDir(), "note.md")
	if err := Write(path, meta, []byte("original")); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.Body = []byte("edited freely")
	if err := doc.Validate("inbox", true); err != nil {
		t.Fatalf("body editing required a hash update: %v", err)
	}
	if doc.Metadata.Extra["custom"] != "preserved" {
		t.Fatal("user property lost")
	}
	for _, version := range []int{0, CurrentSchema, CurrentSchema + 1} {
		doc.Metadata.SchemaVersion = version
		if err := doc.Validate("inbox", false); err != nil {
			t.Fatalf("Inbox version or stale capture metadata blocked editing: %v", err)
		}
		data, err := Render(doc.Metadata, doc.Body)
		if err != nil {
			t.Fatal(err)
		}
		rendered, body, err := Parse(data)
		if err != nil || rendered.SchemaVersion != version || rendered.ContentHash != meta.ContentHash || rendered.PayloadHash != meta.PayloadHash || rendered.PayloadBytes != meta.PayloadBytes || string(body) != "edited freely" {
			t.Fatalf("render migrated Inbox metadata or lost edits: %#v %v", rendered, err)
		}
	}
}

func TestIDPrefixesRejectUndeclaredPrefixes(t *testing.T) {
	for prefix, id := range map[string]string{"inbox": testInboxID, "prm": "prm_01arz3ndektsv4rrffq69g5fax", "know": testKnowledgeID, "op": "op_01arz3ndektsv4rrffq69g5fay"} {
		if !ValidID(prefix, id) {
			t.Fatalf("valid %s id rejected", prefix)
		}
	}
	if ValidID("raw", "raw_01arz3ndektsv4rrffq69g5fav") || ValidID("chg", "chg_01arz3ndektsv4rrffq69g5fav") {
		t.Fatal("undeclared id prefixes were accepted")
	}
}

func TestFindByIDRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	body := []byte("# Duplicate\n")
	meta := Metadata{SchemaVersion: CurrentSchema, ID: testKnowledgeID, Type: "note", Title: "Duplicate", Status: "published",
		PublishedAt: "2026-08-08T10:00:00Z", UpdatedAt: "2026-08-08T10:00:00Z", ContentHash: HashBytes(body),
		Lineage: []LineageRef{{InboxID: testInboxID, PayloadHash: HashBytes([]byte("x")), Source: "test", CapturedAt: "2026-08-08T09:00:00Z"}}}
	for _, name := range []string{"a.md", "b.md"} {
		if err := Write(filepath.Join(root, name), meta, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := FindByID(root, testKnowledgeID); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}
