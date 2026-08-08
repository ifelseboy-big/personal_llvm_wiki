package publish

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/vault"
)

type Proposal struct {
	SchemaVersion  int                  `json:"schema_version"`
	ID             string               `json:"id"`
	CreatedAt      string               `json:"created_at"`
	Sources        []document.SourceRef `json:"sources"`
	KnowledgeID    string               `json:"knowledge_id"`
	TargetPath     string               `json:"target_path"`
	BaseHash       string               `json:"base_hash,omitempty"`
	BaseFileHash   string               `json:"base_file_hash,omitempty"`
	NewContentHash string               `json:"new_content_hash"`
	FileHash       string               `json:"file_hash"`
	DraftFile      string               `json:"draft_file"`
}

type State struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	ProposalHash  string `json:"proposal_hash"`
	UpdatedAt     string `json:"updated_at"`
	Reason        string `json:"reason,omitempty"`
	AppliedAt     string `json:"applied_at,omitempty"`
	RejectedAt    string `json:"rejected_at,omitempty"`
}

type ProposeOptions struct {
	SourceIDs []string
	DraftPath string
	DryRun    bool
	Now       time.Time
}

type ProposeResult struct {
	Proposal Proposal `json:"proposal"`
	State    State    `json:"state"`
	Diff     string   `json:"diff"`
}

type ApplyResult struct {
	ChangeID    string `json:"change_id"`
	OperationID string `json:"operation_id,omitempty"`
	KnowledgeID string `json:"knowledge_id"`
	TargetPath  string `json:"target_path"`
	ContentHash string `json:"content_hash"`
	DryRun      bool   `json:"dry_run"`
}

