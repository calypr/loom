package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func workspaceOutputCaptureRequired(output lower.CompiledRecipeOutput) bool {
	if len(output.WorkspaceOutputSources) != 0 {
		return true
	}
	if output.Plan.ClickHouseCombine == nil {
		return false
	}
	for _, input := range output.Plan.ClickHouseCombine.Inputs {
		if input.WorkspaceOutputID != "" {
			return true
		}
	}
	return false
}

// CompileResolvedRecipePlan is the common execution boundary for a resolved
// recipe bundle. Lowering produces canonical physical plans; this function
// applies the generic optimizer, inserts the typed execution window, and
// renders each output through the renderer selected by the composition-chosen
// compile target. The target is validated once here; there is no per-request
// dialect default.
func CompileResolvedRecipePlan(target Target, resolved semantic.ResolvedRecipePlan, limit int, policy ir.PhysicalOptimizationPolicy) ([]CompiledQuery, error) {
	if err := target.validate(); err != nil {
		return nil, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, policy)
	if err != nil {
		return nil, err
	}
	queries := make([]CompiledQuery, 0, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		output.TranslationVersion = resolved.SemanticPlan.TranslationVersion
		query, err := CompileRecipeOutput(target, output, resolved.SemanticPlan.Bindings, limit, policy)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", output.Name, err)
		}
		queries = append(queries, query)
	}
	return queries, nil
}

