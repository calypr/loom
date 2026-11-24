package recipe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

type FhirAggregateInput struct {
	Name              string                 `json:"name"`
	Operation         FhirAggregateOperation `json:"operation"`
	FieldRef          *string                `json:"fieldRef,omitempty"`
	FhirPath          *string                `json:"fhirPath,omitempty"`
	PredicateFieldRef *string                `json:"predicateFieldRef,omitempty"`
	PredicatePath     *string                `json:"predicatePath,omitempty"`
	PredicateEquals   *string                `json:"predicateEquals,omitempty"`
	ValueMode         FhirValueMode          `json:"valueMode"`
}

type FhirAggregateOperation string

const (
	FhirAggregateOperationCount          FhirAggregateOperation = "COUNT"
	FhirAggregateOperationCountDistinct  FhirAggregateOperation = "COUNT_DISTINCT"
	FhirAggregateOperationExists         FhirAggregateOperation = "EXISTS"
	FhirAggregateOperationDistinctValues FhirAggregateOperation = "DISTINCT_VALUES"
)

var AllFhirAggregateOperation = []FhirAggregateOperation{
	FhirAggregateOperationCount,
	FhirAggregateOperationCountDistinct,
	FhirAggregateOperationExists,
	FhirAggregateOperationDistinctValues,
}

func (e FhirAggregateOperation) IsValid() bool {
	switch e {
	case FhirAggregateOperationCount, FhirAggregateOperationCountDistinct, FhirAggregateOperationExists, FhirAggregateOperationDistinctValues:
		return true
	}
	return false
}

func (e FhirAggregateOperation) String() string {
	return string(e)
}

func (e *FhirAggregateOperation) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirAggregateOperation(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirAggregateOperation", value)
	}
	*e = decoded
	return nil
}

type FhirCatalogProjectionInput struct {
	Name         string           `json:"name"`
	IncludePaths []string         `json:"includePaths"`
	ExcludePaths []string         `json:"excludePaths"`
	Kinds        []string         `json:"kinds"`
	Naming       FhirColumnNaming `json:"naming"`
	ValueMode    FhirValueMode    `json:"valueMode"`
	MaxColumns   int              `json:"maxColumns"`
}

type FhirCodeValueInput struct {
	System  *string `json:"system,omitempty"`
	Code    string  `json:"code"`
	Display *string `json:"display,omitempty"`
}

type FhirColumnNaming string

const (
	FhirColumnNamingPath       FhirColumnNaming = "PATH"
	FhirColumnNamingPathSuffix FhirColumnNaming = "PATH_SUFFIX"
)

var AllFhirColumnNaming = []FhirColumnNaming{
	FhirColumnNamingPath,
	FhirColumnNamingPathSuffix,
}

func (e FhirColumnNaming) IsValid() bool {
	switch e {
	case FhirColumnNamingPath, FhirColumnNamingPathSuffix:
		return true
	}
	return false
}

func (e FhirColumnNaming) String() string {
	return string(e)
}

func (e *FhirColumnNaming) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirColumnNaming(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirColumnNaming", value)
	}
	*e = decoded
	return nil
}

type FhirDataframeInput struct {
	Project                string                          `json:"project"`
	AuthResourcePath       *string                         `json:"authResourcePath,omitempty"`
	AuthResourcePaths      []string                        `json:"authResourcePaths,omitempty"`
	RootResourceType       string                          `json:"rootResourceType"`
	RowGrain               *string                         `json:"rowGrain,omitempty"`
	RootFields             []*FhirFieldSelectInput         `json:"rootFields,omitempty"`
	RootFilters            []*FhirFilterInput              `json:"rootFilters,omitempty"`
	RootPivots             []*FhirPivotInput               `json:"rootPivots,omitempty"`
	RootAggregates         []*FhirAggregateInput           `json:"rootAggregates,omitempty"`
	RootSlices             []*FhirRepresentativeSliceInput `json:"rootSlices,omitempty"`
	RootCatalogProjections []*FhirCatalogProjectionInput   `json:"rootCatalogProjections,omitempty"`
	Traverse               []*FhirTraversalStepInput       `json:"traverse,omitempty"`
	Limit                  *int                            `json:"limit,omitempty"`
	Cursor                 *string                         `json:"cursor,omitempty"`
}

