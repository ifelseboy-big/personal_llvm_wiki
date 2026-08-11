package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"llm-wiki/internal/config"
	"llm-wiki/internal/document"
)

var (
	knowledgeTypes = map[string]bool{
		"claim": true, "concept": true, "guide": true, "tutorial": true,
		"reference": true, "decision": true, "project": true,
	}
	lifecycleValues = map[string]bool{
		"current": true, "disputed": true, "superseded": true, "retracted": true,
	}
	templateVariablePattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	footnotePattern         = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
	footnoteDefinition      = regexp.MustCompile(`^\[\^([^\]\s]+)\]:[ \t]*(.*)$`)
)

const (
	PersonalGovernanceVersion = "personal-2.0"
	PersonalTemplateVersion   = "2.0.0"
)

type LifecycleAssessment struct {
	Lifecycle   string   `json:"lifecycle"`
	Inactive    bool     `json:"inactive"`
	Disputed    bool     `json:"disputed"`
	NotYetValid bool     `json:"not_yet_valid"`
	Expired     bool     `json:"expired"`
	ReviewDue   bool     `json:"review_due"`
	Warnings    []string `json:"warnings"`
}

type Relation struct {
	Property string
	Link     string
	Target   *document.Document
}

func ValidateForPromotion(cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document, now time.Time) error {
	return validateKnowledge(cfg, doc, prospective, now)
}

