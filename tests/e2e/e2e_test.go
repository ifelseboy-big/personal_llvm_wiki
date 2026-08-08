package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	buildlayer "llm-wiki/internal/build"
	"llm-wiki/internal/document"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/publish"
	"llm-wiki/internal/raw"
	"llm-wiki/internal/vault"
)

func TestFileFirstFullWorkflowAndRebuildEquivalence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "personal-wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "personal", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	added, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: "article.md", Stdin: bytes.NewBufferString(
			"# LLVM 架构概览\n\nLLVM 使用稳定的 IR 解耦前端、优化器和后端。\n\n核心结论：稳定 IR 是跨语言复用的关键边界。\n"),
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(t.TempDir(), "draft.md")
	draftBody := []byte("---\ntype: concept\ntitle: LLVM 的模块化架构\ntags: [LLVM, 编译器]\n---\n# LLVM 的模块化架构\n\nLLVM 使用稳定的 IR 解耦前端、优化器和后端。\n\n## 核心结论\n\n稳定 IR 是跨语言复用的关键边界。\n")
	if err := os.WriteFile(draft, draftBody, 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: draft, Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	applyResult, err := publish.Apply(cfg, proposal.Proposal.ID, false, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, applyResult.OperationID); err != nil {
		t.Fatal(err)
	}
	unchangedUpdate, err := indexstore.Update(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if unchangedUpdate.Mode != "incremental" || unchangedUpdate.Changed != 0 || unchangedUpdate.Unchanged != 2 {
		t.Fatalf("unexpected no-op incremental update %#v", unchangedUpdate)
	}
	knowledgeDoc, err := document.FindByID(cfg.KnowledgeDir(), proposal.Proposal.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	updateDraft := filepath.Join(t.TempDir(), "update.md")
	updateBody := []byte("---\nid: " + proposal.Proposal.KnowledgeID + "\ntype: concept\ntitle: LLVM 的模块化架构\n---\n# LLVM 的模块化架构\n\n更新后的正文。\n\n## 核心结论\n\n稳定 IR 是跨语言复用的关键边界。\n")
	if err := os.WriteFile(updateDraft, updateBody, 0o600); err != nil {
		t.Fatal(err)
	}
	updateProposal, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: updateDraft, Now: now.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	knowledgeDoc.Metadata.Tags = append(knowledgeDoc.Metadata.Tags, "独有检索标签")
	if err := document.Write(knowledgeDoc.Path, knowledgeDoc.Metadata, knowledgeDoc.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(cfg, updateProposal.Proposal.ID, false, now.Add(4*time.Hour)); err == nil {
		t.Fatal("frontmatter-only concurrent change did not stale the update proposal")
	}
	metadataUpdate, err := indexstore.Update(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if metadataUpdate.Changed != 1 {
		t.Fatalf("frontmatter-only change was not incrementally indexed: %#v", metadataUpdate)
	}
	tagged, err := indexstore.Query(cfg, "独有检索标签", 8)
	if err != nil || len(tagged) != 1 || tagged[0].KnowledgeID != proposal.Proposal.KnowledgeID {
		t.Fatalf("updated tag is not searchable: %#v %v", tagged, err)
	}
	freshUpdate, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: updateDraft, Now: now.Add(5 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if freshUpdate.Proposal.KnowledgeID != proposal.Proposal.KnowledgeID || freshUpdate.Proposal.TargetPath != proposal.Proposal.TargetPath {
		t.Fatalf("update did not preserve knowledge identity: %#v", freshUpdate.Proposal)
	}
	freshApply, err := publish.Apply(cfg, freshUpdate.Proposal.ID, false, now.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, freshApply.OperationID); err != nil {
		t.Fatal(err)
	}
	before, err := indexstore.Query(cfg, "LLVM 的核心结论", 8)
	if err != nil || len(before) == 0 {
		t.Fatalf("query before rebuild: %v %#v", err, before)
	}
	if before[0].KnowledgeID != proposal.Proposal.KnowledgeID || before[0].Sources[0].ID != added[0].ID {
		t.Fatalf("query lost provenance: %#v", before[0])
	}
	if err := os.Remove(indexstore.DBPath(cfg)); err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := indexstore.Query(cfg, "LLVM 的核心结论", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("query changed after deleting SQLite\nbefore=%#v\nafter=%#v", before, after)
	}

	firstBuild, err := buildlayer.Build(cfg, true, false)
	if err != nil {
		t.Fatal(err)
	}
	derivedPath := filepath.Join(cfg.DerivedDir(), "documents", proposal.Proposal.KnowledgeID+".md")
	firstBytes, err := os.ReadFile(derivedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.DerivedDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Update(cfg, false); err != nil {
		t.Fatalf("missing disposable derived directory blocked incremental indexing: %v", err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatalf("missing disposable derived directory blocked index rebuild: %v", err)
	}
	secondBuild, err := buildlayer.Build(cfg, true, false)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(derivedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("derived document is not byte-reproducible")
	}
	if firstBuild.Manifest.Items[0].Fingerprint != secondBuild.Manifest.Items[0].Fingerprint {
		t.Fatal("derived fingerprint changed after full rebuild")
	}
	noOpBuild, err := buildlayer.Build(cfg, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if noOpBuild.Generated != 0 || noOpBuild.Removed != 0 || len(noOpBuild.Files) != 0 {
		t.Fatalf("unchanged incremental build performed writes: %#v", noOpBuild)
	}
	trace, err := document.FindByID(cfg.KnowledgeDir(), proposal.Proposal.KnowledgeID)
	if err != nil || trace.Metadata.Sources[0].ContentHash != added[0].ContentHash {
		t.Fatalf("published file is not the provenance authority: %#v %v", trace, err)
	}
}

func TestProposalBecomesStaleWhenRawEvidenceChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "stale", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	added, err := raw.Add(cfg, raw.AddOptions{Input: "-", Name: "raw.md", Stdin: bytes.NewBufferString("# Raw\n\nOriginal.\n"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(draft, []byte("# Published\n\nOriginal.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: draft, Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	rawDoc, err := document.FindByID(cfg.RawDir(), added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	rawDoc.Body = []byte("# Raw\n\nChanged.\n")
	rawDoc.Metadata.ContentHash = document.HashBytes(rawDoc.Body)
	if err := document.Write(rawDoc.Path, rawDoc.Metadata, rawDoc.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(cfg, proposal.Proposal.ID, false, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expected stale proposal rejection")
	}
	_, state, err := publish.Load(cfg, proposal.Proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "stale" {
		t.Fatalf("expected stale state, got %s", state.Status)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, filepath.FromSlash(proposal.Proposal.TargetPath))); !os.IsNotExist(err) {
		t.Fatalf("stale proposal wrote trusted knowledge: %v", err)
	}
}
