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
	Version                    int
	Output                     string
	StageID                    string `json:",omitempty"`
	ColumnID                   string `json:",omitempty"`
	ValueColumnID              string `json:",omitempty"`
	Column                     string
	Kind                       string
	Cardinality                string
	MaxValues                  int
	OutputSchemaDigest         string
	PlanFingerprint            string
	QueryFingerprint           string
	OverflowWitnessFingerprint string `json:",omitempty"`
	PresenceTracked            bool   `json:",omitempty"`
	Fingerprint                string
}

type CategoryOverflowWitness struct {
	Query    string
	BindVars map[string]any
}

type CompiledCategoryScanQuery struct {
	Query                string
	BindVars             map[string]any
	PresentColumn        string
	ValueColumn          string
	Proof                CategoryScanProof
	Diagnostics          ir.CompilerPlanDiagnostics
	CategoryIndex        *PreviewCoveringIndexSpec
	PreviewCoveringIndex *PreviewCoveringIndexSpec
	OverflowWitness      *CategoryOverflowWitness
}

func CompileCategoryScanOutputWithPolicy(output lower.CompiledRecipeOutput, columnName string, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	columnName = strings.TrimSpace(columnName)
	column, err := categoryScanColumn(output.OutputSchema, columnName)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	return compileCategoryScan(output, output.OutputSchema, column, lower.CompiledOutputColumn{}, "", "", "", maxValues, policy)
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
			sequence.PreviewSourceWindowByRootID = false
			sequence.PreviewTerminalPivotWindow = false
			sequence.CellTraceReturn = nil
		}
	} else if stageID != recipe.ConstructionSourceProjectionID {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanStageUnknown, Column: stageID}
	}
	return compileCategoryScan(stageOutput, stageOutput.OutputSchema, category, value, stageID, category.ID, value.ID, maxValues, policy)
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