type Journal struct {
	SchemaVersion int    `json:"schema_version"`
	OperationID   string `json:"operation_id"`
	ChangeID      string `json:"change_id"`
	State         string `json:"state"`
	TargetPath    string `json:"target_path"`
	NewFileHash   string `json:"new_file_hash"`
	HadTarget     bool   `json:"had_target"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type RecoveryAction struct {
	OperationID string `json:"operation_id"`
	Previous    string `json:"previous_state"`
	Action      string `json:"action"`
}

func PendingOperations(cfg *config.Instance) ([]string, error) {
	root := filepath.Join(cfg.RuntimeDir(), "transactions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, entry.Name(), "journal.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var journal Journal
		if err := decodeStrict(b, &journal); err != nil {
			return nil, err
		}
		if err := validateJournal(cfg, journal, entry.Name()); err != nil {
			return nil, err
		}
		switch journal.State {
		case "complete", "rolled_back":
		case "prepared", "files_committed":
			pending = append(pending, journal.OperationID)
		default:
			return nil, fmt.Errorf("operation %s has unknown journal state %q", journal.OperationID, journal.State)
		}
	}
	sort.Strings(pending)
	return pending, nil
}

var validType = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func Propose(cfg *config.Instance, opts ProposeOptions) (*ProposeResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if len(opts.SourceIDs) == 0 {
		return nil, errors.New("at least one --source is required")
	}
	if opts.DraftPath == "" {
		return nil, errors.New("draft file is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if info, err := os.Lstat(opts.DraftPath); err != nil {
		return nil, err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("draft file cannot be a symbolic link")
	} else if !info.Mode().IsRegular() {
		return nil, errors.New("draft input is not a regular file")
	} else if info.Size() > cfg.Security.MaxInputBytes {
		return nil, fmt.Errorf("draft exceeds max_input_bytes %d", cfg.Security.MaxInputBytes)
	}
	draftData, err := os.ReadFile(opts.DraftPath)
	if err != nil {
		return nil, err
	}
	draftMeta := document.Metadata{}
	body := document.NormalizeMarkdownBody(draftData)
	if bytes.HasPrefix(body, []byte("---\n")) || bytes.HasPrefix(body, []byte("---\r\n")) {
		draftMeta, body, err = document.Parse(body)
		if err != nil {
			return nil, err
		}
	}
	sourceIDs := append([]string(nil), opts.SourceIDs...)
	sort.Strings(sourceIDs)
	sourceIDs = unique(sourceIDs)
	sources := make([]document.SourceRef, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		source, err := document.FindByID(cfg.RawDir(), id)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", id, err)
		}
		if err := source.Validate("raw", false); err != nil {
			return nil, fmt.Errorf("source %s is invalid: %w", id, err)
		}
		sources = append(sources, document.SourceRef{ID: id, ContentHash: source.Metadata.ContentHash})
	}

	knowledgeID := draftMeta.ID
	baseHash := ""
	baseFileHash := ""
	publishedAt := opts.Now.Format(time.RFC3339)
	targetPath := ""
	oldBytes := []byte{}
	var existing *document.Document
	if knowledgeID != "" {
		if !strings.HasPrefix(knowledgeID, "know_") {
			return nil, errors.New("draft id for an update must start with know_")
		}
		existing, err = document.FindByID(cfg.KnowledgeDir(), knowledgeID)
		if err != nil {
			return nil, fmt.Errorf("update target %s: %w", knowledgeID, err)
		}
		if err := existing.Validate("knowledge", cfg.Publish.RequireSources); err != nil {
			return nil, err
		}
		if draftMeta.Type == "" {
			draftMeta.Type = existing.Metadata.Type
		}
		if draftMeta.Title == "" {
			draftMeta.Title = existing.Metadata.Title
		}
		baseHash = existing.Metadata.ContentHash
		publishedAt = existing.Metadata.PublishedAt
		rel, _ := filepath.Rel(cfg.Root, existing.Path)
		targetPath = filepath.ToSlash(rel)
		oldBytes, err = os.ReadFile(existing.Path)
		if err != nil {
			return nil, err
		}
		baseFileHash = document.HashBytes(oldBytes)
	} else {
		if draftMeta.Type == "" {
			draftMeta.Type = "concept"
		}
		if draftMeta.Title == "" {
			draftMeta.Title = firstHeading(body)
		}
		knowledgeID, err = document.NewID("know", opts.Now)
		if err != nil {
			return nil, err
		}
	}
	if !validType.MatchString(draftMeta.Type) {
		return nil, fmt.Errorf("invalid knowledge type %q", draftMeta.Type)
	}
	if draftMeta.Title == "" {
		return nil, errors.New("draft title is required in frontmatter or first heading")
	}
	if targetPath == "" {
		targetPath = filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, draftMeta.Type,
			document.Slug(draftMeta.Title)+"--"+knowledgeID+".md"))
	}
	tags := draftMeta.Tags
	aliases := draftMeta.Aliases
	extra := mergeUserProperties(nil, draftMeta.Extra)
	if existing != nil {
		if tags == nil {
			tags = existing.Metadata.Tags
		}
		if aliases == nil {
			aliases = existing.Metadata.Aliases
		}
		extra = mergeUserProperties(existing.Metadata.Extra, draftMeta.Extra)
	}
	contentHash := document.HashBytes(body)
	knowledgeMeta := document.Metadata{
		SchemaVersion: document.CurrentSchema,
		ID:            knowledgeID, Type: draftMeta.Type, Title: draftMeta.Title, Status: "published",
		Sources: sources, PublishedAt: publishedAt, UpdatedAt: opts.Now.Format(time.RFC3339),
		ContentHash: contentHash, Tags: cleanStrings(tags), Aliases: cleanStrings(aliases), Extra: extra,
	}
	newBytes, err := document.Render(knowledgeMeta, body)
	if err != nil {
		return nil, err
	}
	if len(newBytes) > document.MaxMarkdownBytes {
		return nil, errors.New("rendered knowledge exceeds the Markdown scanner safety limit")
	}
	changeID, err := document.NewID("chg", opts.Now)
	if err != nil {
		return nil, err
	}
	proposal := Proposal{
		SchemaVersion: 1, ID: changeID, CreatedAt: opts.Now.Format(time.RFC3339), Sources: sources,
		KnowledgeID: knowledgeID, TargetPath: targetPath, BaseHash: baseHash, BaseFileHash: baseFileHash,
		NewContentHash: contentHash, FileHash: document.HashBytes(newBytes), DraftFile: "files/document.md",
	}
	proposalBytes, err := marshalJSON(proposal)
	if err != nil {
		return nil, err
	}
	state := State{
		SchemaVersion: 1, Status: "proposed", ProposalHash: document.HashBytes(proposalBytes),
		UpdatedAt: opts.Now.Format(time.RFC3339),
	}
	diff := lineDiff(targetPath, oldBytes, newBytes)
	result := &ProposeResult{Proposal: proposal, State: state, Diff: diff}
	if opts.DryRun {
		return result, nil
	}

	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	stageRoot := filepath.Join(cfg.RuntimeDir(), "transactions", changeID+"-proposal")
	changeStage := filepath.Join(stageRoot, "change")
	changeTarget := changeDir(cfg, changeID)
	if _, err := os.Stat(changeTarget); err == nil {
		return nil, errors.New("change id collision")
	}
	if err := os.MkdirAll(filepath.Join(changeStage, "files"), 0o700); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(changeStage, "proposal.json"), proposalBytes, 0o600); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(changeStage, "state.json"), mustJSON(state), 0o600); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(changeStage, "diff.patch"), []byte(diff), 0o600); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(changeStage, filepath.FromSlash(proposal.DraftFile)), newBytes, 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(changeTarget), 0o700); err != nil {
		return nil, err
	}
	if err := os.Rename(changeStage, changeTarget); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(stageRoot)
	return result, nil
}

func Load(cfg *config.Instance, changeID string) (Proposal, State, error) {
	var proposal Proposal
	var state State
	if !document.ValidID("chg", changeID) {
		return proposal, state, errors.New("invalid change id")
	}
	dir := changeDir(cfg, changeID)
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, dir); err != nil {
		return proposal, state, err
	}
	proposalBytes, err := os.ReadFile(filepath.Join(dir, "proposal.json"))
	if err != nil {
		return proposal, state, err
	}
	if err := decodeStrict(proposalBytes, &proposal); err != nil {
		return proposal, state, err
	}
	if err := validateProposal(cfg, proposal, changeID); err != nil {
		return proposal, state, err
	}
	stateBytes, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return proposal, state, err
	}
	if err := decodeStrict(stateBytes, &state); err != nil {
		return proposal, state, err
	}
	if err := validateState(state); err != nil {
		return proposal, state, err
	}
	if state.ProposalHash != document.HashBytes(proposalBytes) {
		return proposal, state, errors.New("proposal integrity hash mismatch")
	}
	return proposal, state, nil
}

func Diff(cfg *config.Instance, changeID string) (string, error) {
	if _, _, err := Load(cfg, changeID); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(changeDir(cfg, changeID), "diff.patch"))
	return string(b), err
}

func Apply(cfg *config.Instance, changeID string, dryRun bool, now time.Time) (*ApplyResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
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
	proposal, state, err := Load(cfg, changeID)
	if err != nil {
		return nil, err
	}
	if state.Status != "proposed" {
		return nil, fmt.Errorf("change is %s, expected proposed", state.Status)
	}
	if err := validateProposalBase(cfg, proposal); err != nil {
		state.Status = "stale"
		state.Reason = err.Error()
		state.UpdatedAt = now.Format(time.RFC3339)
		if !dryRun {
			_ = writeState(cfg, changeID, state)
		}
		return nil, err
	}
	proposedPath := filepath.Join(changeDir(cfg, changeID), filepath.FromSlash(proposal.DraftFile))
	newBytes, err := os.ReadFile(proposedPath)
	if err != nil {
		return nil, err
	}
	if document.HashBytes(newBytes) != proposal.FileHash {
		return nil, errors.New("proposed document file integrity hash mismatch")
	}
	meta, body, err := document.Parse(newBytes)
	if err != nil {
		return nil, err
	}
	if meta.ID != proposal.KnowledgeID || meta.ContentHash != proposal.NewContentHash || document.HashBytes(body) != proposal.NewContentHash {
		return nil, errors.New("proposed document metadata does not match proposal")
	}
	result := &ApplyResult{
		ChangeID: changeID, KnowledgeID: proposal.KnowledgeID, TargetPath: proposal.TargetPath,
		ContentHash: proposal.NewContentHash, DryRun: dryRun,
	}
	if dryRun {
		return result, nil
	}
	opID, err := document.NewID("op", now)
	if err != nil {
		return nil, err
	}
	result.OperationID = opID
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	if err := os.MkdirAll(filepath.Join(txnDir, "stage"), 0o700); err != nil {
		return nil, err
	}
	stagePath := filepath.Join(txnDir, "stage", "document.md")
	if err := document.AtomicWrite(stagePath, newBytes, 0o600); err != nil {
		return nil, err
	}
	target := filepath.Join(cfg.Root, filepath.FromSlash(proposal.TargetPath))
	if err := vault.EnsureInside(cfg.Root, target); err != nil {
		return nil, err
	}
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
		return nil, err
	}
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		hadTarget = true
		if err := copyFile(target, filepath.Join(txnDir, "backup.md")); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	journal := Journal{
		SchemaVersion: 1, OperationID: opID, ChangeID: changeID, State: "prepared",
		TargetPath: proposal.TargetPath, NewFileHash: proposal.FileHash, HadTarget: hadTarget,
		CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339),
	}
	if err := writeJournal(txnDir, journal); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(target, newBytes, 0o600); err != nil {
		return nil, err
	}
	journal.State = "files_committed"
	journal.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := writeJournal(txnDir, journal); err != nil {
		return nil, err
	}
	state.Status = "applied"
	state.AppliedAt = now.Format(time.RFC3339)
	state.UpdatedAt = now.Format(time.RFC3339)
	if err := writeState(cfg, changeID, state); err != nil {
		return nil, err
	}
	return result, nil
}

func CompleteOperation(cfg *config.Instance, operationID string) error {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return err
	}
	if !document.ValidID("op", operationID) {
		return errors.New("invalid operation id")
	}
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return err
	}
	defer lock.Close()
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", operationID)
	b, err := os.ReadFile(filepath.Join(txnDir, "journal.json"))
	if err != nil {
		return err
	}
	var journal Journal
	if err := decodeStrict(b, &journal); err != nil {
		return err
	}
	if err := validateJournal(cfg, journal, operationID); err != nil {
		return err
	}
	if journal.State == "complete" {
		return nil
	}
	if journal.State != "files_committed" {
		return fmt.Errorf("operation %s is %s, expected files_committed", operationID, journal.State)
	}
	journal.State = "complete"
	journal.UpdatedAt = time.Now().Format(time.RFC3339)
	return writeJournal(txnDir, journal)
}

func Reject(cfg *config.Instance, changeID, reason string, now time.Time) (State, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return State{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return State{}, err
	}
	defer lock.Close()
	_, state, err := Load(cfg, changeID)
	if err != nil {
		return State{}, err
	}
	if state.Status != "proposed" {
		return State{}, fmt.Errorf("change is %s, expected proposed", state.Status)
	}
	state.Status = "rejected"
	state.Reason = reason
	state.RejectedAt = now.Format(time.RFC3339)
	state.UpdatedAt = now.Format(time.RFC3339)
	if err := writeState(cfg, changeID, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func Recover(cfg *config.Instance) ([]RecoveryAction, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return recoverLocked(cfg)
}

func recoverLocked(cfg *config.Instance) ([]RecoveryAction, error) {
	root := filepath.Join(cfg.RuntimeDir(), "transactions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var actions []RecoveryAction
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		txnDir := filepath.Join(root, entry.Name())
		journalBytes, err := os.ReadFile(filepath.Join(txnDir, "journal.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return actions, err
		}
		var journal Journal
		if err := decodeStrict(journalBytes, &journal); err != nil {
			return actions, err
		}
		if err := validateJournal(cfg, journal, entry.Name()); err != nil {
			return actions, err
		}
		switch journal.State {
		case "prepared":
			target := filepath.Join(cfg.Root, filepath.FromSlash(journal.TargetPath))
			if journal.HadTarget {
				backup, err := os.ReadFile(filepath.Join(txnDir, "backup.md"))
				if err != nil {
					return actions, err
				}
				restoreBackup := true
				current, readErr := os.ReadFile(target)
				if readErr == nil {
					currentHash := document.HashBytes(current)
					backupHash := document.HashBytes(backup)
					if currentHash != backupHash && currentHash != journal.NewFileHash {
						return actions, fmt.Errorf("operation %s target changed outside recovery", journal.OperationID)
					}
					if currentHash == backupHash {
						restoreBackup = false
					}
				} else if !errors.Is(readErr, os.ErrNotExist) {
					return actions, readErr
				}
				if restoreBackup {
					if err := document.AtomicWrite(target, backup, 0o600); err != nil {
						return actions, err
					}
				}
			} else {
				current, readErr := os.ReadFile(target)
				if readErr == nil {
					if document.HashBytes(current) != journal.NewFileHash {
						return actions, fmt.Errorf("operation %s new target changed outside recovery", journal.OperationID)
					}
					if err := os.Remove(target); err != nil {
						return actions, err
					}
				} else if !errors.Is(readErr, os.ErrNotExist) {
					return actions, readErr
				}
			}
			journal.State = "rolled_back"
			journal.UpdatedAt = time.Now().Format(time.RFC3339)
			if err := writeJournal(txnDir, journal); err != nil {
				return actions, err
			}
			actions = append(actions, RecoveryAction{OperationID: journal.OperationID, Previous: "prepared", Action: "rolled_back"})
		case "files_committed":
			target := filepath.Join(cfg.Root, filepath.FromSlash(journal.TargetPath))
			current, err := os.ReadFile(target)
			if err != nil {
				return actions, err
			}
			if document.HashBytes(current) != journal.NewFileHash {
				return actions, fmt.Errorf("operation %s committed target changed outside recovery", journal.OperationID)
			}
			proposal, state, err := Load(cfg, journal.ChangeID)
			if err != nil {
				return actions, err
			}
			if proposal.TargetPath != journal.TargetPath || proposal.FileHash != journal.NewFileHash {
				return actions, fmt.Errorf("operation %s journal does not match its proposal", journal.OperationID)
			}
			if state.Status == "proposed" {
				state.Status = "applied"
				state.AppliedAt = time.Now().Format(time.RFC3339)
				state.UpdatedAt = state.AppliedAt
				if err := writeState(cfg, journal.ChangeID, state); err != nil {
					return actions, err
				}
			}
			actions = append(actions, RecoveryAction{OperationID: journal.OperationID, Previous: "files_committed", Action: "index_required"})
		case "complete", "rolled_back":
		}
	}
	return actions, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateProposal(cfg *config.Instance, proposal Proposal, expectedID string) error {
	if proposal.SchemaVersion != 1 || proposal.ID != expectedID || !document.ValidID("chg", proposal.ID) {
		return errors.New("invalid proposal identity or schema")
	}
	if _, err := time.Parse(time.RFC3339, proposal.CreatedAt); err != nil {
		return errors.New("invalid proposal created_at")
	}
	if !document.ValidID("know", proposal.KnowledgeID) || len(proposal.Sources) == 0 {
		return errors.New("invalid proposal knowledge or sources")
	}
	seen := map[string]bool{}
	for _, source := range proposal.Sources {
		if !document.ValidID("raw", source.ID) || !document.ValidHash(source.ContentHash) || seen[source.ID] {
			return errors.New("invalid or duplicate proposal source")
		}
		seen[source.ID] = true
	}
	if !document.ValidHash(proposal.NewContentHash) || !document.ValidHash(proposal.FileHash) {
		return errors.New("invalid proposal content hash")
	}
	if (proposal.BaseHash == "") != (proposal.BaseFileHash == "") {
		return errors.New("proposal base hashes must both be set or omitted")
	}
	if proposal.BaseHash != "" && (!document.ValidHash(proposal.BaseHash) || !document.ValidHash(proposal.BaseFileHash)) {
		return errors.New("invalid proposal base hash")
	}
	if proposal.DraftFile != "files/document.md" {
		return errors.New("invalid proposal draft path")
	}
	cleanTarget := filepath.ToSlash(filepath.Clean(filepath.FromSlash(proposal.TargetPath)))
	knowledgeRoot := filepath.ToSlash(filepath.Clean(cfg.Paths.Knowledge))
	if cleanTarget != proposal.TargetPath || !strings.HasPrefix(cleanTarget, knowledgeRoot+"/") {
		return errors.New("proposal target is outside the knowledge directory")
	}
	target := filepath.Join(cfg.Root, filepath.FromSlash(cleanTarget))
	if err := vault.EnsureInside(cfg.KnowledgeDir(), target); err != nil {
		return err
	}
	return fsutil.EnsureNoSymlinkPath(cfg.Root, target)
}

func validateState(state State) error {
	if state.SchemaVersion != 1 || !document.ValidHash(state.ProposalHash) {
		return errors.New("invalid change state schema or proposal hash")
	}
	if _, err := time.Parse(time.RFC3339, state.UpdatedAt); err != nil {
		return errors.New("invalid change state updated_at")
	}
	switch state.Status {
	case "proposed", "applied", "rejected", "stale":
		return nil
	default:
		return fmt.Errorf("invalid change state %q", state.Status)
	}
}

func validateJournal(cfg *config.Instance, journal Journal, expectedOperationID string) error {
	if journal.SchemaVersion != 1 || journal.OperationID != expectedOperationID || !document.ValidID("op", journal.OperationID) {
		return errors.New("invalid transaction journal identity or schema")
	}
	if !document.ValidID("chg", journal.ChangeID) || !document.ValidHash(journal.NewFileHash) {
		return errors.New("invalid transaction journal change or hash")
	}
	cleanTarget := filepath.ToSlash(filepath.Clean(filepath.FromSlash(journal.TargetPath)))
	knowledgeRoot := filepath.ToSlash(filepath.Clean(cfg.Paths.Knowledge))
	if cleanTarget != journal.TargetPath || !strings.HasPrefix(cleanTarget, knowledgeRoot+"/") {
		return errors.New("transaction target is outside the knowledge directory")
	}
	if _, err := time.Parse(time.RFC3339, journal.CreatedAt); err != nil {
		return errors.New("invalid transaction created_at")
	}
	if _, err := time.Parse(time.RFC3339, journal.UpdatedAt); err != nil {
		return errors.New("invalid transaction updated_at")
	}
	switch journal.State {
	case "prepared", "files_committed", "complete", "rolled_back":
	default:
		return fmt.Errorf("invalid transaction state %q", journal.State)
	}
	target := filepath.Join(cfg.Root, filepath.FromSlash(cleanTarget))
	if err := vault.EnsureInside(cfg.KnowledgeDir(), target); err != nil {
		return err
	}
	return fsutil.EnsureNoSymlinkPath(cfg.Root, target)
}

func validateProposalBase(cfg *config.Instance, proposal Proposal) error {
	for _, source := range proposal.Sources {
		doc, err := document.FindByID(cfg.RawDir(), source.ID)
		if err != nil {
			return fmt.Errorf("source %s is missing", source.ID)
		}
		if actual, err := doc.ActualContentHash(); err != nil || actual != source.ContentHash || doc.Metadata.ContentHash != source.ContentHash {
			return fmt.Errorf("source %s changed after proposal", source.ID)
		}
	}
	target := filepath.Join(cfg.Root, filepath.FromSlash(proposal.TargetPath))
	if err := vault.EnsureInside(cfg.Root, target); err != nil {
		return err
	}
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
		return err
	}
	if proposal.BaseHash == "" {
		if _, err := os.Stat(target); err == nil {
			return errors.New("target knowledge was created after proposal")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	existing, err := document.FindByID(cfg.KnowledgeDir(), proposal.KnowledgeID)
	if err != nil {
		return errors.New("target knowledge is missing after proposal")
	}
	rel, _ := filepath.Rel(cfg.Root, existing.Path)
	currentBytes, readErr := os.ReadFile(existing.Path)
	if readErr != nil {
		return readErr
	}
	if filepath.ToSlash(rel) != proposal.TargetPath || existing.Metadata.ContentHash != proposal.BaseHash ||
		proposal.BaseFileHash == "" || document.HashBytes(currentBytes) != proposal.BaseFileHash {
		return errors.New("target knowledge changed after proposal")
	}
	return nil
}

func changeDir(cfg *config.Instance, id string) string {
	return filepath.Join(cfg.RuntimeDir(), "changes", id)
}

func writeState(cfg *config.Instance, id string, state State) error {
	return document.AtomicWrite(filepath.Join(changeDir(cfg, id), "state.json"), mustJSON(state), 0o600)
}

func writeJournal(dir string, journal Journal) error {
	return document.AtomicWrite(filepath.Join(dir, "journal.json"), mustJSON(journal), 0o600)
}

func copyFile(source, target string) error {
	b, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return document.AtomicWrite(target, b, 0o600)
}

func marshalJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func mustJSON(v any) []byte {
	b, err := marshalJSON(v)
	if err != nil {
		panic(err)
	}
	return b
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

// mergeUserProperties preserves Obsidian/custom YAML properties while keeping
// the typed Metadata fields under llm-wiki control. A null draft value is an
// explicit request to remove a custom property; omission preserves it.
func mergeUserProperties(base, draft map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(draft))
	for key, value := range base {
		if value != nil {
			out[key] = value
		}
	}
	for key, value := range draft {
		if value == nil {
			delete(out, key)
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstHeading(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func lineDiff(path string, oldData, newData []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n@@ full document @@\n", path, path)
	if len(oldData) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(string(oldData), "\n"), "\n") {
			fmt.Fprintf(&b, "-%s\n", line)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(newData), "\n"), "\n") {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	return b.String()
}
