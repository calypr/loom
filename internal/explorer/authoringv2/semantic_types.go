package authoringv2

import (
	"fmt"
	"regexp"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	LifecycleNew   = "NEW"
	LifecycleReady = "READY"
)

type ExplorerMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// RouteNode is one semantic occurrence in a document route tree. Relationship
// is empty only for the root; every child names the relationship from its
// parent. Resource identities are stable FHIR resource types, not catalog IDs.
type RouteNode struct {
	OccurrenceID string      `json:"occurrenceId"`
	ResourceType string      `json:"resourceType"`
	Relationship string      `json:"relationship,omitempty"`
	Children     []RouteNode `json:"children,omitempty"`
}

type ColumnSource struct {
	Kind      string           `json:"kind"`
	Field     *FieldSource     `json:"field,omitempty"`
	Aggregate *AggregateSource `json:"aggregate,omitempty"`
	Lookup    *LookupSource    `json:"lookup,omitempty"`
}

// FieldSource is the closed payload for a direct field projection. The
// related-selection annotation is intentionally nested in the field variant.
// It cannot accidentally be attached to a project, lookup, or aggregate.
type FieldSource struct {
	Path             string            `json:"path"`
	ProjectionMode   string            `json:"projectionMode,omitempty"`
	RelatedSelection *RelatedSelection `json:"relatedSelection,omitempty"`
}

type AggregateSource struct {
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	// Where is retained only for decoding immutable/pre-v3 in-memory recipes.
	// It is deliberately omitted from the current authoring JSON contract;
	// writable contributor intent lives on Column.Contributor instead.
	Where          *SourceWhere `json:"-"`
	RequiredValues []string     `json:"requiredValues,omitempty"`
}

type SourceWhere struct {
	Path   string `json:"path"`
	Equals string `json:"equals"`
}

// UnmarshalJSON keeps the current aggregate source shape closed. Persisted
// legacy sources are decoded by decodePersistedSource, which is the only
// compatibility boundary allowed to interpret wherePath/whereEquals or a
// nested where object.
func (s *AggregateSource) UnmarshalJSON(raw []byte) error {
	type wire struct {
		Operation      string   `json:"operation"`
		Path           string   `json:"path,omitempty"`
		RequiredValues []string `json:"requiredValues,omitempty"`
	}
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	*s = AggregateSource{Operation: decoded.Operation, Path: decoded.Path, RequiredValues: append([]string(nil), decoded.RequiredValues...)}
	return nil
}

type LookupSource struct {
	Match          string `json:"match,omitempty"`
	Path           string `json:"path,omitempty"`
	ProjectionMode string `json:"projectionMode,omitempty"`
	// Binding is the validated correlated FHIR shape used by code/system
	// lookups. It is a closed alternative to legacy Match/Path, never a
	// precedence rule between two writable lookup meanings.
	Binding *fhirschema.CorrelatedBinding `json:"binding,omitempty"`
	// Key is required with Binding and carries the selected system/code
	// identity. It is not accepted for legacy lookup variants.
	Key *fhirschema.CorrelatedKey `json:"key,omitempty"`
	// Extension is the ancestor-aware closed alternative for extensionByUrl.
	// It cannot coexist with legacy Match/Path or the terminology Binding/Key.
	Extension *fhirschema.ExtensionBinding `json:"extension,omitempty"`
}

type RelatedSelection struct {
	Kind         string `json:"kind"`
	Acknowledged bool   `json:"acknowledged"`
}