func compileCategoryScan(output lower.CompiledRecipeOutput, schema []lower.CompiledOutputColumn, column, valueColumn lower.CompiledOutputColumn, stageID, categoryColumnID, valueColumnID string, maxValues int, policy ir.PhysicalOptimizationPolicy) (CompiledCategoryScanQuery, error) {
	if maxValues < 1 || maxValues > MaxCategoryScanValues {
		return CompiledCategoryScanQuery{}, &CategoryScanRefusal{Code: CategoryScanInvalidLimit, Column: column.Name}
	}

	physical, err := categoryScanPhysicalPlan(output, policy)
	if err != nil {
		return CompiledCategoryScanQuery{}, err
	}
	previewCoveringIndex := categoryScanPreviewCoveringIndexSpec(physical, schema, stageID, categoryColumnID, valueColumnID)
	presenceMarkerColumn := categoryScanPresenceMarkerColumn(physical, schema)
	var query string
	var bindVars map[string]any
	presenceColumnBind := ""
	var overflowWitness *CategoryOverflowWitness
	categoryIndex := categoryScanCategoryIndexSpec(physical, column)
	relatedQuery, relatedBinds, relatedIndex, relatedEligible, relatedErr :=
		compileRelatedCategoryScan(physical, stageID, categoryColumnID, valueColumnID, column, valueColumn, maxValues)
	if relatedErr != nil {
		return CompiledCategoryScanQuery{}, fmt.Errorf("compile related category scan: %w", relatedErr)
	}
	if relatedEligible {
		query, bindVars, categoryIndex = relatedQuery, relatedBinds, relatedIndex
	} else {
		sourceProjectionEquivalent := stageID == recipe.ConstructionSourceProjectionID ||
			categoryScanPreservesSourceCategories(physical, stageID, categoryColumnID, column) ||
			(stageID == "" && categoryScanDirectRootSource(physical, column))
		if sourceProjectionEquivalent {
			// The proof remains bound to the requested terminal stage, while the
			// category query uses the cheaper source projection after proving that
			// every row in the stage prefix still represents a source parent and
			// carries this exact root category unchanged.
			physical.StageSequence = nil
			physical = narrowSourceCategoryScanProjection(physical, column.Name)
		} else {
			physical = withCategoryScanSourceFilterPushdown(physical)
		}
		physical, err = withGenericPhysicalExecutionWindow(physical, 0)
		if err != nil {
			return CompiledCategoryScanQuery{}, fmt.Errorf("apply category scan execution window: %w", err)
		}
		if sourceProjectionEquivalent {
			physical = withoutCategoryScanRootIdentitySort(physical)
		}
		streamRootCategories := sourceProjectionEquivalent && categoryScanDirectRootSource(physical, column)
		categoryIndex = categoryScanCategoryIndexSpec(physical, column)
		if streamRootCategories && categoryIndex != nil {
			query, bindVars, overflowWitness, err = categoryScanIndexedRootQueries(physical, column, categoryIndex, maxValues)
			if err != nil {
				return CompiledCategoryScanQuery{}, fmt.Errorf("compile indexed root category scan: %w", err)
			}
		} else {
			var rendered aql.RenderedPhysicalPlan
			if column.PresenceCompanionName != "" && !streamRootCategories {
				rendered, err = aql.RenderPhysicalPlan(physical)
			} else if physical.StageSequence != nil {
				rendered, err = aql.RenderPhysicalPlanWithUnorderedTerminalProjectionPresenceMarker(physical, column.Name, presenceMarkerColumn)
			} else {
				rendered, err = aql.RenderPhysicalPlanWithCategoryScanPresenceMarker(physical, column.Name, presenceMarkerColumn)
			}
			if err != nil {
				return CompiledCategoryScanQuery{}, fmt.Errorf("render category scan output plan: %w", err)
			}
			if categoryIndex != nil {
				root, _, ok := previewCoveringIndexSource(physical)
				if !ok {
					return CompiledCategoryScanQuery{}, fmt.Errorf("category index requires one direct root scan")
				}
				rendered.Query, ok = categoryScanWithRootIndexHint(rendered.Query, root.Variable, root.CollectionBindKey, categoryIndex.Name)
				if !ok {
					return CompiledCategoryScanQuery{}, fmt.Errorf("category index hint could not be attached to root scan")
				}
			}
			if _, exists := rendered.BindVars[categoryColumnBind]; exists {
				return CompiledCategoryScanQuery{}, fmt.Errorf("category scan bind %q is already defined", categoryColumnBind)
			}
			if _, exists := rendered.BindVars[categoryLimitBind]; exists {
				return CompiledCategoryScanQuery{}, fmt.Errorf("category scan bind %q is already defined", categoryLimitBind)
			}
			bindVars = cloneCategoryScanBinds(rendered.BindVars)
			var streamed bool
			if column.PresenceCompanionName != "" && !streamRootCategories {
				presenceColumnBind = "__loom_category_presence_column"
				if _, exists := bindVars[presenceColumnBind]; exists {
					return CompiledCategoryScanQuery{}, fmt.Errorf("category presence bind %q is already defined", presenceColumnBind)
				}
				query, streamed = categoryScanDistinctQueryFromPresenceColumn(rendered.Query, presenceColumnBind, categoryColumnBind, categoryLimitBind)
				bindVars[presenceColumnBind] = column.PresenceCompanionName
			} else {
				query, streamed = categoryScanDistinctQuery(rendered.Query, presenceMarkerColumn, categoryColumnBind, categoryLimitBind, streamRootCategories)
			}
			bindVars[categoryColumnBind] = column.Name
			bindVars[categoryLimitBind] = maxValues + 1
			if streamRootCategories && !streamed {
				return CompiledCategoryScanQuery{}, fmt.Errorf("stream direct source category scan: terminal RETURN projection was not found")
			}
		}
		if physical.StageSequence != nil {
			witness, eligible, witnessErr := aql.RenderRelatedEligibilityCategoryOverflowWitness(physical, column.Name, presenceMarkerColumn, maxValues*4+1)
			if witnessErr != nil {
				return CompiledCategoryScanQuery{}, fmt.Errorf("render category overflow witness: %w", witnessErr)
			}
			if eligible && column.PresenceCompanionName == "" {
				witnessQuery := "LET __loom_witness_rows = (\n" + witness.Query + "\n)\n" +
					"FOR __loom_witness_row IN __loom_witness_rows\n" +
					fmt.Sprintf("  LET __loom_witness_present = __loom_witness_row[%q]\n", presenceMarkerColumn) +
					"  LET __loom_witness_value = __loom_witness_present ? __loom_witness_row[@" + categoryColumnBind + "] : null\n" +
					"  COLLECT present = __loom_witness_present, value = __loom_witness_value\n" +
					"  LIMIT @" + categoryLimitBind + "\n" +
					"  RETURN { present, value }"
				witnessBinds := cloneCategoryScanBinds(witness.BindVars)
				witnessBinds[categoryColumnBind] = column.Name
				witnessBinds[categoryLimitBind] = maxValues + 1
				overflowWitness = &CategoryOverflowWitness{Query: witnessQuery, BindVars: witnessBinds}
			}
		}
	}

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
		QueryFingerprint: queryDigest, PresenceTracked: column.PresenceCompanionName != "" || relatedEligible || categoryScanDirectRootSource(physical, column),
	}
	if overflowWitness != nil {
		proof.OverflowWitnessFingerprint, err = categoryQueryFingerprint(overflowWitness.Query, overflowWitness.BindVars)
		if err != nil {
			return CompiledCategoryScanQuery{}, fmt.Errorf("fingerprint category overflow witness: %w", err)
		}
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
		Proof: proof, Diagnostics: diagnostics, CategoryIndex: categoryIndex,
		PreviewCoveringIndex: previewCoveringIndex, OverflowWitness: overflowWitness,
	}, nil
}

