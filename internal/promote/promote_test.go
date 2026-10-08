package promote

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/inbox"
	"llm-wiki/internal/vault"
)

func TestPromotionSupportsMultipleInputsAndOutputs(t *testing.T) {
	cfg := initPromotionWiki(t)
	first := addInbox(t, cfg, "first.pdf", "%PDF-1.0\nfirst payload", 100)
	second := addInbox(t, cfg, "second.txt", "second payload", 101)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "one.md"), "note", "First knowledge", "First self-contained fact.")
	writeDraft(t, filepath.Join(base, "two.md"), "note", "Merged knowledge", "Merged self-contained fact.")
	firstKnowledge := "know_01arz3ndektsv4rrffq69g5faw"
	secondKnowledge := "know_01arz3ndektsv4rrffq69g5fax"
	manifest := Manifest{SchemaVersion: 1,
		Inboxes: []ManifestInbox{
			{ID: first.ID, PayloadHash: first.PayloadHash, ItemHash: first.ItemHash, Consume: true},
			{ID: second.ID, PayloadHash: second.PayloadHash, ItemHash: second.ItemHash, Consume: true},
		},
		Targets: []ManifestTarget{
			{Operation: "create", DraftFile: "one.md", KnowledgeID: firstKnowledge, InboxIDs: []string{first.ID}},
			{Operation: "create", DraftFile: "two.md", KnowledgeID: secondKnowledge, InboxIDs: []string{first.ID, second.ID}},
		},
	}
	manifestPath := writeManifest(t, base, manifest)
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: manifestPath, Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Plan.Targets) != 2 || planned.PlanHash == "" || !bytes.Contains([]byte(planned.Diff), []byte(firstKnowledge)) || !bytes.Contains([]byte(planned.Diff), []byte(secondKnowledge)) {
		t.Fatalf("incomplete frozen plan %#v", planned)
	}
	diff, err := Diff(cfg, planned.Plan.ID)
	if err != nil || diff.PlanHash != planned.PlanHash || diff.Diff != planned.Diff {
		t.Fatalf("diff changed after freeze: %#v %v", diff, err)
	}
	if _, err := Apply(cfg, planned.Plan.ID, "sha256:0000000000000000000000000000000000000000000000000000000000000000", false, time.Unix(300, 0).UTC()); err == nil {
		t.Fatal("wrong approval hash was accepted")
	}
	applied, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Targets) != 2 || len(applied.Consumed) != 2 {
		t.Fatalf("incomplete apply result %#v", applied)
	}
	for _, id := range []string{firstKnowledge, secondKnowledge} {
		doc, err := document.FindByID(cfg.KnowledgeDir(), id)
		if err != nil || doc.Metadata.ID != id || len(doc.Metadata.Lineage) == 0 || len(doc.Metadata.Attachments) != 1 || doc.ValidateStoredAttachments() != nil {
			t.Fatalf("knowledge %s missing: %#v %v", id, doc, err)
		}
	}
	firstDoc, err := inbox.Show(cfg, first.ID)
	if err != nil || firstDoc.Metadata.Status != "processed" || len(firstDoc.Metadata.KnowledgeIDs) != 2 {
		t.Fatalf("first inbox not consumed across targets: %#v %v", firstDoc, err)
	}
	if err := CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidFrozenPlanCanBeRejectedWithoutAcceptingIt(t *testing.T) {
	cfg := initPromotionWiki(t)
	input := addInbox(t, cfg, "source.txt", "original source", 100)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "Old plan", "A published fact.")
	manifest := Manifest{SchemaVersion: SchemaVersion,
		Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", InboxIDs: []string{input.ID}}},
	}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	removeFrozenAttachmentList(t, cfg, planned.Plan.ID)
	if _, _, _, err := Load(cfg, planned.Plan.ID); err == nil {
		t.Fatal("invalid frozen plan was accepted")
	}
	if _, err := ActiveInboxIDs(cfg); err == nil {
		t.Fatal("invalid planned promotion was hidden from active Inbox scan")
	}
	if _, err := ActiveCount(cfg); err == nil {
		t.Fatal("invalid planned promotion was hidden from active count")
	}
	statePath := filepath.Join(promotionDir(cfg, planned.Plan.ID), "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Reject(cfg, planned.Plan.ID, "replace with current plan", time.Unix(300, 0).UTC(), true)
	if err != nil || preview.Status != "rejected" {
		t.Fatalf("cannot preview rejection of invalid plan: %#v %v", preview, err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("reject preview changed the state: %v", err)
	}
	rejected, err := Reject(cfg, planned.Plan.ID, "replace with current plan", time.Unix(300, 0).UTC(), false)
	if err != nil || rejected.Status != "rejected" {
		t.Fatalf("cannot reject invalid plan: %#v %v", rejected, err)
	}
	active, err := ActiveInboxIDs(cfg)
	if err != nil || len(active) != 0 {
		t.Fatalf("rejected plan still blocks Inbox: %#v %v", active, err)
	}
	count, err := ActiveCount(cfg)
	if err != nil || count != 0 {
		t.Fatalf("rejected plan still counts as active: %d %v", count, err)
	}
	if _, _, _, err := Load(cfg, planned.Plan.ID); err == nil {
		t.Fatal("rejection made invalid plan readable")
	}
}

func TestPromotionStateScanRejectsChangedFrozenBytes(t *testing.T) {
	cfg := initPromotionWiki(t)
	input := addInbox(t, cfg, "source.txt", "original source", 100)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "Frozen plan", "A published fact.")
	manifest := Manifest{SchemaVersion: SchemaVersion,
		Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", InboxIDs: []string{input.ID}}},
	}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(promotionDir(cfg, planned.Plan.ID), "plan.json")
	if err := os.WriteFile(planPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reject(cfg, planned.Plan.ID, "invalid", time.Unix(300, 0).UTC(), false); err == nil {
		t.Fatal("changed frozen plan was rejected without integrity verification")
	}
	if _, err := ActiveInboxIDs(cfg); err == nil {
		t.Fatal("changed frozen plan was hidden from active Inbox scan")
	}
}

func removeFrozenAttachmentList(t *testing.T, cfg *config.Instance, promotionID string) {
	t.Helper()
	planPath := filepath.Join(promotionDir(cfg, promotionID), "plan.json")
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatal(err)
	}
	for _, target := range plan["targets"].([]any) {
		delete(target.(map[string]any), "attachments")
	}
	planBytes, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	stateBytes, err := os.ReadFile(filepath.Join(promotionDir(cfg, promotionID), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	state.PlanHash = document.HashBytes(planBytes)
	if err := writeState(cfg, promotionID, state); err != nil {
		t.Fatal(err)
	}
}

func TestPromotionPublishesAttachmentIndependentlyOfInbox(t *testing.T) {
	cfg := initPromotionWiki(t)
	payload := []byte("%PDF-1.0\noriginal attachment\n")
	items, err := inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: "paper.pdf", Source: "test", Stdin: bytes.NewReader(payload), Now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	input := items[0]
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "Attachment knowledge", "Facts from the paper.")
	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	manifest := Manifest{SchemaVersion: SchemaVersion,
		Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{input.ID}}},
	}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	attachments := planned.Plan.Targets[0].Attachments
	if len(attachments) != 1 || attachments[0].Hash != document.HashBytes(payload) || !bytes.Contains([]byte(planned.Diff), []byte(attachments[0].Path)) {
		t.Fatalf("attachment missing from frozen review: %#v", planned)
	}
	frozen, err := os.ReadFile(filepath.Join(promotionDir(cfg, planned.Plan.ID), filepath.FromSlash(attachments[0].FrozenFile)))
	if err != nil || !bytes.Equal(frozen, payload) {
		t.Fatalf("attachment bytes were not frozen: %v", err)
	}
	applied, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Targets) != 1 || len(applied.Targets[0].Attachments) != 1 || applied.Targets[0].Attachments[0] != attachments[0].Path {
		t.Fatalf("attachment missing from apply result: %#v", applied)
	}
	published := filepath.Join(cfg.Root, filepath.FromSlash(attachments[0].Path))
	got, err := os.ReadFile(published)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published attachment differs from source: %v", err)
	}
	if err := CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	current, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	second := addInbox(t, cfg, "revision.txt", "new facts", 400)
	writeDraft(t, filepath.Join(base, "revised.md"), "note", "Attachment knowledge", "Revised facts from the paper.")
	update := Manifest{SchemaVersion: SchemaVersion,
		Inboxes: []ManifestInbox{{ID: second.ID, PayloadHash: second.PayloadHash, ItemHash: second.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "update", DraftFile: "revised.md", KnowledgeID: knowledgeID, InboxIDs: []string{second.ID}, BaseContentHash: current.Metadata.ContentHash, BaseFileHash: current.FileHash}},
	}
	updatedPlan, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, update), Now: time.Unix(500, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedPlan.Plan.Targets[0].Attachments) != 0 {
		t.Fatal("update tried to recopy an existing attachment")
	}
	if _, err := Apply(cfg, updatedPlan.Plan.ID, updatedPlan.PlanHash, false, time.Unix(600, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.InboxDir()); err != nil {
		t.Fatal(err)
	}
	doc, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil || len(doc.Metadata.Attachments) != 1 || doc.ValidateStoredAttachments() != nil {
		t.Fatalf("knowledge depends on Inbox after publication: %#v %v", doc, err)
	}
	if err := os.WriteFile(published, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateStoredAttachments(); err == nil {
		t.Fatal("modified published attachment was accepted")
	}
	outside := filepath.Join(t.TempDir(), "same.pdf")
	if err := os.WriteFile(outside, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(published); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, published); err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateStoredAttachments(); err == nil {
		t.Fatal("symlinked published attachment was accepted")
	}
}

func TestPromotionManifestRejectsNonCurrentSchema(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: SchemaVersion + 1,
		Inboxes:       []ManifestInbox{{ID: "inbox_01arz3ndektsv4rrffq69g5fav"}},
		Targets:       []ManifestTarget{{Operation: "create"}},
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("non-current promotion manifest schema was accepted")
	}
}