func validateKnowledge(cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document, now time.Time) error {
	if cfg == nil || doc == nil {
		return errors.New("knowledge governance validation requires a wiki and document")
	}
	if !UsesPersonalGovernance(cfg) {
		return nil
	}
	if cfg.Template.Version != PersonalTemplateVersion {
		return fmt.Errorf("personal template version must be %s", PersonalTemplateVersion)
	}
	if now.IsZero() {
		now = time.Now()
	}
	var problems []error
	if doc.Metadata.GovernanceVersion != PersonalGovernanceVersion {
		problems = append(problems, fmt.Errorf("knowledge governance_version must be %q", PersonalGovernanceVersion))
	}
	if !knowledgeTypes[doc.Metadata.Type] {
		problems = append(problems, fmt.Errorf("knowledge type %q is not supported by personal 2.0.0", doc.Metadata.Type))
	}
	description, ok, err := ExtraString(doc.Metadata, "description")
	if err != nil {
		problems = append(problems, err)
	} else if !ok || strings.TrimSpace(description) == "" {
		problems = append(problems, errors.New("knowledge description is required by personal 2.0.0"))
	}
	lifecycle, lifecycleExists, lifecycleErr := ExtraString(doc.Metadata, "lifecycle")
	if lifecycleErr != nil {
		problems = append(problems, lifecycleErr)
	} else if !lifecycleExists || strings.TrimSpace(lifecycle) == "" {
		problems = append(problems, errors.New("knowledge lifecycle is required by personal 2.0.0"))
	} else if _, err := AssessLifecycle(doc.Metadata, now); err != nil {
		problems = append(problems, err)
	}
	body := lintableMarkdown(string(document.NormalizeMarkdownBody(doc.Body)))
	metadataJSON, _ := json.Marshal(doc.Metadata)
	if templateVariablePattern.MatchString(body) || templateVariablePattern.Match(metadataJSON) {
		problems = append(problems, errors.New("knowledge draft contains unresolved template variables"))
	}
	if strings.Contains(body, "llm-wiki:prompt") || bytesContains(metadataJSON, "llm-wiki:prompt") {
		problems = append(problems, errors.New("knowledge draft contains unresolved llm-wiki prompt comments"))
	}
	if heading := firstHeading(doc.Body); heading == "" {
		problems = append(problems, errors.New("knowledge body requires a level-one heading"))
	} else if heading != strings.TrimSpace(doc.Metadata.Title) {
		problems = append(problems, fmt.Errorf("knowledge title %q does not match first H1 %q", doc.Metadata.Title, heading))
	}
	if err := ValidateCitations(doc, false); err != nil {
		problems = append(problems, err)
	}
	if _, err := validateRelations(cfg, doc, prospective); err != nil {
		problems = append(problems, err)
	}
	if err := validateTypeSpecific(doc.Metadata); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func ValidateStored(cfg *config.Instance, doc *document.Document, now time.Time) error {
	return validateKnowledge(cfg, doc, nil, now)
}

func UsesPersonalGovernance(cfg *config.Instance) bool {
	return cfg != nil && cfg.Template.Name == "personal"
}

func AssessLifecycle(meta document.Metadata, now time.Time) (LifecycleAssessment, error) {
	if now.IsZero() {
		now = time.Now()
	}
	value, ok, err := ExtraString(meta, "lifecycle")
	if err != nil {
		return LifecycleAssessment{}, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		return LifecycleAssessment{}, errors.New("knowledge lifecycle is required")
	}
	value = strings.TrimSpace(value)
	if !lifecycleValues[value] {
		return LifecycleAssessment{}, fmt.Errorf("invalid knowledge lifecycle %q", value)
	}
	if original, exists, _ := ExtraString(meta, "lifecycle"); exists && original != value {
		return LifecycleAssessment{}, errors.New("knowledge lifecycle must not contain surrounding whitespace")
	}
	assessment := LifecycleAssessment{
		Lifecycle: value,
		Inactive:  value == "superseded" || value == "retracted",
		Disputed:  value == "disputed",
	}
	if assessment.Disputed {
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s is disputed", meta.ID))
	}
	if assessment.Inactive {
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s is %s and is included only for audit", meta.ID, value))
	}
	if date, exists, dateOnly, dateErr := extraDate(meta, "valid_from", now.Location()); dateErr != nil {
		return LifecycleAssessment{}, dateErr
	} else if exists && now.Before(date) {
		assessment.NotYetValid = true
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s is not valid until %s", meta.ID, formatDate(date, dateOnly)))
	}
	if date, exists, dateOnly, dateErr := extraDate(meta, "valid_until", now.Location()); dateErr != nil {
		return LifecycleAssessment{}, dateErr
	} else if exists && now.After(endOfDate(date, dateOnly)) {
		assessment.Expired = true
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s expired on %s", meta.ID, formatDate(date, dateOnly)))
	}
	if date, exists, dateOnly, dateErr := extraDate(meta, "review_after", now.Location()); dateErr != nil {
		return LifecycleAssessment{}, dateErr
	} else if exists && !now.Before(date) {
		assessment.ReviewDue = true
		assessment.Warnings = append(assessment.Warnings, fmt.Sprintf("knowledge %s requires review since %s", meta.ID, formatDate(date, dateOnly)))
	}
	return assessment, nil
}

func AssessStoredLifecycle(cfg *config.Instance, meta document.Metadata, now time.Time) (LifecycleAssessment, error) {
	if !UsesPersonalGovernance(cfg) {
		return LifecycleAssessment{Lifecycle: "current"}, nil
	}
	if cfg.Template.Version != PersonalTemplateVersion {
		return LifecycleAssessment{}, fmt.Errorf("personal template version must be %s", PersonalTemplateVersion)
	}
	return AssessLifecycle(meta, now)
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
	for label := range references {
		if _, exists := definitions[label]; !exists {
			problems = append(problems, fmt.Errorf("footnote %q has no definition", label))
		}
	}
	for label := range definitions {
		if !references[label] {
			problems = append(problems, fmt.Errorf("footnote definition %q is unused", label))
		}
	}
	return errors.Join(problems...)
}

func ValidateRelations(cfg *config.Instance, doc *document.Document) ([]Relation, error) {
	return validateRelations(cfg, doc, nil)
}

func validateRelations(cfg *config.Instance, doc *document.Document, prospective map[string]*document.Document) ([]Relation, error) {
	if cfg == nil || doc == nil {
		return nil, errors.New("relation validation requires a wiki and document")
	}
	var relations []Relation
	var problems []error
	seen := map[string]bool{}
	for _, property := range []string{"related", "supersedes", "superseded_by"} {
		links, exists, err := ExtraStringList(doc.Metadata, property)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !exists {
			continue
		}
		for _, id := range links {
			key := property + "\x00" + id
			if seen[key] {
				problems = append(problems, fmt.Errorf("duplicate %s knowledge id %q", property, id))
				continue
			}
			seen[key] = true
			if !document.ValidID("know", id) {
				problems = append(problems, fmt.Errorf("%s relation %q is not a knowledge id", property, id))
				continue
			}
			target := prospective[id]
			var err error
			if target == nil {
				target, err = document.FindByID(cfg.KnowledgeDir(), id)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", property, err))
				continue
			}
			if err := target.Validate("knowledge", true); err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", property, err))
				continue
			}
			if target.Metadata.ID == doc.Metadata.ID {
				problems = append(problems, fmt.Errorf("%s cannot link knowledge %s to itself", property, doc.Metadata.ID))
				continue
			}
			relations = append(relations, Relation{Property: property, Link: id, Target: target})
		}
	}
	return relations, errors.Join(problems...)
}

