package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/raw"
	"llm-wiki/internal/vault"
)

func newRawCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "raw", Short: "Capture and inspect original evidence"}
	cmd.AddCommand(newRawAddCommand(rt), newRawListCommand(rt), newRawShowCommand(rt))
	return cmd
}

func newRawAddCommand(rt *Runtime) *cobra.Command {
	var name, title, typeName, origin string
	var allowSensitive bool
	cmd := &cobra.Command{
		Use:   "add <file|directory|->",
		Short: "Copy new material into raw before any publication",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			added, err := raw.Add(cfg, raw.AddOptions{
				Input: args[0], Name: name, Title: title, Type: typeName,
				Origin: origin, AllowSensitive: allowSensitive, DryRun: rt.DryRun,
				Stdin: rt.Stdin,
			})
			if err != nil {
				exit, code := ExitIO, "RAW_ADD_FAILED"
				if errors.Is(err, vault.ErrLocked) {
					exit, code = ExitLock, "WIKI_LOCKED"
				}
				if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "symbolic link") {
					exit, code = ExitSafety, "RAW_INPUT_REJECTED"
				}
				return E(code, "cannot add raw material", exit, err)
			}
			warnings := recoveryWarnings
			if allowSensitive {
				warnings = append(warnings, "sensitive-file protection was explicitly overridden; review captured paths before publication")
			}
			if !rt.DryRun {
				if _, indexErr := indexstore.Update(cfg, false); indexErr != nil {
					warnings = append(warnings, "raw files were committed but index rebuild failed: "+indexErr.Error())
				}
			}
			files := make([]string, 0, len(added)*2)
			var totalBytes int64
			for _, item := range added {
				totalBytes += item.Bytes
				files = append(files, item.Path)
				if item.AssetPath != "" {
					files = append(files, item.AssetPath)
				}
			}
			return rt.Success("raw.add", ref, map[string]any{
				"items": added, "count": len(added), "total_bytes": totalBytes, "dry_run": rt.DryRun,
			}, warnings, files)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for stdin input")
	cmd.Flags().StringVar(&title, "title", "", "override captured title")
	cmd.Flags().StringVar(&typeName, "type", "note", "raw material type")
	cmd.Flags().StringVar(&origin, "origin", "", "override capture origin; otherwise preserve frontmatter or infer file/stdin")
	cmd.Flags().BoolVar(&allowSensitive, "allow-sensitive", false, "explicitly allow a sensitive file")
	return cmd
}

func newRawListCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "list", Args: cobra.NoArgs, Short: "List captured raw evidence",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			docs, problems := raw.List(cfg)
			items := make([]map[string]any, 0, len(docs))
			for _, doc := range docs {
				rel, _ := filepath.Rel(cfg.Root, doc.Path)
				items = append(items, map[string]any{
					"id": doc.Metadata.ID, "title": doc.Metadata.Title,
					"type": doc.Metadata.Type, "path": filepath.ToSlash(rel),
					"content_hash": doc.Metadata.ContentHash, "captured_at": doc.Metadata.CapturedAt,
				})
			}
			warnings := make([]string, 0, len(problems))
			for _, problem := range problems {
				warnings = append(warnings, problem.Error())
			}
			return rt.Success("raw.list", ref, map[string]any{"items": items, "count": len(items)}, warnings, nil)
		},
	}
}

func newRawShowCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "show <raw-id>", Args: cobra.ExactArgs(1), Short: "Show raw metadata and original text",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			doc, err := raw.Show(cfg, args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("RAW_NOT_FOUND", "raw material not found", ExitNotFound, err)
			}
			if err != nil {
				return E("RAW_READ_FAILED", "cannot read raw material", ExitIO, err)
			}
			rel, _ := filepath.Rel(cfg.Root, doc.Path)
			return rt.Success("raw.show", ref, map[string]any{
				"path": filepath.ToSlash(rel), "metadata": doc.Metadata, "body": string(doc.Body),
			}, nil, nil)
		},
	}
}
