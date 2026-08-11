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

func TestSlugIsReadableAndBounded(t *testing.T) {
	got := Slug(" LLVM 的模块化架构 / IR ")
	if got != "llvm-的模块化架构-ir" {
		t.Fatalf("unexpected slug %q", got)
	}
	if len([]rune(Slug(strings.Repeat("x", 200)))) > 80 {
		t.Fatal("slug exceeded 80 runes")
	}
}

func TestKnowledgeRoundTripV2(t *testing.T) {
	body := []byte("# Stable fact\n\nSelf-contained fact.\n")
	meta := Metadata{
		SchemaVersion: CurrentSchema, ID: testKnowledgeID, Type: "concept", Title: "Stable fact",
		Status: "published", PublishedAt: "2026-08-08T10:00:00Z", UpdatedAt: "2026-08-08T10:00:00Z",
		ContentHash: HashBytes(body), GovernanceVersion: "personal-2.0",
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

func TestInboxValidatesPayloadWithoutRewritingIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "payload"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 1, 2, 3, '\r', '\n'}
	payloadPath := filepath.Join(dir, "payload", "input.bin")
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte("# Preliminary\n")
	meta := Metadata{
		SchemaVersion: CurrentSchema, ID: testInboxID, Title: "Preliminary", Status: "pending", Source: "file",
		CapturedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), ContentHash: HashBytes(body), MediaType: "application/octet-stream",
		OriginalName: "input.bin", Payload: "payload/input.bin", PayloadHash: HashBytes(payload), PayloadBytes: int64(len(payload)),
	}
	path := filepath.Join(dir, "item.md")
	if err := Write(path, meta, body); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate("inbox", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate("inbox", false); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("expected payload drift rejection, got %v", err)
	}
}

func TestInboxPayloadSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "payload"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "payload", "input")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	body := []byte("# Note\n")
	doc := &Document{Path: filepath.Join(dir, "item.md"), Body: body, Metadata: Metadata{
		SchemaVersion: CurrentSchema, ID: testInboxID, Title: "Note", Status: "pending", Source: "file",
		CapturedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), ContentHash: HashBytes(body), MediaType: "text/plain",
		OriginalName: "input", Payload: "payload/input", PayloadHash: HashBytes([]byte("secret")), PayloadBytes: 6,
	}}
	if err := doc.Validate("inbox", false); err == nil {
		t.Fatal("expected payload symlink rejection")
	}
}

func TestCurrentIDPrefixesRejectLegacyIDs(t *testing.T) {
	for prefix, id := range map[string]string{"inbox": testInboxID, "prm": "prm_01arz3ndektsv4rrffq69g5fax", "know": testKnowledgeID, "op": "op_01arz3ndektsv4rrffq69g5fay"} {
		if !ValidID(prefix, id) {
			t.Fatalf("valid %s id rejected", prefix)
		}
	}
	if ValidID("raw", "raw_01arz3ndektsv4rrffq69g5fav") || ValidID("chg", "chg_01arz3ndektsv4rrffq69g5fav") {
		t.Fatal("legacy id prefixes remain accepted")
	}
}

func TestFindByIDRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	body := []byte("# Duplicate\n")
	meta := Metadata{SchemaVersion: CurrentSchema, ID: testKnowledgeID, Type: "concept", Title: "Duplicate", Status: "published",
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
