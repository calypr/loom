package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/fhirschema"
)

// GeneratedRootSummaries returns every concrete root supported by the active
// generated FHIR schema. No root is marked available because this function
// deliberately has no dataset or catalog dependency.
func GeneratedRootSummaries() []RootResourceSummary {
	resourceTypes := fhirschema.ResourceTypes()
	roots := make([]RootResourceSummary, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		roots = append(roots, RootResourceSummary{
			ResourceType:  resourceType,
			Supported:     true,
			SupportReason: RootSupportNotObservedInCatalog,
		})
	}
	return roots
}

// BuildSnapshot converts already-authorized catalog facts into a deterministic
// guided-discovery response. It intentionally ignores catalog fields and
// relationships that are not representable by the active generated schema;
// exposing them would let the frontend offer a choice that the compiler cannot
// prove safe.
func BuildSnapshot(facts CatalogFacts) (Snapshot, error) {
	evidence, err := collectEvidence(facts)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotFromEvidence(evidence)
}

// discoveryEvidence is the common schema/catalog-normalized source for both
// the JSON-safe Snapshot and the non-JSON capability resolver. Keeping this
// private prevents raw catalog paths and graph labels from becoming a product
// transport contract.
type discoveryEvidence struct {
	project       string
	roots         []RootResourceSummary
	rootIndexes   map[string]int
	columns       map[ColumnID]*columnAggregate
	relationships map[RelationshipID]*relationshipAggregate
}

func collectEvidence(facts CatalogFacts) (discoveryEvidence, error) {
	project := strings.TrimSpace(facts.Project)
	if project == "" {
		return discoveryEvidence{}, fmt.Errorf("catalog facts require a project")
	}
	roots := GeneratedRootSummaries()
	rootIndexes := rootIndexes(roots)
	columns, err := aggregateColumns(project, facts.Fields, rootIndexes)
	if err != nil {
		return discoveryEvidence{}, err
	}
	relationships, err := aggregateRelationships(facts.Relationships, rootIndexes)
	if err != nil {
		return discoveryEvidence{}, err
	}
	return discoveryEvidence{
		project:       project,
		roots:         roots,
		rootIndexes:   rootIndexes,
		columns:       columns,
		relationships: relationships,
	}, nil
}

func snapshotFromEvidence(evidence discoveryEvidence) (Snapshot, error) {
	snapshot := Snapshot{
		Dataset: DatasetSummary{
			Project: evidence.project,
			Roots:   append([]RootResourceSummary(nil), evidence.roots...),
		},
		Relationships: RelationshipInventory{Entries: []Relationship{}},
		Columns:       []CandidateColumn{},
		Filters:       []GuidedFilterSuggestion{},
	}
	columns := materializeColumns(evidence.columns)
	snapshot.Columns = columns
	for _, column := range columns {
		root := &snapshot.Dataset.Roots[evidence.rootIndexes[column.ResourceType]]
		markRootAvailable(root)
		root.CandidateColumnCount++
	}

	relationships := materializeRelationships(evidence.relationships)
	snapshot.Relationships.Entries = relationships
	for _, relationship := range relationships {
		from := &snapshot.Dataset.Roots[evidence.rootIndexes[relationship.FromResourceType]]
		to := &snapshot.Dataset.Roots[evidence.rootIndexes[relationship.ToResourceType]]
		markRootAvailable(from)
		markRootAvailable(to)
		from.RelationshipCount++
	}

	snapshot.Filters = materializeFilterSuggestions(columns)
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("build discovery snapshot: %w", err)
	}
	return snapshot, nil
}

func markRootAvailable(root *RootResourceSummary) {
	root.Available = true
	root.SupportReason = RootSupportObservedInCatalog
}

type columnSpec struct {
	resourceType string
	canonical    string
	selector     fhirschema.FieldSelectorSpec
	hasSelector  bool
	valueKind    ValueKind
	repeated     bool
	canSelect    bool
	canFilter    bool
	canPivot     bool
}

type columnAggregate struct {
	columnSpec
	populatedDocumentCount int64
	values                 map[string]struct{}
	valuesTruncated        bool
	pivots                 map[string]*pivotAggregate
}

