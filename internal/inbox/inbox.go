package inbox

import (
	"bytes"
	"encoding/json"
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

const ItemFile = "item.md"

var ErrInputRejected = errors.New("inbox input rejected")

type AddOptions struct {
	Input          string
	Name           string
	Title          string
	Source         string
	NoteFile       string
	BatchManifest  string
	AllowSensitive bool
	DryRun         bool
	Stdin          io.Reader
	Now            time.Time
}

type BatchManifest struct {
	SchemaVersion int         `json:"schema_version"`
	Items         []BatchItem `json:"items"`
}

type BatchItem struct {
	Input    string `json:"input"`
	NoteFile string `json:"note_file"`
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	Source   string `json:"source,omitempty"`
}

type Added struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ItemPath    string `json:"item_path"`
	PayloadPath string `json:"payload_path"`
	ItemHash    string `json:"item_hash"`
	PayloadHash string `json:"payload_hash"`
	MediaType   string `json:"media_type"`
	Bytes       int64  `json:"bytes"`
}

type prepared struct {
	id        string
	original  string
	title     string
	source    string
	mediaType string
	payload   []byte
	body      []byte
	meta      document.Metadata
	relDir    string
	itemBytes []byte
	result    Added
}

type CleanOptions struct {
	IDs                   []string
	Processed             bool
	Yes                   bool
	DryRun                bool
	ActiveInboxIDs        map[string]bool
	ResolveActiveInboxIDs func() (map[string]bool, error)
	Now                   time.Time
}

type CleanResult struct {
	IDs     []string `json:"ids"`
	Paths   []string `json:"paths"`
	DryRun  bool     `json:"dry_run"`
	Deleted int      `json:"deleted"`
}

func Add(cfg *config.Instance, opts AddOptions) ([]Added, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
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
	items, err := inputItems(cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInputRejected, err)
	}
	preparedItems := make([]prepared, 0, len(items))
	seenInputs := map[string]bool{}
	for _, item := range items {
		key := item.Input
		if key != "-" {
			key, err = filepath.Abs(key)
			if err != nil {
				return nil, err
			}
		}
		if seenInputs[key] {
			return nil, fmt.Errorf("duplicate batch input %q", item.Input)
		}
		seenInputs[key] = true
		entry, err := prepare(cfg, opts, item)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInputRejected, err)
		}
		preparedItems = append(preparedItems, entry)
	}
	if opts.DryRun {
		return addedResults(preparedItems), nil
	}
	if err := commit(cfg, preparedItems, opts.Now); err != nil {
		return nil, err
	}
	return addedResults(preparedItems), nil
}

func inputItems(cfg *config.Instance, opts AddOptions) ([]BatchItem, error) {
	if opts.BatchManifest != "" {
		if opts.Input != "" || opts.NoteFile != "" || opts.Name != "" || opts.Title != "" || opts.Source != "" {
			return nil, errors.New("batch manifest cannot be combined with single-input options")
		}
		data, err := readRegularLimited(opts.BatchManifest, cfg.Security.MaxInputBytes, true)
		if err != nil {
			return nil, err
		}
		var manifest BatchManifest
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&manifest); err != nil {
			return nil, fmt.Errorf("parse batch manifest: %w", err)
		}
		if manifest.SchemaVersion != 1 || len(manifest.Items) == 0 {
			return nil, errors.New("batch manifest schema_version 1 and non-empty items are required")
		}
		base := filepath.Dir(opts.BatchManifest)
		for i := range manifest.Items {
			if manifest.Items[i].Input == "" || manifest.Items[i].NoteFile == "" {
				return nil, fmt.Errorf("batch item %d requires input and note_file", i)
			}
			if !filepath.IsAbs(manifest.Items[i].Input) {
				manifest.Items[i].Input = filepath.Join(base, manifest.Items[i].Input)
			}
			if !filepath.IsAbs(manifest.Items[i].NoteFile) {
				manifest.Items[i].NoteFile = filepath.Join(base, manifest.Items[i].NoteFile)
			}
		}
		return manifest.Items, nil
	}
	if opts.Input == "" {
		return nil, errors.New("input or --batch-manifest is required")
	}
	if opts.Input != "-" {
		info, err := os.Lstat(opts.Input)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, errors.New("directory add requires --batch-manifest")
		}
	}
	return []BatchItem{{Input: opts.Input, NoteFile: opts.NoteFile, Name: opts.Name, Title: opts.Title, Source: opts.Source}}, nil
}