func ValidateReciprocalRelations(cfg *config.Instance, doc *document.Document) error {
	relations, err := ValidateRelations(cfg, doc)
	if err != nil {
		return err
	}
	var problems []error
	for _, relation := range relations {
		reciprocal := ""
		switch relation.Property {
		case "supersedes":
			reciprocal = "superseded_by"
		case "superseded_by":
			reciprocal = "supersedes"
		default:
			continue
		}
		links, _, listErr := ExtraStringList(relation.Target.Metadata, reciprocal)
		if listErr != nil {
			problems = append(problems, listErr)
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
			problems = append(problems, fmt.Errorf("knowledge %s %s %s but reciprocal %s link is missing", doc.Metadata.ID, relation.Property, relation.Target.Metadata.ID, reciprocal))
		}
	}
	return errors.Join(problems...)
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

func validateTypeSpecific(meta document.Metadata) error {
	var problems []error
	checkDate := func(key string, required bool) {
		_, exists, err := ExtraDate(meta, key)
		if err != nil {
			problems = append(problems, err)
		} else if required && !exists {
			problems = append(problems, fmt.Errorf("knowledge property %s is required for type %s", key, meta.Type))
		}
	}
	checkList := func(key string) {
		if _, _, err := ExtraStringList(meta, key); err != nil {
			problems = append(problems, err)
		}
	}
	switch meta.Type {
	case "guide", "tutorial", "reference":
		checkList("applies_to")
		checkDate("last_verified", false)
	case "decision":
		state, exists, err := ExtraString(meta, "decision_state")
		if err != nil {
			problems = append(problems, err)
		} else if !exists || !map[string]bool{"proposed": true, "accepted": true, "deprecated": true}[state] {
			problems = append(problems, errors.New("decision_state must be proposed, accepted, or deprecated"))
		}
		checkDate("decided_at", state == "accepted")
		for _, key := range []string{"decision_makers", "consulted", "informed"} {
			checkList(key)
		}
	case "project":
		state, exists, err := ExtraString(meta, "project_state")
		if err != nil {
			problems = append(problems, err)
		} else if !exists || !map[string]bool{"active": true, "paused": true, "completed": true, "cancelled": true}[state] {
			problems = append(problems, errors.New("project_state must be active, paused, completed, or cancelled"))
		}
		checkDate("as_of", true)
		checkDate("started_at", false)
		checkDate("target_date", false)
	}
	return errors.Join(problems...)
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

func hasLocator(definition string) bool {
	for _, field := range strings.Split(definition, ";") {
		parts := strings.SplitN(strings.TrimSpace(field), ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "locator") && strings.TrimSpace(parts[1]) != "" {
			return true
		}
	}
	return false
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

func bytesContains(data []byte, value string) bool {
	return strings.Contains(string(data), value)
}

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

func SortedWarnings(items []string) []string {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}
