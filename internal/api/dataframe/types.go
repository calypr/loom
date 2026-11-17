package dataframe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

type DataframeAggregateInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
	GroupBy   []string                `json:"groupBy,omitempty"`
	Filters   []*DataframeFilterInput `json:"filters,omitempty"`
	Operation string                  `json:"operation"`
	Column    *string                 `json:"column,omitempty"`
}

type DataframeAggregationSpecInput struct {
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	Column            string   `json:"column"`
	Size              *int     `json:"size,omitempty"`
	Interval          *float64 `json:"interval,omitempty"`
	DateInterval      *int     `json:"dateInterval,omitempty"`
	ExcludeSelfFilter *bool    `json:"excludeSelfFilter,omitempty"`
}

type DataframeAggregationsInput struct {
	ProjectID string                           `json:"projectId"`
	Selector  *DataframeSelectorInput          `json:"selector"`
	Filters   []*DataframeFilterInput          `json:"filters,omitempty"`
	Specs     []*DataframeAggregationSpecInput `json:"specs"`
}

type DataframeColumn struct {
	SemanticPath   string `json:"semanticPath"`
	Name           string `json:"name"`
	ClickhouseType string `json:"clickhouseType"`
	LogicalType    string `json:"logicalType"`
	Nullable       bool   `json:"nullable"`
	Repeated       bool   `json:"repeated"`
	Filterable     bool   `json:"filterable"`
	Sortable       bool   `json:"sortable"`
	Aggregatable   bool   `json:"aggregatable"`
}

type DataframeDatasetInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
}

type DataframeFilterInput struct {
	Column string          `json:"column"`
	Op     string          `json:"op"`
	Value  json.RawMessage `json:"value"`
}

type DataframeMaterialization struct {
	ID                string                        `json:"id"`
	Name              string                        `json:"name"`
	Revision          string                        `json:"revision"`
	ProjectID         string                        `json:"projectId"`
	DatasetGeneration string                        `json:"datasetGeneration"`
	State             DataframeMaterializationState `json:"state"`
	Columns           []*DataframeColumn            `json:"columns"`
	RowCount          *int                          `json:"rowCount"`
	CreatedAt         string                        `json:"createdAt"`
	ReadyAt           *string                       `json:"readyAt"`
	Error             *string                       `json:"error"`
	ErrorCode         *string                       `json:"errorCode"`
	ErrorRetryable    *bool                         `json:"errorRetryable"`
	Selector          *DataframeSelector            `json:"selector"`
}

type DataframeMaterializationState string

const (
	DataframeMaterializationStatePending DataframeMaterializationState = "PENDING"
	DataframeMaterializationStateLoading DataframeMaterializationState = "LOADING"
	DataframeMaterializationStateReady   DataframeMaterializationState = "READY"
	DataframeMaterializationStateFailed  DataframeMaterializationState = "FAILED"
)

var AllDataframeMaterializationState = []DataframeMaterializationState{
	DataframeMaterializationStatePending,
	DataframeMaterializationStateLoading,
	DataframeMaterializationStateReady,
	DataframeMaterializationStateFailed,
}

func (e DataframeMaterializationState) IsValid() bool {
	switch e {
	case DataframeMaterializationStatePending, DataframeMaterializationStateLoading, DataframeMaterializationStateReady, DataframeMaterializationStateFailed:
		return true
	}
	return false
}

func (e DataframeMaterializationState) String() string {
	return string(e)
}

func (e *DataframeMaterializationState) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = DataframeMaterializationState(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid DataframeMaterializationState", str)
	}
	return nil
}

func (e DataframeMaterializationState) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *DataframeMaterializationState) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e DataframeMaterializationState) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
}

type DataframeRowsInput struct {
	ProjectID string                  `json:"projectId"`
	Selector  *DataframeSelectorInput `json:"selector"`
	Columns   []string                `json:"columns,omitempty"`
	Filters   []*DataframeFilterInput `json:"filters,omitempty"`
	Sort      *DataframeSortInput     `json:"sort,omitempty"`
	First     *int                    `json:"first,omitempty"`
	After     *string                 `json:"after,omitempty"`
}

type DataframeSelector struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

type DataframeSelectorInput struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

type DataframeSortInput struct {
	Column string `json:"column"`
	Desc   *bool  `json:"desc,omitempty"`
}

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

func (e *FhirAggregateOperation) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirAggregateOperation(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirAggregateOperation", str)
	}
	return nil
}

func (e FhirAggregateOperation) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirAggregateOperation) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirAggregateOperation) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirColumnNaming) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirColumnNaming(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirColumnNaming", str)
	}
	return nil
}

func (e FhirColumnNaming) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirColumnNaming) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirColumnNaming) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirFieldPredicateOperation) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirFieldPredicateOperation(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFieldPredicateOperation", str)
	}
	return nil
}

