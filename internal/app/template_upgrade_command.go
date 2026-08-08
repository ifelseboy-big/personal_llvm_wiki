package app

import (
	"time"

	"github.com/spf13/cobra"

	"llm-wiki/internal/templates"
	"llm-wiki/internal/vault"
)

func newTemplateUpgradeCommand(rt *Runtime) *cobra.Command {
	var planMode, applyMode, keepConflicts bool
	cmd := &cobra.Command{
		Use: "upgrade", Args: cobra.NoArgs,
		Short: "Plan or apply a non-destructive three-way template upgrade",
		RunE: func(_ *cobra.Command, _ []string) error {
			if planMode == applyMode {
				return E("TEMPLATE_UPGRADE_MODE_REQUIRED", "specify exactly one of --plan or --apply", ExitUsage, nil)
			}
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			if planMode {
				plan, err := templates.PlanUpgrade(cfg)
				if err != nil {
					return E("TEMPLATE_UPGRADE_PLAN_FAILED", "cannot plan template upgrade", ExitValidation, err)
				}
				return rt.Success("template.upgrade.plan", ref, plan, nil, nil)
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			var lock *vault.Lock
			if !rt.DryRun {
				lock, err = vault.AcquireWrite(cfg, 5*time.Second)
				if err != nil {
					return E("WIKI_LOCKED", "cannot acquire wiki write lock", ExitLock, err)
				}
				defer lock.Close()
			}
			plan, affected, err := templates.ApplyUpgrade(cfg, keepConflicts, rt.DryRun)
			if err != nil {
				appErr := E("TEMPLATE_UPGRADE_CONFLICT", "cannot apply template upgrade", ExitConflict, err)
				if plan != nil {
					appErr.Details = map[string]any{"plan": plan}
				}
				return appErr
			}
			return rt.Success("template.upgrade.apply", ref, map[string]any{
				"plan": plan, "dry_run": rt.DryRun, "kept_conflicts": keepConflicts,
			}, recoveryWarnings, affected)
		},
	}
	cmd.Flags().BoolVar(&planMode, "plan", false, "preview the three-way upgrade")
	cmd.Flags().BoolVar(&applyMode, "apply", false, "apply safe template changes")
	cmd.Flags().BoolVar(&keepConflicts, "keep-conflicts", false, "preserve user-modified or missing managed files")
	return cmd
}