// CompileRecipeOutput applies the common optimizer, execution window, and
// the target-selected canonical renderer to one already-lowered recipe
// output. The AQL target keeps the exact existing AQL payload bytes, named
// binds, and preview special paths. The PostgreSQL target renders the same
// finalized plan through the PostgreSQL renderer and fails explicitly for
// plans outside its closed supported surface; no path converts AQL to SQL
// and no PostgreSQL plan falls back to AQL.
func CompileRecipeOutput(target Target, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledQuery, error) {
	if err := target.validate(); err != nil {
		return CompiledQuery{}, err
	}
	if workspaceOutputCaptureRequired(output) {
		return CompiledQuery{}, fmt.Errorf("output %q references same-workspace outputs; server-owned workspace capture is not available", output.Name)
	}
	if output.Plan.ClickHousePrefix != nil || (output.OptimizedPlan != nil && output.OptimizedPlan.ClickHousePrefix != nil) {
		return CompiledQuery{}, fmt.Errorf("output %q is a composite ClickHouse plan; compile its source prefix through CompileClickHousePrefix and its combine through the ClickHouse stream path", output.Name)
	}
	var physical ir.PhysicalPlan
	groupRows := len(output.Plan.Operations) == 1 && output.Plan.Operations[0].Kind == ir.PhysicalGroupRowsOp
	if groupRows {
		physical = clonePhysicalPlan(output.Plan)
	} else if output.OptimizedPlan != nil {
		physical = clonePhysicalPlan(*output.OptimizedPlan)
	} else {
		var err error
		physical, err = optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("optimize canonical recipe plan: %w", err)
		}
	}
	includePreviewSourceID := false
	if bindings.IncludeSourceIdentity {
		var err error
		physical, includePreviewSourceID, err = withPreviewSourceResourceID(output, physical)
		if err != nil {
			return CompiledQuery{}, fmt.Errorf("add preview source identity projection: %w", err)
		}
	}
	if limit > 0 {
		physical = withConstructionPreviewRootIDFilter(output, physical)
	}
	physical, err := withGenericPhysicalExecutionWindow(physical, limit)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("apply canonical recipe execution window: %w", err)
	}
	physical = withoutUnusedTerminalPivotPresenceCompanions(physical)
	if bindings.IncludeAuthResourcePath && !groupRows {
		if err := appendAuthResourcePathProjection(&physical); err != nil {
			return CompiledQuery{}, err
		}
	}
	// Target-selected rendering. The PostgreSQL target never touches the AQL
	// renderer or the AQL preview accelerators below; unsupported plans fail
	// explicitly with the PostgreSQL renderer's typed errors.
	if target.backend == command.CompiledBackendPostgreSQL {
		return compilePostgreSQLRecipeOutput(target, output, bindings, limit, physical, includePreviewSourceID)
	}
	streamingMissingCategoryPivot := canRenderStreamingMissingCategoryPivotPreview(output, bindings, physical)
	legacyPreviewCoveringIndex := previewCoveringIndexSpec(physical)
	previewCoveringIndex := legacyPreviewCoveringIndex
	if streamingMissingCategoryPivot {
		if storedValuesIndex := streamingMissingCategoryPivotIndexSpec(physical); storedValuesIndex != nil {
			if storedValuesIndex.Supersedes == nil && legacyPreviewCoveringIndex != nil && legacyPreviewCoveringIndex.Name != storedValuesIndex.Name {
				storedValuesIndex.Supersedes = &PreviewCoveringIndexReplacement{
					Name: legacyPreviewCoveringIndex.Name, Fields: append([]string(nil), legacyPreviewCoveringIndex.Fields...),
				}
			}
			previewCoveringIndex = storedValuesIndex
		}
	}
	previewGroupScan := previewGroupScanSpec(physical, previewCoveringIndex)
	previewGroupProjection := previewGroupProjectionSpec(physical)
	var rendered aql.RenderedPhysicalPlan
	if streamingMissingCategoryPivot {
		if previewCoveringIndex != nil && len(previewCoveringIndex.StoredValues) != 0 {
			rendered, err = aql.RenderPhysicalPlanWithStreamingMissingCategoryPivotPreviewAndRootIndexHint(physical, previewCoveringIndex.Name)
		} else {
			rendered, err = aql.RenderPhysicalPlanWithStreamingMissingCategoryPivotPreview(physical)
		}
	} else if previewCoveringIndex != nil {
		if len(previewCoveringIndex.pivotGroupKeyPaths) != 0 {
			rendered, err = aql.RenderPhysicalPlanWithTwoScanPivotPreview(physical, previewCoveringIndex.Name, previewCoveringIndex.pivotGroupKeyPaths)
		} else {
			rendered, err = aql.RenderPhysicalPlanWithRootIndexHint(physical, previewCoveringIndex.Name)
		}
	} else if canRenderDynamicCategoryPivotPreview(physical) {
		rendered, err = aql.RenderPhysicalPlanWithDynamicCategoryPivotPreview(physical)
	} else {
		rendered, err = aql.RenderPhysicalPlan(physical)
	}
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render canonical recipe physical plan: %w", err)
	}
	// The rendered result becomes execution input only through the tagged
	// command boundary: the plan's AQL rendering is tagged AQL once, here,
	// and the parent-tagged command is the only executable CompiledQuery
	// carries. The query text and named bind map are never re-extracted as
	// loose execution fields. A PostgreSQL-tagged command cannot enter this
	// path; PostgreSQL selection happens through the composition seam.
	compiledCommand, err := command.NewAQLCommand(rendered.Query, rendered.BindVars)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("tag canonical recipe command: %w", err)
	}
	columns, pivotFields := physicalProjectionMetadata(physical)
	if len(output.Columns) != 0 {
		columns = append([]string(nil), output.Columns...)
	}
	outputSchema := lower.CloneCompiledOutputSchema(output.OutputSchema)
	if includePreviewSourceID {
		outputSchema = append(outputSchema, lower.CompiledOutputColumn{
			ID: ir.PreviewSourceResourceIDColumn, Name: ir.PreviewSourceResourceIDColumn,
			Label: ir.PreviewSourceResourceIDColumn, SemanticPath: "preview:source_resource_id",
			Kind: "string", Cardinality: "optional_one", Nullable: true, Internal: true,
		})
	}
	publicColumns := publicOutputColumns(outputSchema)
	if len(publicColumns) == 0 {
		for _, column := range columns {
			if column == "_key" || column == "__loom_row_id" || column == "__loom_dynamic_runtime_keys" {
				continue
			}
			publicColumns = append(publicColumns, column)
		}
	}
	return CompiledQuery{
		PhysicalPlan:       clonePhysicalPlan(physical),
		Project:            bindings.Project,
		DatasetGeneration:  normalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType:   output.RootResourceType,
		TranslationVersion: output.TranslationVersion,
		AuthResourcePaths:  cloneStrings(bindings.AuthResourcePaths),
		PlanMode:           "physical",
		PlanProfile:        "generic_fhir_graph_recipe",
		TraversalCount:     physicalTraversalCount(physical),
		RowIdentity:        output.RowIdentity.Clone(),
		OptimizationRules:  recipeOptimizationRules(physical),
		Command:            compiledCommand,
		Columns:            columns,
		OutputSchema:       outputSchema,
		PublicColumns:      publicColumns,
		PivotFields:        pivotFields,
		Limit:              limit,
		PartialValidation: rendered.PartialValidation || (physical.StageSequence != nil && physical.StageSequence.PreviewLimitBindKey != "" &&
			(physical.StageSequence.PreviewSourceWindowByRootID || physical.StageSequence.PreviewTerminalPivotWindow)),
		PreviewCoveringIndex:   previewCoveringIndex,
		PreviewGroupScan:       previewGroupScan,
		PreviewGroupProjection: previewGroupProjection,
		PlanDiagnostics:        physicalPlanDiagnostics(physical),
	}, nil
}

