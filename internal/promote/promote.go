package promote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/inbox"
	"llm-wiki/internal/vault"
)

const SchemaVersion = 1

type Manifest struct {
	SchemaVersion int              `json:"schema_version"`
	Inboxes       []ManifestInbox  `json:"inboxes"`
	Targets       []ManifestTarget `json:"targets"`
}

type ManifestInbox struct {
	ID          string `json:"id"`
	PayloadHash string `json:"payload_hash"`
	ItemHash    string `json:"item_hash"`
	Consume     bool   `json:"consume"`
}

type ManifestTarget struct {
	Operation       string   `json:"operation"`
	DraftFile       string   `json:"draft_file"`
	InboxIDs        []string `json:"inbox_ids"`
	KnowledgeID     string   `json:"knowledge_id,omitempty"`
	TargetPath      string   `json:"target_path,omitempty"`
	BaseContentHash string   `json:"base_content_hash,omitempty"`
	BaseFileHash    string   `json:"base_file_hash,omitempty"`
}

type Plan struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	CreatedAt     string      `json:"created_at"`
	ManifestHash  string      `json:"manifest_hash"`
	DiffHash      string      `json:"diff_hash"`
	Inboxes       []PlanInbox `json:"inboxes"`
	Targets       []Target    `json:"targets"`
}

type PlanInbox struct {
	ID          string `json:"id"`
	PayloadHash string `json:"payload_hash"`
	ItemHash    string `json:"item_hash"`
	Consume     bool   `json:"consume"`
}

type Target struct {
	Operation       string   `json:"operation"`
	KnowledgeID     string   `json:"knowledge_id"`
	TargetPath      string   `json:"target_path"`
	FrozenFile      string   `json:"frozen_file"`
	InboxIDs        []string `json:"inbox_ids"`
	BaseContentHash string   `json:"base_content_hash,omitempty"`
	BaseFileHash    string   `json:"base_file_hash,omitempty"`
	NewContentHash  string   `json:"new_content_hash"`
	FileHash        string   `json:"file_hash"`
}

type State struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	PlanHash      string `json:"plan_hash"`
	UpdatedAt     string `json:"updated_at"`
	Reason        string `json:"reason,omitempty"`
	AppliedAt     string `json:"applied_at,omitempty"`
	RejectedAt    string `json:"rejected_at,omitempty"`
}

type PlanOptions struct {
	ManifestPath string
	DryRun       bool
	Now          time.Time
}

type PlanResult struct {
	Plan     Plan   `json:"plan"`
	State    State  `json:"state"`
	PlanHash string `json:"plan_hash"`
	Diff     string `json:"diff"`
}

type DiffResult struct {
	PromotionID string `json:"promotion_id"`
	PlanHash    string `json:"plan_hash"`
	Diff        string `json:"diff"`
}

type AppliedTarget struct {
	KnowledgeID string `json:"knowledge_id"`
	TargetPath  string `json:"target_path"`
	ContentHash string `json:"content_hash"`
}

type ApplyResult struct {
	PromotionID string          `json:"promotion_id"`
	PlanHash    string          `json:"plan_hash"`
	OperationID string          `json:"operation_id,omitempty"`
	Targets     []AppliedTarget `json:"targets"`
	Consumed    []string        `json:"consumed_inbox_ids"`
	DryRun      bool            `json:"dry_run"`
}

type JournalFile struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	NewHash    string `json:"new_hash"`
	HadTarget  bool   `json:"had_target"`
	BackupFile string `json:"backup_file,omitempty"`
	StageFile  string `json:"stage_file"`
}

type Journal struct {
	SchemaVersion int           `json:"schema_version"`
	OperationID   string        `json:"operation_id"`
	PromotionID   string        `json:"promotion_id"`
	State         string        `json:"state"`
	Files         []JournalFile `json:"files"`
	CreatedAt     string        `json:"created_at"`
	UpdatedAt     string        `json:"updated_at"`
}

type RecoveryAction struct {
	OperationID string `json:"operation_id"`
	Previous    string `json:"previous_state"`
	Action      string `json:"action"`
}

var ErrApplyConflict = errors.New("promotion apply conflict")

type ApplyConflictError struct {
	Kind  string
	Cause error
}

func (e *ApplyConflictError) Error() string { return e.Cause.Error() }
func (e *ApplyConflictError) Unwrap() error { return e.Cause }
func (e *ApplyConflictError) Is(target error) bool {
	return target == ErrApplyConflict || errors.Is(e.Cause, target)
}

func conflict(kind string, cause error) error {
	return &ApplyConflictError{Kind: kind, Cause: cause}
}

