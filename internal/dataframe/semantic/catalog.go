package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/catalog"
)

// CatalogOptions bounds the amount of researcher-facing metadata returned by
// catalog discovery. A zero limit means no additional limit.
type CatalogOptions struct {
	Project                 string
	SourceGeneration        string
	ResourceType            string
	ResourceLimit           int
	ConceptLimitPerResource int
}

type CatalogCompleteness struct {
	State                   string
	ResourceLimit           int
	ConceptLimitPerResource int
	ReturnedResourceCount   int
	ReturnedConceptCount    int
}

type CatalogResource struct {
	ResourceType  string
	DocumentCount int64
	Families      []Family
}

type CatalogDiagnostic struct {
	Severity string
	Code     string
	RuleID   string
	Path     string
	Message  string
}

// CatalogResult is the transport-neutral semantic concept catalog. It is
// deliberately composed of the same Concept/Family/Trace types used by recipe
// validation so a concept selected in the UI can be resolved without a second
// interpretation of the persisted catalog row.
type CatalogResult struct {
	SchemaVersion     int
	Project           string
	SourceGeneration  string
	AuthResourcePaths []string
	Completeness      CatalogCompleteness
	Resources         []CatalogResource
	Diagnostics       []CatalogDiagnostic
}

// DiscoverCatalog adapts authenticated, persisted catalog rows into semantic
// concepts. Rows are expected to have already been filtered by project,
// generation, and authorization scope; this function never broadens that
// scope. Empty rows are a valid empty catalog, not an error.
func DiscoverCatalog(fields []catalog.PopulatedField, opts CatalogOptions) CatalogResult {
	result := CatalogResult{
		SchemaVersion:    2,
		Project:          strings.TrimSpace(opts.Project),
		SourceGeneration: strings.TrimSpace(opts.SourceGeneration),
		Resources:        []CatalogResource{},
		Diagnostics:      []CatalogDiagnostic{},
		Completeness: CatalogCompleteness{
			State:                   "empty",
			ResourceLimit:           opts.ResourceLimit,
			ConceptLimitPerResource: opts.ConceptLimitPerResource,
		},
	}

	byResource := map[string]*CatalogResource{}
	observations := map[string][]catalog.SemanticObservation{}
	for _, field := range fields {
		resourceType := strings.TrimSpace(field.ResourceType)
		if resourceType == "" || (opts.ResourceType != "" && resourceType != opts.ResourceType) {
			continue
		}
		resource := byResource[resourceType]
		if resource == nil {
			resource = &CatalogResource{ResourceType: resourceType, Families: []Family{}}
			byResource[resourceType] = resource
		}
		if field.DocCount > resource.DocumentCount {
			resource.DocumentCount = field.DocCount
		}
		for _, observation := range field.SemanticObservations {
			if observation.Population <= 0 || strings.TrimSpace(observation.Value.Selector) == "" {
				continue
			}
			key := resourceType + "\x00" + observationKey(observation)
			observations[key] = append(observations[key], observation)
		}
	}

	// Merge rows from independent authorized catalog partitions. The identity
	// includes every extensible selector/rule field, so unrelated mappings can
	// never be combined merely because they share a display label.
	merged := map[string]catalog.SemanticObservation{}
	for key, values := range observations {
		var current catalog.SemanticObservation
		for i, value := range values {
			if i == 0 {
				current = value
				current.Examples = append([]string(nil), value.Examples...)
				continue
			}
			current.Population += value.Population
			current.ExamplesTruncated = current.ExamplesTruncated || value.ExamplesTruncated
			current.Examples = mergeExamples(current.Examples, value.Examples)
		}
		merged[key] = current
	}

	resourceKeys := make([]string, 0, len(byResource))
	for resourceType := range byResource {
		resourceKeys = append(resourceKeys, resourceType)
	}
	sort.Strings(resourceKeys)
	if opts.ResourceLimit > 0 && len(resourceKeys) > opts.ResourceLimit {
		result.Diagnostics = append(result.Diagnostics, CatalogDiagnostic{Severity: SeverityWarning, Code: "DISCOVERY_PARTIAL", Message: "Some populated resources were omitted after the resource limit."})
		resourceKeys = resourceKeys[:opts.ResourceLimit]
	}
	for _, resourceType := range resourceKeys {
		resource := byResource[resourceType]
		concepts := make([]Concept, 0)
		for key, observation := range merged {
			if !strings.HasPrefix(key, resourceType+"\x00") {
				continue
			}
			concepts = append(concepts, conceptFromObservation(resourceType, observation, &result.Diagnostics))
		}
		sort.Slice(concepts, func(i, j int) bool { return concepts[i].ID < concepts[j].ID })
		if opts.ConceptLimitPerResource > 0 && len(concepts) > opts.ConceptLimitPerResource {
			omitted := len(concepts) - opts.ConceptLimitPerResource
			result.Diagnostics = append(result.Diagnostics, CatalogDiagnostic{Severity: SeverityWarning, Code: "CONCEPT_LIMIT_REACHED", Message: fmt.Sprintf("Some concepts were omitted after the per-resource limit (%d omitted).", omitted)})
			concepts = concepts[:opts.ConceptLimitPerResource]
		}
		resource.Families = familiesFromConcepts(concepts)
		result.Completeness.ReturnedConceptCount += len(concepts)
		result.Resources = append(result.Resources, *resource)
	}
	result.Completeness.ReturnedResourceCount = len(result.Resources)
	if len(result.Resources) > 0 {
		result.Completeness.State = "complete"
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "DISCOVERY_PARTIAL" || diagnostic.Code == "CONCEPT_LIMIT_REACHED" {
			result.Completeness.State = "partial"
		}
	}
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		if result.Diagnostics[i].Code != result.Diagnostics[j].Code {
			return result.Diagnostics[i].Code < result.Diagnostics[j].Code
		}
		return result.Diagnostics[i].Path < result.Diagnostics[j].Path
	})
	return result
}

