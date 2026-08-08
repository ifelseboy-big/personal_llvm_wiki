package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	resourcebundle "llm-wiki/resources"
)

type Manifest struct {
	Name          string   `toml:"name" json:"name"`
	Version       string   `toml:"version" json:"version"`
	SchemaVersion int      `toml:"schema_version" json:"schema_version"`
	Description   string   `toml:"description" json:"description"`
	ManagedFiles  []string `toml:"managed_files" json:"managed_files"`
}

type ContentTemplate struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Origin  string `json:"origin"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

type FileState struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type InstallState struct {
	TemplateName    string      `json:"template_name"`
	TemplateVersion string      `json:"template_version"`
	Files           []FileState `json:"files"`
}

type UpgradeAction struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	OldHash     string `json:"old_hash,omitempty"`
	CurrentHash string `json:"current_hash,omitempty"`
	NewHash     string `json:"new_hash,omitempty"`
	Diff        string `json:"diff,omitempty"`
}

type UpgradePlan struct {
	Template       string          `json:"template"`
	CurrentVersion string          `json:"current_version"`
	TargetVersion  string          `json:"target_version"`
	Actions        []UpgradeAction `json:"actions"`
	HasConflicts   bool            `json:"has_conflicts"`
}

func List() ([]Manifest, error) {
	entries, err := fs.ReadDir(resourcebundle.FS, "vault-templates")
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m, err := LoadManifest(entry.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func LoadManifest(name string) (Manifest, error) {
	var m Manifest
	b, err := resourcebundle.FS.ReadFile("vault-templates/" + name + "/template.toml")
	if err != nil {
		return m, err
	}
	if err := toml.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.Name != name || m.Version == "" || m.SchemaVersion != config.CurrentSchema {
		return m, fmt.Errorf("invalid embedded template manifest %q", name)
	}
	return m, nil
}

func ReadFile(templateName, relative string) ([]byte, error) {
	clean := filepath.ToSlash(filepath.Clean(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, fmt.Errorf("invalid template path %q", relative)
	}
	return resourcebundle.FS.ReadFile("vault-templates/" + templateName + "/" + clean)
}

func ListContent(cfg *config.Instance) ([]ContentTemplate, error) {
	items := map[string]ContentTemplate{}
	for _, kind := range []string{"raw", "knowledge"} {
		root := "vault-templates/personal/templates/" + kind
		entries, err := fs.ReadDir(resourcebundle.FS, root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".md")
			key := kind + "/" + name
			items[key] = ContentTemplate{Name: name, Kind: kind, Origin: "built-in", Path: key + ".md"}
		}
	}
	if cfg != nil {
		for _, kind := range []string{"raw", "knowledge"} {
			root := filepath.Join(cfg.TemplatesDir(), kind)
			if err := fsutil.EnsureNoSymlinkPath(cfg.Root, root); err != nil {
				return nil, err
			}
			entries, err := os.ReadDir(root)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.Type()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("content template cannot be a symbolic link: %s", filepath.Join(root, entry.Name()))
				}
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(entry.Name(), ".md")
				key := kind + "/" + name
				rel, _ := filepath.Rel(cfg.Root, filepath.Join(root, entry.Name()))
				items[key] = ContentTemplate{Name: name, Kind: kind, Origin: "wiki", Path: filepath.ToSlash(rel)}
			}
		}
	}
	var out []ContentTemplate
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func ReadContent(cfg *config.Instance, kind, name string) (ContentTemplate, error) {
	if kind != "" && kind != "raw" && kind != "knowledge" {
		return ContentTemplate{}, fmt.Errorf("invalid content template kind %q", kind)
	}
	kinds := []string{"knowledge", "raw"}
	if kind != "" {
		kinds = []string{kind}
	}
	for _, candidateKind := range kinds {
		if cfg != nil {
			path := filepath.Join(cfg.TemplatesDir(), candidateKind, document.SafeBaseName(name)+".md")
			if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
				return ContentTemplate{}, err
			}
			if b, err := os.ReadFile(path); err == nil {
				rel, _ := filepath.Rel(cfg.Root, path)
				return ContentTemplate{Name: name, Kind: candidateKind, Origin: "wiki", Path: filepath.ToSlash(rel), Content: string(b)}, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return ContentTemplate{}, err
			}
		}
		path := "vault-templates/personal/templates/" + candidateKind + "/" + document.SafeBaseName(name) + ".md"
		if b, err := resourcebundle.FS.ReadFile(path); err == nil {
			return ContentTemplate{Name: name, Kind: candidateKind, Origin: "built-in", Path: candidateKind + "/" + name + ".md", Content: string(b)}, nil
		}
	}
	return ContentTemplate{}, os.ErrNotExist
}

func PlanInstall(root, templateName string) ([]string, []string, error) {
	m, err := LoadManifest(templateName)
	if err != nil {
		return nil, nil, err
	}
	var create, conflicts []string
	for _, relative := range m.ManagedFiles {
		target := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Lstat(target); err == nil {
			conflicts = append(conflicts, relative)
		} else if os.IsNotExist(err) {
			create = append(create, relative)
		} else {
			return nil, nil, err
		}
	}
	sort.Strings(create)
	sort.Strings(conflicts)
	return create, conflicts, nil
}

func Install(cfg *config.Instance, templateName string, dryRun, keepExisting bool) ([]string, error) {
	m, err := LoadManifest(templateName)
	if err != nil {
		return nil, err
	}
	create, conflicts, err := PlanInstall(cfg.Root, templateName)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !keepExisting {
		return nil, fmt.Errorf("template files already exist: %s", strings.Join(conflicts, ", "))
	}
	prepared := make(map[string][]byte, len(m.ManagedFiles))
	for _, relative := range m.ManagedFiles {
		b, err := ReadFile(templateName, relative)
		if err != nil {
			return nil, err
		}
		prepared[relative] = b
		target := filepath.Join(cfg.Root, filepath.FromSlash(relative))
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
			return nil, err
		}
		basePath := filepath.Join(cfg.RuntimeDir(), "template-base", m.Version, filepath.FromSlash(relative))
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, basePath); err != nil {
			return nil, err
		}
		if existing, err := os.ReadFile(basePath); err == nil && !bytes.Equal(existing, b) {
			return nil, fmt.Errorf("template baseline already exists with different content: %s", relative)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	statePath := filepath.Join(cfg.RuntimeDir(), "template-state.json")
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, statePath); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(statePath); err == nil {
		return nil, errors.New("template installation state already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if dryRun {
		return create, nil
	}
	state := InstallState{TemplateName: m.Name, TemplateVersion: m.Version}
	createSet := map[string]bool{}
	for _, relative := range create {
		createSet[relative] = true
	}
	var written []string
	rollback := func(cause error) error {
		for i := len(written) - 1; i >= 0; i-- {
			cause = errors.Join(cause, os.Remove(written[i]))
		}
		return cause
	}
	for _, relative := range m.ManagedFiles {
		b := prepared[relative]
		if createSet[relative] {
			target := filepath.Join(cfg.Root, filepath.FromSlash(relative))
			if _, err := os.Lstat(target); err == nil {
				return nil, rollback(fmt.Errorf("template target appeared during installation: %s", relative))
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, rollback(err)
			}
			if err := document.AtomicWrite(target, b, 0o600); err != nil {
				return nil, rollback(err)
			}
			written = append(written, target)
		}
		state.Files = append(state.Files, FileState{Path: relative, Hash: document.HashBytes(b)})
		basePath := filepath.Join(cfg.RuntimeDir(), "template-base", m.Version, filepath.FromSlash(relative))
		if _, err := os.Stat(basePath); errors.Is(err, os.ErrNotExist) {
			if err := document.AtomicWrite(basePath, b, 0o600); err != nil {
				return nil, rollback(err)
			}
			written = append(written, basePath)
		} else if err != nil {
			return nil, rollback(err)
		}
	}
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, rollback(err)
	}
	stateBytes = append(stateBytes, '\n')
	if err := document.AtomicWrite(statePath, stateBytes, 0o600); err != nil {
		return nil, rollback(err)
	}
	return create, nil
}

func PlanUpgrade(cfg *config.Instance) (*UpgradePlan, error) {
	state, err := loadInstallState(cfg)
	if err != nil {
		return nil, err
	}
	target, err := LoadManifest(cfg.Template.Name)
	if err != nil {
		return nil, err
	}
	plan := &UpgradePlan{Template: target.Name, CurrentVersion: state.TemplateVersion, TargetVersion: target.Version}
	old := map[string]string{}
	for _, file := range state.Files {
		old[file.Path] = file.Hash
	}
	newSet := map[string]bool{}
	for _, relative := range target.ManagedFiles {
		newSet[relative] = true
		newBytes, err := ReadFile(target.Name, relative)
		if err != nil {
			return nil, err
		}
		newHash := document.HashBytes(newBytes)
		currentBytes, currentErr := os.ReadFile(filepath.Join(cfg.Root, filepath.FromSlash(relative)))
		currentExists := currentErr == nil
		if currentErr != nil && !os.IsNotExist(currentErr) {
			return nil, currentErr
		}
		currentHash := ""
		if currentExists {
			currentHash = document.HashBytes(currentBytes)
		}
		oldHash, wasManaged := old[relative]
		action := UpgradeAction{Path: relative, OldHash: oldHash, CurrentHash: currentHash, NewHash: newHash}
		switch {
		case !wasManaged && !currentExists:
			action.Action, action.Reason = "create", "new managed template file"
		case !wasManaged && currentExists:
			action.Action, action.Reason = "conflict", "new template path is already occupied"
		case wasManaged && !currentExists:
			action.Action, action.Reason = "missing", "previously managed file was removed"
		case currentHash == newHash:
			action.Action, action.Reason = "unchanged", "already matches target template"
		case currentHash == oldHash:
			action.Action, action.Reason = "update", "unmodified managed file has a new template version"
		default:
			action.Action, action.Reason = "conflict", "managed file contains user changes"
		}
		if action.Action == "update" || action.Action == "conflict" {
			action.Diff = simpleDiff(relative, currentBytes, newBytes)
		}
		if action.Action == "conflict" || action.Action == "missing" {
			plan.HasConflicts = true
		}
		plan.Actions = append(plan.Actions, action)
	}
	for relative, oldHash := range old {
		if !newSet[relative] {
			plan.Actions = append(plan.Actions, UpgradeAction{
				Path: relative, Action: "obsolete", Reason: "removed from new template; user file will be preserved", OldHash: oldHash,
			})
		}
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].Path < plan.Actions[j].Path })
	return plan, nil
}

func ApplyUpgrade(cfg *config.Instance, keepConflicts, dryRun bool) (*UpgradePlan, []string, error) {
	plan, err := PlanUpgrade(cfg)
	if err != nil {
		return nil, nil, err
	}
	if plan.HasConflicts && !keepConflicts {
		return plan, nil, errors.New("template upgrade has conflicts; resolve them or pass --keep-conflicts")
	}
	if dryRun {
		return plan, nil, nil
	}
	hasTemplateWrites := false
	for _, action := range plan.Actions {
		if action.Action == "create" || action.Action == "update" {
			hasTemplateWrites = true
			break
		}
	}
	if !hasTemplateWrites && plan.CurrentVersion == plan.TargetVersion {
		return plan, nil, nil
	}
	m, err := LoadManifest(cfg.Template.Name)
	if err != nil {
		return nil, nil, err
	}
	var affected []string
	for _, action := range plan.Actions {
		if action.Action != "create" && action.Action != "update" {
			continue
		}
		b, err := ReadFile(m.Name, action.Path)
		if err != nil {
			return plan, affected, err
		}
		target := filepath.Join(cfg.Root, filepath.FromSlash(action.Path))
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
			return plan, affected, err
		}
		if err := document.AtomicWrite(target, b, 0o600); err != nil {
			return plan, affected, err
		}
		affected = append(affected, action.Path)
	}
	state := InstallState{TemplateName: m.Name, TemplateVersion: m.Version}
	for _, relative := range m.ManagedFiles {
		b, err := ReadFile(m.Name, relative)
		if err != nil {
			return plan, affected, err
		}
		state.Files = append(state.Files, FileState{Path: relative, Hash: document.HashBytes(b)})
		basePath := filepath.Join(cfg.RuntimeDir(), "template-base", m.Version, filepath.FromSlash(relative))
		if err := document.AtomicWrite(basePath, b, 0o600); err != nil {
			return plan, affected, err
		}
		baseRel, _ := filepath.Rel(cfg.Root, basePath)
		affected = append(affected, filepath.ToSlash(baseRel))
	}
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return plan, affected, err
	}
	statePath := filepath.Join(cfg.RuntimeDir(), "template-state.json")
	if err := document.AtomicWrite(statePath, append(stateBytes, '\n'), 0o600); err != nil {
		return plan, affected, err
	}
	stateRel, _ := filepath.Rel(cfg.Root, statePath)
	affected = append(affected, filepath.ToSlash(stateRel))
	cfg.Template.Version = m.Version
	if err := config.Save(cfg); err != nil {
		return plan, affected, err
	}
	affected = append(affected, config.FileName)
	return plan, affected, nil
}

func loadInstallState(cfg *config.Instance) (InstallState, error) {
	var state InstallState
	b, err := os.ReadFile(filepath.Join(cfg.RuntimeDir(), "template-state.json"))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return state, err
	}
	if state.TemplateName == "" || state.TemplateVersion == "" {
		return state, errors.New("invalid template installation state")
	}
	return state, nil
}

func simpleDiff(path string, current, target []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- current/%s\n+++ template/%s\n", path, path)
	for _, line := range strings.Split(strings.TrimSuffix(string(current), "\n"), "\n") {
		fmt.Fprintf(&b, "-%s\n", line)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(target), "\n"), "\n") {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	return b.String()
}
