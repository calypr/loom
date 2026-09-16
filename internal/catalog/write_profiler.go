package catalog

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func NewShapePlanCacheWithLimit(maxPlans int) *ShapePlanCache {
	return NewShapePlanCacheWithLimits(maxPlans, 0)
}

func NewShapePlanCacheWithLimits(maxPlans, maxBytes int) *ShapePlanCache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRetainedBytes
	}
	return &ShapePlanCache{plans: make(map[string]*shapePlan), maxPlans: maxPlans, maxBytes: maxBytes, budget: newRetentionBudget(maxBytes)}
}

// NewProfilerForGenerationWithLimits constructs a profiler with explicit
// bounds for every write-side catalog structure.
func NewProfilerForGenerationWithLimits(project, datasetGeneration, authResourcePath, resourceType string, cache *ShapePlanCache, limits ProfileLimits) *Profiler {
	limits = limits.normalized()
	if cache == nil {
		cache = NewShapePlanCacheWithLimits(limits.MaxShapePlans, limits.MaxRetainedBytes)
	}
	return &Profiler{
		project:           project,
		datasetGeneration: NormalizeDatasetGeneration(datasetGeneration),
		authResourcePath:  authResourcePath,
		resourceType:      resourceType,
		limits:            limits,
		shapeCache:        cache,
		stats:             make(map[string]*fieldCatalogStats),
		budget:            cache.retentionBudget(limits.MaxRetainedBytes),
	}
}

func (p *Profiler) ObservePayload(payload map[string]any, timings map[string]float64) {
	if payload == nil {
		return
	}
	fingerprintStart := time.Now()
	fingerprint := shapeFingerprintForValue(payload)
	timings["field_shape_fingerprint"] += time.Since(fingerprintStart).Seconds()

	planStart := time.Now()
	plan := p.shapeCache.getOrBuild(fingerprint, payload)
	timings["field_shape_plan"] += time.Since(planStart).Seconds()

	observeStart := time.Now()
	for _, field := range plan.fields {
		values, ok := extractAccessorValues(payload, field.Accessor)
		if !ok {
			continue
		}
		stat := p.ensureStat(field)
		if stat == nil {
			continue
		}
		stat.docCount++
		if field.Kind == fieldKindArray {
			stat.maxItems = max(stat.maxItems, maxArrayItems(payload, field.Accessor))
		}
		switch field.Kind {
		case fieldKindScalar:
			for _, value := range values {
				if text, ok := scalarStringValue(value); ok {
					p.addDistinctWithLimits(stat, text)
				}
			}
		case fieldKindCodeableConcept:
			for _, value := range values {
				if cc, ok := value.(map[string]any); ok {
					for _, col := range codeableConceptColumns(cc) {
						p.addPivotColumnWithLimits(stat, col)
						p.addDistinctWithLimits(stat, col)
					}
				}
			}
		}
	}
	p.observeObservationCodePivot(payload)
	p.observeExtensionValues(payload)
	p.observeSemanticObservations(payload)
	timings["field_profile"] += time.Since(observeStart).Seconds()
}

// ErrProfilerIdentityMismatch reports an attempted merge between independently
// scoped catalog profilers. Combining those stats would let one project,
// generation, authorization path, or resource type claim observations from
// another scope.
var ErrProfilerIdentityMismatch = errors.New("catalog profiler identity mismatch")

type profilerIdentity struct {
	project           string
	datasetGeneration string
	authResourcePath  string
	resourceType      string
}

func (p *Profiler) normalizedIdentity() profilerIdentity {
	return profilerIdentity{
		project:           p.project,
		datasetGeneration: NormalizeDatasetGeneration(p.datasetGeneration),
		authResourcePath:  p.authResourcePath,
		resourceType:      p.resourceType,
	}
}