func TestPlanDryRunValidatesWithoutCreatingPromotionOrLock(t *testing.T) {
	cfg := initPromotionWiki(t)
	input := addInbox(t, cfg, "source.txt", "payload", 100)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "Dry plan", "Dry-run fact.")
	manifest := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: "know_01arz3ndektsv4rrffq69g5faw", InboxIDs: []string{input.ID}}}}
	held, err := vault.AcquireWrite(cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	result, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), DryRun: true, Now: time.Unix(200, 0).UTC()})
	if err != nil || result.PlanHash == "" {
		t.Fatalf("dry-run plan failed: %#v %v", result, err)
	}
	if _, err := os.Stat(promotionDir(cfg, result.Plan.ID)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a promotion: %v", err)
	}
}

func TestOnePromotionCreatesAndUpdatesKnowledgeTogether(t *testing.T) {
	cfg := initPromotionWiki(t)
	first := addInbox(t, cfg, "first.txt", "first payload", 100)
	second := addInbox(t, cfg, "second.txt", "second payload", 101)
	existingID := "know_01arz3ndektsv4rrffq69g5faw"
	createdID := "know_01arz3ndektsv4rrffq69g5fax"
	existingBody := []byte("# Existing knowledge\n\nOriginal fact.\n")
	governanceVersion, err := governance.Version(cfg)
	if err != nil {
		t.Fatal(err)
	}
	existingMeta := document.Metadata{
		SchemaVersion: document.CurrentSchema, ID: existingID, Type: "note", Title: "Existing knowledge", Status: "published",
		PublishedAt: time.Unix(50, 0).UTC().Format(time.RFC3339), UpdatedAt: time.Unix(50, 0).UTC().Format(time.RFC3339), ContentHash: document.HashBytes(existingBody),
		GovernanceVersion: governanceVersion, Lineage: []document.LineageRef{{InboxID: first.ID, PayloadHash: first.PayloadHash, Source: "test", CapturedAt: time.Unix(100, 0).UTC().Format(time.RFC3339)}},
		Extra: map[string]any{"category": "learning", "description": "Existing self-contained knowledge", "lifecycle": "current"},
	}
	existingPath := filepath.Join(cfg.Root, filepath.FromSlash(document.KnowledgePath(cfg.Paths.Knowledge, existingMeta)))
	if err := document.Write(existingPath, existingMeta, existingBody); err != nil {
		t.Fatal(err)
	}
	existingFileHash, err := document.HashFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "update.md"), "note", "Existing knowledge", "Updated fact.")
	writeDraft(t, filepath.Join(base, "create.md"), "note", "Created knowledge", "Created from two inputs.")
	manifest := Manifest{SchemaVersion: 1,
		Inboxes: []ManifestInbox{
			{ID: first.ID, PayloadHash: first.PayloadHash, ItemHash: first.ItemHash, Consume: true},
			{ID: second.ID, PayloadHash: second.PayloadHash, ItemHash: second.ItemHash, Consume: true},
		},
		Targets: []ManifestTarget{
			{Operation: "update", DraftFile: "update.md", KnowledgeID: existingID, InboxIDs: []string{first.ID}, BaseContentHash: existingMeta.ContentHash, BaseFileHash: existingFileHash},
			{Operation: "create", DraftFile: "create.md", KnowledgeID: createdID, InboxIDs: []string{first.ID, second.ID}},
		},
	}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Targets) != 2 || len(applied.Consumed) != 2 {
		t.Fatalf("create/update apply was incomplete: %#v", applied)
	}
	updated, err := document.FindByID(cfg.KnowledgeDir(), existingID)
	if err != nil || !bytes.Contains(updated.Body, []byte("Updated fact.")) {
		t.Fatalf("existing knowledge was not updated: %#v %v", updated, err)
	}
	if _, err := document.FindByID(cfg.KnowledgeDir(), createdID); err != nil {
		t.Fatalf("new knowledge was not created: %v", err)
	}
}

