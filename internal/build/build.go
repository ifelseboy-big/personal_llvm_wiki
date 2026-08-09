package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/vault"
)

const (
	CompilerName    = "standard"
	CompilerVersion = 3
)

type ManifestItem struct {
	KnowledgeID       string `json:"knowledge_id"`
	KnowledgeHash     string `json:"knowledge_hash"`
	KnowledgeFileHash string `json:"knowledge_file_hash"`
	OutputPath        string `json:"output_path"`
	OutputHash        string `json:"output_hash"`
	Fingerprint       string `json:"fingerprint"`
}

type Manifest struct {
	SchemaVersion   int            `json:"schema_version"`
	WikiID          string         `json:"wiki_id"`
	Compiler        string         `json:"compiler"`
	CompilerVersion int            `json:"compiler_version"`
	ConfigHash      string         `json:"config_hash"`
	GeneratedAt     string         `json:"generated_at"`
	Items           []ManifestItem `json:"items"`
}

type Result struct {
	Full      bool     `json:"full"`
	DryRun    bool     `json:"dry_run"`
	Generated int      `json:"generated"`
	Unchanged int      `json:"unchanged"`
	Removed   int      `json:"removed"`
	Files     []string `json:"files"`
	Manifest  Manifest `json:"manifest"`
}