// Merge aggregates worker-local observations only when both profilers describe
// the same persisted catalog namespace. It validates every identity component
// before changing any statistics so a rejected merge is observationally a
// no-op for the destination profiler.
func (p *Profiler) Merge(other *Profiler) error {
	if p == nil || other == nil {
		return fmt.Errorf("%w: nil profiler", ErrProfilerIdentityMismatch)
	}
	identity := p.normalizedIdentity()
	otherIdentity := other.normalizedIdentity()
	if identity != otherIdentity {
		return fmt.Errorf(
			"%w: destination project=%q generation=%q auth_resource_path=%q resource_type=%q; source project=%q generation=%q auth_resource_path=%q resource_type=%q",
			ErrProfilerIdentityMismatch,
			identity.project,
			identity.datasetGeneration,
			identity.authResourcePath,
			identity.resourceType,
			otherIdentity.project,
			otherIdentity.datasetGeneration,
			otherIdentity.authResourcePath,
			otherIdentity.resourceType,
		)
	}
	if p.budget == other.budget && p.budget != nil {
		p.budget.release(other.retainedBytes)
		other.retainedBytes = 0
	}
	if p.stats == nil {
		p.stats = make(map[string]*fieldCatalogStats)
	}
	p.truncated = p.truncated || other.Truncated()
	for path, otherStat := range other.stats {
		stat, ok := p.stats[path]
		if !ok {
			if p.limits.MaxFields > 0 && len(p.stats) >= p.limits.MaxFields {
				p.truncated = true
				continue
			}
			if !p.reserve(fieldStatsWeight(otherStat.path, otherStat.kind)) {
				p.truncated = true
				continue
			}
			stat = &fieldCatalogStats{
				path:                  otherStat.path,
				kind:                  otherStat.kind,
				pivotCandidate:        otherStat.pivotCandidate,
				pivotKind:             otherStat.pivotKind,
				pivotFamily:           otherStat.pivotFamily,
				pivotColumnSelect:     otherStat.pivotColumnSelect,
				pivotValueSelect:      otherStat.pivotValueSelect,
				pivotItemSource:       otherStat.pivotItemSource,
				pivotItemResourceType: otherStat.pivotItemResourceType,
				pivotValueSelectors:   append([]string(nil), otherStat.pivotValueSelectors...),
				distinctSet:           make(map[string]struct{}),
				pivotColumnSet:        make(map[string]struct{}),
				extensionValueSet:     make(map[string]struct{}),
				semanticObservations:  make(map[string]*semanticObservationStats),
			}
			p.stats[path] = stat
		}
		stat.docCount += otherStat.docCount
		stat.maxItems = max(stat.maxItems, otherStat.maxItems)
		stat.distinctTruncated = stat.distinctTruncated || otherStat.distinctTruncated
		stat.setPivotDefaults(otherStat.pivotFamily, otherStat.pivotColumnSelect, otherStat.pivotValueSelect)
		stat.setPivotScope(otherStat.pivotItemSource, otherStat.pivotItemResourceType, otherStat.pivotValueSelectors)
		for _, value := range otherStat.distinctValues {
			p.addDistinctWithLimits(stat, value)
		}
		for _, value := range otherStat.pivotColumns {
			p.addPivotColumnWithLimits(stat, value)
		}
		for _, observation := range otherStat.extensionValues {
			p.addExtensionValueWithLimits(stat, observation)
		}
		mergeSemanticObservations(stat, otherStat)
	}
	return nil
}