func PlanPromotion(cfg *config.Instance, opts PlanOptions) (*PlanResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if opts.ManifestPath == "" {
		return nil, errors.New("promotion manifest is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
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
	manifestBytes, err := readManagedInput(opts.ManifestPath, cfg.Security.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeStrict(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse promotion manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	inboxDocs := map[string]*document.Document{}
	planInboxes := make([]PlanInbox, 0, len(manifest.Inboxes))
	for _, input := range manifest.Inboxes {
		doc, err := inbox.Show(cfg, input.ID)
		if err != nil {
			return nil, fmt.Errorf("inbox %s: %w", input.ID, err)
		}
		if doc.Metadata.Status != "pending" {
			return nil, fmt.Errorf("inbox %s is %s, expected pending", input.ID, doc.Metadata.Status)
		}
		itemHash, err := document.HashFile(doc.Path)
		if err != nil {
			return nil, err
		}
		if input.PayloadHash != doc.Metadata.PayloadHash || input.ItemHash != itemHash {
			return nil, fmt.Errorf("inbox %s hash does not match manifest", input.ID)
		}
		inboxDocs[input.ID] = doc
		planInboxes = append(planInboxes, PlanInbox(input))
	}
	sort.Slice(planInboxes, func(i, j int) bool { return planInboxes[i].ID < planInboxes[j].ID })
	promotionID, err := document.NewID("prm", opts.Now)
	if err != nil {
		return nil, err
	}
	manifestBase := filepath.Dir(opts.ManifestPath)
	targets := make([]Target, 0, len(manifest.Targets))
	frozen := map[string][]byte{}
	oldFiles := map[string][]byte{}
	prospective := map[string]*document.Document{}
	seenTargetPaths := map[string]bool{}
	seenKnowledge := map[string]bool{}
	for _, spec := range manifest.Targets {
		target, finalBytes, oldBytes, doc, err := prepareTarget(cfg, spec, manifestBase, inboxDocs, opts.Now)
		if err != nil {
			return nil, err
		}
		if seenKnowledge[target.KnowledgeID] || seenTargetPaths[target.TargetPath] {
			return nil, fmt.Errorf("duplicate promotion target %s", target.KnowledgeID)
		}
		seenKnowledge[target.KnowledgeID] = true
		seenTargetPaths[target.TargetPath] = true
		target.FrozenFile = filepath.ToSlash(filepath.Join("files", target.KnowledgeID+".md"))
		targets = append(targets, target)
		frozen[target.FrozenFile] = finalBytes
		oldFiles[target.TargetPath] = oldBytes
		prospective[target.KnowledgeID] = doc
	}
	for _, target := range targets {
		doc := prospective[target.KnowledgeID]
		if err := governance.ValidateForPromotion(cfg, doc, prospective, opts.Now); err != nil {
			return nil, fmt.Errorf("knowledge %s governance: %w", target.KnowledgeID, err)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].TargetPath < targets[j].TargetPath })
	diff := promotionDiff(targets, oldFiles, frozen)
	plan := Plan{
		SchemaVersion: SchemaVersion, ID: promotionID, CreatedAt: opts.Now.Format(time.RFC3339),
		ManifestHash: document.HashBytes(manifestBytes), DiffHash: document.HashBytes([]byte(diff)), Inboxes: planInboxes, Targets: targets,
	}
	planBytes, err := marshalJSON(plan)
	if err != nil {
		return nil, err
	}
	planHash := document.HashBytes(planBytes)
	state := State{SchemaVersion: SchemaVersion, Status: "planned", PlanHash: planHash, UpdatedAt: opts.Now.Format(time.RFC3339)}
	stateBytes, err := marshalJSON(state)
	if err != nil {
		return nil, err
	}
	result := &PlanResult{Plan: plan, State: state, PlanHash: planHash, Diff: diff}
	if opts.DryRun {
		return result, nil
	}
	targetDir := promotionDir(cfg, promotionID)
	if _, err := os.Lstat(targetDir); err == nil {
		return nil, errors.New("promotion id collision")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	stage := filepath.Join(cfg.RuntimeDir(), "transactions", promotionID+"-plan", "promotion")
	if err := os.MkdirAll(filepath.Join(stage, "files"), 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Dir(stage))
	if err := document.AtomicWrite(filepath.Join(stage, "plan.json"), planBytes, 0o600); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(stage, "state.json"), stateBytes, 0o600); err != nil {
		return nil, err
	}
	if err := document.AtomicWrite(filepath.Join(stage, "diff.patch"), []byte(diff), 0o600); err != nil {
		return nil, err
	}
	for name, data := range frozen {
		if err := document.AtomicWrite(filepath.Join(stage, filepath.FromSlash(name)), data, 0o600); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o700); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, targetDir); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareTarget(cfg *config.Instance, spec ManifestTarget, base string, inboxDocs map[string]*document.Document, now time.Time) (Target, []byte, []byte, *document.Document, error) {
	draftPath := spec.DraftFile
	if !filepath.IsAbs(draftPath) {
		draftPath = filepath.Join(base, draftPath)
	}
	draftBytes, err := readManagedInput(draftPath, cfg.Security.MaxInputBytes)
	if err != nil {
		return Target{}, nil, nil, nil, fmt.Errorf("draft %s: %w", spec.DraftFile, err)
	}
	draftMeta := document.Metadata{}
	body := document.NormalizeMarkdownBody(draftBytes)
	if bytes.HasPrefix(body, []byte("---\n")) || bytes.HasPrefix(body, []byte("---\r\n")) {
		draftMeta, body, err = document.Parse(body)
		if err != nil {
			return Target{}, nil, nil, nil, err
		}
	}
	var existing *document.Document
	var oldBytes []byte
	knowledgeID := spec.KnowledgeID
	targetPath := spec.TargetPath
	publishedAt := now.Format(time.RFC3339)
	lineage := []document.LineageRef{}
	baseContentHash := ""
	baseFileHash := ""
	if spec.Operation == "update" {
		existing, err = document.FindByID(cfg.KnowledgeDir(), spec.KnowledgeID)
		if err != nil {
			return Target{}, nil, nil, nil, fmt.Errorf("update target %s: %w", spec.KnowledgeID, err)
		}
		if err := existing.Validate("knowledge", true); err != nil {
			return Target{}, nil, nil, nil, err
		}
		oldBytes, err = os.ReadFile(existing.Path)
		if err != nil {
			return Target{}, nil, nil, nil, err
		}
		baseContentHash = existing.Metadata.ContentHash
		baseFileHash = document.HashBytes(oldBytes)
		if spec.BaseContentHash != baseContentHash || spec.BaseFileHash != baseFileHash {
			return Target{}, nil, nil, nil, fmt.Errorf("update target %s baseline hash does not match", spec.KnowledgeID)
		}
		rel, _ := filepath.Rel(cfg.Root, existing.Path)
		canonical := filepath.ToSlash(rel)
		if targetPath != "" && targetPath != canonical {
			return Target{}, nil, nil, nil, errors.New("update target_path is not canonical")
		}
		targetPath = canonical
		publishedAt = existing.Metadata.PublishedAt
		lineage = append(lineage, existing.Metadata.Lineage...)
		if draftMeta.Type == "" {
			draftMeta.Type = existing.Metadata.Type
		}
		if draftMeta.Title == "" {
			draftMeta.Title = existing.Metadata.Title
		}
	} else {
		if knowledgeID == "" {
			knowledgeID, err = document.NewID("know", now)
			if err != nil {
				return Target{}, nil, nil, nil, err
			}
		}
		if !document.ValidID("know", knowledgeID) {
			return Target{}, nil, nil, nil, errors.New("create target knowledge_id is invalid")
		}
		if _, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID); err == nil {
			return Target{}, nil, nil, nil, fmt.Errorf("create target knowledge id %s already exists", knowledgeID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Target{}, nil, nil, nil, err
		}
		if draftMeta.Type == "" {
			draftMeta.Type = "concept"
		}
		if draftMeta.Title == "" {
			draftMeta.Title = firstHeading(body)
		}
	}
	if !document.ValidID("know", knowledgeID) || strings.TrimSpace(draftMeta.Title) == "" {
		return Target{}, nil, nil, nil, errors.New("target requires valid knowledge id and title")
	}
	if targetPath == "" {
		targetPath = filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, draftMeta.Type, document.Slug(draftMeta.Title)+"--"+knowledgeID+".md"))
	}
	if err := validateKnowledgePath(cfg, targetPath, knowledgeID); err != nil {
		return Target{}, nil, nil, nil, err
	}
	lineageByID := map[string]document.LineageRef{}
	for _, item := range lineage {
		lineageByID[item.InboxID] = item
	}
	inboxIDs := append([]string(nil), spec.InboxIDs...)
	sort.Strings(inboxIDs)
	for _, id := range inboxIDs {
		item := inboxDocs[id]
		if item == nil {
			return Target{}, nil, nil, nil, fmt.Errorf("target references undeclared inbox %s", id)
		}
		lineageByID[id] = document.LineageRef{InboxID: id, PayloadHash: item.Metadata.PayloadHash, Source: item.Metadata.Source, CapturedAt: item.Metadata.CapturedAt}
	}
	lineage = lineage[:0]
	for _, item := range lineageByID {
		lineage = append(lineage, item)
	}
	sort.Slice(lineage, func(i, j int) bool { return lineage[i].InboxID < lineage[j].InboxID })
	tags, aliases, extra := draftMeta.Tags, draftMeta.Aliases, copyExtra(draftMeta.Extra)
	if existing != nil {
		if tags == nil {
			tags = existing.Metadata.Tags
		}
		if aliases == nil {
			aliases = existing.Metadata.Aliases
		}
		extra = mergeExtra(existing.Metadata.Extra, extra)
	}
	meta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: knowledgeID, Type: draftMeta.Type, Title: draftMeta.Title,
		Status: "published", PublishedAt: publishedAt, UpdatedAt: now.Format(time.RFC3339), ContentHash: document.HashBytes(body),
		Lineage: lineage, Tags: cleanStrings(tags), Aliases: cleanStrings(aliases), GovernanceVersion: governance.PersonalGovernanceVersion, Extra: extra,
	}
	if expected := document.KnowledgePath(cfg.Paths.Knowledge, meta); targetPath != expected {
		return Target{}, nil, nil, nil, fmt.Errorf("knowledge target path is not canonical: expected %s", expected)
	}
	finalBytes, err := document.Render(meta, body)
	if err != nil {
		return Target{}, nil, nil, nil, err
	}
	doc := &document.Document{Path: filepath.Join(cfg.Root, filepath.FromSlash(targetPath)), Metadata: meta, Body: body}
	if err := doc.Validate("knowledge", true); err != nil {
		return Target{}, nil, nil, nil, err
	}
	target := Target{
		Operation: spec.Operation, KnowledgeID: knowledgeID, TargetPath: targetPath, InboxIDs: inboxIDs,
		BaseContentHash: baseContentHash, BaseFileHash: baseFileHash, NewContentHash: meta.ContentHash, FileHash: document.HashBytes(finalBytes),
	}
	return target, finalBytes, oldBytes, doc, nil
}