func TestApplyDriftMarksPromotionStaleWithoutFactWrites(t *testing.T) {
	cfg := initPromotionWiki(t)
	input := addInbox(t, cfg, "source.txt", "payload", 100)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "No write", "Frozen fact.")
	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	manifest := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{input.ID}}}}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	item, err := inbox.Show(cfg, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.Body = append(item.Body, []byte("\nchanged\n")...)
	if err := document.Write(item.Path, item.Metadata, item.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC()); err == nil {
		t.Fatal("drifted plan was applied")
	}
	if _, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID); !os.IsNotExist(err) {
		t.Fatalf("stale apply wrote knowledge: %v", err)
	}
	_, state, _, err := Load(cfg, planned.Plan.ID)
	if err != nil || state.Status != "stale" {
		t.Fatalf("promotion was not marked stale: %#v %v", state, err)
	}
}

func TestContentPackDriftMarksPromotionStaleWithoutFactWrites(t *testing.T) {
	cfg := initPromotionWiki(t)
	input := addInbox(t, cfg, "source.txt", "payload", 100)
	base := t.TempDir()
	writeDraft(t, filepath.Join(base, "draft.md"), "note", "No policy drift", "Frozen fact.")
	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	manifest := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{input.ID}}}}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := governance.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy.Categories[0].Description += " changed after plan"
	policyBytes, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ContentPackPath(), append(policyBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC()); !errors.Is(err, ErrApplyConflict) {
		t.Fatalf("content-pack drift did not return an apply conflict: %v", err)
	}
	if _, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID); !os.IsNotExist(err) {
		t.Fatalf("content-pack drift wrote knowledge: %v", err)
	}
	_, state, _, err := Load(cfg, planned.Plan.ID)
	if err != nil || state.Status != "stale" {
		t.Fatalf("content-pack drift was not marked stale: %#v %v", state, err)
	}
}

