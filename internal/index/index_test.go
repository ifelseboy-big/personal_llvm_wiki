package index

import (
	"bytes"
	"database/sql"
	"encoding/json"
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
	"llm-wiki/internal/inbox"
	"llm-wiki/internal/sqlite3simple"
	"llm-wiki/internal/vault"
)

func TestIndexContainsOnlyKnowledge(t *testing.T) {
	cfg := indexWiki(t)
	if _, err := inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: "private.txt", Stdin: bytes.NewBufferString("InboxOnlyNeedleZXQ"), Now: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	result, err := Rebuild(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || result.Documents["knowledge"] != 0 {
		t.Fatalf("index exposed non-knowledge layer: %#v", result.Documents)
	}
	db, err := sql.Open(sqlite3simple.DriverName, DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM documents`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("inbox leaked into documents: %d %v", count, err)
	}
	search, err := Search(cfg, "InboxOnlyNeedleZXQ", 8)
	if err != nil || len(search.Candidates) != 0 {
		t.Fatalf("inbox leaked into FTS: %#v %v", search, err)
	}
}

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

func TestSearchUsesStableRankingAndSnapshotValidation(t *testing.T) {
	cfg := indexWiki(t)
	first := writeKnowledge(t, cfg, 1, "LLVM stable IR", "# LLVM stable IR\n\nStableNeedleZXQ enables decoupled compiler components.\n")
	writeKnowledge(t, cfg, 2, "Other", "# Other\n\nUnrelated content.\n")
	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	one, err := Search(cfg, "StableNeedleZXQ", 8)
	if err != nil || len(one.Candidates) == 0 || one.Candidates[0].KnowledgeID != first {
		t.Fatalf("expected knowledge hit: %#v %v", one, err)
	}
	two, err := Search(cfg, "StableNeedleZXQ", 8)
	if err != nil || fmt.Sprint(one.Candidates) != fmt.Sprint(two.Candidates) {
		t.Fatalf("ranking is unstable: %#v %#v %v", one, two, err)
	}
	doc, err := document.FindByID(cfg.KnowledgeDir(), first)
	if err != nil {
		t.Fatal(err)
	}
	doc.Body = append(doc.Body, []byte("changed outside promotion\n")...)
	doc.Metadata.ContentHash = document.HashBytes(doc.Body)
	if err := document.Write(doc.Path, doc.Metadata, doc.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(cfg, "StableNeedleZXQ", 8); !errors.Is(err, ErrStale) {
		t.Fatalf("knowledge drift did not stale index: %v", err)
	}
}

func TestChineseRankingFallbackLifecycleAndMetadataDrift(t *testing.T) {
	cfg := indexWiki(t)
	cfg.Index.ChunkMaxChars = 256
	cfg.Index.ChunkOverlapChars = 0
	base := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	llvmID := writeEvaluationKnowledge(t, cfg, base.Add(time.Millisecond), "LLVM 核心结论", "LLVM 使用稳定 IR 连接前端、优化器和后端，实现组件解耦。", []string{"编译器架构"}, []string{"模块化编译框架"})
	unrelatedID := writeEvaluationKnowledge(t, cfg, base.Add(2*time.Millisecond), "团队会议记录", "核心结论用于记录下周的会议室安排，与编译器无关。", nil, nil)
	titleID := writeEvaluationKnowledge(t, cfg, base.Add(3*time.Millisecond), "TitlePriorityZXQ", "普通正文。", nil, nil)
	writeEvaluationKnowledge(t, cfg, base.Add(4*time.Millisecond), "正文命中文档", "TitlePriorityZXQ 只在正文出现。"+strings.Repeat("填充内容用于降低正文密度。", 20), nil, nil)
	strictID := writeEvaluationKnowledge(t, cfg, base.Add(5*time.Millisecond), "火星协议说明", "完整严格命中。", nil, nil)
	writeEvaluationKnowledge(t, cfg, base.Add(6*time.Millisecond), "火星观测", "只有连续二字片段。", nil, nil)
	writeEvaluationKnowledge(t, cfg, base.Add(7*time.Millisecond), "网络协议", "只有另一个连续二字片段。", nil, nil)
	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	if err := ProbeTokenizer(cfg); err != nil {
		t.Fatalf("simple tokenizer probe failed: %v", err)
	}
	for _, test := range []struct{ query, expected string }{
		{query: "LLVM 的核心结论", expected: llvmID}, {query: "稳定 IR", expected: llvmID},
		{query: "模块化编译框架", expected: llvmID}, {query: "TitlePriorityZXQ", expected: titleID},
	} {
		result, err := Search(cfg, test.query, 8)
		if err != nil || len(result.Candidates) == 0 || result.Candidates[0].KnowledgeID != test.expected {
			t.Fatalf("query %q did not rank %s first: %#v %v", test.query, test.expected, result, err)
		}
	}
	fallback, err := Search(cfg, "火星协议", 3)
	if err != nil || len(fallback.Candidates) != 3 || fallback.Candidates[0].KnowledgeID != strictID || fallback.Candidates[0].RetrievalMode != "strict" {
		t.Fatalf("strict result did not stay ahead of relaxed fallback: %#v %v", fallback, err)
	}
	if fmt.Sprint(fallback.RetrievalModes) != "[strict relaxed]" {
		t.Fatalf("unexpected retrieval modes: %#v", fallback.RetrievalModes)
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
	withoutInactive, err := Search(cfg, "团队会议记录", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range withoutInactive.Candidates {
		if candidate.KnowledgeID == unrelatedID {
			t.Fatal("default query returned superseded knowledge")
		}
	}
	withInactive, err := SearchWithOptions(cfg, "团队会议记录", SearchOptions{Limit: 8, IncludeInactive: true})
	if err != nil {
		t.Fatalf("include-inactive failed: %#v %v", withInactive, err)
	}
	foundInactive := false
	for _, candidate := range withInactive.Candidates {
		foundInactive = foundInactive || candidate.KnowledgeID == unrelatedID
	}
	if !foundInactive {
		t.Fatalf("include-inactive omitted superseded knowledge: %#v", withInactive)
	}
	inactive.Metadata.Extra["lifecycle"] = "current"
	inactive.Metadata.Extra["valid_from"] = "2999-01-01"
	if err := document.Write(inactive.Path, inactive.Metadata, inactive.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(cfg, false); err != nil {
		t.Fatal(err)
	}
	beforeValidity, err := Search(cfg, "团队会议记录", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range beforeValidity.Candidates {
		if candidate.KnowledgeID == unrelatedID {
			t.Fatal("default query returned knowledge before its declared valid_from")
		}
	}
	withInactive, err = SearchWithOptions(cfg, "团队会议记录", SearchOptions{Limit: 8, IncludeInactive: true})
	if err != nil {
		t.Fatal(err)
	}
	foundInactive = false
	for _, candidate := range withInactive.Candidates {
		foundInactive = foundInactive || candidate.KnowledgeID == unrelatedID
	}
	if !foundInactive {
		t.Fatalf("include-inactive omitted not-yet-valid knowledge: %#v", withInactive)
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
	if err != nil || !updated.FullRebuild {
		t.Fatalf("metadata mismatch did not force full rebuild: %#v %v", updated, err)
	}
}

func TestSearchDetectsCompleteKnowledgeSnapshotDrift(t *testing.T) {
	newFixture := func(t *testing.T) (*config.Instance, string) {
		cfg := indexWiki(t)
		base := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
		writeEvaluationKnowledge(t, cfg, base.Add(time.Millisecond), "StableNeedleZXQ", "StableNeedleZXQ is the expected indexed result.", nil, nil)
		otherID := writeEvaluationKnowledge(t, cfg, base.Add(2*time.Millisecond), "UnrelatedTokenAAAA", "UnrelatedTokenAAAA belongs to a different document.", nil, nil)
		if _, err := Rebuild(cfg); err != nil {
			t.Fatal(err)
		}
		return cfg, otherID
	}
	t.Run("added file", func(t *testing.T) {
		cfg, _ := newFixture(t)
		writeEvaluationKnowledge(t, cfg, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC), "NewlyAddedNeedleZXQ", "new", nil, nil)
		if _, err := Search(cfg, "StableNeedleZXQ", 8); !errors.Is(err, ErrStale) {
			t.Fatalf("unindexed addition returned %v", err)
		}
	})
	t.Run("deleted unselected file", func(t *testing.T) {
		cfg, otherID := newFixture(t)
		doc, err := document.FindByID(cfg.KnowledgeDir(), otherID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(doc.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := Search(cfg, "StableNeedleZXQ", 8); !errors.Is(err, ErrStale) {
			t.Fatalf("deleted unselected file returned %v", err)
		}
	})
	t.Run("same size and mtime change", func(t *testing.T) {
		cfg, otherID := newFixture(t)
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
			t.Fatal("mutation did not preserve size")
		}
		if err := os.WriteFile(doc.Path, modified, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(doc.Path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		if _, err := Search(cfg, "StableNeedleZXQ", 8); !errors.Is(err, ErrStale) {
			t.Fatalf("same-size snapshot drift returned %v", err)
		}
	})
}

func TestSearchRejectsContentPackPolicyDrift(t *testing.T) {
	cfg := indexWiki(t)
	writeKnowledgeForTest(t, cfg, 1, "Policy drift", "# Policy drift\n\nPolicyHashNeedle remains searchable.\n")
	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	policy, err := governance.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy.Categories = append(policy.Categories, governance.NamedDefinition{Name: "test-added-domain", Description: "Added only by test data"})
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ContentPackPath(), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(cfg, "PolicyHashNeedle", 8); !errors.Is(err, ErrStale) {
		t.Fatalf("content pack drift returned %v, expected ErrStale", err)
	}
	updated, err := Update(cfg, false)
	if err != nil || !updated.FullRebuild {
		t.Fatalf("content pack drift did not force rebuild: %#v %v", updated, err)
	}
}

func TestIncrementalUpdateDerivesAddChangeDeleteFromKnowledge(t *testing.T) {
	cfg := indexWiki(t)
	first := writeKnowledge(t, cfg, 1, "First", "# First\n\nFirstNeedle.\n")
	if _, err := Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, cfg, 2, "Second", "# Second\n\nSecondNeedle.\n")
	before, err := os.ReadFile(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Update(cfg, true)
	if err != nil || plan.Added != 1 || !plan.DryRun {
		t.Fatalf("bad update plan %#v %v", plan, err)
	}
	after, err := os.ReadFile(DBPath(cfg))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("dry-run changed the index: %v", err)
	}
	doc, _ := document.FindByID(cfg.KnowledgeDir(), first)
	if err := os.Remove(doc.Path); err != nil {
		t.Fatal(err)
	}
	result, err := Update(cfg, false)
	if err != nil || result.Added != 1 || result.Deleted != 1 || result.Documents != 1 {
		t.Fatalf("bad incremental update %#v %v", result, err)
	}
	if _, err := Search(cfg, "SecondNeedle", 8); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildRejectsNonCanonicalKnowledgePath(t *testing.T) {
	cfg := indexWiki(t)
	id := writeKnowledge(t, cfg, 1, "Canonical title", "# Canonical title\n\nCanonical fact.\n")
	doc, err := document.FindByID(cfg.KnowledgeDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(filepath.Dir(doc.Path), "wrong--"+id+".md")
	if err := os.Rename(doc.Path, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(cfg); err == nil {
		t.Fatal("rebuild accepted a non-canonical knowledge path")
	}
}

func indexWiki(t *testing.T) *config.Instance {
	t.Helper()
	result, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "index-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Config
}

func writeKnowledge(t *testing.T, cfg *config.Instance, ordinal int, title, body string) string {
	t.Helper()
	return writeKnowledgeForTest(t, cfg, ordinal, title, body)
}

type fataler interface {
	Helper()
	Fatal(...any)
}

func writeKnowledgeForTest(t fataler, cfg *config.Instance, ordinal int, title, body string) string {
	t.Helper()
	id := fmt.Sprintf("know_01arz3ndektsv4rrffq69g%04x", ordinal)
	if len(id) != len("know_01arz3ndektsv4rrffq69g5fav") || !document.ValidID("know", id) {
		// The fixed ULID alphabet suffix below stays valid for the test range.
		alphabet := "0123456789abcdefghjkmnpqrstvwxyz"
		id = "know_01arz3ndektsv4rrffq69g5fa" + string(alphabet[ordinal%len(alphabet)])
	}
	data := []byte(body)
	governanceVersion, err := governance.Version(cfg)
	if err != nil {
		t.Fatal(err)
	}
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Type: "concept", Title: title, Status: "published",
		PublishedAt: "2026-08-09T00:00:00Z", UpdatedAt: "2026-08-09T00:00:00Z", ContentHash: document.HashBytes(data),
		GovernanceVersion: governanceVersion,
		Lineage:           []document.LineageRef{{InboxID: "inbox_01arz3ndektsv4rrffq69g5fav", PayloadHash: document.HashBytes([]byte("payload")), Source: "test", CapturedAt: "2026-08-08T00:00:00Z"}},
		Extra:             map[string]any{"category": "learning", "description": title + " description", "lifecycle": "current"},
	}
	dir := filepath.Join(cfg.KnowledgeDir(), "concept")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, document.Slug(title)+"--"+id+".md")
	if err := document.Write(path, meta, data); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNormalizeQueryRejectsWrapperOnlyInput(t *testing.T) {
	if normalized := normalizeQuery(strings.Repeat("，。！？", 5)); normalized != "" {
		t.Fatalf("wrapper-only query normalized to %q", normalized)
	}
}
