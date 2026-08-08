package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/publish"
	"llm-wiki/internal/vault"
)

func newPublishCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "publish", Short: "Propose, review, and explicitly apply trusted knowledge changes"}
	cmd.AddCommand(newPublishProposeCommand(rt), newPublishDiffCommand(rt), newPublishApplyCommand(rt), newPublishRejectCommand(rt))
	return cmd
}

func newPublishProposeCommand(rt *Runtime) *cobra.Command {
	var sources []string
	var draftFile string
	cmd := &cobra.Command{
		Use: "propose", Args: cobra.NoArgs,
		Short: "Create an immutable publication proposal without writing knowledge",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := publish.Propose(cfg, publish.ProposeOptions{SourceIDs: sources, DraftPath: draftFile, DryRun: rt.DryRun})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("PUBLISH_PROPOSAL_INVALID", "cannot create publication proposal", ExitValidation, err)
			}
			files := []string{}
			if !rt.DryRun {
				base := cfg.Paths.Runtime + "/changes/" + result.Proposal.ID + "/"
				files = []string{base + "proposal.json", base + "state.json", base + "diff.patch", base + result.Proposal.DraftFile}
			}
			return rt.Success("publish.propose", ref, map[string]any{
				"change_id": result.Proposal.ID, "status": result.State.Status,
				"knowledge_id": result.Proposal.KnowledgeID, "target_path": result.Proposal.TargetPath,
				"diff": result.Diff, "dry_run": rt.DryRun,
			}, recoveryWarnings, files)
		},
	}
	cmd.Flags().StringSliceVar(&sources, "source", nil, "raw source id; may be repeated")
	cmd.Flags().StringVar(&draftFile, "file", "", "knowledge draft markdown file")
	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newPublishDiffCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "diff <change-id>", Args: cobra.ExactArgs(1), Short: "Review a publication proposal",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			diff, err := publish.Diff(cfg, args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("CHANGE_NOT_FOUND", "publication change not found", ExitNotFound, err)
			}
			if err != nil {
				return E("CHANGE_INTEGRITY_FAILED", "cannot read publication change", ExitValidation, err)
			}
			if rt.JSON {
				return rt.Success("publish.diff", ref, map[string]any{"change_id": args[0], "diff": diff}, nil, nil)
			}
			_, err = fmt.Fprint(rt.Stdout, diff)
			return err
		},
	}
}

func newPublishApplyCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "apply <change-id>", Args: cobra.ExactArgs(1), Short: "Explicitly approve and commit a publication proposal",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := publish.Apply(cfg, args[0], rt.DryRun, time.Time{})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				code, exit := "PUBLISH_APPLY_FAILED", ExitValidation
				if strings.Contains(err.Error(), "changed") || strings.Contains(err.Error(), "stale") || strings.Contains(err.Error(), "expected proposed") {
					code, exit = "PUBLISH_BASE_CHANGED", ExitConflict
				}
				return E(code, "cannot apply publication change", exit, err)
			}
			warnings := recoveryWarnings
			files := []string{}
			if !rt.DryRun {
				files = append(files, result.TargetPath)
				if _, indexErr := indexstore.Update(cfg, false); indexErr != nil {
					warnings = append(warnings, "knowledge was committed but index rebuild failed: "+indexErr.Error())
				} else if completeErr := publish.CompleteOperation(cfg, result.OperationID); completeErr != nil {
					warnings = append(warnings, "knowledge and index were committed but transaction finalization failed: "+completeErr.Error())
				}
			}
			return rt.Success("publish.apply", ref, result, warnings, files)
		},
	}
}

func newPublishRejectCommand(rt *Runtime) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use: "reject <change-id>", Args: cobra.ExactArgs(1), Short: "Reject a proposal while preserving its audit trail",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			if rt.DryRun {
				return rt.Success("publish.reject", ref, map[string]any{"change_id": args[0], "status": "rejected", "reason": reason, "dry_run": true}, recoveryWarnings, nil)
			}
			state, err := publish.Reject(cfg, args[0], reason, time.Time{})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("PUBLISH_REJECT_FAILED", "cannot reject publication change", ExitConflict, err)
			}
			return rt.Success("publish.reject", ref, map[string]any{"change_id": args[0], "state": state}, recoveryWarnings,
				[]string{cfg.Paths.Runtime + "/changes/" + args[0] + "/state.json"})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "optional rejection reason")
	return cmd
}