// CompiledClickHousePrefix is the compiled source query of one composite
// ClickHouse plan's typed prefix, tagged once with the command backend named
// by the prefix's own SourceBackend. The command is the only executable
// carrier: the query text, bind variables, and SQL arguments are never
// re-extracted as loose execution fields.
type CompiledClickHousePrefix struct {
	Command       command.CompiledCommand
	OutputColumns []string
	Fingerprint   string
}

// CompileClickHousePrefix is the compiler seam that compiles the typed prefix
// of one composite ClickHouse plan into a source-backend command. The prefix's
// explicit PhysicalPrefixSourceBackend tag — never the terminal Combine, which
// always executes on ClickHouse — selects the renderer, and the selected
// compile target must name the same backend: an AQL-tagged prefix requires
// the Arango target and a PostgreSQL-tagged prefix requires the PostgreSQL
// target. There is no default and no fallback: an untagged, unknown, or
// target-mismatched prefix fails explicitly before any rendering or I/O.
//
// The AQL arm renders through the canonical AQL prefix renderer, preserving
// the exact payload bytes and named-bind contract. The PostgreSQL arm renders
// the same closed typed stage sequence through the target-bound PostgreSQL
// renderer as parameterized SQL and requires the statement to declare exactly
// the prefix's typed final schema, row identity column included, in schema
// order; any drift fails closed.
func CompileClickHousePrefix(target Target, plan ir.PhysicalPlan) (CompiledClickHousePrefix, error) {
	if err := target.validate(); err != nil {
		return CompiledClickHousePrefix{}, err
	}
	if plan.Engine != ir.PhysicalEngineClickHouse || plan.ClickHousePrefix == nil || plan.ClickHouseCombine == nil {
		return CompiledClickHousePrefix{}, fmt.Errorf("composite ClickHouse plan with a typed prefix is required")
	}
	// Validate the composite before backend dispatch, not just the arm-local
	// view. The PostgreSQL arm renders a stripped standalone clone, so with-
	// out this call the composite contract — the restricted PostgreSQL
	// refusal, cell-trace/row-lineage refusal, preview/LIMIT refusal,
	// GROUP/COHORT_GROUP stage refusal, prefix-to-final-stage binding, and
	// OutputAuthResourcePathBindKey equality — would only be enforced for
	// callers that went through the composer. Hand-built or restored plans
	// are validated here, before any rendering or I/O.
	if err := plan.Validate(); err != nil {
		return CompiledClickHousePrefix{}, fmt.Errorf("validate composite ClickHouse prefix plan: %w", err)
	}
	switch prefix := plan.ClickHousePrefix; prefix.SourceBackend {
	case ir.PhysicalPrefixSourceAQL:
		if target.backend != command.CompiledBackendAQL {
			return CompiledClickHousePrefix{}, fmt.Errorf("AQL-tagged ClickHouse prefix requires the AQL compile target, got %s", string(target.backend))
		}
		rendered, err := aql.RenderClickHousePrefix(plan)
		if err != nil {
			return CompiledClickHousePrefix{}, fmt.Errorf("render ClickHouse prefix command: %w", err)
		}
		compiledCommand, err := command.NewAQLCommand(rendered.Query, rendered.BindVars)
		if err != nil {
			return CompiledClickHousePrefix{}, fmt.Errorf("tag ClickHouse prefix command: %w", err)
		}
		fingerprint, err := categoryQueryFingerprint(rendered.Query, rendered.BindVars)
		if err != nil {
			return CompiledClickHousePrefix{}, fmt.Errorf("fingerprint ClickHouse prefix command: %w", err)
		}
		return CompiledClickHousePrefix{
			Command:       compiledCommand,
			OutputColumns: clickHousePrefixSchema(plan),
			Fingerprint:   fingerprint,
		}, nil
	case ir.PhysicalPrefixSourcePostgreSQL:
		if target.backend != command.CompiledBackendPostgreSQL {
			return CompiledClickHousePrefix{}, fmt.Errorf("PostgreSQL-tagged ClickHouse prefix requires the PostgreSQL compile target, got %s", string(target.backend))
		}
		source := ir.ClonePhysicalPlan(plan)
		source.Engine = ir.PhysicalEngineAQL
		source.ClickHouseCombine = nil
		source.ClickHousePrefix = nil
		tagged, rendered, err := target.renderPostgreSQLFinalPlan(source)
		if err != nil {
			return CompiledClickHousePrefix{}, fmt.Errorf("render ClickHouse prefix command: %w", err)
		}
		if err := matchClickHousePrefixSchema(plan, rendered.OutputColumns); err != nil {
			return CompiledClickHousePrefix{}, err
		}
		return CompiledClickHousePrefix{
			Command:       tagged,
			OutputColumns: append([]string(nil), rendered.OutputColumns...),
			Fingerprint:   postgreSQLPlanFingerprint(rendered.SQL, rendered.Args, rendered.OutputColumns),
		}, nil
	default:
		return CompiledClickHousePrefix{}, prefix.SourceBackend.Validate()
	}
}

