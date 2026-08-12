package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/inbox"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/vault"
)

func newInboxCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "inbox", Short: "Capture and manage temporary inbox material"}
	cmd.AddCommand(newInboxAddCommand(rt), newInboxListCommand(rt), newInboxShowCommand(rt), newInboxCleanCommand(rt))
	return cmd
}

func newInboxAddCommand(rt *Runtime) *cobra.Command {
	var name, title, source, noteFile, batchManifest string
	var allowSensitive bool
	cmd := &cobra.Command{
		Use: "add [file|-]", Args: cobra.MaximumNArgs(1),
		Short: "Preserve original input and create a pending inbox item",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			if _, err := governance.Load(cfg); err != nil {
				return E("CONTENT_PACK_INVALID", "cannot load the instance-bound content pack", ExitValidation, err)
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			input := ""
			if len(args) == 1 {
				input = args[0]
			}
			added, err := inbox.Add(cfg, inbox.AddOptions{
				Input: input, Name: name, Title: title, Source: source, NoteFile: noteFile,
				BatchManifest: batchManifest, AllowSensitive: allowSensitive, DryRun: rt.DryRun, Stdin: rt.Stdin,
			})
			if err != nil {
				exit, code := ExitValidation, "INBOX_ADD_FAILED"
				if errors.Is(err, vault.ErrLocked) {
					exit, code = ExitLock, "WIKI_LOCKED"
				} else if errors.Is(err, inbox.ErrInputRejected) {
					exit, code = ExitSafety, "INBOX_INPUT_REJECTED"
				}
				return E(code, "cannot add inbox material", exit, err)
			}
			warnings := recoveryWarnings
			if allowSensitive {
				warnings = append(warnings, "sensitive-file protection was explicitly overridden")
			}
			files := []string{}
			var total int64
			for _, item := range added {
				files = append(files, item.ItemPath, item.PayloadPath)
				total += item.Bytes
			}
			return rt.Success("inbox.add", ref, map[string]any{"items": added, "count": len(added), "total_bytes": total, "dry_run": rt.DryRun}, warnings, files)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "required understandable name for stdin input")
	cmd.Flags().StringVar(&title, "title", "", "preliminary title")
	cmd.Flags().StringVar(&source, "source", "", "input source description")
	cmd.Flags().StringVar(&noteFile, "note-file", "", "preliminary Markdown prepared by the Add Skill")
	cmd.Flags().StringVar(&batchManifest, "batch-manifest", "", "JSON manifest mapping multiple inputs to preliminary notes")
	cmd.Flags().BoolVar(&allowSensitive, "allow-sensitive", false, "explicitly allow a sensitive input file")
	return cmd
}

func newInboxListCommand(rt *Runtime) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use: "list", Args: cobra.NoArgs, Short: "List inbox items without searching their bodies",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			docs, problems := inbox.List(cfg, status)
			active, activeErr := promote.ActiveInboxIDs(cfg)
			if activeErr != nil {
				return E("PROMOTION_READ_FAILED", "cannot inspect active promotions", ExitValidation, activeErr)
			}
			warnings := []string{}
			for _, problem := range problems {
				warnings = append(warnings, problem.Error())
			}
			items := make([]map[string]any, 0, len(docs))
			for _, doc := range docs {
				rel, _ := filepath.Rel(cfg.Root, doc.Path)
				payloadPath := filepath.Join(filepath.Dir(doc.Path), filepath.FromSlash(doc.Metadata.Payload))
				payloadRel, _ := filepath.Rel(cfg.Root, payloadPath)
				itemHash, hashErr := document.HashFile(doc.Path)
				if hashErr != nil {
					warnings = append(warnings, "cannot hash inbox item "+doc.Metadata.ID+": "+hashErr.Error())
				}
				items = append(items, map[string]any{
					"id": doc.Metadata.ID, "title": doc.Metadata.Title, "status": doc.Metadata.Status,
					"path": filepath.ToSlash(rel), "captured_at": doc.Metadata.CapturedAt,
					"item_hash": itemHash, "payload_path": filepath.ToSlash(payloadRel),
					"payload_hash": doc.Metadata.PayloadHash, "active_promotion": active[doc.Metadata.ID],
				})
			}
			return rt.Success("inbox.list", ref, map[string]any{"items": items, "count": len(items), "status": status}, warnings, nil)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by pending or processed")
	return cmd
}

func newInboxShowCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "show <inbox-id>", Args: cobra.ExactArgs(1), Short: "Show inbox metadata, preliminary note, and payload information",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			doc, err := inbox.Show(cfg, args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("INBOX_NOT_FOUND", "inbox item not found", ExitNotFound, err)
			}
			if err != nil {
				return E("INBOX_READ_FAILED", "cannot read inbox item", ExitValidation, err)
			}
			rel, _ := filepath.Rel(cfg.Root, doc.Path)
			payloadPath := filepath.Join(filepath.Dir(doc.Path), filepath.FromSlash(doc.Metadata.Payload))
			payloadRel, relErr := filepath.Rel(cfg.Root, payloadPath)
			if relErr != nil {
				return E("INBOX_READ_FAILED", "cannot resolve inbox payload path", ExitValidation, relErr)
			}
			itemHash, hashErr := document.HashFile(doc.Path)
			if hashErr != nil {
				return E("INBOX_READ_FAILED", "cannot hash inbox item", ExitIO, hashErr)
			}
			return rt.Success("inbox.show", ref, map[string]any{
				"path": filepath.ToSlash(rel), "payload_path": filepath.ToSlash(payloadRel),
				"item_hash": itemHash, "payload_hash": doc.Metadata.PayloadHash,
				"metadata": doc.Metadata, "body": string(doc.Body),
			}, nil, nil)
		},
	}
}

func newInboxCleanCommand(rt *Runtime) *cobra.Command {
	var processed, yes bool
	cmd := &cobra.Command{
		Use: "clean [inbox-id...]", Args: cobra.ArbitraryArgs, Short: "Delete validated processed inbox items",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			if _, err := governance.Load(cfg); err != nil {
				return E("CONTENT_PACK_INVALID", "cannot load the instance-bound content pack", ExitValidation, err)
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := inbox.Clean(cfg, inbox.CleanOptions{
				IDs: args, Processed: processed, Yes: yes, DryRun: rt.DryRun,
				ResolveActiveInboxIDs: func() (map[string]bool, error) { return promote.ActiveInboxIDs(cfg) },
			})
			if err != nil {
				code, exit := "INBOX_CLEAN_REJECTED", ExitValidation
				if errors.Is(err, vault.ErrLocked) {
					code, exit = "WIKI_LOCKED", ExitLock
				}
				return E(code, "cannot clean inbox items", exit, err)
			}
			return rt.Success("inbox.clean", ref, result, recoveryWarnings, result.Paths)
		},
	}
	cmd.Flags().BoolVar(&processed, "processed", false, "select every processed inbox item")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm permanent deletion")
	return cmd
}
