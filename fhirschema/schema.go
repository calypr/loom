package fhirschema

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type ResourceSpec struct {
	ResourceType string
	Fields       []FieldSpec
}

type FieldSpec struct {
	Path           string
	SourcePath     string
	ValuePath      string
	Kind           string
	PredicatePaths []string
}

type Selector struct {
	Steps  []SelectorStep
	Filter *ContainsFilter
}

type SelectorStep struct {
	Field   string
	Iterate bool
	Index   *int
}

type ContainsFilter struct {
	Field  string
	Needle string
}

type FieldSelectorSpec struct {
	SourcePath string
	Where      *FieldPredicateSpec
	ValuePath  string
}

type FieldPredicateSpec struct {
	Path  string
	Op    string
	Value string
}

type ResolvedPath struct {
	Path        string
	Property    generatedProperty
	PropertyRef string
}

type PivotSpec struct {
	Family          string
	CatalogRootPath string
	// ItemSourcePath identifies the repeated item scope that owns key/value.
	// Empty means the resource itself is one pivot item.
	ItemSourcePath   string
	ItemResourceType string
	ColumnSelector   FieldSelectorSpec
	ValueSelector    FieldSelectorSpec
	ValueSelectors   []FieldSelectorSpec
}

type TraversalSpec struct {
	FromType     string
	EdgeLabel    string
	ToType       string
	Direction    []string
	Multiplicity []string
	Backref      []string
	RegexMatch   []string
}

type generatedDefinition struct {
	Properties []generatedProperty
}

type generatedProperty struct {
	Name           string
	Kind           string
	Format         string
	Ref            string
	Properties     []generatedProperty
	ItemKind       string
	ItemFormat     string
	ItemRef        string
	ItemProperties []generatedProperty
}

const (
	PredicateContains               = "CONTAINS"
	maxSelectorFieldDepth           = 6
	PivotFamilyCodeableConcept      = "CODEABLE_CONCEPT"
	PivotFamilyObservationCodeValue = "OBSERVATION_CODE_VALUE"
)

var (
	containsPattern = regexp.MustCompile(`^([A-Za-z0-9_]+)\s+contains\s+"([^"]*)"$`)
	resourceCache   sync.Map
)

func FieldsForResource(resourceType string) []FieldSpec {
	if cached, ok := resourceCache.Load(resourceType); ok {
		return cloneFields(cached.([]FieldSpec))
	}
	fields := flattenDefinition(resourceType, 0, map[string]bool{})
	if len(fields) == 0 {
		return []FieldSpec{}
	}
	resourceCache.Store(resourceType, cloneFields(fields))
	return cloneFields(fields)
}

func LookupField(resourceType, canonicalPath string) (FieldSpec, bool) {
	for _, field := range FieldsForResource(resourceType) {
		if field.Path == canonicalPath {
			return field, true
		}
	}
	return FieldSpec{}, false
}

func LookupTraversal(fromType, edgeLabel, toType string) (TraversalSpec, bool) {
	spec, ok := generatedTraversals[traversalKey(fromType, edgeLabel, toType)]
	if !ok {
		return TraversalSpec{}, false
	}
	return cloneTraversalSpec(spec), true
}

func ResolvePath(resourceType, canonicalPath string) (ResolvedPath, bool) {
	parts := strings.Split(strings.TrimSpace(canonicalPath), ".")
	if len(parts) == 0 || parts[0] == "" {
		return ResolvedPath{}, false
	}
	def, ok := generatedDefinitions[resourceType]
	if !ok {
		return ResolvedPath{}, false
	}
	var currentProps []generatedProperty = def.Properties
	var current generatedProperty
	for _, part := range parts {
		name := strings.TrimSuffix(part, "[]")
		prop, ok := findGeneratedProperty(currentProps, name)
		if !ok {
			return ResolvedPath{}, false
		}
		current = prop
		switch prop.Kind {
		case "object":
			currentProps = childProperties(prop)
		case "array":
			currentProps = arrayChildProperties(prop)
		default:
			currentProps = nil
		}
	}
	return ResolvedPath{
		Path:        canonicalPath,
		Property:    current,
		PropertyRef: propertyRefName(current),
	}, true
}

