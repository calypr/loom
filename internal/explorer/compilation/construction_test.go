package compilation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRecipeConstructionStepCopiesResolvedFieldSourceForGroupRowValue(t *testing.T) {
	fieldSource := &ws.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: "source_projection", SourceShapeID: "shape-component",
		SourceRole: "ANCESTOR", OccurrenceID: "base", ResourceType: "ObservationComponent",
		OwnerOccurrenceID: "base", OwnerResourceType: "Observation", CandidateID: "component-value",
		CanonicalPath: "valueQuantity.value", BoundarySelector: "component[]",
	}
	step := ws.ConstructionStep{
		ID: "group", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{FieldSource: fieldSource, OutputColumnID: "component_value_key"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		RowValues: []ws.ConstructionRowValue{{
			FieldSource: fieldSource, OutputColumnID: "component_values", Policy: ws.ConstructionRowValueAll,
		}},
		Outputs: []ws.StageColumn{
			{ID: "component_value_key", Name: "component_value_key", Label: "Component value", Type: "decimal"},
			{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
			{ID: "component_values", Name: "component_values", Label: "Component values", Type: "array"},
		},
	}

	mapped, err := recipeConstructionStep(step)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.RowValues) != 1 || mapped.RowValues[0].InputColumnID != "" || mapped.RowValues[0].FieldSource == nil {
		t.Fatalf("recipe row value = %#v, want FieldSource with no fabricated input column", mapped.RowValues)
	}
	want := &recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: "source_projection", SourceShapeID: "shape-component",
		Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: &recipe.ConstructionGroupCurrentStageFieldSource{
			SourceRole: "ANCESTOR", OccurrenceID: "base", OwnerResourceType: "Observation",
			ResourceType: "ObservationComponent", CandidateID: "component-value",
			CanonicalPath: "valueQuantity.value", BoundarySelector: "component[]",
		}},
	}
	if !reflect.DeepEqual(mapped.RowValues[0].FieldSource, want) {
		t.Fatalf("recipe row-value FieldSource = %#v, want %#v", mapped.RowValues[0].FieldSource, want)
	}
	if len(mapped.Operation.Group.Keys) != 1 || !reflect.DeepEqual(mapped.Operation.Group.Keys[0].FieldSource, want) {
		t.Fatalf("Group key and row-value FieldSource mapping differ: %#v", mapped.Operation.Group.Keys)
	}
}