// UnmarshalJSON keeps the writable source contract closed. Legacy flat source
// payloads are decoded only by DecodeWorkspace's persisted-draft migration.
func (s *ColumnSource) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind      string           `json:"kind"`
		Field     *FieldSource     `json:"field,omitempty"`
		Aggregate *AggregateSource `json:"aggregate,omitempty"`
		Lookup    *LookupSource    `json:"lookup,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	value := ColumnSource{Kind: wire.Kind, Field: wire.Field, Aggregate: wire.Aggregate, Lookup: wire.Lookup}
	switch wire.Kind {
	case SourceField:
		if wire.Field == nil || wire.Aggregate != nil || wire.Lookup != nil {
			return fmt.Errorf("field source requires exactly the field payload")
		}
	case SourceAggregate:
		if wire.Aggregate == nil || wire.Field != nil || wire.Lookup != nil {
			return fmt.Errorf("aggregate source requires exactly the aggregate payload")
		}
	case SourceIdentifierBySystem, SourceExtensionByURL, SourceCodingBySystem, SourceObservationComponentByCode:
		if wire.Lookup == nil || wire.Field != nil || wire.Aggregate != nil {
			return fmt.Errorf("%s source requires exactly the lookup payload", wire.Kind)
		}
	case SourceProjectID:
		if wire.Field != nil || wire.Aggregate != nil || wire.Lookup != nil {
			return fmt.Errorf("projectId source does not accept a payload")
		}
	default:
		return fmt.Errorf("unsupported source kind %q", wire.Kind)
	}
	*s = value
	return nil
}

func (s ColumnSource) fieldPath() string {
	if s.Field != nil {
		return s.Field.Path
	}
	if s.Lookup != nil {
		return s.Lookup.Path
	}
	if s.Aggregate != nil {
		return s.Aggregate.Path
	}
	return ""
}

func (s ColumnSource) FieldPath() string { return s.fieldPath() }

func (s ColumnSource) projectionMode() string {
	if s.Field != nil {
		return s.Field.ProjectionMode
	}
	if s.Lookup != nil {
		return s.Lookup.ProjectionMode
	}
	return ""
}

func (s ColumnSource) ProjectionMode() string { return s.projectionMode() }

func (s ColumnSource) lookupMatch() string {
	if s.Lookup != nil {
		return s.Lookup.Match
	}
	return ""
}

func (s ColumnSource) LookupMatch() string { return s.lookupMatch() }

func (s ColumnSource) Normalized() ColumnSource {
	n := s
	if n.Field != nil {
		field := *n.Field
		if strings.TrimSpace(field.ProjectionMode) == "" {
			field.ProjectionMode = "FIRST"
		}
		if field.RelatedSelection != nil {
			related := *field.RelatedSelection
			field.RelatedSelection = &related
		}
		n.Field = &field
	}
	if n.Lookup != nil {
		lookup := *n.Lookup
		if strings.TrimSpace(lookup.ProjectionMode) == "" {
			lookup.ProjectionMode = "FIRST"
		}
		if lookup.Binding != nil {
			binding := *lookup.Binding
			binding.ValueFallback = append([]string(nil), lookup.Binding.ValueFallback...)
			binding.ChoiceArms = append([]string(nil), lookup.Binding.ChoiceArms...)
			lookup.Binding = &binding
		}
		if lookup.Key != nil {
			key := *lookup.Key
			lookup.Key = &key
		}
		if lookup.Extension != nil {
			extension := *lookup.Extension
			extension.URLPath = append([]string(nil), lookup.Extension.URLPath...)
			extension.ChoiceArms = append([]string(nil), lookup.Extension.ChoiceArms...)
			extension.ValueFallback = append([]string(nil), lookup.Extension.ValueFallback...)
			lookup.Extension = &extension
		}
		n.Lookup = &lookup
	}
	if n.Aggregate != nil {
		aggregate := *n.Aggregate
		aggregate.RequiredValues = append([]string(nil), n.Aggregate.RequiredValues...)
		if n.Aggregate.Where != nil {
			where := *n.Aggregate.Where
			aggregate.Where = &where
		}
		n.Aggregate = &aggregate
	}
	return n
}

const (
	SourceField                      = "field"
	SourceIdentifierBySystem         = "identifierBySystem"
	SourceExtensionByURL             = "extensionByUrl"
	SourceCodingBySystem             = "codingBySystem"
	SourceObservationComponentByCode = "observationComponentByCode"
	SourceProjectID                  = "projectId"
	SourceAggregate                  = "aggregate"
)

type Column struct {
	Column       string                `json:"column"`
	Label        string                `json:"label"`
	LogicalType  string                `json:"logicalType,omitempty"`
	OccurrenceID string                `json:"occurrenceId"`
	Source       ColumnSource          `json:"source"`
	Contributor  *ContributorPredicate `json:"contributor,omitempty"`
	Table        *TablePresentation    `json:"table,omitempty"`
	Filter       *FilterPresentation   `json:"filter,omitempty"`
	Chart        *ChartPresentation    `json:"chart,omitempty"`
}

// ContributorPredicate is catalog intent scoped to one aggregate feature.
// CandidateID is resolved against the occurrence's pinned catalog at compile
// time; callers never provide a selector or AQL expression.
type ContributorPredicate struct {
	CandidateID string                `json:"candidateId"`
	Operator    ContributorOperator   `json:"operator"`
	Quantifier  ContributorQuantifier `json:"quantifier,omitempty"`
	Value       *ContributorValue     `json:"value,omitempty"`
}

type ContributorOperator string

const (
	ContributorExists ContributorOperator = "EXISTS"
	ContributorEquals ContributorOperator = "EQUALS"
)

func (op ContributorOperator) Valid() bool {
	return op == ContributorExists || op == ContributorEquals
}

type ContributorQuantifier string

const ContributorAny ContributorQuantifier = "ANY"

func (q ContributorQuantifier) Valid() bool {
	return q == "" || q == ContributorAny
}

type ContributorValueKind string

const (
	ContributorString    ContributorValueKind = "STRING"
	ContributorValueCode ContributorValueKind = "CODE"
)

func (kind ContributorValueKind) Valid() bool {
	return kind == ContributorString || kind == ContributorValueCode
}

type ContributorValue struct {
	Kind   ContributorValueKind `json:"kind"`
	String *string              `json:"string,omitempty"`
	Code   *ContributorCode     `json:"code,omitempty"`
}

type ContributorCode struct {
	Code string `json:"code"`
}

func (v ContributorValue) Validate() error {
	if !v.Kind.Valid() {
		return fmt.Errorf("unsupported contributor value kind %q", v.Kind)
	}
	if (v.String == nil) == (v.Code == nil) {
		return fmt.Errorf("contributor value requires exactly one STRING or CODE member")
	}
	switch v.Kind {
	case ContributorString:
		if v.String == nil {
			return fmt.Errorf("STRING contributor value requires string")
		}
	case ContributorValueCode:
		if v.Code == nil || strings.TrimSpace(v.Code.Code) == "" {
			return fmt.Errorf("CODE contributor value requires a non-empty code")
		}
	}
	return nil
}

func (p ContributorPredicate) Validate() error {
	if strings.TrimSpace(p.CandidateID) == "" || p.CandidateID != strings.TrimSpace(p.CandidateID) {
		return fmt.Errorf("contributor candidateId is required")
	}
	if !p.Operator.Valid() {
		return fmt.Errorf("unsupported contributor operator %q", p.Operator)
	}
	if !p.Quantifier.Valid() {
		return fmt.Errorf("unsupported contributor quantifier %q", p.Quantifier)
	}
	switch p.Operator {
	case ContributorExists:
		if p.Value != nil {
			return fmt.Errorf("EXISTS contributor predicate does not accept value")
		}
	case ContributorEquals:
		if p.Value == nil {
			return fmt.Errorf("EQUALS contributor predicate requires value")
		}
		if err := p.Value.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (p ContributorPredicate) Normalized() ContributorPredicate {
	n := p
	n.CandidateID = strings.TrimSpace(n.CandidateID)
	if n.Value != nil {
		value := *n.Value
		if value.String != nil {
			stringValue := *value.String
			value.String = &stringValue
		}
		if value.Code != nil {
			code := *value.Code
			value.Code = &code
		}
		n.Value = &value
	}
	return n
}

type FixedFilter struct {
	Column string   `json:"column"`
	Values []string `json:"values"`
}

type ActionColumn struct {
	Column       string `json:"column"`
	ExportHeader string `json:"exportHeader,omitempty"`
}

type Action struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	FileName string         `json:"fileName,omitempty"`
	Columns  []ActionColumn `json:"columns,omitempty"`
}

type SharedFilterBinding struct {
	OutputID string `json:"outputId"`
	Column   string `json:"column"`
}

type FileActions struct {
	Extensions map[string][]string `json:"extensions"`
	Actions    map[string]string   `json:"actions"`
}

var physicalColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (d Document) semanticOccurrences() (map[string]RouteNode, error) {
	if strings.TrimSpace(d.RootResourceType) == "" {
		return nil, fmt.Errorf("rootResourceType is required")
	}
	if d.Route.OccurrenceID != RootOccurrenceID || d.Route.ResourceType != d.RootResourceType || strings.TrimSpace(d.Route.Relationship) != "" {
		return nil, fmt.Errorf("route root must be occurrence %q with resourceType %q and no relationship", RootOccurrenceID, d.RootResourceType)
	}
	occurrences := map[string]RouteNode{}
	var walk func(RouteNode, string) error
	walk = func(node RouteNode, path string) error {
		if emptyID(node.OccurrenceID) || strings.TrimSpace(node.ResourceType) == "" {
			return fmt.Errorf("%s occurrenceId and resourceType are required", path)
		}
		if _, duplicate := occurrences[node.OccurrenceID]; duplicate {
			return fmt.Errorf("duplicate route occurrence id %q", node.OccurrenceID)
		}
		occurrences[node.OccurrenceID] = node
		for i, child := range node.Children {
			if strings.TrimSpace(child.Relationship) == "" {
				return fmt.Errorf("%s.children[%d].relationship is required", path, i)
			}
			if err := walk(child, fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(d.Route, "route"); err != nil {
		return nil, err
	}
	return occurrences, nil
}

func (s ColumnSource) validate(path string) error {
	if strings.TrimSpace(s.Kind) == "" {
		return fmt.Errorf("%s.kind is required", path)
	}
	variants := 0
	if s.Field != nil {
		variants++
	}
	if s.Aggregate != nil {
		variants++
	}
	if s.Lookup != nil {
		variants++
	}
	if s.Kind == SourceProjectID {
		if variants != 0 {
			return fmt.Errorf("%s projectId source does not accept a payload", path)
		}
		return nil
	}
	if variants != 1 {
		return fmt.Errorf("%s must contain exactly one matching variant payload", path)
	}
	mode := ""
	if s.Field != nil {
		mode = s.Field.ProjectionMode
	}
	if s.Lookup != nil {
		mode = s.Lookup.ProjectionMode
	}
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode == "" {
		mode = "FIRST"
	}
	if mode != "VALUE" && mode != "INDEXED" && mode != "FIRST" && mode != "ALL" && mode != "DISTINCT" {
		return fmt.Errorf("%s projectionMode %q is unsupported", path, mode)
	}
	switch s.Kind {
	case SourceField:
		if s.Field == nil || strings.TrimSpace(s.Field.Path) == "" {
			return fmt.Errorf("%s field source requires field.path", path)
		}
		if s.Field.RelatedSelection != nil && s.Field.RelatedSelection.Kind != "first-by-resource-key" {
			return fmt.Errorf("%s.field.relatedSelection.kind %q is unsupported", path, s.Field.RelatedSelection.Kind)
		}
	case SourceIdentifierBySystem, SourceExtensionByURL, SourceCodingBySystem, SourceObservationComponentByCode:
		if s.Lookup == nil || (s.Lookup.Binding == nil && s.Lookup.Extension == nil && strings.TrimSpace(s.Lookup.Match) == "") {
			return fmt.Errorf("%s %s source requires lookup.match", path, s.Kind)
		}
		if s.Lookup != nil && s.Lookup.Extension != nil {
			if s.Kind != SourceExtensionByURL {
				return fmt.Errorf("%s extension binding is only supported for extensionByUrl", path)
			}
			if s.Lookup.Binding != nil || s.Lookup.Key != nil || strings.TrimSpace(s.Lookup.Match) != "" || strings.TrimSpace(s.Lookup.Path) != "" {
				return fmt.Errorf("%s extension lookup must not combine extension with match, path, binding, or key", path)
			}
			return nil
		}
		if s.Lookup != nil && s.Lookup.Binding != nil {
			if s.Kind != SourceCodingBySystem && s.Kind != SourceObservationComponentByCode {
				return fmt.Errorf("%s correlated binding is only supported for codingBySystem and observationComponentByCode", path)
			}
			if strings.TrimSpace(s.Lookup.Match) != "" || strings.TrimSpace(s.Lookup.Path) != "" {
				return fmt.Errorf("%s correlated lookup must not combine binding with legacy match or path", path)
			}
			if s.Lookup.Key == nil || strings.TrimSpace(s.Lookup.Key.System) == "" || strings.TrimSpace(s.Lookup.Key.Code) == "" {
				return fmt.Errorf("%s correlated lookup requires key.system and key.code", path)
			}
		} else if s.Lookup != nil && s.Lookup.Key != nil {
			return fmt.Errorf("%s legacy lookup must not contain correlated key", path)
		}
	case SourceAggregate:
		if s.Aggregate == nil {
			return fmt.Errorf("%s aggregate source requires aggregate payload", path)
		}
		op := strings.ToUpper(strings.TrimSpace(s.Aggregate.Operation))
		switch op {
		case "COUNT", "COUNT_DISTINCT", "DISTINCT_VALUES", "MIN", "MAX", "EXISTS", "CONTAINS_ALL":
		default:
			return fmt.Errorf("%s aggregate source operation %q is unsupported", path, s.Aggregate.Operation)
		}
		requiresField := op == "COUNT_DISTINCT" || op == "DISTINCT_VALUES" || op == "MIN" || op == "MAX" || op == "CONTAINS_ALL"
		if requiresField && strings.TrimSpace(s.Aggregate.Path) == "" {
			return fmt.Errorf("%s aggregate operation %s requires path", path, op)
		}
		if !requiresField && strings.TrimSpace(s.Aggregate.Path) != "" {
			return fmt.Errorf("%s aggregate operation %s forbids path", path, op)
		}
		if s.Aggregate.Where != nil && strings.TrimSpace(s.Aggregate.Where.Path) == "" {
			return fmt.Errorf("%s.aggregate.where requires path", path)
		}
		if op == "CONTAINS_ALL" {
			if len(s.Aggregate.RequiredValues) == 0 {
				return fmt.Errorf("%s.aggregate.requiredValues is required for CONTAINS_ALL", path)
			}
			seen := map[string]bool{}
			for i, value := range s.Aggregate.RequiredValues {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("%s.aggregate.requiredValues[%d] must be non-empty", path, i)
				}
				if seen[value] {
					return fmt.Errorf("%s.aggregate.requiredValues[%d] is duplicated", path, i)
				}
				seen[value] = true
			}
		} else if len(s.Aggregate.RequiredValues) != 0 {
			return fmt.Errorf("%s.aggregate.requiredValues is only valid for CONTAINS_ALL", path)
		}
	default:
		return fmt.Errorf("%s source kind %q is unsupported", path, s.Kind)
	}
	return nil
}

func (d Document) validateSemantic() error {
	occurrences, err := d.semanticOccurrences()
	if err != nil {
		return err
	}
	if d.Population != nil {
		if err := d.Population.Validate(); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for i, column := range d.Columns {
		path := fmt.Sprintf("columns[%d]", i)
		if !physicalColumnPattern.MatchString(column.Column) {
			return fmt.Errorf("%s.column %q is not a valid physical column", path, column.Column)
		}
		if seen[column.Column] {
			return fmt.Errorf("duplicate column %q", column.Column)
		}
		seen[column.Column] = true
		if strings.TrimSpace(column.Label) == "" {
			return fmt.Errorf("%s.label is required", path)
		}
		if _, ok := occurrences[column.OccurrenceID]; !ok {
			return fmt.Errorf("%s references unknown occurrence %q", path, column.OccurrenceID)
		}
		if err := column.Source.validate(path + ".source"); err != nil {
			return err
		}
		if column.Contributor != nil {
			if column.Source.Kind != SourceAggregate {
				return fmt.Errorf("%s.contributor is only supported for aggregate sources", path)
			}
			if err := column.Contributor.Validate(); err != nil {
				return fmt.Errorf("%s.contributor: %w", path, err)
			}
		}
		if column.Source.Lookup != nil && column.Source.Lookup.Binding != nil {
			if _, err := fhirschema.ValidateCorrelatedBinding(occurrences[column.OccurrenceID].ResourceType, *column.Source.Lookup.Binding); err != nil {
				return fmt.Errorf("%s.source.binding: %w", path, err)
			}
		}
		if column.Source.Lookup != nil && column.Source.Lookup.Extension != nil {
			if _, err := fhirschema.ValidateExtensionBinding(occurrences[column.OccurrenceID].ResourceType, *column.Source.Lookup.Extension); err != nil {
				return fmt.Errorf("%s.source.extension: %w", path, err)
			}
		}
		if column.Table != nil && column.Table.Order != nil && *column.Table.Order < 0 {
			return fmt.Errorf("%s.table.order must not be negative", path)
		}
		if column.Table != nil && column.Table.CellRenderer != "" && column.Table.CellRenderer != "fileActions" {
			return fmt.Errorf("%s.table.cellRenderer is unsupported", path)
		}
		if column.Filter != nil && column.Filter.Order != nil && *column.Filter.Order < 0 {
			return fmt.Errorf("%s.filter.order must not be negative", path)
		}
		if column.Chart != nil {
			if strings.TrimSpace(column.Chart.Type) == "" {
				return fmt.Errorf("%s.chart.type is required", path)
			}
			if column.Chart.Order != nil && *column.Chart.Order < 0 {
				return fmt.Errorf("%s.chart.order must not be negative", path)
			}
		}
	}
	for i, fixed := range d.FixedFilters {
		if !seen[fixed.Column] || len(fixed.Values) == 0 {
			return fmt.Errorf("fixedFilters[%d] must reference a declared column and contain values", i)
		}
	}
	for i, action := range d.Actions {
		if strings.TrimSpace(action.Type) == "" || strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("actions[%d].type and title are required", i)
		}
		for j, binding := range action.Columns {
			if !seen[binding.Column] {
				return fmt.Errorf("actions[%d].columns[%d] references unknown column %q", i, j, binding.Column)
			}
		}
	}
	return nil
}

func (w Workspace) validateSemanticBindings() error {
	columns := map[string]map[string]bool{}
	for _, document := range w.Documents {
		columns[document.Output.ID] = map[string]bool{}
		for _, column := range document.Columns {
			columns[document.Output.ID][column.Column] = true
		}
	}
	for name, bindings := range w.SharedFilters {
		if strings.TrimSpace(name) == "" || len(bindings) == 0 {
			return fmt.Errorf("sharedFilters names and bindings must be non-empty")
		}
		for i, binding := range bindings {
			if !columns[binding.OutputID][binding.Column] {
				return fmt.Errorf("sharedFilters[%q][%d] references unknown output column", name, i)
			}
		}
	}
	if w.FileActions != nil {
		for extension, actions := range w.FileActions.Extensions {
			if strings.TrimSpace(extension) == "" || len(actions) == 0 {
				return fmt.Errorf("fileActions.extensions entries must be non-empty")
			}
			for _, action := range actions {
				if _, ok := w.FileActions.Actions[action]; !ok {
					return fmt.Errorf("fileActions extension %q references unknown action %q", extension, action)
				}
			}
		}
	}
	return nil
}