// categoryScanPreservesSourceCategories proves that complete distinct values
// at a construction stage equal the values on its source parents. It only
// accepts a direct scalar source projection and a contiguous prefix made of
// parent-preserving RELATED_EXPAND and row-preserving RELATED_FIELD stages.
func categoryScanPreservesSourceCategories(plan ir.PhysicalPlan, stageID, categoryColumnID string, category lower.CompiledOutputColumn) bool {
	sequence := plan.StageSequence
	if sequence == nil || stageID == "" || stageID == sequence.SourceStageID || sequence.FinalStageID != stageID ||
		len(sequence.Stages) == 0 || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow || sequence.OutputAuthResourcePathBindKey != "" {
		return false
	}
	if category.ID != categoryColumnID || category.Name == "" {
		return false
	}

	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok {
		return false
	}
	var sourceCategory ir.PhysicalStageColumn
	if !uniquePhysicalStageColumn(sequence.SourceColumns, categoryColumnID, category.Name, &sourceCategory) ||
		!categoryScanDirectScalarStageColumn(sourceCategory) || sourceCategory.NormalizedUnit != nil || sourceCategory.RelatedRecordAnchor != nil {
		return false
	}
	var sourceProjection *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if sourceProjection != nil || projection.Hidden {
			return false
		}
		sourceProjection = projection
	}
	if sourceProjection == nil {
		return false
	}
	value, direct := categoryScanDirectRootProjectionValue(*sourceProjection, root.Variable)
	if !direct || !categoryScanProjectionPresenceMatches(*sourceProjection, root.Variable, value) {
		return false
	}

	priorStageID := sequence.SourceStageID
	seenRelatedExpand := false
	for _, stage := range sequence.Stages {
		if stage.ID == "" || stage.InputStageID != priorStageID || stage.Filter != nil || len(stage.DerivedLets) != 0 {
			return false
		}
		var inputCategory, outputCategory ir.PhysicalStageColumn
		if !uniquePhysicalStageColumn(stage.InputColumns, categoryColumnID, category.Name, &inputCategory) ||
			!uniquePhysicalStageColumn(stage.OutputColumns, categoryColumnID, category.Name, &outputCategory) ||
			!sameCategoryScanColumn(inputCategory, sourceCategory) || !sameCategoryScanColumn(outputCategory, sourceCategory) ||
			!categoryScanPassesThroughProjection(stage, inputCategory) {
			return false
		}
		switch stage.Kind {
		case ir.PhysicalStageRelatedExpandOp:
			if stage.RelatedExpand == nil || stage.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || stage.RelatedField != nil {
				return false
			}
			seenRelatedExpand = true
		case ir.PhysicalStageRelatedFieldOp:
			if !seenRelatedExpand || stage.RelatedField == nil || stage.RelatedField.OutputColumnID == categoryColumnID || stage.RelatedExpand != nil {
				return false
			}
		default:
			return false
		}
		priorStageID = stage.ID
	}
	return seenRelatedExpand
}

func uniquePhysicalStageColumn(columns []ir.PhysicalStageColumn, id, name string, result *ir.PhysicalStageColumn) bool {
	found := false
	for _, column := range columns {
		if column.ID != id && column.Name != name {
			continue
		}
		if column.ID != id || column.Name != name || found {
			return false
		}
		*result = column
		found = true
	}
	return found
}

