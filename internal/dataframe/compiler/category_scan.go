package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const MaxCategoryScanValues = 256

const (
	categoryColumnBind = "__loom_category_column"
	categoryLimitBind  = "__loom_category_limit"
)

type CategoryScanRefusalCode string

const (
	CategoryScanColumnUnknown     CategoryScanRefusalCode = "CATEGORY_COLUMN_UNKNOWN"
	CategoryScanColumnInternal    CategoryScanRefusalCode = "CATEGORY_COLUMN_INTERNAL"
	CategoryScanColumnIdentity    CategoryScanRefusalCode = "CATEGORY_COLUMN_IDENTITY"
	CategoryScanColumnRepeated    CategoryScanRefusalCode = "CATEGORY_COLUMN_REPEATED"
	CategoryScanColumnUnsupported CategoryScanRefusalCode = "CATEGORY_COLUMN_UNSUPPORTED"
	CategoryScanInvalidLimit      CategoryScanRefusalCode = "CATEGORY_LIMIT_INVALID"
	CategoryScanStageUnknown      CategoryScanRefusalCode = "CATEGORY_STAGE_UNKNOWN"
)

type CategoryScanRefusal struct {
	Code   CategoryScanRefusalCode
	Column string
}

func (e *CategoryScanRefusal) Error() string {
	if e.Column == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Column)
}

func CategoryScanRefusalCodeOf(err error) (CategoryScanRefusalCode, bool) {
	var refusal *CategoryScanRefusal
	if !errors.As(err, &refusal) {
		return "", false
	}
	return refusal.Code, true
}

// CategoryScanProof binds a scan to the exact finalized output contract and
// executable query. It is immutable compiler output, suitable for inclusion
// in a later signed receipt without reconstructing either plan or schema.
type CategoryScanProof struct {
	Version            int
	Output             string
	StageID            string `json:",omitempty"`
	ColumnID           string `json:",omitempty"`
	ValueColumnID      string `json:",omitempty"`
	Column             string
	Kind               string
	Cardinality        string
	MaxValues          int
	OutputSchemaDigest string
	PlanFingerprint    string
	QueryFingerprint   string
	Fingerprint        string
}

type CompiledCategoryScanQuery struct {
	Query                string
	BindVars             map[string]any
	PresentColumn        string
	ValueColumn          string
	Proof                CategoryScanProof
	Diagnostics          ir.CompilerPlanDiagnostics
	PreviewCoveringIndex *PreviewCoveringIndexSpec
}

func CompileCategoryScanOutputWithPolicy(output lower.CompiledRecipeOutput, columnName string, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	columnName = strings.TrimSpace(columnName)
	column, err := categoryScanColumn(output.OutputSchema, columnName)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	return compileCategoryScan(output, output.OutputSchema, column, "", "", "", maxValues, policy)
}

// CompileCategoryScanStageWithPolicy compiles the exact prefix ending at a
// compiler-owned construction stage and binds the proof to the selected
// category and value columns. The value column is not read by the scan, but is
// included in the proof so a result cannot be reused for another pivot pair.
func CompileCategoryScanStageWithPolicy(output lower.CompiledRecipeOutput, stageID, categoryColumnID, valueColumnID string, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	stage, found := compiledStageByID(output.Stages, stageID)
	if !found {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
	}
	category, err := categoryScanStageColumn(stage.Columns, categoryColumnID)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	value, err := categoryScanStageColumn(stage.Columns, valueColumnID)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	if category.ID == value.ID {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanColumnUnsupported, Column: categoryColumnID}
	}
	if maxValues < 1 || maxValues > MaxCategoryScanValues {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanInvalidLimit, Column: category.Name}
	}

	stageOutput := output
	stageOutput.OutputSchema = lower.CloneCompiledOutputSchema(stage.Columns)
	stageOutput.OptimizedPlan = nil
	stageOutput.Plan = ir.ClonePhysicalPlan(output.Plan)
	if stageOutput.Plan.StageSequence != nil {
		sequence := stageOutput.Plan.StageSequence
		if stageID == sequence.SourceStageID {
			stageOutput.Plan.StageSequence = nil
		} else {
			stageIndex := -1
			for index := range sequence.Stages {
				if sequence.Stages[index].ID == stageID {
					stageIndex = index
					break
				}
			}
			if stageIndex < 0 {
				return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
			}
			sequence.Stages = sequence.Stages[:stageIndex+1]
			last := sequence.Stages[stageIndex]
			sequence.FinalStageID = last.ID
			sequence.FinalRowIdentity = last.RowIdentityColumn
			sequence.FinalColumns = append(sequence.FinalColumns[:0], last.OutputColumns...)
			sequence.PreviewLimitBindKey = ""
			sequence.PreviewTerminalPivotWindow = false
			sequence.CellTraceReturn = nil
		}
	} else if stageID != recipe.ConstructionSourceProjectionID {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
	}
	return compileCategoryScan(stageOutput, stageOutput.OutputSchema, category, stageID, category.ID, value.ID, maxValues, policy)
}