func clickHousePrefixSchema(plan ir.PhysicalPlan) []string {
	if plan.StageSequence == nil {
		return nil
	}
	columns := make([]string, 0, len(plan.StageSequence.FinalColumns))
	for _, column := range plan.StageSequence.FinalColumns {
		columns = append(columns, column.Name)
	}
	return columns
}

// matchClickHousePrefixSchema requires the rendered PostgreSQL statement to
// declare exactly the prefix's typed final schema in schema order, including
// the row identity column the terminal Combine uses for row identity.
func matchClickHousePrefixSchema(plan ir.PhysicalPlan, declared []string) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("PostgreSQL ClickHouse prefix has no typed stage sequence")
	}
	sequence := plan.StageSequence
	expected := clickHousePrefixSchema(plan)
	if len(expected) == 0 {
		return fmt.Errorf("PostgreSQL ClickHouse prefix declares no typed final schema columns")
	}
	sawRowIdentity := false
	for _, column := range sequence.FinalColumns {
		if column.Name == sequence.FinalRowIdentity {
			sawRowIdentity = true
		}
	}
	if !sawRowIdentity {
		return fmt.Errorf("PostgreSQL ClickHouse prefix typed schema does not carry its row identity column %q", sequence.FinalRowIdentity)
	}
	if len(declared) != len(expected) {
		return fmt.Errorf("PostgreSQL ClickHouse prefix declares %d result columns, want the exact typed prefix schema %v", len(declared), expected)
	}
	for index := range expected {
		if declared[index] != expected[index] {
			return fmt.Errorf("PostgreSQL ClickHouse prefix result column %d is %q, want the typed prefix column %q", index, declared[index], expected[index])
		}
	}
	return nil
}

