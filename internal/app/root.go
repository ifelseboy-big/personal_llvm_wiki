package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/inbox"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/skill"
	"llm-wiki/internal/templates"
	"llm-wiki/internal/vault"
)

func NewRootCommand() *cobra.Command {
	rt := NewRuntime()
	return newRootCommand(rt)
}

func NewRootCommandWithIO(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	rt := NewRuntime()
	rt.Stdin, rt.Stdout, rt.Stderr = stdin, stdout, stderr
	return newRootCommand(rt)
}

func newRootCommand(rt *Runtime) *cobra.Command {
	ctx := context.WithValue(context.Background(), runtimeKey{}, rt)
	root := &cobra.Command{
		Use:           "llm-wiki",
		Short:         "Manage a local, file-first trusted knowledge base",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			rt.Command = strings.ReplaceAll(cmd.CommandPath(), "llm-wiki ", "")
			if rt.JSON {
				rt.NoInteractive = true
				rt.Color = "never"
			}
			if rt.Color != "auto" && rt.Color != "always" && rt.Color != "never" {
				return E("INVALID_COLOR", "--color must be auto, always, or never", ExitUsage, nil)
			}
			return nil
		},
	}
	root.SetContext(ctx)
	root.SetOut(rt.Stdout)
	root.SetErr(rt.Stderr)
	root.PersistentFlags().StringVar(&rt.WikiArg, "wiki", "", "wiki alias or path")
	root.PersistentFlags().BoolVar(&rt.JSON, "json", false, "emit stable JSON output")
	root.PersistentFlags().BoolVar(&rt.NoInteractive, "no-interactive", false, "never prompt for input")
	root.PersistentFlags().BoolVar(&rt.DryRun, "dry-run", false, "preview writes without changing files")
	root.PersistentFlags().BoolVarP(&rt.Quiet, "quiet", "q", false, "suppress human success output")
	root.PersistentFlags().BoolVarP(&rt.Verbose, "verbose", "v", false, "show diagnostic details on stderr")
	root.PersistentFlags().StringVar(&rt.Color, "color", "auto", "color mode: auto, always, never")

	root.AddCommand(newInitCommand(rt))
	root.AddCommand(newLocateCommand(rt))
	root.AddCommand(newStatusCommand(rt))
	root.AddCommand(newDoctorCommand(rt))
	root.AddCommand(newTemplateCommand(rt))
	root.AddCommand(newInboxCommand(rt))
	root.AddCommand(newIndexCommand(rt))
	root.AddCommand(newQueryCommand(rt))
	root.AddCommand(newShowCommand(rt))
	root.AddCommand(newPromoteCommand(rt))
	root.AddCommand(newSkillCommand(rt))
	root.AddCommand(newUpdateCommand(rt))
	return root
}

func resolveWiki(rt *Runtime) (*config.Instance, *WikiRef, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, E("CURRENT_DIRECTORY_FAILED", "cannot determine current directory", ExitIO, err)
	}
	cfg, err := config.Resolve(rt.WikiArg, cwd)
	if err != nil {
		return nil, nil, E("WIKI_NOT_FOUND", "cannot locate wiki", ExitConfig, err)
	}
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, nil, E("UNSAFE_WIKI_PATH", "wiki managed paths are unsafe", ExitSafety, err)
	}
	ref := wikiRef(cfg)
	rt.Wiki = ref
	return cfg, ref, nil
}

func wikiRef(cfg *config.Instance) *WikiRef {
	return &WikiRef{ID: cfg.InstanceID, Name: cfg.Name, Path: cfg.Root}
}

