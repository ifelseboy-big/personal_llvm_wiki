package raw

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/vault"
)

type AddOptions struct {
	Input          string
	Name           string
	Title          string
	Type           string
	Origin         string
	AllowSensitive bool
	DryRun         bool
	Stdin          io.Reader
	Now            time.Time
	fallbackOrigin string
}

type Added struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	AssetPath   string `json:"asset_path,omitempty"`
	ContentHash string `json:"content_hash"`
	MediaType   string `json:"media_type"`
	Bytes       int64  `json:"bytes"`
}

func Add(cfg *config.Instance, opts AddOptions) ([]Added, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if opts.Input == "" {
		return nil, errors.New("input is required")
	}
	if opts.Type == "" {
		opts.Type = "note"
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	var lock *vault.Lock
	var err error
	if !opts.DryRun {
		lock, err = vault.AcquireWrite(cfg, 5*time.Second)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
	}

	if opts.Input == "-" {
		opts.fallbackOrigin = "stdin"
		name := opts.Name
		if name == "" {
			name = "stdin.md"
		}
		limited := io.LimitReader(opts.Stdin, cfg.Security.MaxInputBytes+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > cfg.Security.MaxInputBytes {
			return nil, fmt.Errorf("stdin exceeds max_input_bytes %d", cfg.Security.MaxInputBytes)
		}
		item, err := addBytes(cfg, name, data, opts)
		if err != nil {
			return nil, err
		}
		return []Added{item}, nil
	}

	abs, err := filepath.Abs(opts.Input)
	if err != nil {
		return nil, err
	}
	opts.fallbackOrigin = "file"
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symbolic link input is not allowed: %s", abs)
	}
	if info.IsDir() {
		if inside(abs, cfg.RawDir()) || inside(cfg.RawDir(), abs) {
			return nil, errors.New("cannot import a directory containing the wiki raw directory")
		}
		var files []string
		err := filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symbolic link input is not allowed: %s", path)
			}
			if !entry.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, path := range files {
			if err := preflightFile(cfg, path, opts); err != nil {
				return nil, err
			}
		}
		var added []Added
		for _, path := range files {
			item, err := addFile(cfg, path, opts)
			if err != nil {
				if !opts.DryRun {
					err = errors.Join(err, rollbackAdded(cfg, added))
				}
				return nil, err
			}
			added = append(added, item)
		}
		return added, nil
	}
	if err := preflightFile(cfg, abs, opts); err != nil {
		return nil, err
	}
	item, err := addFile(cfg, abs, opts)
	if err != nil {
		return nil, err
	}
	return []Added{item}, nil
}

