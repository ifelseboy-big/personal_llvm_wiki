package document

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

func TestRenderParseAndValidateKnowledge(t *testing.T) {
	body := []byte("# Trusted fact\n\nEvidence-backed.\n")
	meta := Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5fav", Type: "concept", Title: "Trusted fact",
		Status: "published", PublishedAt: "2026-08-08T10:00:00Z", UpdatedAt: "2026-08-08T10:00:00Z",
		ContentHash: HashBytes(body), Sources: []SourceRef{{ID: "raw_01arz3ndektsv4rrffq69g5fav", ContentHash: HashBytes([]byte("raw"))}},
	}
	b, err := Render(meta, body)
	if err != nil {
		t.Fatal(err)
	}
	parsedMeta, parsedBody, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if parsedMeta.ID != meta.ID || string(parsedBody) != string(body) {
		t.Fatalf("roundtrip mismatch: %#v %q", parsedMeta, parsedBody)
	}
	path := filepath.Join(t.TempDir(), "fact.md")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate("knowledge", true); err != nil {
		t.Fatal(err)
	}
}

func TestSlugIsReadableAndBounded(t *testing.T) {
	got := Slug(" LLVM 的模块化架构 / IR ")
	if got != "llvm-的模块化架构-ir" {
		t.Fatalf("unexpected slug %q", got)
	}
	if len([]rune(Slug(strings.Repeat("x", 200)))) > 80 {
		t.Fatal("slug exceeded 80 characters")
	}
}

func TestFindByIDRejectsDuplicateDocuments(t *testing.T) {
	root := t.TempDir()
	body := []byte("# Duplicate\n")
	meta := Metadata{
		ID: "raw_01arz3ndektsv4rrffq69g5fav", Type: "note", Title: "Duplicate",
		Status: "raw", Origin: "test", CapturedAt: "2026-08-08T10:00:00Z",
		ContentHash: HashBytes(body), MediaType: "text/markdown", OriginalName: "duplicate.md",
	}
	for _, name := range []string{"a.md", "b.md"} {
		if err := Write(filepath.Join(root, name), meta, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := FindByID(root, meta.ID); err == nil || !strings.Contains(err.Error(), "duplicate document id") {
		t.Fatalf("expected duplicate ID rejection, got %v", err)
	}
}

func TestManagedMarkdownHardLinkRejected(t *testing.T) {
	original := filepath.Join(t.TempDir(), "original.md")
	linked := filepath.Join(t.TempDir(), "linked.md")
	if err := os.WriteFile(original, []byte("---\nschema_version: 1\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := Read(linked); err == nil || !strings.Contains(err.Error(), "multiple hard links") {
		t.Fatalf("expected managed hard-link rejection, got %v", err)
	}
}

func TestRawAssetSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.bin")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "asset.bin")); err != nil {
		t.Fatal(err)
	}
	body := []byte("# Source\n")
	meta := Metadata{
		ID: "raw_01arz3ndektsv4rrffq69g5fav", Type: "source", Title: "Source", Status: "raw",
		Origin: "file", CapturedAt: "2026-08-08T10:00:00Z", ContentHash: HashBytes([]byte("external")),
		MediaType: "application/octet-stream", OriginalName: "asset.bin", Asset: "asset.bin",
	}
	sidecar := filepath.Join(root, "asset.source.md")
	if err := Write(sidecar, meta, body); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.ActualContentHash(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected raw asset symlink rejection, got %v", err)
	}
}
