package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// Rule is the extension seam for profile and deployment-specific semantics.
// Higher precedence wins; rules at the same winning precedence are ambiguous
// unless exactly one matches.
type Rule interface {
	ID() string
	Precedence() int
	Match(SchemaDescriptor, SourceDescriptor) bool
	Render(SchemaDescriptor, SourceDescriptor) []Concept
}

type Registry struct {
	rules map[string]Rule
}

func NewRegistry(rules ...Rule) *Registry {
	r := &Registry{rules: make(map[string]Rule)}
	for _, rule := range rules {
		_ = r.Register(rule)
	}
	return r
}

func DefaultRegistry() *Registry {
	return NewRegistry(
		primitiveRule{},
		complexRule{},
		referenceRule{},
		extensionRule{},
		identifierRule{},
		codingRule{},
		codeableConceptRule{},
		quantityRule{},
		choiceRule{},
		observationRule{},
	)
}

func (r *Registry) Register(rule Rule) error {
	if rule == nil || strings.TrimSpace(rule.ID()) == "" {
		return fmt.Errorf("semantic rule and rule ID are required")
	}
	if r.rules == nil {
		r.rules = make(map[string]Rule)
	}
	if _, exists := r.rules[rule.ID()]; exists {
		return fmt.Errorf("semantic rule %q is already registered", rule.ID())
	}
	r.rules[rule.ID()] = rule
	return nil
}

func (r *Registry) RegisterOrReplace(rule Rule) error {
	if rule == nil || strings.TrimSpace(rule.ID()) == "" {
		return fmt.Errorf("semantic rule and rule ID are required")
	}
	if r.rules == nil {
		r.rules = make(map[string]Rule)
	}
	r.rules[rule.ID()] = rule
	return nil
}

func (r *Registry) Rule(id string) (Rule, bool) {
	rule, ok := r.rules[strings.TrimSpace(id)]
	return rule, ok
}

// Discover evaluates every generated source. Profile bindings are considered
// before normal matching, and an unknown profile rule produces a generic
// fallback plus an actionable diagnostic instead of silently disappearing.
func (r *Registry) Discover(schema SchemaDescriptor) Result {
	schema.ResourceType = strings.TrimSpace(schema.ResourceType)
	result := Result{ResourceType: schema.ResourceType, Families: []Family{}, Concepts: []Concept{}, Diagnostics: []Diagnostic{}}
	sources := cloneSources(schema.Fields)
	sort.Slice(sources, func(i, j int) bool { return sourceLess(sources[i], sources[j]) })
	for _, source := range sources {
		binding, hasProfile := profileFor(schema.Profiles, source.Path)
		var selected []Rule
		if hasProfile {
			rule, ok := r.Rule(binding.RuleID)
			if !ok {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{Severity: SeverityWarning, Code: "unknown_rule", RuleID: binding.RuleID, Path: source.Path, Message: fmt.Sprintf("profile rule %q is not registered; generic metadata was returned", binding.RuleID)})
				result.Concepts = append(result.Concepts, genericConcept(source, binding.Label, binding.Description, RuleProfile, 400))
				continue
			}
			selected = []Rule{rule}
		} else {
			selected = r.matching(schema, source)
		}
		if len(selected) == 0 {
			result.Concepts = append(result.Concepts, genericConcept(source, "", "", RulePrimitiveRepeated, 100))
			continue
		}
		if len(selected) > 1 {
			ids := make([]string, len(selected))
			for i, rule := range selected {
				ids[i] = rule.ID()
			}
			sort.Strings(ids)
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Severity: SeverityWarning, Code: "ambiguous_rules", Path: source.Path, Message: fmt.Sprintf("rules %s have the same precedence; generic metadata was returned", strings.Join(ids, ", "))})
			result.Concepts = append(result.Concepts, genericConcept(source, "", "", "ambiguous", selected[0].Precedence()))
			continue
		}
		concepts := selected[0].Render(schema, source)
		for i := range concepts {
			concepts[i].Source = source.canonical()
			concepts[i].RuleID = selected[0].ID()
			concepts[i].Precedence = selected[0].Precedence()
			concepts[i].Trace = traceFor(concepts[i], source, selected[0].ID(), selected[0].Precedence(), false)
			concepts[i].Examples = source.safeExamples(5)
			concepts[i].ID = conceptID(concepts[i])
		}
		result.Concepts = append(result.Concepts, concepts...)
	}
	result.Concepts = uniqueConcepts(result.Concepts)
	result.Families = familiesFromConcepts(result.Concepts)
	sort.Slice(result.Diagnostics, func(i, j int) bool { return diagnosticLess(result.Diagnostics[i], result.Diagnostics[j]) })
	return result
}

