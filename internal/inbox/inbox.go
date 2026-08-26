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
	"unicode/utf8"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/vault"
)

const (
	AttachmentsDir     = "attachments"
	BatchSchemaVersion = 1
)

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
	files  map[string][]byte
	result Added
}

type CleanOptions struct {
	IDs       []string
	Processed bool
	Yes       bool
	DryRun    bool
	Now       time.Time
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
	reserved := map[string]bool{}
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
		entry, err := prepare(cfg, opts, item, reserved)
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
		if manifest.SchemaVersion != BatchSchemaVersion || len(manifest.Items) == 0 {
			return nil, fmt.Errorf("batch manifest schema_version %d and non-empty items are required", BatchSchemaVersion)
		}
		base := filepath.Dir(opts.BatchManifest)
		for i := range manifest.Items {
			if manifest.Items[i].Input == "" {
				return nil, fmt.Errorf("batch item %d requires input", i)
			}
			if !filepath.IsAbs(manifest.Items[i].Input) {
				manifest.Items[i].Input = filepath.Join(base, manifest.Items[i].Input)
			}
			if manifest.Items[i].NoteFile != "" && !filepath.IsAbs(manifest.Items[i].NoteFile) {
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

func prepare(cfg *config.Instance, opts AddOptions, item BatchItem, reserved map[string]bool) (prepared, error) {
	item.Title, item.Source = strings.TrimSpace(item.Title), strings.TrimSpace(item.Source)
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
	var noteMeta document.Metadata
	inline := item.NoteFile == "" && utf8.Valid(payload) && !bytes.ContainsRune(payload, 0) &&
		(strings.HasPrefix(mediaType, "text/") || strings.EqualFold(filepath.Ext(original), ".md"))
	if inline {
		body = document.NormalizeMarkdownBody(payload)
	} else if item.NoteFile != "" {
		body, err = readRegularLimited(item.NoteFile, document.MaxMarkdownBytes-document.MaxFrontmatterBytes, true)
		if err != nil {
			return prepared{}, fmt.Errorf("read optional note: %w", err)
		}
	}
	body = document.NormalizeMarkdownBody(body)
	if item.NoteFile != "" && bytes.HasPrefix(body, []byte("---\n")) {
		noteMeta, body, err = document.Parse(body)
		if err != nil {
			return prepared{}, fmt.Errorf("parse input note: %w", err)
		}
		if noteMeta.ID != "" || noteMeta.SchemaVersion != 0 {
			return prepared{}, errors.New("capture input must not contain managed identity or schema fields")
		}
		if item.Title == "" {
			item.Title = noteMeta.Title
		}
	}
	if item.Title == "" {
		item.Title = firstHeading(body)
	}
	if item.Title == "" {
		item.Title = strings.TrimSuffix(original, filepath.Ext(original))
	}
	if strings.TrimSpace(item.Title) == "" {
		item.Title = "untitled"
	}
	id, err := document.NewID("inbox", opts.Now)
	if err != nil {
		return prepared{}, err
	}
	itemRel, err := availablePath(cfg, cfg.Paths.Inbox, opts.Now.Format("2006-01-02")+"-"+readableName(item.Title)+".md", reserved)
	if err != nil {
		return prepared{}, err
	}
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: id, Title: item.Title, Status: "pending", Source: item.Source,
		CapturedAt: opts.Now.Format(time.RFC3339), MediaType: mediaType, OriginalName: original,
		Tags: noteMeta.Tags, Aliases: noteMeta.Aliases, Extra: noteMeta.Extra,
	}
	files := map[string][]byte{}
	payloadRel := itemRel
	payloadHash := ""
	if !inline {
		payloadRel, err = availablePath(cfg, filepath.Join(cfg.Paths.Inbox, AttachmentsDir), readableName(original), reserved)
		if err != nil {
			return prepared{}, err
		}
		payloadLocal, _ := filepath.Rel(cfg.Paths.Inbox, payloadRel)
		meta.Payload = filepath.ToSlash(payloadLocal)
		files[payloadRel] = payload
		payloadHash = document.HashBytes(payload)
		if len(body) == 0 {
			body = []byte(fmt.Sprintf("# %s\n\n[%s](<%s>)\n", item.Title, original, meta.Payload))
		}
	}
	itemBytes, err := document.Render(meta, body)
	if err != nil {
		return prepared{}, err
	}
	if len(itemBytes) > document.MaxMarkdownBytes {
		return prepared{}, errors.New("inbox note exceeds Markdown size limit")
	}
	files[itemRel] = itemBytes
	if inline {
		payloadHash = document.HashBytes(document.NormalizeMarkdownBody(body))
	}
	return prepared{files: files, result: Added{
		ID: id, Status: "pending", ItemPath: filepath.ToSlash(itemRel), PayloadPath: filepath.ToSlash(payloadRel),
		ItemHash: document.HashBytes(itemBytes), PayloadHash: payloadHash, MediaType: mediaType, Bytes: int64(len(payload)),
	}}, nil
}

func readableName(name string) string {
	name = document.SafeBaseName(strings.NewReplacer("/", "_", "\\", "_").Replace(name))
	// Leave room for collision suffixes on filesystems with 255-byte components.
	for len(name) > 180 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

func availablePath(cfg *config.Instance, dir, name string, reserved map[string]bool) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		rel := filepath.Join(dir, candidate)
		path := filepath.Join(cfg.Root, rel)
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
			return "", err
		}
		if reserved[rel] {
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			reserved[rel] = true
			return rel, nil
		} else if err != nil {
			return "", err
		}
	}
}

