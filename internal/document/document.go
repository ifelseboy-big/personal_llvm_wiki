package document

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/oklog/ulid/v2"
	"gopkg.in/yaml.v3"

	"llm-wiki/internal/fsutil"
)

const CurrentSchema = 3

const MaxFrontmatterBytes = 256 * 1024

const MaxMarkdownBytes = 64 * 1024 * 1024

var validIDPatterns = map[string]*regexp.Regexp{
	"wiki":  regexp.MustCompile(`^wiki_[0-9a-hjkmnp-tv-z]{26}$`),
	"inbox": regexp.MustCompile(`^inbox_[0-9a-hjkmnp-tv-z]{26}$`),
	"know":  regexp.MustCompile(`^know_[0-9a-hjkmnp-tv-z]{26}$`),
	"prm":   regexp.MustCompile(`^prm_[0-9a-hjkmnp-tv-z]{26}$`),
	"op":    regexp.MustCompile(`^op_[0-9a-hjkmnp-tv-z]{26}$`),
}

func ValidID(prefix, id string) bool {
	pattern := validIDPatterns[prefix]
	return pattern != nil && pattern.MatchString(id)
}

type LineageRef struct {
	InboxID     string `yaml:"inbox_id" json:"inbox_id"`
	PayloadHash string `yaml:"payload_hash" json:"payload_hash"`
	Source      string `yaml:"source" json:"source"`
	CapturedAt  string `yaml:"captured_at" json:"captured_at"`
}

type Metadata struct {
	SchemaVersion     int            `yaml:"schema_version" json:"schema_version"`
	ID                string         `yaml:"id" json:"id"`
	Type              string         `yaml:"type,omitempty" json:"type,omitempty"`
	Title             string         `yaml:"title,omitempty" json:"title,omitempty"`
	Status            string         `yaml:"status,omitempty" json:"status,omitempty"`
	Source            string         `yaml:"source,omitempty" json:"source,omitempty"`
	CapturedAt        string         `yaml:"captured_at,omitempty" json:"captured_at,omitempty"`
	PublishedAt       string         `yaml:"published_at,omitempty" json:"published_at,omitempty"`
	UpdatedAt         string         `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
	ContentHash       string         `yaml:"content_hash,omitempty" json:"content_hash,omitempty"`
	MediaType         string         `yaml:"media_type,omitempty" json:"media_type,omitempty"`
	OriginalName      string         `yaml:"original_name,omitempty" json:"original_name,omitempty"`
	Payload           string         `yaml:"payload,omitempty" json:"payload,omitempty"`
	PayloadHash       string         `yaml:"payload_hash,omitempty" json:"payload_hash,omitempty"`
	PayloadBytes      int64          `yaml:"payload_bytes,omitempty" json:"payload_bytes,omitempty"`
	ProcessedAt       string         `yaml:"processed_at,omitempty" json:"processed_at,omitempty"`
	KnowledgeIDs      []string       `yaml:"knowledge_ids,omitempty" json:"knowledge_ids,omitempty"`
	Lineage           []LineageRef   `yaml:"lineage,omitempty" json:"lineage,omitempty"`
	Tags              []string       `yaml:"tags,omitempty" json:"tags,omitempty"`
	Aliases           []string       `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	GovernanceVersion string         `yaml:"governance_version,omitempty" json:"governance_version,omitempty"`
	Extra             map[string]any `yaml:",inline" json:"extra,omitempty"`
}

type Document struct {
	Path     string   `json:"path"`
	Metadata Metadata `json:"metadata"`
	Body     []byte   `json:"-"`
}

func NewID(prefix string, now time.Time) (string, error) {
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return "", err
	}
	return prefix + "_" + strings.ToLower(id.String()), nil
}

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func NormalizeMarkdownBody(body []byte) []byte {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))
	return body
}

