package catalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

type SemanticCoverageOccurrenceKind string

const SemanticCoverageRuleVersion = 1

const (
	SemanticCoverageCodeableConcept  SemanticCoverageOccurrenceKind = "CODEABLE_CONCEPT"
	SemanticCoverageCoding           SemanticCoverageOccurrenceKind = "CODING"
	SemanticCoverageIdentifier       SemanticCoverageOccurrenceKind = "IDENTIFIER"
	SemanticCoverageExtensionValue   SemanticCoverageOccurrenceKind = "EXTENSION_VALUE"
	SemanticCoveragePrimitiveCode    SemanticCoverageOccurrenceKind = "PRIMITIVE_CODE"
	SemanticCoverageUnknownStructure SemanticCoverageOccurrenceKind = "UNKNOWN_STRUCTURE"
)

type SemanticCoveragePresence string

const (
	SemanticCoverageMissing   SemanticCoveragePresence = "MISSING"
	SemanticCoverageNull      SemanticCoveragePresence = "NULL"
	SemanticCoverageEmpty     SemanticCoveragePresence = "EMPTY"
	SemanticCoverageBlank     SemanticCoveragePresence = "BLANK"
	SemanticCoveragePopulated SemanticCoveragePresence = "POPULATED"
)

type SemanticCoverageCensusStatus string

const (
	SemanticCoverageCensusPartial  SemanticCoverageCensusStatus = "PARTIAL"
	SemanticCoverageCensusComplete SemanticCoverageCensusStatus = "COMPLETE"
)

const SemanticCoverageMaxReconciliationItemsPerSource = 10000

type SemanticCoverageCensusGap string

const (
	SemanticCoverageGapPrimitiveCode      SemanticCoverageCensusGap = "PRIMITIVE_CODE_CLASSIFICATION"
	SemanticCoverageGapIdentifiers        SemanticCoverageCensusGap = "IDENTIFIER_OCCURRENCES"
	SemanticCoverageGapExtensionValues    SemanticCoverageCensusGap = "EXTENSION_VALUE_OCCURRENCES"
	SemanticCoverageGapUnknownSubtrees    SemanticCoverageCensusGap = "UNKNOWN_SUBTREE_RECONCILIATION"
	SemanticCoverageGapRawAvailability    SemanticCoverageCensusGap = "EXECUTABLE_RAW_AVAILABILITY"
	SemanticCoverageGapEmissionEvidence   SemanticCoverageCensusGap = "EMISSION_RECONCILIATION"
	SemanticCoverageGapOccurrenceClassify SemanticCoverageCensusGap = "OCCURRENCE_DISPOSITION_CLASSIFICATION"
	SemanticCoverageGapSchemaIdentity     SemanticCoverageCensusGap = "SOURCE_SCHEMA_IDENTITY"
)

type SemanticCoverageDispositionKind string

const (
	SemanticCoverageSemanticDisposition    SemanticCoverageDispositionKind = "SEMANTIC"
	SemanticCoverageRawRetainedDisposition SemanticCoverageDispositionKind = "RAW_RETAINED"
	SemanticCoverageUnsupportedDisposition SemanticCoverageDispositionKind = "UNSUPPORTED"
)

type SemanticCoverageReason string

const (
	SemanticCoverageReasonMissingSystem              SemanticCoverageReason = "MISSING_SYSTEM"
	SemanticCoverageReasonMissingCode                SemanticCoverageReason = "MISSING_CODE"
	SemanticCoverageReasonMissingIdentifierValue     SemanticCoverageReason = "MISSING_IDENTIFIER_VALUE"
	SemanticCoverageReasonTextOnly                   SemanticCoverageReason = "TEXT_ONLY"
	SemanticCoverageReasonAmbiguousAnchor            SemanticCoverageReason = "AMBIGUOUS_ANCHOR"
	SemanticCoverageReasonMixedChoice                SemanticCoverageReason = "MIXED_CHOICE"
	SemanticCoverageReasonUnsupportedValueProjection SemanticCoverageReason = "UNSUPPORTED_VALUE_PROJECTION"
	SemanticCoverageReasonUnsupportedVersion         SemanticCoverageReason = "UNSUPPORTED_VERSION"
	SemanticCoverageReasonUnknownSchemaMember        SemanticCoverageReason = "UNKNOWN_SCHEMA_MEMBER"
	SemanticCoverageReasonUnknownCodeClassification  SemanticCoverageReason = "UNKNOWN_CODE_CLASSIFICATION"
	SemanticCoverageReasonUnsupportedSelector        SemanticCoverageReason = "UNSUPPORTED_SELECTOR"
	SemanticCoverageReasonIncompleteSource           SemanticCoverageReason = "INCOMPLETE_SOURCE"
	SemanticCoverageReasonWalkFailed                 SemanticCoverageReason = "WALK_FAILED"
	SemanticCoverageReasonTruncatedEvidence          SemanticCoverageReason = "TRUNCATED_EVIDENCE"
	SemanticCoverageReasonUnrepresentedMetadata      SemanticCoverageReason = "UNREPRESENTED_METADATA"
)

type SemanticCoverageRole string

const (
	SemanticCoverageRoleValue          SemanticCoverageRole = "VALUE"
	SemanticCoverageRoleConceptKey     SemanticCoverageRole = "CONCEPT_KEY"
	SemanticCoverageRoleCodingMetadata SemanticCoverageRole = "CODING_METADATA"
	SemanticCoverageRoleUnit           SemanticCoverageRole = "UNIT"
	SemanticCoverageRoleReference      SemanticCoverageRole = "REFERENCE"
)

type SemanticCoverageReferenceKind string

const (
	SemanticCoverageExactReference       SemanticCoverageReferenceKind = "EXACT"
	SemanticCoverageFamilyReference      SemanticCoverageReferenceKind = "FAMILY"
	SemanticCoverageNativeFieldReference SemanticCoverageReferenceKind = "NATIVE_FIELD"
)

type SemanticCoverageReference struct {
	Kind          SemanticCoverageReferenceKind
	ConceptID     string
	BindingID     string
	Family        SemanticInventoryFamilyReference
	CandidatePath string
	ResourceType  string
}

type SemanticCoverageFacetAggregate struct {
	Path                      string
	Kind                      string
	ResourceBoundarySignature string
	Disposition               SemanticCoverageDispositionKind
	Reason                    SemanticCoverageReason
	OccurrenceCount           int64
	PresenceCounts            map[SemanticCoveragePresence]int64
	References                []SemanticCoverageReference
	ReferencesTruncated       bool
}

type SemanticCoverageDiagnostic struct {
	Code          string
	CanonicalPath string
	OwnerPath     string
	Reason        SemanticCoverageReason
	Explanation   string
}

type SemanticCoverageSourceIdentity struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	SchemaIdentity    string
	SourceKind        string
	SourceID          string
	ResourceType      string
}

// SemanticCoverageSourceContribution is aggregated by source and canonical
// path. It contains a source sentinel even when Paths is empty.
type SemanticCoverageSourceContribution struct {
	Identity                    SemanticCoverageSourceIdentity
	BuildID                     string
	SourceRecordCount           int64
	Counts                      SemanticCoverageCounts
	Paths                       []SemanticCoveragePathAggregate
	IncompleteGaps              []SemanticCoverageCensusGap
	Diagnostics                 []SemanticCoverageDiagnostic
	CountsTruncated             bool
	FacetsTruncated             bool
	ReferencesTruncated         bool
	ReconciliationTruncated     bool
	DiagnosticLocatorsTruncated bool
	Complete                    bool
}

type SemanticCoverageOwnerLocator struct {
	SourceID      string `json:"-"`
	ResourceType  string
	CanonicalPath string
	OwnerPath     string
}

// SemanticCoverageEmissionEvidence is ephemeral reconciliation evidence. Key
// values are excluded from JSON and must not be persisted.
type SemanticCoverageEmissionEvidence struct {
	Owner                 SemanticCoverageOwnerLocator
	ConceptOwner          SemanticCoverageOwnerLocator
	KeyOwner              SemanticCoverageOwnerLocator
	ValueOwner            SemanticCoverageOwnerLocator
	KeySelector           string
	KeySystem             string `json:"-"`
	KeyVersion            string `json:"-"`
	KeyCode               string `json:"-"`
	ChoiceArm             string
	ValueSelector         string
	FallbackSelectors     []string
	FallbackOnBlankString bool
	SourceKind            string `json:"-"`
	SourcePath            string `json:"-"`
	RuleHint              string `json:"-"`
	ConceptID             string
	BindingID             string
	Status                string
	Roles                 []SemanticCoverageRole
}

type SemanticCoverageEmissionSink func(SemanticCoverageEmissionEvidence)

type SemanticCoverageSourceSink func(SemanticCoverageSourceContribution) error

type semanticCoverageObservationEmitter func(SemanticObservation, []any, SemanticCoverageEmissionEvidence)

