package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"arangodb-proto/internal/dbio"
	"arangodb-proto/internal/fhirschema"
	"arangodb-proto/internal/store"

	"github.com/bytedance/sonic"
)

const (
	FieldCatalogCollection   = "fhir_field_catalog"
	fieldCatalogDistinctCap  = 50
	fieldCatalogPivotCap     = 50
	fieldKindScalar          = "scalar"
	fieldKindObject          = "object"
	fieldKindArray           = "array"
	fieldKindCodeableConcept = "codeable_concept"
	fieldKindCoding          = "coding"
	pivotKindCodeableConcept = "codeable_concept_display_value"
	pivotKindObservation     = "observation_code_value"
)

type FieldCatalogDocument struct {
	Key               string   `json:"_key"`
	Project           string   `json:"project"`
	AuthResourcePath  string   `json:"auth_resource_path,omitempty"`
	ResourceType      string   `json:"resource_type"`
	Path              string   `json:"path"`
	Kind              string   `json:"kind"`
	DocCount          int64    `json:"doc_count"`
	SampleCount       int      `json:"sample_count"`
	DistinctValues    []string `json:"distinct_values,omitempty"`
	DistinctTruncated bool     `json:"distinct_truncated"`
	PivotCandidate    bool     `json:"pivot_candidate"`
	PivotKind         string   `json:"pivot_kind,omitempty"`
	PivotColumns      []string `json:"pivot_columns,omitempty"`
	PivotFamily       string   `json:"pivot_family,omitempty"`
	PivotColumnSelect string   `json:"pivot_column_selector,omitempty"`
	PivotValueSelect  string   `json:"pivot_value_selector,omitempty"`
}

type PopulatedFieldOptions struct {
	dbio.ConnectionOptions
	Project           string
	AuthResourcePaths []string
	ResourceType      string
	PivotOnly         bool
	CursorBatch       int
}

type PopulatedField struct {
	Project           string   `json:"project"`
	AuthResourcePath  string   `json:"auth_resource_path,omitempty"`
	ResourceType      string   `json:"resource_type"`
	Path              string   `json:"path"`
	Kind              string   `json:"kind"`
	DocCount          int64    `json:"doc_count"`
	SampleCount       int      `json:"sample_count"`
	DistinctValues    []string `json:"distinct_values,omitempty"`
	DistinctTruncated bool     `json:"distinct_truncated"`
	PivotCandidate    bool     `json:"pivot_candidate"`
	PivotKind         string   `json:"pivot_kind,omitempty"`
	PivotColumns      []string `json:"pivot_columns,omitempty"`
	PivotFamily       string   `json:"pivot_family,omitempty"`
	PivotColumnSelect string   `json:"pivot_column_selector,omitempty"`
	PivotValueSelect  string   `json:"pivot_value_selector,omitempty"`
}

const populatedFieldsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @resource_type == null OR d.resource_type == @resource_type
  FILTER @pivot_only == false OR d.pivot_candidate == true
  SORT d.resource_type, d.doc_count DESC, d.path
  RETURN {
    project: d.project,
    auth_resource_path: d.auth_resource_path,
    resource_type: d.resource_type,
    path: d.path,
    kind: d.kind,
    doc_count: d.doc_count,
    sample_count: d.sample_count,
    distinct_values: d.distinct_values,
    distinct_truncated: d.distinct_truncated,
    pivot_candidate: d.pivot_candidate,
    pivot_kind: d.pivot_kind,
    pivot_columns: d.pivot_columns,
    pivot_family: d.pivot_family,
    pivot_column_selector: d.pivot_column_selector,
    pivot_value_selector: d.pivot_value_selector
  }
`

const populatedFieldsSurrealQL = `
SELECT
  project,
  resource_type,
  path,
  kind,
  doc_count,
  sample_count,
  distinct_values,
  distinct_truncated,
  pivot_candidate,
  pivot_kind,
  pivot_columns,
  pivot_family,
  pivot_column_selector,
  pivot_value_selector
