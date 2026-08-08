package app

import (
	"errors"

	"github.com/spf13/cobra"

	buildlayer "llm-wiki/internal/build"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/vault"
)

func newBuildCommand(rt *Runtime) *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use: "build", Args: cobra.NoArgs,
		Short: "Deterministically compile the AI-derived layer from published knowledge",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			recoveryWarnings, err := recoverIfNeeded(cfg, rt.DryRun)
			if err != nil {
				return err
			}
			result, err := buildlayer.Build(cfg, full, rt.DryRun)
			if err != nil {
				if errors.Is(err, vault.ErrLocked) {
					return E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
				}
				return E("BUILD_FAILED", "cannot build derived layer from published files", ExitValidation, err)
			}
			warnings := recoveryWarnings
			if !rt.DryRun {
				if _, indexErr := indexstore.Update(cfg, false); indexErr != nil {
					warnings = append(warnings, "derived files were built but index rebuild failed: "+indexErr.Error())
				}
			}
			return rt.Success("build", ref, result, warnings, result.Files)
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "rebuild the complete derived layer")
	status := &cobra.Command{
		Use: "status", Args: cobra.NoArgs, Short: "Check missing, stale, drifted, and orphaned derived files",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			result, err := buildlayer.GetStatus(cfg)
			if err != nil {
				return E("BUILD_STATUS_FAILED", "cannot inspect derived layer", ExitValidation, err)
			}
			return rt.Success("build.status", ref, result, nil, nil)
		},
	}
	cmd.AddCommand(status)
	return cmd
}