func TestRecipeConstructionStepCopiesRelatedFieldSourceRouteForGroupKeyAndRowValue(t *testing.T) {
	fieldSource := &ws.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: "source_projection", SourceShapeID: "shape-related",
		Input: &ws.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &ws.ConstructionGroupFieldRelatedOccurrence{
			AnchorOccurrenceID: "root", AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
			Route: []ws.ConstructionGroupFieldRelatedRouteStep{{
				StepIndex: 0, FromOccurrenceID: "root", FromNodeID: "patient-node", FromResourceType: "Patient",
				EdgeID: "edge-subject", Relationship: "subject", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				ToOccurrenceID: "related-observation", ToNodeID: "observation-node", ToResourceType: "Observation",
			}},
			TargetOccurrenceID: "related-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			ResourceType: "string", CandidateID: "id-candidate", CanonicalPath: "id",
			EmptyPolicy: "PRESERVE_PARENT", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
		}},
	}
	codingFieldSource := &ws.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: "source_projection", SourceShapeID: "shape-related-coding",
		Input: &ws.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &ws.ConstructionGroupFieldRelatedOccurrence{
			AnchorOccurrenceID: "root", AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
			Route: []ws.ConstructionGroupFieldRelatedRouteStep{{
				StepIndex: 0, FromOccurrenceID: "root", FromNodeID: "patient-node", FromResourceType: "Patient",
				EdgeID: "edge-subject", Relationship: "subject", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				ToOccurrenceID: "related-observation", ToNodeID: "observation-node", ToResourceType: "Observation",
			}},
			TargetOccurrenceID: "related-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			ResourceType: "Coding", EmptyPolicy: "PRESERVE_PARENT", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
			CodingSelector: "type.coding[]", CodingEmptyPolicy: "GROUP",
			OrdinalSemantics: recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition,
		}},
	}
	step := ws.ConstructionStep{
		ID: "group", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys: []ws.ConstructionGroupKey{
				{FieldSource: fieldSource, OutputColumnID: "status-key"},
				{FieldSource: codingFieldSource, OutputColumnID: "coding-key"},
			},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
		}},
		RowValues: []ws.ConstructionRowValue{{FieldSource: fieldSource, OutputColumnID: "status-values", Policy: ws.ConstructionRowValueAll}},
		Outputs: []ws.StageColumn{
			{ID: "status-key", Name: "status_key", Label: "Status", Type: "string"},
			{ID: "coding-key", Name: "coding_key", Label: "Coding", Type: "string"},
			{ID: "row-count", Name: "row_count", Label: "Rows", Type: "integer"},
			{ID: "status-values", Name: "status_values", Label: "Status values", Type: "array"},
		},
	}

	mapped, err := recipeConstructionStep(step)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.RowValues) != 1 || mapped.RowValues[0].InputColumnID != "" || mapped.RowValues[0].FieldSource == nil {
		t.Fatalf("recipe row value = %#v, want route-bound FieldSource with no inputColumnId", mapped.RowValues)
	}
	want := &recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: "source_projection", SourceShapeID: "shape-related",
		Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &recipe.ConstructionGroupRelatedOccurrenceFieldSource{
			AnchorOccurrenceID: "root", AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
			Route: []recipe.ConstructionGroupRelatedRouteStep{{
				StepIndex: 0, FromOccurrenceID: "root", FromNodeID: "patient-node", FromResourceType: "Patient",
				EdgeID: "edge-subject", Relationship: "subject", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				ToOccurrenceID: "related-observation", ToNodeID: "observation-node", ToResourceType: "Observation",
			}},
			TargetOccurrenceID: "related-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			ResourceType: "string", CandidateID: "id-candidate", CanonicalPath: "id",
			EmptyPolicy: recipe.ExpansionPreserveParent, Multiplicity: "DISTINCT_TARGET_PER_INPUT",
		}},
	}
	if !reflect.DeepEqual(mapped.RowValues[0].FieldSource, want) {
		t.Fatalf("recipe row-value FieldSource = %#v, want %#v", mapped.RowValues[0].FieldSource, want)
	}
	if !reflect.DeepEqual(mapped.Operation.Group.Keys[0].FieldSource, want) {
		t.Fatalf("Group key and row-value FieldSource mapping differ: %#v", mapped.Operation.Group.Keys[0].FieldSource)
	}
	relatedCoding := mapped.Operation.Group.Keys[1].FieldSource
	if relatedCoding == nil || relatedCoding.Input == nil || relatedCoding.Input.Kind != "RELATED_OCCURRENCE" ||
		relatedCoding.Input.RelatedOccurrence == nil || relatedCoding.Input.RelatedOccurrence.CodingSelector != "type.coding[]" ||
		relatedCoding.Input.RelatedOccurrence.OrdinalSemantics != recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition ||
		len(relatedCoding.Input.RelatedOccurrence.Route) != 1 || relatedCoding.Input.RelatedOccurrence.Route[0].EdgeID != "edge-subject" {
		t.Fatalf("related Coding Group key lost its exact route/selector/ordinal facts: %#v", relatedCoding)
	}
}