func (p *Profiler) Documents() []FieldCatalogDocument {
	out := make([]FieldCatalogDocument, 0, len(p.stats))
	paths := make([]string, 0, len(p.stats))
	for path := range p.stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	datasetGeneration := NormalizeDatasetGeneration(p.datasetGeneration)
	for _, path := range paths {
		stat := p.stats[path]
		distinctValues := append([]string(nil), stat.distinctValues...)
		pivotColumns := append([]string(nil), stat.pivotColumns...)
		extensionValues := append([]ExtensionValueObservation(nil), stat.extensionValues...)
		semanticObservations := make([]SemanticObservation, 0, len(stat.semanticObservations))
		for _, observation := range stat.semanticObservations {
			semanticObservations = append(semanticObservations, semanticObservationValues(observation))
		}
		mixedChoices := make(map[string]map[string]struct{})
		for _, observation := range semanticObservations {
			key := strings.Join([]string{observation.Source.Canonical, observation.OwningScope, observation.Key.System, observation.Key.Code, observation.Key.Selector}, "\x00")
			if mixedChoices[key] == nil {
				mixedChoices[key] = map[string]struct{}{}
			}
			mixedChoices[key][observation.ChoiceArm] = struct{}{}
		}
		for index := range semanticObservations {
			observation := &semanticObservations[index]
			key := strings.Join([]string{observation.Source.Canonical, observation.OwningScope, observation.Key.System, observation.Key.Code, observation.Key.Selector}, "\x00")
			if len(mixedChoices[key]) > 1 {
				observation.Status = "MIXED_CHOICE"
				observation.Completeness = SemanticPartial
			}
		}
		sort.Slice(semanticObservations, func(i, j int) bool {
			return semanticObservationKey(semanticObservations[i]) < semanticObservationKey(semanticObservations[j])
		})
		slices.Sort(distinctValues)
		slices.Sort(pivotColumns)
		sort.Slice(extensionValues, func(i, j int) bool {
			if extensionValues[i].URL != extensionValues[j].URL {
				return extensionValues[i].URL < extensionValues[j].URL
			}
			if strings.Join(extensionValues[i].URLPath, "\x00") != strings.Join(extensionValues[j].URLPath, "\x00") {
				return strings.Join(extensionValues[i].URLPath, "\x00") < strings.Join(extensionValues[j].URLPath, "\x00")
			}
			if extensionValues[i].SourcePath != extensionValues[j].SourcePath {
				return extensionValues[i].SourcePath < extensionValues[j].SourcePath
			}
			return extensionValues[i].ValuePath < extensionValues[j].ValuePath
		})
		out = append(out, FieldCatalogDocument{
			Key:                   fieldCatalogKeyForGeneration(p.project, datasetGeneration, p.authResourcePath, p.resourceType, stat.path),
			Project:               p.project,
			DatasetGeneration:     datasetGeneration,
			AuthResourcePath:      p.authResourcePath,
			ResourceType:          p.resourceType,
			Path:                  stat.path,
			Kind:                  stat.kind,
			DocCount:              stat.docCount,
			MaxItems:              stat.maxItems,
			SampleCount:           len(distinctValues),
			DistinctValues:        distinctValues,
			DistinctTruncated:     stat.distinctTruncated,
			PivotCandidate:        stat.pivotCandidate,
			PivotKind:             stat.pivotKind,
			PivotColumns:          pivotColumns,
			PivotFamily:           stat.pivotFamily,
			PivotColumnSelect:     stat.pivotColumnSelect,
			PivotValueSelect:      stat.pivotValueSelect,
			PivotItemSource:       stat.pivotItemSource,
			PivotItemResourceType: stat.pivotItemResourceType,
			PivotValueSelectors:   append([]string(nil), stat.pivotValueSelectors...),
			ExtensionValues:       extensionValues,
			SemanticObservations:  semanticObservations,
		})
	}
	return out
}

func maxArrayItems(root any, accessor []pathStep) int {
	if len(accessor) == 0 || !accessor[len(accessor)-1].iterateArray {
		return 0
	}
	nodes := []any{root}
	for index, step := range accessor {
		last := index == len(accessor)-1
		next := make([]any, 0, len(nodes))
		for _, node := range nodes {
			object, ok := node.(map[string]any)
			if !ok {
				continue
			}
			value, ok := object[step.field]
			if !ok || value == nil {
				continue
			}
			if step.iterateArray {
				items, ok := value.([]any)
				if !ok {
					continue
				}
				if last {
					return maxLength(nodes, step.field)
				}
				next = append(next, items...)
				continue
			}
			next = append(next, value)
		}
		nodes = next
	}
	return 0
}

func maxLength(nodes []any, field string) int {
	maximum := 0
	for _, node := range nodes {
		object, ok := node.(map[string]any)
		if !ok {
			continue
		}
		items, ok := object[field].([]any)
		if ok {
			maximum = max(maximum, len(items))
		}
	}
	return maximum
}