func Parse(data []byte) (Metadata, []byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !bytes.HasPrefix(data, []byte("---\n")) && !bytes.HasPrefix(data, []byte("---\r\n")) {
		return Metadata{}, NormalizeMarkdownBody(data), errors.New("markdown frontmatter is required")
	}
	normalized := NormalizeMarkdownBody(data)
	rest := normalized[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	endLen := 5
	if end < 0 {
		end = bytes.Index(rest, []byte("\n...\n"))
	}
	if end > MaxFrontmatterBytes {
		return Metadata{}, nil, errors.New("markdown frontmatter exceeds 256 KiB limit")
	}
	if end < 0 {
		return Metadata{}, nil, errors.New("unterminated markdown frontmatter")
	}
	var meta Metadata
	if err := yaml.Unmarshal(rest[:end], &meta); err != nil {
		return Metadata{}, nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	body := rest[end+endLen:]
	return meta, body, nil
}

func Read(path string) (*Document, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("managed Markdown cannot be a symbolic link: %s", path)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed Markdown is not a regular file: %s", path)
	} else if info.Size() > MaxMarkdownBytes {
		return nil, fmt.Errorf("markdown file exceeds %d byte safety limit", MaxMarkdownBytes)
	}
	if err := fsutil.EnsureSingleLink(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta, body, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Document{Path: path, Metadata: meta, Body: body}, nil
}

func Render(meta Metadata, body []byte) ([]byte, error) {
	meta.SchemaVersion = CurrentSchema
	body = NormalizeMarkdownBody(body)
	b, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(b)+len(body)+9)
	out = append(out, []byte("---\n")...)
	out = append(out, b...)
	out = append(out, []byte("---\n")...)
	out = append(out, body...)
	return out, nil
}

func Write(path string, meta Metadata, body []byte) error {
	data, err := Render(meta, body)
	if err != nil {
		return err
	}
	return AtomicWrite(path, data, 0o600)
}

func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	return fsutil.AtomicWrite(path, data, mode)
}

func (d *Document) ActualContentHash() (string, error) {
	return HashBytes(NormalizeMarkdownBody(d.Body)), nil
}