func recoverIfNeeded(cfg *config.Instance, dryRun bool) ([]string, error) {
	if dryRun {
		pending, err := promote.PendingOperations(cfg)
		if err != nil {
			return nil, E("RECOVERY_INSPECTION_FAILED", "cannot inspect interrupted wiki transactions", ExitIO, err)
		}
		if len(pending) > 0 {
			err := E("RECOVERY_REQUIRED", "dry-run cannot continue while interrupted transactions require recovery", ExitConflict, nil)
			err.Details = map[string]any{"operations": pending}
			return nil, err
		}
		return nil, nil
	}
	actions, err := promote.Recover(cfg)
	if err != nil {
		if errors.Is(err, vault.ErrLocked) {
			return nil, E("WIKI_LOCKED", "wiki is locked by another writer", ExitLock, err)
		}
		return nil, E("RECOVERY_FAILED", "cannot recover interrupted wiki transaction", ExitIO, err)
	}
	if len(actions) == 0 {
		return nil, nil
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		return nil, E("RECOVERY_INDEX_FAILED", "files recovered but index rebuild failed", ExitIndex, err)
	}
	warnings := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Action == "index_required" {
			if err := promote.CompleteOperation(cfg, action.OperationID); err != nil {
				return nil, E("RECOVERY_FINALIZE_FAILED", "index rebuilt but transaction could not be finalized", ExitIO, err)
			}
		}
		warnings = append(warnings, fmt.Sprintf("recovered operation %s: %s", action.OperationID, action.Action))
	}
	return warnings, nil
}