func TestRecipeConstructionMapsTypedSequenceAndResolvedSourceSchema(t *testing.T) {
	authoredColumns := []ws.Column{
		{ColumnID: "group_id", Column: "group", Label: "Group", LogicalType: "string"},
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
	}
	alpha, beta := "alpha", "beta"
	minimum := 10.0
	authored := &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			{
				ID: "pivot", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []ws.ConstructionPivotCategory{
						{Key: ws.TableScalar{Kind: ws.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"},
						{Key: ws.TableScalar{Kind: ws.TableScalarString, String: &beta}, OutputColumnID: "beta_id"},
					},
					DuplicatePolicy:        ws.ConstructionPivotDuplicateSum,
					MissingCellPolicy:      ws.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
				}},
				Outputs: []ws.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
				},
			},
			{
				ID: "derive", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
					ConstructionID: "derive", OutputColumnID: "total_id", Operation: ws.ConstructionDerivedAdd,
					Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "alpha_id"},
					Right:              ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "beta_id"},
					MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
				}},
				Outputs: []ws.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
					ColumnID: "total_id", Operator: ws.ConstructionFilterGreaterEq,
					Values: []ws.FilterValue{{Kind: ws.ConstructionFilterDecimal, Decimal: &minimum}},
				}},
				Outputs: []ws.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group", Type: "string"},
					{ID: "alpha_id", Name: "alpha", Label: "Alpha", Type: "decimal"},
					{ID: "beta_id", Name: "beta", Label: "Beta", Type: "decimal"},
					{ID: "total_id", Name: "total", Label: "Total", Type: "decimal"},
				},
			},
			{
				ID: "unpivot", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot", Inputs: []ws.ConstructionUnpivotInput{
						{ColumnID: "alpha_id", Key: ws.TableScalar{Kind: ws.TableScalarString, String: &alpha}},
						{ColumnID: "beta_id", Key: ws.TableScalar{Kind: ws.TableScalarString, String: &beta}},
					},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id",
					NullRowPolicy: ws.ConstructionUnpivotPreserve,
				}},
				Outputs: []ws.StageColumn{
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
	emitted := []viewer.EmittedColumn{
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
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}

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
	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	lowered, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
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

func TestCompileRelatedScalarProjectionsFeedGroupedRowValues(t *testing.T) {
	for _, test := range []struct {
		name          string
		projection    string
		policy        ws.ConstructionRowValuePolicy
		outputType    string
		wantInputMany bool
	}{
		{name: "VALUE remains scalar", projection: "VALUE", policy: ws.ConstructionRowValueAll, outputType: "array"},
		{name: "ALL collects route matches", projection: "ALL", policy: ws.ConstructionRowValueAll, outputType: "array", wantInputMany: true},
		{name: "ALL supports ONE policy", projection: "ALL", policy: ws.ConstructionRowValueOne, outputType: "string", wantInputMany: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lowered, rendered := compileRelatedScalarGroupRowValue(t, test.projection, test.policy, test.outputType)
			if len(lowered.Outputs) != 1 || lowered.Outputs[0].Plan.StageSequence == nil {
				t.Fatalf("construction stages missing: %#v", lowered.Outputs)
			}
			sequence := lowered.Outputs[0].Plan.StageSequence
			if sequence.SourceRowIdentity != "_key" || len(sequence.Stages) != 1 || sequence.Stages[0].Group == nil {
				t.Fatalf("related source changed the source row grain before Group: %#v", sequence)
			}
			group := sequence.Stages[0].Group
			if len(group.RowValues) != 1 || group.RowValues[0].InputMany != test.wantInputMany || group.RowValues[0].Policy != string(test.policy) {
				t.Fatalf("grouped related row value = %#v; want InputMany=%v Policy=%s", group.RowValues, test.wantInputMany, test.policy)
			}
			var relatedIDs *facade.CompiledOutputColumn
			for index := range lowered.Outputs[0].Stages[1].Columns {
				column := &lowered.Outputs[0].Stages[1].Columns[index]
				if column.ID == "observation_ids" {
					relatedIDs = column
					break
				}
			}
			if relatedIDs == nil || relatedIDs.Kind != "string" {
				t.Fatalf("grouped related IDs schema = %#v", relatedIDs)
			}
			if (test.policy == ws.ConstructionRowValueAll) != (relatedIDs.Cardinality == "many") {
				t.Fatalf("grouped related IDs cardinality = %#v for row-value policy %s", relatedIDs, test.policy)
			}
			if test.projection == "ALL" {
				if !strings.Contains(rendered.Query, "FOR child_set_1_edge IN @@child_set_1_edge_collection") ||
					!strings.Contains(rendered.Query, "FLATTEN(child_set_1[*].__loom_projection_0)") ||
					!strings.Contains(rendered.Query, "FOR root IN @@root_collection") {
					t.Fatalf("related ALL did not collect route matches within each root row:\n%s", rendered.Query)
				}
			}
		})
	}
}