// pivotSpec is schema-validated metadata derived from a populated catalog
// field. It remains private until a caller resolves an opaque ColumnID.
type pivotSpec struct {
	family         string
	columnSelector fhirschema.FieldSelectorSpec
	valueSelector  fhirschema.FieldSelectorSpec
}

type pivotAggregate struct {
	spec      pivotSpec
	columns   map[string]struct{}
	truncated bool
}

func aggregateColumns(project string, fields []catalog.PopulatedField, roots map[string]int) (map[ColumnID]*columnAggregate, error) {
	aggregates := make(map[ColumnID]*columnAggregate)
	for _, field := range fields {
		resourceType := strings.TrimSpace(field.ResourceType)
		if resourceType == "" || strings.TrimSpace(field.Path) == "" {
			return nil, fmt.Errorf("catalog field requires resource type and path")
		}
		if field.DocCount < 0 || field.SampleCount < 0 {
			return nil, fmt.Errorf("catalog field has a negative population count")
		}
		if strings.TrimSpace(field.Project) != project {
			return nil, fmt.Errorf("catalog field project does not match requested project")
		}
		if _, ok := roots[resourceType]; !ok {
			// The checked-in generated schema is the authoritative compiler
			// boundary. A stale or wider catalog must not widen this response.
			continue
		}

		canonical := fhirschema.CanonicalizePath(field.Path)
		if canonical == "" {
			return nil, fmt.Errorf("catalog field path has no canonical form")
		}
		pivot, pivotAvailable := observedPivotSpec(resourceType, canonical, field)
		spec, ok := classifyColumn(resourceType, canonical, pivotAvailable)
		if !ok {
			// Object, extension, and unrepresented paths remain intentionally
			// hidden until the generated schema can describe their lowering.
			continue
		}
		id := opaqueColumnID(resourceType, canonical)
		aggregate, ok := aggregates[id]
		if !ok {
			aggregate = &columnAggregate{
				columnSpec: spec,
				values:     make(map[string]struct{}),
				pivots:     make(map[string]*pivotAggregate),
			}
			aggregates[id] = aggregate
		}
		if pivotAvailable {
			aggregate.canPivot = true
			aggregate.addPivot(pivot, field.PivotColumns, field.DistinctTruncated)
		}
		aggregate.populatedDocumentCount += field.DocCount
		aggregate.valuesTruncated = aggregate.valuesTruncated || field.DistinctTruncated
		for _, value := range field.DistinctValues {
			value = strings.TrimSpace(value)
			if value != "" {
				aggregate.values[value] = struct{}{}
			}
		}
	}
	return aggregates, nil
}

func (aggregate *columnAggregate) addPivot(spec pivotSpec, columns []string, truncated bool) {
	key := pivotSpecKey(spec)
	pivot, ok := aggregate.pivots[key]
	if !ok {
		pivot = &pivotAggregate{
			spec:    spec,
			columns: make(map[string]struct{}),
		}
		aggregate.pivots[key] = pivot
	}
	pivot.truncated = pivot.truncated || truncated
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column != "" {
			pivot.columns[column] = struct{}{}
		}
	}
}

func classifyColumn(resourceType, canonical string, pivotAvailable bool) (columnSpec, bool) {
	if metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, canonical); ok {
		if valueKind, ok := valueKindFromPrimitive(metadata.Primitive); ok {
			field, found := fhirschema.LookupField(resourceType, canonical)
			if !found {
				return columnSpec{}, false
			}
			return columnSpec{
				resourceType: resourceType,
				canonical:    canonical,
				selector:     fhirschema.SelectorFromField(field),
				hasSelector:  true,
				valueKind:    valueKind,
				repeated:     metadata.Repeated,
				canSelect:    true,
				canFilter:    true,
				canPivot:     pivotAvailable,
			}, true
		}
	}

	// The catalog knows whether a populated object was observed as a pivot
	// candidate, but generated schema metadata still decides whether that pivot
	// has a supported compiler family. The opaque ID resolves back to the
	// catalog-owned selectors only after the user makes a choice.
	if pivotAvailable {
		return columnSpec{
			resourceType: resourceType,
			canonical:    canonical,
			valueKind:    ValueKindComposite,
			canPivot:     true,
		}, true
	}
	return columnSpec{}, false
}