// canRenderStreamingMissingCategoryPivotPreview only enables the streaming
// renderer for a direct typed-root scan feeding one terminal Pivot. The
// renderer retains the full source query and all scope predicates; this gate
// only excludes shapes whose projections or row membership need an
// intermediate table.
func canRenderStreamingMissingCategoryPivotPreview(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, plan ir.PhysicalPlan) bool {
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PopulationMappingReturn != nil || sequence.OutputAuthResourcePathBindKey != "" ||
		bindings.IncludeAuthResourcePath ||
		(bindings.IncludeSourceIdentity && !outputHasCompositeSource(output)) || len(sequence.Stages) != 1 {
		return false
	}
	stage := sequence.Stages[0]
	pivot := stage.GroupedPivot
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivot.OneInputRowPerGroup ||
		pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		len(pivot.Categories) == 0 || len(pivot.Categories) > 32 ||
		(pivot.DuplicatePolicy != "ERROR" && pivot.DuplicatePolicy != "SUM" && pivot.DuplicatePolicy != "MIN" && pivot.DuplicatePolicy != "MAX") ||
		(pivot.MissingCellPolicy != "NULL" && pivot.MissingCellPolicy != "ERROR") ||
		(pivot.UnlistedCategoryPolicy != "ERROR" && pivot.UnlistedCategoryPolicy != "EXCLUDE_WITH_EVIDENCE") {
		return false
	}
	hasMissingCategory := false
	for _, category := range pivot.Categories {
		if category.MatchKind == ir.PhysicalPivotCategoryMissingMatch {
			hasMissingCategory = true
		}
	}
	if !hasMissingCategory || (!pivot.CategoryPresenceFromInput && pivot.CategoryPresence == nil) || pivot.CategoryPresenceColumn == "" {
		return false
	}
	root, sourceReturn, ok := previewCoveringIndexSource(plan)
	if !ok || root.Population != nil || root.Variable == "" {
		return false
	}
	collection, ok := plan.BindVars[root.CollectionBindKey].(string)
	if !ok || collection != output.RootResourceType {
		return false
	}
	presenceOutputs := 0
	for _, projection := range sourceReturn.Projections {
		if projection.PresenceOutput {
			if !pivot.CategoryPresenceFromInput || projection.Name != pivot.CategoryPresenceColumn || projection.Presence == nil ||
				projection.Presence.Source.Variable != root.Variable || projection.Presence.Source.BindKey != "" {
				return false
			}
			presenceOutputs++
			continue
		}
		if _, ok := previewCoveringProjectionPath(projection, root.Variable); !ok {
			return false
		}
	}
	if presenceOutputs != 1 {
		return false
	}
	sourceProjectionNames := make(map[string]struct{}, len(sourceReturn.Projections))
	for _, projection := range sourceReturn.Projections {
		if projection.Name == "" {
			return false
		}
		if _, duplicate := sourceProjectionNames[projection.Name]; duplicate {
			return false
		}
		sourceProjectionNames[projection.Name] = struct{}{}
	}
	if !streamingPivotInputProjectsSourceRows(*pivot, stage.InputRowVariable, sourceProjectionNames) {
		return false
	}
	return true
}