func TestCompileRootScalarAllStillRequiresAdvertisedMode(t *testing.T) {
	document := ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []ws.Column{{ColumnID: "patient_id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "ALL"}}}},
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a", document, contributorSnapshot())
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "UNSUPPORTED_PROJECTION_MODE" {
		t.Fatalf("compile root scalar ALL = %v, want UNSUPPORTED_PROJECTION_MODE", err)
	}
}

func compileRelatedScalarGroupRowValue(t *testing.T, projection string, policy ws.ConstructionRowValuePolicy, outputType string) (facade.CompiledRecipe, facade.RenderedPhysicalPlan) {
	t.Helper()
	snapshot := contributorSnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_observation_id", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "id",
		Label: "Observation ID", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	})
	snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated,
		snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)

	document := ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{
			OccurrenceID: "observation", ResourceType: "Observation", CatalogEdgeID: "e_observation", Relationship: "focus_Patient",
		}}},
		Columns: []ws.Column{
			{ColumnID: "patient_id_source", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{ColumnID: "observation_id_source", Column: "observation_id", Label: "Observation ID", LogicalType: "string", OccurrenceID: "observation",
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: projection}}},
		},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "group_patients", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group_patients", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
				Keys:       []ws.ConstructionGroupKey{{InputColumnID: "patient_id_source", OutputColumnID: "patient_id_group"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "patient_count"}},
			}},
			Outputs: []ws.StageColumn{
				{ID: "patient_id_group", Name: "patient_id", Label: "Patient ID", Type: "string"},
				{ID: "patient_count", Name: "patient_count", Label: "Patient count", Type: "integer"},
				{ID: "observation_ids", Name: "observation_ids", Label: "Observation IDs", Type: outputType},
			},
			RowValues: []ws.ConstructionRowValue{{InputColumnID: "observation_id_source", OutputColumnID: "observation_ids", Policy: policy}},
		}}},
	}

	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("compile related scalar %s into grouped row values: %v", projection, err)
	}
	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	lowered, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower related scalar %s into grouped row values: %v", projection, err)
	}
	rendered, err := facade.RenderPhysicalPlan(lowered.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	return lowered, rendered
}