type FhirFieldPredicateInput struct {
	Path  string                      `json:"path"`
	Op    FhirFieldPredicateOperation `json:"op"`
	Value string                      `json:"value"`
}

type FhirFieldPredicateOperation string

const (
	FhirFieldPredicateOperationContains FhirFieldPredicateOperation = "CONTAINS"
)

var AllFhirFieldPredicateOperation = []FhirFieldPredicateOperation{
	FhirFieldPredicateOperationContains,
}

func (e FhirFieldPredicateOperation) IsValid() bool {
	switch e {
	case FhirFieldPredicateOperationContains:
		return true
	}
	return false
}

func (e FhirFieldPredicateOperation) String() string {
	return string(e)
}

func (e *FhirFieldPredicateOperation) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirFieldPredicateOperation(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFieldPredicateOperation", value)
	}
	*e = decoded
	return nil
}

type FhirFieldSelectInput struct {
	Name              string                    `json:"name"`
	FieldRef          *string                   `json:"fieldRef,omitempty"`
	Selector          *FhirFieldSelectorInput   `json:"selector,omitempty"`
	SelectionHint     *string                   `json:"selectionHint,omitempty"`
	FallbackFieldRefs []string                  `json:"fallbackFieldRefs"`
	FallbackSelectors []*FhirFieldSelectorInput `json:"fallbackSelectors"`
	ValueMode         FhirValueMode             `json:"valueMode"`
}

type FhirFieldSelectorInput struct {
	SourcePath *string                  `json:"sourcePath,omitempty"`
	Where      *FhirFieldPredicateInput `json:"where,omitempty"`
	ValuePath  string                   `json:"valuePath"`
}

type FhirFilterInput struct {
	Select     string                  `json:"select"`
	FieldRef   *string                 `json:"fieldRef,omitempty"`
	Operator   FhirFilterOperator      `json:"operator"`
	Quantifier *FhirFilterQuantifier   `json:"quantifier,omitempty"`
	Values     []*FhirFilterValueInput `json:"values"`
}

type FhirFilterOperator string

const (
	FhirFilterOperatorEquals       FhirFilterOperator = "EQUALS"
	FhirFilterOperatorNotEquals    FhirFilterOperator = "NOT_EQUALS"
	FhirFilterOperatorIn           FhirFilterOperator = "IN"
	FhirFilterOperatorExists       FhirFilterOperator = "EXISTS"
	FhirFilterOperatorMissing      FhirFilterOperator = "MISSING"
	FhirFilterOperatorContainsText FhirFilterOperator = "CONTAINS_TEXT"
	FhirFilterOperatorGt           FhirFilterOperator = "GT"
	FhirFilterOperatorGte          FhirFilterOperator = "GTE"
	FhirFilterOperatorLt           FhirFilterOperator = "LT"
	FhirFilterOperatorLte          FhirFilterOperator = "LTE"
)

var AllFhirFilterOperator = []FhirFilterOperator{
	FhirFilterOperatorEquals,
	FhirFilterOperatorNotEquals,
	FhirFilterOperatorIn,
	FhirFilterOperatorExists,
	FhirFilterOperatorMissing,
	FhirFilterOperatorContainsText,
	FhirFilterOperatorGt,
	FhirFilterOperatorGte,
	FhirFilterOperatorLt,
	FhirFilterOperatorLte,
}

func (e FhirFilterOperator) IsValid() bool {
	switch e {
	case FhirFilterOperatorEquals, FhirFilterOperatorNotEquals, FhirFilterOperatorIn, FhirFilterOperatorExists, FhirFilterOperatorMissing, FhirFilterOperatorContainsText, FhirFilterOperatorGt, FhirFilterOperatorGte, FhirFilterOperatorLt, FhirFilterOperatorLte:
		return true
	}
	return false
}

