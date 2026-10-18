package lower

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRelatedGroupFieldSourcesShareOneExactRouteAdapter(t *testing.T) {
	base := compileDerivedTestOutput(t, recipe.Output{
		Name: "patient_group_source", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	context, err := describeGroupFieldSourceContext(recipe.ConstructionSourceProjectionID, "Patient", "_key", &base.Plan)
	if err != nil || context == nil {
		t.Fatalf("describe related Group source context: context=%#v err=%v", context, err)
	}
	makeSource := func(valueKind, resourceType, candidateID, canonicalPath string) recipe.ConstructionGroupFieldSource {
		source := recipe.ConstructionGroupFieldSource{
			Kind: "BINDING", ValueKind: valueKind,
			Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &recipe.ConstructionGroupRelatedOccurrenceFieldSource{
				AnchorNodeID: "catalog-patient-node", AnchorResourceType: "Patient",
				Route: []recipe.ConstructionGroupRelatedRouteStep{{
					StepIndex: 0, FromNodeID: "catalog-patient-node", FromResourceType: "Patient",
					EdgeID: "patient-observation-edge", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					ToNodeID: "catalog-observation-node", ToResourceType: "Observation",
				}},
				TargetNodeID: "catalog-observation-node", TargetResourceType: "Observation",
				ResourceType: resourceType, CandidateID: candidateID, CanonicalPath: canonicalPath,
				EmptyPolicy:  recipe.ExpansionPreserveParent,
				Multiplicity: recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
			}},
		}
		if valueKind == "CODING_IDENTITY" {
			source.Input.RelatedOccurrence.ResourceType = "Coding"
			source.Input.RelatedOccurrence.CodingSelector = "valueCodeableConcept.coding[]"
			source.Input.RelatedOccurrence.CodingEmptyPolicy = recipe.ConstructionGroupCodingEmptyPolicyGroup
			source.Input.RelatedOccurrence.OrdinalSemantics = recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition
		}
		bound, err := BindGroupRelatedOccurrenceSource(context, source)
		if err != nil {
			t.Fatalf("bind %s source: %v", valueKind, err)
		}
		return bound
	}
	status := makeSource("SCALAR", "string", "observation-status", "status")
	coding := makeSource("CODING_IDENTITY", "Coding", "", "")
	quantity := makeSource("SCALAR", "decimal", "observation-value-quantity-value", "valueQuantity.value")
	output := recipe.Output{
		Name: "related_group", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_related", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_related_id", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys: []recipe.ConstructionGroupKey{
						{OutputColumnID: "status_group", FieldSource: &status},
						{OutputColumnID: "coding_group", FieldSource: &coding},
					},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "status_group", Name: "status_group"},
					{ID: "coding_group", Name: "coding_group"},
					{ID: "row_count", Name: "row_count"},
					{ID: "quantity_values", Name: "quantity_values", Type: "array"},
				},
				RowValues: []recipe.ConstructionRowValue{{FieldSource: &quantity, OutputColumnID: "quantity_values", Policy: recipe.ConstructionRowValueAll}},
			}},
		},
	}
	if !constructionTerminalRelatedCodingGroupRequeriesPreimage(output.Construction.Steps, &base.Plan, "_key", "Patient") {
		t.Fatal("exact terminal related Coding Group did not authorize on-demand preimage requery")
	}
	compiled, err := compileDerivedTestBundle(t, output)
	if err != nil {
		t.Fatalf("compile related Group: %v", err)
	}
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 1 {
		t.Fatalf("compiled construction stages = %#v; want one Group stage", compiled.Plan.StageSequence)
	}
	stage := compiled.Plan.StageSequence.Stages[0]
	group := stage.Group
	if group == nil || group.RelatedOccurrence == nil || group.CodingKey == nil {
		t.Fatalf("typed Group is missing its shared route adapter/Coding key: %#v", group)
	}
	adapter := group.RelatedOccurrence
	if adapter.RouteID == "" || adapter.RouteHasItemVariable == "" || adapter.RelatedRecords.RouteID != adapter.RouteID ||
		len(adapter.RelatedRecords.Route) != 1 || adapter.RelatedRecords.Route[0].ToResourceType != "Observation" {
		t.Fatalf("typed Group route adapter = %#v", adapter)
	}
	if len(adapter.ScalarProjections) != 2 || adapter.CodingArrayColumn == "" || adapter.CodingMembershipColumn == "" ||
		adapter.CodingUnnest == nil || adapter.CodingUnnest.Ordinality == "" ||
		group.CodingKey.SourceUnnest != nil || group.CodingKey.RelatedSource == nil ||
		group.CodingKey.SourceOccurrenceKeyColumn != adapter.OccurrenceKeyColumn {
		t.Fatalf("related Coding identity did not reuse one exact target projection/ordinal adapter: adapter=%#v coding=%#v", adapter, group.CodingKey)
	}
	var rootKeyColumn ir.PhysicalStageColumn
	rootKeyColumnFound := false
	for _, column := range stage.InputColumns {
		if column.Name == "_key" {
			rootKeyColumn, rootKeyColumnFound = column, true
			break
		}
	}
	if !rootKeyColumnFound || !rootKeyColumn.Internal || !rootKeyColumn.Identity ||
		rootKeyColumn.RootContributorResourceType != "Patient" {
		t.Fatalf("related Group typed inputs omitted the exact root key: %#v", stage.InputColumns)
	}
	rootKeyProjectionFound := false
	for _, projection := range stage.InputProjections {
		if projection.Name == "_key" && projection.Hidden && projection.Expression == nil &&
			projection.Value.Variable == stage.InputRowVariable && reflect.DeepEqual(projection.Value.Path, []string{"_key"}) {
			rootKeyProjectionFound = true
		}
	}
	if !rootKeyProjectionFound {
		t.Fatalf("related Group has no exact typed _key input projection: %#v", stage.InputProjections)
	}
	compiledSourceOperations := ir.ClonePhysicalPlan(ir.PhysicalPlan{Operations: compiled.Plan.Operations}).Operations
	baseSourceOperations := ir.ClonePhysicalPlan(ir.PhysicalPlan{Operations: base.Plan.Operations}).Operations
	for index := range compiledSourceOperations {
		operation := &compiledSourceOperations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := operation.Return.Projections[:0]
		for _, projection := range operation.Return.Projections {
			if projection.Name != "_key" {
				projections = append(projections, projection)
			}
		}
		operation.Return.Projections = projections
	}
	for index := range baseSourceOperations {
		operation := &baseSourceOperations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := operation.Return.Projections[:0]
		for _, projection := range operation.Return.Projections {
			if projection.Name != "_key" {
				projections = append(projections, projection)
			}
		}
		operation.Return.Projections = projections
	}
	if !reflect.DeepEqual(compiledSourceOperations, baseSourceOperations) {
		t.Fatal("related Group changed source operations beyond the exact hidden root-key projection")
	}
	if group.RowValues[0].FieldSource == nil || group.RowValues[0].FieldSource.RelatedOccurrence == nil ||
		group.RowValues[0].FieldSource.RelatedOccurrence.RouteID != adapter.RouteID {
		t.Fatalf("related RowValue did not share the Group-owned route: %#v", group.RowValues)
	}
	if err := compiled.Plan.Validate(); err != nil {
		t.Fatalf("validate compiled related Group: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatalf("render compiled related Group: %v", err)
	}
	if strings.Contains(rendered.Query, "LET "+stage.InputRowVariable+" = {}") ||
		!strings.Contains(rendered.Query, "LET "+stage.InputRowVariable+" = {") ||
		!strings.Contains(rendered.Query, stage.InputRowVariable+"._key") {
		t.Fatalf("related Group route is not anchored by the projected source _key:\n%s", rendered.Query)
	}
	if compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageGroupOp {
		t.Fatalf("stage kind = %q, want GROUP", stage.Kind)
	}
}