func (e FhirFieldPredicateOperation) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirFieldPredicateOperation) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirFieldPredicateOperation) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirFilterOperator) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirFilterOperator(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterOperator", str)
	}
	return nil
}

func (e FhirFilterOperator) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirFilterOperator) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirFilterOperator) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirFilterQuantifier) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirFilterQuantifier(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterQuantifier", str)
	}
	return nil
}

func (e FhirFilterQuantifier) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirFilterQuantifier) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirFilterQuantifier) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirFilterValueKind) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirFilterValueKind(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirFilterValueKind", str)
	}
	return nil
}

func (e FhirFilterValueKind) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirFilterValueKind) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirFilterValueKind) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirTraversalMatchMode) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirTraversalMatchMode(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirTraversalMatchMode", str)
	}
	return nil
}

func (e FhirTraversalMatchMode) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirTraversalMatchMode) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirTraversalMatchMode) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
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

func (e *FhirValueMode) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("enums must be strings")
	}

	*e = FhirValueMode(str)
	if !e.IsValid() {
		return fmt.Errorf("%s is not a valid FhirValueMode", str)
	}
	return nil
}

func (e FhirValueMode) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(e.String()))
}

func (e *FhirValueMode) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return err
	}
	return e.UnmarshalGQL(s)
}

func (e FhirValueMode) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	e.MarshalGQL(&buf)
	return buf.Bytes(), nil
}

type DataframeRowConnection struct {
	Materialization *DataframeMaterialization `json:"materialization"`
	Columns         []string                  `json:"columns"`
	Rows            json.RawMessage           `json:"rows"`
	RowIds          []string                  `json:"rowIds"`
	TotalCount      *int                      `json:"totalCount"`
	PageInfo        *DataframePageInfo        `json:"pageInfo"`
}

type DataframePageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type DataframeAggregationsResult struct {
	Materialization *DataframeMaterialization `json:"materialization"`
	Aggregations    json.RawMessage           `json:"aggregations"`
}

type FhirDataframeResult struct {
	Columns     []string                   `json:"columns"`
	Rows        json.RawMessage            `json:"rows"`
	RowCount    int                        `json:"rowCount"`
	Diagnostics *DataframeQueryDiagnostics `json:"diagnostics"`
}

type DataframeQueryDiagnostics struct {
	InputResolutionMs    float64                           `json:"inputResolutionMs"`
	RequestPreparationMs float64                           `json:"requestPreparationMs"`
	CompilationMs        float64                           `json:"compilationMs"`
	ArangoQueryMs        float64                           `json:"arangoQueryMs"`
	RowMaterializationMs float64                           `json:"rowMaterializationMs"`
	ResultAssemblyMs     float64                           `json:"resultAssemblyMs"`
	TotalMs              float64                           `json:"totalMs"`
	Plan                 *DataframeCompilerPlanDiagnostics `json:"plan"`
}

type DataframeCompilerPlanDiagnostics struct {
	TraversalSets                     int                          `json:"traversalSets"`
	SharedTraversalCount              int                          `json:"sharedTraversalCount"`
	RequiredMatchReuseCount           int                          `json:"requiredMatchReuseCount"`
	ScopedSharingCandidateGroups      int                          `json:"scopedSharingCandidateGroups"`
	ScopedSharingCandidateSets        int                          `json:"scopedSharingCandidateSets"`
	PotentialSharingOpportunityGroups int                          `json:"potentialSharingOpportunityGroups"`
	PotentialSharingOpportunitySets   int                          `json:"potentialSharingOpportunitySets"`
	OptimizationPolicy                *DataframeOptimizationPolicy `json:"optimizationPolicy"`
	RichSourceReuse                   []*DataframeRichSourceReuse  `json:"richSourceReuse"`
}

type DataframeOptimizationPolicy struct {
	Name           string                           `json:"name"`
	Enabled        bool                             `json:"enabled"`
	MinimumSavings int                              `json:"minimumSavings"`
	Decisions      []*DataframeOptimizationDecision `json:"decisions"`
}

type DataframeOptimizationDecision struct {
	Rule                   string `json:"rule"`
	Enabled                bool   `json:"enabled"`
	CandidateSets          int    `json:"candidateSets"`
	EstimatedBaselineWork  int    `json:"estimatedBaselineWork"`
	EstimatedOptimizedWork int    `json:"estimatedOptimizedWork"`
	EstimatedSavings       int    `json:"estimatedSavings"`
	Reason                 string `json:"reason"`
}

type DataframeRichSourceReuse struct {
	SourceSet          string `json:"sourceSet"`
	AggregateConsumers int    `json:"aggregateConsumers"`
	PivotConsumers     int    `json:"pivotConsumers"`
	SliceConsumers     int    `json:"sliceConsumers"`
	TotalConsumers     int    `json:"totalConsumers"`
}