func commit(cfg *config.Instance, items []prepared, now time.Time) error {
	opID, err := document.NewID("op", now)
	if err != nil {
		return err
	}
	txnRoot := filepath.Join(cfg.RuntimeDir(), "transactions", opID+"-inbox-add")
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, txnRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(txnRoot, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(txnRoot)
	files := map[string][]byte{}
	var paths []string
	for _, item := range items {
		for path, data := range item.files {
			paths = append(paths, path)
			files[path] = data
		}
	}
	sort.Strings(paths)
	for i, rel := range paths {
		if err := document.AtomicWrite(filepath.Join(txnRoot, fmt.Sprint(i)), files[rel], 0o600); err != nil {
			return err
		}
	}
	var committed, createdDirs []string
	rollback := func() {
		for i := len(committed) - 1; i >= 0; i-- {
			_ = os.Remove(committed[i])
		}
		for i := len(createdDirs) - 1; i >= 0; i-- {
			_ = os.Remove(createdDirs[i]) // Only remove an empty directory created by this capture.
		}
	}
	for i, rel := range paths {
		target := filepath.Join(cfg.Root, rel)
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
			rollback()
			return err
		}
		dir := filepath.Dir(target)
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(dir, 0o700); err != nil {
				rollback()
				return err
			}
			createdDirs = append(createdDirs, dir)
		}
		stage := filepath.Join(txnRoot, fmt.Sprint(i))
		// Link then unlink installs a complete file without overwriting a concurrently created target.
		if err := os.Link(stage, target); err != nil {
			rollback()
			return err
		}
		committed = append(committed, target)
		if err := os.Remove(stage); err != nil {
			rollback()
			return err
		}
	}
	return nil
}

func addedResults(items []prepared) []Added {
	out := make([]Added, 0, len(items))
	for _, item := range items {
		out = append(out, item.result)
	}
	return out
}