func (d *Document) ActualPayloadHash() (string, error) {
	if d.Metadata.Payload == "" {
		return "", errors.New("inbox payload path is required")
	}
	payloadPath := filepath.Join(filepath.Dir(d.Path), filepath.Clean(d.Metadata.Payload))
	payloadRoot := filepath.Join(filepath.Dir(d.Path), "payload")
	if err := fsutil.EnsureNoSymlinkPath(payloadRoot, payloadPath); err != nil {
		return "", errors.New("payload path must stay inside the inbox payload directory")
	}
	rel, err := filepath.Rel(payloadRoot, payloadPath)
	if err != nil || rel == "." || filepath.Dir(rel) != "." {
		return "", errors.New("payload path must name one file directly inside the inbox payload directory")
	}
	info, err := os.Lstat(payloadPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("inbox payload is not a regular file")
	}
	if info.Size() != d.Metadata.PayloadBytes {
		return "", fmt.Errorf("payload byte count mismatch: recorded %d actual %d", d.Metadata.PayloadBytes, info.Size())
	}
	if err := fsutil.EnsureSingleLink(payloadPath); err != nil {
		return "", err
	}
	f, err := os.Open(payloadPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (d *Document) Validate(layer string, strict bool) error {
	if d.Metadata.SchemaVersion != CurrentSchema {
		return fmt.Errorf("unsupported frontmatter schema_version %d", d.Metadata.SchemaVersion)
	}
	if d.Metadata.ID == "" || !ValidHash(d.Metadata.ContentHash) {
		return errors.New("id and content_hash are required")
	}
	prefix := map[string]string{"inbox": "inbox", "knowledge": "know"}[layer]
	if prefix == "" || !ValidID(prefix, d.Metadata.ID) {
		return fmt.Errorf("id %q does not match layer %s", d.Metadata.ID, layer)
	}
	actual, err := d.ActualContentHash()
	if err != nil {
		return err
	}
	if actual != d.Metadata.ContentHash {
		return fmt.Errorf("content hash mismatch: recorded %s actual %s", d.Metadata.ContentHash, actual)
	}
	switch layer {
	case "inbox":
		if (d.Metadata.Status != "pending" && d.Metadata.Status != "processed") || strings.TrimSpace(d.Metadata.Title) == "" ||
			strings.TrimSpace(d.Metadata.Source) == "" || strings.TrimSpace(d.Metadata.MediaType) == "" ||
			strings.TrimSpace(d.Metadata.OriginalName) == "" || !ValidHash(d.Metadata.PayloadHash) || d.Metadata.PayloadBytes < 0 {
			return errors.New("inbox status, title, source, media_type, original_name, payload hash, and byte count are required")
		}
		if _, err := time.Parse(time.RFC3339, d.Metadata.CapturedAt); err != nil {
			return errors.New("inbox captured_at must be RFC3339")
		}
		if d.Metadata.Status == "pending" || strict {
			payloadHash, err := d.ActualPayloadHash()
			if err != nil {
				return err
			}
			if payloadHash != d.Metadata.PayloadHash {
				return fmt.Errorf("payload hash mismatch: recorded %s actual %s", d.Metadata.PayloadHash, payloadHash)
			}
		}
		if d.Metadata.Status == "pending" {
			if d.Metadata.ProcessedAt != "" || len(d.Metadata.KnowledgeIDs) != 0 {
				return errors.New("pending inbox cannot have processed_at or knowledge_ids")
			}
		} else {
			if _, err := time.Parse(time.RFC3339, d.Metadata.ProcessedAt); err != nil {
				return errors.New("processed inbox requires RFC3339 processed_at")
			}
			if len(d.Metadata.KnowledgeIDs) == 0 {
				return errors.New("processed inbox requires at least one knowledge id")
			}
		}
		seenKnowledge := map[string]bool{}
		for _, id := range d.Metadata.KnowledgeIDs {
			if !ValidID("know", id) || seenKnowledge[id] {
				return errors.New("inbox knowledge_ids must contain unique knowledge ids")
			}
			seenKnowledge[id] = true
		}
	case "knowledge":
		if d.Metadata.Status != "published" || !documentTypePattern.MatchString(d.Metadata.Type) || strings.TrimSpace(d.Metadata.Title) == "" {
			return errors.New("published status, type, and title are required")
		}
		if _, err := time.Parse(time.RFC3339, d.Metadata.PublishedAt); err != nil {
			return errors.New("knowledge published_at must be RFC3339")
		}
		if _, err := time.Parse(time.RFC3339, d.Metadata.UpdatedAt); err != nil {
			return errors.New("knowledge updated_at must be RFC3339")
		}
		if strict && len(d.Metadata.Lineage) == 0 {
			return errors.New("published knowledge requires lineage")
		}
		seenLineage := map[string]bool{}
		for _, item := range d.Metadata.Lineage {
			if !ValidID("inbox", item.InboxID) || !ValidHash(item.PayloadHash) || strings.TrimSpace(item.Source) == "" || seenLineage[item.InboxID] {
				return errors.New("knowledge lineage requires unique inbox id, payload hash, source, and captured_at")
			}
			if _, err := time.Parse(time.RFC3339, item.CapturedAt); err != nil {
				return errors.New("knowledge lineage captured_at must be RFC3339")
			}
			seenLineage[item.InboxID] = true
		}
	}
	return nil
}

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var documentTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func ValidHash(value string) bool { return sha256Pattern.MatchString(value) }

func ScanMarkdown(root string) ([]*Document, []error) {
	var docs []*Document
	var problems []error
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		doc, err := Read(path)
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, problems
}

func FindByID(root, id string) (*Document, error) {
	docs, problems := ScanMarkdown(root)
	var match *Document
	for _, doc := range docs {
		if doc.Metadata.ID == id {
			if match != nil {
				return nil, fmt.Errorf("duplicate document id %s: %s and %s", id, match.Path, doc.Path)
			}
			match = doc
		}
	}
	if match != nil {
		return match, nil
	}
	if len(problems) > 0 {
		return nil, problems[0]
	}
	return nil, os.ErrNotExist
}

var dashRuns = regexp.MustCompile(`-+`)

func Slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(dashRuns.ReplaceAllString(b.String(), "-"), "-")
	if out == "" {
		return "untitled"
	}
	if len([]rune(out)) > 80 {
		out = string([]rune(out)[:80])
	}
	return strings.Trim(out, "-")
}

func KnowledgePath(knowledgeRoot string, meta Metadata) string {
	return filepath.ToSlash(filepath.Join(knowledgeRoot, meta.Type, Slug(meta.Title)+"--"+meta.ID+".md"))
}

func SafeBaseName(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._- ", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "input"
	}
	return out
}
