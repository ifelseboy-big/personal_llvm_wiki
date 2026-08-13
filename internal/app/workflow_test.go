package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCompleteInboxPromotionKnowledgeCleanWorkflow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "workflow", "--json", "--no-interactive")
	work := t.TempDir()
	note := filepath.Join(work, "note.md")
	if err := os.WriteFile(note, []byte("# Stable IR input\n\nInitial organization for later review.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := runCLI(t, "Stable IR decouples compiler components.", "inbox", "add", "-", "--name", "source.txt", "--source", "user", "--note-file", note, "--wiki", root, "--json", "--no-interactive")
	inboxID := nestedString(t, added.Data, "items", 0, "id")
	if status := nestedString(t, added.Data, "items", 0, "status"); status != "pending" {
		t.Fatalf("add did not create pending inbox: %#v", added.Data)
	}
	before := runCLI(t, "", "query", "Stable IR", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, before.Data, "count") != 0 {
		t.Fatalf("query returned Inbox content: %#v", before.Data)
	}
	shownInbox := runCLI(t, "", "inbox", "show", inboxID, "--wiki", root, "--json", "--no-interactive")
	payloadHash := nestedString(t, shownInbox.Data, "payload_hash")
	itemHash := nestedString(t, shownInbox.Data, "item_hash")
	payloadPath := filepath.Join(root, filepath.FromSlash(nestedString(t, shownInbox.Data, "payload_path")))
	payload, err := os.ReadFile(payloadPath)
	if err != nil || string(payload) != "Stable IR decouples compiler components." {
		t.Fatalf("inbox show did not expose the verified original payload: %q %v", payload, err)
	}

	knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
	draft := filepath.Join(work, "draft.md")
	draftData := "---\ntype: concept\ncategory: development\ntitle: \"Stable IR\"\ndescription: \"Stable IR decouples compiler components\"\nlifecycle: current\ncustom_context: round-trip-extension\n---\n# Stable IR\n\nStable IR decouples compiler frontends and backends while preserving a common contract.\n"
	if err := os.WriteFile(draft, []byte(draftData), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(work, "promotion.json")
	manifestData := fmt.Sprintf(`{
  "schema_version": 1,
  "inboxes": [{"id": %q, "payload_hash": %q, "item_hash": %q, "consume": true}],
  "targets": [{"operation": "create", "draft_file": "draft.md", "knowledge_id": %q, "inbox_ids": [%q]}]
}
`, inboxID, payloadHash, itemHash, knowledgeID, inboxID)
	if err := os.WriteFile(manifest, []byte(manifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := runCLI(t, "", "promote", "plan", "--manifest", manifest, "--wiki", root, "--json", "--no-interactive")
	promotionID := nestedString(t, planned.Data, "promotion_id")
	planHash := nestedString(t, planned.Data, "plan_hash")
	if nestedString(t, planned.Data, "content_pack", "version") != "1.0.0" || nestedString(t, planned.Data, "content_pack", "policy_hash") == "" {
		t.Fatalf("plan omitted its frozen content-pack identity: %#v", planned.Data)
	}
	diff := runCLI(t, "", "promote", "diff", promotionID, "--wiki", root, "--json", "--no-interactive")
	if nestedString(t, diff.Data, "plan_hash") != planHash || !bytes.Contains([]byte(nestedString(t, diff.Data, "diff")), []byte("Stable IR")) {
		t.Fatalf("diff is not bound to plan: %#v", diff.Data)
	}
	apply := runCLI(t, "", "promote", "apply", promotionID, "--approve", planHash, "--wiki", root, "--json", "--no-interactive")
	if nestedString(t, apply.Data, "targets", 0, "knowledge_id") != knowledgeID {
		t.Fatalf("promotion did not publish target: %#v", apply.Data)
	}
	if nestedString(t, apply.Data, "transaction_state") != "complete" {
		t.Fatalf("promotion did not report a complete transaction: %#v", apply.Data)
	}
	listed := runCLI(t, "", "inbox", "list", "--status", "processed", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, listed.Data, "count") != 1 {
		t.Fatalf("applied inbox is not processed: %#v", listed.Data)
	}
	query := runCLI(t, "", "query", "compiler frontends backends", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, query.Data, "count") < 1 || nestedString(t, query.Data, "evidence", 0, "knowledge_id") != knowledgeID {
		t.Fatalf("query missed applied Knowledge: %#v", query.Data)
	}
	if nestedString(t, query.Data, "evidence", 0, "metadata", "extra", "custom_context") != "round-trip-extension" {
		t.Fatalf("query lost extension metadata: %#v", query.Data)
	}
	show := runCLI(t, "", "show", knowledgeID, "--wiki", root, "--json", "--no-interactive")
	if nestedString(t, show.Data, "file_hash") == "" || nestedString(t, show.Data, "content_hash") == "" {
		t.Fatalf("show omitted safe update baselines: %#v", show.Data)
	}
	lineageID := nestedString(t, show.Data, "metadata", "lineage", 0, "inbox_id")
	if lineageID != inboxID {
		t.Fatalf("Knowledge lineage missing: %#v", show.Data)
	}
	if nestedString(t, show.Data, "metadata", "extra", "custom_context") != "round-trip-extension" {
		t.Fatalf("show lost extension metadata: %#v", show.Data)
	}
	preview := runCLI(t, "", "inbox", "clean", inboxID, "--dry-run", "--wiki", root, "--json", "--no-interactive")
	if len(preview.AffectedFiles) != 2 {
		t.Fatalf("clean preview is incomplete: %#v", preview)
	}
	runCLI(t, "", "inbox", "clean", inboxID, "--yes", "--wiki", root, "--json", "--no-interactive")
	query = runCLI(t, "", "query", "compiler frontends backends", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, query.Data, "count") < 1 {
		t.Fatalf("clean broke Knowledge query: %#v", query.Data)
	}
	runCLI(t, "", "doctor", "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "index", "rebuild", "--wiki", root, "--json", "--no-interactive")
}

func TestPromotionApprovalAndStaleErrorsAreStable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "conflict", "--json", "--no-interactive")
	work := t.TempDir()
	added := runCLI(t, "payload", "inbox", "add", "-", "--name", "input.txt", "--wiki", root, "--json", "--no-interactive")
	id := nestedString(t, added.Data, "items", 0, "id")
	payloadHash := nestedString(t, added.Data, "items", 0, "payload_hash")
	itemHash := nestedString(t, added.Data, "items", 0, "item_hash")
	if err := os.WriteFile(filepath.Join(work, "draft.md"), []byte("---\ntype: concept\ncategory: learning\ntitle: Conflict\ndescription: Complete\nlifecycle: current\n---\n# Conflict\n\nComplete fact.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"schema_version":1,"inboxes":[{"id":%q,"payload_hash":%q,"item_hash":%q,"consume":true}],"targets":[{"operation":"create","draft_file":"draft.md","knowledge_id":"know_01arz3ndektsv4rrffq69g5faw","inbox_ids":[%q]}]}`, id, payloadHash, itemHash, id)
	manifestPath := filepath.Join(work, "promotion.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := runCLI(t, "", "promote", "plan", "--manifest", manifestPath, "--wiki", root, "--json", "--no-interactive")
	promotionID := nestedString(t, planned.Data, "promotion_id")
	missing := runCLIFailure(t, "", "promote", "apply", promotionID, "--wiki", root, "--json", "--no-interactive")
	if missing.Error == nil || missing.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("missing approval was not rejected as usage: %#v", missing)
	}
	wrong := runCLIFailure(t, "", "promote", "apply", promotionID, "--approve", "sha256:0000000000000000000000000000000000000000000000000000000000000000", "--wiki", root, "--json", "--no-interactive")
	if wrong.Error == nil || wrong.Error.Code != "PROMOTION_APPROVAL_MISMATCH" {
		t.Fatalf("wrong approval used unstable code: %#v", wrong)
	}
	itemPath := filepath.Join(root, nestedString(t, added.Data, "items", 0, "item_path"))
	b, err := os.ReadFile(itemPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(itemPath, append(b, []byte("\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := runCLIFailure(t, "", "promote", "apply", promotionID, "--approve", nestedString(t, planned.Data, "plan_hash"), "--wiki", root, "--json", "--no-interactive")
	if stale.Error == nil || stale.Error.Code != "PROMOTION_STALE" {
		t.Fatalf("drift used unstable code: %#v", stale)
	}
}

func TestProposedKnowledgeIDEnablesAtomicReciprocalPromotion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "reciprocal", "--json", "--no-interactive")
	work := t.TempDir()
	originalID := "know_01arz3ndektsv4rrffq69g5faw"

	first := runCLI(t, "original evidence", "inbox", "add", "-", "--name", "original.txt", "--wiki", root, "--json", "--no-interactive")
	firstID := nestedString(t, first.Data, "items", 0, "id")
	firstDraft := filepath.Join(work, "original.md")
	if err := os.WriteFile(firstDraft, []byte("---\ntype: concept\ncategory: learning\ntitle: Original\ndescription: Original concept\nlifecycle: current\nrelated: []\nsupersedes: []\nsuperseded_by: []\n---\n# Original\n\nOriginal verified fact.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstManifest := filepath.Join(work, "first.json")
	firstManifestData := fmt.Sprintf(`{"schema_version":1,"inboxes":[{"id":%q,"payload_hash":%q,"item_hash":%q,"consume":true}],"targets":[{"operation":"create","draft_file":"original.md","knowledge_id":%q,"inbox_ids":[%q]}]}`,
		firstID, nestedString(t, first.Data, "items", 0, "payload_hash"), nestedString(t, first.Data, "items", 0, "item_hash"), originalID, firstID)
	if err := os.WriteFile(firstManifest, []byte(firstManifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	firstPlan := runCLI(t, "", "promote", "plan", "--manifest", firstManifest, "--wiki", root, "--json", "--no-interactive")
	runCLI(t, "", "promote", "apply", nestedString(t, firstPlan.Data, "promotion_id"), "--approve", nestedString(t, firstPlan.Data, "plan_hash"), "--wiki", root, "--json", "--no-interactive")

	maintenance := runCLI(t, "replacement evidence", "inbox", "add", "-", "--name", "replacement.txt", "--wiki", root, "--json", "--no-interactive")
	maintenanceID := nestedString(t, maintenance.Data, "items", 0, "id")
	maintenanceShow := runCLI(t, "", "inbox", "show", maintenanceID, "--wiki", root, "--json", "--no-interactive")
	originalShow := runCLI(t, "", "show", originalID, "--wiki", root, "--json", "--no-interactive")

	newDraft := filepath.Join(work, "replacement.md")
	createdDraft := runCLI(t, "", "template", "create", "concept", "--title", "Replacement", "--output", newDraft,
		"--set", "category=learning", "--set", "description=Replacement concept", "--wiki", root, "--json", "--no-interactive")
	newID := nestedString(t, createdDraft.Data, "proposed_knowledge_id")
	if err := os.WriteFile(newDraft, []byte(fmt.Sprintf("---\ntype: concept\ncategory: learning\ntitle: Replacement\ndescription: Replacement concept\nlifecycle: current\nrelated: []\nsupersedes: [%s]\nsuperseded_by: []\n---\n# Replacement\n\nReplacement verified fact.\n", originalID)), 0o600); err != nil {
		t.Fatal(err)
	}
	updateDraft := filepath.Join(work, "original-update.md")
	if err := os.WriteFile(updateDraft, []byte(fmt.Sprintf("---\ntype: concept\ncategory: learning\ntitle: Original\ndescription: Original concept\nlifecycle: superseded\nrelated: []\nsupersedes: []\nsuperseded_by: [%s]\n---\n# Original\n\nOriginal verified fact, retained as superseded history.\n", newID)), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(work, "maintenance.json")
	manifestData := fmt.Sprintf(`{"schema_version":1,"inboxes":[{"id":%q,"payload_hash":%q,"item_hash":%q,"consume":true}],"targets":[{"operation":"create","draft_file":"replacement.md","knowledge_id":%q,"inbox_ids":[%q]},{"operation":"update","draft_file":"original-update.md","knowledge_id":%q,"base_content_hash":%q,"base_file_hash":%q,"inbox_ids":[%q]}]}`,
		maintenanceID, nestedString(t, maintenanceShow.Data, "payload_hash"), nestedString(t, maintenanceShow.Data, "item_hash"),
		newID, maintenanceID, originalID, nestedString(t, originalShow.Data, "content_hash"), nestedString(t, originalShow.Data, "file_hash"), maintenanceID)
	if err := os.WriteFile(manifestPath, []byte(manifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := runCLI(t, "", "promote", "plan", "--manifest", manifestPath, "--wiki", root, "--json", "--no-interactive")
	applied := runCLI(t, "", "promote", "apply", nestedString(t, planned.Data, "promotion_id"), "--approve", nestedString(t, planned.Data, "plan_hash"), "--wiki", root, "--json", "--no-interactive")
	if nestedString(t, applied.Data, "transaction_state") != "complete" {
		t.Fatalf("reciprocal promotion did not complete: %#v", applied.Data)
	}
	updatedOriginal := runCLI(t, "", "show", originalID, "--wiki", root, "--json", "--no-interactive")
	publishedReplacement := runCLI(t, "", "show", newID, "--wiki", root, "--json", "--no-interactive")
	if nestedString(t, updatedOriginal.Data, "metadata", "extra", "superseded_by", 0) != newID ||
		nestedString(t, publishedReplacement.Data, "metadata", "extra", "supersedes", 0) != originalID {
		t.Fatalf("reciprocal relation was not atomically published: original=%#v replacement=%#v", updatedOriginal.Data, publishedReplacement.Data)
	}
}

func TestIndexFailureAfterPromotionReturnsWarningAndRecovers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	runCLI(t, "", "init", root, "--name", "index-failure", "--json", "--no-interactive")
	work := t.TempDir()
	added := runCLI(t, "payload", "inbox", "add", "-", "--name", "input.txt", "--wiki", root, "--json", "--no-interactive")
	id := nestedString(t, added.Data, "items", 0, "id")
	payloadHash := nestedString(t, added.Data, "items", 0, "payload_hash")
	itemHash := nestedString(t, added.Data, "items", 0, "item_hash")
	if err := os.WriteFile(filepath.Join(work, "draft.md"), []byte("---\ntype: concept\ncategory: learning\ntitle: Recoverable\ndescription: Complete knowledge\nlifecycle: current\n---\n# Recoverable\n\nCommitted before index recovery.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"schema_version":1,"inboxes":[{"id":%q,"payload_hash":%q,"item_hash":%q,"consume":true}],"targets":[{"operation":"create","draft_file":"draft.md","knowledge_id":"know_01arz3ndektsv4rrffq69g5faw","inbox_ids":[%q]}]}`, id, payloadHash, itemHash, id)
	manifestPath := filepath.Join(work, "promotion.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := runCLI(t, "", "promote", "plan", "--manifest", manifestPath, "--wiki", root, "--json", "--no-interactive")
	indexPath := filepath.Join(root, ".llm-wiki", "index.sqlite")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0o700); err != nil {
		t.Fatal(err)
	}
	applied := runCLI(t, "", "promote", "apply", nestedString(t, planned.Data, "promotion_id"), "--approve", nestedString(t, planned.Data, "plan_hash"), "--wiki", root, "--json", "--no-interactive")
	if len(applied.Warnings) != 1 || nestedString(t, applied.Data, "targets", 0, "knowledge_id") == "" || nestedString(t, applied.Data, "transaction_state") != "files_committed" {
		t.Fatalf("index failure did not preserve promotion result and warning: %#v", applied)
	}
	processed := runCLI(t, "", "inbox", "list", "--status", "processed", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, processed.Data, "count") != 1 {
		t.Fatalf("inbox was not committed before index failure: %#v", processed.Data)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	runCLI(t, "", "index", "update", "--wiki", root, "--json", "--no-interactive")
	query := runCLI(t, "", "query", "Committed recovery", "--wiki", root, "--json", "--no-interactive")
	if nestedFloat(t, query.Data, "count") < 1 {
		t.Fatalf("maintenance did not recover the committed promotion: %#v", query.Data)
	}
}

func runCLI(t *testing.T, stdin string, args ...string) Response {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := NewRootCommandWithIO(bytes.NewBufferString(stdin), &stdout, &stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("command %v failed: %v stderr=%s stdout=%s", args, err, stderr.String(), stdout.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response for %v: %v output=%s", args, err, stdout.String())
	}
	if !response.OK {
		t.Fatalf("command %v returned failure: %#v", args, response)
	}
	return response
}

func runCLIFailure(t *testing.T, stdin string, args ...string) Response {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := NewRootCommandWithIO(bytes.NewBufferString(stdin), &stdout, &stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("command %v unexpectedly succeeded: %s", args, stdout.String())
	} else {
		RenderFailure(cmd, err)
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode failure response for %v: %v output=%s", args, err, stdout.String())
	}
	return response
}

func nestedString(t *testing.T, value any, path ...any) string {
	t.Helper()
	current := value
	for _, part := range path {
		switch key := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				t.Fatalf("%v is not an object at %q", current, key)
			}
			current = object[key]
		case int:
			list, ok := current.([]any)
			if !ok || key >= len(list) {
				t.Fatalf("%v is not a list containing %d", current, key)
			}
			current = list[key]
		}
	}
	result, ok := current.(string)
	if !ok {
		t.Fatalf("%v is not a string", current)
	}
	return result
}

func nestedFloat(t *testing.T, value any, key string) float64 {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object", value)
	}
	result, ok := object[key].(float64)
	if !ok {
		t.Fatalf("%v is not numeric", object[key])
	}
	return result
}