func semanticCoverageEmissionEvidence(
	resourceType string,
	sourceKind string,
	sourceID string,
	scope *semanticScope,
	conceptOwnerPath string,
	keyOwnerPath string,
	valueOwnerPath string,
	observation SemanticObservation,
) SemanticCoverageEmissionEvidence {
	owner := semanticCoverageOwnerLocator(resourceType, sourceID, scope.ownerPath)
	if conceptOwnerPath == "" {
		conceptOwnerPath = scope.ownerPath
	}
	if keyOwnerPath == "" {
		keyOwnerPath = scope.ownerPath
	}
	if valueOwnerPath == "" {
		valueOwnerPath = scope.ownerPath
	}
	roles := []SemanticCoverageRole{SemanticCoverageRoleValue}
	if observation.Key.Selector != "" && (strings.TrimSpace(observation.Key.System) != "" || strings.TrimSpace(observation.Key.Code) != "" || strings.Contains(observation.Key.Selector, "coding[]")) {
		roles = append(roles, SemanticCoverageRoleConceptKey, SemanticCoverageRoleCodingMetadata)
	}
	if len(observation.ObservedUnits) > 0 {
		roles = append(roles, SemanticCoverageRoleUnit)
	}
	if strings.Contains(observation.ChoiceArm, "Reference") {
		roles = append(roles, SemanticCoverageRoleReference)
	}
	return SemanticCoverageEmissionEvidence{
		Owner:                 owner,
		ConceptOwner:          semanticCoverageOwnerLocator(resourceType, sourceID, conceptOwnerPath),
		KeyOwner:              semanticCoverageOwnerLocator(resourceType, sourceID, keyOwnerPath),
		ValueOwner:            semanticCoverageOwnerLocator(resourceType, sourceID, valueOwnerPath),
		KeySelector:           observation.Key.Selector,
		KeySystem:             observation.Key.System,
		KeyVersion:            observation.Key.Version,
		KeyCode:               observation.Key.Code,
		ChoiceArm:             observation.ChoiceArm,
		ValueSelector:         observation.Value.Selector,
		FallbackSelectors:     append([]string(nil), observation.Value.FallbackSelectors...),
		FallbackOnBlankString: observation.Value.FallbackOnBlankString,
		SourceKind:            semanticInventorySourceKind(sourceKind),
		SourcePath:            observation.Source.Path,
		RuleHint:              observation.RuleHint,
		ConceptID:             semanticConceptID(observation),
		BindingID:             semanticBindingID(observation),
		Status:                observation.Status,
		Roles:                 roles,
	}
}

func semanticCoverageOwnerLocator(resourceType, sourceID, ownerPath string) SemanticCoverageOwnerLocator {
	return SemanticCoverageOwnerLocator{
		SourceID:      sourceID,
		ResourceType:  resourceType,
		CanonicalPath: canonicalizeSemanticCoverageOwnerPath(ownerPath),
		OwnerPath:     ownerPath,
	}
}

func canonicalizeSemanticCoverageOwnerPath(path string) string {
	var builder strings.Builder
	for index := 0; index < len(path); index++ {
		if path[index] != '[' {
			builder.WriteByte(path[index])
			continue
		}
		end := strings.IndexByte(path[index:], ']')
		if end < 0 {
			builder.WriteString(path[index:])
			break
		}
		contents := path[index+1 : index+end]
		if contents == "" {
			builder.WriteString(path[index : index+end+1])
			index += end
			continue
		}
		digits := true
		for _, character := range contents {
			if character < '0' || character > '9' {
				digits = false
				break
			}
		}
		if digits {
			builder.WriteString("[]")
			index += end
			continue
		}
		builder.WriteString(path[index : index+end+1])
		index += end
	}
	return builder.String()
}

type SemanticCoverageCounts struct {
	// Owner and occurrence counts include explicit null/empty schema slots.
	CodeableConceptOwners               int64
	PopulatedCodeableConceptValues      int64
	NullCodeableConceptOwners           int64
	EmptyCodeableConceptOwners          int64
	CodingOccurrences                   int64
	PopulatedCodingValues               int64
	NullCodingOccurrences               int64
	EmptyCodingOccurrences              int64
	MissingCodeableConceptFacets        int64
	NullCodeableConceptFacets           int64
	EmptyCodeableConceptFacets          int64
	BlankCodeableConceptFacets          int64
	PopulatedCodeableConceptFacets      int64
	MissingCodingFacets                 int64
	NullCodingFacets                    int64
	EmptyCodingFacets                   int64
	BlankCodingFacets                   int64
	PopulatedCodingFacets               int64
	EmptyCodingCollections              int64
	NullCodingCollections               int64
	PopulatedCodingCollections          int64
	EmptyCodeableConceptCollections     int64
	NullCodeableConceptCollections      int64
	PopulatedCodeableConceptCollections int64
	IdentifierOccurrences               int64
	PopulatedIdentifierValues           int64
	NullIdentifierOccurrences           int64
	EmptyIdentifierOccurrences          int64
	ExtensionValueOccurrences           int64
	PopulatedExtensionValues            int64
	NullExtensionValueOccurrences       int64
	EmptyExtensionValueOccurrences      int64
	PrimitiveCodeOccurrences            int64
	PopulatedPrimitiveCodeValues        int64
	NullPrimitiveCodeOccurrences        int64
	EmptyPrimitiveCodeOccurrences       int64
	UnknownStructureOccurrences         int64
	PopulatedUnknownStructures          int64
	NullUnknownStructures               int64
	EmptyUnknownStructures              int64
}

type SemanticCodeableConceptTemplate struct {
	ResourceType        string
	CanonicalPath       string
	DeclaringType       string
	DefinitionPath      []string
	ChoiceGroup         string
	Repeated            bool
	RecursiveDefinition string
	RecursivePath       string
}

// SemanticCoveragePathAggregate keeps the durable census shape aggregated by
// generated canonical path. Concrete owner paths are bounded witnesses for
// diagnostics and tests; source values are never retained.
type SemanticCoveragePathAggregate struct {
	CanonicalPath             string
	Kind                      SemanticCoverageOccurrenceKind
	ResourceBoundarySignature string
	Disposition               SemanticCoverageDispositionKind
	Reason                    SemanticCoverageReason
	OccurrenceCount           int64
	OwnerCount                int64
	CodingCount               int64
	PresenceCounts            map[SemanticCoveragePresence]int64
	CollectionCounts          map[SemanticCoveragePresence]int64
	FacetPresenceCounts       map[string]map[SemanticCoveragePresence]int64
	Facets                    []SemanticCoverageFacetAggregate
	References                []SemanticCoverageReference
	CountsTruncated           bool
	FacetsTruncated           bool
	ReferencesTruncated       bool
	OwnerPathWitnesses        []string
	WitnessesTruncated        bool
}

type SemanticCoverageCensus struct {
	ResourceType      string
	SourceID          string `json:"-"`
	Identity          SemanticCoverageSourceIdentity
	Status            SemanticCoverageCensusStatus
	IncompleteGaps    []SemanticCoverageCensusGap
	Counts            SemanticCoverageCounts
	EvidenceTruncated bool
	Paths             []SemanticCoveragePathAggregate
	Diagnostics       []fhirsemantic.WalkDiagnostic
	occurrences       []semanticCoverageOccurrence
}

type semanticCoverageOccurrence struct {
	CanonicalPath string
	OwnerPath     string
	Kind          SemanticCoverageOccurrenceKind
	Presence      SemanticCoveragePresence
	System        string
	Version       string
	Code          string
	Text          string
	HasCoding     bool
	HasValue      bool
	Facets        map[string]map[SemanticCoveragePresence]int64
}

const semanticCoverageMaxOwnerPathWitnesses = 16
const semanticCoverageMaxReferencesPerPath = 64
const semanticCoverageMaxDiagnostics = 64

// DiscoverSemanticCodeableConceptTemplates walks every generated concrete
// resource root. Recursive definitions are recorded as cycle witnesses instead
// of expanding an unbounded path.
func DiscoverSemanticCodeableConceptTemplates(index *schema.Index) ([]SemanticCodeableConceptTemplate, error) {
	if index == nil {
		return nil, fmt.Errorf("discover CodeableConcept templates: schema index is nil")
	}
	var templates []SemanticCodeableConceptTemplate
	for _, resourceType := range schema.ResourceTypes() {
		root := schema.DefinitionName(resourceType)
		if _, ok := index.ReadDefinition(root); !ok {
			return nil, fmt.Errorf("discover CodeableConcept templates: generated resource %q is missing from schema index", resourceType)
		}
		walk := semanticCoverageSchemaWalk{
			index:        index,
			resourceType: resourceType,
		}
		if err := walk.definition(root, "", nil, nil); err != nil {
			return nil, fmt.Errorf("discover CodeableConcept templates from %q: %w", resourceType, err)
		}
		for _, template := range walk.templates {
			for _, cycle := range walk.cycles {
				if containsString(template.DefinitionPath, cycle.definition) {
					template.RecursiveDefinition = string(cycle.definition)
					template.RecursivePath = cycle.path
					break
				}
			}
			templates = append(templates, template)
		}
	}
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].ResourceType != templates[j].ResourceType {
			return templates[i].ResourceType < templates[j].ResourceType
		}
		if templates[i].CanonicalPath != templates[j].CanonicalPath {
			return templates[i].CanonicalPath < templates[j].CanonicalPath
		}
		return templates[i].DeclaringType < templates[j].DeclaringType
	})
	return templates, nil
}

