package compilation

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRecipeConstructionMapsTypedSequenceAndResolvedSourceSchema(t *testing.T) {
	authoredColumns := []authoringv2.Column{
		{ColumnID: "group_id", Column: "group", Label: "Group", LogicalType: "string"},
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
	}
	alpha, beta := "alpha", "beta"
	minimum := 10.0
	authored := &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			{
				ID: "pivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
					ConstructionID: "pivot", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []authoringv2.ConstructionPivotCategory{
						{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"},
						{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &beta}, OutputColumnID: "beta_id"},
					},
					DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateSum,
					MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
				},
			},
			{
				ID: "derive", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "pivot"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
					ConstructionID: "derive", OutputColumnID: "total_id", Operation: authoringv2.ConstructionDerivedAdd,
					Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "alpha_id"},
					Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "beta_id"},
					MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
					ColumnID: "total_id", Operator: authoringv2.ConstructionFilterGreaterEq,
					Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterDecimal, Decimal: &minimum}},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "unpivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "filter"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot", Inputs: []authoringv2.ConstructionUnpivotInput{
						{ColumnID: "alpha_id", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &alpha}},
						{ColumnID: "beta_id", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &beta}},
					},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id",
					NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
					{ID: "measure_id", Name: "measure", Label: "Measure", Type: "string"},
					{ID: "amount_output_id", Name: "amount_out", Label: "Amount", Type: "decimal"},
				},
			},
		},
	}
	if err := authored.Validate(authoredColumns); err != nil {
		t.Fatalf("validate authored construction: %v", err)
	}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "emission-group", AuthoredColumns: []string{"group"}, PublicColumn: "resolved_group"},
		{EmissionID: "emission-category", AuthoredColumns: []string{"category"}, PublicColumn: "resolved_category"},
		{EmissionID: "emission-amount", AuthoredColumns: []string{"amount"}, PublicColumn: "resolved_amount"},
	}
	got, err := recipeConstruction(authored, authoredColumns, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(nil); err != nil {
		t.Fatalf("validate mapped recipe construction: %v", err)
	}
	wantSource := []recipe.StageColumn{
		{ID: "group_id", Name: "resolved_group", Label: "Group", Type: "string"},
		{ID: "category_id", Name: "resolved_category", Label: "Category", Type: "string"},
		{ID: "amount_id", Name: "resolved_amount", Label: "Amount", Type: "decimal"},
	}
	if !reflect.DeepEqual(got.SourceColumns, wantSource) {
		t.Fatalf("mapped source schema = %#v, want resolved public emissions %#v", got.SourceColumns, wantSource)
	}
	if len(got.Steps) != 4 || got.Steps[0].Operation.Pivot == nil || got.Steps[1].Operation.Derive == nil || got.Steps[2].Operation.Filter == nil || got.Steps[3].Operation.Unpivot == nil {
		t.Fatalf("mapped operation sequence = %#v", got.Steps)
	}
	if got.Steps[0].Operation.Pivot.Categories[0].Key.String == nil || *got.Steps[0].Operation.Pivot.Categories[0].Key.String != alpha {
		t.Fatalf("pivot scalar was not preserved: %#v", got.Steps[0].Operation.Pivot.Categories)
	}
	if got.Steps[1].Operation.Derive.Left.ColumnID != "alpha_id" || got.Steps[1].Operation.Derive.Right.ColumnID != "beta_id" {
		t.Fatalf("derive stable operands = %#v", got.Steps[1].Operation.Derive)
	}
	if got.Steps[2].Operation.Filter.Values[0].Decimal == nil || *got.Steps[2].Operation.Filter.Values[0].Decimal != minimum {
		t.Fatalf("filter scalar was not preserved: %#v", got.Steps[2].Operation.Filter)
	}
	if got.Steps[3].Operation.Unpivot.Inputs[1].ColumnID != "beta_id" || got.Steps[3].Operation.Unpivot.ValueOutputColumnID != "amount_output_id" {
		t.Fatalf("unpivot stable identities = %#v", got.Steps[3].Operation.Unpivot)
	}
}