func Load(cfg *config.Instance, promotionID string) (Plan, State, []byte, error) {
	var plan Plan
	var state State
	if !document.ValidID("prm", promotionID) {
		return plan, state, nil, errors.New("invalid promotion id")
	}
	dir := promotionDir(cfg, promotionID)
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, dir); err != nil {
		return plan, state, nil, err
	}
	planBytes, err := readRegularExact(filepath.Join(dir, "plan.json"))
	if err != nil {
		return plan, state, nil, err
	}
	if err := decodeStrict(planBytes, &plan); err != nil {
		return plan, state, nil, err
	}
	if err := validatePlan(cfg, plan, promotionID); err != nil {
		return plan, state, nil, err
	}
	stateBytes, err := readRegularExact(filepath.Join(dir, "state.json"))
	if err != nil {
		return plan, state, nil, err
	}
	if err := decodeStrict(stateBytes, &state); err != nil {
		return plan, state, nil, err
	}
	if err := validateState(state); err != nil {
		return plan, state, nil, err
	}
	if state.PlanHash != document.HashBytes(planBytes) {
		return plan, state, nil, errors.New("promotion plan integrity hash mismatch")
	}
	return plan, state, planBytes, nil
}

func Diff(cfg *config.Instance, promotionID string) (*DiffResult, error) {
	plan, state, _, err := Load(cfg, promotionID)
	if err != nil {
		return nil, err
	}
	b, err := readRegularExact(filepath.Join(promotionDir(cfg, promotionID), "diff.patch"))
	if err != nil {
		return nil, err
	}
	if document.HashBytes(b) != plan.DiffHash {
		return nil, errors.New("promotion diff integrity hash mismatch")
	}
	return &DiffResult{PromotionID: promotionID, PlanHash: state.PlanHash, Diff: string(b)}, nil
}