func (p *Profiler) ensureStat(field *fieldPlan) *fieldCatalogStats {
	if stat, ok := p.stats[field.Path]; ok {
		return stat
	}
	if p.limits.MaxFields > 0 && len(p.stats) >= p.limits.MaxFields {
		p.truncated = true
		return nil
	}
	if !p.reserve(fieldStatsWeight(field.Path, field.Kind)) {
		p.truncated = true
		return nil
	}
	stat := &fieldCatalogStats{
		path:              field.Path,
		kind:              field.Kind,
		pivotCandidate:    field.PivotCandidate,
		pivotKind:         field.PivotKind,
		distinctSet:       make(map[string]struct{}),
		pivotColumnSet:    make(map[string]struct{}),
		extensionValueSet: make(map[string]struct{}),
	}
	if field.PivotCandidate {
		if spec, ok := fhirschema.DefaultPivotSpec(p.resourceType, field.Path, ""); ok {
			stat.pivotFamily = spec.Family
			stat.pivotColumnSelect = fhirschema.SelectorExpression(spec.ColumnSelector)
			stat.pivotValueSelect = fhirschema.SelectorExpression(spec.ValueSelector)
			stat.pivotItemSource = spec.ItemSourcePath
			stat.pivotItemResourceType = spec.ItemResourceType
			stat.pivotValueSelectors = selectorExpressions(spec.ValueSelectors)
		}
	}
	p.stats[field.Path] = stat
	return stat
}

func (p *Profiler) RetentionBytes() int {
	if p.budget != nil {
		return p.budget.usage()
	}
	return p.retainedBytes
}

func (p *Profiler) Truncated() bool {
	return p.truncated || (p.shapeCache != nil && p.shapeCache.isTruncated())
}

func (p *Profiler) reserve(weight int) bool {
	if weight <= 0 {
		return true
	}
	if p.budget != nil && !p.budget.reserve(weight) {
		return false
	}
	p.retainedBytes += weight
	return true
}

func fieldStatsWeight(path, kind string) int {
	return 96 + len(path) + len(kind)
}

func distinctValueWeight(value string) int { return 32 + len(value) }

func pivotColumnWeight(value string) int { return 32 + len(value) }

func extensionValueWeight(observation ExtensionValueObservation) int {
	weight := 64 + len(observation.URL) + len(observation.SourcePath) + len(observation.ValuePath) + len(observation.ValueType)
	for _, path := range observation.URLPath {
		weight += len(path)
	}
	return weight
}

func (p *Profiler) addDistinctWithLimits(stat *fieldCatalogStats, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := stat.distinctSet[value]; ok {
		return
	}
	if len(value) > p.limits.MaxDistinctValueBytes || len(stat.distinctValues) >= p.limits.MaxDistinctValuesPerField || !p.reserve(distinctValueWeight(value)) {
		stat.distinctTruncated = true
		p.truncated = true
		return
	}
	stat.distinctSet[value] = struct{}{}
	stat.distinctValues = append(stat.distinctValues, value)
}

func (p *Profiler) addPivotColumnWithLimits(stat *fieldCatalogStats, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := stat.pivotColumnSet[value]; ok {
		return
	}
	if len(value) > p.limits.MaxDistinctValueBytes || len(stat.pivotColumns) >= p.limits.MaxPivotColumnsPerField || !p.reserve(pivotColumnWeight(value)) {
		stat.distinctTruncated = true
		p.truncated = true
		return
	}
	stat.pivotColumnSet[value] = struct{}{}
	stat.pivotColumns = append(stat.pivotColumns, value)
}

