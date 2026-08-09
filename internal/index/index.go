package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/sqlite3simple"
	"llm-wiki/internal/vault"
)

const (
	SchemaVersion       = 3
	QueryPlannerVersion = "1"
)

type RebuildResult struct {
	Path      string         `json:"path"`
	Documents map[string]int `json:"documents"`
	Chunks    int            `json:"chunks"`
	Sources   int            `json:"source_links"`
	RebuiltAt string         `json:"rebuilt_at"`
}

type Status struct {
	Exists              bool           `json:"exists"`
	SchemaVersion       int            `json:"schema_version,omitempty"`
	BuiltAt             string         `json:"built_at,omitempty"`
	Tokenizer           string         `json:"tokenizer,omitempty"`
	TokenizerVersion    string         `json:"tokenizer_version,omitempty"`
	TokenizerCommit     string         `json:"tokenizer_commit,omitempty"`
	QueryPlannerVersion string         `json:"query_planner_version,omitempty"`
	Documents           map[string]int `json:"documents,omitempty"`
	Chunks              int            `json:"chunks,omitempty"`
	Path                string         `json:"path"`
}

type UpdateResult struct {
	Mode        string `json:"mode"`
	DryRun      bool   `json:"dry_run"`
	Added       int    `json:"added"`
	Changed     int    `json:"changed"`
	Deleted     int    `json:"deleted"`
	Unchanged   int    `json:"unchanged"`
	Documents   int    `json:"documents"`
	Chunks      int    `json:"chunks"`
	UpdatedAt   string `json:"updated_at"`
	FullRebuild bool   `json:"full_rebuild"`
}

type Candidate struct {
	KnowledgeID        string  `json:"knowledge_id"`
	Path               string  `json:"path"`
	ChunkID            string  `json:"chunk_id"`
	Ordinal            int     `json:"ordinal"`
	StartLine          int     `json:"start_line"`
	EndLine            int     `json:"end_line"`
	BodyHash           string  `json:"body_hash"`
	IndexedFileHash    string  `json:"indexed_file_hash"`
	IndexedContentHash string  `json:"indexed_content_hash"`
	Score              float64 `json:"score"`
	RetrievalMode      string  `json:"retrieval_mode"`
}

type SearchResult struct {
	NormalizedQuery string
	RetrievalModes  []string
	Candidates      []Candidate
}

var (
	ErrStale         = errors.New("search index is stale")
	ErrNoSearchTerms = errors.New("query contains no searchable terms")
)

type chunk struct {
	ID          string
	Ordinal     int
	HeadingPath string
	Body        string
	Hash        string
	StartLine   int
	EndLine     int
}

type scannedDocument struct {
	layer    string
	rel      string
	doc      *document.Document
	fileHash string
	size     int64
	mtimeNS  int64
}

func DBPath(cfg *config.Instance) string {
	return filepath.Join(cfg.RuntimeDir(), "index.sqlite")
}

func Rebuild(cfg *config.Instance) (*RebuildResult, error) {
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return rebuildLocked(cfg)
}