// streamingPivotInputProjectsSourceRows proves that the Pivot's row-shaped
// inputs are a pure projection of the immediately preceding source RETURN.
// Lowering represents that relationship through the stage input variable,
// even when the source itself is a direct root scan.
func streamingPivotInputProjectsSourceRows(pivot ir.PhysicalGroupedPivot, inputRowVariable string, sourceNames map[string]struct{}) bool {
	if inputRowVariable == "" || len(pivot.InputProjections) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(pivot.InputProjections))
	for _, projection := range pivot.InputProjections {
		if projection.Name == "" || projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput ||
			projection.Value.Variable != inputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 || projection.Value.Path[0] != projection.Name {
			return false
		}
		if _, ok := sourceNames[projection.Name]; !ok {
			return false
		}
		if _, duplicate := seen[projection.Name]; duplicate {
			return false
		}
		seen[projection.Name] = struct{}{}
	}
	required := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn}
	for _, key := range pivot.GroupKeys {
		required = append(required, key.Column)
	}
	for _, name := range required {
		if name == "" {
			return false
		}
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

// withoutUnusedTerminalPivotPresenceCompanions keeps category-presence
// sidecars out of ordinary terminal Pivot previews. Lowering retains them in
// the canonical plan so a later category-discovery scan can distinguish an
// absent source property from explicit NULL. A frozen Pivot only consumes
// that proof when one of its categories is MISSING; otherwise carrying the
// hidden boolean through the preview would disable the bounded two-scan path.
func withoutUnusedTerminalPivotPresenceCompanions(plan ir.PhysicalPlan) ir.PhysicalPlan {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || !sequence.PreviewTerminalPivotWindow ||
		sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return plan
	}
	stage := &sequence.Stages[0]
	if stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.InputStageID != sequence.SourceStageID || sequence.FinalStageID != stage.ID ||
		sequence.FinalRowIdentity != stage.RowIdentityColumn ||
		stage.GroupedPivot.CategoryPresenceColumn != "" || stage.GroupedPivot.CategoryPresence != nil ||
		stage.GroupedPivot.CategoryPresenceFromInput {
		return plan
	}

	presenceNames := make(map[string]struct{})
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.PresenceOutput {
				presenceNames[projection.Name] = struct{}{}
			}
		}
	}
	if len(presenceNames) == 0 {
		return plan
	}

	for operationIndex := range plan.Operations {
		operation := &plan.Operations[operationIndex]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := operation.Return.Projections[:0]
		for _, projection := range operation.Return.Projections {
			if _, unused := presenceNames[projection.Name]; unused && projection.PresenceOutput {
				continue
			}
			projections = append(projections, projection)
		}
		operation.Return.Projections = projections
	}

	filterColumns := func(columns []ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
		filtered := columns[:0]
		for _, column := range columns {
			if _, unused := presenceNames[column.Name]; unused {
				continue
			}
			filtered = append(filtered, column)
		}
		return filtered
	}
	sequence.SourceColumns = filterColumns(sequence.SourceColumns)
	stage.InputColumns = filterColumns(stage.InputColumns)
	stage.InputProjections = filterPresenceProjections(stage.InputProjections, presenceNames)
	stage.GroupedPivot.InputProjections = filterPresenceProjections(stage.GroupedPivot.InputProjections, presenceNames)
	return plan
}

func filterPresenceProjections(projections []ir.PhysicalProjection, presenceNames map[string]struct{}) []ir.PhysicalProjection {
	filtered := projections[:0]
	for _, projection := range projections {
		if _, unused := presenceNames[projection.Name]; unused {
			continue
		}
		filtered = append(filtered, projection)
	}
	return filtered
}

