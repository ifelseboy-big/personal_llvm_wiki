package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"llm-wiki/internal/document"
	"llm-wiki/internal/vault"
)

func TestStatusRejectsManifestOutputPathTraversal(t *testing.T) {
	result, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "manifest-security", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Config
	manifest := Manifest{
		SchemaVersion: 2, WikiID: cfg.InstanceID, Compiler: CompilerName, CompilerVersion: CompilerVersion,
		ConfigHash: buildConfigHash(cfg), GeneratedAt: time.Now().Format(time.RFC3339),
		Items: []ManifestItem{{
			KnowledgeID: "know_01arz3ndektsv4rrffq69g5fav", KnowledgeHash: document.HashBytes([]byte("knowledge")),
			KnowledgeFileHash: document.HashBytes([]byte("knowledge file")),
			OutputPath:        "../../outside.md", OutputHash: document.HashBytes([]byte("output")), Fingerprint: document.HashBytes([]byte("fingerprint")),
		}},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DerivedDir(), "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GetStatus(cfg); err == nil {
		t.Fatal("expected manifest traversal rejection")
	}
}