func observedPivotSpec(resourceType, canonical string, field catalog.PopulatedField) (pivotSpec, bool) {
	if !field.PivotCandidate {
		return pivotSpec{}, false
	}
	columnExpression := strings.TrimSpace(field.PivotColumnSelect)
	valueExpression := strings.TrimSpace(field.PivotValueSelect)
	var columnSelector, valueSelector fhirschema.FieldSelectorSpec
	if columnExpression == "" && valueExpression == "" {
		defaultSpec, ok := fhirschema.DefaultPivotSpec(resourceType, canonical, "")
		if !ok {
			return pivotSpec{}, false
		}
		columnSelector = defaultSpec.ColumnSelector
		valueSelector = defaultSpec.ValueSelector
	} else {
		if columnExpression == "" || valueExpression == "" {
			return pivotSpec{}, false
		}
		var ok bool
		columnSelector, ok = selectorSpecFromExpression(columnExpression)
		if !ok {
			return pivotSpec{}, false
		}
		valueSelector, ok = selectorSpecFromExpression(valueExpression)
		if !ok {
			return pivotSpec{}, false
		}
	}
	pivot, err := fhirschema.ValidatePivotSelectors(resourceType, columnSelector, valueSelector)
	if err != nil || pivot.CatalogRootPath != canonical {
		return pivotSpec{}, false
	}
	if family := strings.TrimSpace(field.PivotFamily); family != "" && family != pivot.Family {
		return pivotSpec{}, false
	}
	return pivotSpec{
		family:         pivot.Family,
		columnSelector: pivot.ColumnSelector,
		valueSelector:  pivot.ValueSelector,
	}, true
}

func selectorSpecFromExpression(expression string) (fhirschema.FieldSelectorSpec, bool) {
	selector, err := fhirschema.ParseSelector(expression)
	if err != nil || len(selector.Steps) == 0 {
		return fhirschema.FieldSelectorSpec{}, false
	}
	parts := make([]string, 0, len(selector.Steps))
	for _, step := range selector.Steps {
		parts = append(parts, selectorStepExpression(step))
	}
	spec := fhirschema.FieldSelectorSpec{ValuePath: parts[len(parts)-1]}
	if len(parts) > 1 {
		spec.SourcePath = strings.Join(parts[:len(parts)-1], ".")
	}
	if selector.Filter != nil {
		spec.Where = &fhirschema.FieldPredicateSpec{
			Path:  selector.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: selector.Filter.Needle,
		}
	}
	return spec, true
}

func selectorStepExpression(step fhirschema.SelectorStep) string {
	if step.Iterate {
		return step.Field + "[]"
	}
	if step.Index != nil {
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	}
	return step.Field
}

func pivotSpecKey(spec pivotSpec) string {
	return strings.Join([]string{
		spec.family,
		fhirschema.SelectorExpression(spec.columnSelector),
		fhirschema.SelectorExpression(spec.valueSelector),
	}, "\x00")
}

func valueKindFromPrimitive(primitive fhirschema.PrimitiveKind) (ValueKind, bool) {
	switch primitive {
	case fhirschema.PrimitiveString:
		return ValueKindString, true
	case fhirschema.PrimitiveBoolean:
		return ValueKindBoolean, true
	case fhirschema.PrimitiveInteger:
		return ValueKindInteger, true
	case fhirschema.PrimitiveDecimal:
		return ValueKindDecimal, true
	case fhirschema.PrimitiveDate:
		return ValueKindDate, true
	case fhirschema.PrimitiveDateTime:
		return ValueKindDateTime, true
	default:
		return "", false
	}
}