func withPreviewSourceResourceID(output lower.CompiledRecipeOutput, plan ir.PhysicalPlan) (ir.PhysicalPlan, bool, error) {
	if plan.Engine == ir.PhysicalEngineClickHouse || outputHasCompositeSource(output) {
		return plan, false, nil
	}
	rootVariable := ""
	for _, operation := range plan.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" {
		return plan, false, nil
	}
	projection := ir.PhysicalProjection{
		Name: ir.PreviewSourceResourceIDColumn, Hidden: true,
		Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"id"}},
	}
	returnFound := false
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returnFound = true
		operation.Return.Projections = appendUniqueProjection(operation.Return.Projections, projection)
	}
	if !returnFound {
		return plan, false, nil
	}
	if sequence := plan.StageSequence; sequence != nil {
		sourceColumn := ir.PhysicalStageColumn{
			ID: ir.PreviewSourceResourceIDColumn, Name: ir.PreviewSourceResourceIDColumn,
			Label: ir.PreviewSourceResourceIDColumn, Kind: "string", Cardinality: "optional_one",
			Nullable: true, Internal: true,
		}
		sequence.SourceColumns = appendUniqueStageColumn(sequence.SourceColumns, sourceColumn)
		sequence.FinalColumns = appendUniqueStageColumn(sequence.FinalColumns, sourceColumn)
		for index := range sequence.Stages {
			stage := &sequence.Stages[index]
			inputProjection := ir.PhysicalProjection{
				Name: sourceColumn.Name, Hidden: true,
				Value: ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{sourceColumn.Name}},
			}
			stage.InputColumns = appendUniqueStageColumn(stage.InputColumns, sourceColumn)
			stage.OutputColumns = appendUniqueStageColumn(stage.OutputColumns, sourceColumn)
			stage.InputProjections = appendUniqueProjection(stage.InputProjections, inputProjection)
			stage.OutputProjections = appendUniqueProjection(stage.OutputProjections, inputProjection)
			if stage.Unpivot != nil {
				stage.Unpivot.InputProjections = appendUniqueProjection(stage.Unpivot.InputProjections, inputProjection)
				if len(stage.Unpivot.PreservedOutputs) != 0 {
					stage.Unpivot.PreservedOutputs = appendUniqueUnpivotOutput(stage.Unpivot.PreservedOutputs, ir.PhysicalUnpivotOutput{
						InputColumn: sourceColumn.Name, OutputColumn: sourceColumn.Name,
					})
				}
			}
		}
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Unpivot == nil {
			continue
		}
		inputProjection := projection
		inputProjection.Value = ir.PhysicalValue{Variable: rootVariable, Path: []string{"id"}}
		operation.Unpivot.InputProjections = appendUniqueProjection(operation.Unpivot.InputProjections, inputProjection)
		if len(operation.Unpivot.PreservedOutputs) != 0 {
			operation.Unpivot.PreservedOutputs = appendUniqueUnpivotOutput(operation.Unpivot.PreservedOutputs, ir.PhysicalUnpivotOutput{
				InputColumn: inputProjection.Name, OutputColumn: inputProjection.Name,
			})
		}
	}
	return plan, true, nil
}

func appendUniqueUnpivotOutput(outputs []ir.PhysicalUnpivotOutput, candidate ir.PhysicalUnpivotOutput) []ir.PhysicalUnpivotOutput {
	for _, output := range outputs {
		if output.InputColumn == candidate.InputColumn {
			return outputs
		}
	}
	return append(outputs, candidate)
}

func appendUniqueProjection(projections []ir.PhysicalProjection, candidate ir.PhysicalProjection) []ir.PhysicalProjection {
	for _, projection := range projections {
		if projection.Name == candidate.Name {
			return projections
		}
	}
	return append(projections, candidate)
}

func appendUniqueStageColumn(columns []ir.PhysicalStageColumn, candidate ir.PhysicalStageColumn) []ir.PhysicalStageColumn {
	for _, column := range columns {
		if column.Name == candidate.Name {
			return columns
		}
	}
	return append(columns, candidate)
}

func outputHasCompositeSource(output lower.CompiledRecipeOutput) bool {
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupRowsOp || operation.Kind == ir.PhysicalGroupedPivotOp {
			return true
		}
	}
	if sequence := output.Plan.StageSequence; sequence != nil {
		for _, stage := range sequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				return true
			}
		}
	}
	for _, stage := range output.Stages {
		if stage.Operation == string(recipe.ConstructionGroupOp) || stage.Operation == string(recipe.ConstructionPivotOp) ||
			stage.Operation == string(recipe.ConstructionCodedGroupOp) {
			return true
		}
	}
	return false
}