func Apply(cfg *config.Instance, promotionID, approve string, dryRun bool, now time.Time) (*ApplyResult, error) {
	if err := vault.EnsureSafeManagedPaths(cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(approve) == "" {
		return nil, conflict("approval", errors.New("--approve plan hash is required"))
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
	plan, state, _, err := Load(cfg, promotionID)
	if err != nil {
		if markStaleAfterLoadFailure(cfg, promotionID, err, now, dryRun) {
			return nil, conflict("baseline", err)
		}
		return nil, err
	}
	if state.Status != "planned" {
		return nil, conflict("state", fmt.Errorf("promotion is %s, expected planned", state.Status))
	}
	if approve != state.PlanHash {
		return nil, conflict("approval", errors.New("approval hash does not match frozen plan"))
	}
	files, inboxDocs, err := validateApplyBase(cfg, plan, now)
	if err != nil {
		state.Status = "stale"
		state.Reason = err.Error()
		state.UpdatedAt = now.Format(time.RFC3339)
		if !dryRun {
			if stateErr := writeState(cfg, promotionID, state); stateErr != nil {
				return nil, fmt.Errorf("mark promotion stale: %w", stateErr)
			}
		}
		return nil, conflict("baseline", err)
	}
	result := &ApplyResult{PromotionID: promotionID, PlanHash: state.PlanHash, DryRun: dryRun}
	knowledgeByInbox := map[string][]string{}
	for _, target := range plan.Targets {
		result.Targets = append(result.Targets, AppliedTarget{KnowledgeID: target.KnowledgeID, TargetPath: target.TargetPath, ContentHash: target.NewContentHash})
		for _, id := range target.InboxIDs {
			knowledgeByInbox[id] = append(knowledgeByInbox[id], target.KnowledgeID)
		}
	}
	for _, input := range plan.Inboxes {
		if !input.Consume {
			continue
		}
		doc := inboxDocs[input.ID]
		ids := cleanStrings(knowledgeByInbox[input.ID])
		if len(ids) == 0 {
			return nil, errors.New("consumed inbox is not used by a promotion target")
		}
		doc.Metadata.Status = "processed"
		doc.Metadata.ProcessedAt = now.Format(time.RFC3339)
		doc.Metadata.KnowledgeIDs = ids
		itemBytes, err := document.Render(doc.Metadata, doc.Body)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(cfg.Root, doc.Path)
		files[filepath.ToSlash(rel)] = itemBytes
		result.Consumed = append(result.Consumed, input.ID)
	}
	state.Status = "applied"
	state.AppliedAt = now.Format(time.RFC3339)
	state.UpdatedAt = now.Format(time.RFC3339)
	state.Reason = ""
	stateBytes, err := marshalJSON(state)
	if err != nil {
		return nil, err
	}
	files[filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "promotions", promotionID, "state.json"))] = stateBytes
	if dryRun {
		return result, nil
	}
	opID, err := document.NewID("op", now)
	if err != nil {
		return nil, err
	}
	result.OperationID = opID
	if err := commitFiles(cfg, promotionID, opID, files, now); err != nil {
		return nil, err
	}
	return result, nil
}

func markStaleAfterLoadFailure(cfg *config.Instance, promotionID string, cause error, now time.Time, dryRun bool) bool {
	if dryRun || !document.ValidID("prm", promotionID) {
		return false
	}
	data, err := readRegularExact(filepath.Join(promotionDir(cfg, promotionID), "state.json"))
	if err != nil {
		return false
	}
	var state State
	if decodeStrict(data, &state) != nil || validateState(state) != nil || state.Status != "planned" {
		return false
	}
	state.Status = "stale"
	state.Reason = cause.Error()
	state.UpdatedAt = now.Format(time.RFC3339)
	return writeState(cfg, promotionID, state) == nil
}