func prepare(cfg *config.Instance, opts AddOptions, item BatchItem) (prepared, error) {
	var payload []byte
	var original string
	var err error
	if item.Input == "-" {
		if strings.TrimSpace(item.Name) == "" {
			return prepared{}, errors.New("stdin input requires --name")
		}
		payload, err = readLimited(opts.Stdin, cfg.Security.MaxInputBytes)
		original = item.Name
		if item.Source == "" {
			item.Source = "stdin"
		}
	} else {
		if cfg.Security.BlockSensitiveFiles && vault.IsSensitiveFile(item.Input) && !opts.AllowSensitive {
			return prepared{}, fmt.Errorf("sensitive file is blocked: %s", item.Input)
		}
		payload, err = readRegularLimited(item.Input, cfg.Security.MaxInputBytes, true)
		original = filepath.Base(item.Input)
		if item.Name != "" {
			original = item.Name
		}
		if item.Source == "" {
			item.Source = "file"
		}
	}
	if err != nil {
		return prepared{}, err
	}
	original = document.SafeBaseName(original)
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(original)))
	if mediaType == "" {
		mediaType = http.DetectContentType(payload)
	}
	body := []byte(nil)
	if item.NoteFile != "" {
		body, err = readRegularLimited(item.NoteFile, document.MaxMarkdownBytes-document.MaxFrontmatterBytes, true)
		if err != nil {
			return prepared{}, fmt.Errorf("read preliminary note: %w", err)
		}
		body = document.NormalizeMarkdownBody(body)
		if bytes.HasPrefix(body, []byte("---\n")) || bytes.HasPrefix(body, []byte("---\r\n")) {
			var noteMeta document.Metadata
			noteMeta, body, err = document.Parse(body)
			if err != nil {
				return prepared{}, fmt.Errorf("parse preliminary note: %w", err)
			}
			if item.Title == "" {
				item.Title = noteMeta.Title
			}
		}
	}
	if item.Title == "" {
		item.Title = firstHeading(body)
	}
	if item.Title == "" {
		item.Title = strings.TrimSuffix(original, filepath.Ext(original))
	}
	if len(body) == 0 {
		body = []byte(fmt.Sprintf("# %s\n\nPending inbox item. The original input is preserved in `%s`.\n", item.Title, filepath.ToSlash(filepath.Join("payload", original))))
	}
	id, err := document.NewID("inbox", opts.Now)
	if err != nil {
		return prepared{}, err
	}
	relDir := filepath.Join(cfg.Paths.Inbox, opts.Now.Format("2006"), opts.Now.Format("01"), id)
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema,
		ID:            id, Title: item.Title, Status: "pending", Source: item.Source,
		CapturedAt: opts.Now.Format(time.RFC3339), ContentHash: document.HashBytes(body),
		MediaType: mediaType, OriginalName: original,
		Payload: filepath.ToSlash(filepath.Join("payload", original)), PayloadHash: document.HashBytes(payload), PayloadBytes: int64(len(payload)),
	}
	itemBytes, err := document.Render(meta, body)
	if err != nil {
		return prepared{}, err
	}
	result := Added{
		ID: id, Status: "pending", ItemPath: filepath.ToSlash(filepath.Join(relDir, ItemFile)),
		PayloadPath: filepath.ToSlash(filepath.Join(relDir, "payload", original)), ItemHash: document.HashBytes(itemBytes),
		PayloadHash: meta.PayloadHash, MediaType: mediaType, Bytes: int64(len(payload)),
	}
	return prepared{id: id, original: original, title: item.Title, source: item.Source, mediaType: mediaType, payload: payload, body: body, meta: meta, relDir: relDir, itemBytes: itemBytes, result: result}, nil
}

