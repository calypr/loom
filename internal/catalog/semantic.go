package catalog

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxSemanticObservations = 512
	maxSemanticExamples     = 32
	maxSemanticExampleBytes = 256
)

// addSemanticObservation records a bounded, correlated semantic fact. The
// identity is derived from every source/key/value/rule component, so two
// distinct mappings can never be merged merely because they share a label.
func (s *fieldCatalogStats) addSemanticObservation(observation SemanticObservation, examples []any) {
	if strings.TrimSpace(observation.Source.Canonical) == "" ||
		strings.TrimSpace(observation.Value.Selector) == "" ||
		strings.TrimSpace(observation.Cardinality) == "" {
		return
	}
	if s.semanticObservations == nil {
		s.semanticObservations = make(map[string]*semanticObservationStats)
	}
	key := semanticObservationKey(observation)
	stat, ok := s.semanticObservations[key]
	if !ok {
		if len(s.semanticObservations) >= maxSemanticObservations {
			return
		}
		observation.SchemaVersion = SemanticObservationSchemaVersion
		stat = &semanticObservationStats{observation: observation, exampleSet: make(map[string]struct{})}
		s.semanticObservations[key] = stat
	}
	stat.observation.Population++
	for _, value := range examples {
		if text, ok := safeSemanticExample(value); ok {
			if _, exists := stat.exampleSet[text]; exists {
				continue
			}
			addBoundedSemanticExample(stat, text)
		}
	}
}

