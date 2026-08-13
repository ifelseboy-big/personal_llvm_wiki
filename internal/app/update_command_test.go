package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestUpdateDryRunJSONProtocol(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs([]string{"update", "--dry-run", "--json", "--no-interactive"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode output %q: %v", stdout.String(), err)
	}
	if !response.OK || response.Command != "update" || response.Warnings == nil || response.AffectedFiles == nil || len(response.Warnings) != 0 || len(response.AffectedFiles) != 0 {
		t.Fatalf("unexpected response %#v", response)
	}
	data, ok := response.Data.(map[string]any)
	if !ok || data["action"] != "check" || data["dry_run"] != true || data["path"] == "" {
		t.Fatalf("unexpected update data %#v", response.Data)
	}
}

func TestUpdateRejectsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs([]string{"update", "unexpected", "--json", "--no-interactive"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected argument rejection")
	}
	if code := RenderFailure(root, err); code != ExitUsage {
		t.Fatalf("unexpected exit code %d", code)
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected response %#v", response)
	}
}