func rebuildLocked(cfg *config.Instance) (*RebuildResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.RuntimeDir(), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(cfg.RuntimeDir(), "index-rebuild-*.sqlite")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	defer os.Remove(tmpPath)
	db, err := openDB(tmpPath)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if _, err := db.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("create index schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	rollback := true
	defer func() {
		if rollback {
			tx.Rollback()
		}
	}()

	result := &RebuildResult{
		Path:      filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "index.sqlite")),
		Documents: map[string]int{"raw": 0, "knowledge": 0, "derived": 0},
		RebuiltAt: time.Now().Format(time.RFC3339),
	}
	type layerDocs struct {
		name string
		root string
		docs []*document.Document
	}
	var layers []layerDocs
	rawHashes := map[string]string{}
	for _, spec := range []struct{ name, root string }{
		{"raw", cfg.RawDir()}, {"knowledge", cfg.KnowledgeDir()}, {"derived", cfg.DerivedDir()},
	} {
		if _, err := os.Stat(spec.root); errors.Is(err, os.ErrNotExist) && spec.name == "derived" {
			layers = append(layers, layerDocs{name: spec.name, root: spec.root})
			continue
		} else if err != nil {
			return nil, err
		}
		docs, problems := document.ScanMarkdown(spec.root)
		if len(problems) > 0 {
			return nil, fmt.Errorf("scan %s: %w", spec.name, problems[0])
		}
		for _, doc := range docs {
			if err := doc.Validate(spec.name, cfg.Publish.RequireSources); err != nil {
				return nil, fmt.Errorf("validate %s: %w", doc.Path, err)
			}
			if spec.name == "raw" {
				if _, duplicate := rawHashes[doc.Metadata.ID]; duplicate {
					return nil, fmt.Errorf("duplicate raw id %s", doc.Metadata.ID)
				}
				rawHashes[doc.Metadata.ID] = doc.Metadata.ContentHash
			}
		}
		layers = append(layers, layerDocs{name: spec.name, root: spec.root, docs: docs})
	}
	for _, layer := range layers {
		for _, doc := range layer.docs {
			if layer.name == "knowledge" {
				for _, source := range doc.Metadata.Sources {
					if rawHashes[source.ID] != source.ContentHash {
						return nil, fmt.Errorf("knowledge %s source %s is missing or changed", doc.Metadata.ID, source.ID)
					}
				}
			}
			rel, err := filepath.Rel(cfg.Root, doc.Path)
			if err != nil {
				return nil, err
			}
			metadataJSON, err := json.Marshal(doc.Metadata)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(doc.Path)
			if err != nil {
				return nil, err
			}
			rel = filepath.ToSlash(rel)
			if _, err := tx.Exec(`INSERT INTO documents(id,layer,path,type,title,status,content_hash,updated_at,metadata_json)
				VALUES(?,?,?,?,?,?,?,?,?)`, doc.Metadata.ID, layer.name, rel, doc.Metadata.Type, doc.Metadata.Title,
				doc.Metadata.Status, doc.Metadata.ContentHash, effectiveUpdatedAt(doc.Metadata), string(metadataJSON)); err != nil {
				return nil, fmt.Errorf("insert document %s: %w", doc.Metadata.ID, err)
			}
			fileBytes, err := os.ReadFile(doc.Path)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`INSERT INTO files(path,layer,size,mtime_ns,file_hash,document_id,indexed_at)
				VALUES(?,?,?,?,?,?,?)`, rel, layer.name, info.Size(), info.ModTime().UnixNano(), document.HashBytes(fileBytes),
				doc.Metadata.ID, result.RebuiltAt); err != nil {
				return nil, err
			}
			result.Documents[layer.name]++
			if layer.name == "knowledge" {
				for _, source := range doc.Metadata.Sources {
					if _, err := tx.Exec(`INSERT INTO source_links(knowledge_id,raw_id,raw_content_hash) VALUES(?,?,?)`,
						doc.Metadata.ID, source.ID, source.ContentHash); err != nil {
						return nil, err
					}
					result.Sources++
				}
				chunks := makeChunks(doc.Metadata.ID, doc.Body, cfg.Index.ChunkMaxChars, cfg.Index.ChunkOverlapChars)
				for _, c := range chunks {
					if _, err := tx.Exec(`INSERT INTO chunks(id,document_id,ordinal,heading_path,body,body_hash,start_line,end_line)
						VALUES(?,?,?,?,?,?,?,?)`, c.ID, doc.Metadata.ID, c.Ordinal, c.HeadingPath, c.Body, c.Hash, c.StartLine, c.EndLine); err != nil {
						return nil, err
					}
					if _, err := tx.Exec(`INSERT INTO chunks_fts(document_id,chunk_id,title,headings,properties,body)
						VALUES(?,?,?,?,?,?)`, doc.Metadata.ID, c.ID, doc.Metadata.Title, c.HeadingPath,
						propertySearchText(doc.Metadata), c.Body); err != nil {
						return nil, err
					}
					result.Chunks++
				}
			}
		}
	}
	meta := map[string]string{
		"schema_version":        fmt.Sprintf("%d", SchemaVersion),
		"built_at":              result.RebuiltAt,
		"wiki_id":               cfg.InstanceID,
		"tokenizer":             sqlite3simple.TokenizerName,
		"tokenizer_version":     sqlite3simple.TokenizerVersion,
		"tokenizer_commit":      sqlite3simple.TokenizerCommit,
		"query_planner_version": QueryPlannerVersion,
	}
	for key, value := range meta {
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES(?,?)`, key, value); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rollback = false
	if err := db.Close(); err != nil {
		return nil, err
	}
	ok = true
	if err := swapFile(tmpPath, DBPath(cfg)); err != nil {
		return nil, err
	}
	return result, nil
}

func GetStatus(cfg *config.Instance) (*Status, error) {
	path := DBPath(cfg)
	status := &Status{Path: filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "index.sqlite"))}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return status, nil
	} else if err != nil {
		return nil, err
	}
	status.Exists = true
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var versionText string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&versionText); err != nil {
		return nil, err
	}
	fmt.Sscanf(versionText, "%d", &status.SchemaVersion)
	_ = db.QueryRow(`SELECT value FROM meta WHERE key='built_at'`).Scan(&status.BuiltAt)
	_ = db.QueryRow(`SELECT value FROM meta WHERE key='tokenizer'`).Scan(&status.Tokenizer)
	_ = db.QueryRow(`SELECT value FROM meta WHERE key='tokenizer_version'`).Scan(&status.TokenizerVersion)
	_ = db.QueryRow(`SELECT value FROM meta WHERE key='tokenizer_commit'`).Scan(&status.TokenizerCommit)
	_ = db.QueryRow(`SELECT value FROM meta WHERE key='query_planner_version'`).Scan(&status.QueryPlannerVersion)
	status.Documents = map[string]int{}
	rows, err := db.Query(`SELECT layer, count(*) FROM documents GROUP BY layer ORDER BY layer`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var layer string
		var count int
		if err := rows.Scan(&layer, &count); err != nil {
			rows.Close()
			return nil, err
		}
		status.Documents[layer] = count
	}
	rows.Close()
	if err := db.QueryRow(`SELECT count(*) FROM chunks`).Scan(&status.Chunks); err != nil {
		return nil, err
	}
	return status, nil
}

func Update(cfg *config.Instance, dryRun bool) (*UpdateResult, error) {
	var lock *vault.Lock
	var err error
	if !dryRun {
		lock, err = vault.AcquireWrite(cfg, 5*time.Second)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
	}
	updatedAt := time.Now().Format(time.RFC3339)
	path := DBPath(cfg)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if dryRun {
			scanned, scanErr := scanValidated(cfg)
			if scanErr != nil {
				return nil, scanErr
			}
			return &UpdateResult{Mode: "full", DryRun: true, Added: len(scanned), Documents: len(scanned), UpdatedAt: updatedAt, FullRebuild: true}, nil
		}
		result, err := rebuildLocked(cfg)
		if err != nil {
			return nil, err
		}
		return &UpdateResult{Mode: "full", Added: totalDocuments(result.Documents), Documents: totalDocuments(result.Documents), Chunks: result.Chunks, UpdatedAt: result.RebuiltAt, FullRebuild: true}, nil
	} else if err != nil {
		return nil, err
	}
	scanned, err := scanValidated(cfg)
	if err != nil {
		return nil, err
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if !dryRun {
		if _, err := db.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;"); err != nil {
			return nil, err
		}
	}
	if err := validateIndexMetadata(db, cfg); err != nil {
		if dryRun {
			return &UpdateResult{Mode: "full", DryRun: true, Added: len(scanned), Documents: len(scanned), UpdatedAt: updatedAt, FullRebuild: true}, nil
		}
		db.Close()
		result, rebuildErr := rebuildLocked(cfg)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		return &UpdateResult{Mode: "full", Added: totalDocuments(result.Documents), Documents: totalDocuments(result.Documents), Chunks: result.Chunks, UpdatedAt: result.RebuiltAt, FullRebuild: true}, nil
	}
	type currentFile struct {
		fileHash string
		docID    string
		layer    string
	}
	current := map[string]currentFile{}
	rows, err := db.Query(`SELECT path,file_hash,document_id,layer FROM files`)
	if err != nil {
		if dryRun {
			return &UpdateResult{Mode: "full", DryRun: true, Added: len(scanned), Documents: len(scanned), UpdatedAt: updatedAt, FullRebuild: true}, nil
		}
		db.Close()
		result, rebuildErr := rebuildLocked(cfg)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		return &UpdateResult{Mode: "full", Added: totalDocuments(result.Documents), Documents: totalDocuments(result.Documents), Chunks: result.Chunks, UpdatedAt: result.RebuiltAt, FullRebuild: true}, nil
	}
	for rows.Next() {
		var rel string
		var item currentFile
		if err := rows.Scan(&rel, &item.fileHash, &item.docID, &item.layer); err != nil {
			rows.Close()
			return nil, err
		}
		current[rel] = item
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	result := &UpdateResult{Mode: "incremental", DryRun: dryRun, Documents: len(scanned), UpdatedAt: updatedAt}
	var changed []*scannedDocument
	seen := map[string]bool{}
	for _, item := range scanned {
		seen[item.rel] = true
		old, exists := current[item.rel]
		if !exists {
			result.Added++
			changed = append(changed, item)
		} else if old.fileHash != item.fileHash || old.docID != item.doc.Metadata.ID || old.layer != item.layer {
			result.Changed++
			changed = append(changed, item)
		} else {
			result.Unchanged++
		}
	}
	var deleted []string
	for rel := range current {
		if !seen[rel] {
			deleted = append(deleted, rel)
			result.Deleted++
		}
	}
	sort.Strings(deleted)
	if dryRun {
		return result, nil
	}
	if len(changed) == 0 && len(deleted) == 0 {
		if err := db.QueryRow(`SELECT count(*) FROM chunks`).Scan(&result.Chunks); err != nil {
			return nil, err
		}
		return result, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`DELETE FROM source_links`); err != nil {
		return nil, err
	}
	for _, rel := range deleted {
		if err := deleteIndexedPath(tx, rel); err != nil {
			return nil, err
		}
	}
	for _, item := range changed {
		if _, exists := current[item.rel]; exists {
			if err := deleteIndexedPath(tx, item.rel); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range changed {
		if err := insertScanned(tx, item, cfg, updatedAt); err != nil {
			return nil, err
		}
	}
	for _, item := range scanned {
		if item.layer != "knowledge" {
			continue
		}
		for _, source := range item.doc.Metadata.Sources {
			if _, err := tx.Exec(`INSERT INTO source_links(knowledge_id,raw_id,raw_content_hash) VALUES(?,?,?)`,
				item.doc.Metadata.ID, source.ID, source.ContentHash); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('built_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, updatedAt); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM chunks`).Scan(&result.Chunks); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rollback = false
	return result, nil
}