func TestPlanAndFrozenDriftMarkPromotionStaleWithoutFactWrites(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *config.Instance, Plan)
	}{
		{name: "plan", mutate: func(t *testing.T, cfg *config.Instance, plan Plan) {
			t.Helper()
			path := filepath.Join(promotionDir(cfg, plan.ID), "plan.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "frozen", mutate: func(t *testing.T, cfg *config.Instance, plan Plan) {
			t.Helper()
			path := filepath.Join(promotionDir(cfg, plan.ID), filepath.FromSlash(plan.Targets[0].FrozenFile))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, []byte("\nchanged\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "diff", mutate: func(t *testing.T, cfg *config.Instance, plan Plan) {
			t.Helper()
			path := filepath.Join(promotionDir(cfg, plan.ID), "diff.patch")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, []byte("\nchanged\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := initPromotionWiki(t)
			input := addInbox(t, cfg, "source.txt", "payload", 100)
			base := t.TempDir()
			writeDraft(t, filepath.Join(base, "draft.md"), "note", "No write", "Frozen fact.")
			knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
			manifest := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}},
				Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{input.ID}}}}
			planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, cfg, planned.Plan)
			if _, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC()); !errors.Is(err, ErrApplyConflict) {
				t.Fatalf("drifted %s did not return an apply conflict: %v", test.name, err)
			}
			if _, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID); !os.IsNotExist(err) {
				t.Fatalf("drifted %s wrote knowledge: %v", test.name, err)
			}
			stateData, err := os.ReadFile(filepath.Join(promotionDir(cfg, planned.Plan.ID), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state State
			if err := decodeStrict(stateData, &state); err != nil || state.Status != "stale" {
				t.Fatalf("drifted %s was not marked stale: %#v %v", test.name, state, err)
			}
		})
	}
}

