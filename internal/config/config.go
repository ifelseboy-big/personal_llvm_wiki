package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"llm-wiki/internal/fsutil"
)

const (
	CurrentSchema = 3
	FileName      = "llm-wiki.toml"
)

var validInstanceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var validInstanceID = regexp.MustCompile(`^wiki_[0-9a-hjkmnp-tv-z]{26}$`)
var validTemplateName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var validTemplateVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type TemplateConfig struct {
	Name        string `toml:"name" json:"name"`
	Version     string `toml:"version" json:"version"`
	ContentPack string `toml:"content_pack" json:"content_pack"`
}

type PathsConfig struct {
	Inbox     string `toml:"inbox" json:"inbox"`
	Knowledge string `toml:"knowledge" json:"knowledge"`
	Templates string `toml:"templates" json:"templates"`
	Rules     string `toml:"rules" json:"rules"`
	Runtime   string `toml:"runtime" json:"runtime"`
}

type IndexConfig struct {
	ChunkMaxChars     int    `toml:"chunk_max_chars" json:"chunk_max_chars"`
	ChunkOverlapChars int    `toml:"chunk_overlap_chars" json:"chunk_overlap_chars"`
	ChineseTokenizer  string `toml:"chinese_tokenizer" json:"chinese_tokenizer"`
}

type SecurityConfig struct {
	FollowSymlinks      bool  `toml:"follow_symlinks" json:"follow_symlinks"`
	MaxInputBytes       int64 `toml:"max_input_bytes" json:"max_input_bytes"`
	BlockSensitiveFiles bool  `toml:"block_sensitive_files" json:"block_sensitive_files"`
}

type Instance struct {
	SchemaVersion int            `toml:"schema_version" json:"schema_version"`
	InstanceID    string         `toml:"instance_id" json:"instance_id"`
	Name          string         `toml:"name" json:"name"`
	CreatedAt     string         `toml:"created_at" json:"created_at"`
	Template      TemplateConfig `toml:"template" json:"template"`
	Paths         PathsConfig    `toml:"paths" json:"paths"`
	Index         IndexConfig    `toml:"index" json:"index"`
	Security      SecurityConfig `toml:"security" json:"security"`
	Root          string         `toml:"-" json:"root"`
	preserved     map[string]any
}

func DefaultInstance(name, id string, now time.Time) *Instance {
	return &Instance{
		SchemaVersion: CurrentSchema,
		InstanceID:    id,
		Name:          name,
		CreatedAt:     now.Format(time.RFC3339),
		Template:      TemplateConfig{Name: "unbound", Version: "0.0.0", ContentPack: "content-pack.json"},
		Paths: PathsConfig{
			Inbox: "inbox", Knowledge: "knowledge",
			Templates: "templates", Rules: "rules", Runtime: ".llm-wiki",
		},
		Index:    IndexConfig{ChunkMaxChars: 1800, ChunkOverlapChars: 180, ChineseTokenizer: "simple"},
		Security: SecurityConfig{MaxInputBytes: 50 * 1024 * 1024, BlockSensitiveFiles: true},
	}
}

func Load(root string) (*Instance, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(abs, FileName))
	if err != nil {
		return nil, err
	}
	var cfg Instance
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", FileName, err)
	}
	if err := toml.Unmarshal(b, &cfg.preserved); err != nil {
		return nil, fmt.Errorf("preserve unknown fields in %s: %w", FileName, err)
	}
	cfg.Root = filepath.Clean(abs)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Save(cfg *Instance) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	b, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	if cfg.preserved != nil {
		var known map[string]any
		if err := toml.Unmarshal(b, &known); err != nil {
			return err
		}
		merged := cloneMap(cfg.preserved)
		mergeMap(merged, known)
		b, err = toml.Marshal(merged)
		if err != nil {
			return err
		}
		cfg.preserved = merged
	}
	return fsutil.AtomicWrite(filepath.Join(cfg.Root, FileName), b, 0o600)
}

func cloneMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneMap(nested)
		} else {
			out[key] = value
		}
	}
	return out
}