func sameCategoryScanColumn(column, source ir.PhysicalStageColumn) bool {
	return column.ID == source.ID && column.Name == source.Name && column.Kind == source.Kind &&
		column.Cardinality == source.Cardinality && column.Nullable == source.Nullable &&
		column.Internal == source.Internal && column.Identity == source.Identity &&
		column.NormalizedUnit == nil && column.RelatedRecordAnchor == nil
}

func categoryScanPassesThroughProjection(stage ir.PhysicalConstructionStage, input ir.PhysicalStageColumn) bool {
	if stage.InputRowVariable == "" {
		return false
	}
	found := false
	for _, projection := range stage.OutputProjections {
		if projection.Name != input.Name {
			continue
		}
		if found || projection.Hidden || projection.Expression != nil || projection.Presence != nil ||
			projection.Value.Variable != stage.InputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != input.Name {
			return false
		}
		found = true
	}
	return found
}

func categoryScanDirectRootSource(plan ir.PhysicalPlan, category lower.CompiledOutputColumn) bool {
	if plan.StageSequence != nil || !categoryScanDirectScalarColumn(category) {
		return false
	}
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Variable == "" {
		return false
	}
	var selected *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if selected != nil || projection.Hidden {
			return false
		}
		selected = projection
	}
	if selected == nil {
		return false
	}
	value, direct := categoryScanDirectRootProjectionValue(*selected, root.Variable)
	return direct && categoryScanProjectionPresenceMatches(*selected, root.Variable, value)
}

func categoryScanRootProjectionSource(plan ir.PhysicalPlan) (*ir.PhysicalRootScan, *ir.PhysicalReturn, bool) {
	if len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return nil, nil, false
	}
	last := len(plan.Operations) - 1
	if plan.Operations[last].Kind != ir.PhysicalReturnOp || plan.Operations[last].Return == nil {
		return nil, nil, false
	}
	root := plan.Operations[0].RootScan
	correlatedVariables := map[string]struct{}{root.Variable: {}}
	for _, operation := range plan.Operations[1:last] {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return nil, nil, false
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return nil, nil, false
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil {
				return nil, nil, false
			}
		case ir.PhysicalSetOp:
			if operation.Set == nil || !categoryScanSetIsScoped(*operation.Set, correlatedVariables) {
				return nil, nil, false
			}
			set := operation.Set
			correlatedVariables[set.Variable] = struct{}{}
			if set.Prepared != nil {
				correlatedVariables[set.Prepared.Variable] = struct{}{}
			}
		default:
			// Top-level traversals and unnests change row grain; only nested
			// correlated sets are safe before a direct root projection.
			return nil, nil, false
		}
	}
	return root, plan.Operations[last].Return, true
}

func categoryScanSetIsScoped(set ir.PhysicalSet, priorVariables map[string]struct{}) bool {
	if set.Variable == "" {
		return false
	}
	if set.SourceSetVariable != "" {
		_, sourceFound := priorVariables[set.SourceSetVariable]
		return sourceFound && set.ItemVariable != "" && len(set.Subplan.Captures) == 1 && set.Subplan.Captures[0] == set.SourceSetVariable
	}
	if len(set.Subplan.Operations) == 0 || set.Subplan.Operations[0].Kind != ir.PhysicalTraversalOp || set.Subplan.Operations[0].Traversal == nil || len(set.Subplan.Captures) == 0 {
		return false
	}
	traversalSource := set.Subplan.Operations[0].Traversal.SourceVariable
	if _, found := priorVariables[traversalSource]; !found {
		return false
	}
	for _, capture := range set.Subplan.Captures {
		if _, found := priorVariables[capture]; !found {
			return false
		}
	}
	return true
}

func categoryScanProjectionPresenceMatches(projection ir.PhysicalProjection, rootVariable string, value ir.PhysicalValue) bool {
	presence := projection.Presence
	if presence == nil || presence.Source.Variable != rootVariable || presence.Source.BindKey != "" || len(presence.Source.Path) == 0 ||
		len(presence.Paths) != 1 || len(presence.Paths[0]) == 0 {
		return false
	}
	path := append(append([]string(nil), presence.Source.Path...), presence.Paths[0]...)
	if presence.Source.Path[0] != "payload" || value.Variable != rootVariable || value.BindKey != "" || len(path) != len(value.Path) {
		return false
	}
	for index := range path {
		if path[index] != value.Path[index] {
			return false
		}
	}
	return true
}