func scanValidated(cfg *config.Instance) ([]*scannedDocument, error) {
	var scanned []*scannedDocument
	rawHashes := map[string]string{}
	for _, spec := range []struct{ layer, root string }{
		{"raw", cfg.RawDir()}, {"knowledge", cfg.KnowledgeDir()}, {"derived", cfg.DerivedDir()},
	} {
		if _, err := os.Stat(spec.root); errors.Is(err, os.ErrNotExist) && spec.layer == "derived" {
			continue
		} else if err != nil {
			return nil, err
		}
		docs, problems := document.ScanMarkdown(spec.root)
		if len(problems) > 0 {
			return nil, problems[0]
		}
		for _, doc := range docs {
			if err := doc.Validate(spec.layer, cfg.Publish.RequireSources); err != nil {
				return nil, fmt.Errorf("validate %s: %w", doc.Path, err)
			}
			if spec.layer == "raw" {
				if _, exists := rawHashes[doc.Metadata.ID]; exists {
					return nil, fmt.Errorf("duplicate raw id %s", doc.Metadata.ID)
				}
				rawHashes[doc.Metadata.ID] = doc.Metadata.ContentHash
			}
			rel, err := filepath.Rel(cfg.Root, doc.Path)
			if err != nil {
				return nil, err
			}
			b, err := os.ReadFile(doc.Path)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(doc.Path)
			if err != nil {
				return nil, err
			}
			scanned = append(scanned, &scannedDocument{
				layer: spec.layer, rel: filepath.ToSlash(rel), doc: doc,
				fileHash: document.HashBytes(b), size: info.Size(), mtimeNS: info.ModTime().UnixNano(),
			})
		}
	}
	for _, item := range scanned {
		if item.layer != "knowledge" {
			continue
		}
		for _, source := range item.doc.Metadata.Sources {
			if rawHashes[source.ID] != source.ContentHash {
				return nil, fmt.Errorf("knowledge %s source %s is missing or changed", item.doc.Metadata.ID, source.ID)
			}
		}
	}
	sort.Slice(scanned, func(i, j int) bool {
		if layerOrder(scanned[i].layer) != layerOrder(scanned[j].layer) {
			return layerOrder(scanned[i].layer) < layerOrder(scanned[j].layer)
		}
		return scanned[i].rel < scanned[j].rel
	})
	return scanned, nil
}

