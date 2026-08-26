package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"llm-wiki/internal/app"
	"llm-wiki/internal/document"
	"llm-wiki/internal/inbox"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/templates"
	"llm-wiki/internal/vault"
)

func TestSelfUpdateDryRunDoesNotModifyVault(t *testing.T) {
	vaultRoot := filepath.Join(t.TempDir(), "wiki")
	if _, err := vault.Init(vault.InitOptions{Path: vaultRoot, Name: "update-dry-run", Template: "personal"}); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, vaultRoot)

	var stdout, stderr bytes.Buffer
	command := app.NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	command.SetArgs([]string{"update", "--wiki", vaultRoot, "--dry-run", "--json", "--no-interactive"})
	if err := command.Execute(); err != nil {
		t.Fatalf("update dry-run: %v stderr=%s", err, stderr.String())
	}
	var response app.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Command != "update" || response.Warnings == nil || response.AffectedFiles == nil || len(response.AffectedFiles) != 0 {
		t.Fatalf("unexpected update response %#v", response)
	}
	after := snapshotFiles(t, vaultRoot)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("update dry-run changed Vault\nbefore=%#v\nafter=%#v", before, after)
	}
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNoObsidianFullLifecycleAndRebuildEquivalence(t *testing.T) {
	for _, layout := range []string{"date-note", "item-payload"} {
		t.Run(layout, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "personal-wiki")
			initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "personal", Template: "personal"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := initialized.Config
			if _, err := os.Stat(filepath.Join(root, ".obsidian")); !os.IsNotExist(err) {
				t.Fatalf("init unexpectedly required .obsidian: %v", err)
			}
			var added []inbox.Added
			if layout == "date-note" {
				added, err = inbox.Add(cfg, inbox.AddOptions{Input: "-", Name: "source.txt", Source: "user", Stdin: bytes.NewBufferString("Stable IR separates compiler components."), Now: time.Unix(100, 0).UTC()})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				// Literal output from the original item/payload format, with edited content
				// and obsolete capture hashes. Raw payload frontmatter is not an Inbox record.
				id := "inbox_01arz3ndektsv4rrffq69g5fav"
				itemDir := filepath.Join(cfg.Paths.Inbox, "2026", "08", id)
				if err := os.MkdirAll(filepath.Join(root, itemDir, "payload"), 0o700); err != nil {
					t.Fatal(err)
				}
				item := fmt.Sprintf("---\nschema_version: 1\nid: %s\ntitle: Stable IR input\nstatus: pending\nsource: user\ncaptured_at: 2026-08-01T00:00:00Z\ncontent_hash: %s\nmedia_type: text/markdown\noriginal_name: source.md\npayload: payload/source.md\npayload_hash: %s\npayload_bytes: 3\ncustom_context: preserved\n---\nStable IR separates compiler components.", id, document.HashBytes([]byte("old body")), document.HashBytes([]byte("old")))
				if err := os.WriteFile(filepath.Join(root, itemDir, "item.md"), []byte(item), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, itemDir, "payload", "source.md"), []byte("---\nschema_version: [raw unfinished frontmatter\n---\nEdited compiler source material."), 0o600); err != nil {
					t.Fatal(err)
				}
				added = []inbox.Added{{ID: id, ItemPath: filepath.Join(itemDir, "item.md")}}
			}
			// Inbox is editable before publication; the manifest must bind its current bytes.
			itemPath := filepath.Join(root, added[0].ItemPath)
			itemBytes, err := os.ReadFile(itemPath)
			if err != nil {
				t.Fatal(err)
			}
			itemBytes = bytes.ReplaceAll(itemBytes, []byte("Stable IR separates compiler components."), []byte("Stable IR separates frontends and backends."))
			moved := filepath.Join(filepath.Dir(itemPath), "自由整理.md")
			if err := os.WriteFile(itemPath, itemBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(itemPath, moved); err != nil {
				t.Fatal(err)
			}
			beforeRead := snapshotFiles(t, root)
			current, err := inbox.Show(cfg, added[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeRead, snapshotFiles(t, root)) {
				t.Fatal("reading Inbox migrated or rewrote existing files")
			}
			if layout == "item-payload" {
				payload, err := os.ReadFile(current.PayloadPath)
				if err != nil || current.PayloadHash != document.HashBytes(payload) || current.PayloadHash == current.Metadata.PayloadHash || current.FileHash != document.HashBytes(itemBytes) {
					t.Fatalf("old Inbox did not snapshot current material: %#v %v", current, err)
				}
			}
			added[0].ItemHash, added[0].PayloadHash = current.FileHash, current.PayloadHash
			work := t.TempDir()
			draft := filepath.Join(work, "draft.md")
			if err := os.WriteFile(draft, []byte("---\ntype: note\ncategory: development\ntitle: Stable IR\ndescription: Stable compiler boundary\nlifecycle: current\n---\n# Stable IR\n\nStable IR separates compiler frontends, optimizers, and backends.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			knowledgeID := "know_01arz3ndektsv4rrffq69g5faw"
			manifest := promote.Manifest{SchemaVersion: 1,
				Inboxes: []promote.ManifestInbox{{ID: added[0].ID, PayloadHash: added[0].PayloadHash, ItemHash: added[0].ItemHash, Consume: true}},
				Targets: []promote.ManifestTarget{{Operation: "create", DraftFile: "draft.md", KnowledgeID: knowledgeID, InboxIDs: []string{added[0].ID}}}}
			data, _ := json.Marshal(manifest)
			manifestPath := filepath.Join(work, "promotion.json")
			if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			planned, err := promote.PlanPromotion(cfg, promote.PlanOptions{ManifestPath: manifestPath, Now: time.Unix(200, 0).UTC()})
			if err != nil {
				t.Fatal(err)
			}
			applied, err := promote.Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := indexstore.Rebuild(cfg); err != nil {
				t.Fatal(err)
			}
			if err := promote.CompleteOperation(cfg, applied.OperationID); err != nil {
				t.Fatal(err)
			}
			processed, err := inbox.Show(cfg, added[0].ID)
			if err != nil || processed.Metadata.Status != "processed" || processed.Path != moved || processed.Metadata.SchemaVersion != current.Metadata.SchemaVersion || processed.Metadata.Payload != current.Metadata.Payload || processed.Metadata.PayloadHash != current.Metadata.PayloadHash || processed.Metadata.PayloadBytes != current.Metadata.PayloadBytes {
				t.Fatalf("consume migrated or broke Inbox material: %#v %v", processed, err)
			}
			before, err := indexstore.SearchCandidates(cfg, "compiler frontends backends", 8)
			if err != nil || len(before) == 0 || before[0].KnowledgeID != knowledgeID {
				t.Fatalf("query before clean failed: %#v %v", before, err)
			}
			cleaned, err := inbox.Clean(cfg, inbox.CleanOptions{IDs: []string{added[0].ID}, Yes: true})
			if err != nil || cleaned.Deleted != 1 {
				t.Fatalf("processed clean failed: %#v %v", cleaned, err)
			}
			doc, err := document.FindByID(cfg.KnowledgeDir(), knowledgeID)
			if err != nil || doc.Metadata.Lineage[0].InboxID != added[0].ID || doc.Metadata.Lineage[0].PayloadHash != current.PayloadHash {
				t.Fatalf("Knowledge lost historical lineage: %#v %v", doc, err)
			}
			if err := doc.Validate("knowledge", true); err != nil {
				t.Fatalf("published Knowledge is not independently valid: %v", err)
			}
			if layout == "item-payload" {
				if _, err := os.Stat(current.PayloadPath); err != nil {
					t.Fatalf("cleanup removed the existing payload: %v", err)
				}
			}
			if err := os.Remove(indexstore.DBPath(cfg)); err != nil {
				t.Fatal(err)
			}
			if _, err := indexstore.Rebuild(cfg); err != nil {
				t.Fatal(err)
			}
			after, err := indexstore.SearchCandidates(cfg, "compiler frontends backends", 8)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("query changed after Inbox clean and index rebuild\nbefore=%#v\nafter=%#v\nerr=%v", before, after, err)
			}
		})
	}
}

