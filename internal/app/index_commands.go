package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"llm-wiki/internal/document"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/vault"
)

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
					return rt.Success("index.rebuild", ref, map[string]any{"dry_run": true, "mode": "full"}, recoveryWarnings, nil)
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
	cmd := &cobra.Command{
		Use: "query <question>", Args: cobra.ExactArgs(1),
		Short: "Retrieve published knowledge evidence without generating an answer",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			syncResult, err := indexstore.Update(cfg, rt.DryRun)
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("INDEX_SYNC_FAILED", "cannot synchronize index from file facts", ExitIndex, err)
			}
			if rt.DryRun && (syncResult.FullRebuild || syncResult.Added > 0 || syncResult.Changed > 0 || syncResult.Deleted > 0) {
				err := E("INDEX_SYNC_REQUIRED", "query dry-run found an index that requires synchronization", ExitConflict, nil)
				err.Details = map[string]any{"index_plan": syncResult}
				return err
			}
			if syncResult.FullRebuild || syncResult.Added > 0 || syncResult.Changed > 0 || syncResult.Deleted > 0 {
				recoveryWarnings = append(recoveryWarnings, "search index was synchronized from knowledge files before querying")
			}
			items, err := indexstore.Query(cfg, args[0], limit)
			if errors.Is(err, os.ErrNotExist) {
				return E("INDEX_NOT_FOUND", "index does not exist; run llm-wiki index rebuild", ExitIndex, err)
			}
			if err != nil {
				return E("QUERY_FAILED", "cannot query index", ExitIndex, err)
			}
			return rt.Success("query", ref, map[string]any{
				"query": args[0], "evidence": items, "count": len(items),
				"answer_generated": false,
			}, recoveryWarnings, nil)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 8, "maximum evidence chunks")
	return cmd
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
			if err := doc.Validate("knowledge", cfg.Publish.RequireSources); err != nil {
				return E("KNOWLEDGE_INVALID", "published knowledge failed file validation", ExitValidation, err)
			}
			for _, source := range doc.Metadata.Sources {
				rawDoc, sourceErr := document.FindByID(cfg.RawDir(), source.ID)
				if sourceErr != nil {
					return E("KNOWLEDGE_SOURCE_INVALID", "published knowledge source is missing", ExitValidation, sourceErr)
				}
				actual, sourceErr := rawDoc.ActualContentHash()
				if sourceErr != nil || actual != source.ContentHash || rawDoc.Metadata.ContentHash != actual {
					return E("KNOWLEDGE_SOURCE_INVALID", "published knowledge source changed", ExitValidation, sourceErr)
				}
			}
			rel, _ := filepath.Rel(cfg.Root, doc.Path)
			return rt.Success("show", ref, map[string]any{
				"path": filepath.ToSlash(rel), "metadata": doc.Metadata, "body": string(doc.Body),
			}, nil, nil)
		},
	}
}

func newTraceCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "trace <knowledge-id>", Args: cobra.ExactArgs(1), Short: "Trace published knowledge to raw evidence",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			knowledge, err := document.FindByID(cfg.KnowledgeDir(), args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("KNOWLEDGE_NOT_FOUND", "published knowledge not found", ExitNotFound, err)
			}
			if err != nil {
				return E("TRACE_FAILED", "cannot read published knowledge", ExitIO, err)
			}
			type rawEvidence struct {
				ID           string `json:"id"`
				Path         string `json:"path,omitempty"`
				ExpectedHash string `json:"expected_hash"`
				ActualHash   string `json:"actual_hash,omitempty"`
				Valid        bool   `json:"valid"`
				Missing      bool   `json:"missing"`
			}
			actualKnowledgeHash, actualErr := knowledge.ActualContentHash()
			if actualErr != nil {
				return E("TRACE_FAILED", "cannot hash published knowledge", ExitIO, actualErr)
			}
			knowledgeValid := actualKnowledgeHash == knowledge.Metadata.ContentHash
			items := make([]rawEvidence, 0, len(knowledge.Metadata.Sources))
			allValid := knowledgeValid
			for _, source := range knowledge.Metadata.Sources {
				item := rawEvidence{ID: source.ID, ExpectedHash: source.ContentHash}
				rawDoc, rawErr := document.FindByID(cfg.RawDir(), source.ID)
				if errors.Is(rawErr, os.ErrNotExist) {
					item.Missing = true
					allValid = false
				} else if rawErr != nil {
					return E("TRACE_FAILED", "cannot read raw source", ExitIO, rawErr)
				} else {
					rel, _ := filepath.Rel(cfg.Root, rawDoc.Path)
					item.Path = filepath.ToSlash(rel)
					actual, hashErr := rawDoc.ActualContentHash()
					if hashErr != nil {
						return E("TRACE_FAILED", "cannot hash raw source", ExitIO, hashErr)
					}
					item.ActualHash = actual
					item.Valid = item.ActualHash == item.ExpectedHash && rawDoc.Metadata.ContentHash == actual
					allValid = allValid && item.Valid
				}
				items = append(items, item)
			}
			return rt.Success("trace", ref, map[string]any{
				"knowledge_id": knowledge.Metadata.ID, "knowledge_hash": knowledge.Metadata.ContentHash,
				"knowledge_actual_hash": actualKnowledgeHash, "knowledge_valid": knowledgeValid,
				"sources": items, "valid": allValid,
			}, nil, nil)
		},
	}
}