func (p *Profiler) addExtensionValueWithLimits(stat *fieldCatalogStats, observation ExtensionValueObservation) {
	observation.URL = strings.TrimSpace(observation.URL)
	observation.SourcePath = strings.TrimSpace(observation.SourcePath)
	observation.ValuePath = strings.TrimSpace(observation.ValuePath)
	observation.ValueType = strings.TrimSpace(observation.ValueType)
	if observation.URL == "" || observation.SourcePath == "" || observation.ValueType == "" {
		return
	}
	if len(observation.URL) > p.limits.MaxDistinctValueBytes || len(observation.SourcePath) > p.limits.MaxDistinctValueBytes || len(observation.ValuePath) > p.limits.MaxDistinctValueBytes {
		stat.distinctTruncated = true
		p.truncated = true
		return
	}
	for _, ancestor := range observation.URLPath {
		if len(ancestor) > p.limits.MaxDistinctValueBytes {
			stat.distinctTruncated = true
			p.truncated = true
			return
		}
	}
	key := extensionObservationKey(observation)
	if _, ok := stat.extensionValueSet[key]; ok {
		return
	}
	if len(stat.extensionValues) >= p.limits.MaxExtensionValuesPerField || !p.reserve(extensionValueWeight(observation)) {
		stat.distinctTruncated = true
		p.truncated = true
		return
	}
	stat.extensionValueSet[key] = struct{}{}
	stat.extensionValues = append(stat.extensionValues, observation)
}

func extensionObservationKey(observation ExtensionValueObservation) string {
	return observation.URL + "\x00" + strings.Join(observation.URLPath, "\x00") + "\x00" + observation.SourcePath + "\x00" + observation.ValuePath + "\x00" + observation.ValueType
}

func (s *fieldCatalogStats) addDistinctWithLimits(value string, limits ProfileLimits) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	limits = limits.normalized()
	if _, ok := s.distinctSet[value]; ok {
		return
	}
	if len(value) > limits.MaxDistinctValueBytes || len(s.distinctValues) >= limits.MaxDistinctValuesPerField {
		s.distinctTruncated = true
		return
	}
	s.distinctSet[value] = struct{}{}
	s.distinctValues = append(s.distinctValues, value)
}

func (s *fieldCatalogStats) addPivotColumnWithLimits(value string, limits ProfileLimits) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	limits = limits.normalized()
	if _, ok := s.pivotColumnSet[value]; ok {
		return
	}
	if len(value) > limits.MaxDistinctValueBytes || len(s.pivotColumns) >= limits.MaxPivotColumnsPerField {
		s.distinctTruncated = true
		return
	}
	s.pivotColumnSet[value] = struct{}{}
	s.pivotColumns = append(s.pivotColumns, value)
}

func (s *fieldCatalogStats) addExtensionValueWithLimits(observation ExtensionValueObservation, limits ProfileLimits) {
	limits = limits.normalized()
	observation.URL = strings.TrimSpace(observation.URL)
	observation.SourcePath = strings.TrimSpace(observation.SourcePath)
	observation.ValuePath = strings.TrimSpace(observation.ValuePath)
	observation.ValueType = strings.TrimSpace(observation.ValueType)
	if observation.URL == "" || observation.SourcePath == "" || observation.ValueType == "" {
		return
	}
	if len(observation.URL) > limits.MaxDistinctValueBytes || len(observation.SourcePath) > limits.MaxDistinctValueBytes || len(observation.ValuePath) > limits.MaxDistinctValueBytes {
		s.distinctTruncated = true
		return
	}
	for _, ancestor := range observation.URLPath {
		if len(ancestor) > limits.MaxDistinctValueBytes {
			s.distinctTruncated = true
			return
		}
	}
	if s.extensionValueSet == nil {
		s.extensionValueSet = make(map[string]struct{})
	}
	key := observation.URL + "\x00" + strings.Join(observation.URLPath, "\x00") + "\x00" + observation.SourcePath + "\x00" + observation.ValuePath + "\x00" + observation.ValueType
	if _, ok := s.extensionValueSet[key]; ok {
		return
	}
	if len(s.extensionValues) >= limits.MaxExtensionValuesPerField {
		s.distinctTruncated = true
		return
	}
	s.extensionValueSet[key] = struct{}{}
	s.extensionValues = append(s.extensionValues, observation)
}

