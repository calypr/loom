package catalog

// This file records observed FHIR concept candidates at ingestion time. It is
// deliberately a bounded catalog operation: it never decides clinical
// equivalence and it never removes the raw field profile when a candidate is
// unresolved or mixed.

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	SemanticObservationSchemaVersion = 2
	maxSemanticObservations          = 512
	maxSemanticExamples              = 32
	maxSemanticExampleBytes          = 256
)

func (p *Profiler) observeSemanticObservations(payload map[string]any) {
	profile := semanticProfileForPayload(payload)
	if p.resourceType == "Observation" {
		p.observeObservationSemantics(payload, profile)
	}
	walkSemanticValue(payload, "", nil, p, profile)
}

func (p *Profiler) observeObservationSemantics(payload map[string]any, profile string) {
	if code, ok := payload["code"].(map[string]any); ok {
		p.emitObservationCodeValue("", code, payload, profile)
	}
	components, _ := payload["component"].([]any)
	for _, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		code, ok := component["code"].(map[string]any)
		if ok {
			p.emitObservationCodeValue("component[]", code, component, profile)
		}
	}
}

func (p *Profiler) emitObservationCodeValue(owner string, code map[string]any, ownerValue map[string]any, profile string) {
	valueSelectors := semanticChoiceValues(p.resourceType, ownerValue)
	if len(valueSelectors) == 0 {
		return
	}
	codings := semanticCodings(code)
	if len(codings) == 0 {
		// Preserve an unresolved text/code candidate. A missing Coding is not
		// equivalent to a matching Coding and must remain visible.
		for _, value := range valueSelectors {
			p.emitSemantic(owner, code, nil, value, profile, "OBSERVATION_CODE_VALUE")
		}
		return
	}
	for _, coding := range codings {
		for _, value := range valueSelectors {
			p.emitSemantic(owner, code, coding, value, profile, "OBSERVATION_CODE_VALUE")
		}
	}
}

type semanticChoiceValue struct {
	Arm      string
	Selector string
	Value    any
}

func semanticChoiceValues(resourceType string, value map[string]any) []semanticChoiceValue {
	keys := make([]string, 0)
	for key, raw := range value {
		if strings.HasPrefix(key, "value") && key != "value" && raw != nil {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := make([]semanticChoiceValue, 0, len(keys))
	for _, key := range keys {
		selector, _ := semanticValuePathAndType(resourceType, key, value[key])
		if selector == "" {
			continue
		}
		result = append(result, semanticChoiceValue{Arm: key, Selector: selector, Value: value[key]})
	}
	return result
}

func (p *Profiler) emitSemantic(owner string, code map[string]any, coding map[string]any, value semanticChoiceValue, profile, rule string) {
	key := SemanticObservationKey{Selector: "code.coding[]"}
	if coding == nil {
		key.Selector = "code.text"
		key.Display = stringValue(code["text"])
	} else {
		key.System = stringValue(coding["system"])
		key.Code = stringValue(coding["code"])
		key.Display = stringValue(coding["display"])
	}
	ownerPath := strings.Trim(owner, ".")
	storagePath := ownerPath
	if storagePath == "" {
		// Root terminology/value observations belong to the code field. Keep
		// owning scope empty so the binding remains relative to the resource,
		// while retaining the catalog's existing field-path contract.
		storagePath = "code"
	}
	canonical := p.resourceType
	if storagePath != "" {
		canonical += "." + storagePath
	}
	_, valueType := semanticValuePathAndType(p.resourceType, value.Arm, value.Value)
	observedUnits := semanticObservedUnits(value.Value)
	status := "SUPPORTED"
	if strings.TrimSpace(key.System) == "" {
		status = "UNRESOLVED_SYSTEM"
	}
	if strings.TrimSpace(key.Code) == "" && coding != nil {
		status = "UNRESOLVED_CODE"
	}
	if coding == nil {
		status = "UNRESOLVED_BINDING"
	}
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source:        SemanticObservationSource{Canonical: canonical, Type: p.resourceType, Profile: profile, Path: storagePath},
		Key:           key,
		Value:         SemanticObservationValue{Selector: value.Selector, Type: valueType},
		OwningScope:   ownerPath,
		ChoiceArm:     value.Arm,
		LogicalType:   valueType,
		ObservedUnits: observedUnits,
		Completeness:  SemanticComplete,
		Status:        status,
		RuleHint:      rule,
		RuleVersion:   "2",
	}
	if stat := p.ensureSemanticStat(storagePath); stat != nil {
		stat.addSemanticObservation(observation, []any{semanticExampleValue(value)})
	}
}

func semanticExampleValue(value semanticChoiceValue) any {
	object, ok := value.Value.(map[string]any)
	if !ok {
		return value.Value
	}
	// Keep a bounded scalar example for structured FHIR choice values while
	// preserving the arm and selector in the observation itself.
	path := strings.TrimPrefix(value.Selector, value.Arm+".")
	if path != "" {
		parts := strings.Split(path, ".")
		current := any(object)
		for _, part := range parts {
			mapValue, mapOK := current.(map[string]any)
			if !mapOK {
				return value.Value
			}
			current = mapValue[part]
		}
		return current
	}
	return value.Value
}

func walkSemanticValue(value any, path string, ancestors []string, profiler *Profiler, profile string) {
	switch typed := value.(type) {
	case map[string]any:
		isExtension := strings.HasSuffix(path, "extension[]")
		if isExtension {
			profiler.emitExtensionSemantic(typed, path, ancestors, profile)
		}
		if strings.HasSuffix(path, "identifier[]") {
			profiler.emitIdentifierSemantic(typed, path, profile)
		}
		for _, key := range sortedKeys(typed) {
			child := typed[key]
			if child == nil {
				continue
			}
			childPath := appendPath(path, key, false)
			if _, ok := child.([]any); ok {
				childPath = appendPath(path, key, true)
			}
			nextAncestors := ancestors
			if isExtension && key == "extension" {
				if url := strings.TrimSpace(stringValue(typed["url"])); url != "" {
					nextAncestors = append(append([]string(nil), ancestors...), url)
				}
			}
			walkSemanticValue(child, childPath, nextAncestors, profiler, profile)
		}
	case []any:
		for _, item := range typed {
			if item != nil {
				walkSemanticValue(item, path, ancestors, profiler, profile)
			}
		}
	}
}

func (p *Profiler) emitIdentifierSemantic(value map[string]any, path, profile string) {
	raw, ok := value["value"]
	if !ok || raw == nil {
		return
	}
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source:        SemanticObservationSource{Canonical: p.resourceType + "." + path, Type: p.resourceType, Profile: profile, Path: path},
		Key:           SemanticObservationKey{Selector: path + ".system", System: stringValue(value["system"])},
		Value:         SemanticObservationValue{Selector: path + ".value", Type: semanticValueType(raw)},
		OwningScope:   path, LogicalType: semanticValueType(raw), Completeness: SemanticComplete,
		Status: "SUPPORTED", RuleHint: "IDENTIFIER_SYSTEM_VALUE", RuleVersion: "2",
	}
	if observation.Key.System == "" {
		observation.Status = "UNRESOLVED_SYSTEM"
	}
	if stat := p.ensureSemanticStat(path); stat != nil {
		stat.addSemanticObservation(observation, []any{raw})
	}
}