func TestCompileSourceOnlyConstructionCarriesColumnIDsIntoStageDescriptors(t *testing.T) {
	document := exactRecodeDocument(nil)
	document.Columns[0].ColumnID = "status_id"
	document.Columns[1].ColumnID = "untouched_id"
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion}

	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 || compiled.Bundle.Outputs[0].Construction == nil {
		t.Fatalf("recipe construction = %#v", compiled.Bundle.Outputs)
	}
	wantSource := []recipe.StageColumn{
		{ID: "status_id", Name: "status", Label: "Status", Type: "string"},
		{ID: "untouched_id", Name: "untouched", Label: "Untouched", Type: "string"},
	}
	if !reflect.DeepEqual(compiled.Bundle.Outputs[0].Construction.SourceColumns, wantSource) {
		t.Fatalf("source construction schema = %#v, want %#v", compiled.Bundle.Outputs[0].Construction.SourceColumns, wantSource)
	}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered.Outputs) != 1 || len(lowered.Outputs[0].Stages) != 1 || lowered.Outputs[0].Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("source-only stage descriptors = %#v", lowered.Outputs)
	}
	columnsByID := map[string]string{}
	for _, column := range lowered.Outputs[0].Stages[0].Columns {
		columnsByID[column.ID] = column.Name
	}
	if columnsByID["status_id"] != "status" || columnsByID["untouched_id"] != "untouched" {
		t.Fatalf("compiled source stage column identities = %#v", columnsByID)
	}
}

func TestRecipeConstructionKeepsScalarGroupSourceOutOfPublicColumns(t *testing.T) {
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: "source_active", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
	}
	authored := &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion, SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
		Steps: []authoringv2.ConstructionStep{{
			ID: "group_by_active", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
				ConstructionID: "group_by_active", Keys: []authoringv2.ConstructionGroupKey{{InputColumnID: projection.ColumnID, OutputColumnID: "active_key"}},
				Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "record_count"}},
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "active_key", Name: "active", Label: projection.Label, Type: "boolean"},
				{ID: "record_count", Name: "record_count", Label: "Record count", Type: "integer"},
			},
		}},
	}
	got, err := recipeConstruction(authored, nil, nil)
	if err != nil {
		t.Fatalf("map scalar source GROUP: %v", err)
	}
	if err := got.Validate(nil); err != nil {
		t.Fatalf("validate mapped scalar source GROUP: %v", err)
	}
	wantInput := recipe.StageColumn{
		ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
		Label: projection.Label, Type: projection.LogicalType,
	}
	if len(got.SourceColumns) != 1 || got.SourceColumns[0] != wantInput {
		t.Fatalf("hidden source schema = %#v, want %#v", got.SourceColumns, wantInput)
	}
	if got.Steps[0].Outputs[0].ID != "active_key" || got.Steps[0].Outputs[1].ID != "record_count" {
		t.Fatalf("public group outputs contain source input: %#v", got.Steps[0].Outputs)
	}
}