func (p *Profiler) observeExtensionValues(payload map[string]any) {
	walkExtensionValues(payload, "", p)
}

func walkExtensionValues(value any, path string, profiler *Profiler) {
	walkExtensionValuesWithAncestors(value, path, profiler, nil)
}

func walkExtensionValuesWithAncestors(value any, path string, profiler *Profiler, ancestors []string) {
	switch typed := value.(type) {
	case map[string]any:
		isExtension := strings.HasSuffix(path, "extension[]")
		if url, ok := typed["url"].(string); ok && strings.TrimSpace(url) != "" && isExtension {
			stat := profiler.ensureExtensionURLStat(path + ".url")
			if stat == nil {
				return
			}
			for key, raw := range typed {
				if !strings.HasPrefix(key, "value") || len(key) == len("value") || raw == nil {
					continue
				}
				valuePath, valueType := extensionValueMapping(key, raw)
				profiler.addExtensionValueWithLimits(stat, ExtensionValueObservation{URL: url, SourcePath: path, ValuePath: valuePath, ValueType: valueType, URLPath: append([]string(nil), ancestors...)})
			}
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
			if key == "extension" && isExtension {
				if url, ok := typed["url"].(string); ok && strings.TrimSpace(url) != "" {
					nextAncestors = append(append([]string(nil), ancestors...), strings.TrimSpace(url))
				}
			}
			walkExtensionValuesWithAncestors(child, childPath, profiler, nextAncestors)
		}
	case []any:
		for _, item := range typed {
			if item != nil {
				walkExtensionValuesWithAncestors(item, path, profiler, ancestors)
			}
		}
	}
}

func (p *Profiler) ensureExtensionURLStat(path string) *fieldCatalogStats {
	if stat, ok := p.stats[path]; ok {
		return stat
	}
	if p.limits.MaxFields > 0 && len(p.stats) >= p.limits.MaxFields {
		p.truncated = true
		return nil
	}
	if !p.reserve(fieldStatsWeight(path, fieldKindScalar)) {
		p.truncated = true
		return nil
	}
	stat := &fieldCatalogStats{path: path, kind: fieldKindScalar, distinctSet: make(map[string]struct{}), pivotColumnSet: make(map[string]struct{}), extensionValueSet: make(map[string]struct{})}
	p.stats[path] = stat
	return stat
}

func extensionValueMapping(path string, value any) (string, string) {
	// FHIR complex value[x] payloads and arrays require lossless JSON fallback;
	// an empty value path is the resolver's explicit canonical-JSON marker.
	switch value.(type) {
	case map[string]any, []any:
		return "", "string"
	}
	suffix := strings.TrimPrefix(path, "value")
	switch suffix {
	case "Integer", "PositiveInt", "UnsignedInt", "Integer64":
		return path, "integer"
	case "Decimal":
		return path, "decimal"
	case "Boolean":
		return path, "boolean"
	case "Date":
		return path, "date"
	case "DateTime":
		return path, "date_time"
	default:
		if _, ok := scalarStringValue(value); ok {
			return path, "string"
		}
		return "", "string"
	}
}

func (s *fieldCatalogStats) setPivotDefaults(family string, columnSelector string, valueSelector string) {
	if strings.TrimSpace(family) != "" {
		s.pivotFamily = family
	}
	if strings.TrimSpace(columnSelector) != "" {
		s.pivotColumnSelect = columnSelector
	}
	if strings.TrimSpace(valueSelector) != "" {
		s.pivotValueSelect = valueSelector
	}
}

func (s *fieldCatalogStats) setPivotScope(itemSource, itemResourceType string, valueSelectors []string) {
	if strings.TrimSpace(itemSource) != "" {
		s.pivotItemSource = itemSource
	}
	if strings.TrimSpace(itemResourceType) != "" {
		s.pivotItemResourceType = itemResourceType
	}
	if len(valueSelectors) > 0 {
		s.pivotValueSelectors = append([]string(nil), valueSelectors...)
	}
}

