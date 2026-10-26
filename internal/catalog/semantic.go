package catalog

// This file records observed FHIR concept candidates at ingestion time. It is
// deliberately a bounded catalog operation: it never decides clinical
// equivalence and it never removes the raw field profile when a candidate is
// unresolved or mixed.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	SemanticObservationSchemaVersion         = 4
	maxSemanticObservations                  = 512
	maxSemanticExamples                      = 32
	maxSemanticExampleBytes                  = 256
	SemanticStatusUnsupportedValueProjection = "UNSUPPORTED_VALUE_PROJECTION"
)

func (p *Profiler) observeSemanticObservations(payload map[string]any, sourceID string, sink SemanticInventoryObservationSink) error {
	return p.observeSemanticObservationsWithCoverage(payload, sourceID, "", sink, nil, nil, nil)
}

func (p *Profiler) observeSemanticObservationsWithCoverage(
	payload map[string]any,
	sourceID string,
	schemaIdentity string,
	sink SemanticInventoryObservationSink,
	emissionSink SemanticCoverageEmissionSink,
	coverageSink SemanticCoverageSourceSink,
	rawPaths map[string]struct{},
) error {
	ordinal := 0
	emissions := make([]SemanticCoverageEmissionEvidence, 0)
	emissionsTruncated := false
	emit := func(observation SemanticObservation, examples []any, evidence SemanticCoverageEmissionEvidence) {
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
	profile := semanticProfileForPayload(payload)
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
			DatasetGeneration: NormalizeDatasetGeneration(p.datasetGeneration),
			AuthResourcePath:  p.authResourcePath,
			SchemaIdentity:    schemaIdentity,
			SourceKind:        semanticInventorySourceKind(p.semanticSourceKind),
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
	return p.observeSchemaSemantics(index, payload, profile, func(observation SemanticObservation, examples []any) {
		if sourceID != "" && sink != nil {
			sink(p.semanticInventoryContribution(sourceID, ordinal, observation, examples))
			ordinal++
		}
	})
}

type semanticChoiceValue struct {
	Arm                   string
	OwnerPath             string
	Selector              string
	FallbackSelectors     []string
	FallbackOnBlankString bool
	Type                  string
	Status                string
	Value                 any
}

func semanticExampleValues(value semanticChoiceValue) []any {
	object, ok := value.Value.(map[string]any)
	if !ok {
		return []any{value.Value}
	}
	if value.FallbackOnBlankString {
		primary := semanticValueAtPath(object, semanticValuePathWithinArm(value.Selector, value.Arm))
		if text, ok := primary.(string); ok && strings.TrimSpace(text) != "" {
			return []any{text}
		}
		var examples []any
		for _, selector := range value.FallbackSelectors {
			examples = append(examples, semanticValuesAtPath(object, semanticValuePathWithinArm(selector, value.Arm))...)
		}
		return examples
	}
	// Keep a bounded scalar example for structured FHIR choice values while
	// preserving the arm and selector in the observation itself.
	path := strings.TrimPrefix(value.Selector, value.Arm+".")
	if path != "" {
		return []any{semanticValueAtPath(object, path)}
	}
	return []any{value.Value}
}

func semanticValuePathWithinArm(path, arm string) string {
	path, arm = strings.TrimSpace(path), strings.TrimSpace(arm)
	if arm == "" {
		return path
	}
	prefix := arm + "."
	if index := strings.LastIndex(path, prefix); index >= 0 {
		return path[index+len(prefix):]
	}
	return strings.TrimPrefix(path, prefix)
}

func semanticValueAtPath(value any, path string) any {
	current := value
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSuffix(part, "[]")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func semanticValuesAtPath(value any, path string) []any {
	parts := strings.Split(path, ".")
	var walk func(any, int) []any
	walk = func(current any, index int) []any {
		if index == len(parts) {
			return []any{current}
		}
		part := parts[index]
		array := strings.HasSuffix(part, "[]")
		name := strings.TrimSuffix(part, "[]")
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next := object[name]
		if !array {
			return walk(next, index+1)
		}
		items, ok := next.([]any)
		if !ok {
			return nil
		}
		var values []any
		for _, item := range items {
			values = append(values, walk(item, index+1)...)
		}
		return values
	}
	return walk(value, 0)
}

func semanticProjectionStatus(value map[string]any, path string) string {
	var current any = value
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return SemanticStatusUnsupportedValueProjection
		}
		current, ok = object[part]
		if !ok || current == nil {
			return SemanticStatusUnsupportedValueProjection
		}
	}
	if text, ok := current.(string); ok && strings.TrimSpace(text) == "" {
		return SemanticStatusUnsupportedValueProjection
	}
	return "SUPPORTED"
}