func deleteIndexedPath(tx *sql.Tx, rel string) error {
	var docID string
	err := tx.QueryRow(`SELECT document_id FROM files WHERE path=?`, rel).Scan(&docID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM chunks_fts WHERE document_id=?`, docID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM chunks WHERE document_id=?`, docID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE path=?`, rel); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM documents WHERE id=?`, docID)
	return err
}

func insertScanned(tx *sql.Tx, item *scannedDocument, cfg *config.Instance, indexedAt string) error {
	metaJSON, err := json.Marshal(item.doc.Metadata)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO documents(id,layer,path,type,title,status,content_hash,updated_at,metadata_json)
		VALUES(?,?,?,?,?,?,?,?,?)`, item.doc.Metadata.ID, item.layer, item.rel, item.doc.Metadata.Type,
		item.doc.Metadata.Title, item.doc.Metadata.Status, item.doc.Metadata.ContentHash,
		effectiveUpdatedAt(item.doc.Metadata), string(metaJSON)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO files(path,layer,size,mtime_ns,file_hash,document_id,indexed_at)
		VALUES(?,?,?,?,?,?,?)`, item.rel, item.layer, item.size, item.mtimeNS, item.fileHash, item.doc.Metadata.ID, indexedAt); err != nil {
		return err
	}
	if item.layer != "knowledge" {
		return nil
	}
	for _, c := range makeChunks(item.doc.Metadata.ID, item.doc.Body, cfg.Index.ChunkMaxChars, cfg.Index.ChunkOverlapChars) {
		if _, err := tx.Exec(`INSERT INTO chunks(id,document_id,ordinal,heading_path,body,body_hash,start_line,end_line)
			VALUES(?,?,?,?,?,?,?,?)`, c.ID, item.doc.Metadata.ID, c.Ordinal, c.HeadingPath, c.Body, c.Hash, c.StartLine, c.EndLine); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO chunks_fts(document_id,chunk_id,title,headings,properties,body)
			VALUES(?,?,?,?,?,?)`, item.doc.Metadata.ID, c.ID, item.doc.Metadata.Title, c.HeadingPath,
			propertySearchText(item.doc.Metadata), c.Body); err != nil {
			return err
		}
	}
	return nil
}

func layerOrder(layer string) int {
	switch layer {
	case "raw":
		return 0
	case "knowledge":
		return 1
	default:
		return 2
	}
}

func totalDocuments(items map[string]int) int {
	total := 0
	for _, count := range items {
		total += count
	}
	return total
}

func openDB(path string) (*sql.DB, error) {
	return sql.Open(sqlite3simple.DriverName, path)
}

func validateIndexMetadata(db *sql.DB, cfg *config.Instance) error {
	required := []struct {
		key   string
		value string
	}{
		{"schema_version", fmt.Sprintf("%d", SchemaVersion)},
		{"wiki_id", cfg.InstanceID},
		{"tokenizer", sqlite3simple.TokenizerName},
		{"tokenizer_version", sqlite3simple.TokenizerVersion},
		{"tokenizer_commit", sqlite3simple.TokenizerCommit},
		{"query_planner_version", QueryPlannerVersion},
	}
	for _, item := range required {
		var actual string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key=?`, item.key).Scan(&actual); err != nil {
			return fmt.Errorf("index metadata %s is missing: %w", item.key, err)
		}
		if actual != item.value {
			return fmt.Errorf("index metadata %s is %q, expected %q", item.key, actual, item.value)
		}
	}
	return nil
}

