package app

import (
	"bufio"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"llm-wiki/internal/templates"
)

func newTemplateCreateCommand(rt *Runtime) *cobra.Command {
	var kind, title, output string
	var set, related []string
	var overwrite, yes bool
	cmd := &cobra.Command{
		Use: "create <name>", Args: cobra.ExactArgs(1),
		Short: "Create an editable draft from an inbox or knowledge template",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			if output == "" {
				return E("TEMPLATE_OUTPUT_REQUIRED", "--output is required", ExitUsage, nil)
			}
			if _, statErr := os.Lstat(output); statErr == nil {
				if !overwrite {
					return E("TEMPLATE_OUTPUT_EXISTS", "template output exists; pass --overwrite to replace it", ExitConflict, nil)
				}
				if !rt.DryRun && !yes {
					if rt.NoInteractive || !stdinIsTerminal() {
						return E("CONFIRMATION_REQUIRED", "overwriting a template output requires --yes in non-interactive mode", ExitUsage, nil)
					}
					confirmed, promptErr := promptYesNo(bufio.NewReader(rt.Stdin), rt, "Overwrite existing draft "+output, false)
					if promptErr != nil {
						return E("TEMPLATE_PROMPT_FAILED", "cannot read overwrite confirmation", ExitIO, promptErr)
					}
					if !confirmed {
						return E("TEMPLATE_CREATE_CANCELLED", "template creation was cancelled", ExitConflict, nil)
					}
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return E("TEMPLATE_OUTPUT_INSPECTION_FAILED", "cannot inspect template output", ExitIO, statErr)
			}
			result, err := templates.CreateDraft(cfg, templates.CreateOptions{
				Kind: kind, Name: args[0], Title: title, Output: output,
				Set: set, Related: related, Overwrite: overwrite, DryRun: rt.DryRun,
			})
			if errors.Is(err, os.ErrNotExist) {
				return E("TEMPLATE_NOT_FOUND", "content template not found", ExitNotFound, err)
			}
			if err != nil {
				return E("TEMPLATE_CREATE_FAILED", "cannot create template draft", ExitValidation, err)
			}
			files := []string{}
			if !rt.DryRun {
				files = append(files, result.Output)
			}
			return rt.Success("template.create", ref, result, nil, files)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "knowledge", "template kind: inbox or knowledge")
	cmd.Flags().StringVar(&title, "title", "", "draft title used for {{title}}")
	cmd.Flags().StringVar(&output, "output", "", "explicit draft output path")
	cmd.Flags().StringArrayVar(&set, "set", nil, "set a draft property as name=YAML-value; may be repeated")
	cmd.Flags().StringArrayVar(&related, "related", nil, "stable knowledge ID to add through the content pack's default draft relation; may be repeated")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "allow replacing an existing output file")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm overwriting an existing output file")
	_ = cmd.MarkFlagRequired("title")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}
