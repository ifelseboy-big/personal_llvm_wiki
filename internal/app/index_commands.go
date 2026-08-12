package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/governance"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/vault"
)

var (
	errQueryIndexStale     = errors.New("search index does not match published knowledge")
	errKnowledgeGovernance = errors.New("published knowledge governance is invalid")
)

type queryEvidence struct {
	KnowledgeID   string                         `json:"knowledge_id"`
	Title         string                         `json:"title"`
	Type          string                         `json:"type"`
	Path          string                         `json:"path"`
	ChunkID       string                         `json:"chunk_id"`
	Ordinal       int                            `json:"ordinal"`
	HeadingPath   string                         `json:"heading_path,omitempty"`
	Body          string                         `json:"body"`
	StartLine     int                            `json:"start_line"`
	EndLine       int                            `json:"end_line"`
	Score         float64                        `json:"score"`
	RetrievalMode string                         `json:"retrieval_mode"`
	ContentHash   string                         `json:"content_hash"`
	FileHash      string                         `json:"file_hash"`
	Lineage       []document.LineageRef          `json:"lineage"`
	Metadata      document.Metadata              `json:"metadata"`
	Lifecycle     governance.LifecycleAssessment `json:"lifecycle"`
}

type loadedQueryDocument struct {
	doc      *document.Document
	path     string
	fileHash string
	lines    []string
}

func newIndexCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "index", Short: "Inspect and rebuild the disposable SQLite search index"}
	cmd.AddCommand(&cobra.Command{
		Use: "status", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			status, err := indexstore.GetStatus(cfg)
			if err != nil {
				return E("INDEX_STATUS_FAILED", "cannot inspect index", ExitIndex, err)
			}
			return rt.Success("index.status", ref, status, nil, nil)
		},
	})
	for _, name := range []string{"update", "rebuild"} {
		name := name
		cmd.AddCommand(&cobra.Command{
			Use: name, Args: cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				cfg, ref, err := resolveWiki(rt)
				if err != nil {
					return err
				}
				recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
				if err != nil {
					return err
				}
				if name == "update" {
					result, err := indexstore.Update(cfg, rt.DryRun)
					if err != nil {
						if errors.Is(err, vault.ErrLocked) {
							return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
						}
						return E("INDEX_UPDATE_FAILED", "cannot update index from files", ExitIndex, err)
					}
					files := []string{}
					if !rt.DryRun {
						files = []string{filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "index.sqlite"))}
					}
					return rt.Success("index.update", ref, result, recoveryWarnings, files)
				}
				if rt.DryRun {
					preview, previewErr := indexstore.Update(cfg, true)
					if previewErr != nil {
						return E("INDEX_REBUILD_FAILED", "cannot rebuild index from files", ExitIndex, previewErr)
					}
					return rt.Success("index.rebuild", ref, map[string]any{"dry_run": true, "mode": "full", "validation": preview}, recoveryWarnings, nil)
				}
				result, err := indexstore.Rebuild(cfg)
				if err != nil {
					if errors.Is(err, vault.ErrLocked) {
						return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
					}
					return E("INDEX_REBUILD_FAILED", "cannot rebuild index from files", ExitIndex, err)
				}
				return rt.Success("index.rebuild", ref, map[string]any{"mode": "full", "result": result}, recoveryWarnings, []string{result.Path})
			},
		})
	}
	return cmd
}