func ResolvesToCodeableConcept(resourceType, canonicalPath string) bool {
	resolved, ok := ResolvePath(resourceType, canonicalPath)
	return ok && resolved.PropertyRef == "CodeableConcept"
}

// ChoiceValueSelectorOptions returns generated-schema-backed value[x]
// selectors for a resource or generated backbone definition. FHIR-specific
// knowledge stays in this schema package; compiler and renderer code consume
// the resulting ordered selectors generically.
func ChoiceValueSelectorOptions(resourceType string) []FieldSelectorSpec {
	candidates := []string{
		"valueQuantity.value",
		"valueCodeableConcept.text",
		"valueCodeableConcept.coding[].display",
		"valueString",
		"valueInteger",
		"valueBoolean",
		"valueDecimal",
		"valueDateTime",
		"valueTime",
		"valuePeriod.start",
		"valuePeriod.end",
		"valueRange.low.value",
		"valueRange.high.value",
		"valueRatio.numerator.value",
		"valueRatio.denominator.value",
	}
	out := make([]FieldSelectorSpec, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := LookupField(resourceType, CanonicalizePath(candidate)); !ok {
			continue
		}
		out = append(out, FieldSelectorSpecFromPath(candidate))
	}
	return out
}

func prependSelector(existing []FieldSelectorSpec, preferred FieldSelectorSpec) []FieldSelectorSpec {
	preferredPath := CanonicalPath(preferred)
	result := []FieldSelectorSpec{preferred}
	for _, candidate := range existing {
		if CanonicalPath(candidate) != preferredPath {
			result = append(result, candidate)
		}
	}
	return result
}

func normalizePivotSelectors(resourceType, catalogRootPath string, column, value FieldSelectorSpec) (FieldSelectorSpec, FieldSelectorSpec, string, string) {
	itemType := resourceType
	itemSource := ""
	if source, repeatedType, ok := repeatedPivotItemScope(resourceType, catalogRootPath); ok {
		itemType = repeatedType
		itemSource = source
		column = relativeSelector(column, itemSource)
		value = relativeSelector(value, itemSource)
	}
	return column, value, itemType, itemSource
}

func repeatedPivotItemScope(resourceType, canonicalPath string) (string, string, bool) {
	parts := strings.Split(CanonicalizePath(canonicalPath), ".")
	for index := len(parts) - 1; index >= 0; index-- {
		if !strings.HasSuffix(parts[index], "[]") {
			continue
		}
		source := strings.Join(parts[:index+1], ".")
		resolved, ok := ResolvePath(resourceType, source)
		if !ok || resolved.Property.Kind != "array" || strings.TrimSpace(resolved.Property.ItemRef) == "" || index+1 >= len(parts) {
			continue
		}
		itemType := resolved.Property.ItemRef
		relative := strings.Join(parts[index+1:], ".")
		if _, ok := ResolvePath(itemType, relative); ok {
			return source, itemType, true
		}
	}
	return "", "", false
}

func relativeSelector(selector FieldSelectorSpec, prefix string) FieldSelectorSpec {
	prefix = CanonicalizePath(prefix)
	path := CanonicalPath(selector)
	if strings.HasPrefix(path, prefix+".") {
		return FieldSelectorSpecFromPath(strings.TrimPrefix(path, prefix+"."))
	}
	return selector
}

func FieldSelectorSpecFromPath(path string) FieldSelectorSpec {
	sourcePath, valuePath := selectorParts(CanonicalizePath(path))
	return FieldSelectorSpec{
		SourcePath: sourcePath,
		ValuePath:  valuePath,
	}
}