func newInitCommand(rt *Runtime) *cobra.Command {
	var name, templateName, onConflict, skillClient string
	var register, makeDefault, installSkill, yes bool
	cmd := &cobra.Command{
		Use:   "init <path>",
		Short: "Initialize a wiki without moving existing files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if installSkill || cmd.Flags().Changed("skill-client") {
				if _, err := skill.ResolveTarget(skillClient); err != nil {
					return E("SKILL_CLIENT_UNSUPPORTED", "unsupported AI client", ExitUnsupported, err)
				}
			}
			interactive := !rt.NoInteractive && stdinIsTerminal()
			reader := bufio.NewReader(rt.Stdin)
			if interactive {
				var promptErr error
				if !cmd.Flags().Changed("name") {
					name, promptErr = promptText(reader, rt, "Wiki name", filepath.Base(args[0]))
					if promptErr != nil {
						return E("INIT_PROMPT_FAILED", "cannot read wiki name", ExitIO, promptErr)
					}
				}
				if !cmd.Flags().Changed("template") {
					templateName, promptErr = promptText(reader, rt, "Template", "personal")
					if promptErr != nil {
						return E("INIT_PROMPT_FAILED", "cannot read template", ExitIO, promptErr)
					}
				}
				if !cmd.Flags().Changed("register") {
					register, promptErr = promptYesNo(reader, rt, "Register this wiki", false)
					if promptErr != nil {
						return E("INIT_PROMPT_FAILED", "cannot read registration choice", ExitIO, promptErr)
					}
				}
				if register && !cmd.Flags().Changed("default") {
					makeDefault, promptErr = promptYesNo(reader, rt, "Set as default wiki", false)
					if promptErr != nil {
						return E("INIT_PROMPT_FAILED", "cannot read default choice", ExitIO, promptErr)
					}
				}
				if !cmd.Flags().Changed("install-skill") {
					status, _ := skill.GetStatus(skillClient)
					if (status == nil || !status.Detected || status.Installed) && !cmd.Flags().Changed("skill-client") {
						for _, candidate := range skill.SupportedClients() {
							candidateStatus, _ := skill.GetStatus(candidate)
							if candidateStatus != nil && candidateStatus.Detected && !candidateStatus.Installed {
								skillClient = candidate
								status = candidateStatus
								break
							}
						}
					}
					if status != nil && status.Detected && !status.Installed {
						installSkill, promptErr = promptYesNo(reader, rt, "Install "+skillClient+" skill set under "+status.Target, false)
						if promptErr != nil {
							return E("INIT_PROMPT_FAILED", "cannot read skill choice", ExitIO, promptErr)
						}
					}
				}
			}
			if installSkill && !interactive && !yes && !rt.DryRun {
				target, _ := skill.ResolveTarget(skillClient)
				err := E("CONFIRMATION_REQUIRED", "--install-skill requires --yes in non-interactive mode", ExitUsage, nil)
				err.Details = map[string]any{"target": target, "client": skillClient}
				return err
			}
			if onConflict != "" && onConflict != "error" && onConflict != "keep" {
				return E("INVALID_CONFLICT_POLICY", "--on-conflict must be error or keep", ExitUsage, nil)
			}
			if interactive && onConflict == "" && !rt.DryRun {
				preview, previewErr := vault.Init(vault.InitOptions{
					Path: args[0], Name: name, Template: templateName, DryRun: true,
				})
				if previewErr != nil {
					return E("INIT_PREFLIGHT_FAILED", "cannot preview wiki initialization", ExitConflict, previewErr)
				}
				if len(preview.Conflicts) > 0 {
					fmt.Fprintf(rt.Stderr, "Conflicting files: %s\n", strings.Join(preview.Conflicts, ", "))
					keep, promptErr := promptYesNo(reader, rt, "Keep all existing conflicting files", false)
					if promptErr != nil {
						return E("INIT_PROMPT_FAILED", "cannot read conflict choice", ExitIO, promptErr)
					}
					if keep {
						onConflict = "keep"
					} else {
						return E("INIT_CONFLICT", "initialization cancelled because files conflict", ExitConflict, nil)
					}
				}
			}
			result, err := vault.Init(vault.InitOptions{
				Path: args[0], Name: name, Template: templateName,
				Register: register, MakeDefault: makeDefault, KeepConflicts: onConflict == "keep", DryRun: rt.DryRun,
			})
			if err != nil && result == nil {
				return E("INIT_CONFLICT", "cannot initialize wiki", ExitConflict, err)
			}
			warnings := []string{}
			if err != nil {
				warnings = append(warnings, err.Error())
			}
			if installSkill {
				skillResult, skillErr := skill.Install(skillClient, false, rt.DryRun)
				if skillErr != nil {
					warnings = append(warnings, "wiki initialized but "+skillClient+" skill was not installed: "+skillErr.Error())
				} else {
					for _, file := range skillResult.Files {
						result.CreatedFiles = append(result.CreatedFiles, filepath.Join(skillResult.Target, filepath.FromSlash(file)))
					}
				}
			}
			if !rt.DryRun {
				indexResult, indexErr := indexstore.Rebuild(result.Config)
				if indexErr != nil {
					warnings = append(warnings, "wiki initialized but empty index creation failed: "+indexErr.Error())
				} else {
					result.CreatedFiles = append(result.CreatedFiles, indexResult.Path)
				}
			}
			files := append([]string{}, result.CreatedFiles...)
			files = append(files, result.UpdatedFiles...)
			return rt.Success("init", wikiRef(result.Config), result, warnings, files)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "registered wiki name; defaults to directory name")
	cmd.Flags().StringVar(&templateName, "template", "personal", "built-in vault template")
	cmd.Flags().BoolVar(&register, "register", false, "register this wiki in the user config")
	cmd.Flags().BoolVar(&makeDefault, "default", false, "set the registered wiki as default")
	cmd.Flags().BoolVar(&installSkill, "install-skill", false, "install the optional AI client skill set")
	cmd.Flags().StringVar(&skillClient, "skill-client", "codex", "AI client for --install-skill: codex or claude-code")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm optional external installation target")
	cmd.Flags().StringVar(&onConflict, "on-conflict", "", "existing template file policy: error or keep")
	return cmd
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func promptText(reader *bufio.Reader, rt *Runtime, label, defaultValue string) (string, error) {
	fmt.Fprintf(rt.Stderr, "%s [%s]: ", label, defaultValue)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultValue
	}
	return value, nil
}

