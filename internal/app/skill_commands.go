package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"llm-wiki/internal/skill"
)

func newSkillCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Manage the optional AI client skill adapter"}
	cmd.AddCommand(newSkillStatusCommand(rt))
	cmd.AddCommand(newSkillMutationCommand(rt, "install"))
	cmd.AddCommand(newSkillMutationCommand(rt, "update"))
	cmd.AddCommand(newSkillMutationCommand(rt, "uninstall"))
	return cmd
}

func newSkillStatusCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "status [client]", Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			clients := skill.SupportedClients()
			if len(args) == 1 {
				clients = []string{args[0]}
			}
			var statuses []*skill.Status
			for _, client := range clients {
				status, err := skill.GetStatus(client)
				if err != nil {
					return E("SKILL_STATUS_FAILED", "cannot inspect skill", ExitUnsupported, err)
				}
				statuses = append(statuses, status)
			}
			return rt.Success("skill.status", nil, map[string]any{"clients": statuses}, nil, nil)
		},
	}
}

func newSkillMutationCommand(rt *Runtime, action string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use: action + " <client>", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			client := args[0]
			target, err := skill.ResolveTarget(client)
			if err != nil {
				return E("SKILL_CLIENT_UNSUPPORTED", "unsupported AI client", ExitUnsupported, err)
			}
			if !rt.DryRun && !yes {
				if rt.NoInteractive {
					err := E("CONFIRMATION_REQUIRED", "skill mutation requires --yes in non-interactive mode", ExitUsage, nil)
					err.Details = map[string]any{"target": target, "action": action}
					return err
				}
				confirmed, err := promptConfirmation(rt, fmt.Sprintf("%s llm-wiki skill at %s?", action, target))
				if err != nil {
					return E("CONFIRMATION_FAILED", "cannot read confirmation", ExitIO, err)
				}
				if !confirmed {
					return E("CANCELLED", "skill operation cancelled", ExitConflict, nil)
				}
			}
			var result *skill.Result
			switch action {
			case "install":
				result, err = skill.Install(client, false, rt.DryRun)
			case "update":
				result, err = skill.Install(client, true, rt.DryRun)
			case "uninstall":
				result, err = skill.Uninstall(client, rt.DryRun)
			}
			if errors.Is(err, os.ErrNotExist) {
				return E("SKILL_NOT_INSTALLED", "skill is not installed", ExitNotFound, err)
			}
			if err != nil {
				return E("SKILL_"+strings.ToUpper(action)+"_FAILED", "cannot "+action+" skill", ExitConflict, err)
			}
			files := make([]string, 0, len(result.Files))
			for _, file := range result.Files {
				files = append(files, target+string(os.PathSeparator)+file)
			}
			return rt.Success("skill."+action, nil, result, nil, files)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the displayed installation target")
	return cmd
}

func promptConfirmation(rt *Runtime, question string) (bool, error) {
	fmt.Fprintf(rt.Stderr, "%s [y/N] ", question)
	line, err := bufio.NewReader(rt.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