func materializeColumns(aggregates map[ColumnID]*columnAggregate) []CandidateColumn {
	columns := make([]CandidateColumn, 0, len(aggregates))
	for id, aggregate := range aggregates {
		values, truncated := normalizedValues(aggregate.values, aggregate.valuesTruncated)
		columns = append(columns, CandidateColumn{
			ID:                     id,
			ResourceType:           aggregate.resourceType,
			Label:                  humanizePath(aggregate.canonical),
			ValueKind:              aggregate.valueKind,
			Repeated:               aggregate.repeated,
			CanSelect:              aggregate.canSelect,
			CanFilter:              aggregate.canFilter,
			CanPivot:               aggregate.canPivot,
			PopulatedDocumentCount: aggregate.populatedDocumentCount,
			SuggestedValues:        values,
			ValuesTruncated:        truncated,
		})
	}
	sort.Slice(columns, func(i, j int) bool {
		if columns[i].ResourceType != columns[j].ResourceType {
			return columns[i].ResourceType < columns[j].ResourceType
		}
		if columns[i].Label != columns[j].Label {
			return columns[i].Label < columns[j].Label
		}
		return columns[i].ID < columns[j].ID
	})
	return columns
}

type relationshipAggregate struct {
	fromType  string
	label     string
	toType    string
	multiple  RelationshipMultiplicity
	traversal fhirschema.CompilerTraversal
	edgeCount int64
}

func aggregateRelationships(references []catalog.PopulatedReference, roots map[string]int) (map[RelationshipID]*relationshipAggregate, error) {
	aggregates := make(map[RelationshipID]*relationshipAggregate)
	for _, reference := range references {
		fromType := strings.TrimSpace(reference.FromType)
		label := strings.TrimSpace(reference.Label)
		toType := strings.TrimSpace(reference.ToType)
		if fromType == "" || label == "" || toType == "" {
			return nil, fmt.Errorf("catalog relationship requires source, label, and target")
		}
		if reference.EdgeCount < 0 {
			return nil, fmt.Errorf("catalog relationship has a negative edge count")
		}
		if _, ok := roots[fromType]; !ok {
			continue
		}
		if _, ok := roots[toType]; !ok {
			continue
		}
		traversal, found, err := fhirschema.ResolveCompilerTraversal(fromType, label, toType)
		if err != nil {
			return nil, fmt.Errorf("active generated schema has unsafe traversal metadata")
		}
		if !found {
			continue
		}

		id := opaqueRelationshipID(fromType, label, toType)
		multiplicity := RelationshipOne
		if traversal.Multiplicity == fhirschema.TraversalMany {
			multiplicity = RelationshipMany
		}
		aggregate, ok := aggregates[id]
		if !ok {
			aggregate = &relationshipAggregate{
				fromType:  fromType,
				label:     label,
				toType:    toType,
				multiple:  multiplicity,
				traversal: traversal,
			}
			aggregates[id] = aggregate
		}
		// DiscoverPopulatedReferences already aggregates each route. If a
		// caller accidentally supplies it more than once, max preserves an
		// idempotent normalized snapshot instead of double-counting a route.
		if reference.EdgeCount > aggregate.edgeCount {
			aggregate.edgeCount = reference.EdgeCount
		}
	}

	return aggregates, nil
}

func materializeRelationships(aggregates map[RelationshipID]*relationshipAggregate) []Relationship {
	relationships := make([]Relationship, 0, len(aggregates))
	for id, aggregate := range aggregates {
		relationships = append(relationships, Relationship{
			ID:                id,
			FromResourceType:  aggregate.fromType,
			ToResourceType:    aggregate.toType,
			Label:             humanizeRelationshipLabel(aggregate.label, aggregate.toType),
			Multiplicity:      aggregate.multiple,
			ObservedEdgeCount: aggregate.edgeCount,
		})
	}
	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].FromResourceType != relationships[j].FromResourceType {
			return relationships[i].FromResourceType < relationships[j].FromResourceType
		}
		if relationships[i].ToResourceType != relationships[j].ToResourceType {
			return relationships[i].ToResourceType < relationships[j].ToResourceType
		}
		if relationships[i].Label != relationships[j].Label {
			return relationships[i].Label < relationships[j].Label
		}
		return relationships[i].ID < relationships[j].ID
	})
	return relationships
}