func (r *Registry) matching(schema SchemaDescriptor, source SourceDescriptor) []Rule {
	best := -1
	matched := []Rule{}
	for _, rule := range r.rules {
		if !rule.Match(schema, source) {
			continue
		}
		precedence := rule.Precedence()
		if precedence > best {
			best = precedence
			matched = []Rule{rule}
		} else if precedence == best {
			matched = append(matched, rule)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID() < matched[j].ID() })
	return matched
}

func DescribeResource(resourceType string) (SchemaDescriptor, error) {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return SchemaDescriptor{}, fmt.Errorf("resource type is required")
	}
	if !fhirHasResource(resourceType) {
		return SchemaDescriptor{}, fmt.Errorf("unknown generated resource type %q", resourceType)
	}
	paths := map[string]struct{}{}
	for _, field := range fhirFields(resourceType) {
		for _, candidate := range pathPrefixes(field.Path) {
			paths[candidate] = struct{}{}
		}
	}
	sources := make([]SourceDescriptor, 0, len(paths))
	for path := range paths {
		sources = append(sources, describePath(resourceType, path))
	}
	sort.Slice(sources, func(i, j int) bool { return sourceLess(sources[i], sources[j]) })
	return SchemaDescriptor{ResourceType: resourceType, Fields: sources}, nil
}

func profileFor(profiles []ProfileBinding, path string) (ProfileBinding, bool) {
	for _, profile := range profiles {
		if strings.TrimSpace(profile.Path) == path {
			return profile, true
		}
	}
	return ProfileBinding{}, false
}

func cloneSources(in []SourceDescriptor) []SourceDescriptor {
	out := make([]SourceDescriptor, len(in))
	for i := range in {
		out[i] = in[i].canonical()
	}
	return out
}

func sourceLess(a, b SourceDescriptor) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.ValuePath < b.ValuePath
}

func diagnosticLess(a, b Diagnostic) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	return a.RuleID < b.RuleID
}

func uniqueConcepts(in []Concept) []Concept {
	seen := map[string]struct{}{}
	out := make([]Concept, 0, len(in))
	for _, concept := range in {
		if concept.ID == "" {
			concept.ID = conceptID(concept)
		}
		if _, ok := seen[concept.ID]; ok {
			continue
		}
		seen[concept.ID] = struct{}{}
		out = append(out, concept)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func familiesFromConcepts(concepts []Concept) []Family {
	byID := map[string]*Family{}
	for _, concept := range concepts {
		if concept.Output.Selection.SourcePath == "" && concept.Source.Path == "" {
			continue
		}
		family := &Family{Label: concept.Group, RuleID: concept.RuleID, Precedence: concept.Precedence, Source: concept.Source, Concepts: []Concept{}}
		family.ID = familyID(*family)
		if existing := byID[family.ID]; existing != nil {
			existing.Concepts = append(existing.Concepts, concept)
			continue
		}
		byID[family.ID] = family
	}
	out := make([]Family, 0, len(byID))
	for _, family := range byID {
		sort.Slice(family.Concepts, func(i, j int) bool { return family.Concepts[i].ID < family.Concepts[j].ID })
		family.Trace = TraceDescriptor{ResourceType: family.Source.ResourceType, RawPath: family.Source.Path, SourcePath: family.Source.SourcePath, ValuePath: family.Source.ValuePath, RuleID: family.RuleID, Precedence: family.Precedence}
		out = append(out, *family)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