func newQueryCommand(rt *Runtime) *cobra.Command {
	var limit int
	var includeInactive bool
	cmd := &cobra.Command{
		Use: "query <question>", Args: cobra.ExactArgs(1),
		Short: "Retrieve published knowledge evidence without generating an answer",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			pending, err := promote.PendingOperations(cfg)
			if err != nil {
				return E("RECOVERY_INSPECTION_FAILED", "cannot inspect interrupted wiki transactions", ExitIO, err)
			}
			if len(pending) > 0 {
				recoveryErr := E("RECOVERY_REQUIRED", "query is read-only and cannot continue while promotion recovery is required", ExitConflict, nil)
				recoveryErr.Details = map[string]any{"operations": pending}
				return recoveryErr
			}
			searchResult, err := indexstore.SearchWithOptions(cfg, args[0], indexstore.SearchOptions{
				Limit: limit, IncludeInactive: includeInactive,
			})
			if errors.Is(err, os.ErrNotExist) {
				return E("INDEX_NOT_FOUND", "index does not exist; run llm-wiki index rebuild", ExitIndex, err)
			}
			if errors.Is(err, indexstore.ErrStale) {
				return E("INDEX_STALE", "index metadata does not match this wiki; run llm-wiki index update", ExitConflict, err)
			}
			if errors.Is(err, indexstore.ErrNoSearchTerms) {
				return E("QUERY_INVALID", "query contains no searchable terms", ExitValidation, err)
			}
			if err != nil {
				return E("QUERY_FAILED", "cannot query index", ExitIndex, err)
			}
			items, lifecycleWarnings, err := hydrateQueryCandidates(cfg, searchResult.Candidates)
			if errors.Is(err, errQueryIndexStale) {
				return E("INDEX_STALE", "index candidates do not match published knowledge; run llm-wiki index update", ExitConflict, err)
			}
			if err != nil {
				if errors.Is(err, errKnowledgeGovernance) {
					return E("KNOWLEDGE_INVALID", "published knowledge has invalid governance metadata", ExitValidation, err)
				}
				return E("KNOWLEDGE_READ_FAILED", "cannot load published knowledge selected by the index", ExitIO, err)
			}
			return rt.Success("query", ref, map[string]any{
				"query": args[0], "normalized_query": searchResult.NormalizedQuery,
				"retrieval_modes": searchResult.RetrievalModes,
				"evidence":        items, "count": len(items),
				"answer_generated": false, "facts_from": "knowledge_markdown",
			}, lifecycleWarnings, nil)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 8, "maximum evidence chunks")
	cmd.Flags().BoolVar(&includeInactive, "include-inactive", false, "include knowledge marked inactive by the content pack for audit")
	return cmd
}

func hydrateQueryCandidates(cfg *config.Instance, candidates []indexstore.Candidate) ([]queryEvidence, []string, error) {
	cache := map[string]*loadedQueryDocument{}
	assessments := map[string]governance.LifecycleAssessment{}
	items := make([]queryEvidence, 0, len(candidates))
	var warnings []string
	for _, candidate := range candidates {
		loaded := cache[candidate.KnowledgeID]
		if loaded == nil {
			var err error
			loaded, err = loadIndexedKnowledge(cfg, candidate)
			if err != nil {
				return nil, nil, err
			}
			cache[candidate.KnowledgeID] = loaded
			if governanceErr := governance.ValidateStored(cfg, loaded.doc, time.Now()); governanceErr != nil {
				return nil, nil, fmt.Errorf("%w: %s: %v", errKnowledgeGovernance, candidate.KnowledgeID, governanceErr)
			}
			assessment, assessmentErr := governance.AssessStoredLifecycle(cfg, loaded.doc.Metadata, time.Now())
			if assessmentErr != nil {
				return nil, nil, fmt.Errorf("%w: %s: %v", errKnowledgeGovernance, candidate.KnowledgeID, assessmentErr)
			}
			assessments[candidate.KnowledgeID] = assessment
			warnings = append(warnings, assessment.Warnings...)
		} else if loaded.path != candidate.Path || loaded.fileHash != candidate.IndexedFileHash ||
			loaded.doc.Metadata.ContentHash != candidate.IndexedContentHash {
			return nil, nil, fmt.Errorf("%w: inconsistent candidates for %s", errQueryIndexStale, candidate.KnowledgeID)
		}
		body, heading, err := candidateExcerpt(loaded.lines, candidate)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, queryEvidence{
			KnowledgeID:   loaded.doc.Metadata.ID,
			Title:         loaded.doc.Metadata.Title,
			Type:          loaded.doc.Metadata.Type,
			Path:          loaded.path,
			ChunkID:       candidate.ChunkID,
			Ordinal:       candidate.Ordinal,
			HeadingPath:   heading,
			Body:          body,
			StartLine:     candidate.StartLine,
			EndLine:       candidate.EndLine,
			Score:         candidate.Score,
			RetrievalMode: candidate.RetrievalMode,
			ContentHash:   loaded.doc.Metadata.ContentHash,
			FileHash:      loaded.fileHash,
			Lineage:       loaded.doc.Metadata.Lineage,
			Metadata:      loaded.doc.Metadata,
			Lifecycle:     assessments[candidate.KnowledgeID],
		})
	}
	return items, governance.SortedWarnings(warnings), nil
}