func categoryScanDirectScalarColumn(column lower.CompiledOutputColumn) bool {
	if column.Internal || column.Identity || column.Name == "" ||
		(column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne)) {
		return false
	}
	switch expression.ValueKind(column.Kind) {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return true
	default:
		return false
	}
}

func categoryScanCategoryIndexSpec(plan ir.PhysicalPlan, category lower.CompiledOutputColumn) *PreviewCoveringIndexSpec {
	if !categoryScanDirectRootSource(plan, category) {
		return nil
	}
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Population != nil {
		return nil
	}
	var selected *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		projection := &sourceReturn.Projections[index]
		if projection.Name != category.Name {
			continue
		}
		if selected != nil || projection.Hidden {
			return nil
		}
		selected = projection
	}
	if selected == nil {
		return nil
	}
	path, ok := previewCoveringProjectionPath(*selected, root.Variable)
	if !ok || !validPreviewIndexPath(strings.Split(path, ".")) {
		return nil
	}
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return nil
	}
	fields := []string{"project", "dataset_generation", path, "auth_resource_path"}
	var supersedes *PreviewCoveringIndexReplacement
	rootType := strings.TrimSpace(plan.Source.ResourceType)
	if rootType != "" && collection == rootType {
		legacyFields := append([]string(nil), fields...)
		fields = []string{"project", "dataset_generation", "resourceType", path, "auth_resource_path"}
		supersedes = &PreviewCoveringIndexReplacement{
			Name:   previewCoveringIndexName(collection, legacyFields),
			Fields: legacyFields,
		}
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, duplicate := seen[field]; duplicate {
			return nil
		}
		seen[field] = struct{}{}
	}
	return &PreviewCoveringIndexSpec{
		Collection: collection, Name: previewCoveringIndexName(collection, fields), Fields: fields,
		Supersedes: supersedes,
	}
}

func categoryScanIndexedRootQueries(plan ir.PhysicalPlan, category lower.CompiledOutputColumn, index *PreviewCoveringIndexSpec, maxValues int) (string, map[string]any, *CategoryOverflowWitness, error) {
	root, sourceReturn, ok := categoryScanRootProjectionSource(plan)
	if !ok || root.Population != nil {
		return "", nil, nil, fmt.Errorf("category index requires one direct root scan")
	}
	var projection *ir.PhysicalProjection
	for position := range sourceReturn.Projections {
		candidate := &sourceReturn.Projections[position]
		if candidate.Name != category.Name {
			continue
		}
		if projection != nil || candidate.Hidden {
			return "", nil, nil, fmt.Errorf("category projection is ambiguous or hidden")
		}
		projection = candidate
	}
	if projection == nil {
		return "", nil, nil, fmt.Errorf("category projection is missing")
	}
	value, direct := categoryScanDirectRootProjectionValue(*projection, root.Variable)
	if !direct || !categoryScanProjectionPresenceMatches(*projection, root.Variable, value) || !validPreviewIndexPath(value.Path) {
		return "", nil, nil, fmt.Errorf("category projection has no exact direct scalar presence proof")
	}

	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		return "", nil, nil, fmt.Errorf("render source root scan: %w", err)
	}
	_, prefix, ok := splitTerminalPhysicalReturn(rendered.Query)
	if !ok {
		return "", nil, nil, fmt.Errorf("source root scan has no terminal return")
	}
	prefix, ok = categoryScanWithRootIndexHint(prefix, root.Variable, root.CollectionBindKey, index.Name)
	if !ok {
		return "", nil, nil, fmt.Errorf("category index hint could not be attached to root scan")
	}
	valueExpression := root.Variable + "." + strings.Join(value.Path, ".")
	bindVars := cloneCategoryScanBinds(rendered.BindVars)
	if err := categoryScanAddRootResourceTypeGuard(plan, root, value.Path, index, &prefix, bindVars); err != nil {
		return "", nil, nil, err
	}
	presenceExpression, err := categoryScanRootPresenceExpression(*projection.Presence, root.Variable, bindVars)
	if err != nil {
		return "", nil, nil, err
	}
	variables := categoryScanIndexedQueryVariables(prefix)
	branch := func(filter string, distinct string, result string) string {
		return prefix + "\nFILTER " + filter + "\n" + distinct + "\n" +
			"LIMIT @" + categoryLimitBind + "\n" + "RETURN " + result
	}
	nonNullQuery := branch(valueExpression+" != null", "COLLECT value = "+valueExpression,
		"{ present: true, value }")
	nullQuery := branch(valueExpression+" == null", "COLLECT present = "+presenceExpression+", value = "+valueExpression,
		"{ present, value }")
	query := "LET " + variables["nonnull"] + " = (\n" + nonNullQuery + "\n)\n" +
		"LET " + variables["null"] + " = (\n" + nullQuery + "\n)\n" +
		"FOR " + variables["row"] + " IN UNION_DISTINCT(" + variables["nonnull"] + ", " + variables["null"] + ")\n" +
		"SORT " + variables["row"] + ".present ASC, TYPENAME(" + variables["row"] + ".value) ASC, " + variables["row"] + ".value ASC\n" +
		"LIMIT @" + categoryLimitBind + "\n" +
		"RETURN { present: " + variables["row"] + ".present, value: " + variables["row"] + ".value }"
	if _, exists := bindVars[categoryLimitBind]; exists {
		return "", nil, nil, fmt.Errorf("category scan bind %q is already defined", categoryLimitBind)
	}
	bindVars[categoryLimitBind] = maxValues + 1

	witnessQuery := prefix + "\nFILTER " + valueExpression + " == null\nFILTER NOT (" + presenceExpression + ")\nLIMIT 1\nRETURN { present: false, value: null }"
	witnessBinds := cloneCategoryScanBinds(bindVars)
	delete(witnessBinds, categoryLimitBind)
	bindVars = categoryScanPruneBindVars(bindVars, query)
	witnessBinds = categoryScanPruneBindVars(witnessBinds, witnessQuery)
	return query, bindVars, &CategoryOverflowWitness{Query: witnessQuery, BindVars: witnessBinds}, nil
}