// List reads registered notes at any path inside Inbox. Unregistered material is
// allowed in the workspace; it acquires an identity through inbox add before publication.
func List(cfg *config.Instance, status string) ([]*document.Document, []error) {
	if status != "" && status != "pending" && status != "processed" {
		return nil, []error{fmt.Errorf("invalid inbox status %q", status)}
	}
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, []error{err}
	}
	var docs []*document.Document
	var problems []error
	seen := map[string]string{}
	err := filepath.WalkDir(cfg.InboxDir(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if !errors.Is(walkErr, os.ErrNotExist) {
				problems = append(problems, walkErr)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			problems = append(problems, fmt.Errorf("symbolic link is not allowed: %s", path))
			return nil
		}
		if entry.IsDir() {
			// Both shared attachments and existing per-item payload directories
			// contain raw input, which may itself have Markdown frontmatter.
			if path == filepath.Join(cfg.InboxDir(), AttachmentsDir) || entry.Name() == "payload" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		doc, err := document.Read(path)
		if errors.Is(err, document.ErrFrontmatterRequired) {
			return nil
		}
		if err == nil && doc.Metadata.ID == "" && doc.Metadata.SchemaVersion == 0 {
			return nil
		}
		if err == nil {
			err = doc.Validate("inbox", false)
		}
		if err == nil {
			if prior, exists := seen[doc.Metadata.ID]; exists {
				err = fmt.Errorf("duplicate inbox id %s: %s and %s", doc.Metadata.ID, prior, path)
			}
			seen[doc.Metadata.ID] = path
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
	if err != nil {
		problems = append(problems, err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return docs, problems
}

// PayloadPath resolves an optional attachment relative to its note, for both
// existing item/payload bundles and new date-named notes. No guessed paths.
func PayloadPath(cfg *config.Instance, doc *document.Document) (string, error) {
	path := doc.Path
	if doc.Metadata.Payload != "" {
		rel := doc.Metadata.Payload
		if filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
			return "", errors.New("inbox attachment must be a note-relative path inside Inbox")
		}
		for _, part := range strings.Split(rel, "/") {
			if part == ".." || part == "." || part == "" {
				return "", errors.New("inbox attachment path contains an invalid component")
			}
		}
		path = filepath.Join(filepath.Dir(doc.Path), filepath.FromSlash(rel))
		if path == doc.Path {
			return "", errors.New("omit payload for a self-contained inbox note")
		}
	}
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
		return "", err
	}
	if err := vault.EnsureInside(cfg.InboxDir(), path); err != nil {
		return "", err
	}
	return path, nil
}

// Snapshot computes current hashes without persisting or comparing capture-time
// hashes. Only Promotion keeps these values as a baseline that must not drift.
func Snapshot(cfg *config.Instance, doc *document.Document) error {
	path, err := PayloadPath(cfg, doc)
	if err != nil {
		return err
	}
	doc.PayloadPath = path
	if doc.Metadata.Payload == "" {
		doc.PayloadHash = document.HashBytes(document.NormalizeMarkdownBody(doc.Body))
		return nil
	}
	data, err := readRegularLimited(path, cfg.Security.MaxInputBytes, true)
	if err != nil {
		return err
	}
	doc.PayloadHash = document.HashBytes(data)
	return nil
}

func Show(cfg *config.Instance, id string) (*document.Document, error) {
	if !document.ValidID("inbox", id) {
		return nil, errors.New("invalid inbox id")
	}
	docs, problems := List(cfg, "")
	if len(problems) > 0 {
		return nil, problems[0]
	}
	for _, doc := range docs {
		if doc.Metadata.ID == id {
			if err := Snapshot(cfg, doc); err != nil {
				return nil, err
			}
			return doc, nil
		}
	}
	return nil, os.ErrNotExist
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
	files := []string{}
	for _, id := range ids {
		if !document.ValidID("inbox", id) {
			return nil, fmt.Errorf("invalid inbox id %q", id)
		}
		doc := byID[id]
		if doc == nil {
			return nil, fmt.Errorf("inbox %s: %w", id, os.ErrNotExist)
		}
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, doc.Path); err != nil {
			return nil, err
		}
		if err := vault.EnsureInside(cfg.InboxDir(), doc.Path); err != nil {
			return nil, err
		}
		if err := fsutil.EnsureSingleLink(doc.Path); err != nil {
			return nil, err
		}
		// Attachments may be shared or manually edited. Cleaning a note never
		// recursively removes its directory or follows its attachment references.
		rel, _ := filepath.Rel(cfg.Root, doc.Path)
		paths = append(paths, filepath.ToSlash(rel))
		files = append(files, doc.Path)
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
	for i, file := range files {
		target := filepath.Join(trash, ids[i])
		if err := os.Rename(file, target); err != nil {
			for j := len(moved) - 1; j >= 0; j-- {
				_ = os.Rename(filepath.Join(trash, moved[j]), files[j])
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
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f, limit)
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