func compiledStageByID(stages []lower.CompiledStageDescriptor, stageID string) (lower.CompiledStageDescriptor, bool) {
	for _, stage := range stages {
		if stage.ID == stageID {
			return stage, true
		}
	}
	return lower.CompiledStageDescriptor{}, false
}

func categoryScanStageColumn(columns []lower.CompiledOutputColumn, id string) (lower.CompiledOutputColumn, error) {
	for _, column := range columns {
		if column.ID != id {
			continue
		}
		if _, err := categoryScanColumn([]lower.CompiledOutputColumn{column}, column.Name); err != nil {
			return column, err
		}
		return column, nil
	}
	return lower.CompiledOutputColumn{}, &CategoryScanRefusal{Code: CategoryScanColumnUnknown, Column: id}
}

func compileCategoryScan(output lower.CompiledRecipeOutput, schema []lower.CompiledOutputColumn, column lower.CompiledOutputColumn, stageID, categoryColumnID, valueColumnID string, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	if maxValues < 1 || maxValues > MaxCategoryScanValues {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanInvalidLimit, Column: column.Name}
	}

	physical, err := categoryScanPhysicalPlan(output, policy)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	previewCoveringIndex := categoryScanPreviewCoveringIndexSpec(physical, schema, stageID, categoryColumnID, valueColumnID)
	if stageID == recipe.ConstructionSourceProjectionID {
		physical = narrowSourceCategoryScanProjection(physical, column.Name)
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("apply category scan execution window: %w", err)
	}
	var rendered aql.RenderedPhysicalPlan
	if physical.StageSequence != nil {
		rendered, err = aql.RenderPhysicalPlanWithUnorderedTerminalProjection(physical, column.Name)
	} else {
		rendered, err = aql.RenderPhysicalPlan(physical)
	}
	if err != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("render category scan output plan: %w", err)
	}
	if _, exists := rendered.BindVars[categoryColumnBind]; exists {
		return CompiledCategoryScanQuery{}, fmt.Errorf("category scan bind %q is already defined", categoryColumnBind)
	}
	if _, exists := rendered.BindVars[categoryLimitBind]; exists {
		return CompiledCategoryScanQuery{}, fmt.Errorf("category scan bind %q is already defined", categoryLimitBind)
	}
	bindVars := cloneCategoryScanBinds(rendered.BindVars)
	bindVars[categoryColumnBind] = column.Name
	bindVars[categoryLimitBind] = maxValues + 1
	query := "LET __loom_category_rows = (\n" + rendered.Query + "\n)\n" +
		"FOR __loom_category_row IN __loom_category_rows\n" +
		"  LET __loom_category_present = HAS(__loom_category_row, @" + categoryColumnBind + ")\n" +
		"  LET __loom_category_value = __loom_category_present ? __loom_category_row[@" + categoryColumnBind + "] : null\n" +
		"  COLLECT __loom_category_group_present = __loom_category_present, __loom_category_group_value = __loom_category_value\n" +
		"  SORT __loom_category_group_present ASC, TYPENAME(__loom_category_group_value) ASC, __loom_category_group_value ASC\n" +
		"  LIMIT @" + categoryLimitBind + "\n" +
		"  RETURN { present: __loom_category_group_present, value: __loom_category_group_value }"

	diagnostics := physicalPlanDiagnostics(physical)
	schemaDigest, err := categoryHash(schema)
	if err != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("fingerprint category scan schema: %w", err)
	}
	queryDigest, err := categoryQueryFingerprint(query, bindVars)
	if err != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("fingerprint category scan query: %w", err)
	}
	proof := CategoryScanProof{
		Version: 1, Output: output.Name, Column: column.Name, Kind: column.Kind,
		Cardinality: column.Cardinality, MaxValues: maxValues,
		OutputSchemaDigest: schemaDigest, PlanFingerprint: diagnostics.Fingerprint,
		QueryFingerprint: queryDigest,
	}
	if stageID != "" {
		proof.Version = 2
		proof.StageID = stageID
		proof.ColumnID = categoryColumnID
		proof.ValueColumnID = valueColumnID
	}
	proof.Fingerprint, err = categoryHash(proof)
	if err != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("fingerprint category scan proof: %w", err)
	}
	return CompiledCategoryScanQuery{
		Query: query, BindVars: bindVars, PresentColumn: "present", ValueColumn: "value",
		Proof: proof, Diagnostics: diagnostics, PreviewCoveringIndex: previewCoveringIndex,
	}, nil
}

