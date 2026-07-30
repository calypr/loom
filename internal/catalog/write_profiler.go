package catalog

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/fhirschema"
)

func NewShapePlanCache() *ShapePlanCache {
	return &ShapePlanCache{plans: make(map[string]*shapePlan)}
}

// NewProfiler constructs a profiler in the legacy catalog namespace. Keep
// this constructor for existing ingest callers: an empty generation produces
// the exact same catalog documents and keys as before generation support.
func NewProfiler(project, authResourcePath, resourceType string, cache *ShapePlanCache) *Profiler {
	return NewProfilerForGeneration(project, "", authResourcePath, resourceType, cache)
}

// NewProfilerForGeneration constructs a profiler whose catalog documents are
// bound to one immutable dataset generation. A blank (or whitespace-only)
// generation intentionally selects the legacy namespace for compatibility.
func NewProfilerForGeneration(project, datasetGeneration, authResourcePath, resourceType string, cache *ShapePlanCache) *Profiler {
	return &Profiler{
		project:           project,
		datasetGeneration: NormalizeDatasetGeneration(datasetGeneration),
		authResourcePath:  authResourcePath,
		resourceType:      resourceType,
		shapeCache:        cache,
		stats:             make(map[string]*fieldCatalogStats),
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
		stat.docCount++
		switch field.Kind {
		case fieldKindScalar:
			for _, value := range values {
				if text, ok := scalarStringValue(value); ok {
					stat.addDistinct(text)
				}
			}
		case fieldKindCodeableConcept:
			for _, value := range values {
				if cc, ok := value.(map[string]any); ok {
					for _, col := range codeableConceptColumns(cc) {
						stat.addPivotColumn(col)
						stat.addDistinct(col)
					}
				}
			}
		}
	}
	p.observeObservationCodePivot(payload)
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
	if p.stats == nil {
		p.stats = make(map[string]*fieldCatalogStats)
	}
	for path, otherStat := range other.stats {
		stat, ok := p.stats[path]
		if !ok {
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
			}
			p.stats[path] = stat
		}
		stat.docCount += otherStat.docCount
		stat.distinctTruncated = stat.distinctTruncated || otherStat.distinctTruncated
		stat.setPivotDefaults(otherStat.pivotFamily, otherStat.pivotColumnSelect, otherStat.pivotValueSelect)
		stat.setPivotScope(otherStat.pivotItemSource, otherStat.pivotItemResourceType, otherStat.pivotValueSelectors)
		for _, value := range otherStat.distinctValues {
			stat.addDistinct(value)
		}
		for _, value := range otherStat.pivotColumns {
			stat.addPivotColumn(value)
		}
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
		slices.Sort(distinctValues)
		slices.Sort(pivotColumns)
		out = append(out, FieldCatalogDocument{
			Key:                   fieldCatalogKeyForGeneration(p.project, datasetGeneration, p.authResourcePath, p.resourceType, stat.path),
			Project:               p.project,
			DatasetGeneration:     datasetGeneration,
			AuthResourcePath:      p.authResourcePath,
			ResourceType:          p.resourceType,
			Path:                  stat.path,
			Kind:                  stat.kind,
			DocCount:              stat.docCount,
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
		})
	}
	return out
}

func (p *Profiler) ensureStat(field *fieldPlan) *fieldCatalogStats {
	if stat, ok := p.stats[field.Path]; ok {
		return stat
	}
	stat := &fieldCatalogStats{
		path:           field.Path,
		kind:           field.Kind,
		pivotCandidate: field.PivotCandidate,
		pivotKind:      field.PivotKind,
		distinctSet:    make(map[string]struct{}),
		pivotColumnSet: make(map[string]struct{}),
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

func (s *fieldCatalogStats) addDistinct(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := s.distinctSet[value]; ok {
		return
	}
	s.distinctSet[value] = struct{}{}
	s.distinctValues = append(s.distinctValues, value)
}

func (s *fieldCatalogStats) addPivotColumn(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := s.pivotColumnSet[value]; ok {
		return
	}
	s.pivotColumnSet[value] = struct{}{}
	s.pivotColumns = append(s.pivotColumns, value)
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
	c.plans[fingerprint] = plan
	return plan
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
		stat.addPivotColumn(col)
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
