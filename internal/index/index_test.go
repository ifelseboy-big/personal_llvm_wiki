package index

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/publish"
	"llm-wiki/internal/raw"
	"llm-wiki/internal/vault"
)

func TestQueryNormalizationAndRelaxedBigrams(t *testing.T) {
	normalized := normalizeQuery("请问：LLVM 的核心结论是什么？")
	if normalized != "llvm 核心结论" {
		t.Fatalf("unexpected normalized query %q", normalized)
	}
	if got := relaxedQuery(normalized); got != `"llvm" OR "核心" OR "心结" OR "结论"` {
		t.Fatalf("unexpected relaxed query %q", got)
	}
	for _, query := range []string{"的", "是什么", "请问，是否？"} {
		if got := normalizeQuery(query); got != "" {
			t.Fatalf("wrapper-only query %q normalized to %q", query, got)
		}
	}
}

func TestMakeChunksInheritsHeading(t *testing.T) {
	body := []byte("# Inherited heading\n\nfirst line fills the first chunk\nsecond line starts later\nthird line remains under the heading\n")
	chunks := makeChunks("know_01arz3ndektsv4rrffq69g5faw", body, 48, 0)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %#v", chunks)
	}
	for i, chunk := range chunks {
		if chunk.HeadingPath != "Inherited heading" {
			t.Fatalf("chunk %d lost inherited heading: %#v", i, chunk)
		}
	}
}

