package governance

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
)

const (
	PolicySchemaVersion = 1
	maxPolicyBytes      = 1024 * 1024
)

var (
	namePattern             = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	typeNamePattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	versionPattern          = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	templateVariablePattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	footnotePattern         = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
	footnoteDefinition      = regexp.MustCompile(`^\[\^([^\]\s]+)\]:[ \t]*(.*)$`)
	reservedProperties      = map[string]bool{
		"schema_version": true, "id": true, "type": true, "title": true, "status": true,
		"source": true, "captured_at": true, "published_at": true, "updated_at": true,
		"content_hash": true, "media_type": true, "original_name": true, "payload": true,
		"payload_hash": true, "payload_bytes": true, "processed_at": true, "knowledge_ids": true,
		"lineage": true, "tags": true, "aliases": true, "governance_version": true,
	}
)

type Policy struct {
	SchemaVersion     int                  `json:"schema_version"`
	Name              string               `json:"name"`
	Version           string               `json:"version"`
	GovernanceVersion string               `json:"governance_version"`
	Categories        []NamedDefinition    `json:"categories"`
	Types             []TypeRule           `json:"types"`
	Knowledge         KnowledgeRules       `json:"knowledge"`
	Workflows         []WorkflowDefinition `json:"workflows"`
}

type NamedDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type TypeRule struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Template    string      `json:"template"`
	Fields      []FieldRule `json:"fields"`
}

type FieldRule struct {
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	Required     bool               `json:"required,omitempty"`
	Values       []string           `json:"values,omitempty"`
	ValuesFrom   string             `json:"values_from,omitempty"`
	Unique       bool               `json:"unique,omitempty"`
	RequiredWhen *RequiredCondition `json:"required_when,omitempty"`
}

type RequiredCondition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

type KnowledgeRules struct {
	Fields    []FieldRule    `json:"fields"`
	Relations []RelationRule `json:"relations"`
	Lifecycle *LifecycleRule `json:"lifecycle,omitempty"`
	Quality   QualityRules   `json:"quality"`
}

type RelationRule struct {
	Field            string `json:"field"`
	Reciprocal       string `json:"reciprocal,omitempty"`
	DefaultForCreate bool   `json:"default_for_create,omitempty"`
}

type LifecycleRule struct {
	Field              string   `json:"field"`
	InactiveValues     []string `json:"inactive_values"`
	DisputedValues     []string `json:"disputed_values"`
	ValidFromField     string   `json:"valid_from_field,omitempty"`
	ValidUntilField    string   `json:"valid_until_field,omitempty"`
	ReviewAfterField   string   `json:"review_after_field,omitempty"`
	ExcludeNotYetValid bool     `json:"exclude_not_yet_valid,omitempty"`
	ExcludeExpired     bool     `json:"exclude_expired,omitempty"`
}

type QualityRules struct {
	RequireH1Title           bool `json:"require_h1_title"`
	RejectTemplateVariables  bool `json:"reject_template_variables"`
	RejectPromptComments     bool `json:"reject_prompt_comments"`
	RequireCompleteFootnotes bool `json:"require_complete_footnotes"`
}

type WorkflowDefinition struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type LifecycleAssessment struct {
	Field       string   `json:"field,omitempty"`
	Lifecycle   string   `json:"value,omitempty"`
	Inactive    bool     `json:"inactive"`
	Disputed    bool     `json:"disputed"`
	NotYetValid bool     `json:"not_yet_valid"`
	Expired     bool     `json:"expired"`
	ReviewDue   bool     `json:"review_due"`
	Warnings    []string `json:"warnings"`
}

type RetrievalConstraints struct {
	Active        bool
	NotBeforeUnix *int64
	NotAfterUnix  *int64
}

type Relation struct {
	Property string
	Link     string
	Target   *document.Document
}