func (e FhirFilterOperator) String() string {
	return string(e)
}

func (e *FhirFilterOperator) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirFilterOperator(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterOperator", value)
	}
	*e = decoded
	return nil
}

type FhirFilterQuantifier string

const (
	FhirFilterQuantifierAny  FhirFilterQuantifier = "ANY"
	FhirFilterQuantifierAll  FhirFilterQuantifier = "ALL"
	FhirFilterQuantifierNone FhirFilterQuantifier = "NONE"
)

var AllFhirFilterQuantifier = []FhirFilterQuantifier{
	FhirFilterQuantifierAny,
	FhirFilterQuantifierAll,
	FhirFilterQuantifierNone,
}

func (e FhirFilterQuantifier) IsValid() bool {
	switch e {
	case FhirFilterQuantifierAny, FhirFilterQuantifierAll, FhirFilterQuantifierNone:
		return true
	}
	return false
}

func (e FhirFilterQuantifier) String() string {
	return string(e)
}

func (e *FhirFilterQuantifier) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirFilterQuantifier(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterQuantifier", value)
	}
	*e = decoded
	return nil
}

type FhirFilterValueInput struct {
	Kind     FhirFilterValueKind `json:"kind"`
	String   *string             `json:"string,omitempty"`
	Code     *FhirCodeValueInput `json:"code,omitempty"`
	Boolean  *bool               `json:"boolean,omitempty"`
	Integer  *int                `json:"integer,omitempty"`
	Decimal  *float64            `json:"decimal,omitempty"`
	Date     *string             `json:"date,omitempty"`
	DateTime *string             `json:"dateTime,omitempty"`
}

type FhirFilterValueKind string

const (
	FhirFilterValueKindString   FhirFilterValueKind = "STRING"
	FhirFilterValueKindCode     FhirFilterValueKind = "CODE"
	FhirFilterValueKindBoolean  FhirFilterValueKind = "BOOLEAN"
	FhirFilterValueKindInteger  FhirFilterValueKind = "INTEGER"
	FhirFilterValueKindDecimal  FhirFilterValueKind = "DECIMAL"
	FhirFilterValueKindDate     FhirFilterValueKind = "DATE"
	FhirFilterValueKindDateTime FhirFilterValueKind = "DATE_TIME"
)

var AllFhirFilterValueKind = []FhirFilterValueKind{
	FhirFilterValueKindString,
	FhirFilterValueKindCode,
	FhirFilterValueKindBoolean,
	FhirFilterValueKindInteger,
	FhirFilterValueKindDecimal,
	FhirFilterValueKindDate,
	FhirFilterValueKindDateTime,
}

func (e FhirFilterValueKind) IsValid() bool {
	switch e {
	case FhirFilterValueKindString, FhirFilterValueKindCode, FhirFilterValueKindBoolean, FhirFilterValueKindInteger, FhirFilterValueKindDecimal, FhirFilterValueKindDate, FhirFilterValueKindDateTime:
		return true
	}
	return false
}

func (e FhirFilterValueKind) String() string {
	return string(e)
}

func (e *FhirFilterValueKind) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirFilterValueKind(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterValueKind", value)
	}
	*e = decoded
	return nil
}

type FhirPivotDiscoveryInput struct {
	Family     *string `json:"family,omitempty"`
	Path       *string `json:"path,omitempty"`
	MaxColumns int     `json:"maxColumns"`
}

type FhirPivotInput struct {
	Name           string                   `json:"name"`
	FieldRef       *string                  `json:"fieldRef,omitempty"`
	ColumnSelector *FhirFieldSelectorInput  `json:"columnSelector,omitempty"`
	ValueSelector  *FhirFieldSelectorInput  `json:"valueSelector,omitempty"`
	Columns        []string                 `json:"columns"`
	Discovery      *FhirPivotDiscoveryInput `json:"discovery,omitempty"`
}