type semanticCoverageSchemaCycle struct {
	definition schema.DefinitionName
	path       string
}

type semanticCoverageSchemaWalk struct {
	index        *schema.Index
	resourceType string
	templates    []SemanticCodeableConceptTemplate
	cycles       []semanticCoverageSchemaCycle
}

func (w *semanticCoverageSchemaWalk) definition(
	name schema.DefinitionName,
	prefix string,
	stack []schema.DefinitionName,
	definitionPath []string,
) error {
	view, ok := w.index.ReadDefinition(name)
	if !ok {
		return fmt.Errorf("definition %q is missing from schema index", name)
	}
	stack = append(append([]schema.DefinitionName(nil), stack...), name)
	definitionPath = append(append([]string(nil), definitionPath...), string(name))
	return w.elements(view.Elements(), name, prefix, stack, definitionPath)
}

func (w *semanticCoverageSchemaWalk) elements(
	elements schema.ElementListView,
	declaringType schema.DefinitionName,
	prefix string,
	stack []schema.DefinitionName,
	definitionPath []string,
) error {
	for index := 0; index < elements.Len(); index++ {
		element, ok := elements.At(index)
		if !ok {
			return fmt.Errorf("schema element %d is unavailable in %q", index, declaringType)
		}
		metadata := element.Snapshot()
		path := joinCoveragePath(prefix, element.Name())
		referencedType := element.ReferencedType()
		inline := element.Elements()
		if element.JSONType() == schema.JSONTypeArray {
			path += "[]"
			referencedType = element.ArrayElementType()
			inline = element.ArrayElements()
		}
		if referencedType == "CodeableConcept" {
			w.templates = append(w.templates, SemanticCodeableConceptTemplate{
				ResourceType:   w.resourceType,
				CanonicalPath:  path,
				DeclaringType:  string(declaringType),
				DefinitionPath: append([]string(nil), definitionPath...),
				ChoiceGroup:    metadata.ChoiceGroup,
				Repeated:       strings.Contains(path, "[]"),
			})
			continue
		}
		if referencedType != "" {
			if containsDefinition(stack, referencedType) {
				w.cycles = append(w.cycles, semanticCoverageSchemaCycle{definition: referencedType, path: path})
				continue
			}
			if err := w.definition(referencedType, path, stack, definitionPath); err != nil {
				return err
			}
			continue
		}
		if inline.Len() > 0 {
			if err := w.elements(inline, declaringType, path, stack, definitionPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsDefinition(stack []schema.DefinitionName, candidate schema.DefinitionName) bool {
	for _, definition := range stack {
		if definition == candidate {
			return true
		}
	}
	return false
}

func containsString(values []string, candidate schema.DefinitionName) bool {
	for _, value := range values {
		if value == string(candidate) {
			return true
		}
	}
	return false
}

func joinCoveragePath(prefix, member string) string {
	if prefix == "" {
		return member
	}
	return prefix + "." + member
}

// CensusSemanticCoverage counts concrete CodeableConcept and Coding owners
// from generated schema facts. It runs independently of semantic emission.
func CensusSemanticCoverage(index *schema.Index, resourceType, sourceID string, payload []byte) (SemanticCoverageCensus, error) {
	if index == nil {
		return SemanticCoverageCensus{}, fmt.Errorf("census semantic coverage for %q: schema index is nil", resourceType)
	}
	if resourceType == "" {
		return SemanticCoverageCensus{}, fmt.Errorf("census semantic coverage: resource type is empty")
	}
	if sourceID == "" {
		return SemanticCoverageCensus{}, fmt.Errorf("census semantic coverage for %q: source ID is empty", resourceType)
	}
	census := SemanticCoverageCensus{
		ResourceType: resourceType,
		SourceID:     sourceID,
		Identity: SemanticCoverageSourceIdentity{
			SourceID:     sourceID,
			ResourceType: resourceType,
		},
		Status: SemanticCoverageCensusComplete,
	}
	aggregates := make(map[string]*SemanticCoveragePathAggregate)
	diagnostics, err := fhirsemantic.WalkMembers(index, schema.DefinitionName(resourceType), payload, func(fact fhirsemantic.MemberFact) error {
		return recordSemanticCoverageFact(index, fact, &census, aggregates)
	})
	if err != nil {
		return SemanticCoverageCensus{}, fmt.Errorf("census semantic coverage for %q source %q: %w", resourceType, sourceID, err)
	}
	census.Diagnostics = diagnostics
	if err := recordSemanticCoverageUnknownSubtrees(payload, diagnostics, &census, aggregates); err != nil {
		return SemanticCoverageCensus{}, fmt.Errorf("census unknown semantic coverage subtrees for %q source %q: %w", resourceType, sourceID, err)
	}
	census.Paths = make([]SemanticCoveragePathAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		census.Paths = append(census.Paths, *aggregate)
	}
	if census.EvidenceTruncated {
		census.Status = SemanticCoverageCensusPartial
		census.IncompleteGaps = append(census.IncompleteGaps, SemanticCoverageGapEmissionEvidence)
	}
	sort.Slice(census.Paths, func(i, j int) bool {
		if census.Paths[i].CanonicalPath != census.Paths[j].CanonicalPath {
			return census.Paths[i].CanonicalPath < census.Paths[j].CanonicalPath
		}
		return census.Paths[i].Kind < census.Paths[j].Kind
	})
	return census, nil
}

func recordSemanticCoverageFact(
	index *schema.Index,
	fact fhirsemantic.MemberFact,
	census *SemanticCoverageCensus,
	aggregates map[string]*SemanticCoveragePathAggregate,
) error {
	kinds := make([]SemanticCoverageOccurrenceKind, 0, 2)
	switch fact.ReferencedType {
	case "CodeableConcept":
		kinds = append(kinds, SemanticCoverageCodeableConcept)
	case "Coding":
		kinds = append(kinds, SemanticCoverageCoding)
	case "Identifier":
		kinds = append(kinds, SemanticCoverageIdentifier)
	}
	if fact.DeclaringType == "Extension" && fact.Element.ChoiceGroup != "" {
		kinds = append(kinds, SemanticCoverageExtensionValue)
	}
	primitiveString := fact.Element.JSONType == schema.JSONType("string") ||
		fact.Element.JSONType == schema.JSONTypeArray && fact.Element.ItemJSONType == schema.JSONType("string")
	if fact.ReferencedType == "" && strings.TrimSpace(fact.Element.BindingURI) != "" && primitiveString {
		kinds = append(kinds, SemanticCoveragePrimitiveCode)
	}
	if len(kinds) == 0 {
		return nil
	}
	presence, err := semanticCoveragePresence(fact.RawJSON)
	if err != nil {
		return fmt.Errorf("classify presence at %q: %w", fact.OwnerPath, err)
	}
	for _, kind := range kinds {
		key := string(kind) + "\x00" + fact.CanonicalPath
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &SemanticCoveragePathAggregate{
				CanonicalPath:       fact.CanonicalPath,
				Kind:                kind,
				PresenceCounts:      make(map[SemanticCoveragePresence]int64),
				CollectionCounts:    make(map[SemanticCoveragePresence]int64),
				FacetPresenceCounts: make(map[string]map[SemanticCoveragePresence]int64),
				OwnerPathWitnesses:  make([]string, 0, semanticCoverageMaxOwnerPathWitnesses),
			}
			aggregates[key] = aggregate
		}
		if fact.Repeated && !fact.ArrayItem {
			aggregate.CollectionCounts[presence]++
			if kind == SemanticCoverageCodeableConcept {
				switch presence {
				case SemanticCoverageNull:
					census.Counts.NullCodeableConceptCollections++
				case SemanticCoverageEmpty:
					census.Counts.EmptyCodeableConceptCollections++
				case SemanticCoveragePopulated:
					census.Counts.PopulatedCodeableConceptCollections++
				}
			} else if kind == SemanticCoverageCoding {
				switch presence {
				case SemanticCoverageNull:
					census.Counts.NullCodingCollections++
				case SemanticCoverageEmpty:
					census.Counts.EmptyCodingCollections++
				case SemanticCoveragePopulated:
					census.Counts.PopulatedCodingCollections++
				}
			}
			continue
		}
		aggregate.OccurrenceCount++
		aggregate.PresenceCounts[presence]++
		if len(aggregate.OwnerPathWitnesses) < semanticCoverageMaxOwnerPathWitnesses {
			aggregate.OwnerPathWitnesses = append(aggregate.OwnerPathWitnesses, fact.OwnerPath)
		} else {
			aggregate.WitnessesTruncated = true
		}
		incrementSemanticCoverageOccurrenceCount(&census.Counts, aggregate, kind, presence)
		occurrence := semanticCoverageOccurrence{
			CanonicalPath: fact.CanonicalPath,
			OwnerPath:     fact.OwnerPath,
			Kind:          kind,
			Presence:      presence,
			Facets:        make(map[string]map[SemanticCoveragePresence]int64),
		}
		if kind == SemanticCoveragePrimitiveCode || kind == SemanticCoverageExtensionValue {
			occurrence.HasValue = presence == SemanticCoveragePopulated || presence == SemanticCoverageBlank
		}
		if presence != SemanticCoverageNull && strings.HasPrefix(strings.TrimSpace(string(fact.RawJSON)), "{") {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(fact.RawJSON, &object); err != nil {
				return fmt.Errorf("read semantic coverage occurrence at %q: %w", fact.OwnerPath, err)
			}
			switch kind {
			case SemanticCoverageCodeableConcept:
				if raw, exists := object["text"]; exists {
					occurrence.Text = semanticStringFromRaw(raw)
				}
				if raw, exists := object["coding"]; exists {
					var codings []json.RawMessage
					if err := json.Unmarshal(raw, &codings); err != nil {
						return fmt.Errorf("read CodeableConcept codings at %q: %w", fact.OwnerPath, err)
					}
					occurrence.HasCoding = len(codings) > 0
				}
			case SemanticCoverageCoding:
				if raw, exists := object["system"]; exists {
					occurrence.System = semanticStringFromRaw(raw)
				}
				if raw, exists := object["version"]; exists {
					occurrence.Version = semanticStringFromRaw(raw)
				}
				if raw, exists := object["code"]; exists {
					occurrence.Code = semanticStringFromRaw(raw)
				}
			case SemanticCoverageIdentifier:
				if raw, exists := object["value"]; exists {
					valuePresence, err := semanticCoveragePresence(raw)
					if err != nil {
						return fmt.Errorf("read Identifier value at %q: %w", fact.OwnerPath, err)
					}
					occurrence.HasValue = valuePresence == SemanticCoveragePopulated
				}
			}
			if _, hasDefinition := index.ReadDefinition(fact.ReferencedType); hasDefinition && (kind == SemanticCoverageCodeableConcept || kind == SemanticCoverageCoding || kind == SemanticCoverageIdentifier) {
				if err := recordSemanticCoverageDirectFacets(index, fact, aggregate, &census.Counts, &occurrence); err != nil {
					return err
				}
			}
		}
		if len(census.occurrences) < SemanticCoverageMaxReconciliationItemsPerSource {
			census.occurrences = append(census.occurrences, occurrence)
		} else {
			census.EvidenceTruncated = true
		}
	}
	return nil
}

func incrementSemanticCoverageOccurrenceCount(
	counts *SemanticCoverageCounts,
	aggregate *SemanticCoveragePathAggregate,
	kind SemanticCoverageOccurrenceKind,
	presence SemanticCoveragePresence,
) {
	switch kind {
	case SemanticCoverageCodeableConcept:
		counts.CodeableConceptOwners++
		aggregate.OwnerCount++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedCodeableConceptValues++
		case SemanticCoverageNull:
			counts.NullCodeableConceptOwners++
		case SemanticCoverageEmpty:
			counts.EmptyCodeableConceptOwners++
		}
	case SemanticCoverageCoding:
		counts.CodingOccurrences++
		aggregate.CodingCount++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedCodingValues++
		case SemanticCoverageNull:
			counts.NullCodingOccurrences++
		case SemanticCoverageEmpty:
			counts.EmptyCodingOccurrences++
		}
	case SemanticCoverageIdentifier:
		counts.IdentifierOccurrences++
		aggregate.OwnerCount++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedIdentifierValues++
		case SemanticCoverageNull:
			counts.NullIdentifierOccurrences++
		case SemanticCoverageEmpty:
			counts.EmptyIdentifierOccurrences++
		}
	case SemanticCoverageExtensionValue:
		counts.ExtensionValueOccurrences++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedExtensionValues++
		case SemanticCoverageNull:
			counts.NullExtensionValueOccurrences++
		case SemanticCoverageEmpty:
			counts.EmptyExtensionValueOccurrences++
		}
	case SemanticCoveragePrimitiveCode:
		counts.PrimitiveCodeOccurrences++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedPrimitiveCodeValues++
		case SemanticCoverageNull:
			counts.NullPrimitiveCodeOccurrences++
		case SemanticCoverageEmpty:
			counts.EmptyPrimitiveCodeOccurrences++
		}
	case SemanticCoverageUnknownStructure:
		counts.UnknownStructureOccurrences++
		switch presence {
		case SemanticCoveragePopulated:
			counts.PopulatedUnknownStructures++
		case SemanticCoverageNull:
			counts.NullUnknownStructures++
		case SemanticCoverageEmpty:
			counts.EmptyUnknownStructures++
		}
	}
}