func commit(cfg *config.Instance, items []prepared, now time.Time) error {
	opID, err := document.NewID("op", now)
	if err != nil {
		return err
	}
	txnRoot := filepath.Join(cfg.RuntimeDir(), "transactions", opID+"-inbox-add")
	stageRoot := filepath.Join(txnRoot, "stage")
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, stageRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(txnRoot)
	for _, item := range items {
		target := filepath.Join(cfg.Root, item.relDir)
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("inbox entry path already exists: %s", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		stage := filepath.Join(stageRoot, item.id)
		if err := os.MkdirAll(filepath.Join(stage, "payload"), 0o700); err != nil {
			return err
		}
		if err := document.AtomicWrite(filepath.Join(stage, "payload", item.original), item.payload, 0o600); err != nil {
			return err
		}
		if err := document.AtomicWrite(filepath.Join(stage, ItemFile), item.itemBytes, 0o600); err != nil {
			return err
		}
	}
	committed := []string{}
	for _, item := range items {
		target := filepath.Join(cfg.Root, item.relDir)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			rollbackDirs(committed)
			return err
		}
		if err := os.Rename(filepath.Join(stageRoot, item.id), target); err != nil {
			rollbackDirs(committed)
			return err
		}
		committed = append(committed, target)
	}
	return nil
}

func rollbackDirs(paths []string) {
	for i := len(paths) - 1; i >= 0; i-- {
		_ = os.RemoveAll(paths[i])
	}
}

func addedResults(items []prepared) []Added {
	out := make([]Added, 0, len(items))
	for _, item := range items {
		out = append(out, item.result)
	}
	return out
}