func ValidatePivotSelectors(resourceType string, column FieldSelectorSpec, value FieldSelectorSpec) (PivotSpec, error) {
	columnExpr := SelectorExpression(column)
	valueExpr := SelectorExpression(value)
	columnCanonical := CanonicalPath(column)
	valueCanonical := CanonicalPath(value)

	if strings.TrimSpace(columnCanonical) == "" {
		return PivotSpec{}, fmt.Errorf("pivot column selector is required")
	}
	if strings.TrimSpace(valueCanonical) == "" {
		return PivotSpec{}, fmt.Errorf("pivot value selector is required")
	}

	if match, ok := resolvePivotFamily(resourceType, columnCanonical, valueCanonical); ok {
		column, value, itemType, itemSource := normalizePivotSelectors(resourceType, match.catalogRootPath, column, value)
		return PivotSpec{
			Family:           match.family,
			CatalogRootPath:  match.catalogRootPath,
			ItemSourcePath:   itemSource,
			ItemResourceType: itemType,
			ColumnSelector:   normalizeSelectorSpec(column, columnExpr),
			ValueSelector:    normalizeSelectorSpec(value, valueExpr),
			ValueSelectors:   []FieldSelectorSpec{normalizeSelectorSpec(value, valueExpr)},
		}, nil
	}

	return PivotSpec{}, fmt.Errorf("unsupported pivot selector pair %q / %q for resourceType %q", columnExpr, valueExpr, resourceType)
}

func DefaultPivotSpec(resourceType, canonicalPath string, observedValuePath string) (PivotSpec, bool) {
	canonicalPath = CanonicalizePath(canonicalPath)
	if source, itemType, ok := repeatedPivotItemScope(resourceType, canonicalPath); ok {
		relativeRoot := strings.TrimPrefix(canonicalPath, source+".")
		if !ResolvesToCodeableConcept(itemType, relativeRoot) {
			return PivotSpec{}, false
		}
		column := defaultCodeableColumnSelector(itemType, relativeRoot)
		values := ChoiceValueSelectorOptions(itemType)
		if strings.TrimSpace(observedValuePath) != "" {
			values = prependSelector(values, FieldSelectorSpecFromPath(observedValuePath))
		}
		if len(values) == 0 {
			return PivotSpec{}, false
		}
		return PivotSpec{
			Family: PivotFamilyCodeableConcept, CatalogRootPath: canonicalPath,
			ItemSourcePath: source, ItemResourceType: itemType,
			ColumnSelector: column, ValueSelector: values[0], ValueSelectors: values,
		}, true
	}
	if ResolvesToCodeableConcept(resourceType, canonicalPath) {
		column := defaultCodeableColumnSelector(resourceType, canonicalPath)
		// Observation-style code/value[x] pivots use the resource's choice
		// value. Other CodeableConcept fields (for example
		// valueCodeableConcept itself) pivot their own text/coding value and
		// must not borrow unrelated sibling value[x] selectors.
		values := []FieldSelectorSpec{column}
		if canonicalPath == "code" {
			options := ChoiceValueSelectorOptions(resourceType)
			if strings.TrimSpace(observedValuePath) != "" {
				values = prependSelector(options, FieldSelectorSpecFromPath(observedValuePath))
			} else if len(options) > 0 {
				values = options
			}
		}
		spec, err := ValidatePivotSelectors(resourceType, column, values[0])
		if err == nil {
			spec.ValueSelectors = values
		}
		return spec, err == nil
	}
	return PivotSpec{}, false
}

// defaultCodeableColumnSelector prefers the human-facing CodeableConcept.text
// for the conventional `code` shape when the generated schema contains it.
// FHIR producers frequently use that text as the semantic pivot key while
// coding.display is only a broad category (for example
// Observation.code.coding.display == "Component" for many distinct component
// observations). Other CodeableConcept roots retain coding.display.
func defaultCodeableColumnSelector(resourceType, rootPath string) FieldSelectorSpec {
	rootPath = strings.TrimSuffix(CanonicalizePath(rootPath), ".")
	// The dataframer-compatible semantic-key convention applies to FHIR
	// CodeableConcept fields named `code` (including repeated backbone
	// component.code). Other CodeableConcept pivots retain coding.display as
	// their stable key unless a recipe explicitly chooses another selector.
	if rootPath != "code" {
		return FieldSelectorSpecFromPath(rootPath + ".coding[].display")
	}
	textPath := rootPath + ".text"
	if _, ok := LookupField(resourceType, textPath); ok {
		return FieldSelectorSpecFromPath(textPath)
	}
	return FieldSelectorSpecFromPath(rootPath + ".coding[].display")
}

func SelectorFromField(field FieldSpec) FieldSelectorSpec {
	return FieldSelectorSpec{
		SourcePath: field.SourcePath,
		ValuePath:  field.ValuePath,
	}
}