func observationKey(observation catalog.SemanticObservation) string {
	return strings.Join([]string{
		observation.Source.Canonical, observation.Source.Type, observation.Source.Profile, observation.Source.Path,
		observation.Key.Selector, observation.Key.System, observation.Key.Code, observation.Key.Display,
		observation.Value.Selector, observation.Value.Type, observation.Cardinality,
		observation.RuleHint, observation.RuleVersion,
	}, "\x00")
}

func mergeExamples(left, right []string) []string {
	set := map[string]struct{}{}
	for _, value := range append(append([]string(nil), left...), right...) {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func conceptFromObservation(resourceType string, observation catalog.SemanticObservation, diagnostics *[]CatalogDiagnostic) Concept {
	source := SourceDescriptor{
		Canonical:         observation.Source.Canonical,
		ResourceType:      resourceType,
		Path:              observation.Source.Path,
		Profile:           observation.Source.Profile,
		SourcePath:        observation.Source.Canonical,
		ValuePath:         observation.Value.Selector,
		KeySelector:       observation.Key.Selector,
		KeySystem:         observation.Key.System,
		KeyCode:           observation.Key.Code,
		KeyDisplay:        observation.Key.Display,
		RuleVersion:       observation.RuleVersion,
		Shape:             "primitive",
		Primitive:         observation.Value.Type,
		Repeated:          observation.Cardinality == "repeated" || observation.Cardinality == "pivoted",
		PopulationCount:   observation.Population,
		DistinctTruncated: observation.ExamplesTruncated,
	}
	cardinality := observation.Cardinality
	if cardinality == "single" || cardinality == "" {
		cardinality = CardinalityOptionalOne
	}
	if cardinality == CardinalityRepeated || cardinality == CardinalityPivoted {
		source.Shape = "array"
		source.Repeated = true
	}
	for _, value := range observation.Examples {
		source.Examples = append(source.Examples, SafeExample(value))
	}
	if source.Path == "" {
		source.Path = observation.Source.Canonical
	}

	ruleID := strings.TrimSpace(observation.RuleHint)
	if ruleID == "" {
		ruleID = RuleResourceSemantic
	}
	mode, group, description := OutputMetadata, "Other", "Observed semantic data"
	switch ruleID {
	case "OBSERVATION_CODE_VALUE", "OBSERVATION_COMPONENT_VALUE":
		mode, group, description = OutputMeasurement, "Measurements", "Observation code paired with its value"
	case "CODEABLE_CONCEPT_VALUE":
		mode, group, description = OutputCodedValue, "Coded values", "CodeableConcept text or coding display/code"
	case "IDENTIFIER_SYSTEM_VALUE":
		mode, group, description = OutputDynamicFamily, "Identifiers", "Identifier value keyed by its issuing system"
	case "EXTENSION_URL_VALUE":
		mode, group, description = OutputDynamicFamily, "Extensions", "Extension URL keyed by its value[x] member"
	default:
		*diagnostics = append(*diagnostics, CatalogDiagnostic{Severity: SeverityWarning, Code: "unknown_rule", RuleID: ruleID, Path: source.Path, Message: fmt.Sprintf("catalog rule %q is not registered; generic metadata was returned", ruleID)})
	}
	selection := Selection{Mode: mode, SourcePath: observation.Source.Canonical, ValueSelector: observation.Value.Selector, KeySelector: observation.Key.Selector}
	if mode == OutputDynamicFamily {
		selection.ItemSource = observation.Source.Path
	}
	examples := source.safeExamples(32)
	examples.Limited = examples.Limited || source.DistinctTruncated
	concept := Concept{
		Label: labelFor(source, "Value"), Group: group, Description: description, RuleID: ruleID, RuleVersion: observation.RuleVersion, Precedence: 300,
		Source: source, Output: OutputDescriptor{Mode: mode, ValueType: observation.Value.Type, Cardinality: cardinality, Selection: selection, Generic: mode == OutputMetadata},
		Examples: examples, Trace: TraceDescriptor{ResourceType: resourceType, RawPath: observation.Source.Canonical, RawKey: observation.Key.Selector, RawValue: observation.Value.Selector, RawCardinality: observation.Cardinality, SourcePath: observation.Key.Selector, ValuePath: observation.Value.Selector, RuleID: ruleID, RuleVersion: observation.RuleVersion, Precedence: 300},
	}
	concept.ID = stableID("concept", struct{ Resource, Rule, Version, Source, Key, Value string }{resourceType, ruleID, observation.RuleVersion, observation.Source.Canonical, observation.Key.Selector, observation.Value.Selector})
	return concept
}