func promptYesNo(reader *bufio.Reader, rt *Runtime, label string, defaultValue bool) (bool, error) {
	suffix := "y/N"
	if defaultValue {
		suffix = "Y/n"
	}
	fmt.Fprintf(rt.Stderr, "%s [%s]: ", label, suffix)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	value := strings.ToLower(strings.TrimSpace(line))
	if value == "" {
		return defaultValue, nil
	}
	return value == "y" || value == "yes", nil
}

func newLocateCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "locate",
		Short: "Locate the selected wiki",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			return rt.Success("locate", ref, map[string]any{
				"root": cfg.Root, "config": filepath.Join(cfg.Root, config.FileName),
				"resolution_order": []string{"explicit", "nearest", "default"},
			}, nil, nil)
		},
	}
}

func newStatusCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show wiki file and index status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			policy, policyErr := governance.Load(cfg)
			if policyErr != nil {
				return E("CONTENT_PACK_INVALID", "cannot load the instance-bound content pack", ExitValidation, policyErr)
			}
			counts := map[string]int{"pending_inbox": 0, "processed_inbox": 0, "knowledge": 0}
			problems := []string{}
			inboxDocs, inboxProblems := inbox.List(cfg, "")
			for _, doc := range inboxDocs {
				counts[doc.Metadata.Status+"_inbox"]++
			}
			for _, problem := range inboxProblems {
				problems = append(problems, problem.Error())
			}
			problems = append(problems, inbox.ProcessedPayloadWarnings(cfg)...)
			knowledgeDocs, knowledgeProblems := document.ScanMarkdown(cfg.KnowledgeDir())
			counts["knowledge"] = len(knowledgeDocs)
			for _, doc := range knowledgeDocs {
				rel, relErr := filepath.Rel(cfg.Root, doc.Path)
				if relErr != nil || filepath.ToSlash(rel) != document.KnowledgePath(cfg.Paths.Knowledge, doc.Metadata) {
					problems = append(problems, "knowledge path is not canonical: "+doc.Path)
				}
				if validateErr := doc.Validate("knowledge", true); validateErr != nil {
					problems = append(problems, doc.Path+": "+validateErr.Error())
				} else if governanceErr := governance.ValidateStored(cfg, doc, time.Now()); governanceErr != nil {
					problems = append(problems, doc.Path+": "+governanceErr.Error())
				}
			}
			for _, problem := range knowledgeProblems {
				problems = append(problems, problem.Error())
			}
			activePromotions, promotionErr := promote.ActiveCount(cfg)
			if promotionErr != nil {
				problems = append(problems, promotionErr.Error())
			}
			_, indexErr := os.Stat(filepath.Join(cfg.RuntimeDir(), "index.sqlite"))
			data := map[string]any{
				"documents": counts, "problems": problems,
				"index_exists": indexErr == nil,
				"template":     cfg.Template, "content_pack": map[string]any{
					"name": policy.Name, "version": policy.Version, "governance_version": policy.GovernanceVersion,
				}, "active_promotions": activePromotions,
			}
			return rt.Success("status", ref, data, problems, nil)
		},
	}
}

func newDoctorCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate wiki invariants and report repairs",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, ref, err := resolveWiki(rt)
			if err != nil {
				return err
			}
			type check struct {
				Name    string `json:"name"`
				OK      bool   `json:"ok"`
				Message string `json:"message"`
			}
			policy, policyErr := governance.Load(cfg)
			checks := []check{{Name: "config", OK: true, Message: "schema and managed paths are valid"}}
			if policyErr != nil {
				checks = append(checks, check{Name: "content-pack", OK: false, Message: policyErr.Error()})
			} else {
				checks = append(checks, check{Name: "content-pack", OK: true, Message: policy.Name + "@" + policy.Version + " policy is valid"})
			}
			attention := []string{}
			attention = append(attention, inbox.ProcessedPayloadWarnings(cfg)...)
			inboxDocs, inboxProblems := inbox.List(cfg, "")
			inboxIDs := map[string]bool{}
			for _, doc := range inboxDocs {
				err := error(nil)
				if inboxIDs[doc.Metadata.ID] {
					err = fmt.Errorf("duplicate inbox id %s", doc.Metadata.ID)
				}
				inboxIDs[doc.Metadata.ID] = true
				checks = append(checks, check{Name: "inbox:" + doc.Metadata.ID, OK: err == nil, Message: errorMessage(err, "valid")})
			}
			for _, problem := range inboxProblems {
				checks = append(checks, check{Name: "inbox:parse", OK: false, Message: problem.Error()})
			}
			knowledgeDocs, knowledgeProblems := document.ScanMarkdown(cfg.KnowledgeDir())
			knowledgeIDs := map[string]bool{}
			for _, doc := range knowledgeDocs {
				err := doc.Validate("knowledge", true)
				rel, relErr := filepath.Rel(cfg.Root, doc.Path)
				if err == nil && (relErr != nil || filepath.ToSlash(rel) != document.KnowledgePath(cfg.Paths.Knowledge, doc.Metadata)) {
					err = errors.New("knowledge path is not canonical")
				}
				if knowledgeIDs[doc.Metadata.ID] {
					err = fmt.Errorf("duplicate knowledge id %s", doc.Metadata.ID)
				}
				knowledgeIDs[doc.Metadata.ID] = true
				checks = append(checks, check{Name: "knowledge:" + doc.Metadata.ID, OK: err == nil, Message: errorMessage(err, "valid")})
				if err == nil && policyErr == nil {
					if governanceErr := governance.ValidateStored(cfg, doc, time.Now()); governanceErr != nil {
						checks = append(checks, check{Name: "knowledge-governance:" + doc.Metadata.ID, OK: false, Message: governanceErr.Error()})
						continue
					}
					assessment, assessmentErr := governance.AssessStoredLifecycle(cfg, doc.Metadata, time.Now())
					if assessmentErr != nil {
						checks = append(checks, check{Name: "knowledge-lifecycle:" + doc.Metadata.ID, OK: false, Message: assessmentErr.Error()})
						continue
					}
					attention = append(attention, assessment.Warnings...)
					checks = append(checks, check{
						Name: "knowledge-governance:" + doc.Metadata.ID, OK: true,
						Message: "content-pack metadata, lineage, and stable relations are valid",
					})
					if reciprocalErr := governance.ValidateReciprocalRelations(cfg, doc); reciprocalErr != nil {
						checks = append(checks, check{Name: "knowledge-relations:" + doc.Metadata.ID, OK: false, Message: reciprocalErr.Error()})
					}
				}
			}
			for _, problem := range knowledgeProblems {
				checks = append(checks, check{Name: "knowledge:parse", OK: false, Message: problem.Error()})
			}
			indexStatus, indexErr := indexstore.GetStatus(cfg)
			if indexErr != nil {
				checks = append(checks, check{Name: "index", OK: false, Message: indexErr.Error() + "; run index rebuild"})
			} else {
				expectedCounts := map[string]int{"knowledge": len(knowledgeDocs)}
				policyHash := ""
				policyHashErr := policyErr
				if policyHashErr == nil {
					policyHash, policyHashErr = policy.Hash()
				}
				indexOK := indexStatus.Exists && indexStatus.SchemaVersion == indexstore.SchemaVersion &&
					indexStatus.Tokenizer == "simple" &&
					indexStatus.TokenizerVersion != "" && indexStatus.TokenizerCommit != "" &&
					indexStatus.QueryPlannerVersion == indexstore.QueryPlannerVersion && policyHashErr == nil &&
					indexStatus.ContentPackName == policy.Name && indexStatus.ContentPackVersion == policy.Version &&
					indexStatus.GovernanceVersion == policy.GovernanceVersion && indexStatus.ContentPackHash == policyHash
				message := "index schema, tokenizer, content pack, and document counts match files"
				for layer, expected := range expectedCounts {
					indexOK = indexOK && indexStatus.Documents[layer] == expected
				}
				if indexOK {
					if probeErr := indexstore.ProbeTokenizer(cfg); probeErr != nil {
						indexOK = false
						message = "simple tokenizer probe failed: " + probeErr.Error() + "; run index rebuild"
					}
				}
				if indexOK {
					updatePlan, planErr := indexstore.Update(cfg, true)
					if planErr != nil {
						indexOK = false
					} else {
						indexOK = !updatePlan.FullRebuild && updatePlan.Added == 0 && updatePlan.Changed == 0 && updatePlan.Deleted == 0
					}
				}
				if !indexOK {
					if !strings.HasPrefix(message, "simple tokenizer probe failed:") {
						message = "index is missing, outdated, or stale; run index rebuild"
					}
				}
				checks = append(checks, check{Name: "index", OK: indexOK, Message: message})
			}
			ok := true
			for _, c := range checks {
				ok = ok && c.OK
			}
			attention = governance.SortedWarnings(attention)
			data := map[string]any{"healthy": ok, "checks": checks, "attention": attention}
			if !ok {
				err := E("DOCTOR_CHECK_FAILED", "wiki invariants failed", ExitValidation, nil)
				err.Details = data
				return err
			}
			return rt.Success("doctor", ref, data, attention, nil)
		},
	}
}