func TestRecipeConstructionMapsIndexedSourceFanoutToStableChildren(t *testing.T) {
	columns := []authoringv2.Column{{
		ColumnID: "indexed_id", Column: "indexed", Label: "Indexed", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "items[]", ProjectionMode: "INDEXED"}},
	}}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "indexed-a", OccurrenceID: authoringv2.RootOccurrenceID, AuthoredColumns: []string{"indexed"}, PublicColumn: "indexed_0", Label: "Indexed [0]", LogicalType: "string", Nullable: true, Shape: "indexed_scalar", Coordinates: []capability.RepeatedCoordinate{{BoundaryPath: "items[]", Index: 0, Width: 2}}},
		{EmissionID: "indexed-count", OccurrenceID: authoringv2.RootOccurrenceID, AuthoredColumns: []string{"indexed"}, PublicColumn: "items__count", Label: "Item count", LogicalType: "integer", Shape: "repeated_count"},
	}
	got, err := recipeConstructionSourceColumns(columns, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "indexed_0" || got[1].Name != "items__count" {
		t.Fatalf("mapped indexed source schema = %#v", got)
	}
	valueID, canonical, err := lineage.StableSourceChildID(lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"indexed_id"}, OccurrenceID: authoringv2.RootOccurrenceID,
		SourcePath: "items[]", Coordinates: []lineage.Coordinate{{BoundaryPath: "items[]", Index: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != valueID || got[0].SourceChild == nil || got[0].SourceChild.Kind != canonical.Kind {
		t.Fatalf("indexed value identity = %#v, want %q", got[0], valueID)
	}
	if got[1].SourceChild == nil || got[1].SourceChild.Kind != lineage.RepeatedCountChild || got[1].SourceChild.BoundaryPath != "items[]" {
		t.Fatalf("repeated count lineage = %#v", got[1].SourceChild)
	}
}

func TestCompileIndexedSourceChildrenReachCompilerStageCapabilities(t *testing.T) {
	document := authoringv2.Document{
		Rows: authoringv2.RecordsRowDefinition(), Kind: authoringv2.Kind,
		Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{
			{ColumnID: "given_slot", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}}},
			{ColumnID: "family_slot", Column: "family", Label: "Family", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].family", ProjectionMode: "INDEXED"}}},
		},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion},
	}
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(result Result) lower.CompiledRecipeOutput {
		t.Helper()
		plan, err := semantic.BuildRecipePlan(result.Bundle, recipe.RuntimeBindings{Project: "project-a"})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
		if err != nil {
			t.Fatal(err)
		}
		physical, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return physical.Outputs[0]
	}

	output := resolve(compiled)
	if len(output.Stages) == 0 || output.Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("compiled source stages = %#v", output.Stages)
	}
	stageColumns := make(map[string]lower.CompiledOutputColumn)
	for _, column := range output.Stages[0].Columns {
		if !column.Internal {
			stageColumns[column.Name] = column
		}
	}
	if len(stageColumns) != len(compiled.EmittedColumns) {
		t.Fatalf("source stage has %d public columns, compiler emitted %d: %#v", len(stageColumns), len(compiled.EmittedColumns), stageColumns)
	}
	var filterColumn lower.CompiledOutputColumn
	var sharedCountID string
	for _, emission := range compiled.EmittedColumns {
		column, ok := stageColumns[emission.PublicColumn]
		if !ok {
			t.Fatalf("compiler stage omits exact public emission %q", emission.PublicColumn)
		}
		if emission.Shape == "indexed_scalar" || emission.Shape == "repeated_count" {
			if column.ID == "" || column.SourceChild == nil {
				t.Fatalf("indexed emission %q lacks stable source child identity: %#v", emission.PublicColumn, column)
			}
			if emission.Shape == "indexed_scalar" && filterColumn.ID == "" {
				filterColumn = column
			}
			if emission.Shape == "repeated_count" && len(emission.AuthoredColumns) > 1 {
				if len(column.SourceChild.ParentColumnIDs) != 2 || column.SourceChild.ParentColumnIDs[0] != "family_slot" || column.SourceChild.ParentColumnIDs[1] != "given_slot" {
					t.Fatalf("shared count source owners = %#v", column.SourceChild.ParentColumnIDs)
				}
				sharedCountID = column.ID
			}
		}
	}
	if filterColumn.ID == "" || sharedCountID == "" {
		t.Fatalf("indexed source children are incomplete: filter=%q shared_count=%q", filterColumn.ID, sharedCountID)
	}

	renamed := document
	renamed.Columns = append([]authoringv2.Column(nil), document.Columns...)
	renamed.Columns[0].Column, renamed.Columns[0].Label = "given_renamed", "Given renamed"
	renamed.Columns[1].Column, renamed.Columns[1].Label = "family_renamed", "Family renamed"
	renamedResult, err := Compile(context.Background(), "project-a", "explorer-a", renamed, fixtureSnapshot())
	if err != nil {
		t.Fatalf("compile renamed INDEXED source slots: %v", err)
	}
	renamedOutput := resolve(renamedResult)
	renamedByID := make(map[string]string)
	for _, column := range renamedOutput.Stages[0].Columns {
		if column.SourceChild != nil {
			renamedByID[column.ID] = column.Name
		}
	}
	if renamedByID[filterColumn.ID] == "" || renamedByID[filterColumn.ID] == filterColumn.Name || renamedByID[sharedCountID] == "" {
		t.Fatalf("child IDs did not survive public name/label edits: old=%#v new=%#v", filterColumn, renamedByID)
	}

	outputs := make([]authoringv2.StageColumn, 0, len(compiled.Bundle.Outputs[0].Construction.SourceColumns))
	for _, column := range compiled.Bundle.Outputs[0].Construction.SourceColumns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Nullable: column.Nullable})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "keep_given_item", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: filterColumn.ID, Operator: authoringv2.ConstructionFilterExists}},
		Outputs:   outputs,
	}}
	filtered, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatalf("compile construction using generated indexed child: %v", err)
	}
	filteredOutput := resolve(filtered)
	if len(filteredOutput.Stages) != 2 || filteredOutput.Stages[1].Operation != string(recipe.ConstructionFilterOp) {
		t.Fatalf("compiled filter stage descriptors = %#v", filteredOutput.Stages)
	}
	if filteredOutput.Stages[0].Columns[0].ID == "" {
		t.Fatalf("source capability descriptor did not retain column IDs: %#v", filteredOutput.Stages[0])
	}
	filteredChildFound := false
	for _, column := range filteredOutput.Stages[0].Columns {
		if column.ID == filterColumn.ID && column.Name == filterColumn.Name && column.SourceChild != nil {
			filteredChildFound = true
		}
	}
	if !filteredChildFound {
		t.Fatalf("filter input child %q was not retained in source capabilities", filterColumn.ID)
	}
}

