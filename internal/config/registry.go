package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"

	"llm-wiki/internal/fsutil"
)

type RegisteredWiki struct {
	InstanceID string `toml:"instance_id" json:"instance_id"`
	Path       string `toml:"path" json:"path"`
}

type Registry struct {
	SchemaVersion int                       `toml:"schema_version" json:"schema_version"`
	Default       string                    `toml:"default,omitempty" json:"default,omitempty"`
	Wikis         map[string]RegisteredWiki `toml:"wikis" json:"wikis"`
}

func LoadRegistry() (*Registry, string, error) {
	path, err := UserConfigPath()
	if err != nil {
		return nil, "", err
	}
	r := &Registry{SchemaVersion: CurrentSchema, Wikis: map[string]RegisteredWiki{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if err := toml.Unmarshal(b, r); err != nil {
		return nil, path, err
	}
	if r.SchemaVersion != CurrentSchema {
		return nil, path, fmt.Errorf("unsupported registry schema_version %d", r.SchemaVersion)
	}
	if r.Wikis == nil {
		r.Wikis = map[string]RegisteredWiki{}
	}
	if err := validateRegistry(r); err != nil {
		return nil, path, err
	}
	return r, path, nil
}

func SaveRegistry(r *Registry, path string) error {
	if err := validateRegistry(r); err != nil {
		return err
	}
	b, err := toml.Marshal(r)
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, b, 0o600)
}

func Register(cfg *Instance, alias string, makeDefault bool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if alias == "" {
		alias = cfg.Name
	}
	if !validInstanceName.MatchString(alias) {
		return fmt.Errorf("invalid wiki alias %q", alias)
	}
	path, err := UserConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	registryLock := flock.New(path + ".lock")
	lockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locked, err := registryLock.TryLockContext(lockContext, 50*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("user wiki registry is locked by another process")
		}
		return err
	}
	if !locked {
		return errors.New("user wiki registry is locked by another process")
	}
	defer registryLock.Unlock()
	r, _, err := LoadRegistry()
	if err != nil {
		return err
	}
	if existing, ok := r.Wikis[alias]; ok && existing.InstanceID != cfg.InstanceID {
		return fmt.Errorf("wiki alias %q already belongs to another instance", alias)
	}
	for name, existing := range r.Wikis {
		if existing.InstanceID == cfg.InstanceID && name != alias {
			delete(r.Wikis, name)
			if r.Default == name {
				r.Default = alias
			}
		}
	}
	r.Wikis[alias] = RegisteredWiki{InstanceID: cfg.InstanceID, Path: cfg.Root}
	if makeDefault || r.Default == "" {
		r.Default = alias
	}
	return SaveRegistry(r, path)
}

func validateRegistry(r *Registry) error {
	if r.SchemaVersion != CurrentSchema {
		return fmt.Errorf("unsupported registry schema_version %d", r.SchemaVersion)
	}
	seenIDs := map[string]string{}
	for alias, wiki := range r.Wikis {
		if !validInstanceName.MatchString(alias) || !validInstanceID.MatchString(wiki.InstanceID) || !filepath.IsAbs(wiki.Path) {
			return fmt.Errorf("invalid registered wiki %q", alias)
		}
		if previous := seenIDs[wiki.InstanceID]; previous != "" && previous != alias {
			return fmt.Errorf("instance %s is registered more than once", wiki.InstanceID)
		}
		seenIDs[wiki.InstanceID] = alias
	}
	if r.Default != "" {
		if _, ok := r.Wikis[r.Default]; !ok {
			return fmt.Errorf("default wiki alias %q is missing", r.Default)
		}
	}
	return nil
}

func Resolve(arg, cwd string) (*Instance, error) {
	if arg != "" {
		pathLike := filepath.IsAbs(arg) || arg == "." || arg == ".." || strings.ContainsAny(arg, `/\`)
		if pathLike {
			if root, err := Find(arg); err == nil {
				return Load(root)
			}
			return nil, fmt.Errorf("%s does not contain %s", arg, FileName)
		}
		r, _, err := LoadRegistry()
		if err != nil {
			return nil, err
		}
		if wiki, ok := r.Wikis[arg]; ok {
			return Load(wiki.Path)
		}
		if info, statErr := os.Stat(arg); statErr == nil && info.IsDir() {
			if root, findErr := Find(arg); findErr == nil {
				return Load(root)
			}
			return nil, fmt.Errorf("%s does not contain %s", arg, FileName)
		}
		return nil, fmt.Errorf("wiki %q is neither a path nor a registered alias", arg)
	}
	if root, err := Find(cwd); err == nil {
		return Load(root)
	}
	r, _, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	if r.Default == "" {
		return nil, errors.New("no wiki found and no default wiki is registered")
	}
	wiki, ok := r.Wikis[r.Default]
	if !ok {
		return nil, fmt.Errorf("default wiki alias %q is missing", r.Default)
	}
	return Load(filepath.Clean(wiki.Path))
}