func recordSemanticCoverageUnknownSubtrees(
	payload []byte,
	diagnostics []fhirsemantic.WalkDiagnostic,
	census *SemanticCoverageCensus,
	aggregates map[string]*SemanticCoveragePathAggregate,
) error {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != fhirsemantic.WalkUnknownMember {
			continue
		}
		raw, ok, err := rawJSONAtConcretePath(payload, diagnostic.OwnerPath)
		if err != nil {
			return fmt.Errorf("read unknown member at %q: %w", diagnostic.OwnerPath, err)
		}
		if !ok {
			census.Status = SemanticCoverageCensusPartial
			census.IncompleteGaps = append(census.IncompleteGaps, SemanticCoverageGapUnknownSubtrees)
			continue
		}
		presence, err := semanticCoveragePresence(raw)
		if err != nil {
			return fmt.Errorf("classify unknown member at %q: %w", diagnostic.OwnerPath, err)
		}
		kind := SemanticCoverageUnknownStructure
		key := string(kind) + "\x00" + diagnostic.CanonicalPath
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &SemanticCoveragePathAggregate{
				CanonicalPath:       diagnostic.CanonicalPath,
				Kind:                kind,
				PresenceCounts:      make(map[SemanticCoveragePresence]int64),
				FacetPresenceCounts: make(map[string]map[SemanticCoveragePresence]int64),
				OwnerPathWitnesses:  make([]string, 0, semanticCoverageMaxOwnerPathWitnesses),
			}
			aggregates[key] = aggregate
		}
		aggregate.OccurrenceCount++
		aggregate.PresenceCounts[presence]++
		if len(aggregate.OwnerPathWitnesses) < semanticCoverageMaxOwnerPathWitnesses {
			aggregate.OwnerPathWitnesses = append(aggregate.OwnerPathWitnesses, diagnostic.OwnerPath)
		} else {
			aggregate.WitnessesTruncated = true
		}
		incrementSemanticCoverageOccurrenceCount(&census.Counts, aggregate, kind, presence)
		occurrence := semanticCoverageOccurrence{
			CanonicalPath: diagnostic.CanonicalPath,
			OwnerPath:     diagnostic.OwnerPath,
			Kind:          kind,
			Presence:      presence,
		}
		if len(census.occurrences) < SemanticCoverageMaxReconciliationItemsPerSource {
			census.occurrences = append(census.occurrences, occurrence)
		} else {
			census.EvidenceTruncated = true
		}
		if presence == SemanticCoveragePopulated || presence == SemanticCoverageBlank {
			census.Status = SemanticCoverageCensusPartial
			if !hasSemanticCoverageGap(census.IncompleteGaps, SemanticCoverageGapUnknownSubtrees) {
				census.IncompleteGaps = append(census.IncompleteGaps, SemanticCoverageGapUnknownSubtrees)
			}
		}
	}
	return nil
}

func rawJSONAtConcretePath(payload []byte, path string) (json.RawMessage, bool, error) {
	var current json.RawMessage = payload
	if path == "" || path == "root" {
		return current, true, nil
	}
	for _, segment := range strings.Split(path, ".") {
		name := segment
		arrayIndex := -1
		if bracket := strings.IndexByte(segment, '['); bracket >= 0 && strings.HasSuffix(segment, "]") {
			name = segment[:bracket]
			indexText := segment[bracket+1 : len(segment)-1]
			parsed, err := strconv.Atoi(indexText)
			if err != nil {
				return nil, false, fmt.Errorf("parse array index in %q: %w", segment, err)
			}
			arrayIndex = parsed
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false, fmt.Errorf("decode object while resolving %q: %w", path, err)
		}
		value, exists := object[name]
		if !exists {
			return nil, false, nil
		}
		if arrayIndex >= 0 {
			var items []json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil {
				return nil, false, fmt.Errorf("decode array %q: %w", name, err)
			}
			if arrayIndex >= len(items) {
				return nil, false, nil
			}
			value = items[arrayIndex]
		}
		current = value
	}
	return current, true, nil
}

func semanticCoveragePresence(value json.RawMessage) (SemanticCoveragePresence, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", fmt.Errorf("decode JSON presence: %w", err)
	}
	switch value := decoded.(type) {
	case nil:
		return SemanticCoverageNull, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return SemanticCoverageBlank, nil
		}
	case []any:
		if len(value) == 0 {
			return SemanticCoverageEmpty, nil
		}
	case map[string]any:
		if len(value) == 0 {
			return SemanticCoverageEmpty, nil
		}
	}
	return SemanticCoveragePopulated, nil
}