func SelectorExpression(spec FieldSelectorSpec) string {
	path := strings.TrimSpace(spec.ValuePath)
	if strings.TrimSpace(spec.SourcePath) != "" {
		path = strings.TrimSpace(spec.SourcePath) + "." + path
	}
	if spec.Where == nil || spec.Where.Op != PredicateContains || strings.TrimSpace(spec.Where.Path) == "" || spec.Where.Value == "" {
		return path
	}
	return fmt.Sprintf(`%s where %s contains %q`, path, spec.Where.Path, spec.Where.Value)
}

func CanonicalPath(spec FieldSelectorSpec) string {
	return CanonicalizePath(SelectorExpression(FieldSelectorSpec{
		SourcePath: spec.SourcePath,
		ValuePath:  spec.ValuePath,
	}))
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}

func cloneTraversalSpec(spec TraversalSpec) TraversalSpec {
	spec.Direction = cloneStrings(spec.Direction)
	spec.Multiplicity = cloneStrings(spec.Multiplicity)
	spec.Backref = cloneStrings(spec.Backref)
	spec.RegexMatch = cloneStrings(spec.RegexMatch)
	return spec
}

func CanonicalizePath(path string) string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.Contains(part, " ") {
			continue
		}
		if strings.HasSuffix(part, "]") && strings.Contains(part, "[") && !strings.HasSuffix(part, "[]") {
			part = part[:strings.Index(part, "[")] + "[]"
		}
		out = append(out, part)
	}
	return strings.Join(out, ".")
}

func ParseSelector(input string) (Selector, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Selector{}, fmt.Errorf("selector is required")
	}
	var filter *ContainsFilter
	pathPart := input
	if before, after, found := strings.Cut(input, " where "); found {
		pathPart = strings.TrimSpace(before)
		match := containsPattern.FindStringSubmatch(strings.TrimSpace(after))
		if len(match) != 3 {
			return Selector{}, fmt.Errorf("unsupported where clause %q", after)
		}
		filter = &ContainsFilter{Field: match[1], Needle: match[2]}
	}
	parts := strings.Split(pathPart, ".")
	steps := make([]SelectorStep, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return Selector{}, fmt.Errorf("invalid path segment in %q", input)
		}
		step := SelectorStep{}
		switch {
		case strings.HasSuffix(part, "[]"):
			step.Field = strings.TrimSuffix(part, "[]")
			step.Iterate = true
		case strings.HasSuffix(part, "]") && strings.Contains(part, "["):
			idxStart := strings.Index(part, "[")
			step.Field = part[:idxStart]
			idx, err := strconv.Atoi(strings.TrimSuffix(part[idxStart+1:], "]"))
			if err != nil {
				return Selector{}, fmt.Errorf("invalid array index in %q", part)
			}
			step.Index = &idx
		default:
			step.Field = part
		}
		if step.Field == "" {
			return Selector{}, fmt.Errorf("invalid field in %q", part)
		}
		steps = append(steps, step)
	}
	return Selector{Steps: steps, Filter: filter}, nil
}

func (s Selector) CanonicalPath() string {
	parts := make([]string, 0, len(s.Steps))
	for _, step := range s.Steps {
		switch {
		case step.Iterate:
			parts = append(parts, step.Field+"[]")
		case step.Index != nil:
			parts = append(parts, step.Field+"[]")
		default:
			parts = append(parts, step.Field)
		}
	}
	return strings.Join(parts, ".")
}

func flattenDefinition(defName string, depth int, stack map[string]bool) []FieldSpec {
	if depth >= maxSelectorFieldDepth || stack[defName] {
		return nil
	}
	def, ok := generatedDefinitions[defName]
	if !ok {
		return nil
	}
	stack[defName] = true
	defer delete(stack, defName)
	return flattenProperties(def.Properties, depth, stack)
}