func Load(cfg *config.Instance) (*Policy, error) {
	if cfg == nil {
		return nil, errors.New("content pack requires a resolved wiki")
	}
	path := cfg.ContentPackPath()
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
		return nil, fmt.Errorf("content pack path: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read content pack: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("content pack must be a regular file")
	}
	if info.Size() > maxPolicyBytes {
		return nil, fmt.Errorf("content pack exceeds %d byte limit", maxPolicyBytes)
	}
	if err := fsutil.EnsureSingleLink(path); err != nil {
		return nil, fmt.Errorf("content pack: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	policy, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if policy.Name != cfg.Template.Name || policy.Version != cfg.Template.Version {
		return nil, fmt.Errorf("content pack identity %s@%s does not match instance template %s@%s", policy.Name, policy.Version, cfg.Template.Name, cfg.Template.Version)
	}
	for _, item := range policy.Types {
		if err := validatePackReference(cfg, item.Template, "type template"); err != nil {
			return nil, err
		}
	}
	for _, item := range policy.Workflows {
		if err := validatePackReference(cfg, item.Path, "workflow"); err != nil {
			return nil, err
		}
	}
	return policy, nil
}

func Parse(data []byte) (*Policy, error) {
	var policy Policy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("parse content pack: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("parse content pack: %w", err)
	}
	if err := policy.validate(); err != nil {
		return nil, fmt.Errorf("validate content pack: %w", err)
	}
	return &policy, nil
}

func Version(cfg *config.Instance) (string, error) {
	policy, err := Load(cfg)
	if err != nil {
		return "", err
	}
	return policy.GovernanceVersion, nil
}

func Hash(cfg *config.Instance) (string, error) {
	policy, err := Load(cfg)
	if err != nil {
		return "", err
	}
	return policy.Hash()
}

func (p *Policy) Hash() (string, error) {
	if p == nil {
		return "", errors.New("content pack hash requires a policy")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return document.HashBytes(data), nil
}

func ValidateForPromotion(cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document, now time.Time) error {
	policy, err := Load(cfg)
	if err != nil {
		return err
	}
	return validateKnowledge(policy, cfg, doc, prospective, now)
}

func ValidateForPromotionWithPolicy(policy *Policy, cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document, now time.Time) error {
	return validateKnowledge(policy, cfg, doc, prospective, now)
}

func ValidateStored(cfg *config.Instance, doc *document.Document, now time.Time) error {
	policy, err := Load(cfg)
	if err != nil {
		return err
	}
	return validateKnowledge(policy, cfg, doc, nil, now)
}

func validateKnowledge(policy *Policy, cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document, now time.Time) error {
	if policy == nil || cfg == nil || doc == nil {
		return errors.New("knowledge governance validation requires a content pack, wiki, and document")
	}
	if now.IsZero() {
		now = time.Now()
	}
	var problems []error
	if doc.Metadata.GovernanceVersion != policy.GovernanceVersion {
		problems = append(problems, fmt.Errorf("knowledge governance_version must be %q", policy.GovernanceVersion))
	}
	typeRule := policy.typeRule(doc.Metadata.Type)
	if typeRule == nil {
		problems = append(problems, fmt.Errorf("knowledge type %q is not declared by content pack %s@%s", doc.Metadata.Type, policy.Name, policy.Version))
	}
	for _, rule := range policy.Knowledge.Fields {
		if err := validateField(doc.Metadata, rule, policy, "content pack"); err != nil {
			problems = append(problems, err)
		}
	}
	if typeRule != nil {
		for _, rule := range typeRule.Fields {
			if err := validateField(doc.Metadata, rule, policy, "knowledge type "+typeRule.Name); err != nil {
				problems = append(problems, err)
			}
		}
	}
	body := lintableMarkdown(string(document.NormalizeMarkdownBody(doc.Body)))
	metadataJSON, _ := json.Marshal(doc.Metadata)
	if policy.Knowledge.Quality.RejectTemplateVariables && (templateVariablePattern.MatchString(body) || templateVariablePattern.Match(metadataJSON)) {
		problems = append(problems, errors.New("knowledge draft contains unresolved template variables"))
	}
	if policy.Knowledge.Quality.RejectPromptComments && (strings.Contains(body, "llm-wiki:prompt") || bytesContains(metadataJSON, "llm-wiki:prompt")) {
		problems = append(problems, errors.New("knowledge draft contains unresolved llm-wiki prompt comments"))
	}
	if policy.Knowledge.Quality.RequireH1Title {
		if heading := firstHeading(doc.Body); heading == "" {
			problems = append(problems, errors.New("knowledge body requires a level-one heading"))
		} else if heading != strings.TrimSpace(doc.Metadata.Title) {
			problems = append(problems, fmt.Errorf("knowledge title %q does not match first H1 %q", doc.Metadata.Title, heading))
		}
	}
	if policy.Knowledge.Quality.RequireCompleteFootnotes {
		if err := ValidateCitations(doc, false); err != nil {
			problems = append(problems, err)
		}
	}
	relations, relationErr := validateRelations(policy, cfg, doc, prospective)
	if relationErr != nil {
		problems = append(problems, relationErr)
	} else if err := validateReciprocals(policy, doc, relations); err != nil {
		problems = append(problems, err)
	}
	if _, err := assessLifecycle(policy, doc.Metadata, now); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func ValidateCitations(doc *document.Document, requireReference bool) error {
	if doc == nil {
		return errors.New("citation validation requires a document")
	}
	definitions := map[string]string{}
	references := map[string]bool{}
	var problems []error
	for _, line := range strings.Split(lintableMarkdown(string(document.NormalizeMarkdownBody(doc.Body))), "\n") {
		remaining := line
		if match := footnoteDefinition.FindStringSubmatch(line); match != nil {
			label := match[1]
			if _, duplicate := definitions[label]; duplicate {
				problems = append(problems, fmt.Errorf("duplicate footnote definition %q", label))
			}
			definitions[label] = strings.TrimSpace(match[2])
			remaining = ""
		}
		for _, match := range footnotePattern.FindAllStringSubmatch(remaining, -1) {
			references[match[1]] = true
		}
	}
	if requireReference && len(references) == 0 {
		problems = append(problems, errors.New("knowledge body requires at least one footnote reference"))
	}
	for _, label := range sortedMapKeys(references) {
		if _, exists := definitions[label]; !exists {
			problems = append(problems, fmt.Errorf("footnote %q has no definition", label))
		}
	}
	for _, label := range sortedMapKeys(definitions) {
		if !references[label] {
			problems = append(problems, fmt.Errorf("footnote definition %q is unused", label))
		}
	}
	return errors.Join(problems...)
}

func ValidateRelations(cfg *config.Instance, doc *document.Document) ([]Relation, error) {
	policy, err := Load(cfg)
	if err != nil {
		return nil, err
	}
	return validateRelations(policy, cfg, doc, nil)
}

func validateRelations(policy *Policy, cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document) ([]Relation, error) {
	if policy == nil || cfg == nil || doc == nil {
		return nil, errors.New("relation validation requires a content pack, wiki, and document")
	}
	var relations []Relation
	var problems []error
	for _, rule := range policy.Knowledge.Relations {
		links, exists, err := ExtraStringList(doc.Metadata, rule.Field)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !exists {
			continue
		}
		seen := map[string]bool{}
		for _, id := range links {
			if seen[id] {
				problems = append(problems, fmt.Errorf("duplicate %s knowledge id %q", rule.Field, id))
				continue
			}
			seen[id] = true
			if !document.ValidID("know", id) {
				problems = append(problems, fmt.Errorf("%s relation %q is not a knowledge id", rule.Field, id))
				continue
			}
			target := prospective[id]
			var resolveErr error
			if target == nil {
				target, resolveErr = document.FindByID(cfg.KnowledgeDir(), id)
			}
			if resolveErr != nil {
				problems = append(problems, fmt.Errorf("%s: %w", rule.Field, resolveErr))
				continue
			}
			if err := target.Validate("knowledge", true); err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", rule.Field, err))
				continue
			}
			if target.Metadata.ID == doc.Metadata.ID {
				problems = append(problems, fmt.Errorf("%s cannot link knowledge %s to itself", rule.Field, doc.Metadata.ID))
				continue
			}
			relations = append(relations, Relation{Property: rule.Field, Link: id, Target: target})
		}
	}
	return relations, errors.Join(problems...)
}

func ValidateReciprocalRelations(cfg *config.Instance, doc *document.Document) error {
	policy, err := Load(cfg)
	if err != nil {
		return err
	}
	relations, err := validateRelations(policy, cfg, doc, nil)
	if err != nil {
		return err
	}
	return validateReciprocals(policy, doc, relations)
}

func validateReciprocals(policy *Policy, doc *document.Document, relations []Relation) error {
	var problems []error
	for _, relation := range relations {
		rule := policy.relationRule(relation.Property)
		if rule == nil || rule.Reciprocal == "" {
			continue
		}
		links, _, err := ExtraStringList(relation.Target.Metadata, rule.Reciprocal)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		found := false
		for _, id := range links {
			if id == doc.Metadata.ID {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Errorf("knowledge %s %s %s but reciprocal %s link is missing", doc.Metadata.ID, relation.Property, relation.Target.Metadata.ID, rule.Reciprocal))
		}
	}
	return errors.Join(problems...)
}

func AssessStoredLifecycle(cfg *config.Instance, meta document.Metadata, now time.Time) (LifecycleAssessment, error) {
	policy, err := Load(cfg)
	if err != nil {
		return LifecycleAssessment{}, err
	}
	return assessLifecycle(policy, meta, now)
}

func RetrievalConstraintsForStored(cfg *config.Instance, meta document.Metadata) (RetrievalConstraints, error) {
	policy, err := Load(cfg)
	if err != nil {
		return RetrievalConstraints{}, err
	}
	lifecycle := policy.Knowledge.Lifecycle
	if lifecycle == nil {
		return RetrievalConstraints{Active: true}, nil
	}
	value, exists, err := ExtraString(meta, lifecycle.Field)
	if err != nil {
		return RetrievalConstraints{}, err
	}
	if !exists || value == "" || value != strings.TrimSpace(value) {
		return RetrievalConstraints{}, fmt.Errorf("knowledge property %s is invalid for retrieval", lifecycle.Field)
	}
	constraints := RetrievalConstraints{Active: !contains(lifecycle.InactiveValues, value)}
	if lifecycle.ExcludeNotYetValid && lifecycle.ValidFromField != "" {
		value, exists, _, dateErr := extraDate(meta, lifecycle.ValidFromField, time.Local)
		if dateErr != nil {
			return RetrievalConstraints{}, dateErr
		}
		if exists {
			seconds := value.Unix()
			constraints.NotBeforeUnix = &seconds
		}
	}
	if lifecycle.ExcludeExpired && lifecycle.ValidUntilField != "" {
		value, exists, dateOnly, dateErr := extraDate(meta, lifecycle.ValidUntilField, time.Local)
		if dateErr != nil {
			return RetrievalConstraints{}, dateErr
		}
		if exists {
			seconds := endOfDate(value, dateOnly).Unix()
			constraints.NotAfterUnix = &seconds
		}
	}
	return constraints, nil
}

func assessLifecycle(policy *Policy, meta document.Metadata, now time.Time) (LifecycleAssessment, error) {
	assessment := LifecycleAssessment{Warnings: []string{}}
	if policy == nil || policy.Knowledge.Lifecycle == nil {
		return assessment, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	rule := policy.Knowledge.Lifecycle
	value, ok, err := ExtraString(meta, rule.Field)
	if err != nil {
		return assessment, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		return assessment, fmt.Errorf("knowledge property %s is required for lifecycle assessment", rule.Field)
	}
	if value != strings.TrimSpace(value) {
		return assessment, fmt.Errorf("knowledge property %s must not contain surrounding whitespace", rule.Field)
	}
	assessment.Field = rule.Field
	assessment.Lifecycle = value
	assessment.Inactive = contains(rule.InactiveValues, value)
	assessment.Disputed = contains(rule.DisputedValues, value)
	if assessment.Disputed {
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s has disputed %s %s", meta.ID, rule.Field, value))
	}
	if assessment.Inactive {
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s has inactive %s %s and is included only for audit", meta.ID, rule.Field, value))
	}
	if rule.ValidFromField != "" {
		if date, exists, dateOnly, dateErr := extraDate(meta, rule.ValidFromField, now.Location()); dateErr != nil {
			return assessment, dateErr
		} else if exists && now.Before(date) {
			assessment.NotYetValid = true
			assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s is not valid until %s", meta.ID, formatDate(date, dateOnly)))
		}
	}
	if rule.ValidUntilField != "" {
		if date, exists, dateOnly, dateErr := extraDate(meta, rule.ValidUntilField, now.Location()); dateErr != nil {
			return assessment, dateErr
		} else if exists && now.After(endOfDate(date, dateOnly)) {
			assessment.Expired = true
			assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s expired on %s", meta.ID, formatDate(date, dateOnly)))
		}
	}
	if rule.ReviewAfterField != "" {
		if date, exists, dateOnly, dateErr := extraDate(meta, rule.ReviewAfterField, now.Location()); dateErr != nil {
			return assessment, dateErr
		} else if exists && !now.Before(date) {
			assessment.ReviewDue = true
			assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s requires review since %s", meta.ID, formatDate(date, dateOnly)))
		}
	}
	assessment.Warnings = SortedWarnings(assessment.Warnings)
	return assessment, nil
}

func ExtraString(meta document.Metadata, key string) (string, bool, error) {
	value, exists := meta.Extra[key]
	if !exists || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", true, fmt.Errorf("knowledge property %s must be text", key)
	}
	return text, true, nil
}

func ExtraStringList(meta document.Metadata, key string) ([]string, bool, error) {
	value, exists := meta.Extra[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	var out []string
	switch items := value.(type) {
	case []string:
		out = append(out, items...)
	case []any:
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("knowledge property %s must be a list of text", key)
			}
			out = append(out, text)
		}
	default:
		return nil, true, fmt.Errorf("knowledge property %s must be a list of text", key)
	}
	return out, true, nil
}

func ExtraDate(meta document.Metadata, key string) (time.Time, bool, error) {
	value, exists, _, err := extraDate(meta, key, time.Local)
	return value, exists, err
}

func validateField(meta document.Metadata, rule FieldRule, policy *Policy, owner string) error {
	value, exists := meta.Extra[rule.Name]
	required := rule.Required
	if rule.RequiredWhen != nil {
		other, otherExists := meta.Extra[rule.RequiredWhen.Field]
		required = required || (otherExists && fmt.Sprint(other) == rule.RequiredWhen.Equals)
	}
	if !exists || value == nil || (rule.Kind == "string" && strings.TrimSpace(fmt.Sprint(value)) == "") {
		if required {
			return fmt.Errorf("knowledge property %s is required by %s", rule.Name, owner)
		}
		return nil
	}
	switch rule.Kind {
	case "string":
		text, ok := value.(string)
		if !ok || text != strings.TrimSpace(text) || text == "" {
			return fmt.Errorf("knowledge property %s must be non-empty trimmed text", rule.Name)
		}
	case "enum":
		text, ok := value.(string)
		if !ok || text != strings.TrimSpace(text) || text == "" {
			return fmt.Errorf("knowledge property %s must be a declared text value", rule.Name)
		}
		values := rule.Values
		if rule.ValuesFrom == "categories" {
			values = policy.categoryNames()
		}
		if !contains(values, text) {
			return fmt.Errorf("knowledge property %s value %q is not declared by %s", rule.Name, text, owner)
		}
	case "string_list":
		items, _, err := ExtraStringList(meta, rule.Name)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, item := range items {
			if item == "" || item != strings.TrimSpace(item) {
				return fmt.Errorf("knowledge property %s must contain non-empty trimmed text", rule.Name)
			}
			if rule.Unique && seen[item] {
				return fmt.Errorf("knowledge property %s must contain unique values", rule.Name)
			}
			seen[item] = true
		}
	case "date":
		if _, _, err := ExtraDate(meta, rule.Name); err != nil {
			return err
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("knowledge property %s must be boolean", rule.Name)
		}
	case "integer":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		default:
			return fmt.Errorf("knowledge property %s must be an integer", rule.Name)
		}
	default:
		return fmt.Errorf("content pack field %s has unsupported kind %q", rule.Name, rule.Kind)
	}
	return nil
}