func canRenderDynamicCategoryPivotPreview(physical ir.PhysicalPlan) bool {
	sequence := physical.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || len(sequence.Stages) != 1 {
		return false
	}
	stage := sequence.Stages[0]
	return stage.ID == sequence.FinalStageID && stage.InputStageID == sequence.SourceStageID &&
		stage.Kind == ir.PhysicalStagePivotOp && stage.GroupedPivot != nil &&
		!stage.GroupedPivot.OneInputRowPerGroup && len(stage.GroupedPivot.GroupKeys) > 0
}

func appendAuthResourcePathProjection(physical *ir.PhysicalPlan) error {
	rootVariable := ""
	for _, operation := range physical.Operations {
		if operation.RootScan != nil {
			rootVariable = operation.RootScan.Variable
			break
		}
	}
	if rootVariable == "" {
		return fmt.Errorf("canonical plan has no root scan for auth_resource_path projection")
	}
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "auth_resource_path" {
				return nil
			}
		}
		value := ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}}
		for previous := 0; previous < index; previous++ {
			reshape := &physical.Operations[previous]
			switch reshape.Kind {
			case ir.PhysicalGroupedPivotOp:
				if reshape.GroupedPivot == nil {
					return fmt.Errorf("grouped pivot is missing payload for auth_resource_path projection")
				}
				pivot := reshape.GroupedPivot
				reserved := []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn, pivot.UnlistedEvidenceColumn, "__loom_row_id"}
				for _, key := range pivot.GroupKeys {
					reserved = append(reserved, key.Column)
				}
				for _, category := range pivot.Categories {
					reserved = append(reserved, category.Output)
				}
				column := uniqueAuthResourcePathColumn(pivot.InputProjections, reserved...)
				pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
					Name: column, Hidden: true,
					Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}},
				})
				variable := pivot.GroupRowsVariable + "_auth_resource_path"
				for _, key := range pivot.GroupKeys {
					if key.Variable == variable {
						variable += "_group"
					}
				}
				pivot.GroupKeys = append(pivot.GroupKeys, ir.PhysicalGroupedPivotKey{
					Column: column, Variable: variable, Kind: "STRING", Hidden: true,
				})
				value = ir.PhysicalValue{Variable: pivot.OutputRowVariable, Path: []string{column}}
			case ir.PhysicalUnpivotOp:
				if reshape.Unpivot == nil {
					return fmt.Errorf("unpivot is missing payload for auth_resource_path projection")
				}
				unpivot := reshape.Unpivot
				column := uniqueAuthResourcePathColumn(unpivot.InputProjections, unpivot.KeyOutput, unpivot.ValueOutput, "__loom_row_id")
				unpivot.InputProjections = append(unpivot.InputProjections, ir.PhysicalProjection{
					Name: column, Hidden: true,
					Value: ir.PhysicalValue{Variable: rootVariable, Path: []string{"auth_resource_path"}},
				})
				value = ir.PhysicalValue{Variable: unpivot.OutputRowVariable, Path: []string{column}}
			}
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{
			Name: "auth_resource_path", Hidden: true,
			Value: value,
		})
		return nil
	}
	return fmt.Errorf("canonical plan has no RETURN operation for auth_resource_path projection")
}

func uniqueAuthResourcePathColumn(projections []ir.PhysicalProjection, reserved ...string) string {
	for suffix := 0; ; suffix++ {
		name := "__loom_auth_resource_path"
		if suffix > 0 {
			name = fmt.Sprintf("%s_%d", name, suffix)
		}
		found := false
		for _, projection := range projections {
			if projection.Name == name {
				found = true
				break
			}
		}
		for _, reservedName := range reserved {
			if reservedName == name {
				found = true
				break
			}
		}
		if !found {
			return name
		}
	}
}

func recipeOptimizationRules(plan ir.PhysicalPlan) []string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalFilterOp {
			return []string{OptimizerRuleFilterPushdown}
		}
	}
	return nil
}