func categoryScanAddRootResourceTypeGuard(plan ir.PhysicalPlan, root *ir.PhysicalRootScan, path []string, index *PreviewCoveringIndexSpec, prefix *string, bindVars map[string]any) error {
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || collection == "" || index.Collection != collection {
		return fmt.Errorf("category index collection does not match the direct root scan")
	}
	categoryPath := strings.Join(path, ".")
	legacyFields := []string{"project", "dataset_generation", categoryPath, "auth_resource_path"}
	if sameCategoryIndexFields(index.Fields, legacyFields) {
		return nil
	}
	sharedFields := []string{"project", "dataset_generation", "resourceType", categoryPath, "auth_resource_path"}
	rootType := strings.TrimSpace(plan.Source.ResourceType)
	if !sameCategoryIndexFields(index.Fields, sharedFields) || rootType == "" || collection != rootType {
		return fmt.Errorf("category index does not match the exact source collection type and path")
	}
	const typeBindKey = "__loom_category_resource_type"
	if _, exists := bindVars[typeBindKey]; exists {
		return fmt.Errorf("category scan bind %q is already defined", typeBindKey)
	}
	*prefix += "\nFILTER " + root.Variable + ".resourceType == @" + typeBindKey
	bindVars[typeBindKey] = rootType
	return nil
}

func sameCategoryIndexFields(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func categoryScanPruneBindVars(bindVars map[string]any, query string) map[string]any {
	pruned := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if categoryScanQueryReferencesBind(query, key) {
			pruned[key] = value
		}
	}
	return pruned
}

func categoryScanQueryReferencesBind(query, key string) bool {
	token := "@" + key
	for offset := 0; offset < len(query); {
		index := strings.Index(query[offset:], token)
		if index < 0 {
			return false
		}
		end := offset + index + len(token)
		if end == len(query) || !categoryScanIdentifierByte(query[end]) {
			return true
		}
		offset = end
	}
	return false
}

func categoryScanIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func categoryScanRootPresenceExpression(presence ir.PhysicalProjectionPresence, rootVariable string, bindVars map[string]any) (string, error) {
	if presence.Source.Variable != rootVariable || presence.Source.BindKey != "" || len(presence.Paths) != 1 || len(presence.Paths[0]) == 0 {
		return "", fmt.Errorf("category projection presence is not one direct root path")
	}
	source := rootVariable
	if len(presence.Source.Path) > 0 {
		source += "." + strings.Join(presence.Source.Path, ".")
	}
	path := presence.Paths[0]
	for _, segment := range path {
		if segment == "" {
			return "", fmt.Errorf("category projection presence contains an empty path segment")
		}
	}
	var render func(string, int) (string, error)
	render = func(object string, index int) (string, error) {
		key := categoryScanUniqueBindKey(bindVars, fmt.Sprintf("__loom_category_presence_%d", index))
		bindVars[key] = path[index]
		has := fmt.Sprintf("HAS(%s, @%s)", object, key)
		if index == len(path)-1 {
			return fmt.Sprintf("(IS_OBJECT(%s) ? %s : false)", object, has), nil
		}
		child := object + "[@" + key + "]"
		rest, err := render(child, index+1)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(IS_OBJECT(%s) ? (%s ? %s : false) : false)", object, has, rest), nil
	}
	return render(source, 0)
}