func TestRecipeConstructionMapsTypedGroupAndExpandOperations(t *testing.T) {
	authoredColumns := []authoringv2.Column{
		{ColumnID: "status_id", Column: "status", Label: "Status", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
		{ColumnID: "tags_id", Column: "tags", Label: "Tags", LogicalType: "string"},
	}
	authored := &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			{
				ID: "expand_tags", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationExpand, Expand: &authoringv2.ConstructionExpand{
					ConstructionID: "expand_tags", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "position_id", EmptyPolicy: authoringv2.ConstructionExpandEmptyPreserveParent,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "status_id", Name: "status", Label: "Status"}, {ID: "amount_id", Name: "amount", Label: "Amount"},
					{ID: "tag_id", Name: "tag", Label: "Tag"}, {ID: "position_id", Name: "position", Label: "Position"},
				},
			},
			{
				ID: "group_tags", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "expand_tags"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "group_tags",
					Keys:           []authoringv2.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
					Aggregates: []authoringv2.ConstructionGroupAggregate{
						{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: authoringv2.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: authoringv2.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: authoringv2.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: authoringv2.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "grouped_tag_id", Name: "tag", Label: "Tag"}, {ID: "rows_id", Name: "rows", Label: "Rows"},
					{ID: "status_count_id", Name: "status_count", Label: "Status count"},
					{ID: "status_distinct_id", Name: "status_distinct", Label: "Distinct statuses"},
					{ID: "amount_sum_id", Name: "amount_sum", Label: "Amount sum"},
					{ID: "amount_mean_id", Name: "amount_mean", Label: "Amount mean"},
				},
			},
		},
	}
	if err := authored.Validate(authoredColumns); err != nil {
		t.Fatalf("validate authored construction: %v", err)
	}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "status-emission", AuthoredColumns: []string{"status"}, PublicColumn: "resolved_status"},
		{EmissionID: "amount-emission", AuthoredColumns: []string{"amount"}, PublicColumn: "resolved_amount"},
		{EmissionID: "tags-emission", AuthoredColumns: []string{"tags"}, PublicColumn: "resolved_tags"},
	}
	mapped, err := recipeConstruction(authored, authoredColumns, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if err := mapped.Validate(nil); err != nil {
		t.Fatalf("validate mapped recipe construction: %v", err)
	}
	if len(mapped.Steps) != 2 {
		t.Fatalf("mapped steps = %d, want EXPAND and GROUP", len(mapped.Steps))
	}
	expand := mapped.Steps[0].Operation.Expand
	if expand == nil || expand.InputColumnID != "tags_id" || expand.OutputColumnID != "tag_id" ||
		expand.OrdinalColumnID != "position_id" || expand.EmptyPolicy != recipe.ExpansionPreserveParent {
		t.Fatalf("mapped expand = %#v", expand)
	}
	group := mapped.Steps[1].Operation.Group
	if group == nil || len(group.Keys) != 1 || group.Keys[0] != (recipe.ConstructionGroupKey{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}) {
		t.Fatalf("mapped group keys = %#v", group)
	}
	wantOperations := []recipe.ConstructionGroupAggregateOp{
		recipe.ConstructionGroupCountRows, recipe.ConstructionGroupCountNonNull,
		recipe.ConstructionGroupCountDistinct, recipe.ConstructionGroupSum, recipe.ConstructionGroupMean,
	}
	if len(group.Aggregates) != len(wantOperations) {
		t.Fatalf("mapped summaries = %#v", group.Aggregates)
	}
	for index, operation := range wantOperations {
		if group.Aggregates[index].Operation != operation {
			t.Errorf("summary %d operation = %q, want %q", index, group.Aggregates[index].Operation, operation)
		}
	}
	if mapped.SourceColumns[2].Name != "resolved_tags" {
		t.Fatalf("mapped source name = %q, want resolved public emission", mapped.SourceColumns[2].Name)
	}
}
