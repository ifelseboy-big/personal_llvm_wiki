package architecture_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"llm-wiki/internal/app"
	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/governance"
	"llm-wiki/internal/inbox"
	indexstore "llm-wiki/internal/index"
	"llm-wiki/internal/promote"
	"llm-wiki/internal/skill"
	"llm-wiki/internal/templates"
)

func TestFirstPartyContractsMatchCurrentVersions(t *testing.T) {
	if app.ProtocolVersion != 1 || config.CurrentSchema != 1 || document.CurrentSchema != 1 ||
		governance.PolicySchemaVersion != 1 || inbox.BatchSchemaVersion != 1 || promote.SchemaVersion != 1 || indexstore.SchemaVersion != 1 ||
		indexstore.QueryPlannerVersion != "1" {
		t.Fatalf("first-party contract versions must match the current contract: response=%d instance=%d frontmatter=%d content_pack=%d inbox_batch=%d promotion=%d index=%d planner=%s",
			app.ProtocolVersion, config.CurrentSchema, document.CurrentSchema, governance.PolicySchemaVersion,
			inbox.BatchSchemaVersion, promote.SchemaVersion, indexstore.SchemaVersion, indexstore.QueryPlannerVersion)
	}

	manifest, err := templates.LoadManifest("personal")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.Version != "1.1.0" {
		t.Fatalf("personal manifest must match the current version: %#v", manifest)
	}
	policyData, err := templates.ReadFile("personal", manifest.ContentPack)
	if err != nil {
		t.Fatal(err)
	}
	var policy governance.Policy
	if err := json.Unmarshal(policyData, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.SchemaVersion != 1 || policy.Version != "1.1.0" || policy.GovernanceVersion != "personal-1.0.0" {
		t.Fatalf("personal policy must match the current version: %#v", policy)
	}
	if skill.SkillVersion != "1.1.0" {
		t.Fatalf("skill content must match the current version: %s", skill.SkillVersion)
	}
}

func TestSchemaAuthoritiesMatchCurrentVersions(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"content-pack", "instance", "promotion", "response"} {
		assertSchemaVersion(t, filepath.Join(root, "schemas", name+".schema.json"), false)
	}
	assertSchemaVersion(t, filepath.Join(root, "schemas", "frontmatter.schema.json"), true)
}

func assertSchemaVersion(t *testing.T, path string, nested bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	wantID := "https://llm-wiki.dev/schemas/" + filepath.Base(path)
	if schema["$id"] != wantID {
		t.Fatalf("schema id mismatch for %s: %v", path, schema["$id"])
	}
	if nested {
		variants, ok := schema["oneOf"].([]any)
		if !ok || len(variants) == 0 {
			t.Fatalf("frontmatter schema has no variants: %s", path)
		}
		for index, variant := range variants {
			if index == 0 {
				properties := variant.(map[string]any)["properties"].(map[string]any)
				version := properties["schema_version"].(map[string]any)
				if _, restricted := version["const"]; restricted {
					t.Fatal("Inbox schema must not reject existing material by version")
				}
				continue
			}
			assertJSONSchemaVersion(t, variant, fmt.Sprintf("%s oneOf[%d]", path, index), document.CurrentSchema)
		}
		return
	}
	assertJSONSchemaVersion(t, schema, path, 1)
}

func assertJSONSchemaVersion(t *testing.T, value any, label string, want int) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema object is invalid: %s", label)
	}
	properties, ok := object["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties are missing: %s", label)
	}
	version, ok := properties["schema_version"].(map[string]any)
	if !ok || version["const"] != float64(want) {
		t.Fatalf("schema_version must match the current contract: %s: %#v", label, version)
	}
}
