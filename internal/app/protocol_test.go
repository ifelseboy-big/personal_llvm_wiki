package app

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIJSONSuccessEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	wiki := filepath.Join(t.TempDir(), "wiki")
	root.SetArgs([]string{"init", wiki, "--name", "cli-test", "--json", "--no-interactive"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode output %q: %v", stdout.String(), err)
	}
	if !response.OK || response.SchemaVersion != ProtocolVersion || response.Command != "init" || response.Wiki == nil {
		t.Fatalf("unexpected response %#v", response)
	}
	if response.Warnings == nil || response.AffectedFiles == nil {
		t.Fatalf("JSON arrays must not be null: %#v", response)
	}
}

func TestCLIJSONUsageFailureEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs([]string{"--json", "--unknown"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected flag error")
	}
	if code := RenderFailure(root, err); code != ExitUsage {
		t.Fatalf("expected usage exit, got %d", code)
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode output %q: %v", stdout.String(), err)
	}
	if response.OK || response.Error == nil || response.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected response %#v", response)
	}
}

func TestCLIJSONResolvedFailureIncludesWikiIdentity(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "wiki")
	var initOut, initErr bytes.Buffer
	initCommand := NewRootCommandWithIO(strings.NewReader(""), &initOut, &initErr)
	initCommand.SetArgs([]string{"init", rootPath, "--name", "failure-wiki", "--json", "--no-interactive"})
	if err := initCommand.Execute(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRootCommandWithIO(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs([]string{"show", "know_missing", "--wiki", rootPath, "--json", "--no-interactive"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected missing knowledge error")
	}
	if code := RenderFailure(root, err); code != ExitNotFound {
		t.Fatalf("expected not-found exit, got %d", code)
	}
	var response Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Wiki == nil || response.Wiki.Name != "failure-wiki" || response.Wiki.ID == "" {
		t.Fatalf("resolved failure omitted wiki identity: %#v", response)
	}
}