func ProbeTokenizer(cfg *config.Instance) error {
	db, err := openDB(DBPath(cfg))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var generated string
	if err := db.QueryRow(`SELECT simple_query('中文检索', 0)`).Scan(&generated); err != nil {
		return fmt.Errorf("simple_query probe: %w", err)
	}
	if generated == "" {
		return errors.New("simple_query probe returned an empty query")
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS temp.simple_tokenizer_probe`); err != nil {
		return fmt.Errorf("reset simple tokenizer probe: %w", err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE temp.simple_tokenizer_probe USING fts5(text, tokenize='simple 0')`); err != nil {
		return fmt.Errorf("create simple tokenizer probe: %w", err)
	}
	defer db.Exec(`DROP TABLE IF EXISTS temp.simple_tokenizer_probe`)
	if _, err := db.Exec(`INSERT INTO temp.simple_tokenizer_probe(text) VALUES('中文检索')`); err != nil {
		return fmt.Errorf("insert simple tokenizer probe: %w", err)
	}
	var matches int
	if err := db.QueryRow(`SELECT count(*) FROM temp.simple_tokenizer_probe WHERE text MATCH simple_query('中文检索', 0)`).Scan(&matches); err != nil {
		return fmt.Errorf("query simple tokenizer probe: %w", err)
	}
	if matches != 1 {
		return fmt.Errorf("simple tokenizer probe returned %d matches, expected 1", matches)
	}
	return nil
}