type FhirRepresentativeSliceInput struct {
	Name          string                  `json:"name"`
	Limit         int                     `json:"limit"`
	WhereFieldRef *string                 `json:"whereFieldRef,omitempty"`
	WherePath     *string                 `json:"wherePath,omitempty"`
	WhereEquals   *string                 `json:"whereEquals,omitempty"`
	Fields        []*FhirFieldSelectInput `json:"fields"`
}

type FhirTraversalMatchMode string

const (
	FhirTraversalMatchModeOptional FhirTraversalMatchMode = "OPTIONAL"
	FhirTraversalMatchModeRequired FhirTraversalMatchMode = "REQUIRED"
)

var AllFhirTraversalMatchMode = []FhirTraversalMatchMode{
	FhirTraversalMatchModeOptional,
	FhirTraversalMatchModeRequired,
}

func (e FhirTraversalMatchMode) IsValid() bool {
	switch e {
	case FhirTraversalMatchModeOptional, FhirTraversalMatchModeRequired:
		return true
	}
	return false
}

func (e FhirTraversalMatchMode) String() string {
	return string(e)
}

func (e *FhirTraversalMatchMode) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirTraversalMatchMode(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirTraversalMatchMode", value)
	}
	*e = decoded
	return nil
}

type FhirTraversalStepInput struct {
	EdgeLabel          string                          `json:"edgeLabel"`
	ToResourceType     string                          `json:"toResourceType"`
	Alias              string                          `json:"alias"`
	MatchMode          *FhirTraversalMatchMode         `json:"matchMode,omitempty"`
	Fields             []*FhirFieldSelectInput         `json:"fields"`
	Filters            []*FhirFilterInput              `json:"filters,omitempty"`
	Pivots             []*FhirPivotInput               `json:"pivots,omitempty"`
	Aggregates         []*FhirAggregateInput           `json:"aggregates,omitempty"`
	Slices             []*FhirRepresentativeSliceInput `json:"slices,omitempty"`
	CatalogProjections []*FhirCatalogProjectionInput   `json:"catalogProjections,omitempty"`
	Traverse           []*FhirTraversalStepInput       `json:"traverse,omitempty"`
}

type FhirValueMode string

const (
	FhirValueModeAuto     FhirValueMode = "AUTO"
	FhirValueModeFirst    FhirValueMode = "FIRST"
	FhirValueModeAll      FhirValueMode = "ALL"
	FhirValueModeDistinct FhirValueMode = "DISTINCT"
)

var AllFhirValueMode = []FhirValueMode{
	FhirValueModeAuto,
	FhirValueModeFirst,
	FhirValueModeAll,
	FhirValueModeDistinct,
}

func (e FhirValueMode) IsValid() bool {
	switch e {
	case FhirValueModeAuto, FhirValueModeFirst, FhirValueModeAll, FhirValueModeDistinct:
		return true
	}
	return false
}

func (e FhirValueMode) String() string {
	return string(e)
}

func (e *FhirValueMode) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	decoded := FhirValueMode(value)
	if !decoded.IsValid() {
		return fmt.Errorf("%s is not a valid FhirValueMode", value)
	}
	*e = decoded
	return nil
}

func predicatePathFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Path
}

func predicateOpFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Op.String()
}