func (p *Policy) validate() error {
	if p.SchemaVersion != PolicySchemaVersion {
		return fmt.Errorf("unsupported content pack schema_version %d", p.SchemaVersion)
	}
	if !namePattern.MatchString(p.Name) || !versionPattern.MatchString(p.Version) || strings.TrimSpace(p.GovernanceVersion) == "" || p.GovernanceVersion != strings.TrimSpace(p.GovernanceVersion) {
		return errors.New("content pack name, version, or governance_version is invalid")
	}
	var problems []error
	categories := map[string]bool{}
	for _, item := range p.Categories {
		if !namePattern.MatchString(item.Name) || strings.TrimSpace(item.Description) == "" {
			problems = append(problems, fmt.Errorf("invalid content pack category %q", item.Name))
		} else if categories[item.Name] {
			problems = append(problems, fmt.Errorf("duplicate content pack category %q", item.Name))
		}
		categories[item.Name] = true
	}
	if len(categories) == 0 {
		problems = append(problems, errors.New("content pack requires at least one category"))
	}
	commonFields := map[string]FieldRule{}
	for _, field := range p.Knowledge.Fields {
		if err := p.validateFieldRule(field, commonFields); err != nil {
			problems = append(problems, err)
		}
		commonFields[field.Name] = field
	}
	for _, field := range p.Knowledge.Fields {
		if field.RequiredWhen != nil {
			if _, exists := commonFields[field.RequiredWhen.Field]; !exists {
				problems = append(problems, fmt.Errorf("common field %s required_when references undeclared field %s", field.Name, field.RequiredWhen.Field))
			}
		}
	}
	types := map[string]bool{}
	typeTemplates := map[string]bool{}
	allFields := make(map[string]bool, len(commonFields))
	for name := range commonFields {
		allFields[name] = true
	}
	for _, item := range p.Types {
		if !typeNamePattern.MatchString(item.Name) || strings.TrimSpace(item.Description) == "" || !safePackRelative(item.Template) || filepath.Ext(item.Template) != ".md" {
			problems = append(problems, fmt.Errorf("invalid content pack type %q", item.Name))
		}
		if types[item.Name] {
			problems = append(problems, fmt.Errorf("duplicate content pack type %q", item.Name))
		}
		types[item.Name] = true
		if typeTemplates[item.Template] {
			problems = append(problems, fmt.Errorf("duplicate content pack type template %q", item.Template))
		}
		typeTemplates[item.Template] = true
		fields := make(map[string]FieldRule, len(commonFields)+len(item.Fields))
		for key, field := range commonFields {
			fields[key] = field
		}
		for _, field := range item.Fields {
			if _, exists := commonFields[field.Name]; exists {
				problems = append(problems, fmt.Errorf("type %s redeclares common field %s", item.Name, field.Name))
			}
			if err := p.validateFieldRule(field, fields); err != nil {
				problems = append(problems, fmt.Errorf("type %s: %w", item.Name, err))
			}
			fields[field.Name] = field
			allFields[field.Name] = true
		}
		for _, field := range item.Fields {
			if field.RequiredWhen != nil {
				if _, exists := fields[field.RequiredWhen.Field]; !exists {
					problems = append(problems, fmt.Errorf("type %s field %s required_when references undeclared field %s", item.Name, field.Name, field.RequiredWhen.Field))
				}
			}
		}
	}
	if len(types) == 0 {
		problems = append(problems, errors.New("content pack requires at least one type"))
	}
	relations := map[string]RelationRule{}
	defaultCreateRelations := 0
	for _, rule := range p.Knowledge.Relations {
		if !namePattern.MatchString(rule.Field) || reservedProperties[rule.Field] || (rule.Reciprocal != "" && !namePattern.MatchString(rule.Reciprocal)) {
			problems = append(problems, fmt.Errorf("invalid relation field %q", rule.Field))
		}
		if _, duplicate := relations[rule.Field]; duplicate {
			problems = append(problems, fmt.Errorf("duplicate relation field %q", rule.Field))
		}
		if allFields[rule.Field] {
			problems = append(problems, fmt.Errorf("relation field %s conflicts with a declared content field", rule.Field))
		}
		if rule.DefaultForCreate {
			defaultCreateRelations++
		}
		relations[rule.Field] = rule
	}
	if defaultCreateRelations > 1 {
		problems = append(problems, errors.New("content pack can declare at most one default_for_create relation"))
	}
	for _, rule := range p.Knowledge.Relations {
		if rule.Reciprocal == "" {
			continue
		}
		reciprocal, exists := relations[rule.Reciprocal]
		if !exists || reciprocal.Reciprocal != rule.Field {
			problems = append(problems, fmt.Errorf("relation %s reciprocal %s is not symmetrically declared", rule.Field, rule.Reciprocal))
		}
	}
	if lifecycle := p.Knowledge.Lifecycle; lifecycle != nil {
		field, exists := commonFields[lifecycle.Field]
		if !exists || field.Kind != "enum" {
			problems = append(problems, fmt.Errorf("lifecycle field %s must be a common enum field", lifecycle.Field))
		} else {
			for _, value := range append(append([]string{}, lifecycle.InactiveValues...), lifecycle.DisputedValues...) {
				if !contains(field.Values, value) {
					problems = append(problems, fmt.Errorf("lifecycle value %q is not declared by field %s", value, lifecycle.Field))
				}
			}
		}
		for _, name := range []string{lifecycle.ValidFromField, lifecycle.ValidUntilField, lifecycle.ReviewAfterField} {
			if name == "" {
				continue
			}
			field, exists := commonFields[name]
			if !exists || field.Kind != "date" {
				problems = append(problems, fmt.Errorf("lifecycle date field %s must be a common date field", name))
			}
		}
	}
	workflows := map[string]bool{}
	workflowPaths := map[string]bool{}
	for _, item := range p.Workflows {
		if !namePattern.MatchString(item.Name) || !safePackRelative(item.Path) || filepath.Ext(item.Path) != ".md" {
			problems = append(problems, fmt.Errorf("invalid workflow %q", item.Name))
		}
		if workflows[item.Name] {
			problems = append(problems, fmt.Errorf("duplicate workflow %q", item.Name))
		}
		if workflowPaths[item.Path] {
			problems = append(problems, fmt.Errorf("duplicate workflow path %q", item.Path))
		}
		workflows[item.Name] = true
		workflowPaths[item.Path] = true
	}
	if len(workflows) == 0 {
		problems = append(problems, errors.New("content pack requires at least one workflow"))
	}
	return errors.Join(problems...)
}