func List(cfg *config.Instance, status string) ([]*document.Document, []error) {
	if status != "" && status != "pending" && status != "processed" {
		return nil, []error{fmt.Errorf("invalid inbox status %q", status)}
	}
	var docs []*document.Document
	var problems []error
	err := filepath.WalkDir(cfg.InboxDir(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			problems = append(problems, walkErr)
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			problems = append(problems, fmt.Errorf("symbolic link is not allowed: %s", path))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != ItemFile {
			return nil
		}
		doc, err := document.Read(path)
		if err == nil {
			err = doc.Validate("inbox", false)
		}
		if err == nil {
			captured, _ := time.Parse(time.RFC3339, doc.Metadata.CapturedAt)
			rel, relErr := filepath.Rel(cfg.InboxDir(), path)
			expected := filepath.ToSlash(filepath.Join(captured.Format("2006"), captured.Format("01"), doc.Metadata.ID, ItemFile))
			if relErr != nil || filepath.ToSlash(rel) != expected {
				err = fmt.Errorf("inbox item path is not canonical: expected %s", expected)
			}
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, err))
			return nil
		}
		if status == "" || doc.Metadata.Status == status {
			docs = append(docs, doc)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		problems = append(problems, err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return docs, problems
}

func Show(cfg *config.Instance, id string) (*document.Document, error) {
	if !document.ValidID("inbox", id) {
		return nil, errors.New("invalid inbox id")
	}
	docs, problems := List(cfg, "")
	for _, doc := range docs {
		if doc.Metadata.ID == id {
			return doc, nil
		}
	}
	if len(problems) > 0 {
		return nil, problems[0]
	}
	return nil, os.ErrNotExist
}

func ProcessedPayloadWarnings(cfg *config.Instance) []string {
	docs, _ := List(cfg, "processed")
	warnings := []string{}
	for _, doc := range docs {
		actual, err := doc.ActualPayloadHash()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("processed inbox %s payload cannot be verified: %v", doc.Metadata.ID, err))
			continue
		}
		if actual != doc.Metadata.PayloadHash {
			warnings = append(warnings, fmt.Sprintf("processed inbox %s payload hash changed", doc.Metadata.ID))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func Clean(cfg *config.Instance, opts CleanOptions) (*CleanResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if opts.Processed && len(opts.IDs) != 0 {
		return nil, errors.New("explicit inbox ids and --processed cannot be combined")
	}
	if !opts.Processed && len(opts.IDs) == 0 {
		return nil, errors.New("explicit inbox ids or --processed is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if !opts.DryRun && !opts.Yes {
		return nil, errors.New("inbox clean requires --yes")
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
	active := opts.ActiveInboxIDs
	if opts.ResolveActiveInboxIDs != nil {
		active, err = opts.ResolveActiveInboxIDs()
		if err != nil {
			return nil, err
		}
	}
	docs, problems := List(cfg, "")
	if len(problems) > 0 {
		return nil, problems[0]
	}
	byID := map[string]*document.Document{}
	for _, doc := range docs {
		byID[doc.Metadata.ID] = doc
	}
	ids := append([]string(nil), opts.IDs...)
	if opts.Processed {
		for id, doc := range byID {
			if doc.Metadata.Status == "processed" {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	ids = unique(ids)
	paths := []string{}
	dirs := []string{}
	for _, id := range ids {
		if !document.ValidID("inbox", id) {
			return nil, fmt.Errorf("invalid inbox id %q", id)
		}
		doc := byID[id]
		if doc == nil {
			return nil, fmt.Errorf("inbox %s: %w", id, os.ErrNotExist)
		}
		if doc.Metadata.Status != "processed" {
			return nil, fmt.Errorf("inbox %s is %s, expected processed", id, doc.Metadata.Status)
		}
		if err := doc.Validate("inbox", true); err != nil {
			return nil, fmt.Errorf("inbox %s failed cleanup integrity validation: %w", id, err)
		}
		if active[id] {
			return nil, fmt.Errorf("inbox %s is referenced by an active promotion", id)
		}
		dir := filepath.Dir(doc.Path)
		if filepath.Base(dir) != id {
			return nil, fmt.Errorf("inbox %s path is not canonical", id)
		}
		if err := vault.EnsureInside(cfg.InboxDir(), dir); err != nil {
			return nil, err
		}
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, dir); err != nil {
			return nil, err
		}
		entryPaths, err := validatedEntryPaths(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range entryPaths {
			rel, _ := filepath.Rel(cfg.Root, path)
			paths = append(paths, filepath.ToSlash(rel))
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(paths)
	result := &CleanResult{IDs: ids, Paths: paths, DryRun: opts.DryRun}
	if opts.DryRun || len(ids) == 0 {
		return result, nil
	}
	opID, err := document.NewID("op", opts.Now)
	if err != nil {
		return nil, err
	}
	trash := filepath.Join(cfg.RuntimeDir(), "transactions", opID+"-inbox-clean")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return nil, err
	}
	moved := []string{}
	for i, dir := range dirs {
		target := filepath.Join(trash, ids[i])
		if err := os.Rename(dir, target); err != nil {
			for j := len(moved) - 1; j >= 0; j-- {
				_ = os.Rename(filepath.Join(trash, moved[j]), dirs[j])
			}
			return nil, err
		}
		moved = append(moved, ids[i])
	}
	if err := os.RemoveAll(trash); err != nil {
		return nil, err
	}
	result.Deleted = len(ids)
	return result, nil
}

func validatedEntryPaths(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("inbox entry contains a non-regular file: %s", path)
		}
		if err := fsutil.EnsureSingleLink(path); err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func readRegularLimited(path string, limit int64, rejectHardlink bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input is not a regular non-symlink file: %s", path)
	}
	if rejectHardlink {
		if err := fsutil.EnsureSingleLink(path); err != nil {
			return nil, err
		}
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("input exceeds %d byte limit: %s", limit, path)
	}
	return os.ReadFile(path)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input exceeds %d byte limit", limit)
	}
	return data, nil
}

func firstHeading(body []byte) string {
	for _, line := range strings.Split(string(document.NormalizeMarkdownBody(body)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func unique(items []string) []string {
	out := items[:0]
	for _, item := range items {
		if len(out) == 0 || out[len(out)-1] != item {
			out = append(out, item)
		}
	}
	return out
}