func predicateValueFromInput(input *FhirFieldPredicateInput) string {
	if input == nil {
		return ""
	}
	return input.Value
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func composeSelector(sourcePath string, wherePath string, whereOp string, whereValue string, valuePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	valuePath = strings.TrimSpace(valuePath)
	if valuePath == "" {
		return ""
	}
	path := valuePath
	if sourcePath != "" {
		path = sourcePath + "." + valuePath
	}
	wherePath = strings.TrimSpace(wherePath)
	whereOp = strings.TrimSpace(strings.ToUpper(whereOp))
	whereValue = strings.TrimSpace(whereValue)
	if wherePath == "" || whereOp == "" || whereValue == "" {
		return path
	}
	if whereOp == "CONTAINS" {
		return fmt.Sprintf(`%s where %s contains %q`, path, wherePath, whereValue)
	}
	return path
}

func (in FhirDataframeInput) Bundle() (Bundle, error) {
	if strings.TrimSpace(in.RootResourceType) == "" {
		return Bundle{}, fmt.Errorf("rootResourceType is required")
	}
	grain, ok := spec.InferRowGrain(in.RootResourceType)
	if in.RowGrain != nil {
		grain = spec.RowGrain(strings.TrimSpace(*in.RowGrain))
		ok = true
	}
	if !ok || strings.TrimSpace(string(grain)) == "" {
		return Bundle{}, fmt.Errorf("no row grain is available for root resource type %q", in.RootResourceType)
	}
	output, err := recipeOutputFromInput(in.RootResourceType, in.RootFields, in.RootFilters, in.RootPivots, in.RootAggregates, in.RootSlices, in.RootCatalogProjections, in.Traverse)
	if err != nil {
		return Bundle{}, err
	}
	output.Name = "dataframe"
	output.RowGrain = string(grain)
	bundle := Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion,
		Name:                "graphql_request",
		TranslationVersion:  "graphql-request",
		Outputs:             []Output{output},
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func recipeOutputFromInput(resourceType string, fields []*FhirFieldSelectInput, filters []*FhirFilterInput, pivots []*FhirPivotInput, aggregates []*FhirAggregateInput, slices []*FhirRepresentativeSliceInput, catalogProjections []*FhirCatalogProjectionInput, traversals []*FhirTraversalStepInput) (Output, error) {
	rf, err := recipeFieldsFromInput(fields)
	if err != nil {
		return Output{}, fmt.Errorf("root fields: %w", err)
	}
	rfilt, err := recipeFiltersFromInput(filters)
	if err != nil {
		return Output{}, fmt.Errorf("root filters: %w", err)
	}
	rp, err := recipePivotsFromInput(pivots)
	if err != nil {
		return Output{}, fmt.Errorf("root pivots: %w", err)
	}
	ra, err := recipeAggregatesFromInput(aggregates)
	if err != nil {
		return Output{}, fmt.Errorf("root aggregates: %w", err)
	}
	rs, err := recipeSlicesFromInput(slices)
	if err != nil {
		return Output{}, fmt.Errorf("root slices: %w", err)
	}
	rcp, err := recipeCatalogProjectionsFromInput(catalogProjections)
	if err != nil {
		return Output{}, fmt.Errorf("catalog projections: %w", err)
	}
	rt, err := recipeTraversalsFromInput(traversals)
	if err != nil {
		return Output{}, fmt.Errorf("traversals: %w", err)
	}
	return Output{RootResourceType: resourceType, Fields: rf, Filters: rfilt, Pivots: rp, Aggregates: ra, Slices: rs, CatalogProjections: rcp, Traversals: rt}, nil
}

func recipeCatalogProjectionsFromInput(in []*FhirCatalogProjectionInput) ([]CatalogProjection, error) {
	out := make([]CatalogProjection, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		projection := CatalogProjection{
			Name: item.Name, IncludePaths: cloneStrings(item.IncludePaths), ExcludePaths: cloneStrings(item.ExcludePaths),
			Kinds: cloneStrings(item.Kinds), Naming: ColumnNaming(item.Naming.String()), ValueMode: ValueMode(item.ValueMode.String()), MaxColumns: item.MaxColumns,
		}
		if err := projection.Validate(); err != nil {
			return nil, fmt.Errorf("catalogProjections[%d] %q: %w", index, item.Name, err)
		}
		out = append(out, projection)
	}
	return out, nil
}

func recipeFieldsFromInput(in []*FhirFieldSelectInput) ([]Field, error) {
	out := make([]Field, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		expr, err := recipeSelectorExpression(item.Selector)
		if err != nil {
			return nil, fmt.Errorf("fields[%d] %q: %w", index, item.Name, err)
		}
		field := Field{Name: item.Name, FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), Expr: expr, ValueMode: ValueMode(item.ValueMode.String())}
		for fallbackIndex, fallback := range item.FallbackSelectors {
			fallbackExpr, err := recipeSelectorExpression(fallback)
			if err != nil {
				return nil, fmt.Errorf("fields[%d] %q fallback[%d]: %w", index, item.Name, fallbackIndex, err)
			}
			field.Fallbacks = append(field.Fallbacks, fallbackExpr)
		}
		if len(item.FallbackFieldRefs) > 0 && len(item.FallbackFieldRefs) != len(field.Fallbacks) {
			return nil, fmt.Errorf("fields[%d] %q has unresolved fallback field references", index, item.Name)
		}
		out = append(out, field)
	}
	return out, nil
}

func recipeFiltersFromInput(in []*FhirFilterInput) ([]Filter, error) {
	out := make([]Filter, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		filter := Filter{
			Select:   strings.TrimSpace(item.Select),
			FieldRef: strings.TrimSpace(optionalString(item.FieldRef)),
			Operator: spec.FilterOperator(item.Operator.String()),
		}
		if item.Quantifier != nil {
			filter.Quantifier = ArrayQuantifier(item.Quantifier.String())
		}
		for valueIndex, value := range item.Values {
			converted, err := recipeFilterValue(value)
			if err != nil {
				return nil, fmt.Errorf("filters[%d].values[%d]: %w", index, valueIndex, err)
			}
			filter.Values = append(filter.Values, converted)
		}
		out = append(out, filter)
	}
	return out, nil
}

func recipeFilterValue(input *FhirFilterValueInput) (spec.FilterValue, error) {
	if input == nil {
		return spec.FilterValue{}, fmt.Errorf("value is required")
	}
	value := spec.FilterValue{Kind: spec.FilterValueKind(input.Kind.String())}
	switch input.Kind {
	case FhirFilterValueKindString:
		value.String = input.String
	case FhirFilterValueKindCode:
		if input.Code != nil {
			value.Code = &CodeValue{System: optionalString(input.Code.System), Code: input.Code.Code, Display: optionalString(input.Code.Display)}
		}
	case FhirFilterValueKindBoolean:
		value.Boolean = input.Boolean
	case FhirFilterValueKindInteger:
		if input.Integer != nil {
			converted := int64(*input.Integer)
			value.Integer = &converted
		}
	case FhirFilterValueKindDecimal:
		value.Decimal = input.Decimal
	case FhirFilterValueKindDate:
		value.Date = input.Date
	case FhirFilterValueKindDateTime:
		value.DateTime = input.DateTime
	default:
		return spec.FilterValue{}, fmt.Errorf("unsupported value kind %q", input.Kind)
	}
	if err := value.Validate(); err != nil {
		return spec.FilterValue{}, err
	}
	return value, nil
}

func recipePivotsFromInput(in []*FhirPivotInput) ([]Pivot, error) {
	out := make([]Pivot, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		column, err := recipeSelectorExpression(item.ColumnSelector)
		if err != nil {
			return nil, fmt.Errorf("pivots[%d] %q columnSelector: %w", index, item.Name, err)
		}
		value, err := recipeSelectorExpression(item.ValueSelector)
		if err != nil {
			return nil, fmt.Errorf("pivots[%d] %q valueSelector: %w", index, item.Name, err)
		}
		pivot := Pivot{Name: item.Name, FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), ColumnExpr: column, ValueExpr: value, Columns: cloneStrings(item.Columns)}
		if item.Discovery != nil {
			pivot.Discovery = &PivotDiscovery{Family: optionalString(item.Discovery.Family), Path: optionalString(item.Discovery.Path), MaxColumns: item.Discovery.MaxColumns}
		}
		out = append(out, pivot)
	}
	return out, nil
}