func (p *Policy) validateFieldRule(rule FieldRule, already map[string]FieldRule) error {
	if !namePattern.MatchString(rule.Name) {
		return fmt.Errorf("invalid field name %q", rule.Name)
	}
	if reservedProperties[rule.Name] {
		return fmt.Errorf("field %s is reserved for the core", rule.Name)
	}
	if _, duplicate := already[rule.Name]; duplicate {
		return fmt.Errorf("duplicate field %q", rule.Name)
	}
	if !contains([]string{"string", "string_list", "enum", "date", "boolean", "integer"}, rule.Kind) {
		return fmt.Errorf("field %s has unsupported kind %q", rule.Name, rule.Kind)
	}
	if rule.ValuesFrom != "" && rule.ValuesFrom != "categories" {
		return fmt.Errorf("field %s has unsupported values_from %q", rule.Name, rule.ValuesFrom)
	}
	if rule.Kind != "enum" && (len(rule.Values) > 0 || rule.ValuesFrom != "") {
		return fmt.Errorf("field %s can declare values only for enum kind", rule.Name)
	}
	if rule.Kind == "enum" && (len(rule.Values) == 0) == (rule.ValuesFrom == "") {
		return fmt.Errorf("enum field %s must declare exactly one of values or values_from", rule.Name)
	}
	if duplicate := duplicateString(rule.Values); duplicate != "" {
		return fmt.Errorf("field %s has duplicate enum value %q", rule.Name, duplicate)
	}
	if rule.Unique && rule.Kind != "string_list" {
		return fmt.Errorf("field %s can require unique values only for string_list kind", rule.Name)
	}
	if rule.RequiredWhen != nil && (!namePattern.MatchString(rule.RequiredWhen.Field) || rule.RequiredWhen.Equals == "") {
		return fmt.Errorf("field %s has invalid required_when", rule.Name)
	}
	return nil
}