func TestSearchNeverIndexesOrReturnsRawBody(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw-not-searchable")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "raw-not-searchable", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	if _, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: "private-source.md",
		Stdin: bytes.NewBufferString("# RawOnlyNeedleZXQ\n\nThis body must never enter FTS.\n"),
		Now:   time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	result, err := Search(cfg, "RawOnlyNeedleZXQ", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("raw body leaked into query candidates: %#v", result.Candidates)
	}
	db, err := openDB(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rawChunks int
	if err := db.QueryRow(`SELECT count(*) FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.layer='raw'`).Scan(&rawChunks); err != nil {
		t.Fatal(err)
	}
	if rawChunks != 0 {
		t.Fatalf("raw documents produced %d searchable chunks", rawChunks)
	}
}

func TestSimpleChineseRetrievalRankingAndFallback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "search-wiki")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "search", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	cfg.Index.ChunkMaxChars = 256
	cfg.Index.ChunkOverlapChars = 0

	base := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	llvmID := publishSearchFixture(t, cfg, 0, base,
		"LLVM 核心结论", []string{"模块化编译框架"},
		"# LLVM 核心结论\n\nLLVM 使用稳定 IR 连接前端、优化器和后端，实现组件解耦。\n")
	unrelatedID := publishSearchFixture(t, cfg, 1, base,
		"团队会议记录", nil,
		"# 团队会议记录\n\n核心结论用于记录下周的会议室安排，与编译器无关。\n")
	titleID := publishSearchFixture(t, cfg, 2, base,
		"TitlePriorityZXQ", nil,
		"# TitlePriorityZXQ\n\n普通正文。\n")
	publishSearchFixture(t, cfg, 3, base,
		"正文命中文档", nil,
		"# 正文命中文档\n\nTitlePriorityZXQ 只在正文出现。\n"+strings.Repeat("填充内容用于降低正文密度。\n", 20))

	var longBody strings.Builder
	longBody.WriteString("# 多块文档\n\n")
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&longBody, "ChunkCapZXQ section %02d repeats across chunks with deterministic filler text.\n", i)
	}
	chunkedID := publishSearchFixture(t, cfg, 4, base, "多块文档", nil, longBody.String())
	strictID := publishSearchFixture(t, cfg, 5, base, "火星协议说明", nil, "# 火星协议说明\n\n完整严格命中。\n")
	publishSearchFixture(t, cfg, 6, base, "火星观测", nil, "# 火星观测\n\n只有连续二字片段。\n")
	publishSearchFixture(t, cfg, 7, base, "网络协议", nil, "# 网络协议\n\n只有另一个连续二字片段。\n")

	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	if err := ProbeTokenizer(cfg); err != nil {
		t.Fatalf("simple tokenizer probe failed: %v", err)
	}

	assertFirstKnowledge(t, cfg, "LLVM 的核心结论", llvmID)
	assertFirstKnowledge(t, cfg, "稳定 IR", llvmID)
	assertFirstKnowledge(t, cfg, "核心结论", llvmID)
	assertFirstKnowledge(t, cfg, "模块化编译框架", llvmID)
	assertFirstKnowledge(t, cfg, "TitlePriorityZXQ", titleID)

	for _, query := range []string{"的", "是什么"} {
		if _, err := Search(cfg, query, 8); err != ErrNoSearchTerms {
			t.Fatalf("query %q returned %v, expected ErrNoSearchTerms", query, err)
		}
	}

	chunkMatches, err := SearchCandidates(cfg, "ChunkCapZXQ", 8)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, candidate := range chunkMatches {
		if candidate.KnowledgeID == chunkedID {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("same document returned %d chunks, expected 2: %#v", count, chunkMatches)
	}

	fallback, err := Search(cfg, "火星协议", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback.Candidates) != 3 || fallback.Candidates[0].KnowledgeID != strictID ||
		fallback.Candidates[0].RetrievalMode != "strict" {
		t.Fatalf("strict candidate did not stay ahead of relaxed candidates: %#v", fallback)
	}
	if len(fallback.RetrievalModes) != 2 || fallback.RetrievalModes[0] != "strict" || fallback.RetrievalModes[1] != "relaxed" {
		t.Fatalf("unexpected retrieval modes: %#v", fallback.RetrievalModes)
	}
	for _, candidate := range fallback.Candidates[1:] {
		if candidate.RetrievalMode != "relaxed" {
			t.Fatalf("fallback candidate has mode %q: %#v", candidate.RetrievalMode, fallback.Candidates)
		}
	}

	llvmMatches, err := SearchCandidates(cfg, "LLVM 的核心结论", 8)
	if err != nil {
		t.Fatal(err)
	}
	llvmPosition, unrelatedPosition := -1, -1
	for i, candidate := range llvmMatches {
		if candidate.KnowledgeID == llvmID && llvmPosition < 0 {
			llvmPosition = i
		}
		if candidate.KnowledgeID == unrelatedID && unrelatedPosition < 0 {
			unrelatedPosition = i
		}
	}
	if llvmPosition < 0 || unrelatedPosition < 0 || llvmPosition >= unrelatedPosition {
		t.Fatalf("LLVM result did not rank ahead of unrelated Chinese result: %#v", llvmMatches)
	}

	inactive, err := document.FindByID(cfg.KnowledgeDir(), unrelatedID)
	if err != nil {
		t.Fatal(err)
	}
	inactive.Metadata.Extra["lifecycle"] = "superseded"
	if err := document.Write(inactive.Path, inactive.Metadata, inactive.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	defaultInactive, err := Search(cfg, "团队会议记录", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range defaultInactive.Candidates {
		if candidate.KnowledgeID == unrelatedID {
			t.Fatal("default query returned superseded knowledge")
		}
	}
	withInactive, err := SearchWithOptions(cfg, "团队会议记录", SearchOptions{Limit: 8, IncludeInactive: true})
	if err != nil {
		t.Fatal(err)
	}
	foundInactive := false
	for _, candidate := range withInactive.Candidates {
		if candidate.KnowledgeID == unrelatedID {
			foundInactive = true
		}
	}
	if !foundInactive {
		t.Fatal("include-inactive did not return superseded knowledge")
	}

	legacyRaw, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: "legacy-source.md", Stdin: bytes.NewBufferString("# Legacy source\n"), Now: base.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyBody := []byte("# LegacyCustomLifecycleZXQ\n\nLegacy searchable body.\n")
	legacyMeta := document.Metadata{
		ID: "know_01arz3ndektsv4rrffq69g5fay", Type: "concept", Title: "LegacyCustomLifecycleZXQ", Status: "published",
		PublishedAt: base.Format(time.RFC3339), UpdatedAt: base.Format(time.RFC3339), ContentHash: document.HashBytes(legacyBody),
		Sources:           []document.SourceRef{{ID: legacyRaw[0].ID, ContentHash: legacyRaw[0].ContentHash}},
		GovernanceVersion: "pre-1.2-user-property",
		Extra:             map[string]any{"lifecycle": "retracted"},
	}
	legacyPath := filepath.Join(cfg.KnowledgeDir(), "concept", "legacy-custom--"+legacyMeta.ID+".md")
	if err := document.Write(legacyPath, legacyMeta, legacyBody); err != nil {
		t.Fatal(err)
	}
	cfg.Template.Version = "1.1.1"
	if _, err := Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	preUpgradeMatches, err := Search(cfg, "LegacyCustomLifecycleZXQ", 8)
	if err != nil {
		t.Fatal(err)
	}
	foundPreUpgrade := false
	for _, candidate := range preUpgradeMatches.Candidates {
		foundPreUpgrade = foundPreUpgrade || candidate.KnowledgeID == legacyMeta.ID
	}
	if !foundPreUpgrade {
		t.Fatal("new binary applied personal 1.2 lifecycle filtering before explicit template upgrade")
	}
	cfg.Template.Version = "1.2.0"
	legacyMeta.GovernanceVersion = ""
	if err := document.Write(legacyPath, legacyMeta, legacyBody); err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := governance.WriteLegacyBaseline(cfg, map[string]string{legacyMeta.ID: document.HashBytes(legacyBytes)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	legacyMatches, err := Search(cfg, "LegacyCustomLifecycleZXQ", 8)
	if err != nil {
		t.Fatal(err)
	}
	foundLegacy := false
	for _, candidate := range legacyMatches.Candidates {
		foundLegacy = foundLegacy || candidate.KnowledgeID == legacyMeta.ID
	}
	if !foundLegacy {
		t.Fatal("default query hid upgrade-baselined legacy knowledge because of a pre-1.2 lifecycle property")
	}

	db, err := openDB(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET value='tampered' WHERE key='tokenizer_version'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(cfg, "稳定 IR", 8); !errors.Is(err, ErrStale) {
		t.Fatalf("tokenizer metadata mismatch returned %v, expected ErrStale", err)
	}
	updated, err := Update(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.FullRebuild {
		t.Fatalf("tokenizer metadata mismatch did not force a full rebuild: %#v", updated)
	}
}

func TestSearchDetectsCompleteKnowledgeSnapshotDrift(t *testing.T) {
	newFixture := func(t *testing.T) (*config.Instance, string) {
		t.Helper()
		root := filepath.Join(t.TempDir(), "snapshot-wiki")
		initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "snapshot", Template: "personal"})
		if err != nil {
			t.Fatal(err)
		}
		cfg := initialized.Config
		base := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
		publishSearchFixture(t, cfg, 0, base, "StableNeedleZXQ", nil,
			"# StableNeedleZXQ\n\nStableNeedleZXQ is the expected indexed result.\n")
		otherID := publishSearchFixture(t, cfg, 1, base, "UnrelatedTokenAAAA", nil,
			"# UnrelatedTokenAAAA\n\nUnrelatedTokenAAAA belongs to a different document.\n")
		if _, err := Rebuild(cfg); err != nil {
			t.Fatal(err)
		}
		return cfg, otherID
	}

	t.Run("added matching document before an empty result", func(t *testing.T) {
		cfg, _ := newFixture(t)
		base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
		addedID := publishSearchFixture(t, cfg, 2, base, "NewlyAddedNeedleZXQ", nil,
			"# NewlyAddedNeedleZXQ\n\nNewlyAddedNeedleZXQ must not be hidden by an old index.\n")
		if _, err := Search(cfg, "NewlyAddedNeedleZXQ", 8); !errors.Is(err, ErrStale) {
			t.Fatalf("query against an unindexed new document returned %v, expected ErrStale", err)
		}
		if _, err := Update(cfg, false); err != nil {
			t.Fatal(err)
		}
		result, err := Search(cfg, "NewlyAddedNeedleZXQ", 8)
		if err != nil || len(result.Candidates) == 0 || result.Candidates[0].KnowledgeID != addedID {
			t.Fatalf("updated index did not return the new document: %#v %v", result, err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *config.Instance, string)
	}{
		{
			name: "deleted unselected document",
			mutate: func(t *testing.T, cfg *config.Instance, otherID string) {
				doc, err := document.FindByID(cfg.KnowledgeDir(), otherID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(doc.Path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "renamed unselected document",
			mutate: func(t *testing.T, cfg *config.Instance, otherID string) {
				doc, err := document.FindByID(cfg.KnowledgeDir(), otherID)
				if err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(filepath.Dir(doc.Path), "renamed--"+otherID+".md")
				if err := os.Rename(doc.Path, target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "modified unselected document",
			mutate: func(t *testing.T, cfg *config.Instance, otherID string) {
				doc, err := document.FindByID(cfg.KnowledgeDir(), otherID)
				if err != nil {
					t.Fatal(err)
				}
				doc.Body = append(doc.Body, []byte("\nExternally changed but still unrelated.\n")...)
				doc.Metadata.ContentHash = document.HashBytes(document.NormalizeMarkdownBody(doc.Body))
				if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "same size and mtime modification",
			mutate: func(t *testing.T, cfg *config.Instance, otherID string) {
				doc, err := document.FindByID(cfg.KnowledgeDir(), otherID)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(doc.Path)
				if err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(doc.Path)
				if err != nil {
					t.Fatal(err)
				}
				modified := bytes.ReplaceAll(original, []byte("UnrelatedTokenAAAA"), []byte("UnrelatedTokenBBBB"))
				if len(modified) != len(original) || bytes.Equal(modified, original) {
					t.Fatal("test mutation did not preserve size or content changed unexpectedly")
				}
				if err := os.WriteFile(doc.Path, modified, info.Mode().Perm()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(doc.Path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, otherID := newFixture(t)
			test.mutate(t, cfg, otherID)
			result, err := Search(cfg, "StableNeedleZXQ", 8)
			if !errors.Is(err, ErrStale) {
				t.Fatalf("query returned %#v, %v; expected ErrStale after %s", result, err, test.name)
			}
		})
	}
}

func assertFirstKnowledge(t *testing.T, cfg *config.Instance, query, expectedID string) {
	t.Helper()
	matches, err := SearchCandidates(cfg, query, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 || matches[0].KnowledgeID != expectedID {
		t.Fatalf("query %q first result = %#v, expected %s", query, matches, expectedID)
	}
}

func publishSearchFixture(t *testing.T, cfg *config.Instance, ordinal int, base time.Time, title string, aliases []string, body string) string {
	t.Helper()
	added, err := raw.Add(cfg, raw.AddOptions{
		Input: "-", Name: fmt.Sprintf("source-%02d.md", ordinal),
		Stdin: bytes.NewBufferString(body), Now: base.Add(time.Duration(ordinal) * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(t.TempDir(), fmt.Sprintf("draft-%02d.md", ordinal))
	frontmatter := "---\ntype: concept\ntitle: " + title + "\ndescription: Search fixture\nlifecycle: current\n"
	if len(aliases) > 0 {
		frontmatter += "aliases:\n"
		for _, alias := range aliases {
			frontmatter += "  - " + alias + "\n"
		}
	}
	frontmatter += "---\n"
	body += fmt.Sprintf("\nFixture evidence.[^%s-1]\n\n[^%s-1]: locator: test fixture\n", added[0].ID, added[0].ID)
	if err := os.WriteFile(draft, []byte(frontmatter+body), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal, err := publish.Propose(cfg, publish.ProposeOptions{
		SourceIDs: []string{added[0].ID}, DraftPath: draft,
		Now: base.Add(time.Duration(ordinal)*time.Minute + time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := publish.Apply(cfg, proposal.Proposal.ID, false,
		base.Add(time.Duration(ordinal)*time.Minute+2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	return proposal.Proposal.KnowledgeID
}
