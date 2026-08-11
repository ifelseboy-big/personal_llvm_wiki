package inbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/vault"
)

func TestAddPreservesPayloadAndPreliminaryNote(t *testing.T) {
	cfg := initWiki(t)
	note := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(note, []byte("# Initial\n\nSummary without data loss.\n"), 0o600); err != nil {
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
	if err != nil || !bytes.Contains(doc.Body, []byte("Summary without data loss")) {
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
	manifest := BatchManifest{SchemaVersion: 1, Items: []BatchItem{{Input: "one.txt", NoteFile: "one.md"}, {Input: "missing.txt", NoteFile: "one.md"}}}
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

func TestCleanRequiresProcessedAndNoActivePromotion(t *testing.T) {
	cfg := initWiki(t)
	added, err := Add(cfg, AddOptions{Input: "-", Name: "note.txt", Stdin: bytes.NewBufferString("payload"), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	id := added[0].ID
	if _, err := Clean(cfg, CleanOptions{IDs: []string{id}, Yes: true}); err == nil {
		t.Fatal("pending inbox was cleaned")
	}
	doc, err := Show(cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	doc.Metadata.Status = "processed"
	doc.Metadata.ProcessedAt = time.Unix(200, 0).UTC().Format(time.RFC3339)
	doc.Metadata.KnowledgeIDs = []string{"know_01arz3ndektsv4rrffq69g5faw"}
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	pending, err := Add(cfg, AddOptions{Input: "-", Name: "pending.txt", Stdin: bytes.NewBufferString("pending"), Now: time.Unix(201, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{id, pending[0].ID}, Yes: true}); err == nil {
		t.Fatal("batch clean accepted a pending inbox")
	}
	if _, err := Show(cfg, id); err != nil {
		t.Fatalf("failed batch clean partially deleted processed inbox: %v", err)
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{id}, Yes: true, ActiveInboxIDs: map[string]bool{id: true}}); err == nil {
		t.Fatal("active promotion reference was ignored")
	}
	preview, err := Clean(cfg, CleanOptions{IDs: []string{id}, DryRun: true})
	if err != nil || preview.Deleted != 0 || len(preview.Paths) != 2 {
		t.Fatalf("bad clean preview %#v %v", preview, err)
	}
	result, err := Clean(cfg, CleanOptions{IDs: []string{id}, Yes: true})
	if err != nil || result.Deleted != 1 {
		t.Fatalf("clean failed %#v %v", result, err)
	}
	if _, err := Show(cfg, id); !os.IsNotExist(err) {
		t.Fatalf("cleaned inbox remains: %v", err)
	}
}

func TestProcessedPayloadDriftWarnsButCleanRejects(t *testing.T) {
	cfg := initWiki(t)
	added, err := Add(cfg, AddOptions{Input: "-", Name: "note.txt", Stdin: bytes.NewBufferString("payload"), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Show(cfg, added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	doc.Metadata.Status = "processed"
	doc.Metadata.ProcessedAt = time.Unix(200, 0).UTC().Format(time.RFC3339)
	doc.Metadata.KnowledgeIDs = []string{"know_01arz3ndektsv4rrffq69g5faw"}
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(filepath.Dir(doc.Path), filepath.FromSlash(doc.Metadata.Payload))
	if err := os.WriteFile(payloadPath, []byte("drifted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(cfg, added[0].ID); err != nil {
		t.Fatalf("processed payload drift made Inbox unreadable: %v", err)
	}
	if warnings := ProcessedPayloadWarnings(cfg); len(warnings) != 1 {
		t.Fatalf("processed drift warning missing: %#v", warnings)
	}
	if _, err := Clean(cfg, CleanOptions{IDs: []string{added[0].ID}, Yes: true}); err == nil {
		t.Fatal("clean accepted drifted processed payload")
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