func TestKnowledgeBaselineDriftMarksPromotionStale(t *testing.T) {
	cfg := initPromotionWiki(t)
	base := t.TempDir()
	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	first := addInbox(t, cfg, "first.txt", "first payload", 100)
	writeDraft(t, filepath.Join(base, "first.md"), "note", "Stable title", "Original fact.")
	create := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: first.ID, PayloadHash: first.PayloadHash, ItemHash: first.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "create", DraftFile: "first.md", KnowledgeID: knowledgeID, InboxIDs: []string{first.ID}}}}
	created, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, create), Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(cfg, created.Plan.ID, created.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}

	existing, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	baseFileHash, err := document.HashFile(existing.Path)
	if err != nil {
		t.Fatal(err)
	}
	second := addInbox(t, cfg, "second.txt", "second payload", 400)
	writeDraft(t, filepath.Join(base, "second.md"), "note", "Stable title", "Approved update.")
	update := Manifest{SchemaVersion: 1, Inboxes: []ManifestInbox{{ID: second.ID, PayloadHash: second.PayloadHash, ItemHash: second.ItemHash, Consume: true}},
		Targets: []ManifestTarget{{Operation: "update", DraftFile: "second.md", KnowledgeID: knowledgeID, InboxIDs: []string{second.ID}, BaseContentHash: existing.Metadata.ContentHash, BaseFileHash: baseFileHash}}}
	planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, update), Now: time.Unix(500, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	existing.Body = []byte("# Stable title\n\nOutside drift.\n")
	existing.Metadata.ContentHash = document.HashBytes(existing.Body)
	if err := document.Write(existing.Path, existing.Metadata, existing.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(600, 0).UTC()); !errors.Is(err, ErrApplyConflict) {
		t.Fatalf("knowledge baseline drift did not conflict: %v", err)
	}
	actual, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
	if err != nil || !bytes.Contains(actual.Body, []byte("Outside drift.")) || bytes.Contains(actual.Body, []byte("Approved update.")) {
		t.Fatalf("stale apply changed knowledge: %#v %v", actual, err)
	}
}

func TestRecoveryUsesPreparedRollbackAndCommittedFiles(t *testing.T) {
	cfg := initPromotionWiki(t)
	promotionID := "prm_01arz3ndektsv4rrffq69g5fav"
	path := filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, "note", "recovery--know_01arz3ndektsv4rrffq69g5faw.md"))
	target := filepath.Join(cfg.Root, filepath.FromSlash(path))
	oldData, newData := []byte("old"), []byte("new")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, newData, 0o600); err != nil {
		t.Fatal(err)
	}
	opID := "op_01arz3ndektsv4rrffq69g5faw"
	txn := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	if err := os.MkdirAll(filepath.Join(txn, "backup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(txn, "stage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txn, "backup", "0000"), oldData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txn, "stage", "0000"), newData, 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(700, 0).UTC().Format(time.RFC3339)
	journal := Journal{SchemaVersion: 1, OperationID: opID, PromotionID: promotionID, State: "prepared", CreatedAt: fixed, UpdatedAt: fixed,
		Files: []JournalFile{{Kind: "knowledge", Path: path, NewHash: document.HashBytes(newData), HadTarget: true, BackupFile: "backup/0000", StageFile: "stage/0000"}}}
	if err := writeJournal(txn, journal); err != nil {
		t.Fatal(err)
	}
	actions, err := Recover(cfg)
	if err != nil || len(actions) != 1 || actions[0].Action != "rolled_back" {
		t.Fatalf("prepared recovery failed %#v %v", actions, err)
	}
	actual, _ := os.ReadFile(target)
	if !bytes.Equal(actual, oldData) {
		t.Fatalf("prepared recovery kept partial file %q", actual)
	}

	opID2 := "op_01arz3ndektsv4rrffq69g5fax"
	txn2 := filepath.Join(cfg.RuntimeDir(), "transactions", opID2)
	if err := os.MkdirAll(filepath.Join(txn2, "stage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, newData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txn2, "stage", "0000"), newData, 0o600); err != nil {
		t.Fatal(err)
	}
	journal.OperationID, journal.State = opID2, "files_committed"
	journal.Files[0].HadTarget = false
	journal.Files[0].BackupFile = ""
	if err := writeJournal(txn2, journal); err != nil {
		t.Fatal(err)
	}
	actions, err = Recover(cfg)
	if err != nil || len(actions) != 1 || actions[0].Action != "index_required" {
		t.Fatalf("committed recovery failed %#v %v", actions, err)
	}
	if err := CompleteOperation(cfg, opID2); err != nil {
		t.Fatal(err)
	}
}

