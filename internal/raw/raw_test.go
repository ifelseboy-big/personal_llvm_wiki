package raw

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/vault"
)

func newTestWiki(t *testing.T) *config.Instance {
	t.Helper()
	result, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "raw-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Config
}

func TestSensitiveInputRequiresExplicitOverride(t *testing.T) {
	cfg := newTestWiki(t)
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, AddOptions{Input: path, Now: time.Now()}); err == nil {
		t.Fatal("expected sensitive-file rejection")
	}
	items, err := Add(cfg, AddOptions{Input: path, AllowSensitive: true, Now: time.Now()})
	if err != nil || len(items) != 1 {
		t.Fatalf("explicit override failed: %#v %v", items, err)
	}
}

func TestInputSizeLimit(t *testing.T) {
	cfg := newTestWiki(t)
	cfg.Security.MaxInputBytes = 4
	if _, err := Add(cfg, AddOptions{Input: "-", Name: "large.md", Stdin: bytes.NewBufferString("12345")}); err == nil {
		t.Fatal("expected size-limit rejection")
	}
}

func TestSymlinkInputRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	cfg := newTestWiki(t)
	root := t.TempDir()
	target := filepath.Join(root, "target.md")
	link := filepath.Join(root, "link.md")
	if err := os.WriteFile(target, []byte("# Target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, AddOptions{Input: link}); err == nil {
		t.Fatal("expected symbolic-link rejection")
	}
}

func TestNestedManagedSymlinkCannotRedirectWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	cfg := newTestWiki(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cfg.RawDir(), "2026")); err != nil {
		t.Fatal(err)
	}
	_, err := Add(cfg, AddOptions{
		Input: "-", Name: "escape.md", Stdin: bytes.NewBufferString("# Escape\n"),
		Now: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected nested managed symlink rejection")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("write escaped wiki root: entries=%v err=%v", entries, readErr)
	}
}

func TestBinaryInputUsesSidecarAsCanonicalMetadata(t *testing.T) {
	cfg := newTestWiki(t)
	path := filepath.Join(t.TempDir(), "paper.pdf")
	payload := []byte("%PDF-1.7\nnot-a-real-pdf-fixture\n")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := Add(cfg, AddOptions{Input: path, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AssetPath == "" {
		t.Fatalf("binary sidecar result missing: %#v", items)
	}
	doc, err := Show(cfg, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Asset != "paper.pdf" || doc.Metadata.ContentHash != document.HashBytes(payload) {
		t.Fatalf("invalid sidecar metadata %#v", doc.Metadata)
	}
	if err := doc.Validate("raw", false); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryImportPreflightsEveryFileBeforeWriting(t *testing.T) {
	cfg := newTestWiki(t)
	input := t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "a-valid.md"), []byte("# Valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, ".env"), []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(cfg, AddOptions{Input: input, Now: time.Now()}); err == nil {
		t.Fatal("expected directory preflight to reject sensitive input")
	}
	docs, problems := List(cfg)
	if len(problems) != 0 || len(docs) != 0 {
		t.Fatalf("preflight failure left partial raw entries: docs=%d problems=%v", len(docs), problems)
	}
}

func TestSpecialFileInputRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("special device fixture is not portable to Windows")
	}
	cfg := newTestWiki(t)
	if _, err := Add(cfg, AddOptions{Input: "/dev/null"}); err == nil {
		t.Fatal("expected non-regular input rejection")
	}
}

func TestDryRunDoesNotCreateRawFilesOrWriteLock(t *testing.T) {
	cfg := newTestWiki(t)
	lockPath := filepath.Join(cfg.RuntimeDir(), "locks", "write.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	items, err := Add(cfg, AddOptions{
		Input: "-", Name: "preview.md", Stdin: bytes.NewBufferString("# Preview\n"), DryRun: true,
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("dry-run failed: items=%#v err=%v", items, err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a write lock: %v", err)
	}
	docs, problems := List(cfg)
	if len(docs) != 0 || len(problems) != 0 {
		t.Fatalf("dry-run wrote raw files: docs=%d problems=%v", len(docs), problems)
	}
}