func validateApplyBase(cfg *config.Instance, plan Plan, now time.Time) (map[string][]byte, map[string]*document.Document, error) {
	files := map[string][]byte{}
	inboxDocs := map[string]*document.Document{}
	diffBytes, err := readRegularExact(filepath.Join(promotionDir(cfg, plan.ID), "diff.patch"))
	if err != nil || document.HashBytes(diffBytes) != plan.DiffHash {
		return nil, nil, errors.New("promotion diff changed after plan")
	}
	for _, input := range plan.Inboxes {
		doc, err := inbox.Show(cfg, input.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("inbox %s: %w", input.ID, err)
		}
		if doc.Metadata.Status != "pending" || doc.Metadata.PayloadHash != input.PayloadHash {
			return nil, nil, fmt.Errorf("inbox %s changed after plan", input.ID)
		}
		itemHash, err := document.HashFile(doc.Path)
		if err != nil || itemHash != input.ItemHash {
			return nil, nil, fmt.Errorf("inbox %s item changed after plan", input.ID)
		}
		inboxDocs[input.ID] = doc
	}
	prospective := map[string]*document.Document{}
	for _, target := range plan.Targets {
		frozenPath := filepath.Join(promotionDir(cfg, plan.ID), filepath.FromSlash(target.FrozenFile))
		data, err := readRegularExact(frozenPath)
		if err != nil {
			return nil, nil, err
		}
		if document.HashBytes(data) != target.FileHash {
			return nil, nil, fmt.Errorf("frozen file for %s changed after plan", target.KnowledgeID)
		}
		meta, body, err := document.Parse(data)
		if err != nil {
			return nil, nil, err
		}
		if meta.ID != target.KnowledgeID || meta.ContentHash != target.NewContentHash || document.HashBytes(body) != target.NewContentHash {
			return nil, nil, errors.New("frozen knowledge metadata does not match plan")
		}
		if expected := document.KnowledgePath(cfg.Paths.Knowledge, meta); target.TargetPath != expected {
			return nil, nil, fmt.Errorf("frozen knowledge path is not canonical: expected %s", expected)
		}
		path := filepath.Join(cfg.Root, filepath.FromSlash(target.TargetPath))
		if target.Operation == "create" {
			if _, err := os.Lstat(path); err == nil {
				return nil, nil, fmt.Errorf("create target appeared after plan: %s", target.TargetPath)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		} else {
			current, err := readRegularExact(path)
			if err != nil {
				return nil, nil, err
			}
			currentMeta, currentBody, err := document.Parse(current)
			if err != nil {
				return nil, nil, err
			}
			if currentMeta.ID != target.KnowledgeID || document.HashBytes(currentBody) != target.BaseContentHash || document.HashBytes(current) != target.BaseFileHash {
				return nil, nil, fmt.Errorf("update target %s changed after plan", target.KnowledgeID)
			}
		}
		doc := &document.Document{Path: path, Metadata: meta, Body: body}
		if err := doc.Validate("knowledge", true); err != nil {
			return nil, nil, err
		}
		prospective[target.KnowledgeID] = doc
		files[target.TargetPath] = data
	}
	for id, doc := range prospective {
		if err := governance.ValidateForPromotion(cfg, doc, prospective, now); err != nil {
			return nil, nil, fmt.Errorf("knowledge %s governance: %w", id, err)
		}
	}
	return files, inboxDocs, nil
}

func commitFiles(cfg *config.Instance, promotionID, opID string, files map[string][]byte, now time.Time) error {
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	stageDir := filepath.Join(txnDir, "stage")
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	journal := Journal{SchemaVersion: SchemaVersion, OperationID: opID, PromotionID: promotionID, State: "prepared", CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339)}
	for i, rel := range paths {
		target := filepath.Join(cfg.Root, filepath.FromSlash(rel))
		if err := vault.EnsureInside(cfg.Root, target); err != nil {
			return err
		}
		if err := fsutil.EnsureNoSymlinkPath(cfg.Root, target); err != nil {
			return err
		}
		stageRel := filepath.ToSlash(filepath.Join("stage", fmt.Sprintf("%04d", i)))
		stagePath := filepath.Join(txnDir, filepath.FromSlash(stageRel))
		if err := document.AtomicWrite(stagePath, files[rel], 0o600); err != nil {
			return err
		}
		entry := JournalFile{Kind: fileKind(cfg, rel), Path: rel, NewHash: document.HashBytes(files[rel]), StageFile: stageRel}
		if current, err := readRegularExact(target); err == nil {
			entry.HadTarget = true
			entry.BackupFile = filepath.ToSlash(filepath.Join("backup", fmt.Sprintf("%04d", i)))
			if err := document.AtomicWrite(filepath.Join(txnDir, filepath.FromSlash(entry.BackupFile)), current, 0o600); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		journal.Files = append(journal.Files, entry)
	}
	if err := writeJournal(txnDir, journal); err != nil {
		return err
	}
	for _, entry := range journal.Files {
		data, err := readRegularExact(filepath.Join(txnDir, filepath.FromSlash(entry.StageFile)))
		if err != nil {
			return err
		}
		if err := document.AtomicWrite(filepath.Join(cfg.Root, filepath.FromSlash(entry.Path)), data, 0o600); err != nil {
			return err
		}
	}
	journal.State = "files_committed"
	journal.UpdatedAt = now.Format(time.RFC3339)
	return writeJournal(txnDir, journal)
}

func Reject(cfg *config.Instance, promotionID, reason string, now time.Time) (State, error) {
	if now.IsZero() {
		now = time.Now()
	}
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return State{}, err
	}
	defer lock.Close()
	_, state, _, err := Load(cfg, promotionID)
	if err != nil {
		return State{}, err
	}
	if state.Status != "planned" {
		return State{}, fmt.Errorf("promotion is %s, expected planned", state.Status)
	}
	state.Status = "rejected"
	state.Reason = strings.TrimSpace(reason)
	state.RejectedAt = now.Format(time.RFC3339)
	state.UpdatedAt = now.Format(time.RFC3339)
	if err := writeState(cfg, promotionID, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func ActiveInboxIDs(cfg *config.Instance) (map[string]bool, error) {
	active := map[string]bool{}
	root := filepath.Join(cfg.RuntimeDir(), "promotions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return active, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !document.ValidID("prm", entry.Name()) {
			continue
		}
		plan, state, _, err := Load(cfg, entry.Name())
		if err != nil {
			return nil, err
		}
		if state.Status != "planned" {
			continue
		}
		for _, input := range plan.Inboxes {
			active[input.ID] = true
		}
	}
	return active, nil
}

func ActiveCount(cfg *config.Instance) (int, error) {
	root := filepath.Join(cfg.RuntimeDir(), "promotions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || !document.ValidID("prm", entry.Name()) {
			continue
		}
		_, state, _, err := Load(cfg, entry.Name())
		if err != nil {
			return 0, err
		}
		if state.Status == "planned" {
			count++
		}
	}
	return count, nil
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
		if !entry.IsDir() || !document.ValidID("op", entry.Name()) {
			continue
		}
		journal, err := loadJournal(cfg, entry.Name())
		if err != nil {
			return nil, err
		}
		if journal.State == "prepared" || journal.State == "files_committed" {
			pending = append(pending, journal.OperationID)
		}
	}
	sort.Strings(pending)
	return pending, nil
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
		if !entry.IsDir() || !document.ValidID("op", entry.Name()) {
			continue
		}
		journal, err := loadJournal(cfg, entry.Name())
		if err != nil {
			return actions, err
		}
		txnDir := filepath.Join(root, entry.Name())
		switch journal.State {
		case "prepared":
			for i := len(journal.Files) - 1; i >= 0; i-- {
				file := journal.Files[i]
				target := filepath.Join(cfg.Root, filepath.FromSlash(file.Path))
				current, readErr := readRegularExact(target)
				if file.HadTarget {
					backup, err := readRegularExact(filepath.Join(txnDir, filepath.FromSlash(file.BackupFile)))
					if err != nil {
						return actions, err
					}
					if readErr == nil && document.HashBytes(current) != file.NewHash && document.HashBytes(current) != document.HashBytes(backup) {
						return actions, fmt.Errorf("transaction %s target changed outside recovery", journal.OperationID)
					}
					if err := document.AtomicWrite(target, backup, 0o600); err != nil {
						return actions, err
					}
				} else if readErr == nil {
					if document.HashBytes(current) != file.NewHash {
						return actions, fmt.Errorf("transaction %s new target changed outside recovery", journal.OperationID)
					}
					if err := os.Remove(target); err != nil {
						return actions, err
					}
				} else if !errors.Is(readErr, os.ErrNotExist) {
					return actions, readErr
				}
			}
			journal.State = "complete"
			journal.UpdatedAt = time.Now().Format(time.RFC3339)
			if err := writeJournal(txnDir, journal); err != nil {
				return actions, err
			}
			actions = append(actions, RecoveryAction{OperationID: journal.OperationID, Previous: "prepared", Action: "rolled_back"})
		case "files_committed":
			for _, file := range journal.Files {
				current, err := readRegularExact(filepath.Join(cfg.Root, filepath.FromSlash(file.Path)))
				if err != nil || document.HashBytes(current) != file.NewHash {
					return actions, fmt.Errorf("transaction %s committed file is missing or changed", journal.OperationID)
				}
			}
			actions = append(actions, RecoveryAction{OperationID: journal.OperationID, Previous: "files_committed", Action: "index_required"})
		case "complete":
		default:
			return actions, fmt.Errorf("transaction %s has invalid state %q", journal.OperationID, journal.State)
		}
	}
	return actions, nil
}

