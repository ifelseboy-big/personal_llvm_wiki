package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstanceRoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Unix(0, 0).UTC())
	cfg.Root = root
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InstanceID != cfg.InstanceID || loaded.Paths.Knowledge != "knowledge" {
		t.Fatalf("unexpected config %#v", loaded)
	}
	found, err := Find(filepath.Join(root, "knowledge", "nested"))
	if err != nil || found != root {
		t.Fatalf("expected upward discovery from nested path: %s %v", found, err)
	}
}

func TestSavePreservesUnknownConfigurationFields(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Unix(0, 0).UTC())
	cfg.Root = root
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("\nfuture_top = \"keep\"\n[index.future_strategy]\nenabled = true\n")...)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Template.Version = "1.0.1"
	if err := Save(loaded); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	for _, expected := range []string{"future_top = 'keep'", "[index.future_strategy]", "enabled = true", "version = '1.0.1'"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("saved configuration lost %q:\n%s", expected, text)
		}
	}
}

func TestLegacyDerivedPathIsIgnoredAndPreserved(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Unix(0, 0).UTC())
	cfg.Root = root
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "[paths]\n"
	if !strings.Contains(string(b), marker) {
		t.Fatalf("saved config omitted paths table:\n%s", b)
	}
	b = []byte(strings.Replace(string(b), marker, marker+"derived = 'llm-wiki'\n", 1))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("legacy paths.derived must remain loadable: %v", err)
	}
	if err := Save(loaded); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "derived = 'llm-wiki'") {
		t.Fatalf("legacy unknown field was not preserved:\n%s", after)
	}
}

func TestRejectEscapingManagedPath(t *testing.T) {
	cfg := DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	cfg.Paths.Inbox = "../outside"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected path escape validation error")
	}
}

func TestRejectOverlappingManagedPaths(t *testing.T) {
	cfg := DefaultInstance("test", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	cfg.Paths.Knowledge = filepath.Join(cfg.Paths.Inbox, "published")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected overlapping path rejection, got %v", err)
	}
}

func TestExplicitAliasWinsOverNearestWiki(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "registry.toml")
	t.Setenv("LLM_WIKI_CONFIG", configPath)
	outerRoot := filepath.Join(t.TempDir(), "outer")
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	outer := DefaultInstance("outer", "wiki_01arz3ndektsv4rrffq69g5fav", time.Now())
	outer.Root = outerRoot
	alias := DefaultInstance("personal", "wiki_01arz3ndektsv4rrffq69g5faw", time.Now())
	alias.Root = aliasRoot
	if err := Save(outer); err != nil {
		t.Fatal(err)
	}
	if err := Save(alias); err != nil {
		t.Fatal(err)
	}
	if err := Register(alias, "personal", true); err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve("personal", filepath.Join(outerRoot, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.InstanceID != alias.InstanceID {
		t.Fatalf("alias resolved to nearest wiki: %#v", resolved)
	}
}