func recordSemanticCoverageDirectFacets(
	index *schema.Index,
	fact fhirsemantic.MemberFact,
	aggregate *SemanticCoveragePathAggregate,
	counts *SemanticCoverageCounts,
	occurrence *semanticCoverageOccurrence,
) error {
	definition, ok := index.ReadDefinition(fact.ReferencedType)
	if !ok {
		return fmt.Errorf("record facets for %q: definition %q is missing", fact.OwnerPath, fact.ReferencedType)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(fact.RawJSON, &object); err != nil {
		return fmt.Errorf("record facets for %q: decode object: %w", fact.OwnerPath, err)
	}
	if object == nil {
		return fmt.Errorf("record facets for %q: expected object for %q", fact.OwnerPath, fact.ReferencedType)
	}
	for elementIndex := 0; elementIndex < definition.Elements().Len(); elementIndex++ {
		element, ok := definition.Elements().At(elementIndex)
		if !ok {
			return fmt.Errorf("record facets for %q: schema element %d is unavailable", fact.OwnerPath, elementIndex)
		}
		member := element.Name()
		facetPath := joinCoveragePath(fact.CanonicalPath, member)
		if element.JSONType() == schema.JSONTypeArray {
			facetPath += "[]"
		}
		presence := SemanticCoverageMissing
		if raw, exists := object[member]; exists {
			var err error
			presence, err = semanticCoveragePresence(raw)
			if err != nil {
				return fmt.Errorf("record facet presence for %q.%s: %w", fact.OwnerPath, member, err)
			}
		}
		facetCounts := aggregate.FacetPresenceCounts[facetPath]
		if facetCounts == nil {
			facetCounts = make(map[SemanticCoveragePresence]int64)
			aggregate.FacetPresenceCounts[facetPath] = facetCounts
		}
		facetCounts[presence]++
		occurrenceFacetCounts := occurrence.Facets[facetPath]
		if occurrenceFacetCounts == nil {
			occurrenceFacetCounts = make(map[SemanticCoveragePresence]int64)
			occurrence.Facets[facetPath] = occurrenceFacetCounts
		}
		occurrenceFacetCounts[presence]++
		switch fact.ReferencedType {
		case "CodeableConcept":
			incrementSemanticCoverageFacetCount(counts, presence, true)
		case "Coding":
			incrementSemanticCoverageFacetCount(counts, presence, false)
		}
	}
	return nil
}

func incrementSemanticCoverageFacetCount(counts *SemanticCoverageCounts, presence SemanticCoveragePresence, concept bool) {
	if concept {
		switch presence {
		case SemanticCoverageMissing:
			counts.MissingCodeableConceptFacets++
		case SemanticCoverageNull:
			counts.NullCodeableConceptFacets++
		case SemanticCoverageEmpty:
			counts.EmptyCodeableConceptFacets++
		case SemanticCoverageBlank:
			counts.BlankCodeableConceptFacets++
		case SemanticCoveragePopulated:
			counts.PopulatedCodeableConceptFacets++
		}
		return
	}
	switch presence {
	case SemanticCoverageMissing:
		counts.MissingCodingFacets++
	case SemanticCoverageNull:
		counts.NullCodingFacets++
	case SemanticCoverageEmpty:
		counts.EmptyCodingFacets++
	case SemanticCoverageBlank:
		counts.BlankCodingFacets++
	case SemanticCoveragePopulated:
		counts.PopulatedCodingFacets++
	}
}

// ReconcileSemanticCoverage combines the independent schema census with exact
// emission owner locators and paths that remain executable in the raw source.
// It never infers a match from code labels or values.
func ReconcileSemanticCoverage(
	census SemanticCoverageCensus,
	emissions []SemanticCoverageEmissionEvidence,
	rawPaths map[string]struct{},
) (SemanticCoverageSourceContribution, error) {
	if census.ResourceType == "" || census.SourceID == "" {
		return SemanticCoverageSourceContribution{}, fmt.Errorf("reconcile semantic coverage requires resource type and source ID")
	}
	if len(census.occurrences) == 0 && !census.EvidenceTruncated && (census.Counts.CodeableConceptOwners > 0 || census.Counts.CodingOccurrences > 0) {
		return SemanticCoverageSourceContribution{}, fmt.Errorf("reconcile semantic coverage: occurrence details were not retained for a nonempty census")
	}
	if census.EvidenceTruncated || len(emissions) > SemanticCoverageMaxReconciliationItemsPerSource {
		return semanticCoverageTruncatedContribution(census, rawPaths), nil
	}

	identity := census.Identity
	identity.SourceID = census.SourceID
	identity.ResourceType = census.ResourceType
	evidenceByKeyOwner := make(map[string][]SemanticCoverageEmissionEvidence)
	evidenceByOwner := make(map[string][]SemanticCoverageEmissionEvidence)
	evidenceByValueOwner := make(map[string][]SemanticCoverageEmissionEvidence)
	for _, evidence := range emissions {
		if evidence.Owner.SourceID != census.SourceID || evidence.Owner.ResourceType != census.ResourceType {
			continue
		}
		if evidence.KeyOwner.OwnerPath != "" {
			evidenceByKeyOwner[evidence.KeyOwner.OwnerPath] = append(evidenceByKeyOwner[evidence.KeyOwner.OwnerPath], evidence)
		}
		if evidence.Owner.OwnerPath != "" {
			evidenceByOwner[evidence.Owner.OwnerPath] = append(evidenceByOwner[evidence.Owner.OwnerPath], evidence)
		}
		if evidence.ValueOwner.OwnerPath != "" {
			evidenceByValueOwner[evidence.ValueOwner.OwnerPath] = append(evidenceByValueOwner[evidence.ValueOwner.OwnerPath], evidence)
		}
	}
	occurrencesByOwner := make(map[string]semanticCoverageOccurrence, len(census.occurrences))
	for _, occurrence := range census.occurrences {
		occurrencesByOwner[occurrence.OwnerPath] = occurrence
	}

	groups := make(map[string]*SemanticCoveragePathAggregate)
	facetGroups := make(map[string]map[string]*SemanticCoverageFacetAggregate)
	for _, occurrence := range census.occurrences {
		disposition, reason, references := reconcileCoverageOccurrence(
			occurrence, census, evidenceByKeyOwner, evidenceByOwner, evidenceByValueOwner, occurrencesByOwner, rawPaths,
		)
		groupKey := strings.Join([]string{
			occurrence.CanonicalPath,
			string(occurrence.Kind),
			string(disposition),
			string(reason),
		}, "\x00")
		aggregate := groups[groupKey]
		if aggregate == nil {
			aggregate = &SemanticCoveragePathAggregate{
				CanonicalPath:       occurrence.CanonicalPath,
				Kind:                occurrence.Kind,
				Disposition:         disposition,
				Reason:              reason,
				PresenceCounts:      make(map[SemanticCoveragePresence]int64),
				CollectionCounts:    make(map[SemanticCoveragePresence]int64),
				FacetPresenceCounts: make(map[string]map[SemanticCoveragePresence]int64),
				OwnerPathWitnesses:  make([]string, 0, semanticCoverageMaxOwnerPathWitnesses),
			}
			groups[groupKey] = aggregate
		}
		aggregate.OccurrenceCount++
		aggregate.PresenceCounts[occurrence.Presence]++
		switch occurrence.Kind {
		case SemanticCoverageCodeableConcept:
			aggregate.OwnerCount++
		case SemanticCoverageCoding:
			aggregate.CodingCount++
		}
		if len(aggregate.OwnerPathWitnesses) < semanticCoverageMaxOwnerPathWitnesses {
			aggregate.OwnerPathWitnesses = append(aggregate.OwnerPathWitnesses, occurrence.OwnerPath)
		} else {
			aggregate.WitnessesTruncated = true
		}
		for member, presences := range occurrence.Facets {
			memberCounts := aggregate.FacetPresenceCounts[member]
			if memberCounts == nil {
				memberCounts = make(map[SemanticCoveragePresence]int64)
				aggregate.FacetPresenceCounts[member] = memberCounts
			}
			for presence, count := range presences {
				memberCounts[presence] += count
			}
			facetDisposition, facetReason, facetReferences := reconcileCoverageFacet(
				occurrence, census, occurrencesByOwner, member, presences, evidenceByKeyOwner, evidenceByOwner, evidenceByValueOwner, rawPaths,
			)
			facetKey := strings.Join([]string{member, string(facetDisposition), string(facetReason)}, "\x00")
			byFacetKey := facetGroups[groupKey]
			if byFacetKey == nil {
				byFacetKey = make(map[string]*SemanticCoverageFacetAggregate)
				facetGroups[groupKey] = byFacetKey
			}
			facet := byFacetKey[facetKey]
			if facet == nil {
				facet = &SemanticCoverageFacetAggregate{
					Path: member, Kind: semanticCoverageFacetMember(member), Disposition: facetDisposition,
					Reason: facetReason, PresenceCounts: make(map[SemanticCoveragePresence]int64),
				}
				byFacetKey[facetKey] = facet
			}
			facet.OccurrenceCount++
			for presence, count := range presences {
				facet.PresenceCounts[presence] += count
			}
			for _, reference := range facetReferences {
				appendSemanticCoverageFacetReference(facet, reference)
			}
		}
		for _, reference := range references {
			appendSemanticCoverageReference(aggregate, reference)
		}
	}

	paths := make([]SemanticCoveragePathAggregate, 0, len(groups))
	countsTruncated := false
	facetsTruncated := false
	referencesTruncated := false
	diagnosticLocatorsTruncated := false
	for _, aggregate := range groups {
		groupKey := strings.Join([]string{aggregate.CanonicalPath, string(aggregate.Kind), string(aggregate.Disposition), string(aggregate.Reason)}, "\x00")
		for _, facet := range facetGroups[groupKey] {
			if len(facet.References) > semanticCoverageMaxReferencesPerPath {
				facet.References = facet.References[:semanticCoverageMaxReferencesPerPath]
				facet.ReferencesTruncated = true
			}
			referencesTruncated = referencesTruncated || facet.ReferencesTruncated
			aggregate.Facets = append(aggregate.Facets, *facet)
		}
		sort.Slice(aggregate.Facets, func(i, j int) bool {
			if aggregate.Facets[i].Path != aggregate.Facets[j].Path {
				return aggregate.Facets[i].Path < aggregate.Facets[j].Path
			}
			if aggregate.Facets[i].Disposition != aggregate.Facets[j].Disposition {
				return aggregate.Facets[i].Disposition < aggregate.Facets[j].Disposition
			}
			return aggregate.Facets[i].Reason < aggregate.Facets[j].Reason
		})
		if len(aggregate.References) > semanticCoverageMaxReferencesPerPath {
			aggregate.References = aggregate.References[:semanticCoverageMaxReferencesPerPath]
			aggregate.ReferencesTruncated = true
		}
		countsTruncated = countsTruncated || aggregate.CountsTruncated
		facetsTruncated = facetsTruncated || aggregate.FacetsTruncated
		referencesTruncated = referencesTruncated || aggregate.ReferencesTruncated
		paths = append(paths, *aggregate)
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].CanonicalPath != paths[j].CanonicalPath {
			return paths[i].CanonicalPath < paths[j].CanonicalPath
		}
		if paths[i].Kind != paths[j].Kind {
			return paths[i].Kind < paths[j].Kind
		}
		if paths[i].Disposition != paths[j].Disposition {
			return paths[i].Disposition < paths[j].Disposition
		}
		return paths[i].Reason < paths[j].Reason
	})

	diagnostics := make([]SemanticCoverageDiagnostic, 0, min(len(census.Diagnostics), semanticCoverageMaxDiagnostics))
	for _, diagnostic := range census.Diagnostics {
		if len(diagnostics) == semanticCoverageMaxDiagnostics {
			diagnosticLocatorsTruncated = true
			break
		}
		reason := SemanticCoverageReasonIncompleteSource
		if diagnostic.Code == fhirsemantic.WalkUnknownMember {
			reason = SemanticCoverageReasonUnknownSchemaMember
		}
		diagnostics = append(diagnostics, SemanticCoverageDiagnostic{
			Code: string(diagnostic.Code), CanonicalPath: diagnostic.CanonicalPath, OwnerPath: diagnostic.OwnerPath,
			Reason: reason, Explanation: "generated schema walker reported source/schema structure requiring review",
		})
	}

	incompleteGaps := append([]SemanticCoverageCensusGap(nil), census.IncompleteGaps...)
	if identity.SchemaIdentity == "" && !hasSemanticCoverageGap(incompleteGaps, SemanticCoverageGapSchemaIdentity) {
		incompleteGaps = append(incompleteGaps, SemanticCoverageGapSchemaIdentity)
	}
	if int64(len(census.occurrences)) != semanticCoverageOccurrenceCount(census.Counts) &&
		!hasSemanticCoverageGap(incompleteGaps, SemanticCoverageGapOccurrenceClassify) {
		incompleteGaps = append(incompleteGaps, SemanticCoverageGapOccurrenceClassify)
	}
	complete := census.Status == SemanticCoverageCensusComplete && len(incompleteGaps) == 0 &&
		identity.SchemaIdentity != "" && !countsTruncated && !facetsTruncated && !referencesTruncated
	contribution := SemanticCoverageSourceContribution{
		Identity:                    identity,
		SourceRecordCount:           1,
		Counts:                      census.Counts,
		Paths:                       paths,
		IncompleteGaps:              incompleteGaps,
		Diagnostics:                 diagnostics,
		CountsTruncated:             countsTruncated,
		FacetsTruncated:             facetsTruncated,
		ReferencesTruncated:         referencesTruncated,
		ReconciliationTruncated:     false,
		DiagnosticLocatorsTruncated: diagnosticLocatorsTruncated,
		Complete:                    complete,
	}
	if identity.Project != "" && NormalizeDatasetGeneration(identity.DatasetGeneration) != "" {
		contribution.BuildID = SemanticInventoryBuildID(identity.Project, identity.DatasetGeneration)
	}
	return contribution, nil
}