func categoryScanUniqueBindKey(bindVars map[string]any, base string) string {
	key := base
	for suffix := 1; ; suffix++ {
		if _, exists := bindVars[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func categoryScanIndexedQueryVariables(sourceQuery string) map[string]string {
	variables := make(map[string]string, 3)
	for _, role := range []struct{ key, base string }{
		{key: "nonnull", base: "__loom_category_nonnull"},
		{key: "null", base: "__loom_category_null"},
		{key: "row", base: "__loom_category_row"},
	} {
		candidate := role.base
		for suffix := 1; strings.Contains(sourceQuery, candidate) || variablesContain(variables, candidate); suffix++ {
			candidate = fmt.Sprintf("%s_%d", role.base, suffix)
		}
		variables[role.key] = candidate
	}
	return variables
}

func categoryScanWithRootIndexHint(query, rootVariable, collectionBindKey, indexName string) (string, bool) {
	if rootVariable == "" || collectionBindKey == "" || indexName == "" {
		return query, false
	}
	rootScan := fmt.Sprintf("FOR %s IN @@%s", rootVariable, collectionBindKey)
	if strings.Count(query, rootScan) != 1 {
		return query, false
	}
	hintedRootScan := rootScan + fmt.Sprintf(" OPTIONS { indexHint: %q, forceIndexHint: false }", indexName)
	return strings.Replace(query, rootScan, hintedRootScan, 1), true
}

func categoryScanDistinctQuery(sourceQuery, presenceMarkerColumn, categoryColumnBind, categoryLimitBind string, streamRoot bool) (string, bool) {
	variables := make(map[string]string, 6)
	for _, variableRole := range []struct{ key, base string }{
		{key: "row", base: "__loom_category_row"},
		{key: "rows", base: "__loom_category_rows"},
		{key: "present", base: "__loom_category_present"},
		{key: "value", base: "__loom_category_value"},
		{key: "groupPresent", base: "__loom_category_group_present"},
		{key: "groupValue", base: "__loom_category_group_value"},
	} {
		key, base := variableRole.key, variableRole.base
		variable := base
		for suffix := 1; strings.Contains(sourceQuery, variable) || variablesContain(variables, variable); suffix++ {
			variable = fmt.Sprintf("%s_%d", base, suffix)
		}
		variables[key] = variable
	}
	if streamRoot {
		returnExpression, prefix, ok := splitTerminalPhysicalReturn(sourceQuery)
		if !ok {
			return "", false
		}
		return prefix + "\nLET " + variables["row"] + " = " + returnExpression + "\n" +
			categoryScanDistinctSuffix(variables, presenceMarkerColumn, categoryColumnBind, categoryLimitBind), true
	}
	return "LET " + variables["rows"] + " = (\n" + sourceQuery + "\n)\n" +
		"FOR " + variables["row"] + " IN " + variables["rows"] + "\n" +
		categoryScanDistinctSuffix(variables, presenceMarkerColumn, categoryColumnBind, categoryLimitBind), true
}

func categoryScanDistinctQueryFromPresenceColumn(sourceQuery, presenceColumnBind, categoryColumnBind, categoryLimitBind string) (string, bool) {
	variables := make(map[string]string, 6)
	for _, role := range []struct{ key, base string }{
		{key: "row", base: "__loom_category_row"},
		{key: "rows", base: "__loom_category_rows"},
		{key: "present", base: "__loom_category_present"},
		{key: "value", base: "__loom_category_value"},
		{key: "groupPresent", base: "__loom_category_group_present"},
		{key: "groupValue", base: "__loom_category_group_value"},
	} {
		variable := role.base
		for suffix := 1; strings.Contains(sourceQuery, variable) || variablesContain(variables, variable); suffix++ {
			variable = fmt.Sprintf("%s_%d", role.base, suffix)
		}
		variables[role.key] = variable
	}
	return "LET " + variables["rows"] + " = (\n" + sourceQuery + "\n)\n" +
		"FOR " + variables["row"] + " IN " + variables["rows"] + "\n" +
		"LET " + variables["present"] + " = " + variables["row"] + "[@" + presenceColumnBind + "] == true\n" +
		"LET " + variables["value"] + " = " + variables["present"] + " ? " + variables["row"] + "[@" + categoryColumnBind + "] : null\n" +
		"COLLECT " + variables["groupPresent"] + " = " + variables["present"] + ", " + variables["groupValue"] + " = " + variables["value"] + "\n" +
		"SORT " + variables["groupPresent"] + " ASC, TYPENAME(" + variables["groupValue"] + ") ASC, " + variables["groupValue"] + " ASC\n" +
		"LIMIT @" + categoryLimitBind + "\n" +
		"RETURN { present: " + variables["groupPresent"] + ", value: " + variables["groupValue"] + " }", true
}

func variablesContain(variables map[string]string, candidate string) bool {
	for _, variable := range variables {
		if variable == candidate {
			return true
		}
	}
	return false
}

func splitTerminalPhysicalReturn(query string) (returnExpression, prefix string, ok bool) {
	returnStart := strings.LastIndex(query, "\nRETURN ")
	if returnStart < 0 {
		return "", "", false
	}
	start := returnStart + len("\nRETURN ")
	expression := strings.TrimSpace(query[start:])
	if expression == "" || strings.ContainsAny(expression, "\r\n") {
		return "", "", false
	}
	return expression, query[:returnStart], true
}

func categoryScanDistinctSuffix(variables map[string]string, presenceMarkerColumn, categoryColumnBind, categoryLimitBind string) string {
	return "LET " + variables["present"] + " = " + variables["row"] + fmt.Sprintf("[%q]\n", presenceMarkerColumn) +
		"LET " + variables["value"] + " = " + variables["present"] + " ? " + variables["row"] + "[@" + categoryColumnBind + "] : null\n" +
		"COLLECT " + variables["groupPresent"] + " = " + variables["present"] + ", " + variables["groupValue"] + " = " + variables["value"] + "\n" +
		"SORT " + variables["groupPresent"] + " ASC, TYPENAME(" + variables["groupValue"] + ") ASC, " + variables["groupValue"] + " ASC\n" +
		"LIMIT @" + categoryLimitBind + "\n" +
		"RETURN { present: " + variables["groupPresent"] + ", value: " + variables["groupValue"] + " }"
}

func categoryScanPresenceMarkerColumn(plan ir.PhysicalPlan, schema []lower.CompiledOutputColumn) string {
	used := make(map[string]struct{}, len(schema))
	for _, column := range schema {
		used[column.Name] = struct{}{}
	}
	if sequence := plan.StageSequence; sequence != nil {
		for _, column := range sequence.SourceColumns {
			used[column.Name] = struct{}{}
		}
		for _, column := range sequence.FinalColumns {
			used[column.Name] = struct{}{}
		}
		for _, stage := range sequence.Stages {
			for _, column := range stage.InputColumns {
				used[column.Name] = struct{}{}
			}
			for _, column := range stage.OutputColumns {
				used[column.Name] = struct{}{}
			}
		}
	}
	const markerPrefix = "__loom_category_scan_presence"
	for suffix := 0; ; suffix++ {
		candidate := markerPrefix
		if suffix > 0 {
			candidate = fmt.Sprintf("%s_%d", markerPrefix, suffix)
		}
		if _, collides := used[candidate]; !collides {
			return candidate
		}
	}
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

func withoutCategoryScanRootIdentitySort(plan ir.PhysicalPlan) ir.PhysicalPlan {
	if plan.StageSequence != nil || len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return plan
	}
	root := plan.Operations[0].RootScan.Variable
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalSortOp || operation.Sort == nil || len(operation.Sort.Keys) != 1 {
			continue
		}
		key := operation.Sort.Keys[0]
		if key.Variable == root && len(key.Path) == 1 && key.Path[0] == "_key" && key.BindKey == "" {
			plan.Operations = append(plan.Operations[:index], plan.Operations[index+1:]...)
			break
		}
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
	rootScan, sourceReturn, ok := categoryScanRootProjectionSource(plan)
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