func CompleteOperation(cfg *config.Instance, operationID string) error {
	if !document.ValidID("op", operationID) {
		return errors.New("invalid operation id")
	}
	lock, err := vault.AcquireWrite(cfg, 5*time.Second)
	if err != nil {
		return err
	}
	defer lock.Close()
	journal, err := loadJournal(cfg, operationID)
	if err != nil {
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
	return writeJournal(filepath.Join(cfg.RuntimeDir(), "transactions", operationID), journal)
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != SchemaVersion || len(manifest.Inboxes) == 0 || len(manifest.Targets) == 0 {
		return errors.New("promotion manifest requires schema_version 1, inboxes, and targets")
	}
	seen := map[string]bool{}
	declared := map[string]bool{}
	for _, input := range manifest.Inboxes {
		if !document.ValidID("inbox", input.ID) || !document.ValidHash(input.PayloadHash) || !document.ValidHash(input.ItemHash) || seen[input.ID] {
			return errors.New("promotion inboxes require unique id, payload_hash, and item_hash")
		}
		seen[input.ID] = true
		declared[input.ID] = true
	}
	for _, target := range manifest.Targets {
		if target.Operation != "create" && target.Operation != "update" {
			return errors.New("promotion target operation must be create or update")
		}
		if target.DraftFile == "" || filepath.IsAbs(target.DraftFile) || filepath.Clean(target.DraftFile) == ".." || strings.HasPrefix(filepath.Clean(target.DraftFile), ".."+string(filepath.Separator)) {
			return errors.New("promotion draft_file must be a safe relative path")
		}
		if len(target.InboxIDs) == 0 {
			return errors.New("promotion target requires inbox_ids")
		}
		local := map[string]bool{}
		for _, id := range target.InboxIDs {
			if !seen[id] || local[id] {
				return fmt.Errorf("promotion target has undeclared or duplicate inbox id %s", id)
			}
			local[id] = true
		}
		if target.Operation == "update" {
			if !document.ValidID("know", target.KnowledgeID) || !document.ValidHash(target.BaseContentHash) || !document.ValidHash(target.BaseFileHash) {
				return errors.New("update target requires knowledge_id and both baseline hashes")
			}
		} else if target.BaseContentHash != "" || target.BaseFileHash != "" {
			return errors.New("create target cannot declare baseline hashes")
		}
	}
	for id := range declared {
		used := false
		for _, target := range manifest.Targets {
			for _, targetID := range target.InboxIDs {
				used = used || targetID == id
			}
		}
		if !used {
			return fmt.Errorf("promotion inbox %s is unused", id)
		}
	}
	return nil
}

func validatePlan(cfg *config.Instance, plan Plan, expected string) error {
	if plan.SchemaVersion != SchemaVersion || plan.ID != expected || !document.ValidID("prm", plan.ID) || !document.ValidHash(plan.ManifestHash) || !document.ValidHash(plan.DiffHash) || len(plan.Inboxes) == 0 || len(plan.Targets) == 0 {
		return errors.New("invalid promotion plan")
	}
	if _, err := time.Parse(time.RFC3339, plan.CreatedAt); err != nil {
		return errors.New("promotion created_at must be RFC3339")
	}
	inboxes := map[string]bool{}
	for _, input := range plan.Inboxes {
		if !document.ValidID("inbox", input.ID) || !document.ValidHash(input.PayloadHash) || !document.ValidHash(input.ItemHash) || inboxes[input.ID] {
			return errors.New("promotion plan has an invalid or duplicate inbox")
		}
		inboxes[input.ID] = true
	}
	seenIDs := map[string]bool{}
	seenPaths := map[string]bool{}
	usedInboxes := map[string]bool{}
	for _, target := range plan.Targets {
		if (target.Operation != "create" && target.Operation != "update") || !document.ValidID("know", target.KnowledgeID) || seenIDs[target.KnowledgeID] || seenPaths[target.TargetPath] || !document.ValidHash(target.NewContentHash) || !document.ValidHash(target.FileHash) {
			return errors.New("invalid or duplicate promotion target")
		}
		if target.Operation == "update" {
			if !document.ValidHash(target.BaseContentHash) || !document.ValidHash(target.BaseFileHash) {
				return errors.New("promotion update target requires baseline hashes")
			}
		} else if target.BaseContentHash != "" || target.BaseFileHash != "" {
			return errors.New("promotion create target cannot declare baseline hashes")
		}
		if len(target.InboxIDs) == 0 {
			return errors.New("promotion target requires inbox ids")
		}
		targetInboxes := map[string]bool{}
		for _, id := range target.InboxIDs {
			if !inboxes[id] || targetInboxes[id] {
				return errors.New("promotion target has an undeclared or duplicate inbox id")
			}
			targetInboxes[id] = true
			usedInboxes[id] = true
		}
		if err := validateKnowledgePath(cfg, target.TargetPath, target.KnowledgeID); err != nil {
			return err
		}
		expectedFrozen := filepath.ToSlash(filepath.Join("files", target.KnowledgeID+".md"))
		if target.FrozenFile != expectedFrozen {
			return errors.New("promotion frozen file path is not canonical")
		}
		seenIDs[target.KnowledgeID] = true
		seenPaths[target.TargetPath] = true
	}
	for id := range inboxes {
		if !usedInboxes[id] {
			return errors.New("promotion plan has an unused inbox")
		}
	}
	return nil
}

func validateState(state State) error {
	if state.SchemaVersion != SchemaVersion || !document.ValidHash(state.PlanHash) {
		return errors.New("invalid promotion state")
	}
	if state.Status != "planned" && state.Status != "applied" && state.Status != "rejected" && state.Status != "stale" {
		return errors.New("invalid promotion status")
	}
	if _, err := time.Parse(time.RFC3339, state.UpdatedAt); err != nil {
		return errors.New("promotion updated_at must be RFC3339")
	}
	if state.Status == "applied" {
		if _, err := time.Parse(time.RFC3339, state.AppliedAt); err != nil {
			return errors.New("applied promotion requires applied_at")
		}
		if state.RejectedAt != "" {
			return errors.New("applied promotion cannot have rejected_at")
		}
	}
	if state.Status == "rejected" {
		if _, err := time.Parse(time.RFC3339, state.RejectedAt); err != nil {
			return errors.New("rejected promotion requires rejected_at")
		}
		if state.AppliedAt != "" {
			return errors.New("rejected promotion cannot have applied_at")
		}
	}
	if (state.Status == "planned" || state.Status == "stale") && (state.AppliedAt != "" || state.RejectedAt != "") {
		return errors.New("planned or stale promotion cannot have terminal timestamps")
	}
	if state.Status == "stale" && strings.TrimSpace(state.Reason) == "" {
		return errors.New("stale promotion requires a reason")
	}
	return nil
}

func validateKnowledgePath(cfg *config.Instance, rel, id string) error {
	if rel == "" || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))) != rel {
		return errors.New("knowledge target path must be canonical and relative")
	}
	if !strings.HasSuffix(rel, "--"+id+".md") {
		return errors.New("knowledge target path must stay under knowledge and end with its id")
	}
	return vault.EnsureInside(cfg.KnowledgeDir(), filepath.Join(cfg.Root, filepath.FromSlash(rel)))
}

