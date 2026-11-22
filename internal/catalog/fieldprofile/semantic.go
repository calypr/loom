package fieldprofile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog/codedconcept"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	maxSemanticObservations = 512
	maxSemanticExamples     = 32
)

func (p *Profiler) observeSemanticObservations(payload map[string]any, sourceID string, sink semanticinventory.ObservationSink) error {
	return p.observeSemanticObservationsWithCoverage(payload, sourceID, "", sink, nil, nil, nil)
}

func (p *Profiler) observeSemanticObservationsWithCoverage(
	payload map[string]any,
	sourceID string,
	schemaIdentity string,
	sink semanticinventory.ObservationSink,
	emissionSink SemanticCoverageEmissionSink,
	coverageSink SemanticCoverageSourceSink,
	rawPaths map[string]struct{},
) error {
	ordinal := 0
	emissions := make([]SemanticCoverageEmissionEvidence, 0)
	emissionsTruncated := false
	emit := func(observation codedconcept.Observation, examples []any, evidence SemanticCoverageEmissionEvidence) {
		if sourceID != "" && sink != nil {
			sink(p.semanticInventoryContribution(sourceID, ordinal, observation, examples))
			ordinal++
		}
		if emissionSink != nil {
			emissionSink(evidence)
		}
		if coverageSink != nil {
			if len(emissions) < SemanticCoverageMaxReconciliationItemsPerSource {
				emissions = append(emissions, evidence)
			} else {
				emissionsTruncated = true
			}
		}
	}
	profile := codedconcept.ProfileForPayload(payload)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated FHIR schema for semantic observations: %w", err)
	}
	if coverageSink != nil {
		if sourceID == "" {
			return fmt.Errorf("semantic coverage source ID is required")
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode payload for semantic coverage census: %w", err)
		}
		census, err := CensusSemanticCoverage(index, p.resourceType, sourceID, raw)
		if err != nil {
			return err
		}
		census.Identity = SemanticCoverageSourceIdentity{
			Project:           p.project,
			DatasetGeneration: strings.TrimSpace(p.datasetGeneration),
			AuthResourcePath:  p.authResourcePath,
			SchemaIdentity:    schemaIdentity,
			SourceKind:        semanticinventory.SourceKind(p.semanticSourceKind),
			SourceID:          sourceID,
			ResourceType:      p.resourceType,
		}
		if err := p.observeSchemaSemanticsWithEvidence(index, payload, profile, sourceID, emit); err != nil {
			return fmt.Errorf("walk semantic observations for %q source %q: %w", p.resourceType, sourceID, err)
		}
		if emissionsTruncated {
			census.EvidenceTruncated = true
			if !hasSemanticCoverageGap(census.IncompleteGaps, SemanticCoverageGapEmissionEvidence) {
				census.IncompleteGaps = append(census.IncompleteGaps, SemanticCoverageGapEmissionEvidence)
			}
		}
		contribution, err := ReconcileSemanticCoverage(census, emissions, rawPaths)
		if err != nil {
			return err
		}
		if err := coverageSink(contribution); err != nil {
			return fmt.Errorf("write semantic coverage source contribution: %w", err)
		}
		return nil
	}
	return p.observeSchemaSemantics(index, payload, profile, func(observation codedconcept.Observation, examples []any) {
		if sourceID != "" && sink != nil {
			sink(p.semanticInventoryContribution(sourceID, ordinal, observation, examples))
			ordinal++
		}
	})
}

func (p *Profiler) ensureSemanticStat(path string) *fieldCatalogStats {
	if p.semanticOnly {
		return nil
	}
	stat, ok := p.stats[path]
	if !ok {
		if p.limits.MaxFields > 0 && len(p.stats) >= p.limits.MaxFields {
			p.truncated = true
			return nil
		}
		if !p.reserve(fieldStatsWeight(path, fieldKindObject)) {
			p.truncated = true
			return nil
		}
		stat = &fieldCatalogStats{path: path, kind: fieldKindObject, distinctSet: map[string]struct{}{}, pivotColumnSet: map[string]struct{}{}, extensionValueSet: map[string]struct{}{}, semanticObservations: map[string]*semanticObservationStats{}}
		p.stats[path] = stat
	}
	if stat.semanticObservations == nil {
		stat.semanticObservations = map[string]*semanticObservationStats{}
	}
	return stat
}