func (p *Policy) typeRule(name string) *TypeRule {
	for i := range p.Types {
		if p.Types[i].Name == name {
			return &p.Types[i]
		}
	}
	return nil
}

func (p *Policy) relationRule(name string) *RelationRule {
	for i := range p.Knowledge.Relations {
		if p.Knowledge.Relations[i].Field == name {
			return &p.Knowledge.Relations[i]
		}
	}
	return nil
}

func (p *Policy) DefaultCreateRelation() (string, bool) {
	if p == nil {
		return "", false
	}
	for _, rule := range p.Knowledge.Relations {
		if rule.DefaultForCreate {
			return rule.Field, true
		}
	}
	return "", false
}

func (p *Policy) categoryNames() []string {
	out := make([]string, 0, len(p.Categories))
	for _, item := range p.Categories {
		out = append(out, item.Name)
	}
	return out
}

func validatePackReference(cfg *config.Instance, relative, kind string) error {
	if !safePackRelative(relative) {
		return fmt.Errorf("content pack %s path is invalid: %q", kind, relative)
	}
	path := filepath.Join(cfg.Root, filepath.FromSlash(relative))
	if err := fsutil.EnsureNoSymlinkPath(cfg.Root, path); err != nil {
		return fmt.Errorf("content pack %s %s: %w", kind, relative, err)
	}
	for _, managed := range []string{cfg.InboxDir(), cfg.KnowledgeDir(), cfg.RuntimeDir()} {
		rel, err := filepath.Rel(managed, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("content pack %s cannot point inside managed fact/runtime path: %s", kind, relative)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("content pack %s %s: %w", kind, relative, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("content pack %s must be a regular file: %s", kind, relative)
	}
	if info.Size() > document.MaxMarkdownBytes {
		return fmt.Errorf("content pack %s exceeds %d byte limit: %s", kind, document.MaxMarkdownBytes, relative)
	}
	if err := fsutil.EnsureSingleLink(path); err != nil {
		return fmt.Errorf("content pack %s %s: %w", kind, relative, err)
	}
	return nil
}

func safePackRelative(path string) bool {
	if path == "" || strings.Contains(path, `\`) || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("content pack contains multiple JSON values")
		}
		return err
	}
	return nil
}

func extraDate(meta document.Metadata, key string, location *time.Location) (time.Time, bool, bool, error) {
	value, exists := meta.Extra[key]
	if !exists || value == nil {
		return time.Time{}, false, false, nil
	}
	if location == nil {
		location = time.Local
	}
	switch typed := value.(type) {
	case time.Time:
		if typed.Hour() != 0 || typed.Minute() != 0 || typed.Second() != 0 || typed.Nanosecond() != 0 {
			return time.Time{}, true, false, fmt.Errorf("knowledge property %s must be YYYY-MM-DD", key)
		}
		return time.Date(typed.Year(), typed.Month(), typed.Day(), 0, 0, 0, 0, location), true, true, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return time.Time{}, false, false, nil
		}
		if parsed, err := time.ParseInLocation("2006-01-02", typed, location); err == nil {
			return parsed, true, true, nil
		}
	}
	return time.Time{}, true, false, fmt.Errorf("knowledge property %s must be YYYY-MM-DD", key)
}

func firstHeading(body []byte) string {
	for _, line := range strings.Split(string(document.NormalizeMarkdownBody(body)), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "# "))
		}
	}
	return ""
}

func lintableMarkdown(body string) string {
	var out strings.Builder
	inFence := false
	fenceMarker := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		marker := ""
		if strings.HasPrefix(trimmed, "```") {
			marker = "```"
		} else if strings.HasPrefix(trimmed, "~~~") {
			marker = "~~~"
		}
		if marker != "" {
			if !inFence {
				inFence, fenceMarker = true, marker
			} else if marker == fenceMarker {
				inFence, fenceMarker = false, ""
			}
			out.WriteByte('\n')
			continue
		}
		if inFence {
			out.WriteByte('\n')
			continue
		}
		out.WriteString(stripInlineCode(line))
		out.WriteByte('\n')
	}
	return out.String()
}

func stripInlineCode(line string) string {
	var out strings.Builder
	inCode := false
	for _, r := range line {
		if r == '`' {
			inCode = !inCode
			continue
		}
		if !inCode {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func bytesContains(data []byte, value string) bool { return strings.Contains(string(data), value) }

func formatDate(value time.Time, dateOnly bool) string {
	if dateOnly {
		return value.Format("2006-01-02")
	}
	return value.Format(time.RFC3339)
}

func endOfDate(value time.Time, dateOnly bool) time.Time {
	if !dateOnly {
		return value
	}
	return time.Date(value.Year(), value.Month(), value.Day()+1, 0, 0, 0, 0, value.Location()).Add(-time.Nanosecond)
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func duplicateString(items []string) string {
	seen := map[string]bool{}
	for _, item := range items {
		if item == "" || item != strings.TrimSpace(item) || seen[item] {
			return item
		}
		seen[item] = true
	}
	return ""
}

func sortedMapKeys[V any](items map[string]V) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func SortedWarnings(items []string) []string {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}