func TestRecipeConstructionKeepsScalarGroupSourceOutOfPublicColumns(t *testing.T) {
	projection := ws.ConstructionSourceProjection{
		ColumnID: "source_active", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
	}
	authored := &ws.Construction{
		Version: ws.ConstructionVersion, SourceProjections: []ws.ConstructionSourceProjection{projection},
		Steps: []ws.ConstructionStep{{
			ID: "group_by_active", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group_by_active", Keys: []ws.ConstructionGroupKey{{InputColumnID: projection.ColumnID, OutputColumnID: "active_key"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "record_count"}},
			}},
			Outputs: []ws.StageColumn{
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
		ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
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
	columns := []ws.Column{{
		ColumnID: "indexed_id", Column: "indexed", Label: "Indexed", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "items[]", ProjectionMode: "INDEXED"}},
	}}
	emitted := []viewer.EmittedColumn{
		{EmissionID: "indexed-a", OccurrenceID: ws.RootOccurrenceID, AuthoredColumns: []string{"indexed"}, PublicColumn: "indexed_0", Label: "Indexed [0]", LogicalType: "string", Nullable: true, Shape: "indexed_scalar", Coordinates: []capability.RepeatedCoordinate{{BoundaryPath: "items[]", Index: 0, Width: 2}}},
		{EmissionID: "indexed-count", OccurrenceID: ws.RootOccurrenceID, AuthoredColumns: []string{"indexed"}, PublicColumn: "items__count", Label: "Item count", LogicalType: "integer", Shape: "repeated_count"},
	}
	got, err := recipeConstructionSourceColumns(columns, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "indexed_0" || got[1].Name != "items__count" {
		t.Fatalf("mapped indexed source schema = %#v", got)
	}
	valueID, canonical, err := lineage.StableSourceChildID(lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"indexed_id"}, OccurrenceID: ws.RootOccurrenceID,
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
	document := ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []ws.Column{
			{ColumnID: "given_slot", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}}},
			{ColumnID: "family_slot", Column: "family", Label: "Family", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].family", ProjectionMode: "INDEXED"}}},
		},
		Construction: &ws.Construction{Version: ws.ConstructionVersion},
	}
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(result Result) facade.CompiledRecipeOutput {
		t.Helper()
		plan, err := recipeplan.BuildRecipePlan(result.Bundle, recipe.RuntimeBindings{Project: "project-a"})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
		if err != nil {
			t.Fatal(err)
		}
		physical, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return physical.Outputs[0]
	}

	output := resolve(compiled)
	if len(output.Stages) == 0 || output.Stages[0].ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("compiled source stages = %#v", output.Stages)
	}
	stageColumns := make(map[string]facade.CompiledOutputColumn)
	for _, column := range output.Stages[0].Columns {
		if !column.Internal {
			stageColumns[column.Name] = column
		}
	}
	if len(stageColumns) != len(compiled.EmittedColumns) {
		t.Fatalf("source stage has %d public columns, compiler emitted %d: %#v", len(stageColumns), len(compiled.EmittedColumns), stageColumns)
	}
	var filterColumn facade.CompiledOutputColumn
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
	renamed.Columns = append([]ws.Column(nil), document.Columns...)
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

	outputs := make([]ws.StageColumn, 0, len(compiled.Bundle.Outputs[0].Construction.SourceColumns))
	for _, column := range compiled.Bundle.Outputs[0].Construction.SourceColumns {
		outputs = append(outputs, ws.StageColumn{ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Nullable: column.Nullable})
	}
	document.Construction.Steps = []ws.ConstructionStep{{
		ID: "keep_given_item", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: filterColumn.ID, Operator: ws.ConstructionFilterExists}},
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
	authoredColumns := []ws.Column{
		{ColumnID: "status_id", Column: "status", Label: "Status", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
		{ColumnID: "tags_id", Column: "tags", Label: "Tags", LogicalType: "string"},
	}
	authored := &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			{
				ID: "expand_tags", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationExpand, Expand: &ws.ConstructionExpand{
					ConstructionID: "expand_tags", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "position_id", EmptyPolicy: ws.ConstructionExpandEmptyPreserveParent,
				}},
				Outputs: []ws.StageColumn{
					{ID: "status_id", Name: "status", Label: "Status"}, {ID: "amount_id", Name: "amount", Label: "Amount"},
					{ID: "tag_id", Name: "tag", Label: "Tag"}, {ID: "position_id", Name: "position", Label: "Position"},
				},
			},
			{
				ID: "group_tags", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "expand_tags"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
					ConstructionID: "group_tags",
					Keys:           []ws.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
					Aggregates: []ws.ConstructionGroupAggregate{
						{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: ws.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: ws.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: ws.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: ws.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []ws.StageColumn{
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
	emitted := []viewer.EmittedColumn{
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