func SearchCandidates(cfg *config.Instance, question string, limit int) ([]Candidate, error) {
	result, err := Search(cfg, question, limit)
	if err != nil {
		return nil, err
	}
	return result.Candidates, nil
}

func Search(cfg *config.Instance, question string, limit int) (*SearchResult, error) {
	normalized := normalizeQuery(question)
	if normalized == "" {
		return nil, ErrNoSearchTerms
	}
	if limit <= 0 {
		limit = 8
	}
	path := DBPath(cfg)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := validateIndexMetadata(db, cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStale, err)
	}

	result := &SearchResult{NormalizedQuery: normalized, RetrievalModes: []string{"strict"}}
	fetchLimit := limit * 4
	strict, err := searchLevel(db, normalized, fetchLimit, true, "strict")
	if err != nil {
		return nil, err
	}
	result.Candidates = mergeCandidates(result.Candidates, strict, limit)
	if len(result.Candidates) < limit {
		if relaxed := relaxedQuery(normalized); relaxed != "" {
			result.RetrievalModes = append(result.RetrievalModes, "relaxed")
			items, err := searchLevel(db, relaxed, fetchLimit, false, "relaxed")
			if err != nil {
				return nil, err
			}
			result.Candidates = mergeCandidates(result.Candidates, items, limit)
		}
	}
	return result, nil
}

