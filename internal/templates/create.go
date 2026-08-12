package templates

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
	"llm-wiki/internal/fsutil"
	"llm-wiki/internal/governance"
)

type CreateOptions struct {
	Kind      string
	Name      string
	Title     string
	Output    string
	Set       []string
	Related   []string
	Overwrite bool
	DryRun    bool
	Now       time.Time
}

type CreateResult struct {
	Template        ContentTemplate `json:"template"`
	TemplateVersion string          `json:"template_version"`
	Output          string          `json:"output"`
	DryRun          bool            `json:"dry_run"`
	Overwritten     bool            `json:"overwritten"`
	UnfilledFields  []string        `json:"unfilled_fields"`
	PromptCount     int             `json:"prompt_count"`
	UnresolvedVars  []string        `json:"unresolved_variables"`
	NextCommandHint string          `json:"next_command_hint"`
}

var (
	propertyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	variablePattern     = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	systemProperties    = map[string]bool{
		"schema_version": true, "id": true, "type": true, "status": true,
		"captured_at": true, "published_at": true, "updated_at": true, "processed_at": true,
		"payload": true, "payload_hash": true, "payload_bytes": true, "knowledge_ids": true, "lineage": true,
		"content_hash": true, "media_type": true, "original_name": true,
		"governance_version": true,
	}
)

func CreateDraft(cfg *config.Instance, opts CreateOptions) (*CreateResult, error) {
	if cfg == nil {
		return nil, errors.New("template creation requires a resolved wiki")
	}
	if opts.Kind != "inbox" && opts.Kind != "knowledge" {
		return nil, errors.New("template kind must be inbox or knowledge")
	}
	if strings.TrimSpace(opts.Name) == "" || strings.TrimSpace(opts.Title) == "" || strings.TrimSpace(opts.Output) == "" {
		return nil, errors.New("template name, title, and output are required")
	}
	if strings.ContainsAny(opts.Title, "\r\n") {
		return nil, errors.New("template title must be a single line")
	}
	if len(opts.Related) > 0 && opts.Kind != "knowledge" {
		return nil, errors.New("--related is only valid for knowledge templates")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	policy, err := governance.Load(cfg)
	if err != nil {
		return nil, err
	}
	item, err := ReadContent(cfg, opts.Kind, opts.Name)
	if err != nil {
		return nil, err
	}
	content := strings.NewReplacer(
		"{{date:YYYY-MM-DD}}", opts.Now.Format("2006-01-02"),
		"{{time:HH:mm}}", opts.Now.Format("15:04"),
		"{{date}}", opts.Now.Format("2006-01-02"),
		"{{time}}", opts.Now.Format("15:04"),
	).Replace(item.Content)
	mapping, body, err := parseTemplate(content)
	if err != nil {
		return nil, err
	}
	if opts.Kind == "knowledge" {
		var declaredType string
		for _, rule := range policy.Types {
			if rule.Template == item.Path {
				declaredType = rule.Name
				break
			}
		}
		if declaredType == "" {
			return nil, fmt.Errorf("knowledge template %s is not declared by content pack %s@%s", item.Path, policy.Name, policy.Version)
		}
		templateType, ok := mappingString(mapping, "type")
		if !ok || templateType != declaredType {
			return nil, fmt.Errorf("knowledge template %s type must be %q", item.Path, declaredType)
		}
	}
	setMappingValue(mapping, "title", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.TrimSpace(opts.Title)})
	body = []byte(strings.ReplaceAll(string(body), "{{title}}", strings.TrimSpace(opts.Title)))
	for _, assignment := range opts.Set {
		key, value, ok := strings.Cut(assignment, "=")
		key = strings.TrimSpace(key)
		if !ok || !propertyNamePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid --set assignment %q; expected property=value", assignment)
		}
		if systemProperties[key] {
			return nil, fmt.Errorf("--set cannot override system property %s", key)
		}
		if key == "title" {
			return nil, errors.New("--set cannot override title; use --title so frontmatter and H1 stay consistent")
		}
		valueNode, err := parseYAMLValue(value)
		if err != nil {
			return nil, fmt.Errorf("parse --set %s: %w", key, err)
		}
		setMappingValue(mapping, key, valueNode)
	}
	if len(opts.Related) > 0 {
		relationField, ok := policy.DefaultCreateRelation()
		if !ok {
			return nil, errors.New("content pack does not declare a default_for_create relation for --related")
		}
		links, err := mappingStringList(mapping, relationField)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, link := range links {
			seen[link] = true
		}
		for _, id := range opts.Related {
			if _, err := document.FindByID(cfg.KnowledgeDir(), id); err != nil {
				return nil, fmt.Errorf("resolve relation target knowledge %s: %w", id, err)
			}
			if !seen[id] {
				seen[id] = true
				links = append(links, id)
			}
		}
		setMappingValue(mapping, relationField, stringSequence(links))
	}
	rendered, err := renderTemplate(mapping, body)
	if err != nil {
		return nil, err
	}
	target, resolvedTarget, err := resolvedOutputPath(opts.Output)
	if err != nil {
		return nil, err
	}
	for _, managed := range []string{cfg.InboxDir(), cfg.KnowledgeDir(), cfg.RuntimeDir()} {
		_, resolvedManaged, resolveErr := resolvedOutputPath(managed)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if pathInside(target, managed) || pathInside(resolvedTarget, resolvedManaged) {
			return nil, fmt.Errorf("template output cannot be written inside managed directory %s", managed)
		}
	}
	overwritten := false
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("template output must be a regular file, not a symbolic link")
		}
		if !opts.Overwrite {
			return nil, fmt.Errorf("template output already exists: %s", target)
		}
		overwritten = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	nextCommand := fmt.Sprintf("prepare a promotion manifest using %q, then run llm-wiki promote plan --manifest <file>", target)
	if opts.Kind == "inbox" {
		nextCommand = fmt.Sprintf("llm-wiki inbox add <input> --note-file %q", target)
	}
	result := &CreateResult{
		Template: item, TemplateVersion: cfg.Template.Version,
		Output: target, DryRun: opts.DryRun, Overwritten: overwritten,
		UnfilledFields:  unfilledFields(mapping),
		PromptCount:     strings.Count(string(body), "llm-wiki:prompt"),
		UnresolvedVars:  uniqueSorted(variablePattern.FindAllString(string(rendered), -1)),
		NextCommandHint: nextCommand,
	}
	result.Template.Content = ""
	if opts.DryRun {
		return result, nil
	}
	if err := fsutil.AtomicWrite(target, rendered, 0o600); err != nil {
		return nil, err
	}
	return result, nil
}