type ItemStatus struct {
	KnowledgeID string `json:"knowledge_id"`
	OutputPath  string `json:"output_path"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
}

type Status struct {
	Fresh    bool         `json:"fresh"`
	Manifest bool         `json:"manifest_exists"`
	Items    []ItemStatus `json:"items"`
	Orphans  []string     `json:"orphans"`
}

func Build(cfg *config.Instance, full, dryRun bool) (*Result, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	var lock *vault.Lock
	var err error
	if !dryRun {
		lock, err = vault.AcquireWrite(cfg, 5*time.Second)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
	}

	docs, problems := document.ScanMarkdown(cfg.KnowledgeDir())
	if len(problems) > 0 {
		return nil, problems[0]
	}
	rawHashes := map[string]string{}
	rawDocs, rawProblems := document.ScanMarkdown(cfg.RawDir())
	if len(rawProblems) > 0 {
		return nil, rawProblems[0]
	}
	for _, rawDoc := range rawDocs {
		if err := rawDoc.Validate("raw", false); err != nil {
			return nil, err
		}
		rawHashes[rawDoc.Metadata.ID] = rawDoc.Metadata.ContentHash
	}

	configHash := buildConfigHash(cfg)
	manifest := Manifest{
		SchemaVersion: 2, WikiID: cfg.InstanceID, Compiler: CompilerName,
		CompilerVersion: CompilerVersion, ConfigHash: configHash,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}
	type output struct {
		rel  string
		data []byte
	}
	var outputs []output
	for _, doc := range docs {
		if err := doc.Validate("knowledge", cfg.Publish.RequireSources); err != nil {
			return nil, err
		}
		legacyGovernance, err := governance.ValidateStored(cfg, doc, time.Now())
		if err != nil {
			return nil, err
		}
		lifecycle, err := governance.AssessStoredLifecycle(cfg, doc.Metadata, time.Now(), legacyGovernance)
		if err != nil {
			return nil, err
		}
		for _, source := range doc.Metadata.Sources {
			if rawHashes[source.ID] != source.ContentHash {
				return nil, fmt.Errorf("knowledge %s source %s is missing or changed", doc.Metadata.ID, source.ID)
			}
		}
		derivedID, err := document.DerivedID(doc.Metadata.ID)
		if err != nil {
			return nil, err
		}
		knowledgeBytes, err := os.ReadFile(doc.Path)
		if err != nil {
			return nil, err
		}
		knowledgeFileHash := document.HashBytes(knowledgeBytes)
		fingerprint := derivedFingerprint(doc.Metadata.ID, knowledgeFileHash, configHash,
			governance.UsesPersonalV12(cfg), legacyGovernance, lifecycle)
		body := compileBody(doc, governance.UsesPersonalV12(cfg), legacyGovernance, lifecycle)
		meta := document.Metadata{
			SchemaVersion: document.CurrentSchema, ID: derivedID,
			Title: doc.Metadata.Title, Type: doc.Metadata.Type,
			ContentHash: document.HashBytes(body),
			DerivedFrom: &document.DerivedFrom{ID: doc.Metadata.ID, ContentHash: doc.Metadata.ContentHash},
			Compiler:    CompilerName, CompilerVersion: CompilerVersion,
			GovernanceVersion: doc.Metadata.GovernanceVersion,
			BuildFingerprint:  fingerprint, GeneratedAt: doc.Metadata.UpdatedAt,
			Tags: doc.Metadata.Tags, Aliases: doc.Metadata.Aliases, Extra: doc.Metadata.Extra,
		}
		data, err := document.Render(meta, body)
		if err != nil {
			return nil, err
		}
		if len(data) > document.MaxMarkdownBytes {
			return nil, errors.New("rendered derived document exceeds the Markdown scanner safety limit")
		}
		rel := filepath.ToSlash(filepath.Join("documents", doc.Metadata.ID+".md"))
		manifest.Items = append(manifest.Items, ManifestItem{
			KnowledgeID: doc.Metadata.ID, KnowledgeHash: doc.Metadata.ContentHash, KnowledgeFileHash: knowledgeFileHash,
			OutputPath: rel, OutputHash: document.HashBytes(data), Fingerprint: fingerprint,
		})
		outputs = append(outputs, output{rel: rel, data: data})
	}
	sort.Slice(manifest.Items, func(i, j int) bool { return manifest.Items[i].KnowledgeID < manifest.Items[j].KnowledgeID })
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].rel < outputs[j].rel })

	result := &Result{Full: full, DryRun: dryRun, Manifest: manifest}
	desired := map[string]string{}
	for _, item := range manifest.Items {
		desired[item.OutputPath] = item.OutputHash
		path := filepath.Join(cfg.DerivedDir(), filepath.FromSlash(item.OutputPath))
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
			return nil, err
		}
		current, err := os.ReadFile(path)
		if err == nil && document.HashBytes(current) == item.OutputHash {
			result.Unchanged++
		} else {
			result.Generated++
			result.Files = append(result.Files, filepath.ToSlash(filepath.Join(cfg.Paths.Derived, item.OutputPath)))
		}
	}
	currentFiles, _ := derivedMarkdownFiles(cfg.DerivedDir())
	for _, rel := range currentFiles {
		if _, ok := desired[rel]; !ok {
			result.Removed++
		}
	}
	if dryRun {
		return result, nil
	}
	if !full && result.Generated == 0 && result.Removed == 0 {
		manifestPath := filepath.Join(cfg.DerivedDir(), "manifest.json")
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, manifestPath); err != nil {
			return nil, err
		}
		if currentManifest, err := os.ReadFile(manifestPath); err == nil {
			var persisted Manifest
			if json.Unmarshal(currentManifest, &persisted) == nil && validateManifest(cfg, persisted) == nil {
				result.Manifest = persisted
			}
		}
		result.Files = nil
		return result, nil
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if !full {
		for _, output := range outputs {
			target := filepath.Join(cfg.DerivedDir(), filepath.FromSlash(output.rel))
			current, readErr := os.ReadFile(target)
			if readErr == nil && document.HashBytes(current) == document.HashBytes(output.data) {
				continue
			}
			if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
				return nil, err
			}
			if err := document.AtomicWrite(target, output.data, 0o600); err != nil {
				return nil, err
			}
		}
		for _, rel := range currentFiles {
			if _, ok := desired[rel]; ok {
				continue
			}
			target := filepath.Join(cfg.DerivedDir(), filepath.FromSlash(rel))
			if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
				return nil, err
			}
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			result.Files = append(result.Files, filepath.ToSlash(filepath.Join(cfg.Paths.Derived, rel)))
		}
		manifestPath := filepath.Join(cfg.DerivedDir(), "manifest.json")
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, manifestPath); err != nil {
			return nil, err
		}
		if err := document.AtomicWrite(manifestPath, manifestBytes, 0o600); err != nil {
			return nil, err
		}
		result.Files = append(result.Files, filepath.ToSlash(filepath.Join(cfg.Paths.Derived, "manifest.json")))
		return result, nil
	}

	opID, err := document.NewID("op", time.Now())
	if err != nil {
		return nil, err
	}
	txnRoot := filepath.Join(cfg.RuntimeDir(), "transactions", opID+"-build")
	stage := filepath.Join(txnRoot, "derived")
	if err := os.MkdirAll(filepath.Join(stage, "documents"), 0o700); err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if err := document.AtomicWrite(filepath.Join(stage, filepath.FromSlash(output.rel)), output.data, 0o600); err != nil {
			return nil, err
		}
	}
	if err := document.AtomicWrite(filepath.Join(stage, "manifest.json"), manifestBytes, 0o600); err != nil {
		return nil, err
	}
	backup := filepath.Join(txnRoot, "previous")
	if _, err := os.Stat(cfg.DerivedDir()); err == nil {
		if err := os.Rename(cfg.DerivedDir(), backup); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(stage, cfg.DerivedDir()); err != nil {
		_ = os.Rename(backup, cfg.DerivedDir())
		return nil, err
	}
	_ = os.RemoveAll(backup)
	_ = os.RemoveAll(txnRoot)
	result.Files = nil
	for _, output := range outputs {
		result.Files = append(result.Files, filepath.ToSlash(filepath.Join(cfg.Paths.Derived, output.rel)))
	}
	result.Files = append(result.Files, filepath.ToSlash(filepath.Join(cfg.Paths.Derived, "manifest.json")))
	return result, nil
}

func GetStatus(cfg *config.Instance) (*Status, error) {
	return getStatusAt(cfg, time.Now())
}

func getStatusAt(cfg *config.Instance, now time.Time) (*Status, error) {
	if now.IsZero() {
		now = time.Now()
	}
	status := &Status{Fresh: true}
	manifestPath := filepath.Join(cfg.DerivedDir(), "manifest.json")
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, manifestPath); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		status.Fresh = false
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status.Manifest = true
	var manifest Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return nil, err
	}
	if err := validateManifest(cfg, manifest); err != nil {
		return nil, err
	}
	knowledge, problems := document.ScanMarkdown(cfg.KnowledgeDir())
	if len(problems) > 0 {
		return nil, problems[0]
	}
	known := map[string]*document.Document{}
	for _, doc := range knowledge {
		known[doc.Metadata.ID] = doc
	}
	desiredPaths := map[string]bool{}
	for _, item := range manifest.Items {
		entry := ItemStatus{KnowledgeID: item.KnowledgeID, OutputPath: item.OutputPath, State: "fresh"}
		desiredPaths[item.OutputPath] = true
		doc := known[item.KnowledgeID]
		legacy := false
		assessment := governance.LifecycleAssessment{Lifecycle: "current"}
		if doc != nil {
			var governanceErr error
			legacy, governanceErr = governance.ValidateStored(cfg, doc, now)
			if governanceErr != nil {
				return nil, governanceErr
			}
			var assessmentErr error
			assessment, assessmentErr = governance.AssessStoredLifecycle(cfg, doc.Metadata, now, legacy)
			if assessmentErr != nil {
				return nil, assessmentErr
			}
		}
		if doc == nil {
			entry.State, entry.Reason = "orphan", "source knowledge is missing"
		} else if doc.Metadata.ContentHash != item.KnowledgeHash {
			entry.State, entry.Reason = "stale", "source knowledge hash changed"
		} else if knowledgeBytes, err := os.ReadFile(doc.Path); err != nil {
			return nil, err
		} else if document.HashBytes(knowledgeBytes) != item.KnowledgeFileHash {
			entry.State, entry.Reason = "stale", "source knowledge metadata changed"
		} else if expected := derivedFingerprint(doc.Metadata.ID, item.KnowledgeFileHash, buildConfigHash(cfg),
			governance.UsesPersonalV12(cfg), legacy, assessment); item.Fingerprint != expected {
			entry.State, entry.Reason = "stale", "build fingerprint does not match source knowledge"
		} else if output, err := os.ReadFile(filepath.Join(cfg.DerivedDir(), filepath.FromSlash(item.OutputPath))); errors.Is(err, os.ErrNotExist) {
			entry.State, entry.Reason = "missing", "derived output is missing"
		} else if err != nil {
			return nil, err
		} else if document.HashBytes(output) != item.OutputHash {
			entry.State, entry.Reason = "drift", "derived output was modified"
		}
		if entry.State == "fresh" {
			derivedBytes, readErr := os.ReadFile(filepath.Join(cfg.DerivedDir(), filepath.FromSlash(item.OutputPath)))
			if readErr != nil {
				return nil, readErr
			}
			derivedMeta, _, parseErr := document.Parse(derivedBytes)
			if parseErr != nil {
				return nil, parseErr
			}
			expectedBodyHash := document.HashBytes(compileBody(doc, governance.UsesPersonalV12(cfg), legacy, assessment))
			if derivedMeta.ContentHash != expectedBodyHash {
				entry.State, entry.Reason = "stale", "knowledge lifecycle state changed"
			}
		}
		if entry.State != "fresh" {
			status.Fresh = false
		}
		status.Items = append(status.Items, entry)
		delete(known, item.KnowledgeID)
	}
	for id := range known {
		status.Items = append(status.Items, ItemStatus{KnowledgeID: id, State: "missing", Reason: "not present in build manifest"})
		status.Fresh = false
	}
	files, _ := derivedMarkdownFiles(cfg.DerivedDir())
	for _, rel := range files {
		if !desiredPaths[rel] {
			status.Orphans = append(status.Orphans, rel)
			status.Fresh = false
		}
	}
	sort.Slice(status.Items, func(i, j int) bool { return status.Items[i].KnowledgeID < status.Items[j].KnowledgeID })
	return status, nil
}

func validateManifest(cfg *config.Instance, manifest Manifest) error {
	if manifest.SchemaVersion != 2 || manifest.WikiID != cfg.InstanceID || manifest.Compiler != CompilerName ||
		manifest.CompilerVersion != CompilerVersion || manifest.ConfigHash != buildConfigHash(cfg) {
		return errors.New("derived manifest schema, wiki, compiler, or configuration does not match")
	}
	if _, err := time.Parse(time.RFC3339, manifest.GeneratedAt); err != nil {
		return errors.New("derived manifest generated_at must be RFC3339")
	}
	seenIDs := map[string]bool{}
	seenPaths := map[string]bool{}
	previousID := ""
	for _, item := range manifest.Items {
		expectedPath := filepath.ToSlash(filepath.Join("documents", item.KnowledgeID+".md"))
		if !document.ValidID("know", item.KnowledgeID) || item.OutputPath != expectedPath ||
			!document.ValidHash(item.KnowledgeHash) || !document.ValidHash(item.KnowledgeFileHash) ||
			!document.ValidHash(item.OutputHash) || !document.ValidHash(item.Fingerprint) ||
			seenIDs[item.KnowledgeID] || seenPaths[item.OutputPath] || (previousID != "" && item.KnowledgeID < previousID) {
			return errors.New("derived manifest contains invalid, duplicate, or unsorted items")
		}
		if err := fsutil.EnsureNoSymlinkPath(cfg.DerivedDir(), filepath.Join(cfg.DerivedDir(), filepath.FromSlash(item.OutputPath))); err != nil {
			return err
		}
		seenIDs[item.KnowledgeID] = true
		seenPaths[item.OutputPath] = true
		previousID = item.KnowledgeID
	}
	return nil
}

func compileBody(doc *document.Document, usesPersonalV12, legacyGovernance bool, assessment governance.LifecycleAssessment) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", doc.Metadata.Title)
	if usesPersonalV12 && legacyGovernance {
		fmt.Fprintf(&b, "> **Legacy governance:** `%s` predates personal 1.2 claim-level citation checks; verify its raw sources before relying on it.\n", doc.Metadata.ID)
	} else {
		fmt.Fprintf(&b, "> Trusted knowledge: `%s` at content hash `%s`.\n", doc.Metadata.ID, doc.Metadata.ContentHash)
	}
	if !usesPersonalV12 {
		writeRawEvidence(&b, doc)
		return appendSourceBody(&b, doc)
	}
	switch {
	case assessment.Inactive:
		fmt.Fprintf(&b, "> **Warning:** lifecycle is `%s`; this document is retained for audit and must not be used as a current fact.\n", assessment.Lifecycle)
	case assessment.Disputed:
		b.WriteString("> **Warning:** lifecycle is `disputed`; present the unresolved evidence conflict when using this knowledge.\n")
	case assessment.LegacyDefaulted:
		b.WriteString("> Lifecycle is missing in this legacy document and is treated as `current` until republished.\n")
	default:
		fmt.Fprintf(&b, "> Lifecycle: `%s`.\n", assessment.Lifecycle)
	}
	if assessment.NotYetValid {
		b.WriteString("> **Warning:** this knowledge is not yet valid and must not be used as a current fact.\n")
	}
	if assessment.Expired {
		b.WriteString("> **Warning:** this knowledge has expired and must not be used as a current fact.\n")
	}
	if assessment.ReviewDue {
		b.WriteString("> **Review due:** this knowledge requires review but is not automatically invalid.\n")
	}
	for _, key := range []string{"valid_from", "valid_until", "review_after"} {
		if value, exists := doc.Metadata.Extra[key]; exists && value != nil && fmt.Sprint(value) != "" {
			fmt.Fprintf(&b, "> %s: `%v`.\n", key, value)
		}
	}
	writeRawEvidence(&b, doc)
	return appendSourceBody(&b, doc)
}

func writeRawEvidence(b *strings.Builder, doc *document.Document) {
	if len(doc.Metadata.Sources) > 0 {
		b.WriteString("> Raw evidence:")
		for _, source := range doc.Metadata.Sources {
			fmt.Fprintf(b, " `%s`@`%s`", source.ID, source.ContentHash)
		}
		b.WriteString(".\n")
	}
}

func appendSourceBody(b *strings.Builder, doc *document.Document) []byte {
	b.WriteString("\n")
	body := strings.TrimSpace(string(document.NormalizeMarkdownBody(doc.Body)))
	if body != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func derivedFingerprint(knowledgeID, knowledgeFileHash, configHash string, usesPersonalV12, legacy bool, assessment governance.LifecycleAssessment) string {
	lifecycleState := fmt.Sprintf("v12=%t;legacy=%t;lifecycle=%s;inactive=%t;disputed=%t;not_yet_valid=%t;expired=%t;review_due=%t",
		usesPersonalV12, legacy, assessment.Lifecycle, assessment.Inactive, assessment.Disputed,
		assessment.NotYetValid, assessment.Expired, assessment.ReviewDue)
	return document.HashBytes([]byte(strings.Join([]string{
		knowledgeID, knowledgeFileHash, CompilerName, fmt.Sprintf("%d", CompilerVersion), configHash, lifecycleState,
	}, "\x00")))
}

func buildConfigHash(cfg *config.Instance) string {
	return document.HashBytes([]byte(fmt.Sprintf("compiler=%s:%d\n", CompilerName, CompilerVersion)))
}

func derivedMarkdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			rel, _ := filepath.Rel(root, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}