func searchLevel(db *sql.DB, query string, limit int, useSimpleQuery bool, mode string) ([]Candidate, error) {
	matchExpression := "?"
	if useSimpleQuery {
		matchExpression = "simple_query(?, 0)"
	}
	rows, err := db.Query(fmt.Sprintf(`
		SELECT d.id,d.path,c.id,c.ordinal,c.start_line,c.end_line,c.body_hash,
		       f.file_hash,d.content_hash,
		       bm25(chunks_fts,0.0,0.0,8.0,4.0,6.0,1.0) AS rank
		FROM chunks_fts
		JOIN chunks c ON c.id=chunks_fts.chunk_id
		JOIN documents d ON d.id=c.document_id
		JOIN files f ON f.document_id=d.id AND f.path=d.path
		WHERE chunks_fts MATCH %s AND d.layer='knowledge'
		ORDER BY rank ASC,d.id ASC,c.ordinal ASC LIMIT ?`, matchExpression), query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var item Candidate
		var rank float64
		if err := rows.Scan(&item.KnowledgeID, &item.Path, &item.ChunkID, &item.Ordinal,
			&item.StartLine, &item.EndLine, &item.BodyHash,
			&item.IndexedFileHash, &item.IndexedContentHash, &rank); err != nil {
			return nil, err
		}
		item.Score = -rank
		item.RetrievalMode = mode
		out = append(out, item)
	}
	return out, rows.Err()
}

func mergeCandidates(current, incoming []Candidate, limit int) []Candidate {
	seenChunks := make(map[string]bool, len(current))
	perDocument := make(map[string]int, len(current))
	for _, candidate := range current {
		seenChunks[candidate.ChunkID] = true
		perDocument[candidate.KnowledgeID]++
	}
	for _, candidate := range incoming {
		if len(current) >= limit {
			break
		}
		if seenChunks[candidate.ChunkID] || perDocument[candidate.KnowledgeID] >= 2 {
			continue
		}
		seenChunks[candidate.ChunkID] = true
		perDocument[candidate.KnowledgeID]++
		current = append(current, candidate)
	}
	return current
}

func makeChunks(documentID string, body []byte, maxChars, overlap int) []chunk {
	normalized := string(document.NormalizeMarkdownBody(body))
	if normalized == "" {
		return nil
	}
	lines := strings.Split(normalized, "\n")
	headingAtLine := make([]string, len(lines))
	activeHeading := ""
	for i, line := range lines {
		if heading := headingText(line); heading != "" {
			activeHeading = heading
		}
		headingAtLine[i] = activeHeading
	}
	var out []chunk
	start := 0
	for start < len(lines) {
		end := start
		chars := 0
		heading := headingAtLine[start]
		for end < len(lines) {
			lineChars := utf8.RuneCountInString(lines[end]) + 1
			if end > start && chars+lineChars > maxChars {
				break
			}
			if h := headingText(lines[end]); h != "" {
				heading = h
			}
			chars += lineChars
			end++
		}
		if end == start {
			end++
		}
		text := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if text != "" {
			ordinal := len(out)
			out = append(out, chunk{
				ID: fmt.Sprintf("%s:%04d", documentID, ordinal), Ordinal: ordinal,
				HeadingPath: heading, Body: text, Hash: document.HashBytes([]byte(text)),
				StartLine: start + 1, EndLine: end,
			})
		}
		if end >= len(lines) {
			break
		}
		if overlap <= 0 {
			start = end
			continue
		}
		backChars := 0
		newStart := end
		for newStart > start && backChars < overlap {
			newStart--
			backChars += utf8.RuneCountInString(lines[newStart]) + 1
		}
		if newStart == start {
			start = end
		} else {
			start = newStart
		}
	}
	return out
}

func headingText(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return ""
	}
	i := 0
	for i < len(trimmed) && trimmed[i] == '#' {
		i++
	}
	if i > 6 || i >= len(trimmed) || trimmed[i] != ' ' {
		return ""
	}
	return strings.TrimSpace(trimmed[i:])
}

func propertySearchText(meta document.Metadata) string {
	parts := make([]string, 0, len(meta.Tags)+len(meta.Aliases)+len(meta.Extra)*2)
	parts = append(parts, meta.Tags...)
	parts = append(parts, meta.Aliases...)
	keys := make([]string, 0, len(meta.Extra))
	for key := range meta.Extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := json.Marshal(meta.Extra[key])
		if err != nil {
			continue
		}
		parts = append(parts, key, string(value))
	}
	return strings.Join(parts, " ")
}