func promotionDir(cfg *config.Instance, id string) string {
	return filepath.Join(cfg.RuntimeDir(), "promotions", id)
}

func writeState(cfg *config.Instance, id string, state State) error {
	data, err := marshalJSON(state)
	if err != nil {
		return err
	}
	return document.AtomicWrite(filepath.Join(promotionDir(cfg, id), "state.json"), data, 0o600)
}

func loadJournal(cfg *config.Instance, operationID string) (Journal, error) {
	var journal Journal
	data, err := readRegularExact(filepath.Join(cfg.RuntimeDir(), "transactions", operationID, "journal.json"))
	if err != nil {
		return journal, err
	}
	if err := decodeStrict(data, &journal); err != nil {
		return journal, err
	}
	if journal.SchemaVersion != SchemaVersion || journal.OperationID != operationID || !document.ValidID("op", operationID) || !document.ValidID("prm", journal.PromotionID) || len(journal.Files) == 0 {
		return journal, errors.New("invalid transaction journal")
	}
	if journal.State != "prepared" && journal.State != "files_committed" && journal.State != "complete" {
		return journal, errors.New("invalid transaction journal state")
	}
	if _, err := time.Parse(time.RFC3339, journal.CreatedAt); err != nil {
		return journal, errors.New("invalid transaction journal created_at")
	}
	if _, err := time.Parse(time.RFC3339, journal.UpdatedAt); err != nil {
		return journal, errors.New("invalid transaction journal updated_at")
	}
	seen := map[string]bool{}
	for i, file := range journal.Files {
		if !document.ValidHash(file.NewHash) || !canonicalRelative(file.Path) || seen[file.Path] {
			return journal, errors.New("invalid transaction file entry")
		}
		seen[file.Path] = true
		expectedStage := filepath.ToSlash(filepath.Join("stage", fmt.Sprintf("%04d", i)))
		if file.StageFile != expectedStage {
			return journal, errors.New("transaction stage path is not canonical")
		}
		if file.HadTarget {
			expectedBackup := filepath.ToSlash(filepath.Join("backup", fmt.Sprintf("%04d", i)))
			if file.BackupFile != expectedBackup {
				return journal, errors.New("transaction backup path is not canonical")
			}
		} else if file.BackupFile != "" {
			return journal, errors.New("new transaction target cannot have a backup")
		}
		if err := vault.EnsureInside(cfg.Root, filepath.Join(cfg.Root, filepath.FromSlash(file.Path))); err != nil {
			return journal, err
		}
		if file.Kind != fileKind(cfg, file.Path) {
			return journal, errors.New("transaction file kind does not match its path")
		}
		if file.Kind == "promotion_state" {
			expected := filepath.ToSlash(filepath.Join(cfg.Paths.Runtime, "promotions", journal.PromotionID, "state.json"))
			if file.Path != expected {
				return journal, errors.New("promotion state transaction path is not canonical")
			}
		}
	}
	return journal, nil
}