func parseTemplate(content string) (*yaml.Node, []byte, error) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(content, "\ufeff"), "\r\n", "\n"), "\r", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, nil, errors.New("content template requires YAML frontmatter")
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return nil, nil, errors.New("content template has unterminated YAML frontmatter")
	}
	frontmatter := normalized[4 : 4+end]
	body := []byte(normalized[4+end+5:])
	var documentNode yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &documentNode); err != nil {
		return nil, nil, err
	}
	if len(documentNode.Content) != 1 || documentNode.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("content template frontmatter must be a mapping")
	}
	return documentNode.Content[0], body, nil
}

func renderTemplate(mapping *yaml.Node, body []byte) ([]byte, error) {
	frontmatter, err := yaml.Marshal(mapping)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	out.Write(frontmatter)
	out.WriteString("---\n")
	out.Write(body)
	return out.Bytes(), nil
}

func parseYAMLValue(value string) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(value)), &node); err != nil {
		return nil, err
	}
	if len(node.Content) != 1 {
		return nil, errors.New("property value is empty")
	}
	return node.Content[0], nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func mappingStringList(mapping *yaml.Node, key string) ([]string, error) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		value := mapping.Content[i+1]
		if value.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("template property %s must be a list", key)
		}
		out := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
				return nil, fmt.Errorf("template property %s must contain text values", key)
			}
			out = append(out, item.Value)
		}
		return out, nil
	}
	return nil, nil
}

func mappingString(mapping *yaml.Node, key string) (string, bool) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		value := mapping.Content[i+1]
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return "", false
		}
		return value.Value, true
	}
	return "", false
}

func stringSequence(items []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, item := range items {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
	}
	return node
}

func unfilledFields(mapping *yaml.Node) []string {
	var out []string
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i].Value, mapping.Content[i+1]
		if value.Tag == "!!null" || (value.Kind == yaml.ScalarNode && strings.TrimSpace(value.Value) == "") {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueSorted(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func resolvedOutputPath(output string) (string, string, error) {
	target, err := filepath.Abs(output)
	if err != nil {
		return "", "", err
	}
	parent := filepath.Dir(target)
	for {
		info, statErr := os.Lstat(parent)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
				return "", "", fmt.Errorf("template output parent is not a directory: %s", parent)
			}
			resolvedParent, evalErr := filepath.EvalSymlinks(parent)
			if evalErr != nil {
				return "", "", evalErr
			}
			rel, relErr := filepath.Rel(parent, target)
			if relErr != nil {
				return "", "", relErr
			}
			return target, filepath.Join(resolvedParent, rel), nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", statErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", "", fmt.Errorf("cannot resolve template output parent for %s", target)
		}
		parent = next
	}
}

func pathInside(path, root string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		path, root = strings.ToLower(path), strings.ToLower(root)
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