func normalizeQuery(query string) string {
	for _, wrapper := range []string{"为什么", "是什么", "有什么", "请问", "怎么", "如何", "是否"} {
		query = strings.ReplaceAll(query, wrapper, " ")
	}
	var normalized strings.Builder
	lastSpace := true
	for _, r := range query {
		if strings.ContainsRune("的了吗呢", r) {
			continue
		}
		if unicode.Is(unicode.Han, r) || r == '_' || (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if r >= 'A' && r <= 'Z' {
				r = unicode.ToLower(r)
			}
			normalized.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace && normalized.Len() > 0 {
			normalized.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(normalized.String())
}

func relaxedQuery(normalized string) string {
	seen := map[string]bool{}
	var terms []string
	appendTerm := func(term string) {
		quoted := `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
		if !seen[quoted] {
			seen[quoted] = true
			terms = append(terms, quoted)
		}
	}
	for _, field := range strings.Fields(normalized) {
		var hanRun []rune
		var wordRun strings.Builder
		flushHan := func() {
			for i := 0; i+1 < len(hanRun); i++ {
				appendTerm(string(hanRun[i : i+2]))
			}
			hanRun = nil
		}
		flushWord := func() {
			if wordRun.Len() > 0 {
				appendTerm(wordRun.String())
				wordRun.Reset()
			}
		}
		for _, r := range field {
			if unicode.Is(unicode.Han, r) {
				flushWord()
				hanRun = append(hanRun, r)
			} else {
				flushHan()
				wordRun.WriteRune(r)
			}
		}
		flushHan()
		flushWord()
	}
	return strings.Join(terms, " OR ")
}

func effectiveUpdatedAt(meta document.Metadata) string {
	if meta.UpdatedAt != "" {
		return meta.UpdatedAt
	}
	if meta.CapturedAt != "" {
		return meta.CapturedAt
	}
	return meta.GeneratedAt
}

func swapFile(staged, target string) error {
	backup := target + ".backup"
	os.Remove(backup)
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		hadTarget = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return err
	}
	if hadTarget {
		_ = os.Remove(backup)
	}
	return nil
}

const schemaSQL = `
CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE documents(
  id TEXT PRIMARY KEY,layer TEXT NOT NULL,path TEXT NOT NULL UNIQUE,type TEXT,title TEXT,status TEXT,
  content_hash TEXT NOT NULL,updated_at TEXT,metadata_json TEXT NOT NULL
);
CREATE TABLE files(
  path TEXT PRIMARY KEY,layer TEXT NOT NULL,size INTEGER NOT NULL,mtime_ns INTEGER NOT NULL,
  file_hash TEXT NOT NULL,document_id TEXT NOT NULL,indexed_at TEXT NOT NULL,
  FOREIGN KEY(document_id) REFERENCES documents(id) ON DELETE CASCADE
);
CREATE TABLE source_links(
  knowledge_id TEXT NOT NULL,raw_id TEXT NOT NULL,raw_content_hash TEXT NOT NULL,
  PRIMARY KEY(knowledge_id,raw_id),
  FOREIGN KEY(knowledge_id) REFERENCES documents(id) ON DELETE CASCADE,
  FOREIGN KEY(raw_id) REFERENCES documents(id) ON DELETE RESTRICT
);
CREATE TABLE chunks(
  id TEXT PRIMARY KEY,document_id TEXT NOT NULL,ordinal INTEGER NOT NULL,heading_path TEXT,
  body TEXT NOT NULL,body_hash TEXT NOT NULL,start_line INTEGER,end_line INTEGER,
  UNIQUE(document_id,ordinal),FOREIGN KEY(document_id) REFERENCES documents(id) ON DELETE CASCADE
);
CREATE VIRTUAL TABLE chunks_fts USING fts5(
  document_id UNINDEXED,chunk_id UNINDEXED,title,headings,properties,body,
  tokenize='simple 0',
  prefix='2 3 4'
);
CREATE TABLE operations(
  id TEXT PRIMARY KEY,kind TEXT NOT NULL,state TEXT NOT NULL,started_at TEXT NOT NULL,
  finished_at TEXT,detail_json TEXT
);
`