func flattenProperties(props []generatedProperty, depth int, stack map[string]bool) []FieldSpec {
	containerPredicates := predicatePaths(props)
	out := map[string]FieldSpec{}
	for _, prop := range props {
		if ignoreProperty(prop.Name) {
			continue
		}
		for _, field := range flattenProperty(prop, depth, containerPredicates, stack) {
			out[field.Path] = field
		}
	}
	fields := make([]FieldSpec, 0, len(out))
	for _, field := range out {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	return fields
}

func flattenProperty(prop generatedProperty, depth int, containerPredicates []string, stack map[string]bool) []FieldSpec {
	if depth >= maxSelectorFieldDepth {
		return nil
	}
	switch prop.Kind {
	case "scalar", "string", "number", "boolean", "integer":
		return []FieldSpec{newField(prop.Name, "scalar", containerPredicates)}
	case "object":
		return flattenObjectProperty(prop.Name, prop.Ref, prop.Properties, depth, stack)
	case "array":
		return flattenArrayProperty(prop, depth, containerPredicates, stack)
	default:
		return nil
	}
}

func flattenObjectProperty(name, ref string, inline []generatedProperty, depth int, stack map[string]bool) []FieldSpec {
	var childFields []FieldSpec
	switch {
	case ref != "":
		childFields = flattenDefinition(ref, depth+1, stack)
	case len(inline) > 0:
		childFields = flattenProperties(inline, depth+1, stack)
	}
	return prefixFields(name, childFields)
}

func flattenArrayProperty(prop generatedProperty, depth int, containerPredicates []string, stack map[string]bool) []FieldSpec {
	arrayPath := prop.Name + "[]"
	switch {
	case prop.ItemKind == "scalar" || (prop.ItemKind == "" && prop.ItemRef == "" && len(prop.ItemProperties) == 0):
		return []FieldSpec{newField(arrayPath, "array", containerPredicates)}
	case prop.ItemRef != "":
		return prefixFields(arrayPath, flattenDefinition(prop.ItemRef, depth+1, stack))
	case len(prop.ItemProperties) > 0:
		return prefixFields(arrayPath, flattenProperties(prop.ItemProperties, depth+1, stack))
	default:
		return []FieldSpec{newField(arrayPath, "array", containerPredicates)}
	}
}

func prefixFields(prefix string, fields []FieldSpec) []FieldSpec {
	out := make([]FieldSpec, 0, len(fields))
	for _, field := range fields {
		path := prefix + "." + field.Path
		sourcePath, valuePath := selectorParts(path)
		out = append(out, FieldSpec{
			Path:           path,
			SourcePath:     sourcePath,
			ValuePath:      valuePath,
			Kind:           field.Kind,
			PredicatePaths: cloneStrings(field.PredicatePaths),
		})
	}
	return out
}

func newField(path, kind string, predicatePaths []string) FieldSpec {
	sourcePath, valuePath := selectorParts(path)
	return FieldSpec{
		Path:           path,
		SourcePath:     sourcePath,
		ValuePath:      valuePath,
		Kind:           kind,
		PredicatePaths: cloneStrings(predicatePaths),
	}
}

func selectorParts(path string) (string, string) {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[:idx], path[idx+1:]
	}
	return "", path
}

func childProperties(prop generatedProperty) []generatedProperty {
	switch {
	case prop.Ref != "":
		if def, ok := generatedDefinitions[prop.Ref]; ok {
			return def.Properties
		}
	case len(prop.Properties) > 0:
		return prop.Properties
	}
	return nil
}

func arrayChildProperties(prop generatedProperty) []generatedProperty {
	switch {
	case prop.ItemRef != "":
		if def, ok := generatedDefinitions[prop.ItemRef]; ok {
			return def.Properties
		}
	case len(prop.ItemProperties) > 0:
		return prop.ItemProperties
	}
	return nil
}

func propertyRefName(prop generatedProperty) string {
	switch {
	case prop.Kind == "object":
		return prop.Ref
	case prop.Kind == "array":
		return prop.ItemRef
	default:
		return ""
	}
}

func findGeneratedProperty(props []generatedProperty, name string) (generatedProperty, bool) {
	for _, prop := range props {
		if prop.Name == name {
			return prop, true
		}
	}
	return generatedProperty{}, false
}

func codeableConceptRoots(resourceType, canonicalPath string) ([]string, bool) {
	parts := strings.Split(canonicalPath, ".")
	for i := len(parts); i > 0; i-- {
		root := strings.Join(parts[:i], ".")
		if ResolvesToCodeableConcept(resourceType, root) {
			return []string{root}, true
		}
	}
	return nil, false
}