func (p *Profiler) emitExtensionSemantic(value map[string]any, path string, ancestors []string, profile string) {
	url := strings.TrimSpace(stringValue(value["url"]))
	if url == "" {
		return
	}
	for _, key := range sortedKeys(value) {
		if !strings.HasPrefix(key, "value") || key == "value" || value[key] == nil {
			continue
		}
		valuePath, valueType := semanticValuePathAndType(p.resourceType, key, value[key])
		if valuePath == "" {
			continue
		}
		status := "SUPPORTED"
		if valueType == "mixed" {
			status = "UNRESOLVED_VALUE_TYPE"
		}
		observation := SemanticObservation{
			SchemaVersion: SemanticObservationSchemaVersion,
			Source:        SemanticObservationSource{Canonical: p.resourceType + "." + path, Type: p.resourceType, Profile: profile, Path: path},
			Key:           SemanticObservationKey{Selector: path + ".url", Display: url},
			Value:         SemanticObservationValue{Selector: path + "." + valuePath, Type: valueType},
			OwningScope:   path, ExtensionURLPath: append(append([]string(nil), ancestors...), url), ChoiceArm: key,
			LogicalType: valueType, ObservedUnits: semanticObservedUnits(value[key]), Completeness: SemanticComplete,
			Status: status, RuleHint: "EXTENSION_URL_VALUE", RuleVersion: "2",
		}
		if stat := p.ensureSemanticStat(path); stat != nil {
			stat.addSemanticObservation(observation, []any{value[key]})
		}
	}
}

func semanticValuePathAndType(resourceType, path string, value any) (string, string) {
	if value == nil {
		return "", ""
	}
	if _, object := value.(map[string]any); object {
		// Complex choices retain the arm and any observed unit; the concrete
		// scalar path is resolved through generated FHIR metadata for known
		// datatypes rather than inferred from the JSON object shape.
		switch path {
		case "valueQuantity":
			valuePath := path + ".value"
			return valuePath, semanticGeneratedValueType(resourceType, valuePath, value, "decimal")
		case "valueCodeableConcept":
			valuePath := path + ".text"
			return valuePath, semanticGeneratedValueType(resourceType, valuePath, value, "string")
		case "valuePeriod":
			valuePath := path + ".start"
			return valuePath, semanticGeneratedValueType(resourceType, valuePath, value, "date_time")
		case "valueRange":
			valuePath := path + ".low.value"
			return valuePath, semanticGeneratedValueType(resourceType, valuePath, value, "decimal")
		case "valueRatio":
			valuePath := path + ".numerator.value"
			return valuePath, semanticGeneratedValueType(resourceType, valuePath, value, "decimal")
		default:
			return path, semanticGeneratedValueType(resourceType, path, value, "mixed")
		}
	}
	return path, semanticGeneratedValueType(resourceType, path, value, semanticValueType(value))
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
		observation.Key.Selector, observation.Key.System, observation.Key.Code, observation.Key.Display,
		observation.Value.Selector, observation.Value.Type, observation.ChoiceArm, observation.LogicalType,
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