func selectorExpressions(selectors []fhirschema.FieldSelectorSpec) []string {
	result := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		if value := strings.TrimSpace(fhirschema.SelectorExpression(selector)); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (c *ShapePlanCache) getOrBuild(fingerprint string, payload map[string]any) *shapePlan {
	c.mu.RLock()
	plan, ok := c.plans[fingerprint]
	c.mu.RUnlock()
	if ok {
		return plan
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if plan, ok = c.plans[fingerprint]; ok {
		return plan
	}
	plan = buildShapePlan(payload)
	weight := shapePlanWeight(plan)
	if c.maxPlans > 0 && len(c.plans) >= c.maxPlans {
		c.truncated = true
		return plan
	}
	if c.budget != nil && !c.budget.reserve(weight) {
		c.truncated = true
		return plan
	}
	c.plans[fingerprint] = plan
	c.retainedBytes += weight
	return plan
}

func shapePlanWeight(plan *shapePlan) int {
	weight := 64
	for _, field := range plan.fields {
		weight += 48 + len(field.Path) + len(field.Kind) + len(field.PivotKind)
		for _, step := range field.Accessor {
			weight += 24 + len(step.field)
		}
	}
	return weight
}

func buildShapePlan(payload map[string]any) *shapePlan {
	fieldMap := make(map[string]*fieldPlan)
	walkShapeValue(payload, nil, "", fieldMap)
	paths := make([]string, 0, len(fieldMap))
	for path := range fieldMap {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fields := make([]*fieldPlan, 0, len(paths))
	for _, path := range paths {
		fields = append(fields, fieldMap[path])
	}
	return &shapePlan{fields: fields}
}

func walkShapeValue(value any, accessor []pathStep, path string, fieldMap map[string]*fieldPlan) {
	switch typed := value.(type) {
	case map[string]any:
		if path != "" {
			kind, pivotCandidate, pivotKind := classifyObjectShape(typed)
			addFieldPlan(fieldMap, path, accessor, kind, pivotCandidate, pivotKind)
		}
		keys := sortedKeys(typed)
		for _, key := range keys {
			if path == "" && isLoomMetadataField(key) {
				continue
			}
			child := typed[key]
			if child == nil {
				continue
			}
			switch childTyped := child.(type) {
			case []any:
				arrayPath := appendPath(path, key, true)
				arrayAccessor := appendAccessor(accessor, pathStep{field: key, iterateArray: true})
				addFieldPlan(fieldMap, arrayPath, arrayAccessor, fieldKindArray, false, "")
				for _, item := range childTyped {
					if item == nil {
						continue
					}
					walkShapeValue(item, arrayAccessor, arrayPath, fieldMap)
				}
			default:
				childPath := appendPath(path, key, false)
				childAccessor := appendAccessor(accessor, pathStep{field: key})
				walkShapeValue(child, childAccessor, childPath, fieldMap)
			}
		}
	case []any:
		if path != "" {
			addFieldPlan(fieldMap, path, accessor, fieldKindArray, false, "")
		}
		for _, item := range typed {
			if item == nil {
				continue
			}
			walkShapeValue(item, accessor, path, fieldMap)
		}
	default:
		if path != "" {
			addFieldPlan(fieldMap, path, accessor, fieldKindScalar, false, "")
		}
	}
}

func isLoomMetadataField(path string) bool {
	switch path {
	case "project_id", "auth_resource_path", "dataset_generation":
		return true
	default:
		return false
	}
}

func addFieldPlan(fieldMap map[string]*fieldPlan, path string, accessor []pathStep, kind string, pivotCandidate bool, pivotKind string) {
	if existing, ok := fieldMap[path]; ok {
		if existing.Kind == fieldKindObject && (kind == fieldKindCodeableConcept || kind == fieldKindCoding) {
			existing.Kind = kind
		}
		if existing.Kind == fieldKindArray && kind != fieldKindArray {
			return
		}
		if pivotCandidate {
			existing.PivotCandidate = true
			existing.PivotKind = pivotKind
		}
		return
	}
	copiedAccessor := append([]pathStep(nil), accessor...)
	fieldMap[path] = &fieldPlan{
		Path:           path,
		Kind:           kind,
		Accessor:       copiedAccessor,
		PivotCandidate: pivotCandidate,
		PivotKind:      pivotKind,
	}
}

func classifyObjectShape(value map[string]any) (string, bool, string) {
	if isCodeableConceptShape(value) {
		return fieldKindCodeableConcept, true, pivotKindCodeableConcept
	}
	if isCodingShape(value) {
		return fieldKindCoding, false, ""
	}
	return fieldKindObject, false, ""
}

func isCodeableConceptShape(value map[string]any) bool {
	_, hasCoding := value["coding"]
	_, hasText := value["text"]
	return hasCoding || hasText
}

func isCodingShape(value map[string]any) bool {
	_, hasSystem := value["system"]
	_, hasCode := value["code"]
	_, hasDisplay := value["display"]
	return hasSystem || hasCode || hasDisplay
}

func (p *Profiler) observeObservationCodePivot(payload map[string]any) {
	if p.resourceType != "Observation" {
		return
	}
	codeValue, ok := payload["code"].(map[string]any)
	if !ok {
		return
	}
	valueSelector := observationValueSelectorFromPayload(payload)
	if valueSelector == "" {
		return
	}
	stat, ok := p.stats["code"]
	if !ok {
		if p.limits.MaxFields > 0 && len(p.stats) >= p.limits.MaxFields {
			p.truncated = true
			return
		}
		if !p.reserve(fieldStatsWeight("code", fieldKindCodeableConcept)) {
			p.truncated = true
			return
		}
		stat = &fieldCatalogStats{
			path:           "code",
			kind:           fieldKindCodeableConcept,
			pivotCandidate: true,
			pivotKind:      pivotKindObservation,
			distinctSet:    make(map[string]struct{}),
			pivotColumnSet: make(map[string]struct{}),
		}
		p.stats["code"] = stat
	}
	stat.pivotCandidate = true
	stat.pivotKind = pivotKindObservation
	columnSelector := "code.coding[].display"
	if _, hasText := codeValue["text"]; hasText {
		columnSelector = "code.text"
	}
	stat.setPivotDefaults(fhirschema.PivotFamilyObservationCodeValue, columnSelector, valueSelector)
	for _, col := range codeableConceptColumns(codeValue) {
		p.addPivotColumnWithLimits(stat, col)
	}
}

func observationValueSelectorFromPayload(payload map[string]any) string {
	if value, ok := payload["valueQuantity"].(map[string]any); ok && value["value"] != nil {
		return "valueQuantity.value"
	}
	if value, ok := payload["valueCodeableConcept"].(map[string]any); ok {
		if strings.TrimSpace(stringValue(value["text"])) != "" {
			return "valueCodeableConcept.text"
		}
		if len(codeableConceptColumns(value)) > 0 {
			return "valueCodeableConcept.coding[].display"
		}
	}
	for _, name := range []string{"valueString", "valueInteger", "valueBoolean", "valueDecimal", "valueDateTime", "valueTime"} {
		if payload[name] != nil {
			return name
		}
	}
	if value, ok := payload["valuePeriod"].(map[string]any); ok {
		if value["start"] != nil {
			return "valuePeriod.start"
		}
		if value["end"] != nil {
			return "valuePeriod.end"
		}
	}
	if value, ok := payload["valueRange"].(map[string]any); ok {
		if low, ok := value["low"].(map[string]any); ok && low["value"] != nil {
			return "valueRange.low.value"
		}
		if high, ok := value["high"].(map[string]any); ok && high["value"] != nil {
			return "valueRange.high.value"
		}
	}
	if value, ok := payload["valueRatio"].(map[string]any); ok {
		if num, ok := value["numerator"].(map[string]any); ok && num["value"] != nil {
			return "valueRatio.numerator.value"
		}
		if den, ok := value["denominator"].(map[string]any); ok && den["value"] != nil {
			return "valueRatio.denominator.value"
		}
	}
	return ""
}