func semanticCoverageTruncatedContribution(
	census SemanticCoverageCensus,
	rawPaths map[string]struct{},
) SemanticCoverageSourceContribution {
	paths := append([]SemanticCoveragePathAggregate(nil), census.Paths...)
	for index := range paths {
		path := &paths[index]
		path.Reason = SemanticCoverageReasonTruncatedEvidence
		if _, retained := rawPaths[path.CanonicalPath]; retained {
			path.Disposition = SemanticCoverageRawRetainedDisposition
		} else {
			path.Disposition = SemanticCoverageUnsupportedDisposition
		}
		path.References = nil
		path.ReferencesTruncated = true
		path.Facets = make([]SemanticCoverageFacetAggregate, 0, len(path.FacetPresenceCounts))
		for member, presenceCounts := range path.FacetPresenceCounts {
			path.Facets = append(path.Facets, SemanticCoverageFacetAggregate{
				Path: member, Kind: semanticCoverageFacetMember(member),
				Disposition: path.Disposition, Reason: path.Reason,
				OccurrenceCount: path.OccurrenceCount,
				PresenceCounts:  cloneSemanticCoveragePresenceCounts(presenceCounts), ReferencesTruncated: true,
			})
		}
		sort.Slice(path.Facets, func(i, j int) bool { return path.Facets[i].Path < path.Facets[j].Path })
	}
	diagnostics, diagnosticLocatorsTruncated := semanticCoverageDiagnostics(census)
	incompleteGaps := append([]SemanticCoverageCensusGap(nil), census.IncompleteGaps...)
	if !hasSemanticCoverageGap(incompleteGaps, SemanticCoverageGapEmissionEvidence) {
		incompleteGaps = append(incompleteGaps, SemanticCoverageGapEmissionEvidence)
	}
	identity := census.Identity
	identity.SourceID = census.SourceID
	identity.ResourceType = census.ResourceType
	contribution := SemanticCoverageSourceContribution{
		Identity:                    identity,
		SourceRecordCount:           1,
		Counts:                      census.Counts,
		Paths:                       paths,
		IncompleteGaps:              incompleteGaps,
		Diagnostics:                 diagnostics,
		ReferencesTruncated:         len(paths) > 0,
		ReconciliationTruncated:     true,
		DiagnosticLocatorsTruncated: diagnosticLocatorsTruncated,
		Complete:                    false,
	}
	if identity.Project != "" && NormalizeDatasetGeneration(identity.DatasetGeneration) != "" {
		contribution.BuildID = SemanticInventoryBuildID(identity.Project, identity.DatasetGeneration)
	}
	return contribution
}

func semanticCoverageDiagnostics(census SemanticCoverageCensus) ([]SemanticCoverageDiagnostic, bool) {
	diagnostics := make([]SemanticCoverageDiagnostic, 0, min(len(census.Diagnostics), semanticCoverageMaxDiagnostics))
	for _, diagnostic := range census.Diagnostics {
		if len(diagnostics) == semanticCoverageMaxDiagnostics {
			return diagnostics, true
		}
		reason := SemanticCoverageReasonIncompleteSource
		if diagnostic.Code == fhirsemantic.WalkUnknownMember {
			reason = SemanticCoverageReasonUnknownSchemaMember
		}
		diagnostics = append(diagnostics, SemanticCoverageDiagnostic{
			Code: string(diagnostic.Code), CanonicalPath: diagnostic.CanonicalPath, OwnerPath: diagnostic.OwnerPath,
			Reason: reason, Explanation: "generated schema walker reported source/schema structure requiring review",
		})
	}
	return diagnostics, false
}