func narrowSourceCategoryScanProjection(plan ir.PhysicalPlan, categoryColumn string) ir.PhysicalPlan {
	if plan.StageSequence != nil || len(plan.Operations) == 0 {
		return plan
	}
	returnIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp {
			continue
		}
		if returnIndex >= 0 || operation.Return == nil || index != len(plan.Operations)-1 {
			return plan
		}
		returnIndex = index
	}
	if returnIndex < 0 {
		return plan
	}
	for _, projection := range plan.Operations[returnIndex].Return.Projections {
		if projection.Name != categoryColumn || projection.Hidden {
			continue
		}
		plan.Operations[returnIndex].Return.Projections = []ir.PhysicalProjection{projection}
		return plan
	}
	return plan
}

func categoryScanPreviewCoveringIndexSpec(plan ir.PhysicalPlan, schema []lower.CompiledOutputColumn, stageID, categoryColumnID, valueColumnID string) *PreviewCoveringIndexSpec {
	if stageID != recipe.ConstructionSourceProjectionID || categoryColumnID == "" || valueColumnID == "" || categoryColumnID == valueColumnID {
		return nil
	}
	categoryName, categoryFound := categoryScanSchemaColumnName(schema, categoryColumnID)
	valueName, valueFound := categoryScanSchemaColumnName(schema, valueColumnID)
	if !categoryFound || !valueFound {
		return nil
	}
	rootScan, sourceReturn, ok := previewCoveringIndexSource(plan)
	if !ok || rootScan.Population != nil {
		return nil
	}
	projectionPaths, ok := previewCoveringProjectionPaths(sourceReturn.Projections, rootScan.Variable)
	if !ok {
		return nil
	}
	projectionNames := make(map[string]struct{}, len(sourceReturn.Projections))
	for _, projection := range sourceReturn.Projections {
		projectionNames[projection.Name] = struct{}{}
	}
	if _, ok := projectionNames[categoryName]; !ok {
		return nil
	}
	if _, ok := projectionNames[valueName]; !ok {
		return nil
	}
	collection, ok := plan.BindVars[rootScan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return nil
	}

	return previewCoveringIndexSpecForSourcePaths(collection, projectionPaths)
}

func categoryScanSchemaColumnName(schema []lower.CompiledOutputColumn, id string) (string, bool) {
	for _, column := range schema {
		if column.ID == id && !column.Internal && !column.Identity {
			return column.Name, column.Name != ""
		}
	}
	return "", false
}

func categoryScanColumn(schema []lower.CompiledOutputColumn, name string) (lower.CompiledOutputColumn, error) {
	var column lower.CompiledOutputColumn
	found := false
	for _, candidate := range schema {
		if candidate.Name == name {
			column, found = candidate, true
			break
		}
	}
	if !found || name == "" {
		return column, &CategoryScanRefusal{Code: CategoryScanColumnUnknown, Column: name}
	}
	if column.Internal {
		return column, &CategoryScanRefusal{Code: CategoryScanColumnInternal, Column: name}
	}
	if column.Identity {
		return column, &CategoryScanRefusal{Code: CategoryScanColumnIdentity, Column: name}
	}
	if column.Cardinality == string(expression.Many) {
		return column, &CategoryScanRefusal{Code: CategoryScanColumnRepeated, Column: name}
	}
	if column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne) {
		return column, &CategoryScanRefusal{Code: CategoryScanColumnUnsupported, Column: name}
	}
	switch expression.ValueKind(column.Kind) {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return column, nil
	default:
		return column, &CategoryScanRefusal{Code: CategoryScanColumnUnsupported, Column: name}
	}
}

func categoryScanPhysicalPlan(output lower.CompiledRecipeOutput, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	if len(output.Plan.Operations) == 1 && output.Plan.Operations[0].Kind == ir.PhysicalGroupRowsOp {
		return clonePhysicalPlan(output.Plan), nil
	}
	if output.OptimizedPlan != nil {
		return clonePhysicalPlan(*output.OptimizedPlan), nil
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
	if err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("optimize category scan output plan: %w", err)
	}
	return physical, nil
}

func cloneCategoryScanBinds(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func categoryQueryFingerprint(query string, binds map[string]any) (string, error) {
	keys := make([]string, 0, len(binds))
	for key := range binds {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		values = append(values, key, binds[key])
	}
	return categoryHash(struct {
		Query string
		Binds []any
	}{query, values})
}

func categoryHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