func (s *fieldCatalogStats) addSemanticObservation(observation codedconcept.Observation, examples []any) {
	if strings.TrimSpace(observation.Source.Canonical) == "" || strings.TrimSpace(observation.Value.Selector) == "" {
		return
	}
	if s.semanticObservations == nil {
		s.semanticObservations = map[string]*semanticObservationStats{}
	}
	key := semanticObservationKey(observation)
	stat, ok := s.semanticObservations[key]
	if !ok {
		if len(s.semanticObservations) >= maxSemanticObservations {
			return
		}
		observation.SchemaVersion = codedconcept.ObservationSchemaVersion
		if observation.Completeness == "" {
			observation.Completeness = codedconcept.Complete
		}
		stat = &semanticObservationStats{observation: observation, exampleSet: map[string]struct{}{}, unitSet: map[string]struct{}{}}
		s.semanticObservations[key] = stat
	}
	stat.observation.Population++
	for _, unit := range observation.ObservedUnits {
		stat.unitSet[unit] = struct{}{}
	}
	for _, value := range examples {
		if text, valid := codedconcept.SafeExample(value); valid {
			addBoundedSemanticExample(stat, text)
		}
	}
}

func semanticObservationKey(observation codedconcept.Observation) string {
	return strings.Join([]string{
		observation.Source.Canonical, observation.Source.Profile, observation.Source.Path,
		observation.OwningScope, strings.Join(observation.ExtensionURLPath, "\x1f"),
		observation.Key.Selector, observation.Key.System, observation.Key.Version, observation.Key.Code, observation.Key.Display,
		observation.Value.Selector, observation.Value.Type,
		strings.Join(observation.Value.FallbackSelectors, "\x1f"), strconv.FormatBool(observation.Value.FallbackOnBlankString),
		observation.ChoiceArm, observation.LogicalType,
		observation.RuleHint, observation.RuleVersion,
	}, "\x00")
}

func addBoundedSemanticExample(stat *semanticObservationStats, value string) {
	if _, exists := stat.exampleSet[value]; exists {
		return
	}
	if len(stat.exampleSet) < maxSemanticExamples {
		stat.exampleSet[value] = struct{}{}
		return
	}
	stat.observation.ExamplesTruncated = true
	largest := ""
	for candidate := range stat.exampleSet {
		if largest == "" || candidate > largest {
			largest = candidate
		}
	}
	if value < largest {
		delete(stat.exampleSet, largest)
		stat.exampleSet[value] = struct{}{}
	}
}

func semanticObservationValues(stat *semanticObservationStats) codedconcept.Observation {
	observation := stat.observation
	observation.Examples = make([]string, 0, len(stat.exampleSet))
	for value := range stat.exampleSet {
		observation.Examples = append(observation.Examples, value)
	}
	sort.Strings(observation.Examples)
	observation.ObservedUnits = make([]string, 0, len(stat.unitSet))
	for unit := range stat.unitSet {
		observation.ObservedUnits = append(observation.ObservedUnits, unit)
	}
	sort.Strings(observation.ObservedUnits)
	if observation.ExamplesTruncated {
		observation.Completeness = codedconcept.Partial
	}
	return observation
}

func mergeSemanticObservations(destination, source *fieldCatalogStats) {
	if source == nil {
		return
	}
	if destination.semanticObservations == nil {
		destination.semanticObservations = map[string]*semanticObservationStats{}
	}
	for key, incoming := range source.semanticObservations {
		current := destination.semanticObservations[key]
		newObservation := false
		if current == nil {
			if len(destination.semanticObservations) >= maxSemanticObservations {
				continue
			}
			current = &semanticObservationStats{observation: incoming.observation, exampleSet: map[string]struct{}{}, unitSet: map[string]struct{}{}}
			destination.semanticObservations[key] = current
			newObservation = true
		}
		if !newObservation {
			current.observation.Population += incoming.observation.Population
		}
		current.observation.ExamplesTruncated = current.observation.ExamplesTruncated || incoming.observation.ExamplesTruncated
		for value := range incoming.exampleSet {
			addBoundedSemanticExample(current, value)
		}
		for unit := range incoming.unitSet {
			current.unitSet[unit] = struct{}{}
		}
	}
}

func (p *Profiler) semanticInventoryContribution(sourceID string, ordinal int, observation codedconcept.Observation, examples []any) semanticinventory.Contribution {
	source := semanticinventory.ContributionSource{
		Project: p.project, DatasetGeneration: p.datasetGeneration, BuildID: p.semanticInventoryBuildID,
		AuthResourcePath: p.authResourcePath, ResourceType: p.resourceType, Kind: p.semanticSourceKind,
	}
	return source.Contribution(sourceID, ordinal, observation, examples)
}
