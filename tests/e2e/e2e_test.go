package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	buildlayer "llm-wiki/internal/build"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
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
	draftBody := []byte(fmt.Sprintf("---\ntype: concept\ntitle: LLVM 的模块化架构\ndescription: LLVM 模块化架构的稳定知识\nlifecycle: current\ntags: [LLVM, 编译器]\n---\n# LLVM 的模块化架构\n\nLLVM 使用稳定的 IR 解耦前端、优化器和后端。[^%s-1]\n\n## 核心结论\n\n稳定 IR 是跨语言复用的关键边界。\n\n[^%s-1]: locator: 原始内容\n", added[0].ID, added[0].ID))
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
	updateBody := []byte(fmt.Sprintf("---\nid: %s\ntype: concept\ntitle: LLVM 的模块化架构\n---\n# LLVM 的模块化架构\n\n更新后的正文。[^%s-1]\n\n## 核心结论\n\n稳定 IR 是跨语言复用的关键边界。\n\n[^%s-1]: locator: 原始内容\n", proposal.Proposal.KnowledgeID, added[0].ID, added[0].ID))
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
	tagged, err := indexstore.SearchCandidates(cfg, "独有检索标签", 8)
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
	before, err := indexstore.SearchCandidates(cfg, "LLVM 的核心结论", 8)
	if err != nil || len(before) == 0 {
		t.Fatalf("query before rebuild: %v %#v", err, before)
	}
	if before[0].KnowledgeID != proposal.Proposal.KnowledgeID || knowledgeDoc.Metadata.Sources[0].ID != added[0].ID {
		t.Fatalf("candidate lookup or published provenance was lost: %#v %#v", before[0], knowledgeDoc.Metadata.Sources)
	}
	if err := os.Remove(indexstore.DBPath(cfg)); err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := indexstore.SearchCandidates(cfg, "LLVM 的核心结论", 8)
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

