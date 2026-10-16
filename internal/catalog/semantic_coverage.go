package catalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

type SemanticCoverageOccurrenceKind string

const (
	SemanticCoverageCodeableConcept SemanticCoverageOccurrenceKind = "CODEABLE_CONCEPT"
	SemanticCoverageCoding          SemanticCoverageOccurrenceKind = "CODING"
)

type SemanticCoveragePresence string

const (
	SemanticCoverageNull      SemanticCoveragePresence = "NULL"
	SemanticCoverageEmpty     SemanticCoveragePresence = "EMPTY"
	SemanticCoverageBlank     SemanticCoveragePresence = "BLANK"
	SemanticCoveragePopulated SemanticCoveragePresence = "POPULATED"
)

type SemanticCoverageCounts struct {
	CodeableConceptOwners int64
	CodingOccurrences     int64
}

// SemanticCoveragePathAggregate keeps the durable census shape aggregated by
// generated canonical path. Concrete owner paths are bounded witnesses for
// diagnostics and tests; source values are never retained.
type SemanticCoveragePathAggregate struct {
	CanonicalPath      string
	Kind               SemanticCoverageOccurrenceKind
	OccurrenceCount    int64
	OwnerCount         int64
	CodingCount        int64
	PresenceCounts     map[SemanticCoveragePresence]int64
	OwnerPathWitnesses []string
	WitnessesTruncated bool
}

type SemanticCoverageCensus struct {
	ResourceType string
	SourceID     string
	Counts       SemanticCoverageCounts
	Paths        []SemanticCoveragePathAggregate
	Diagnostics  []fhirsemantic.WalkDiagnostic
}

const semanticCoverageMaxOwnerPathWitnesses = 16

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
	}
	aggregates := make(map[string]*SemanticCoveragePathAggregate)
	diagnostics, err := fhirsemantic.WalkMembers(index, schema.DefinitionName(resourceType), payload, func(fact fhirsemantic.MemberFact) error {
		if fact.Null || fact.Repeated && !fact.ArrayItem {
			return nil
		}
		kind := SemanticCoverageOccurrenceKind("")
		switch fact.ReferencedType {
		case "CodeableConcept":
			kind = SemanticCoverageCodeableConcept
		case "Coding":
			kind = SemanticCoverageCoding
		default:
			return nil
		}
		presence := semanticCoveragePresence(fact.RawJSON)
		key := string(kind) + "\x00" + fact.CanonicalPath
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &SemanticCoveragePathAggregate{
				CanonicalPath:      fact.CanonicalPath,
				Kind:               kind,
				PresenceCounts:     make(map[SemanticCoveragePresence]int64),
				OwnerPathWitnesses: make([]string, 0, semanticCoverageMaxOwnerPathWitnesses),
			}
			aggregates[key] = aggregate
		}
		aggregate.OccurrenceCount++
		aggregate.PresenceCounts[presence]++
		if len(aggregate.OwnerPathWitnesses) < semanticCoverageMaxOwnerPathWitnesses {
			aggregate.OwnerPathWitnesses = append(aggregate.OwnerPathWitnesses, fact.OwnerPath)
		} else {
			aggregate.WitnessesTruncated = true
		}
		switch kind {
		case SemanticCoverageCodeableConcept:
			census.Counts.CodeableConceptOwners++
			aggregate.OwnerCount++
		case SemanticCoverageCoding:
			census.Counts.CodingOccurrences++
			aggregate.CodingCount++
		}
		return nil
	})
	if err != nil {
		return SemanticCoverageCensus{}, fmt.Errorf("census semantic coverage for %q source %q: %w", resourceType, sourceID, err)
	}
	census.Diagnostics = diagnostics
	census.Paths = make([]SemanticCoveragePathAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		census.Paths = append(census.Paths, *aggregate)
	}
	sort.Slice(census.Paths, func(i, j int) bool {
		if census.Paths[i].CanonicalPath != census.Paths[j].CanonicalPath {
			return census.Paths[i].CanonicalPath < census.Paths[j].CanonicalPath
		}
		return census.Paths[i].Kind < census.Paths[j].Kind
	})
	return census, nil
}

func semanticCoveragePresence(value json.RawMessage) SemanticCoveragePresence {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return SemanticCoveragePopulated
	}
	switch value := decoded.(type) {
	case nil:
		return SemanticCoverageNull
	case string:
		if strings.TrimSpace(value) == "" {
			return SemanticCoverageBlank
		}
	case []any:
		if len(value) == 0 {
			return SemanticCoverageEmpty
		}
	case map[string]any:
		if len(value) == 0 {
			return SemanticCoverageEmpty
		}
	}
	return SemanticCoveragePopulated
}
