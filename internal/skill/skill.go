package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	resourcebundle "llm-wiki/resources"
)

const (
	SkillVersion    = "4.0.1"
	manifestSchema  = 3
	manifestName    = ".llm-wiki-install.json"
	installLockName = ".llm-wiki-install.lock"
)

var skillNames = []string{
	"llm-wiki-add",
	"llm-wiki-query",
}

var legacySkillNames = []string{"llm-wiki-add", "llm-wiki-maintain", "llm-wiki-publish", "llm-wiki-query"}

type OwnedFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type InstallManifest struct {
	SchemaVersion int         `json:"schema_version"`
	Client        string      `json:"client"`
	SkillVersion  string      `json:"skill_version"`
	InstalledAt   string      `json:"installed_at"`
	Skills        []string    `json:"skills,omitempty"`
	Files         []OwnedFile `json:"files"`
}

type Status struct {
	Client          string   `json:"client"`
	Detected        bool     `json:"detected"`
	Target          string   `json:"target"`
	Installed       bool     `json:"installed"`
	Version         string   `json:"version,omitempty"`
	CurrentVersion  string   `json:"current_version"`
	Skills          []string `json:"skills"`
	Modified        []string `json:"modified"`
	Missing         []string `json:"missing"`
	UpdateAvailable bool     `json:"update_available"`
}

type Result struct {
	Client    string   `json:"client"`
	Target    string   `json:"target"`
	Action    string   `json:"action"`
	Skills    []string `json:"skills"`
	Files     []string `json:"files"`
	Preserved []string `json:"preserved"`
	DryRun    bool     `json:"dry_run"`
}

func SupportedClients() []string { return []string{"codex"} }

func SkillNames() []string { return append([]string(nil), skillNames...) }

func ResolveTarget(client string) (string, error) {
	if client != "codex" {
		return "", fmt.Errorf("unsupported AI client %q", client)
	}
	if override := os.Getenv("LLM_WIKI_CODEX_SKILLS_DIR"); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func GetStatus(client string) (*Status, error) {
	target, err := ResolveTarget(client)
	if err != nil {
		return nil, err
	}
	if err := ensureTargetSafe(target); err != nil {
		return nil, err
	}
	_, lookErr := exec.LookPath("codex")
	status := &Status{
		Client: client, Target: target, Detected: lookErr == nil,
		CurrentVersion: SkillVersion, Skills: SkillNames(),
	}
	manifest, err := readManifest(target)
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.Installed = true
	status.Version = manifest.SkillVersion
	status.UpdateAvailable = manifest.SkillVersion != SkillVersion || !equalStrings(manifest.Skills, skillNames)
	for _, owned := range manifest.Files {
		path, err := managedFilePath(target, owned.Path)
		if err != nil {
			return nil, err
		}
		if err := fsutil.EnsureNoSymlinkPath(target, path); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			status.Missing = append(status.Missing, owned.Path)
			continue
		}
		if err != nil {
			return nil, err
		}
		if document.HashBytes(b) != owned.Hash {
			status.Modified = append(status.Modified, owned.Path)
		}
	}
	sort.Strings(status.Modified)
	sort.Strings(status.Missing)
	return status, nil
}