func preflightFile(cfg *config.Instance, path string, opts AddOptions) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic link input is not allowed: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("input is not a regular file: %s", path)
	}
	if info.Size() > cfg.Security.MaxInputBytes {
		return fmt.Errorf("%s exceeds max_input_bytes %d", path, cfg.Security.MaxInputBytes)
	}
	if cfg.Security.BlockSensitiveFiles && vault.IsSensitiveFile(path) && !opts.AllowSensitive {
		return fmt.Errorf("sensitive file is blocked: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".md") &&
		(bytes.HasPrefix(data, []byte("---\n")) || bytes.HasPrefix(data, []byte("---\r\n"))) {
		if _, _, err := document.Parse(data); err != nil {
			return fmt.Errorf("invalid Markdown frontmatter in %s: %w", path, err)
		}
	}
	if strings.EqualFold(filepath.Ext(path), ".md") && len(data) > document.MaxMarkdownBytes-document.MaxFrontmatterBytes {
		return fmt.Errorf("Markdown input is too large to add managed frontmatter: %s", path)
	}
	return nil
}

func rollbackAdded(cfg *config.Instance, added []Added) error {
	var rollbackErr error
	for i := len(added) - 1; i >= 0; i-- {
		entryDir := filepath.Dir(filepath.Join(cfg.Root, filepath.FromSlash(added[i].Path)))
		if err := vault.EnsureInside(cfg.RawDir(), entryDir); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, entryDir); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := os.RemoveAll(entryDir); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}

func addFile(cfg *config.Instance, path string, opts AddOptions) (Added, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Added{}, err
	}
	if info.Size() > cfg.Security.MaxInputBytes {
		return Added{}, fmt.Errorf("%s exceeds max_input_bytes %d", path, cfg.Security.MaxInputBytes)
	}
	if cfg.Security.BlockSensitiveFiles && vault.IsSensitiveFile(path) && !opts.AllowSensitive {
		return Added{}, fmt.Errorf("sensitive file is blocked: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Added{}, err
	}
	return addBytes(cfg, filepath.Base(path), data, opts)
}

func addBytes(cfg *config.Instance, originalName string, data []byte, opts AddOptions) (Added, error) {
	id, err := document.NewID("raw", opts.Now)
	if err != nil {
		return Added{}, err
	}
	originalName = document.SafeBaseName(originalName)
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(originalName)))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	relDir := filepath.Join(cfg.Paths.Raw, opts.Now.Format("2006"), opts.Now.Format("01"), id)
	absDir := filepath.Join(cfg.Root, relDir)
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, absDir); err != nil {
		return Added{}, err
	}
	if !opts.DryRun {
		if _, err := os.Lstat(absDir); err == nil {
			return Added{}, fmt.Errorf("raw entry path already exists: %s", absDir)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Added{}, err
		}
	}
	base := filepath.Base(originalName)
	result := Added{ID: id, MediaType: mediaType, Bytes: int64(len(data))}

	if strings.EqualFold(filepath.Ext(base), ".md") {
		if len(data) > document.MaxMarkdownBytes-document.MaxFrontmatterBytes {
			return Added{}, errors.New("Markdown input is too large to add managed frontmatter")
		}
		body := document.NormalizeMarkdownBody(data)
		title := opts.Title
		inputMeta := document.Metadata{}
		if bytes.HasPrefix(body, []byte("---\n")) || bytes.HasPrefix(body, []byte("---\r\n")) {
			existing, parsedBody, err := document.Parse(body)
			if err != nil {
				return Added{}, err
			}
			inputMeta = existing
			body = parsedBody
			if title == "" {
				title = existing.Title
			}
			if opts.Type == "note" && existing.Type != "" {
				opts.Type = existing.Type
			}
		}
		if title == "" {
			title = firstHeading(body)
		}
		if title == "" {
			title = strings.TrimSuffix(base, filepath.Ext(base))
		}
		hash := document.HashBytes(body)
		origin := resolveOrigin(opts, inputMeta.Origin)
		meta := document.Metadata{
			SchemaVersion: document.CurrentSchema, ID: id, Type: opts.Type,
			Title: title, Status: "raw", Origin: origin,
			CapturedAt: opts.Now.Format(time.RFC3339), ContentHash: hash,
			MediaType: "text/markdown", OriginalName: originalName,
			Tags: cleanStrings(inputMeta.Tags), Aliases: cleanStrings(inputMeta.Aliases),
			Extra: copyUserProperties(inputMeta.Extra),
		}
		result.Path = filepath.ToSlash(filepath.Join(relDir, base))
		result.ContentHash = hash
		result.MediaType = "text/markdown"
		rendered, err := document.Render(meta, body)
		if err != nil {
			return Added{}, err
		}
		if len(rendered) > document.MaxMarkdownBytes {
			return Added{}, errors.New("managed Markdown exceeds the scanner safety limit after rendering")
		}
		if !opts.DryRun {
			if err := commitRawEntry(cfg, absDir, id, func(stageDir string) error {
				return document.AtomicWrite(filepath.Join(stageDir, base), rendered, 0o600)
			}); err != nil {
				return Added{}, err
			}
		}
		return result, nil
	}

	hash := document.HashBytes(data)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	sidecarName := stem + ".source.md"
	title := opts.Title
	if title == "" {
		title = stem
	}
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Type: "source", Title: title,
		Status: "raw", Origin: resolveOrigin(opts, ""), CapturedAt: opts.Now.Format(time.RFC3339),
		ContentHash: hash, MediaType: mediaType, OriginalName: originalName, Asset: base,
	}
	body := []byte(fmt.Sprintf("# %s\n\nImported binary source `%s`.\n", title, base))
	result.Path = filepath.ToSlash(filepath.Join(relDir, sidecarName))
	result.AssetPath = filepath.ToSlash(filepath.Join(relDir, base))
	result.ContentHash = hash
	if opts.DryRun {
		return result, nil
	}
	if err := commitRawEntry(cfg, absDir, id, func(stageDir string) error {
		if err := document.AtomicWrite(filepath.Join(stageDir, base), data, 0o600); err != nil {
			return err
		}
		return document.Write(filepath.Join(stageDir, sidecarName), meta, body)
	}); err != nil {
		return Added{}, err
	}
	return result, nil
}