func writeJournal(dir string, journal Journal) error {
	data, err := marshalJSON(journal)
	if err != nil {
		return err
	}
	return document.AtomicWrite(filepath.Join(dir, "journal.json"), data, 0o600)
}

func fileKind(cfg *config.Instance, rel string) string {
	target := filepath.Join(cfg.Root, filepath.FromSlash(rel))
	if vault.EnsureInside(cfg.KnowledgeDir(), target) == nil {
		return "knowledge"
	}
	if vault.EnsureInside(cfg.InboxDir(), target) == nil {
		return "inbox"
	}
	return "promotion_state"
}

func canonicalRelative(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))) == rel && rel != "."
}

func readManagedInput(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input is not a regular non-symlink file: %s", path)
	}
	if err := fsutil.EnsureSingleLink(path); err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("input exceeds %d byte limit", limit)
	}
	return os.ReadFile(path)
}

func readRegularExact(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed file is not a regular non-symlink file: %s", path)
	}
	if info.Size() > document.MaxMarkdownBytes {
		return nil, fmt.Errorf("managed file exceeds %d byte safety limit: %s", document.MaxMarkdownBytes, path)
	}
	if err := fsutil.EnsureSingleLink(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func decodeStrict(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain exactly one value")
	}
	return nil
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func promotionDiff(targets []Target, oldFiles map[string][]byte, frozen map[string][]byte) string {
	var out strings.Builder
	for _, target := range targets {
		out.WriteString(lineDiff(target.TargetPath, oldFiles[target.TargetPath], frozen[target.FrozenFile]))
	}
	return out.String()
}

func lineDiff(path string, oldData, newData []byte) string {
	var out strings.Builder
	oldPath := "a/" + path
	if len(oldData) == 0 {
		oldPath = "/dev/null"
	}
	fmt.Fprintf(&out, "--- %s\n+++ b/%s\n", oldPath, path)
	oldLines := scanLines(oldData)
	newLines := scanLines(newData)
	fmt.Fprintf(&out, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		fmt.Fprintf(&out, "-%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&out, "+%s\n", line)
	}
	return out.String()
}

func scanLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
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

func copyExtra(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func mergeExtra(base, overlay map[string]any) map[string]any {
	out := copyExtra(base)
	if out == nil && len(overlay) != 0 {
		out = map[string]any{}
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func cleanStrings(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
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