func Install(client string, update, dryRun bool) (*Result, error) {
	target, err := ResolveTarget(client)
	if err != nil {
		return nil, err
	}
	if err := ensureTargetSafe(target); err != nil {
		return nil, err
	}
	if !dryRun {
		clientLock, lockPath, err := acquireClientLock(target)
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = clientLock.Unlock()
			_ = os.Remove(lockPath)
		}()
	}
	if err := ensureTargetSafe(target); err != nil {
		return nil, err
	}
	files, err := sourceFiles()
	if err != nil {
		return nil, err
	}
	existing, manifestErr := readManifest(target)
	installed := manifestErr == nil
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return nil, manifestErr
	}
	if installed && !update {
		return nil, errors.New("skill is already installed; use skill update")
	}
	if !installed && update {
		return nil, errors.New("skill is not installed; use skill install")
	}
	newPaths := map[string]bool{}
	for _, file := range files {
		newPaths[file.Path] = true
	}
	preservedOld := []string{}
	if installed {
		for _, owned := range existing.Files {
			path, err := managedFilePath(target, owned.Path)
			if err != nil {
				return nil, err
			}
			if err := fsutil.EnsureNoSymlinkPath(target, path); err != nil {
				return nil, err
			}
			b, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if document.HashBytes(b) != owned.Hash {
				if newPaths[owned.Path] {
					return nil, fmt.Errorf("installed skill file was modified: %s", owned.Path)
				}
				preservedOld = append(preservedOld, owned.Path)
			}
		}
	}
	previouslyOwned := map[string]bool{}
	if installed {
		for _, owned := range existing.Files {
			previouslyOwned[owned.Path] = true
		}
	}
	for _, file := range files {
		path, err := managedFilePath(target, file.Path)
		if err != nil {
			return nil, err
		}
		if err := fsutil.EnsureNoSymlinkPath(target, path); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(path); err == nil && !previouslyOwned[file.Path] {
			return nil, fmt.Errorf("skill target contains unmanaged conflicting file: %s", file.Path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	action := "installed"
	if update {
		action = "updated"
	}
	result := &Result{
		Client: client, Target: target, Action: action, Skills: SkillNames(), Preserved: preservedOld, DryRun: dryRun,
	}
	for _, file := range files {
		result.Files = append(result.Files, file.Path)
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, err
	}
	manifest := InstallManifest{
		SchemaVersion: manifestSchema, Client: client, SkillVersion: SkillVersion,
		InstalledAt: time.Now().Format(time.RFC3339), Skills: SkillNames(), Files: files,
	}
	for _, file := range files {
		b, err := resourcebundle.FS.ReadFile("skills/" + file.Path)
		if err != nil {
			return nil, err
		}
		path, err := managedFilePath(target, file.Path)
		if err != nil {
			return nil, err
		}
		if err := document.AtomicWrite(path, b, 0o600); err != nil {
			return nil, err
		}
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := document.AtomicWrite(filepath.Join(target, manifestName), manifestBytes, 0o600); err != nil {
		return nil, err
	}
	if installed {
		current := make(map[string]bool, len(files))
		for _, file := range files {
			current[file.Path] = true
		}
		for _, owned := range existing.Files {
			if current[owned.Path] {
				continue
			}
			if contains(result.Preserved, owned.Path) {
				continue
			}
			path, pathErr := managedFilePath(target, owned.Path)
			if pathErr != nil {
				return nil, pathErr
			}
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return nil, removeErr
			}
		}
		removeOwnedEmptyDirs(target, existing.Files)
	}
	return result, nil
}

func Uninstall(client string, dryRun bool) (*Result, error) {
	target, err := ResolveTarget(client)
	if err != nil {
		return nil, err
	}
	if err := ensureTargetSafe(target); err != nil {
		return nil, err
	}
	if !dryRun {
		clientLock, lockPath, err := acquireClientLock(target)
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = clientLock.Unlock()
			_ = os.Remove(lockPath)
		}()
		if err := ensureTargetSafe(target); err != nil {
			return nil, err
		}
	}
	manifest, err := readManifest(target)
	if err != nil {
		return nil, err
	}
	result := &Result{
		Client: client, Target: target, Action: "uninstalled", Skills: append([]string(nil), manifest.Skills...), DryRun: dryRun,
	}
	for _, owned := range manifest.Files {
		path, err := managedFilePath(target, owned.Path)
		if err != nil {
			return nil, err
		}
		if err := fsutil.EnsureNoSymlinkPath(target, path); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if document.HashBytes(b) != owned.Hash {
			result.Preserved = append(result.Preserved, owned.Path)
		}
	}
	if len(result.Preserved) > 0 {
		return nil, fmt.Errorf("modified skill files were preserved: %s", strings.Join(result.Preserved, ", "))
	}
	for _, owned := range manifest.Files {
		path, err := managedFilePath(target, owned.Path)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if document.HashBytes(b) != owned.Hash {
			continue
		}
		result.Files = append(result.Files, owned.Path)
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
		}
	}
	if !dryRun {
		if err := os.Remove(filepath.Join(target, manifestName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		removeOwnedEmptyDirs(target, manifest.Files)
	}
	return result, nil
}

func sourceFiles() ([]OwnedFile, error) {
	var out []OwnedFile
	for _, name := range skillNames {
		root := "skills/" + name
		err := fs.WalkDir(resourcebundle.FS, root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel := strings.TrimPrefix(path, "skills/")
			b, err := resourcebundle.FS.ReadFile(path)
			if err != nil {
				return err
			}
			out = append(out, OwnedFile{Path: rel, Hash: document.HashBytes(b)})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func readManifest(target string) (InstallManifest, error) {
	var manifest InstallManifest
	b, err := os.ReadFile(filepath.Join(target, manifestName))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return manifest, err
	}
	if (manifest.SchemaVersion != 2 && manifest.SchemaVersion != manifestSchema) || manifest.Client != "codex" || manifest.SkillVersion == "" {
		return manifest, errors.New("invalid skill installation manifest")
	}
	if !validManifestSkills(manifest.Skills) {
		return manifest, errors.New("skill installation manifest has an unexpected skill set")
	}
	seen := map[string]bool{}
	for _, owned := range manifest.Files {
		if _, err := managedFilePath(target, owned.Path); err != nil {
			return manifest, fmt.Errorf("invalid owned skill path: %w", err)
		}
		if !isManagedSkillPath(owned.Path, manifest.Skills) {
			return manifest, fmt.Errorf("owned path is outside the llm-wiki skill set: %s", owned.Path)
		}
		if !document.ValidHash(owned.Hash) || seen[owned.Path] {
			return manifest, errors.New("invalid or duplicate owned skill file")
		}
		seen[owned.Path] = true
	}
	return manifest, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validManifestSkills(names []string) bool {
	return equalStrings(names, skillNames) || equalStrings(names, legacySkillNames)
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func isManagedSkillPath(relative string, names []string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	for _, name := range names {
		if clean == name || strings.HasPrefix(clean, name+"/") {
			return true
		}
	}
	return false
}

func managedFilePath(target, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if relative == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("managed path escapes skill target: %q", relative)
	}
	return filepath.Join(target, clean), nil
}

func ensureTargetSafe(target string) error {
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("skill target cannot be a symbolic link")
		}
		if !info.IsDir() {
			return errors.New("skill target exists and is not a directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fsutil.EnsureNoSymlinkPath(target, filepath.Join(target, manifestName))
}

func removeOwnedEmptyDirs(root string, files []OwnedFile) {
	dirs := map[string]bool{}
	for _, owned := range files {
		path, err := managedFilePath(root, owned.Path)
		if err != nil {
			continue
		}
		for dir := filepath.Dir(path); dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i], string(filepath.Separator)) > strings.Count(ordered[j], string(filepath.Separator))
	})
	for _, dir := range ordered {
		_ = os.Remove(dir)
	}
}

func acquireClientLock(target string) (*flock.Flock, string, error) {
	path := filepath.Join(target, installLockName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, path, err
	}
	f := flock.New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := f.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, path, errors.New("skill target is locked by another process")
		}
		return nil, path, err
	}
	if !ok {
		return nil, path, errors.New("skill target is locked by another process")
	}
	return f, path, nil
}
