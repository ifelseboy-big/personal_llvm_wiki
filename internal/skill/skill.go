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

const SkillVersion = "1.0.0"

type OwnedFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type InstallManifest struct {
	SchemaVersion int         `json:"schema_version"`
	Client        string      `json:"client"`
	SkillVersion  string      `json:"skill_version"`
	InstalledAt   string      `json:"installed_at"`
	Files         []OwnedFile `json:"files"`
}

type Status struct {
	Client          string   `json:"client"`
	Detected        bool     `json:"detected"`
	Target          string   `json:"target"`
	Installed       bool     `json:"installed"`
	Version         string   `json:"version,omitempty"`
	CurrentVersion  string   `json:"current_version"`
	Modified        []string `json:"modified"`
	Missing         []string `json:"missing"`
	UpdateAvailable bool     `json:"update_available"`
}

type Result struct {
	Client    string   `json:"client"`
	Target    string   `json:"target"`
	Action    string   `json:"action"`
	Files     []string `json:"files"`
	Preserved []string `json:"preserved"`
	DryRun    bool     `json:"dry_run"`
}

func SupportedClients() []string { return []string{"codex"} }

func ResolveTarget(client string) (string, error) {
	if client != "codex" {
		return "", fmt.Errorf("unsupported AI client %q", client)
	}
	if override := os.Getenv("LLM_WIKI_CODEX_SKILLS_DIR"); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", err
		}
		return filepath.Join(abs, "llm-wiki"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills", "llm-wiki"), nil
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
	status := &Status{Client: client, Target: target, Detected: lookErr == nil, CurrentVersion: SkillVersion}
	manifest, err := readManifest(target)
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.Installed = true
	status.Version = manifest.SkillVersion
	status.UpdateAvailable = manifest.SkillVersion != SkillVersion
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
				return nil, fmt.Errorf("installed skill file was modified: %s", owned.Path)
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
	result := &Result{Client: client, Target: target, Action: action, DryRun: dryRun}
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
		SchemaVersion: 1, Client: client, SkillVersion: SkillVersion,
		InstalledAt: time.Now().Format(time.RFC3339), Files: files,
	}
	for _, file := range files {
		b, err := resourcebundle.FS.ReadFile("skills/llm-wiki/" + file.Path)
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
	if err := document.AtomicWrite(filepath.Join(target, ".llm-wiki-install.json"), manifestBytes, 0o600); err != nil {
		return nil, err
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
	}
	manifest, err := readManifest(target)
	if err != nil {
		return nil, err
	}
	result := &Result{Client: client, Target: target, Action: "uninstalled", DryRun: dryRun}
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
		if err := os.Remove(filepath.Join(target, ".llm-wiki-install.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		removeEmptyParents(target)
	}
	return result, nil
}

func sourceFiles() ([]OwnedFile, error) {
	var out []OwnedFile
	err := fs.WalkDir(resourcebundle.FS, "skills/llm-wiki", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, "skills/llm-wiki/")
		b, err := resourcebundle.FS.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, OwnedFile{Path: rel, Hash: document.HashBytes(b)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func readManifest(target string) (InstallManifest, error) {
	var manifest InstallManifest
	b, err := os.ReadFile(filepath.Join(target, ".llm-wiki-install.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return manifest, err
	}
	if manifest.SchemaVersion != 1 || manifest.Client != "codex" || manifest.SkillVersion == "" {
		return manifest, errors.New("invalid skill installation manifest")
	}
	seen := map[string]bool{}
	for _, owned := range manifest.Files {
		if _, err := managedFilePath(target, owned.Path); err != nil {
			return manifest, fmt.Errorf("invalid owned skill path: %w", err)
		}
		if !document.ValidHash(owned.Hash) || seen[owned.Path] {
			return manifest, errors.New("invalid or duplicate owned skill file")
		}
		seen[owned.Path] = true
	}
	return manifest, nil
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
	return fsutil.EnsureNoSymlinkPath(target, filepath.Join(target, ".llm-wiki-install.json"))
}

func removeEmptyParents(target string) {
	_ = os.Remove(filepath.Join(target, "agents"))
	_ = os.Remove(target)
}

func acquireClientLock(target string) (*flock.Flock, string, error) {
	path := target + ".lock"
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