FROM fhir_field_catalog
WHERE project = $project
  AND ($auth_resource_paths_unrestricted = true OR auth_resource_path INSIDE $auth_resource_paths)
  AND ($resource_type = "" OR resource_type = $resource_type)
  AND ($pivot_only = false OR pivot_candidate = true)
ORDER BY resource_type ASC, doc_count DESC, path ASC;
`

const populatedFieldsPostgresSQL = `
SELECT
  project,
  auth_resource_path,
  resource_type,
  path,
  kind,
  doc_count,
  sample_count,
  COALESCE(distinct_values, ARRAY[]::text[]) AS distinct_values,
  distinct_truncated,
  pivot_candidate,
  pivot_kind,
  COALESCE(pivot_columns, ARRAY[]::text[]) AS pivot_columns,
  pivot_family,
  pivot_column_selector,
  pivot_value_selector
FROM fhir_field_catalog
WHERE project = @project
  AND (@auth_resource_paths_unrestricted = true OR auth_resource_path = ANY(@auth_resource_paths))
  AND (NULLIF(@resource_type, '') IS NULL OR resource_type = @resource_type)
  AND (@pivot_only = false OR pivot_candidate = true)
ORDER BY resource_type ASC, doc_count DESC, path ASC;
`

type Profiler struct {
	project          string
	authResourcePath string
	resourceType     string
	shapeCache       *ShapePlanCache
	stats            map[string]*fieldCatalogStats
}

type fieldCatalogStats struct {
	path              string
	kind              string
	docCount          int64
	distinctValues    []string
	distinctSet       map[string]struct{}
	distinctTruncated bool
	pivotCandidate    bool
	pivotKind         string
	pivotColumns      []string
	pivotColumnSet    map[string]struct{}
	pivotFamily       string
	pivotColumnSelect string
	pivotValueSelect  string
}

type ShapePlanCache struct {
	mu    sync.RWMutex
	plans map[string]*shapePlan
}

type shapePlan struct {
	fields []*fieldPlan
}

type fieldPlan struct {
	Path           string
	Kind           string
	Accessor       []pathStep
	PivotCandidate bool
	PivotKind      string
}

type pathStep struct {
	field        string
	iterateArray bool
}

func NewShapePlanCache() *ShapePlanCache {
	return &ShapePlanCache{plans: make(map[string]*shapePlan)}
}

func NewProfiler(project, authResourcePath, resourceType string, cache *ShapePlanCache) *Profiler {
	return &Profiler{
		project:          project,
		authResourcePath: authResourcePath,
		resourceType:     resourceType,
		shapeCache:       cache,
		stats:            make(map[string]*fieldCatalogStats),
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
	if len(s.distinctValues) >= fieldCatalogDistinctCap {
		s.distinctTruncated = true
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
	if len(s.pivotColumns) >= fieldCatalogPivotCap {
		s.distinctTruncated = true
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

func (p *Profiler) Merge(other *Profiler) {
	for path, otherStat := range other.stats {
		stat, ok := p.stats[path]
		if !ok {
			stat = &fieldCatalogStats{
				path:           otherStat.path,
				kind:           otherStat.kind,
				pivotCandidate: otherStat.pivotCandidate,
				pivotKind:      otherStat.pivotKind,
				pivotFamily:    otherStat.pivotFamily,
				pivotColumnSelect: otherStat.pivotColumnSelect,
				pivotValueSelect:  otherStat.pivotValueSelect,
				distinctSet:    make(map[string]struct{}),
				pivotColumnSet: make(map[string]struct{}),
			}
			p.stats[path] = stat
		}
		stat.docCount += otherStat.docCount
		stat.distinctTruncated = stat.distinctTruncated || otherStat.distinctTruncated
		stat.setPivotDefaults(otherStat.pivotFamily, otherStat.pivotColumnSelect, otherStat.pivotValueSelect)
		for _, value := range otherStat.distinctValues {
			stat.addDistinct(value)
		}
		for _, value := range otherStat.pivotColumns {
			stat.addPivotColumn(value)
		}
	}
}

func (p *Profiler) Documents() []FieldCatalogDocument {
	out := make([]FieldCatalogDocument, 0, len(p.stats))
	paths := make([]string, 0, len(p.stats))
	for path := range p.stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		stat := p.stats[path]
		distinctValues := append([]string(nil), stat.distinctValues...)
		pivotColumns := append([]string(nil), stat.pivotColumns...)
		slices.Sort(distinctValues)
		slices.Sort(pivotColumns)
		out = append(out, FieldCatalogDocument{
			Key:               fieldCatalogKey(p.project, p.authResourcePath, p.resourceType, stat.path),
			Project:           p.project,
			AuthResourcePath:  p.authResourcePath,
			ResourceType:      p.resourceType,
			Path:              stat.path,
			Kind:              stat.kind,
			DocCount:          stat.docCount,
			SampleCount:       len(distinctValues),
			DistinctValues:    distinctValues,
			DistinctTruncated: stat.distinctTruncated,
			PivotCandidate:    stat.pivotCandidate,
			PivotKind:         stat.pivotKind,
			PivotColumns:      pivotColumns,
			PivotFamily:       stat.pivotFamily,
			PivotColumnSelect: stat.pivotColumnSelect,
			PivotValueSelect:  stat.pivotValueSelect,
		})
	}
	return out
}

func fieldCatalogKey(project, authResourcePath, resourceType, path string) string {
	return sanitizeCollectionKey(project + "::" + authResourcePath + "::" + resourceType + "::" + path)
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

func (c *ShapePlanCache) planCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.plans)
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
			// preserve array kind for the array path itself
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
	stat.setPivotDefaults(fhirschema.PivotFamilyObservationCodeValue, "code.coding[].display", valueSelector)
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

func appendPath(prefix, key string, array bool) string {
	if prefix == "" {
		if array {
			return key + "[]"
		}
		return key
	}
	if array {
		return prefix + "." + key + "[]"
	}
	return prefix + "." + key
}

func appendAccessor(accessor []pathStep, step pathStep) []pathStep {
	out := append([]pathStep(nil), accessor...)
	out = append(out, step)
	return out
}

func extractAccessorValues(root any, accessor []pathStep) ([]any, bool) {
	nodes := []any{root}
	for _, step := range accessor {
		next := make([]any, 0, len(nodes))
		for _, node := range nodes {
			obj, ok := node.(map[string]any)
			if !ok {
				continue
			}
			value, ok := obj[step.field]
			if !ok || value == nil {
				continue
			}
			if step.iterateArray {
				items, ok := value.([]any)
				if !ok {
					continue
				}
				next = append(next, items...)
				continue
			}
			next = append(next, value)
		}
		if len(next) == 0 {
			return nil, false
		}
		nodes = next
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

func scalarStringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case float64:
		return fmt.Sprintf("%v", typed), true
	case float32:
		return fmt.Sprintf("%v", typed), true
	case int:
		return fmt.Sprintf("%d", typed), true
	case int64:
		return fmt.Sprintf("%d", typed), true
	case int32:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}

func codeableConceptColumns(value map[string]any) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	appendValue := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if _, ok := seen[text]; ok {
			return
		}
		seen[text] = struct{}{}
		out = append(out, text)
	}
	if text, ok := value["text"].(string); ok {
		appendValue(text)
	}
	if codingValues, ok := value["coding"].([]any); ok {
		for _, raw := range codingValues {
			coding, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if display, ok := coding["display"].(string); ok {
				appendValue(display)
				continue
			}
			if code, ok := coding["code"].(string); ok {
				appendValue(code)
			}
		}
	}
	return out
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func shapeFingerprintForValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedKeys(typed)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+":"+shapeFingerprintForValue(typed[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		childPrints := make([]string, 0, len(typed))
		seen := make(map[string]struct{})
		for _, item := range typed {
			fingerprint := shapeFingerprintForValue(item)
			if _, ok := seen[fingerprint]; ok {
				continue
			}
			seen[fingerprint] = struct{}{}
			childPrints = append(childPrints, fingerprint)
		}
		sort.Strings(childPrints)
		return "[" + strings.Join(childPrints, "|") + "]"
	case string:
		return "s"
	case bool:
		return "b"
	case float64, float32, int, int32, int64:
		return "n"
	case nil:
		return "0"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func WriteFieldCatalog(ctx context.Context, client store.Backend, collection string, docs []FieldCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	if len(docs) == 0 {
		return nil
	}
	start := time.Now()
	rawDocs := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		data, err := sonic.ConfigFastest.Marshal(&doc)
		if err != nil {
			return err
		}
		rawDocs = append(rawDocs, json.RawMessage(data))
	}
	timings["field_catalog_marshal"] += time.Since(start).Seconds()

	for i := 0; i < len(rawDocs); i += batchSize {
		end := i + batchSize
		if end > len(rawDocs) {
			end = len(rawDocs)
		}
		insertStart := time.Now()
		if err := client.InsertBatchRaw(ctx, collection, rawDocs[i:end], overwrite, writeAPI); err != nil {
			return err
		}
		timings["field_catalog_insert"] += time.Since(insertStart).Seconds()
	}
	return nil
}

func sanitizeCollectionKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			strings.ContainsRune("_-:.@()+,=;$!*'", r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func DiscoverPopulatedFields(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := dbio.OpenBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	emit("go_discovery_start", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"resource_type":       opts.ResourceType,
		"pivot_only":          opts.PivotOnly,
		"auth_resource_paths": opts.AuthResourcePaths,
		"cursor_batch_size":   opts.CursorBatch,
		"query":               "populated_fields",
	})

	query := populatedFieldsAQL
	bindVars := map[string]any{
		"project":                          opts.Project,
		"pivot_only":                       opts.PivotOnly,
		"auth_resource_paths":              cloneStrings(opts.AuthResourcePaths),
		"auth_resource_paths_unrestricted": len(opts.AuthResourcePaths) == 0,
	}
	switch dbio.BackendName(opts.Backend) {
	case dbio.BackendSurreal:
		query = populatedFieldsSurrealQL
		bindVars["resource_type"] = opts.ResourceType
	case dbio.BackendPostgres:
		query = populatedFieldsPostgresSQL
		bindVars["resource_type"] = opts.ResourceType
	default:
		if opts.ResourceType != "" {
			bindVars["resource_type"] = opts.ResourceType
		} else {
			bindVars["resource_type"] = nil
		}
	}

	results := make([]PopulatedField, 0, 64)
	err = client.QueryRows(ctx, query, opts.CursorBatch, bindVars, func(row map[string]any) error {
		results = append(results, PopulatedField{
			Project:           stringValue(row["project"]),
			AuthResourcePath:  stringValue(row["auth_resource_path"]),
			ResourceType:      stringValue(row["resource_type"]),
			Path:              stringValue(row["path"]),
			Kind:              stringValue(row["kind"]),
			DocCount:          int64Must(row["doc_count"]),
			SampleCount:       int(int64Must(row["sample_count"])),
			DistinctValues:    stringSliceValue(row["distinct_values"]),
			DistinctTruncated: boolValue(row["distinct_truncated"]),
			PivotCandidate:    boolValue(row["pivot_candidate"]),
			PivotKind:         stringValue(row["pivot_kind"]),
			PivotColumns:      stringSliceValue(row["pivot_columns"]),
			PivotFamily:       stringValue(row["pivot_family"]),
			PivotColumnSelect: stringValue(row["pivot_column_selector"]),
			PivotValueSelect:  stringValue(row["pivot_value_selector"]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	emit("go_discovery_complete", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"resource_type":       opts.ResourceType,
		"pivot_only":          opts.PivotOnly,
		"auth_resource_paths": opts.AuthResourcePaths,
		"rows":                len(results),
		"seconds":             secondsSince(start),
	})
	return results, nil
}

func stringSliceValue(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func boolValue(value any) bool {
	v, _ := value.(bool)
	return v
}

func int64Must(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}