func mergeMap(target, known map[string]any) {
	for key, value := range known {
		knownNested, knownIsMap := value.(map[string]any)
		currentNested, currentIsMap := target[key].(map[string]any)
		if knownIsMap && currentIsMap {
			mergeMap(currentNested, knownNested)
			continue
		}
		target[key] = value
	}
}

func (c *Instance) Validate() error {
	if c.SchemaVersion != CurrentSchema {
		return fmt.Errorf("unsupported instance schema_version %d", c.SchemaVersion)
	}
	if !validInstanceID.MatchString(c.InstanceID) || !validInstanceName.MatchString(c.Name) {
		return errors.New("instance_id or name is invalid")
	}
	if _, err := time.Parse(time.RFC3339, c.CreatedAt); err != nil {
		return errors.New("created_at must be RFC3339")
	}
	if !validTemplateName.MatchString(c.Template.Name) || !validTemplateVersion.MatchString(c.Template.Version) {
		return errors.New("template name or semantic version is invalid")
	}
	if err := validateRelativePath(c.Template.ContentPack); err != nil {
		return fmt.Errorf("template content_pack: %w", err)
	}
	paths := []string{c.Paths.Inbox, c.Paths.Knowledge, c.Paths.Templates, c.Paths.Rules, c.Paths.Runtime}
	cleanPaths := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		if err := validateRelativePath(p); err != nil {
			return err
		}
		clean := filepath.Clean(p)
		key := clean
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return fmt.Errorf("duplicate managed path %q", clean)
		}
		seen[key] = true
		cleanPaths = append(cleanPaths, clean)
	}
	for i := 0; i < len(cleanPaths); i++ {
		for j := i + 1; j < len(cleanPaths); j++ {
			if pathsOverlap(cleanPaths[i], cleanPaths[j]) {
				return fmt.Errorf("managed paths overlap: %q and %q", cleanPaths[i], cleanPaths[j])
			}
		}
	}
	contentPack := filepath.Clean(c.Template.ContentPack)
	for _, index := range []int{0, 1, 4} {
		if pathsOverlap(contentPack, cleanPaths[index]) {
			return fmt.Errorf("template content_pack overlaps protected managed path %q", cleanPaths[index])
		}
	}
	if c.Index.ChunkMaxChars < 256 || c.Index.ChunkOverlapChars < 0 || c.Index.ChunkOverlapChars >= c.Index.ChunkMaxChars {
		return errors.New("invalid index chunk configuration")
	}
	if c.Index.ChineseTokenizer != "simple" {
		return errors.New("unsupported index tokenizer")
	}
	if c.Security.FollowSymlinks {
		return errors.New("following symbolic links is not supported")
	}
	if c.Security.MaxInputBytes <= 0 {
		return errors.New("security.max_input_bytes must be positive")
	}
	return nil
}

func validateRelativePath(p string) error {
	if p == "" || strings.Contains(p, `\`) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return fmt.Errorf("managed path must be non-empty and relative: %q", p)
	}
	clean := filepath.Clean(p)
	if filepath.ToSlash(clean) != p || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("managed path escapes wiki root: %q", p)
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	rel, err := filepath.Rel(a, b)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	rel, err = filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c *Instance) Path(relative string) string { return filepath.Join(c.Root, relative) }
func (c *Instance) InboxDir() string            { return c.Path(c.Paths.Inbox) }
func (c *Instance) KnowledgeDir() string        { return c.Path(c.Paths.Knowledge) }
func (c *Instance) TemplatesDir() string        { return c.Path(c.Paths.Templates) }
func (c *Instance) RulesDir() string            { return c.Path(c.Paths.Rules) }
func (c *Instance) RuntimeDir() string          { return c.Path(c.Paths.Runtime) }
func (c *Instance) ContentPackPath() string     { return c.Path(c.Template.ContentPack) }

func Find(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(current)
	if err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		candidate := filepath.Join(current, FileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", os.ErrNotExist
}

func UserConfigPath() (string, error) {
	if p := os.Getenv("LLM_WIKI_CONFIG"); p != "" {
		return filepath.Abs(p)
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(base, "llm-wiki", "config.toml"), nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "llm-wiki", "config.toml"), nil
}
