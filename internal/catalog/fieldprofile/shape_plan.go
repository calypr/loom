package fieldprofile

import (
	"sort"
	"sync"
)

type ShapePlanCache struct {
	mu            sync.RWMutex
	plans         map[string]*shapePlan
	maxPlans      int
	maxBytes      int
	retainedBytes int
	truncated     bool
	budget        *retentionBudget
	sharedPlans   *ShapePlanCache
}

type retentionBudget struct {
	mu   sync.Mutex
	max  int
	used int
}

func newRetentionBudget(max int) *retentionBudget {
	return &retentionBudget{max: max}
}

func (b *retentionBudget) reserve(weight int) bool {
	if weight <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max > 0 && b.used > b.max-weight {
		return false
	}
	b.used += weight
	return true
}

func (b *retentionBudget) release(weight int) {
	if weight <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= weight
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
}

func (c *ShapePlanCache) retentionBudget(max int) *retentionBudget {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budget == nil {
		c.budget = newRetentionBudget(max)
	} else if c.budget.max <= 0 && max > 0 {
		c.budget.max = max
	}
	return c.budget
}

func (c *ShapePlanCache) isTruncated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.truncated
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

func NewShapePlanCacheWithLimits(maxPlans, maxBytes int) *ShapePlanCache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRetainedBytes
	}
	return &ShapePlanCache{plans: make(map[string]*shapePlan), maxPlans: maxPlans, maxBytes: maxBytes, budget: newRetentionBudget(maxBytes)}
}

// Essential: files share immutable shape plans while retaining independent profiling budgets.
func NewShapePlanCacheWithSharedPlans(maxPlans, maxBytes int, shared *ShapePlanCache) *ShapePlanCache {
	cache := NewShapePlanCacheWithLimits(maxPlans, maxBytes)
	cache.sharedPlans = shared
	return cache
}

func (c *ShapePlanCache) getOrBuild(fingerprint string, payload map[string]any) *shapePlan {
	c.mu.RLock()
	plan, ok := c.plans[fingerprint]
	c.mu.RUnlock()
	if ok {
		return plan
	}
	var sharedPlan *shapePlan
	if c.sharedPlans != nil {
		sharedPlan = c.sharedPlans.getOrBuild(fingerprint, payload)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cachedPlan, exists := c.plans[fingerprint]; exists {
		return cachedPlan
	}
	plan = sharedPlan
	if plan == nil {
		plan = buildShapePlan(payload)
	}
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
