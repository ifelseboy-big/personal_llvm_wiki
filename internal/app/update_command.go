package app

import (
	"errors"

	"github.com/spf13/cobra"

	"llm-wiki/internal/selfupdate"
)

func newUpdateCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update llm-wiki to the latest public release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := selfupdate.Run(cmd.Context(), selfupdate.Options{
				CurrentVersion: Version,
				DryRun:         rt.DryRun,
			})
			if err != nil {
				switch {
				case errors.Is(err, selfupdate.ErrUnsupported):
					return E("UPDATE_UNSUPPORTED", "self-update is unavailable", ExitUnsupported, err)
				case errors.Is(err, selfupdate.ErrDownload):
					appErr := E("UPDATE_DOWNLOAD_FAILED", "cannot download the public installer", ExitIO, err)
					appErr.Retryable = true
					return appErr
				default:
					return E("UPDATE_FAILED", "cannot update llm-wiki", ExitIO, err)
				}
			}
			files := []string(nil)
			if result.Action == "updated" && !result.DryRun {
				files = []string{result.Path}
			}
			return rt.Success("update", nil, result, nil, files)
		},
	}
}