type pivotFamilyMatch struct {
	family          string
	catalogRootPath string
}

func resolvePivotFamily(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	resolvers := []func(string, string, string) (pivotFamilyMatch, bool){
		matchObservationCodeValuePivot,
		matchSharedCodeableConceptPivot,
	}
	for _, resolver := range resolvers {
		if match, ok := resolver(resourceType, columnCanonical, valueCanonical); ok {
			return match, true
		}
	}
	return pivotFamilyMatch{}, false
}

func matchObservationCodeValuePivot(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	// The code/value[x] shape is not exclusive to Observation. Generated
	// FHIR backbone definitions (for example ObservationComponent) can expose
	// the same correlated pair, so prove the shape from the active schema
	// rather than branching on a resource name.
	if ResolvesToCodeableConcept(resourceType, "code") && isObservationCodeSelector(columnCanonical) && isObservationValueSelector(valueCanonical) {
		if _, valueExists := ResolvePath(resourceType, valueCanonical); !valueExists {
			return pivotFamilyMatch{}, false
		}
		return pivotFamilyMatch{
			family:          PivotFamilyObservationCodeValue,
			catalogRootPath: "code",
		}, true
	}
	return pivotFamilyMatch{}, false
}

func matchSharedCodeableConceptPivot(resourceType, columnCanonical, valueCanonical string) (pivotFamilyMatch, bool) {
	roots, ok := codeableConceptRoots(resourceType, columnCanonical)
	if !ok {
		return pivotFamilyMatch{}, false
	}
	valueRoots, valueOK := codeableConceptRoots(resourceType, valueCanonical)
	if !valueOK {
		return pivotFamilyMatch{}, false
	}
	for _, root := range roots {
		if slicesContains(valueRoots, root) {
			return pivotFamilyMatch{
				family:          PivotFamilyCodeableConcept,
				catalogRootPath: root,
			}, true
		}
	}
	return pivotFamilyMatch{}, false
}

func isObservationCodeSelector(canonicalPath string) bool {
	if canonicalPath == "code" {
		return true
	}
	return strings.HasPrefix(canonicalPath, "code.")
}

func isObservationValueSelector(canonicalPath string) bool {
	return canonicalPath == "valueString" ||
		canonicalPath == "valueInteger" ||
		canonicalPath == "valueBoolean" ||
		canonicalPath == "valueDecimal" ||
		canonicalPath == "valueDateTime" ||
		canonicalPath == "valueTime" ||
		strings.HasPrefix(canonicalPath, "valueQuantity.") ||
		strings.HasPrefix(canonicalPath, "valueCodeableConcept.") ||
		strings.HasPrefix(canonicalPath, "valuePeriod.") ||
		strings.HasPrefix(canonicalPath, "valueRange.") ||
		strings.HasPrefix(canonicalPath, "valueRatio.")
}

func normalizeSelectorSpec(spec FieldSelectorSpec, expr string) FieldSelectorSpec {
	if strings.TrimSpace(spec.ValuePath) != "" {
		return spec
	}
	return FieldSelectorSpecFromPath(expr)
}

func slicesContains(in []string, want string) bool {
	for _, item := range in {
		if item == want {
			return true
		}
	}
	return false
}

func predicatePaths(props []generatedProperty) []string {
	out := make([]string, 0, len(props))
	for _, prop := range props {
		if ignoreProperty(prop.Name) {
			continue
		}
		if prop.Kind == "array" {
			out = append(out, prop.Name+"[]")
			continue
		}
		out = append(out, prop.Name)
	}
	sort.Strings(out)
	return out
}

func ignoreProperty(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "resourceType", "fhir_comments", "links":
		return true
	default:
		return false
	}
}

func cloneFields(in []FieldSpec) []FieldSpec {
	out := make([]FieldSpec, len(in))
	for i := range in {
		out[i] = FieldSpec{
			Path:           in[i].Path,
			SourcePath:     in[i].SourcePath,
			ValuePath:      in[i].ValuePath,
			Kind:           in[i].Kind,
			PredicatePaths: cloneStrings(in[i].PredicatePaths),
		}
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
