package recipe

import (
	"encoding/json"
	"fmt"
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