func initPromotionWiki(t *testing.T) *config.Instance {
	t.Helper()
	result, err := vault.Init(vault.InitOptions{Path: filepath.Join(t.TempDir(), "wiki"), Name: "promotion-test", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Config
}

func addInbox(t *testing.T, cfg *config.Instance, name, payload string, second int64) inbox.Added {
	t.Helper()
	items, err := inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: name, Source: "test", Stdin: bytes.NewBufferString(payload), Now: time.Unix(second, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return items[0]
}

func writeDraft(t *testing.T, path, kind, title, fact string) {
	t.Helper()
	data := []byte("---\ntype: " + kind + "\ncategory: learning\ntitle: \"" + title + "\"\ndescription: \"Self-contained description\"\nlifecycle: current\n---\n# " + title + "\n\n" + fact + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, base string, manifest Manifest) string {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "promotion.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInboxChangesOnlyInvalidateFrozenPublication(t *testing.T) {
	for _, change := range []string{"attachment", "delete", "frozen"} {
		t.Run(change, func(t *testing.T) {
			cfg := initPromotionWiki(t)
			items, err := inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: "input.pdf", Stdin: bytes.NewReader([]byte("%PDF-1.0\noriginal")), Now: time.Unix(100, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			input := items[0]
			base := t.TempDir()
			writeDraft(t, filepath.Join(base, "draft.md"), "note", "Snapshot boundary", "Self-contained fact.")
			manifest := Manifest{SchemaVersion: SchemaVersion, Inboxes: []ManifestInbox{{ID: input.ID, PayloadHash: input.PayloadHash, ItemHash: input.ItemHash, Consume: true}}, Targets: []ManifestTarget{{Operation: "create", DraftFile: "draft.md", InboxIDs: []string{input.ID}}}}
			planned, err := PlanPromotion(cfg, PlanOptions{ManifestPath: writeManifest(t, base, manifest), Now: time.Unix(200, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if change == "attachment" {
				if err := os.WriteFile(filepath.Join(cfg.Root, input.PayloadPath), []byte("%PDF-1.0\nmodified"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := inbox.Show(cfg, input.ID); err != nil {
					t.Fatalf("normal Inbox edit rejected: %v", err)
				}
			} else if change == "delete" {
				if _, err := inbox.Clean(cfg, inbox.CleanOptions{IDs: []string{input.ID}, Yes: true, Now: time.Unix(250, 0).UTC()}); err != nil {
					t.Fatalf("explicit deletion of planned input failed: %v", err)
				}
			} else {
				frozen := filepath.Join(promotionDir(cfg, planned.Plan.ID), filepath.FromSlash(planned.Plan.Targets[0].Attachments[0].FrozenFile))
				if err := os.WriteFile(frozen, []byte("changed frozen attachment"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC()); !errors.Is(err, ErrApplyConflict) {
				t.Fatalf("changed frozen input was applied: %v", err)
			}
			_, state, _, err := Load(cfg, planned.Plan.ID)
			if err != nil || state.Status != "stale" {
				t.Fatalf("plan was not stale: %#v %v", state, err)
			}
			if docs, problems := document.ScanMarkdown(cfg.KnowledgeDir()); len(docs) != 0 || len(problems) != 0 {
				t.Fatal("stale plan wrote facts")
			}
		})
	}
}