func recipeAggregatesFromInput(in []*FhirAggregateInput) ([]Aggregate, error) {
	out := make([]Aggregate, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		aggregate := Aggregate{Name: item.Name, Operation: AggregateOperation(item.Operation.String()), FieldRef: strings.TrimSpace(optionalString(item.FieldRef)), ValueMode: ValueMode(item.ValueMode.String())}
		path := strings.TrimSpace(optionalString(item.FhirPath))
		if path != "" {
			expr := Expression{Select: path}
			aggregate.Expr = &expr
		}
		predicatePath := strings.TrimSpace(optionalString(item.PredicatePath))
		predicateValue := optionalString(item.PredicateEquals)
		if predicatePath != "" || strings.TrimSpace(predicateValue) != "" {
			if predicatePath == "" || strings.TrimSpace(predicateValue) == "" {
				return nil, fmt.Errorf("aggregates[%d] %q has an incomplete predicate", index, item.Name)
			}
			aggregate.Where = recipeStringEqualityFilter(predicatePath, predicateValue)
		}
		out = append(out, aggregate)
	}
	return out, nil
}

func recipeSlicesFromInput(in []*FhirRepresentativeSliceInput) ([]RepresentativeSlice, error) {
	out := make([]RepresentativeSlice, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		fields, err := recipeFieldsFromInput(item.Fields)
		if err != nil {
			return nil, fmt.Errorf("slices[%d] %q fields: %w", index, item.Name, err)
		}
		slice := RepresentativeSlice{Name: item.Name, Limit: item.Limit, Fields: fields}
		wherePath := strings.TrimSpace(optionalString(item.WherePath))
		whereValue := optionalString(item.WhereEquals)
		if wherePath != "" || strings.TrimSpace(whereValue) != "" {
			if wherePath == "" || strings.TrimSpace(whereValue) == "" {
				return nil, fmt.Errorf("slices[%d] %q has an incomplete predicate", index, item.Name)
			}
			slice.Where = recipeStringEqualityFilter(wherePath, whereValue)
		}
		out = append(out, slice)
	}
	return out, nil
}