func cleanStrings(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func copyUserProperties(properties map[string]any) map[string]any {
	out := make(map[string]any, len(properties))
	for key, value := range properties {
		if value != nil {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveOrigin(opts AddOptions, frontmatterOrigin string) string {
	if origin := strings.TrimSpace(opts.Origin); origin != "" {
		return origin
	}
	if origin := strings.TrimSpace(frontmatterOrigin); origin != "" {
		return origin
	}
	if origin := strings.TrimSpace(opts.fallbackOrigin); origin != "" {
		return origin
	}
	return "file"
}

func commitRawEntry(cfg *config.Instance, targetDir, id string, write func(stageDir string) error) error {
	txnRoot := filepath.Join(cfg.RuntimeDir(), "transactions", id+"-raw")
	stageDir := filepath.Join(txnRoot, "entry")
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, stageDir); err != nil {
		return err
	}
	if _, err := os.Lstat(txnRoot); err == nil {
		return fmt.Errorf("raw staging path already exists: %s", txnRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(txnRoot)
	if err := write(stageDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(targetDir); err == nil {
		return fmt.Errorf("raw entry path appeared during commit: %s", targetDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(stageDir, targetDir)
}

func firstHeading(body []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func inside(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func List(cfg *config.Instance) ([]*document.Document, []error) {
	docs, problems := document.ScanMarkdown(cfg.RawDir())
	for _, doc := range docs {
		if err := doc.Validate("raw", false); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", doc.Path, err))
		}
	}
	return docs, problems
}

// ReferenceMap derives raw-to-knowledge usage only from published Markdown.
// It deliberately does not use SQLite so a disposable cache cannot decide the
// capture backlog.
func ReferenceMap(cfg *config.Instance) (map[string][]string, error) {
	docs, problems := document.ScanMarkdown(cfg.KnowledgeDir())
	if len(problems) > 0 {
		return nil, problems[0]
	}
	references := map[string][]string{}
	seenKnowledge := map[string]bool{}
	for _, doc := range docs {
		if err := doc.Validate("knowledge", cfg.Publish.RequireSources); err != nil {
			return nil, fmt.Errorf("%s: %w", doc.Path, err)
		}
		if seenKnowledge[doc.Metadata.ID] {
			return nil, fmt.Errorf("duplicate knowledge id %s", doc.Metadata.ID)
		}
		seenKnowledge[doc.Metadata.ID] = true
		for _, source := range doc.Metadata.Sources {
			references[source.ID] = append(references[source.ID], doc.Metadata.ID)
		}
	}
	for id := range references {
		sort.Strings(references[id])
	}
	return references, nil
}

func Show(cfg *config.Instance, id string) (*document.Document, error) {
	if !strings.HasPrefix(id, "raw_") {
		return nil, errors.New("raw id must start with raw_")
	}
	doc, err := document.FindByID(cfg.RawDir(), id)
	if err != nil {
		return nil, err
	}
	if err := doc.Validate("raw", false); err != nil {
		return nil, err
	}
	return doc, nil
}
