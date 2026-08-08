package publish

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"llm-wiki/internal/document"
	"llm-wiki/internal/vault"
)

func TestRecoveryRollsBackPreparedNewFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "recover", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	opID := "op_01arz3ndektsv4rrffq69g5fav"
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	targetRel := filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, "concept", "interrupted.md"))
	target := filepath.Join(cfg.Root, filepath.FromSlash(targetRel))
	newData := []byte("interrupted write")
	if err := document.AtomicWrite(target, newData, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, OperationID: opID, ChangeID: "chg_01arz3ndektsv4rrffq69g5fav",
		State: "prepared", TargetPath: targetRel, NewFileHash: document.HashBytes(newData),
		CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if err := writeJournal(txnDir, journal); err != nil {
		t.Fatal(err)
	}
	actions, err := Recover(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Action != "rolled_back" {
		t.Fatalf("unexpected recovery %#v", actions)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("prepared new target was not removed: %v", err)
	}
}

func TestRecoveryRestoresPreparedReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "recover-replace", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	opID := "op_01arz3ndektsv4rrffq69g5faw"
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	targetRel := filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, "concept", "replacement.md"))
	target := filepath.Join(cfg.Root, filepath.FromSlash(targetRel))
	oldData, newData := []byte("old trusted bytes"), []byte("new interrupted bytes")
	if err := document.AtomicWrite(filepath.Join(txnDir, "backup.md"), oldData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := document.AtomicWrite(target, newData, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, OperationID: opID, ChangeID: "chg_01arz3ndektsv4rrffq69g5faw",
		State: "prepared", TargetPath: targetRel, NewFileHash: document.HashBytes(newData), HadTarget: true,
		CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if err := writeJournal(txnDir, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != string(oldData) {
		t.Fatalf("old trusted file was not restored: %q %v", b, err)
	}
}

func TestLoadRejectsTraversalChangeIDAndTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "change-security", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	if _, _, err := Load(cfg, "chg_../../outside"); err == nil {
		t.Fatal("expected traversal change ID rejection")
	}

	changeID := "chg_01arz3ndektsv4rrffq69g5fav"
	proposal := Proposal{
		SchemaVersion: 1, ID: changeID, CreatedAt: time.Now().Format(time.RFC3339),
		Sources:     []document.SourceRef{{ID: "raw_01arz3ndektsv4rrffq69g5fav", ContentHash: document.HashBytes([]byte("raw"))}},
		KnowledgeID: "know_01arz3ndektsv4rrffq69g5fav", TargetPath: "../outside.md",
		NewContentHash: document.HashBytes([]byte("body")), FileHash: document.HashBytes([]byte("file")), DraftFile: "files/document.md",
	}
	proposalBytes := mustJSON(proposal)
	state := State{
		SchemaVersion: 1, Status: "proposed", ProposalHash: document.HashBytes(proposalBytes), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	dir := changeDir(cfg, changeID)
	if err := document.AtomicWrite(filepath.Join(dir, "proposal.json"), proposalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := document.AtomicWrite(filepath.Join(dir, "state.json"), mustJSON(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(cfg, changeID); err == nil {
		t.Fatal("expected proposal target traversal rejection")
	}
	if err := CompleteOperation(cfg, "op_../../outside"); err == nil {
		t.Fatal("expected traversal operation ID rejection")
	}
}

func TestRecoveryPreservesExternalChangeAfterPreparedTransaction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "wiki")
	initResult, err := vault.Init(vault.InitOptions{Path: root, Name: "recover-conflict", Template: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := initResult.Config
	opID := "op_01arz3ndektsv4rrffq69g5fax"
	txnDir := filepath.Join(cfg.RuntimeDir(), "transactions", opID)
	targetRel := filepath.ToSlash(filepath.Join(cfg.Paths.Knowledge, "concept", "external-change.md"))
	target := filepath.Join(cfg.Root, filepath.FromSlash(targetRel))
	oldData, stagedData, externalData := []byte("old"), []byte("staged"), []byte("external")
	if err := document.AtomicWrite(filepath.Join(txnDir, "backup.md"), oldData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := document.AtomicWrite(target, externalData, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := Journal{
		SchemaVersion: 1, OperationID: opID, ChangeID: "chg_01arz3ndektsv4rrffq69g5fax",
		State: "prepared", TargetPath: targetRel, NewFileHash: document.HashBytes(stagedData), HadTarget: true,
		CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if err := writeJournal(txnDir, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(cfg); err == nil {
		t.Fatal("expected recovery conflict")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != string(externalData) {
		t.Fatalf("recovery overwrote external change: %q %v", b, err)
	}
}