func recipeTraversalsFromInput(in []*FhirTraversalStepInput) ([]Traversal, error) {
	out := make([]Traversal, 0, len(in))
	for index, item := range in {
		if item == nil {
			continue
		}
		output, err := recipeOutputFromInput(item.ToResourceType, item.Fields, item.Filters, item.Pivots, item.Aggregates, item.Slices, item.CatalogProjections, item.Traverse)
		if err != nil {
			return nil, fmt.Errorf("traversals[%d] %q: %w", index, item.Alias, err)
		}
		matchMode := MatchOptional
		if item.MatchMode != nil {
			matchMode = TraversalMatchMode(item.MatchMode.String())
		}
		out = append(out, Traversal{Name: item.EdgeLabel, ToResourceType: item.ToResourceType, Alias: item.Alias, MatchMode: matchMode, Fields: output.Fields, Filters: output.Filters, Pivots: output.Pivots, Aggregates: output.Aggregates, Slices: output.Slices, CatalogProjections: output.CatalogProjections, Traversals: output.Traversals})
	}
	return out, nil
}

func recipeSelectorExpression(in *FhirFieldSelectorInput) (Expression, error) {
	if in == nil {
		return Expression{}, fmt.Errorf("selector is required")
	}
	selectText := composeSelector(optionalString(in.SourcePath), predicatePathFromInput(in.Where), predicateOpFromInput(in.Where), predicateValueFromInput(in.Where), in.ValuePath)
	if strings.TrimSpace(selectText) == "" {
		return Expression{}, fmt.Errorf("selector valuePath is required")
	}
	return Expression{Select: selectText}, nil
}

func recipeStringEqualityFilter(path, value string) *Filter {
	valueCopy := value
	return &Filter{Select: path, Operator: FilterEquals, Values: []spec.FilterValue{{Kind: FilterString, String: &valueCopy}}}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