func TestPersonalContentPackPublishesSixTypesAcrossFourDomains(t *testing.T) {
	root := filepath.Join(t.TempDir(), "personal-domains")
	initialized, err := vault.Init(vault.InitOptions{Path: root, Name: "personal-domains", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initialized.Config
	for _, name := range []string{"capture", "organize", "publish", "maintain", "query"} {
		if _, err := os.Stat(filepath.Join(root, "workflows", name+".md")); err != nil {
			t.Fatalf("initialized Vault omitted %s workflow: %v", name, err)
		}
	}
	type fixture struct {
		category string
		kind     string
		title    string
		keyword  string
		set      []string
		id       string
	}
	fixtures := []fixture{
		{category: "development", kind: "plan", title: "Audit export plan", keyword: "auditexporttoken", set: []string{"plan_state=approved", "stakeholders=[auditor]"}, id: "know_01arz3ndektsv4rrffq69g5faw"},
		{category: "learning", kind: "note", title: "Compiler pipeline notes", keyword: "compilerlearningtoken", set: []string{"source_references=[book-chapter-1]"}, id: "know_01arz3ndektsv4rrffq69g5fax"},
		{category: "configuration", kind: "config", title: "Build cache configuration", keyword: "cacheconfigurationtoken", set: []string{"system=build-cache", "environment=local", "security_reference=secret-manager/path-only"}, id: "know_01arz3ndektsv4rrffq69g5fay"},
		{category: "business", kind: "rule", title: "Refund review rule", keyword: "refundruletoken", set: []string{"rule_state=active", "rule_owner=finance"}, id: "know_01arz3ndektsv4rrffq69g5faz"},
		{category: "business", kind: "guide", title: "Refund handling guide", keyword: "refundguidetoken", set: []string{"owner=operations"}, id: "know_01arz3ndektsv4rrffq69g5fb0"},
		{category: "learning", kind: "review", title: "Compiler study review", keyword: "compilerreviewtoken", set: []string{"participants=[learner]"}, id: "know_01arz3ndektsv4rrffq69g5fb1"},
	}
	work := t.TempDir()
	manifest := promote.Manifest{SchemaVersion: 1}
	for i, item := range fixtures {
		added, err := inbox.Add(cfg, inbox.AddOptions{
			Input: "-", Name: item.kind + ".txt", Source: "e2e",
			Stdin: bytes.NewBufferString(item.keyword + " source material"), Now: time.Unix(int64(100+i), 0).UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		draftName := item.kind + ".md"
		draftPath := filepath.Join(work, draftName)
		set := append([]string{"category=" + item.category, "description=Representative " + item.category + " knowledge"}, item.set...)
		if _, err := templates.CreateDraft(cfg, templates.CreateOptions{
			Kind: "knowledge", Name: item.kind, Title: item.title, Output: draftPath, Set: set,
			Now: time.Unix(150, 0).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		draftBytes, err := os.ReadFile(draftPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := document.Parse(draftBytes); err != nil {
			t.Fatal(err)
		}
		frontmatterEnd := bytes.Index(draftBytes[4:], []byte("\n---\n"))
		if frontmatterEnd < 0 {
			t.Fatalf("generated draft %s has no closing frontmatter delimiter", draftPath)
		}
		frontmatterEnd += 4 + len("\n---\n")
		finalBody := []byte("# " + item.title + "\n\n" + item.keyword + " is verified representative content with scope, evidence, boundaries, and validation.\n")
		finalDraft := append(append([]byte(nil), draftBytes[:frontmatterEnd]...), finalBody...)
		if err := os.WriteFile(draftPath, finalDraft, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest.Inboxes = append(manifest.Inboxes, promote.ManifestInbox{
			ID: added[0].ID, PayloadHash: added[0].PayloadHash, ItemHash: added[0].ItemHash, Consume: true,
		})
		manifest.Targets = append(manifest.Targets, promote.ManifestTarget{
			Operation: "create", DraftFile: draftName, KnowledgeID: item.id, InboxIDs: []string{added[0].ID},
		})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(work, "promotion.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	planned, err := promote.PlanPromotion(cfg, promote.PlanOptions{ManifestPath: manifestPath, Now: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := promote.Apply(cfg, planned.Plan.ID, planned.PlanHash, false, time.Unix(300, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexstore.Rebuild(cfg); err != nil {
		t.Fatal(err)
	}
	if err := promote.CompleteOperation(cfg, applied.OperationID); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixtures {
		doc, err := document.FindByID(cfg.KnowledgeDir(), item.id)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Metadata.Type != item.kind || doc.Metadata.Extra["category"] != item.category {
			t.Fatalf("published metadata lost orthogonal category/type: %#v", doc.Metadata)
		}
		candidates, err := indexstore.SearchCandidates(cfg, item.keyword, 4)
		if err != nil || len(candidates) == 0 || candidates[0].KnowledgeID != item.id {
			t.Fatalf("query failed for %s/%s: %#v %v", item.category, item.kind, candidates, err)
		}
	}
}