func loadIndexedKnowledge(cfg *config.Instance, candidate indexstore.Candidate) (*loadedQueryDocument, error) {
	cleanPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate.Path)))
	if cleanPath != candidate.Path {
		return nil, fmt.Errorf("%w: candidate path %q is outside knowledge", errQueryIndexStale, candidate.Path)
	}
	target := filepath.Join(cfg.Root, filepath.FromSlash(cleanPath))
	if err := vault.EnsureInside(cfg.KnowledgeDir(), target); err != nil {
		return nil, fmt.Errorf("%w: %v", errQueryIndexStale, err)
	}
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
		return nil, fmt.Errorf("%w: %v", errQueryIndexStale, err)
	}
	doc, err := document.Read(target)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read %s: %v", errQueryIndexStale, cleanPath, err)
	}
	if err := doc.Validate("knowledge", true); err != nil {
		return nil, fmt.Errorf("%w: %s is invalid: %v", errQueryIndexStale, cleanPath, err)
	}
	if cleanPath != document.KnowledgePath(cfg.Paths.Knowledge, doc.Metadata) {
		return nil, fmt.Errorf("%w: non-canonical knowledge path %s", errQueryIndexStale, cleanPath)
	}
	if doc.Metadata.ID != candidate.KnowledgeID || doc.Metadata.ContentHash != candidate.IndexedContentHash {
		return nil, fmt.Errorf("%w: identity or content hash changed for %s", errQueryIndexStale, cleanPath)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	fileHash := document.HashBytes(b)
	if fileHash != candidate.IndexedFileHash {
		return nil, fmt.Errorf("%w: file hash changed for %s", errQueryIndexStale, cleanPath)
	}
	return &loadedQueryDocument{
		doc: doc, path: cleanPath, fileHash: fileHash,
		lines: strings.Split(string(document.NormalizeMarkdownBody(doc.Body)), "\n"),
	}, nil
}

func candidateExcerpt(lines []string, candidate indexstore.Candidate) (string, string, error) {
	if candidate.StartLine < 1 || candidate.EndLine < candidate.StartLine || candidate.EndLine > len(lines) {
		return "", "", fmt.Errorf("%w: invalid line range for %s", errQueryIndexStale, candidate.ChunkID)
	}
	selected := lines[candidate.StartLine-1 : candidate.EndLine]
	body := strings.TrimSpace(strings.Join(selected, "\n"))
	if document.HashBytes([]byte(body)) != candidate.BodyHash {
		return "", "", fmt.Errorf("%w: chunk locator changed for %s", errQueryIndexStale, candidate.ChunkID)
	}
	heading := ""
	for _, line := range lines[:candidate.EndLine] {
		if value := markdownHeading(line); value != "" {
			heading = value
		}
	}
	return body, heading, nil
}

func markdownHeading(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return ""
	}
	i := 0
	for i < len(trimmed) && trimmed[i] == '#' {
		i++
	}
	if i > 6 || i >= len(trimmed) || trimmed[i] != ' ' {
		return ""
	}
	return strings.TrimSpace(trimmed[i:])
}

func newShowCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "show <knowledge-id>", Args: cobra.ExactArgs(1), Short: "Show a published knowledge file",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			doc, err := document.FindByID(cfg.KnowledgeDir(), args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("KNOWLEDGE_NOT_FOUND", "published knowledge not found", ExitNotFound, err)
			}
			if err != nil {
				return E("KNOWLEDGE_READ_FAILED", "cannot read published knowledge", ExitIO, err)
			}
			if err := doc.Validate("knowledge", true); err != nil {
				return E("KNOWLEDGE_INVALID", "published knowledge failed file validation", ExitValidation, err)
			}
			rel, _ := filepath.Rel(cfg.Root, doc.Path)
			if filepath.ToSlash(rel) != document.KnowledgePath(cfg.Paths.Knowledge, doc.Metadata) {
				return E("KNOWLEDGE_INVALID", "published knowledge path is not canonical", ExitValidation, nil)
			}
			if governanceErr := governance.ValidateStored(cfg, doc, time.Now()); governanceErr != nil {
				return E("KNOWLEDGE_INVALID", "published knowledge failed governance validation", ExitValidation, governanceErr)
			}
			assessment, assessmentErr := governance.AssessStoredLifecycle(cfg, doc.Metadata, time.Now())
			if assessmentErr != nil {
				return E("KNOWLEDGE_INVALID", "published knowledge has invalid lifecycle metadata", ExitValidation, assessmentErr)
			}
			warnings := append([]string(nil), assessment.Warnings...)
			fileHash, hashErr := document.HashFile(doc.Path)
			if hashErr != nil {
				return E("KNOWLEDGE_READ_FAILED", "cannot hash published knowledge", ExitIO, hashErr)
			}
			return rt.Success("show", ref, map[string]any{
				"path": filepath.ToSlash(rel), "metadata": doc.Metadata, "body": string(doc.Body),
				"content_hash": doc.Metadata.ContentHash, "file_hash": fileHash,
				"facts_from": "knowledge_markdown", "lifecycle": assessment,
			}, governance.SortedWarnings(warnings), nil)
		},
	}
}