func errorMessage(err error, success string) string {
	if err == nil {
		return success
	}
	return err.Error()
}

func newTemplateCommand(rt *Runtime) *cobra.Command {
	cmd := &cobra.Command{Use: "template", Short: "Inspect built-in and wiki templates"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			items, err := templates.List()
			if err != nil {
				return E("TEMPLATE_READ_FAILED", "cannot read built-in templates", ExitIO, err)
			}
			cfg, ref, err := optionalWiki(rt)
			if err != nil {
				return err
			}
			content, err := templates.ListContent(cfg)
			if err != nil {
				return E("TEMPLATE_READ_FAILED", "cannot read content templates", ExitIO, err)
			}
			return rt.Success("template.list", ref, map[string]any{"vault_templates": items, "content_templates": content}, nil, nil)
		},
	})
	var file, kind string
	show := &cobra.Command{
		Use: "show <name>", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if file == "" {
				cfg, ref, err := optionalWiki(rt)
				if err != nil {
					return err
				}
				content, contentErr := templates.ReadContent(cfg, kind, args[0])
				if contentErr == nil {
					return rt.Success("template.show", ref, content, nil, nil)
				}
				m, manifestErr := templates.LoadManifest(args[0])
				if manifestErr != nil {
					return E("TEMPLATE_NOT_FOUND", "template not found", ExitNotFound, contentErr)
				}
				return rt.Success("template.show", nil, m, nil, nil)
			}
			b, err := templates.ReadFile(args[0], file)
			if err != nil {
				return E("TEMPLATE_FILE_NOT_FOUND", "template file not found", ExitNotFound, err)
			}
			return rt.Success("template.show", nil, map[string]any{"name": args[0], "file": file, "content": string(b)}, nil, nil)
		},
	}
	show.Flags().StringVar(&file, "file", "", "show a file inside the template")
	show.Flags().StringVar(&kind, "kind", "", "content template kind: inbox or knowledge")
	cmd.AddCommand(show)
	cmd.AddCommand(newTemplateCreateCommand(rt))
	cmd.AddCommand(newTemplateUpgradeCommand(rt))
	return cmd
}

func optionalWiki(rt *Runtime) (*config.Instance, *WikiRef, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, nil
	}
	cfg, err := config.Resolve(rt.WikiArg, cwd)
	if err != nil {
		if rt.WikiArg != "" {
			return nil, nil, E("WIKI_NOT_FOUND", "cannot locate wiki", ExitConfig, err)
		}
		return nil, nil, nil
	}
	return cfg, wikiRef(cfg), nil
}