func TestObsidianPropertiesSurvivePublishCreateAndUpdate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "properties", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	added, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: "source.md", Stdin: bytes.NewBufferString("# Source\n\nProperty evidence.\n"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	relationDraft := filepath.Join(t.TempDir(), "relation-target.md")
	relationBody := []byte(fmt.Sprintf("---\ntype: concept\ntitle: RelatedOnlyZXQ\ndescription: Canonical relation target\nlifecycle: current\n---\n# RelatedOnlyZXQ\n\nRelation target evidence.[^%s-1]\n\n[^%s-1]: locator: property evidence\n", added[0].ID, added[0].ID))
	if err := os.WriteFile(relationDraft, relationBody, 0o600); err != nil {
		t.Fatal(err)
	}
	relationProposal, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: []string{added[0].ID}, DraftPath: relationDraft, Now: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	relationApplied, err := publish.Apply(cfg, relationProposal.Proposal.ID, false, now.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, relationApplied.OperationID); err != nil {
		t.Fatal(err)
	}
	relationLink, err := governance.CanonicalKnowledgeLink(cfg, relationProposal.Proposal.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}

	draft := filepath.Join(t.TempDir(), "create.md")
	createBody := []byte(fmt.Sprintf(`---
type: concept
title: Obsidian properties
tags:
  - metadata
aliases:
  - AliasOnlyZXQ
description: DescriptionOnlyZXQ
lifecycle: current
cssclasses:
  - knowledge-note
related:
  - %q
rating: 5
obsolete: remove-me
status: raw
sources: []
content_hash: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
published_at: invalid
---
# Obsidian properties

Property-backed knowledge.[^%s-1]

[^%s-1]: locator: property evidence
`, relationLink, added[0].ID, added[0].ID))
	if err := os.WriteFile(draft, createBody, 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := publish.Propose(cfg, publish.ProposeOptions{
		SourceIDs: []string{added[0].ID}, DraftPath: draft, Now: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	relationTarget, err := document.FindByID(cfg.KnowledgeDir(), relationProposal.Proposal.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	relationTarget.Metadata.Title = "Relation target changed after proposal"
	if err := document.Write(relationTarget.Path, relationTarget.Metadata, relationTarget.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(cfg, proposal.Proposal.ID, false, now.Add(90*time.Minute)); err == nil {
		t.Fatal("apply accepted a relation target that changed after proposal")
	}
	relationTarget.Metadata.Title = "RelatedOnlyZXQ"
	if err := document.Write(relationTarget.Path, relationTarget.Metadata, relationTarget.Body); err != nil {
		t.Fatal(err)
	}
	applied, err := publish.Apply(cfg, proposal.Proposal.ID, false, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	created, err := document.FindByID(cfg.KnowledgeDir(), proposal.Proposal.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Metadata.Status != "published" || len(created.Metadata.Sources) != 1 || created.Metadata.Sources[0].ID != added[0].ID {
		t.Fatalf("draft overrode system-managed metadata: %#v", created.Metadata)
	}
	if created.Metadata.Extra["description"] != "DescriptionOnlyZXQ" || created.Metadata.Extra["rating"] != 5 {
		t.Fatalf("custom properties were not preserved: %#v", created.Metadata.Extra)
	}
	if got, ok := created.Metadata.Extra["cssclasses"].([]any); !ok || len(got) != 1 || got[0] != "knowledge-note" {
		t.Fatalf("cssclasses property was not preserved: %#v", created.Metadata.Extra["cssclasses"])
	}
	if got, ok := created.Metadata.Extra["related"].([]any); !ok || len(got) != 1 || got[0] != relationLink {
		t.Fatalf("canonical related property was not preserved: %#v", created.Metadata.Extra["related"])
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"AliasOnlyZXQ", "DescriptionOnlyZXQ"} {
		matches, err := indexstore.SearchCandidates(cfg, query, 8)
		if err != nil || len(matches) != 1 || matches[0].KnowledgeID != proposal.Proposal.KnowledgeID {
			t.Fatalf("property %q is not searchable: %#v %v", query, matches, err)
		}
	}
	relatedMatches, err := indexstore.SearchCandidates(cfg, "RelatedOnlyZXQ", 8)
	if err != nil {
		t.Fatal(err)
	}
	foundRelatedProperty := false
	for _, match := range relatedMatches {
		if match.KnowledgeID == proposal.Proposal.KnowledgeID {
			foundRelatedProperty = true
		}
	}
	if !foundRelatedProperty {
		t.Fatalf("canonical related property is not searchable: %#v", relatedMatches)
	}

	update := filepath.Join(t.TempDir(), "update.md")
	updateBody := []byte("---\n" +
		"id: " + proposal.Proposal.KnowledgeID + "\n" +
		"aliases: []\n" +
		"description: DescriptionUpdatedZXQ\n" +
		"obsolete: null\n" +
		"status: raw\n" +
		"sources: []\n" +
		"---\n" +
		"# Obsidian properties\n\nUpdated property-backed knowledge.[^" + added[0].ID + "-1]\n\n" +
		"[^" + added[0].ID + "-1]: locator: property evidence\n")
	if err := os.WriteFile(update, updateBody, 0o600); err != nil {
		t.Fatal(err)
	}
	updateProposal, err := publish.Propose(cfg, publish.ProposeOptions{
		SourceIDs: []string{added[0].ID}, DraftPath: update, Now: now.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedResult, err := publish.Apply(cfg, updateProposal.Proposal.ID, false, now.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, updatedResult.OperationID); err != nil {
		t.Fatal(err)
	}
	updated, err := document.FindByID(cfg.KnowledgeDir(), proposal.Proposal.KnowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Metadata.Type != "concept" || updated.Metadata.Title != "Obsidian properties" ||
		!reflect.DeepEqual(updated.Metadata.Tags, []string{"metadata"}) || len(updated.Metadata.Aliases) != 0 {
		t.Fatalf("omitted tags were not preserved or aliases were not cleared: %#v", updated.Metadata)
	}
	if updated.Metadata.Extra["description"] != "DescriptionUpdatedZXQ" || updated.Metadata.Extra["obsolete"] != nil {
		t.Fatalf("custom property update/delete semantics failed: %#v", updated.Metadata.Extra)
	}
	if _, ok := updated.Metadata.Extra["cssclasses"]; !ok {
		t.Fatalf("omitted custom property was not preserved: %#v", updated.Metadata.Extra)
	}
	if err := updated.Validate("knowledge", true); err != nil {
		t.Fatalf("updated knowledge is invalid: %v", err)
	}
	if _, err := indexstore.Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	updatedMatches, err := indexstore.SearchCandidates(cfg, "DescriptionUpdatedZXQ", 8)
	if err != nil || len(updatedMatches) != 1 {
		t.Fatalf("updated property is not searchable: %#v %v", updatedMatches, err)
	}
	oldMatches, err := indexstore.SearchCandidates(cfg, "DescriptionOnlyZXQ", 8)
	if err != nil || len(oldMatches) != 0 {
		t.Fatalf("deleted property value remains searchable: %#v %v", oldMatches, err)
	}
	if _, err := buildlayer.Build(cfg, false, false); err != nil {
		t.Fatal(err)
	}
	derivedID, err := document.DerivedID(updated.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := document.FindByID(cfg.DerivedDir(), derivedID)
	if err != nil || derived.Metadata.Extra["description"] != "DescriptionUpdatedZXQ" {
		t.Fatalf("derived document lost custom properties: %#v %v", derived, err)
	}
	updated.Metadata.Extra["description"] = "MetadataDriftZXQ"
	if err := document.Write(updated.Path, updated.Metadata, updated.Body); err != nil {
		t.Fatal(err)
	}
	buildStatus, err := buildlayer.GetStatus(cfg)
	foundMetadataDrift := false
	if err == nil {
		for _, item := range buildStatus.Items {
			if item.KnowledgeID == updated.Metadata.ID && item.Reason == "source knowledge metadata changed" {
				foundMetadataDrift = true
			}
		}
	}
	if err != nil || buildStatus.Fresh || !foundMetadataDrift {
		t.Fatalf("metadata-only knowledge change was not detected: %#v %v", buildStatus, err)
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
	staleBody := []byte(fmt.Sprintf("---\ntype: concept\ntitle: Published\ndescription: Stale proposal fixture\nlifecycle: current\n---\n# Published\n\nOriginal.[^%s-1]\n\n[^%s-1]: locator: raw original\n", added[0].ID, added[0].ID))
	if err := os.WriteFile(draft, staleBody, 0o600); err != nil {
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