func reconcileCoverageOccurrence(
	occurrence semanticCoverageOccurrence,
	census SemanticCoverageCensus,
	evidenceByKeyOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByValueOwner map[string][]SemanticCoverageEmissionEvidence,
	occurrencesByOwner map[string]semanticCoverageOccurrence,
	rawPaths map[string]struct{},
) (SemanticCoverageDispositionKind, SemanticCoverageReason, []SemanticCoverageReference) {
	var references []SemanticCoverageReference
	referenceSet := make(map[string]struct{})
	addReference := func(evidence SemanticCoverageEmissionEvidence) {
		if evidence.Status != "SUPPORTED" || evidence.ConceptID == "" || evidence.BindingID == "" {
			return
		}
		if reference, ok := semanticCoverageReferenceFromEvidence(evidence); ok {
			key := semanticCoverageReferenceKey(reference)
			if _, exists := referenceSet[key]; exists {
				return
			}
			referenceSet[key] = struct{}{}
			references = append(references, reference)
			return
		}
		key := evidence.ConceptID + "\x00" + evidence.BindingID
		if _, exists := referenceSet[key]; exists {
			return
		}
		referenceSet[key] = struct{}{}
		references = append(references, SemanticCoverageReference{
			Kind: SemanticCoverageExactReference, ConceptID: evidence.ConceptID, BindingID: evidence.BindingID,
		})
	}
	if occurrence.Presence == SemanticCoveragePopulated {
		switch occurrence.Kind {
		case SemanticCoverageCoding:
			ownerEvidence := evidenceByKeyOwner[occurrence.OwnerPath]
			if semanticCoverageHasUnrepresentedCodingFacets(occurrence) || semanticCoverageEvidenceHasMixedChoices(ownerEvidence) {
				break
			}
			for _, evidence := range ownerEvidence {
				addReference(evidence)
			}
		case SemanticCoverageCodeableConcept:
			if occurrence.HasCoding && !semanticCoverageHasUnrepresentedConceptFacets(occurrence) {
				children := semanticCoverageCodingChildren(occurrence, census.occurrences, occurrencesByOwner)
				allChildrenCovered := len(children) > 0
				for _, child := range children {
					childReferences := make(map[string]struct{})
					childEvidence := evidenceByKeyOwner[child.OwnerPath]
					if semanticCoverageEvidenceHasMixedChoices(childEvidence) {
						allChildrenCovered = false
						continue
					}
					for _, evidence := range childEvidence {
						if evidence.Status == "SUPPORTED" && evidence.ConceptID != "" && evidence.BindingID != "" {
							childReferences[evidence.ConceptID+"\x00"+evidence.BindingID] = struct{}{}
							addReference(evidence)
						}
					}
					if len(childReferences) == 0 {
						allChildrenCovered = false
					}
				}
				if allChildrenCovered && len(references) > 0 {
					return SemanticCoverageSemanticDisposition, "", references
				}
			}
		case SemanticCoverageIdentifier:
			if occurrence.HasValue {
				for _, evidence := range evidenceByOwner[occurrence.OwnerPath] {
					addReference(evidence)
				}
			}
		case SemanticCoverageExtensionValue:
			for _, evidence := range evidenceByValueOwner[occurrence.OwnerPath] {
				addReference(evidence)
			}
		case SemanticCoveragePrimitiveCode:
			for _, evidence := range evidenceByValueOwner[occurrence.OwnerPath] {
				addReference(evidence)
			}
		}
		if (occurrence.Kind == SemanticCoverageCoding || occurrence.Kind == SemanticCoverageIdentifier ||
			occurrence.Kind == SemanticCoverageExtensionValue || occurrence.Kind == SemanticCoveragePrimitiveCode) && len(references) > 0 {
			return SemanticCoverageSemanticDisposition, "", references
		}
	}

	reason := semanticCoverageOccurrenceReason(occurrence, evidenceByKeyOwner, evidenceByOwner, evidenceByValueOwner)
	if _, retained := rawPaths[occurrence.CanonicalPath]; retained {
		return SemanticCoverageRawRetainedDisposition, reason, nil
	}
	return SemanticCoverageUnsupportedDisposition, reason, nil
}

func reconcileCoverageFacet(
	occurrence semanticCoverageOccurrence,
	census SemanticCoverageCensus,
	occurrencesByOwner map[string]semanticCoverageOccurrence,
	facetPath string,
	presences map[SemanticCoveragePresence]int64,
	evidenceByKeyOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByValueOwner map[string][]SemanticCoverageEmissionEvidence,
	rawPaths map[string]struct{},
) (SemanticCoverageDispositionKind, SemanticCoverageReason, []SemanticCoverageReference) {
	member := semanticCoverageFacetMember(facetPath)
	var matched []SemanticCoverageEmissionEvidence
	var references []SemanticCoverageReference
	switch occurrence.Kind {
	case SemanticCoverageCodeableConcept:
		if member == "coding" {
			if occurrence.Presence == SemanticCoveragePopulated && !semanticCoverageHasUnrepresentedConceptFacets(occurrence) {
				children := semanticCoverageCodingChildren(occurrence, census.occurrences, occurrencesByOwner)
				allChildrenCovered := len(children) > 0
				for _, child := range children {
					childEvidence := evidenceByKeyOwner[child.OwnerPath]
					if semanticCoverageEvidenceHasMixedChoices(childEvidence) {
						allChildrenCovered = false
						continue
					}
					matchedEvidence := false
					for _, evidence := range childEvidence {
						if evidence.Status != "SUPPORTED" || evidence.ConceptID == "" || evidence.BindingID == "" {
							continue
						}
						matchedEvidence = true
						if reference, ok := semanticCoverageReferenceFromEvidence(evidence); ok {
							references = appendUniqueCoverageReference(references, reference)
						} else {
							references = appendUniqueCoverageReference(references, SemanticCoverageReference{
								Kind: SemanticCoverageExactReference, ConceptID: evidence.ConceptID, BindingID: evidence.BindingID,
							})
						}
					}
					if !matchedEvidence {
						allChildrenCovered = false
					}
				}
				if allChildrenCovered && len(references) > 0 {
					return SemanticCoverageSemanticDisposition, "", references
				}
			}
		}
	case SemanticCoverageCoding:
		if member == "system" || member == "version" || member == "code" || member == "display" {
			matched = evidenceByKeyOwner[occurrence.OwnerPath]
		}
	case SemanticCoverageIdentifier:
		if member == "system" {
			matched = evidenceByOwner[occurrence.OwnerPath]
		} else if member == "value" {
			matched = evidenceByValueOwner[occurrence.OwnerPath+".value"]
		}
	case SemanticCoverageExtensionValue, SemanticCoveragePrimitiveCode:
		matched = evidenceByValueOwner[occurrence.OwnerPath]
	}
	for _, evidence := range matched {
		if !semanticCoverageEvidenceCoversFacet(evidence, occurrence, member) {
			continue
		}
		if reference, ok := semanticCoverageReferenceFromEvidence(evidence); ok {
			references = appendUniqueCoverageReference(references, reference)
		} else if evidence.ConceptID != "" && evidence.BindingID != "" {
			references = appendUniqueCoverageReference(references, SemanticCoverageReference{
				Kind: SemanticCoverageExactReference, ConceptID: evidence.ConceptID, BindingID: evidence.BindingID,
			})
		}
	}
	if len(references) > 0 {
		return SemanticCoverageSemanticDisposition, "", references
	}
	if _, retained := rawPaths[facetPath]; retained {
		return SemanticCoverageRawRetainedDisposition, semanticCoverageFacetReason(occurrence, member, presences), nil
	}
	return SemanticCoverageUnsupportedDisposition, semanticCoverageFacetReason(occurrence, member, presences), nil
}

func semanticCoverageEvidenceCoversFacet(evidence SemanticCoverageEmissionEvidence, occurrence semanticCoverageOccurrence, member string) bool {
	if evidence.Status != "SUPPORTED" {
		return false
	}
	switch occurrence.Kind {
	case SemanticCoverageCoding:
		return evidence.KeySelector != "" && semanticCoverageHasRole(evidence.Roles, SemanticCoverageRoleConceptKey) &&
			semanticCoverageHasRole(evidence.Roles, SemanticCoverageRoleCodingMetadata)
	case SemanticCoverageIdentifier:
		if member == "system" {
			return strings.HasSuffix(evidence.KeySelector, ".system")
		}
		return member == "value" && strings.HasSuffix(evidence.ValueSelector, ".value")
	case SemanticCoverageExtensionValue:
		return evidence.ValueOwner.OwnerPath == occurrence.OwnerPath && evidence.ValueSelector != ""
	case SemanticCoveragePrimitiveCode:
		return evidence.ValueOwner.OwnerPath == occurrence.OwnerPath && evidence.ValueSelector != ""
	}
	return false
}

func semanticCoverageHasRole(roles []SemanticCoverageRole, target SemanticCoverageRole) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