func materializeFilterSuggestions(columns []CandidateColumn) []GuidedFilterSuggestion {
	filters := make([]GuidedFilterSuggestion, 0, len(columns))
	for _, column := range columns {
		if !column.CanFilter {
			continue
		}
		filters = append(filters, GuidedFilterSuggestion{
			ID:              opaqueFilterSuggestionID(column.ID),
			ColumnID:        column.ID,
			ResourceType:    column.ResourceType,
			Label:           column.Label,
			ValueKind:       column.ValueKind,
			Repeated:        column.Repeated,
			Operators:       filterOperators(column.ValueKind),
			Quantifiers:     filterQuantifiers(column.Repeated),
			SuggestedValues: append([]string(nil), column.SuggestedValues...),
			ValuesTruncated: column.ValuesTruncated,
		})
	}
	sort.Slice(filters, func(i, j int) bool {
		if filters[i].ResourceType != filters[j].ResourceType {
			return filters[i].ResourceType < filters[j].ResourceType
		}
		if filters[i].Label != filters[j].Label {
			return filters[i].Label < filters[j].Label
		}
		return filters[i].ID < filters[j].ID
	})
	return filters
}

func filterOperators(kind ValueKind) []FilterOperator {
	operators := []FilterOperator{
		FilterEquals,
		FilterNotEquals,
		FilterIn,
		FilterExists,
		FilterMissing,
	}
	switch kind {
	case ValueKindString:
		operators = append(operators, FilterContains)
	case ValueKindInteger, ValueKindDecimal, ValueKindDate, ValueKindDateTime:
		operators = append(operators, FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq)
	}
	return operators
}

func filterQuantifiers(repeated bool) []FilterQuantifier {
	if !repeated {
		return []FilterQuantifier{}
	}
	return []FilterQuantifier{FilterAny, FilterAll, FilterNone}
}

func normalizedValues(values map[string]struct{}, alreadyTruncated bool) ([]string, bool) {
	if len(values) == 0 {
		return []string{}, alreadyTruncated
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) > maxSuggestedValues {
		return append([]string(nil), out[:maxSuggestedValues]...), true
	}
	return out, alreadyTruncated
}

func rootIndexes(roots []RootResourceSummary) map[string]int {
	indexes := make(map[string]int, len(roots))
	for i, root := range roots {
		indexes[root.ResourceType] = i
	}
	return indexes
}

func opaqueColumnID(resourceType, canonical string) ColumnID {
	return ColumnID("col_" + stableHash("column", resourceType, canonical))
}

func opaqueRelationshipID(fromType, label, toType string) RelationshipID {
	return RelationshipID("rel_" + stableHash("relationship", fromType, label, toType))
}

func opaqueFilterSuggestionID(columnID ColumnID) FilterSuggestionID {
	return FilterSuggestionID("flt_" + stableHash("filter", string(columnID)))
}

func stableHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func humanizePath(canonical string) string {
	segments := strings.Split(canonical, ".")
	words := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSuffix(segment, "[]")
		if label := humanizeIdentifier(segment); label != "" {
			words = append(words, label)
		}
	}
	return strings.Join(words, " ")
}

func humanizeRelationshipLabel(label, toType string) string {
	label = strings.TrimSuffix(label, "_"+toType)
	parts := strings.Split(label, "_")
	if len(parts) > 1 && fhirschema.HasResource(parts[len(parts)-1]) {
		label = strings.Join(parts[:len(parts)-1], "_")
	}
	return humanizeIdentifier(label)
}

func humanizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var words []string
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		words = append(words, strings.ToLower(string(word)))
		word = word[:0]
	}
	for _, r := range value {
		if r == '_' || r == '-' || r == '.' || unicode.IsSpace(r) {
			flush()
			continue
		}
		if len(word) > 0 && unicode.IsUpper(r) && unicode.IsLower(word[len(word)-1]) {
			flush()
		}
		word = append(word, r)
	}
	flush()
	for i, word := range words {
		if word == "" {
			continue
		}
		first, size := utf8.DecodeRuneInString(word)
		words[i] = string(unicode.ToUpper(first)) + word[size:]
	}
	return strings.Join(words, " ")
}
