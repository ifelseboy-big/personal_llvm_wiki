package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"llm-wiki/internal/inbox"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/vault"
)

type promoteApplyCommandResult struct {
	*promote.ApplyResult
	Index *indexstore.UpdateResult `json:"index,omitempty"`
}

func newPromoteCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "promote", Short: "Freeze, review, and explicitly apply knowledge promotions"}
	cmd.AddCommand(newPromotePlanCommand(rt), newPromoteDiffCommand(rt), newPromoteApplyCommand(rt), newPromoteRejectCommand(rt))
	return cmd
}

func newPromotePlanCommand(rt *Runtime) *cobra.Command {
	var manifest string
	cmd := &cobra.Command{
		Use: "plan", Args: cobra.NoArgs, Short: "Validate and freeze a multi-file promotion",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := promote.PlanPromotion(cfg, promote.PlanOptions{ManifestPath: manifest, DryRun: rt.DryRun})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("PROMOTION_PLAN_INVALID", "cannot create promotion plan", ExitValidation, err)
			}
			files := []string{}
			if !rt.DryRun {
				base := filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "promotions", result.Plan.ID))
				files = append(files, base+"/plan.json", base+"/state.json", base+"/diff.patch")
				for _, target := range result.Plan.Targets {
					files = append(files, base+"/"+target.FrozenFile)
				}
			}
			return rt.Success("promote.plan", ref, map[string]any{
				"promotion_id": result.Plan.ID, "status": result.State.Status, "plan_hash": result.PlanHash,
				"targets": result.Plan.Targets, "inboxes": result.Plan.Inboxes, "diff": result.Diff, "dry_run": rt.DryRun,
			}, recoveryWarnings, files)
		},
	}
	cmd.Flags().StringVar(&manifest, "manifest", "", "promotion manifest JSON file")
	_ = cmd.MarkFlagRequired("manifest")
	return cmd
}

func newPromoteDiffCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "diff <promotion-id>", Args: cobra.ExactArgs(1), Short: "Review the complete frozen promotion diff",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			result, err := promote.Diff(cfg, args[0])
			if errors.Is(err, os.ErrNotExist) {
				return E("PROMOTION_NOT_FOUND", "promotion not found", ExitNotFound, err)
			}
			if err != nil {
				return E("PROMOTION_INTEGRITY_FAILED", "cannot read promotion", ExitValidation, err)
			}
			if rt.JSON {
				return rt.Success("promote.diff", ref, result, nil, nil)
			}
			_, err = fmt.Fprint(rt.Stdout, result.Diff)
			return err
		},
	}
}

func newPromoteApplyCommand(rt *Runtime) *cobra.Command {
	var approve string
	cmd := &cobra.Command{
		Use: "apply <promotion-id>", Args: cobra.ExactArgs(1), Short: "Apply only the approved frozen promotion",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := promote.Apply(cfg, args[0], approve, rt.DryRun, time.Time{})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				code, exit := "PROMOTION_APPLY_FAILED", ExitValidation
				if errors.Is(err, promote.ErrApplyConflict) {
					code, exit = "PROMOTION_STALE", ExitConflict
					var conflictErr *promote.ApplyConflictError
					if errors.As(err, &conflictErr) && conflictErr.Kind == "approval" {
						code = "PROMOTION_APPROVAL_MISMATCH"
					}
				}
				return E(code, "cannot apply promotion", exit, err)
			}
			warnings := recoveryWarnings
			files := []string{}
			commandResult := &promoteApplyCommandResult{ApplyResult: result}
			for _, target := range result.Targets {
				files = append(files, target.TargetPath)
			}
			if !rt.DryRun {
				for _, inboxID := range result.Consumed {
					files = append(files, filepath.ToSlash(filepath.Join(cfg.Paths.Inbox, inboxID, inbox.ItemFile)))
				}
				files = append(files, filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "promotions", result.PromotionID, "state.json")))
				indexResult, indexErr := indexstore.Update(cfg, false)
				if indexErr != nil {
					warnings = append(warnings, "knowledge and inbox state were committed but index update failed: "+indexErr.Error())
				} else {
					commandResult.Index = indexResult
					files = append(files, filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "index.sqlite")))
					if completeErr := promote.CompleteOperation(cfg, result.OperationID); completeErr != nil {
						warnings = append(warnings, "promotion and index were committed but transaction finalization failed: "+completeErr.Error())
					}
				}
			}
			return rt.Success("promote.apply", ref, commandResult, warnings, files)
		},
	}
	cmd.Flags().StringVar(&approve, "approve", "", "exact plan hash returned by promote diff")
	_ = cmd.MarkFlagRequired("approve")
	return cmd
}

func newPromoteRejectCommand(rt *Runtime) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use: "reject <promotion-id>", Args: cobra.ExactArgs(1), Short: "Reject a frozen promotion while preserving it",
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
				_, state, _, loadErr := promote.Load(cfg, args[0])
				if loadErr != nil {
					return E("PROMOTION_REJECT_FAILED", "cannot reject promotion", ExitConflict, loadErr)
				}
				if state.Status != "planned" {
					return E("PROMOTION_REJECT_FAILED", "cannot reject promotion", ExitConflict, fmt.Errorf("promotion is %s, expected planned", state.Status))
				}
				return rt.Success("promote.reject", ref, map[string]any{"promotion_id": args[0], "status": "rejected", "reason": reason, "dry_run": true}, recoveryWarnings, nil)
			}
			state, err := promote.Reject(cfg, args[0], reason, time.Time{})
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("PROMOTION_REJECT_FAILED", "cannot reject promotion", ExitConflict, err)
			}
			statePath := filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "promotions", args[0], "state.json"))
			return rt.Success("promote.reject", ref, map[string]any{"promotion_id": args[0], "state": state}, recoveryWarnings, []string{statePath})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "optional rejection reason")
	return cmd
}
