package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"llm-wiki/internal/document"
	"llm-wiki/internal/inbox"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/vault"
)

func TestNoObsidianFullLifecycleAndRebuildEquivalence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "personal-wiki")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "personal", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	if _, err := os.Stat(filepath.Join(root, ".obsidian")); !os.IsNotExist(err) {
		t.Fatalf("init unexpectedly required .obsidian: %v", err)
	}
	added, err := inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: "source.txt", Source: "user", Stdin: bytes.NewBufferString("Stable IR separates compiler components."), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	draft := filepath.Join(work, "draft.md")
	if err := os.WriteFile(draft, []byte("---\ntype: concept\ntitle: Stable IR\ndescription: Stable compiler boundary\nlifecycle: current\n---\n# Stable IR\n\nStable IR separates compiler frontends, optimizers, and backends.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	manifest := promote.Manifest{SchemaVersion: 1,
		Inboxes: []promote.ManifestInbox{{ID: added[0].ID, PayloadHash: added[0].PayloadHash, ItemHash: added[0].ItemHash, Consume: true}},
		Targets: []promote.ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{added[0].ID}}}}
	data, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(work, "promotion.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	planned, err := promote.PlanPromotion(cfg, promote.PlanOptions{ManifestPath: manifestPath, Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := promote.Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	if err := promote.CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	before, err := indexstore.SearchCandidates(cfg, "compiler frontends backends", 8)
	if err != nil || len(before) == 0 || before[0].KnowledgeID != knowledgeID {
		t.Fatalf("query before clean failed: %#v %v", before, err)
	}
	cleaned, err := inbox.Clean(cfg, inbox.CleanOptions{IDs: []string{added[0].ID}, Yes: true})
	if err != nil || cleaned.Deleted != 1 {
		t.Fatalf("processed clean failed: %#v %v", cleaned, err)
	}
	doc, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil || doc.Metadata.Lineage[0].InboxID != added[0].ID {
		t.Fatalf("Knowledge lost historical lineage: %#v %v", doc, err)
	}
	if err := os.Remove(indexstore.DBPath(cfg)); err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := indexstore.SearchCandidates(cfg, "compiler frontends backends", 8)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("query changed after Inbox clean and index rebuild\nbefore=%#v\nafter=%#v\nerr=%v", before, after, err)
	}
}
