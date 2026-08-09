package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/publish"
	"llm-wiki/internal/raw"
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

func TestCompileBodySurfacesLifecycleDateWarnings(t *testing.T) {
	doc := &document.Document{Metadata: document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5fav", Title: "Lifecycle dates", ContentHash: document.HashBytes([]byte("body")),
	}}
	body := string(compileBody(doc, true, false, governance.LifecycleAssessment{
		Lifecycle: "current", NotYetValid: true, Expired: true, ReviewDue: true,
	}))
	for _, expected := range []string{"not yet valid", "has expired", "requires review but is not automatically invalid"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("derived body omitted %q warning: %s", expected, body)
		}
	}
}

func TestStatusBecomesStaleWhenLifecycleDateCrossesBoundary(t *testing.T) {
	initialized, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "lifecycle-status", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	now := time.Now()
	added, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: "source.md", Stdin: bytes.NewBufferString("# Source\n\nLifecycle evidence.\n"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(t.TempDir(), "draft.md")
	body := fmt.Sprintf("---\ntype: concept\ntitle: Date-bound knowledge\ndescription: Lifecycle boundary fixture\nlifecycle: current\nvalid_until: %s\n---\n# Date-bound knowledge\n\nLifecycle evidence.[^%s-1]\n\n[^%s-1]: locator: fixture\n", now.Format("2006-01-02"), added[0].ID, added[0].ID)
	if err := os.WriteFile(draft, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: draft, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := publish.Apply(cfg, proposal.Proposal.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(cfg, true, false); err != nil {
		t.Fatal(err)
	}
	status, err := getStatusAt(cfg, now.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if status.Fresh || len(status.Items) != 1 || status.Items[0].Reason != "build fingerprint does not match source knowledge" {
		t.Fatalf("crossing valid_until did not stale the derived view: %#v", status)
	}
}