func semanticGeneratedValueType(resourceType, path string, value any, fallback string) string {
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, path)
	if ok && metadata.Primitive != fhirschema.PrimitiveUnknown {
		return string(metadata.Primitive)
	}
	// Extension value[x] arms are resolved against the generated Extension
	// definition when the containing resource path does not expose the arm.
	if resourceType != "Extension" {
		metadata, ok = fhirschema.ResolveTerminalScalarMetadata("Extension", path)
		if ok && metadata.Primitive != fhirschema.PrimitiveUnknown {
			return string(metadata.Primitive)
		}
	}
	return fallback
}

func semanticObservedUnits(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	units := make([]string, 0, 3)
	for _, key := range []string{"unit", "code", "system"} {
		if text := strings.TrimSpace(stringValue(object[key])); text != "" {
			units = append(units, text)
		}
	}
	return units
}

func semanticProfileForPayload(payload map[string]any) string {
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		return ""
	}
	profiles := []string{}
	if values, ok := meta["profile"].([]any); ok {
		for _, value := range values {
			if text, valid := safeSemanticExample(value); valid {
				profiles = append(profiles, text)
			}
		}
	}
	if text, valid := safeSemanticExample(meta["profile"]); valid {
		profiles = append(profiles, text)
	}
	sort.Strings(profiles)
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0]
}

func semanticCodings(value map[string]any) []map[string]any {
	items, _ := value["coding"].([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if coding, ok := item.(map[string]any); ok {
			result = append(result, coding)
		}
	}
	return result
}

func semanticValueType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "number"
	default:
		return "string"
	}
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

func (s *fieldCatalogStats) addSemanticObservation(observation SemanticObservation, examples []any) {
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
		observation.SchemaVersion = SemanticObservationSchemaVersion
		if observation.Completeness == "" {
			observation.Completeness = SemanticComplete
		}
		stat = &semanticObservationStats{observation: observation, exampleSet: map[string]struct{}{}, unitSet: map[string]struct{}{}}
		s.semanticObservations[key] = stat
	}
	stat.observation.Population++
	for _, unit := range observation.ObservedUnits {
		stat.unitSet[unit] = struct{}{}
	}
	for _, value := range examples {
		if text, valid := safeSemanticExample(value); valid {
			addBoundedSemanticExample(stat, text)
		}
	}
}

func semanticObservationKey(observation SemanticObservation) string {
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

func safeSemanticExample(value any) (string, bool) {
	var text string
	switch typed := value.(type) {
	case string:
		text = strings.TrimSpace(typed)
	case bool:
		text = strconv.FormatBool(typed)
	case int:
		text = strconv.Itoa(typed)
	case int8, int16, int32, int64:
		text = strconv.FormatInt(reflectInt64(typed), 10)
	case uint, uint8, uint16, uint32, uint64:
		text = strconv.FormatUint(reflectUint64(typed), 10)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return "", false
		}
		text = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", false
		}
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		text = typed.String()
	default:
		return "", false
	}
	if text == "" || len(text) > maxSemanticExampleBytes || !utf8.ValidString(text) {
		return "", false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return text, true
}

// These helpers avoid reflection while supporting every integer type in the
// untyped JSON fixture values used by the profiler.
func reflectInt64(value any) int64 {
	switch v := value.(type) {
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	}
	return 0
}
func reflectUint64(value any) uint64 {
	switch v := value.(type) {
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case uint:
		return uint64(v)
	}
	return 0
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

func semanticObservationValues(stat *semanticObservationStats) SemanticObservation {
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
		observation.Completeness = SemanticPartial
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