func semanticObservationKey(observation SemanticObservation) string {
	return strings.Join([]string{
		observation.Source.Canonical,
		observation.Source.Type,
		observation.Source.Profile,
		observation.Source.Path,
		observation.Key.Selector,
		observation.Key.System,
		observation.Key.Code,
		observation.Key.Display,
		observation.Value.Selector,
		observation.Value.Type,
		observation.Cardinality,
		observation.RuleHint,
		observation.RuleVersion,
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
	case int8:
		text = strconv.FormatInt(int64(typed), 10)
	case int16:
		text = strconv.FormatInt(int64(typed), 10)
	case int32:
		text = strconv.FormatInt(int64(typed), 10)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case uint:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint8:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint16:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint32:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		text = strconv.FormatUint(typed, 10)
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

func semanticExamples(stats *semanticObservationStats) []string {
	examples := make([]string, 0, len(stats.exampleSet))
	for value := range stats.exampleSet {
		examples = append(examples, value)
	}
	sort.Strings(examples)
	if len(examples) > maxSemanticExamples {
		examples = examples[:maxSemanticExamples]
		stats.observation.ExamplesTruncated = true
	}
	return examples
}

func mergeSemanticObservation(destination *fieldCatalogStats, source *semanticObservationStats) {
	if source == nil {
		return
	}
	if destination.semanticObservations == nil {
		destination.semanticObservations = make(map[string]*semanticObservationStats)
	}
	key := semanticObservationKey(source.observation)
	stat := destination.semanticObservations[key]
	if stat == nil {
		if len(destination.semanticObservations) >= maxSemanticObservations {
			return
		}
		stat = &semanticObservationStats{observation: source.observation, exampleSet: make(map[string]struct{})}
		destination.semanticObservations[key] = stat
	}
	stat.observation.Population += source.observation.Population
	stat.observation.ExamplesTruncated = stat.observation.ExamplesTruncated || source.observation.ExamplesTruncated
	for value := range source.exampleSet {
		if _, exists := stat.exampleSet[value]; exists {
			continue
		}
		addBoundedSemanticExample(stat, value)
	}
}

// addBoundedSemanticExample retains the lexicographically smallest examples,
// making bounded aggregation independent of worker/document arrival order.
func addBoundedSemanticExample(stat *semanticObservationStats, value string) {
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

func semanticObservationSource(resourceType, path, profile string) SemanticObservationSource {
	path = strings.Trim(path, ".")
	canonical := resourceType
	if path != "" {
		canonical += "." + path
	}
	return SemanticObservationSource{Canonical: canonical, Type: resourceType, Profile: profile, Path: path}
}

func profileForPayload(payload map[string]any) string {
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		return ""
	}
	profiles := make([]string, 0)
	if raw, ok := meta["profile"].([]any); ok {
		for _, value := range raw {
			if text, ok := safeSemanticExample(value); ok {
				profiles = append(profiles, text)
			}
		}
	}
	if value, ok := meta["profile"].(string); ok {
		if text, valid := safeSemanticExample(value); valid {
			profiles = append(profiles, text)
		}
	}
	if len(profiles) == 0 {
		return ""
	}
	sort.Strings(profiles)
	return profiles[0]
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

func semanticObservationValueExamples(payload map[string]any, selector string) []any {
	accessor := make([]pathStep, 0)
	path := strings.Trim(selector, ".")
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}
		iterate := strings.HasSuffix(segment, "[]")
		segment = strings.TrimSuffix(segment, "[]")
		accessor = append(accessor, pathStep{field: segment, iterateArray: iterate})
	}
	values, ok := extractAccessorValues(payload, accessor)
	if !ok {
		return nil
	}
	return values
}

func (p *Profiler) observeSemanticObservations(payload map[string]any) {
	profile := profileForPayload(payload)
	if p.resourceType == "Observation" {
		p.observeObservationSemantics(payload, profile)
	}
	walkSemanticValue(payload, "", p, profile)
}

func (p *Profiler) observeObservationSemantics(payload map[string]any, profile string) {
	code, ok := payload["code"].(map[string]any)
	if ok {
		valueSelector := observationValueSelectorFromPayload(payload)
		if valueSelector != "" {
			for _, coding := range codeableCodings(code) {
				p.emitSemantic(code, "code", coding, valueSelector, payload, "OBSERVATION_CODE_VALUE", profile)
			}
			if len(codeableCodings(code)) == 0 {
				p.emitSemantic(code, "code", nil, valueSelector, payload, "OBSERVATION_CODE_VALUE", profile)
			}
		}
	}
	components, _ := payload["component"].([]any)
	for _, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		code, ok := component["code"].(map[string]any)
		if !ok {
			continue
		}
		valueSelector := observationValueSelectorFromPayload(component)
		if valueSelector == "" {
			continue
		}
		for _, coding := range codeableCodings(code) {
			p.emitSemantic(component, "component[].code", coding, "component[]."+valueSelector, component, "OBSERVATION_COMPONENT_VALUE", profile)
		}
	}
}

func (p *Profiler) emitSemantic(container map[string]any, sourcePath string, coding map[string]any, valueSelector string, valueRoot map[string]any, rule string, profile string) {
	key := SemanticObservationKey{Selector: sourcePath + ".coding[]"}
	if coding != nil {
		key.System = stringValue(coding["system"])
		key.Code = stringValue(coding["code"])
		key.Display = stringValue(coding["display"])
	} else {
		key.Selector = sourcePath + ".text"
		key.Display = stringValue(container["text"])
	}
	valuePath := valueSelector
	if strings.HasPrefix(valuePath, "component[].") {
		valuePath = strings.TrimPrefix(valuePath, "component[].")
	}
	examples := semanticObservationValueExamples(valueRoot, valuePath)
	valueType := "string"
	if len(examples) > 0 {
		valueType = semanticValueType(examples[0])
	}
	path := sourcePath
	if strings.HasSuffix(sourcePath, ".code") {
		path = strings.TrimSuffix(sourcePath, ".code")
	}
	observation := SemanticObservation{
		Source:      semanticObservationSource(p.resourceType, path, profile),
		Key:         key,
		Value:       SemanticObservationValue{Selector: valueSelector, Type: valueType},
		Cardinality: semanticCardinality(sourcePath, valueSelector),
		RuleHint:    rule,
		RuleVersion: "1",
	}
	if stat := p.ensureSemanticStat(path); stat != nil {
		stat.addSemanticObservation(observation, examples)
	}
}

func walkSemanticValue(value any, path string, profiler *Profiler, profile string) {
	switch typed := value.(type) {
	case map[string]any:
		if strings.HasSuffix(path, "extension[]") {
			profiler.emitExtensionSemantic(typed, path, profile)
		}
		if isCodeableConceptShape(typed) && path != "code" && !strings.HasSuffix(path, ".code") {
			profiler.emitCodeableSemantic(typed, path, profile)
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
			walkSemanticValue(child, childPath, profiler, profile)
		}
	case []any:
		for _, item := range typed {
			if item != nil {
				walkSemanticValue(item, path, profiler, profile)
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
		Source:      semanticObservationSource(p.resourceType, path, profile),
		Key:         SemanticObservationKey{Selector: path + ".system", System: stringValue(value["system"])},
		Value:       SemanticObservationValue{Selector: path + ".value", Type: semanticValueType(raw)},
		Cardinality: semanticCardinality(path, path+".value"),
		RuleHint:    "IDENTIFIER_SYSTEM_VALUE", RuleVersion: "1",
	}
	if stat := p.ensureSemanticStat(path); stat != nil {
		stat.addSemanticObservation(observation, []any{raw})
	}
}

func (p *Profiler) emitCodeableSemantic(value map[string]any, path, profile string) {
	selector := path + ".coding[].display"
	examples := semanticObservationValueExamples(value, "coding[].display")
	if len(examples) == 0 {
		selector = path + ".text"
		examples = semanticObservationValueExamples(value, "text")
	}
	if len(examples) == 0 {
		return
	}
	codings := codeableCodings(value)
	if len(codings) == 0 {
		codings = []map[string]any{nil}
	}
	for _, coding := range codings {
		key := SemanticObservationKey{Selector: path + ".coding[]"}
		if coding == nil {
			key.Selector = path + ".text"
			key.Display = stringValue(value["text"])
		} else {
			key.System = stringValue(coding["system"])
			key.Code = stringValue(coding["code"])
			key.Display = stringValue(coding["display"])
		}
		observation := SemanticObservation{
			Source:      semanticObservationSource(p.resourceType, path, profile),
			Key:         key,
			Value:       SemanticObservationValue{Selector: selector, Type: semanticValueType(examples[0])},
			Cardinality: semanticCardinality(path, selector),
			RuleHint:    "CODEABLE_CONCEPT_VALUE", RuleVersion: "1",
		}
		if stat := p.ensureSemanticStat(path); stat != nil {
			stat.addSemanticObservation(observation, examples)
		}
	}
}

func (p *Profiler) emitExtensionSemantic(value map[string]any, path, profile string) {
	url := strings.TrimSpace(stringValue(value["url"]))
	if url == "" {
		return
	}
	for _, key := range sortedKeys(value) {
		if !strings.HasPrefix(key, "value") || key == "value" || value[key] == nil {
			continue
		}
		valuePath, valueType := extensionValueMapping(key, value[key])
		if valuePath == "" {
			continue
		}
		observation := SemanticObservation{
			Source:      semanticObservationSource(p.resourceType, path, profile),
			Key:         SemanticObservationKey{Selector: path + ".url", Display: url},
			Value:       SemanticObservationValue{Selector: path + "." + valuePath, Type: valueType},
			Cardinality: semanticCardinality(path, valuePath),
			RuleHint:    "EXTENSION_URL_VALUE", RuleVersion: "1",
		}
		if stat := p.ensureSemanticStat(path); stat != nil {
			stat.addSemanticObservation(observation, []any{value[key]})
		}
	}
}

func (p *Profiler) ensureSemanticStat(path string) *fieldCatalogStats {
	stat, ok := p.stats[path]
	if !ok {
		stat = &fieldCatalogStats{path: path, kind: fieldKindObject, distinctSet: make(map[string]struct{}), pivotColumnSet: make(map[string]struct{}), extensionValueSet: make(map[string]struct{}), semanticObservations: make(map[string]*semanticObservationStats)}
		p.stats[path] = stat
	}
	if stat.semanticObservations == nil {
		stat.semanticObservations = make(map[string]*semanticObservationStats)
	}
	return stat
}

func codeableCodings(value map[string]any) []map[string]any {
	raw, _ := value["coding"].([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if coding, ok := item.(map[string]any); ok {
			result = append(result, coding)
		}
	}
	return result
}

func semanticCardinality(source, value string) string {
	if strings.Contains(source, "[]") || strings.Contains(value, "[]") {
		return "repeated"
	}
	return "single"
}