func semanticCoverageFacetReason(
	occurrence semanticCoverageOccurrence,
	member string,
	presences map[SemanticCoveragePresence]int64,
) SemanticCoverageReason {
	if member == "system" && occurrence.Kind == SemanticCoverageCoding && strings.TrimSpace(occurrence.System) == "" {
		return SemanticCoverageReasonMissingSystem
	}
	if member == "code" && occurrence.Kind == SemanticCoverageCoding && strings.TrimSpace(occurrence.Code) == "" {
		return SemanticCoverageReasonMissingCode
	}
	if member == "value" && occurrence.Kind == SemanticCoverageIdentifier && !occurrence.HasValue {
		return SemanticCoverageReasonMissingIdentifierValue
	}
	if occurrence.Kind == SemanticCoveragePrimitiveCode {
		return SemanticCoverageReasonUnknownCodeClassification
	}
	if occurrence.Kind == SemanticCoverageUnknownStructure {
		return SemanticCoverageReasonUnknownSchemaMember
	}
	if presences[SemanticCoveragePopulated] == 0 && presences[SemanticCoverageBlank] == 0 {
		return SemanticCoverageReasonIncompleteSource
	}
	return SemanticCoverageReasonUnrepresentedMetadata
}

func semanticCoverageFacetMember(path string) string {
	if index := strings.LastIndex(path, "."); index >= 0 {
		path = path[index+1:]
	}
	return strings.TrimSuffix(path, "[]")
}

func appendUniqueCoverageReference(references []SemanticCoverageReference, reference SemanticCoverageReference) []SemanticCoverageReference {
	key := semanticCoverageReferenceKey(reference)
	for _, existing := range references {
		if semanticCoverageReferenceKey(existing) == key {
			return references
		}
	}
	return append(references, reference)
}

func appendSemanticCoverageFacetReference(facet *SemanticCoverageFacetAggregate, reference SemanticCoverageReference) {
	facet.References = appendUniqueCoverageReference(facet.References, reference)
}

func semanticCoverageReferenceFromEvidence(evidence SemanticCoverageEmissionEvidence) (SemanticCoverageReference, bool) {
	if evidence.RuleHint != SemanticRuleHintCodedConceptV1 || strings.TrimSpace(evidence.KeySystem) == "" {
		return SemanticCoverageReference{}, false
	}
	family := SemanticInventoryFamilyReference{
		BindingID:    evidence.BindingID,
		SourceKind:   evidence.SourceKind,
		ResourceType: evidence.Owner.ResourceType,
		SourcePath:   evidence.SourcePath,
		System:       evidence.KeySystem,
		Version:      evidence.KeyVersion,
	}
	family.FamilyID = SemanticInventoryFamilyID(family)
	return SemanticCoverageReference{Kind: SemanticCoverageFamilyReference, BindingID: evidence.BindingID, Family: family}, true
}

func semanticCoverageReferenceKey(reference SemanticCoverageReference) string {
	if reference.Kind == SemanticCoverageFamilyReference {
		return strings.Join([]string{
			string(reference.Kind), reference.Family.BindingID, reference.Family.SourceKind,
			reference.Family.ResourceType, reference.Family.SourcePath, reference.Family.System, reference.Family.Version,
		}, "\x00")
	}
	return strings.Join([]string{string(reference.Kind), reference.ConceptID, reference.BindingID}, "\x00")
}

func semanticCoverageEvidenceHasMixedChoices(evidence []SemanticCoverageEmissionEvidence) bool {
	var previousChoice string
	hasChoice := false
	for _, item := range evidence {
		if item.Status != "SUPPORTED" {
			continue
		}
		choice := item.ChoiceArm + "\x00" + item.ValueSelector
		if hasChoice && previousChoice != choice {
			return true
		}
		previousChoice = choice
		hasChoice = true
	}
	return false
}

func semanticCoverageCodingChildren(
	concept semanticCoverageOccurrence,
	all []semanticCoverageOccurrence,
	byOwner map[string]semanticCoverageOccurrence,
) []semanticCoverageOccurrence {
	prefix := concept.OwnerPath + ".coding["
	children := make([]semanticCoverageOccurrence, 0)
	for _, occurrence := range all {
		if occurrence.Kind != SemanticCoverageCoding || !strings.HasPrefix(occurrence.OwnerPath, prefix) {
			continue
		}
		if child, ok := byOwner[occurrence.OwnerPath]; ok && strings.HasPrefix(child.CanonicalPath, concept.CanonicalPath+".coding[") {
			children = append(children, child)
		}
	}
	return children
}

func semanticCoverageHasUnrepresentedConceptFacets(occurrence semanticCoverageOccurrence) bool {
	for facetPath, presences := range occurrence.Facets {
		if semanticCoverageFacetMember(facetPath) == "coding" {
			continue
		}
		if presences[SemanticCoveragePopulated] > 0 || presences[SemanticCoverageBlank] > 0 {
			return true
		}
	}
	return false
}

func semanticCoverageHasUnrepresentedCodingFacets(occurrence semanticCoverageOccurrence) bool {
	for facetPath, presences := range occurrence.Facets {
		switch semanticCoverageFacetMember(facetPath) {
		case "system", "version", "code", "display":
			continue
		}
		if presences[SemanticCoveragePopulated] > 0 || presences[SemanticCoverageBlank] > 0 {
			return true
		}
	}
	return false
}

func semanticCoverageOccurrenceReason(
	occurrence semanticCoverageOccurrence,
	evidenceByKeyOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByOwner map[string][]SemanticCoverageEmissionEvidence,
	evidenceByValueOwner map[string][]SemanticCoverageEmissionEvidence,
) SemanticCoverageReason {
	ownerEvidence := evidenceByKeyOwner[occurrence.OwnerPath]
	if occurrence.Kind == SemanticCoverageIdentifier {
		ownerEvidence = evidenceByOwner[occurrence.OwnerPath]
	} else if occurrence.Kind == SemanticCoverageExtensionValue || occurrence.Kind == SemanticCoveragePrimitiveCode {
		ownerEvidence = evidenceByValueOwner[occurrence.OwnerPath]
	}
	if semanticCoverageEvidenceHasMixedChoices(ownerEvidence) {
		return SemanticCoverageReasonMixedChoice
	}
	for _, evidence := range ownerEvidence {
		switch evidence.Status {
		case "UNRESOLVED_SYSTEM":
			return SemanticCoverageReasonMissingSystem
		case "UNRESOLVED_CODE":
			return SemanticCoverageReasonMissingCode
		case "MIXED_CHOICE":
			return SemanticCoverageReasonMixedChoice
		case SemanticStatusUnsupportedValueProjection, "UNRESOLVED_VALUE_TYPE":
			return SemanticCoverageReasonUnsupportedValueProjection
		}
	}
	if occurrence.Kind == SemanticCoverageCodeableConcept {
		if occurrence.Text != "" && !occurrence.HasCoding {
			return SemanticCoverageReasonTextOnly
		}
		if semanticCoverageHasUnrepresentedConceptFacets(occurrence) {
			return SemanticCoverageReasonUnrepresentedMetadata
		}
	}
	if occurrence.Kind == SemanticCoverageUnknownStructure {
		return SemanticCoverageReasonUnknownSchemaMember
	}
	if occurrence.Kind == SemanticCoveragePrimitiveCode {
		return SemanticCoverageReasonUnknownCodeClassification
	}
	if occurrence.Kind == SemanticCoverageIdentifier && !occurrence.HasValue {
		return SemanticCoverageReasonMissingIdentifierValue
	}
	if occurrence.Kind == SemanticCoverageCoding && semanticCoverageHasUnrepresentedCodingFacets(occurrence) {
		return SemanticCoverageReasonUnrepresentedMetadata
	}
	if strings.TrimSpace(occurrence.System) == "" && occurrence.Kind == SemanticCoverageCoding {
		return SemanticCoverageReasonMissingSystem
	}
	if strings.TrimSpace(occurrence.Code) == "" && occurrence.Kind == SemanticCoverageCoding {
		return SemanticCoverageReasonMissingCode
	}
	if occurrence.Presence == SemanticCoverageBlank {
		return SemanticCoverageReasonUnrepresentedMetadata
	}
	if occurrence.Presence != SemanticCoveragePopulated {
		return SemanticCoverageReasonIncompleteSource
	}
	return SemanticCoverageReasonUnsupportedSelector
}

func appendSemanticCoverageReference(aggregate *SemanticCoveragePathAggregate, reference SemanticCoverageReference) {
	for _, existing := range aggregate.References {
		if existing.Kind == reference.Kind && existing.ConceptID == reference.ConceptID && existing.BindingID == reference.BindingID && existing.Family == reference.Family && existing.CandidatePath == reference.CandidatePath && existing.ResourceType == reference.ResourceType {
			return
		}
	}
	aggregate.References = append(aggregate.References, reference)
}

func cloneSemanticCoveragePresenceCounts(source map[SemanticCoveragePresence]int64) map[SemanticCoveragePresence]int64 {
	clone := make(map[SemanticCoveragePresence]int64, len(source))
	for presence, count := range source {
		clone[presence] = count
	}
	return clone
}

func hasSemanticCoverageGap(gaps []SemanticCoverageCensusGap, target SemanticCoverageCensusGap) bool {
	for _, gap := range gaps {
		if gap == target {
			return true
		}
	}
	return false
}

func semanticCoverageOccurrenceCount(counts SemanticCoverageCounts) int64 {
	return counts.CodeableConceptOwners + counts.CodingOccurrences + counts.IdentifierOccurrences +
		counts.ExtensionValueOccurrences + counts.PrimitiveCodeOccurrences + counts.UnknownStructureOccurrences
}
