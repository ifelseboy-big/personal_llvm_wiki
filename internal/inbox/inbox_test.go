package inbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/vault"
)

func TestAddPreservesPayloadAndPreliminaryNote(t *testing.T) {
	cfg := initWiki(t)
	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("---\ntitle: Initial\ncustom: kept\ntags: [capture]\n---\n# Initial\n\nSummary without data loss.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 1, 2, '\r', '\n', 0xff}
	result, err := Add(cfg, AddOptions{Input: "-", Name: "input.bin", NoteFile: note, Source: "user", Stdin: bytes.NewReader(payload), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Status != "pending" || result[0].PayloadHash != document.HashBytes(payload) {
		t.Fatalf("unexpected add result %#v", result)
	}
	stored, err := os.ReadFile(filepath.Join(cfg.Root, filepath.FromSlash(result[0].PayloadPath)))
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("payload changed: %v %x", err, stored)
	}
	doc, err := Show(cfg, result[0].ID)
	if err != nil || (!bytes.Contains(doc.Body, []byte("Summary without data loss")) || doc.Metadata.Extra["custom"] != "kept" || len(doc.Metadata.Tags) != 1) {
		t.Fatalf("preliminary note missing: %v %#v", err, doc)
	}
}

func TestBatchManifestPreflightFailureWritesNothing(t *testing.T) {
	cfg := initWiki(t)
	base := t.TempDir()
	input := filepath.Join(base, "one.txt")
	note := filepath.Join(base, "one.md")
	if err := os.WriteFile(input, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("# One\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := BatchManifest{SchemaVersion: BatchSchemaVersion, Items: []BatchItem{{Input: "one.txt", NoteFile: "one.md"}, {Input: "missing.txt", NoteFile: "one.md"}}}
	data, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(base, "batch.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, AddOptions{BatchManifest: manifestPath, Now: time.Unix(100, 0).UTC()}); err == nil {
		t.Fatal("expected batch preflight failure")
	}
	docs, problems := List(cfg, "")
	if len(docs) != 0 || len(problems) != 0 {
		t.Fatalf("batch failure wrote inbox data: %d %#v", len(docs), problems)
	}
}

func TestBatchManifestRejectsNonCurrentSchemaWithoutWrites(t *testing.T) {
	cfg := initWiki(t)
	base := t.TempDir()
	input := filepath.Join(base, "one.txt")
	note := filepath.Join(base, "one.md")
	if err := os.WriteFile(input, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("# One\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := BatchManifest{
		SchemaVersion: BatchSchemaVersion + 1,
		Items:         []BatchItem{{Input: "one.txt", NoteFile: "one.md"}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(base, "batch.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, AddOptions{BatchManifest: manifestPath, Now: time.Unix(100, 0).UTC()}); err == nil {
		t.Fatal("non-current batch manifest schema was accepted")
	}
	docs, problems := List(cfg, "")
	if len(docs) != 0 || len(problems) != 0 {
		t.Fatalf("rejected batch manifest wrote inbox data: %d %#v", len(docs), problems)
	}
}

func TestDirectoryRequiresBatchManifestAndDryRunIsZeroWrite(t *testing.T) {
	cfg := initWiki(t)
	inputDir := t.TempDir()
	if _, err := Add(cfg, AddOptions{Input: inputDir}); err == nil {
		t.Fatal("directory input bypassed batch manifest")
	}
	result, err := Add(cfg, AddOptions{Input: "-", Name: "note.txt", Stdin: bytes.NewBufferString("payload"), DryRun: true, Now: time.Unix(100, 0).UTC()})
	if err != nil || len(result) != 1 {
		t.Fatalf("dry-run failed: %#v %v", result, err)
	}
	docs, _ := List(cfg, "")
	if len(docs) != 0 {
		t.Fatal("dry-run wrote inbox item")
	}
}

func TestAddRejectsUnsafeAndOversizedInputsWithoutWrites(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *config.Instance, string) string
	}{
		{name: "symlink", prepare: func(t *testing.T, _ *config.Instance, base string) string {
			target := filepath.Join(base, "target.txt")
			if err := os.WriteFile(target, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(base, "link.txt")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "hardlink", prepare: func(t *testing.T, _ *config.Instance, base string) string {
			if runtime.GOOS == "windows" {
				t.Skip("hardlink count validation is Unix-specific")
			}
			target := filepath.Join(base, "target.txt")
			if err := os.WriteFile(target, []byte("payload"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(base, "hard.txt")
			if err := os.Link(target, path); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "sensitive", prepare: func(t *testing.T, _ *config.Instance, base string) string {
			path := filepath.Join(base, ".env")
			if err := os.WriteFile(path, []byte("SECRET=value"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "oversized", prepare: func(t *testing.T, cfg *config.Instance, base string) string {
			cfg.Security.MaxInputBytes = 3
			path := filepath.Join(base, "large.txt")
			if err := os.WriteFile(path, []byte("four"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := initWiki(t)
			path := test.prepare(t, cfg, t.TempDir())
			if _, err := Add(cfg, AddOptions{Input: path, Now: time.Unix(100, 0).UTC()}); err == nil {
				t.Fatalf("%s input was accepted", test.name)
			}
			docs, problems := List(cfg, "")
			if len(docs) != 0 || len(problems) != 0 {
				t.Fatalf("%s rejection wrote inbox data: %#v %#v", test.name, docs, problems)
			}
		})
	}
}

func initWiki(t *testing.T) *config.Instance {
	t.Helper()
	result, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "inbox-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Config
}

func TestCleanExplicitPendingAndProcessedSelection(t *testing.T) {
	cfg := initWiki(t)
	added, err := Add(cfg, AddOptions{Input: "-", Name: "note.txt", Stdin: strings.NewReader("pending"), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	id := added[0].ID
	if r, err := Clean(cfg, CleanOptions{Processed: true, Yes: true}); err != nil || r.Deleted != 0 {
		t.Fatalf("processed selection included pending: %#v %v", r, err)
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{id}}); err == nil {
		t.Fatal("cleanup omitted confirmation")
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{id, "inbox_01arz3ndektsv4rrffq69g5fax"}, Yes: true}); err == nil {
		t.Fatal("missing batch target accepted")
	}
	if _, err := Show(cfg, id); err != nil {
		t.Fatalf("failed cleanup partially deleted note: %v", err)
	}
	preview, err := Clean(cfg, CleanOptions{IDs: []string{id}, DryRun: true})
	if err != nil || preview.Deleted != 0 || len(preview.Paths) != 1 {
		t.Fatalf("bad preview: %#v %v", preview, err)
	}
	if r, err := Clean(cfg, CleanOptions{IDs: []string{id}, Yes: true}); err != nil || r.Deleted != 1 {
		t.Fatalf("explicit pending cleanup failed: %#v %v", r, err)
	}
	if _, err := Show(cfg, id); !os.IsNotExist(err) {
		t.Fatalf("cleaned note remains: %v", err)
	}
}

func TestEditableInboxUsesReadableDateNameAndCurrentSnapshots(t *testing.T) {
	cfg := initWiki(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for n, want := range []string{"2026-08-26-登录功能.md", "2026-08-26-登录功能 (2).md"} {
		added, err := Add(cfg, AddOptions{Input: "-", Name: "source.txt", Title: "登录功能", Stdin: strings.NewReader("待整理原文"), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		item := added[0]
		if item.ItemPath != "inbox/"+want || item.PayloadPath != item.ItemPath {
			t.Fatalf("unexpected path: %#v", item)
		}
		path := filepath.Join(cfg.Root, item.ItemPath)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("content_hash:")) || bytes.Contains(data, []byte("payload_hash:")) || !bytes.Contains(data, []byte("待整理原文")) {
			t.Fatalf("capture not directly editable: %s", data)
		}
		if n != 0 {
			continue
		}
		data = bytes.ReplaceAll(data, []byte("待整理原文"), []byte("手动补充后的原文"))
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		moved := filepath.Join(cfg.InboxDir(), "自定义", "已改名.md")
		if err := os.MkdirAll(filepath.Dir(moved), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		doc, err := Show(cfg, item.ID)
		if err != nil || doc.Path != moved || doc.FileHash == item.ItemHash || doc.PayloadHash != document.HashBytes([]byte("手动补充后的原文")) {
			t.Fatalf("editing/moving broke Inbox: %#v %v", doc, err)
		}
		if err := os.Rename(moved, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAttachmentEditsAndMissingAttachmentsDoNotBlockCleanup(t *testing.T) {
	cfg := initWiki(t)
	added, err := Add(cfg, AddOptions{Input: "-", Name: "source.bin", Stdin: bytes.NewReader([]byte{0, 1, 2}), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	item := added[0]
	payload := filepath.Join(cfg.Root, item.PayloadPath)
	if err := os.WriteFile(payload, []byte("edited attachment"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := Show(cfg, item.ID)
	if err != nil || doc.PayloadHash != document.HashBytes([]byte("edited attachment")) {
		t.Fatalf("edit rejected: %#v %v", doc, err)
	}
	doc.Metadata.Status = "processed"
	doc.Metadata.ProcessedAt = time.Unix(200, 0).UTC().Format(time.RFC3339)
	doc.Metadata.KnowledgeIDs = []string{"know_01arz3ndektsv4rrffq69g5faw"}
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(cfg.InboxDir(), "unrelated.txt")
	if err := os.WriteFile(neighbor, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := Clean(cfg, CleanOptions{Processed: true, Yes: true}); err != nil || r.Deleted != 1 {
		t.Fatalf("clean failed: %#v %v", r, err)
	}
	for _, path := range []string{payload, neighbor} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup removed shared/neighbor material: %v", err)
		}
	}
	second, err := Add(cfg, AddOptions{Input: "-", Name: "other.bin", Stdin: bytes.NewReader([]byte{0}), Now: time.Unix(201, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.Root, second[0].PayloadPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{second[0].ID}, Yes: true}); err != nil {
		t.Fatalf("missing attachment blocked cleanup: %v", err)
	}
}

func TestInboxRejectsDuplicateIdentityAndUnsafeAttachments(t *testing.T) {
	for _, kind := range []string{"duplicate", "escape", "symlink", "hardlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			cfg := initWiki(t)
			added, err := Add(cfg, AddOptions{Input: "-", Name: "source.bin", Stdin: bytes.NewReader([]byte{0, 1}), Now: time.Unix(100, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			item := added[0]
			path := filepath.Join(cfg.Root, item.ItemPath)
			payload := filepath.Join(cfg.Root, item.PayloadPath)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate":
				if err := os.WriteFile(filepath.Join(cfg.InboxDir(), "duplicate.md"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "escape":
				data = bytes.ReplaceAll(data, []byte("payload: attachments/source.bin"), []byte("payload: ../outside.bin"))
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				link := os.Link
				if kind == "symlink" {
					link = os.Symlink
				}
				if err := link(outside, payload); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				cfg.Security.MaxInputBytes = 1
			}
			if _, err := Show(cfg, item.ID); err == nil {
				t.Fatalf("%s accepted", kind)
			}
		})
	}
}

func TestBatchAllowsRawInputsAndResolvesCollisionsBeforeWriting(t *testing.T) {
	cfg := initWiki(t)
	base := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("raw"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(BatchManifest{SchemaVersion: BatchSchemaVersion, Items: []BatchItem{{Input: "a.txt", Title: "同名"}, {Input: "b.txt", Title: "同名"}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(base, "batch.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	opts := AddOptions{BatchManifest: manifest, Now: time.Unix(100, 0).UTC(), DryRun: true}
	preview, err := Add(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(cfg.InboxDir()); err != nil || len(entries) != 0 {
		t.Fatalf("dry run wrote files: %v %v", entries, err)
	}
	opts.DryRun = false
	actual, err := Add(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range actual {
		if actual[i].ItemPath != preview[i].ItemPath {
			t.Fatalf("preview differs: %#v %#v", actual, preview)
		}
	}
	if actual[0].ItemPath == actual[1].ItemPath {
		t.Fatal("duplicate titles overwrote material")
	}
}

func TestCommitCollisionRollsBackOnlyNewFiles(t *testing.T) {
	cfg := initWiki(t)
	existing := filepath.Join(cfg.InboxDir(), "z.md")
	if err := os.WriteFile(existing, []byte("keep existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	items := []prepared{{files: map[string][]byte{"inbox/attachments/a.bin": []byte("new attachment"), "inbox/z.md": []byte("must not overwrite")}}}
	if err := commit(cfg, items, time.Unix(100, 0).UTC()); err == nil {
		t.Fatal("commit overwrote an existing target")
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "keep existing" {
		t.Fatalf("existing material changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.InboxDir(), AttachmentsDir)); !os.IsNotExist(err) {
		t.Fatalf("failed commit left new files/directories: %v", err)
	}
}

func TestCaptureDoesNotRequireRawTextToBeValidFrontmatter(t *testing.T) {
	cfg := initWiki(t)
	for _, raw := range []string{"---\nnot frontmatter", "---\nuser: [unfinished\n---\n# Raw", "---\ncustom_property: source metadata\n---\n# Raw"} {
		added, err := Add(cfg, AddOptions{Input: "-", Name: "raw.md", Stdin: strings.NewReader(raw), Now: time.Unix(100, 0).UTC()})
		if err != nil {
			t.Fatalf("raw capture required a metadata schema: %v", err)
		}
		doc, err := Show(cfg, added[0].ID)
		if err != nil || string(doc.Body) != raw {
			t.Fatalf("raw input was lost or interpreted: %#v %v", doc, err)
		}
	}
}
