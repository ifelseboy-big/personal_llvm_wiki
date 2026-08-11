package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/templates"
)

type InitOptions struct {
	Path          string
	Name          string
	Template      string
	Register      bool
	MakeDefault   bool
	KeepConflicts bool
	DryRun        bool
}

type InitResult struct {
	Config        *config.Instance `json:"config"`
	CreatedFiles  []string         `json:"created_files"`
	UpdatedFiles  []string         `json:"updated_files,omitempty"`
	CreatedDirs   []string         `json:"created_directories"`
	Registered    bool             `json:"registered"`
	Default       bool             `json:"default"`
	TemplateFiles []string         `json:"template_files"`
	Conflicts     []string         `json:"conflicts"`
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func Init(opts InitOptions) (*InitResult, error) {
	if opts.Path == "" {
		return nil, errors.New("init path is required")
	}
	root, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, err
	}
	if opts.Name == "" {
		opts.Name = filepath.Base(root)
	}
	if !validName.MatchString(opts.Name) {
		return nil, fmt.Errorf("invalid wiki name %q", opts.Name)
	}
	if opts.Template == "" {
		opts.Template = "personal"
	}
	if opts.MakeDefault {
		opts.Register = true
	}
	m, err := templates.LoadManifest(opts.Template)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("cannot initialize a wiki at a symbolic link")
		}
		if !info.IsDir() {
			return nil, errors.New("init path exists and is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, config.FileName)); err == nil {
		return nil, fmt.Errorf("%s already exists", config.FileName)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	create, conflicts, err := templates.PlanInstall(root, opts.Template)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !opts.KeepConflicts && !opts.DryRun {
		return nil, fmt.Errorf("initialization conflicts with existing files: %v", conflicts)
	}
	ignorePlan, err := planGitIgnore(root)
	if err != nil {
		return nil, fmt.Errorf("configure %s: %w", gitIgnoreFileName, err)
	}
	wikiID, err := document.NewID("wiki", time.Now())
	if err != nil {
		return nil, err
	}
	cfg := config.DefaultInstance(opts.Name, wikiID, time.Now())
	cfg.Root = root
	cfg.Template.Name = m.Name
	cfg.Template.Version = m.Version

	dirs := []string{
		cfg.Paths.Inbox, cfg.Paths.Knowledge,
		cfg.Paths.Templates, cfg.Paths.Rules,
		filepath.Join(cfg.Paths.Runtime, "promotions"),
		filepath.Join(cfg.Paths.Runtime, "transactions"),
		filepath.Join(cfg.Paths.Runtime, "locks"),
		filepath.Join(cfg.Paths.Runtime, "logs"),
		filepath.Join(cfg.Paths.Runtime, "cache"),
	}
	internalTemplateFiles := []string{filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "template-state.json"))}
	for _, relative := range m.ManagedFiles {
		internalTemplateFiles = append(internalTemplateFiles,
			filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "template-base", m.Version, filepath.FromSlash(relative))))
	}
	result := &InitResult{
		Config: cfg, CreatedDirs: dirs,
		CreatedFiles: append([]string{config.FileName}, internalTemplateFiles...), Conflicts: conflicts,
	}
	if ignorePlan.changed {
		if ignorePlan.existed {
			result.UpdatedFiles = append(result.UpdatedFiles, gitIgnoreFileName)
		} else {
			result.CreatedFiles = append(result.CreatedFiles, gitIgnoreFileName)
		}
	}
	if opts.DryRun {
		if _, err := templates.Install(cfg, opts.Template, true, true); err != nil {
			return nil, err
		}
		result.TemplateFiles = create
		result.CreatedFiles = append(result.CreatedFiles, create...)
		result.Registered = opts.Register
		result.Default = opts.MakeDefault
		return result, nil
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return nil, err
		}
	}
	if err := config.Save(cfg); err != nil {
		return nil, err
	}
	if err := ignorePlan.apply(); err != nil {
		_ = os.Remove(filepath.Join(root, config.FileName))
		return nil, fmt.Errorf("configure %s: %w", gitIgnoreFileName, err)
	}
	installed, err := templates.Install(cfg, opts.Template, false, opts.KeepConflicts)
	if err != nil {
		cleanupErr := os.Remove(filepath.Join(root, config.FileName))
		if errors.Is(cleanupErr, os.ErrNotExist) {
			cleanupErr = nil
		}
		return nil, errors.Join(err, ignorePlan.rollback(), cleanupErr)
	}
	result.TemplateFiles = installed
	result.CreatedFiles = append(result.CreatedFiles, installed...)
	if opts.Register {
		if err := config.Register(cfg, opts.Name, opts.MakeDefault); err != nil {
			return result, fmt.Errorf("wiki initialized but registration failed: %w", err)
		}
		result.Registered = true
		result.Default = opts.MakeDefault
		if registry, _, loadErr := config.LoadRegistry(); loadErr == nil {
			result.Default = registry.Default == opts.Name
		}
	}
	return result, nil
}
