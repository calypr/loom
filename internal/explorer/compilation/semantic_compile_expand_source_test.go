package compilation

import (
	"context"
	"errors"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/capability"
)

func expandCompileSnapshot(uriLogicalType string) capability.Snapshot {
	base := fixtureSnapshot()
	return capability.NewSnapshot(base.Identity, base.Policy, capability.StatusReady, true, false,
		[]capability.Node{{ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "procedure", Populated: true}},
		nil,
		[]capability.Candidate{
			{ID: "c_procedure_id", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
			{ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "instantiatesUri[]", Label: "Instantiates uri", LogicalType: uriLogicalType, Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}, RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "instantiatesUri[]", MaxItems: 2}}},
			{ID: "c_procedure_use", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "identifier.use", Label: "Identifier use", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		}, nil)
}

func expandCompileDocument(projection ws.ConstructionSourceProjection, payload *ws.ConstructionExpandedFieldSource, steps []ws.ConstructionStep) ws.Document {
	return ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "procedures", Title: "Procedures"}, RootResourceType: "Procedure",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Procedure"},
		Columns: []ws.Column{{
			ColumnID: "procedure-id", Column: "procedure_id", Label: "Procedure ID",
			LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps:             steps,
		},
	}
}

func expandCompileSignedProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "expand_uri", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
}

func expandCompileSignedPayload() *ws.ConstructionExpandedFieldSource {
	return &ws.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "string"}
}

func expandCompileStep(payload *ws.ConstructionExpandedFieldSource) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "expand_uri",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationExpand, Expand: &ws.ConstructionExpand{
			ConstructionID: "expand_uri", InputColumnID: "uri_column", OutputColumnID: "uri_item",
			EmptyPolicy:   ws.ConstructionExpandEmptyExclude,
			ExpandedField: payload,
		}},

		Outputs: []ws.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
		},
	}
}

func expandCompileLower(t *testing.T, document ws.Document, snapshot capability.Snapshot) facade.CompiledRecipe {
	t.Helper()
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("BUG-002/004: compile signed expand state: %v", err)
	}
	plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("resolve plan: %v", err)
	}
	lowered, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return lowered
}

func expandCompileError(err error) string {
	var compileErr *Error
	if errors.As(err, &compileErr) {
		return compileErr.Code
	}
	return ""
}

func TestCompileConstructionExpandSignedArrayProjectionCompilesWithManyCardinality(t *testing.T) {
	lowered := expandCompileLower(t,
		expandCompileDocument(expandCompileSignedProjection(), expandCompileSignedPayload(),
			[]ws.ConstructionStep{expandCompileStep(expandCompileSignedPayload())}),
		expandCompileSnapshot("string"))
	if len(lowered.Outputs) != 1 {
		t.Fatalf("lowered outputs = %d, want one", len(lowered.Outputs))
	}
	output := lowered.Outputs[0]
	slotName := ws.ConstructionSourceProjectionName("uri_column")
	var sourceCardinality string
	for _, stage := range output.Stages {
		if stage.ID == recipe.ConstructionSourceProjectionID {
			for _, column := range stage.Columns {
				if column.Name == slotName {
					sourceCardinality = string(column.Cardinality)
				}
			}
		}
	}
	if sourceCardinality != "many" {
		t.Fatalf("compiled root array column %q has cardinality %q, want many for the physical EXPAND gate", slotName, sourceCardinality)
	}
	expandStageSeen := false
	for _, stage := range output.Stages {
		if stage.Operation == string(recipe.ConstructionExpandOp) {
			expandStageSeen = true
		}
	}
	if !expandStageSeen {
		t.Fatalf("compiled pipeline lost the EXPAND stage: %#v", output.Stages)
	}
}

func TestCompileConstructionExpandProjectionWithoutOwnerIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.OwnerStepID = ""
	document := expandCompileDocument(projection, expandCompileSignedPayload(),
		[]ws.ConstructionStep{expandCompileStep(expandCompileSignedPayload())})
	_, err := Compile(context.Background(), "project-a", "explorer-a", document, expandCompileSnapshot("string"))
	if err == nil || expandCompileError(err) != "INVALID_DOCUMENT" {
		t.Fatalf("unowned expand projection error = %v, want INVALID_DOCUMENT from the authoring owner-consumption wall", err)
	}
	if !strings.Contains(err.Error(), "unowned source projections are supported only by a direct-source GROUP step") {
		t.Fatalf("unowned expand projection error = %v, want the explicit unowned-state refusal", err)
	}
}

func TestCompileConstructionExpandPayloadClaimingScalarAsManyIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.FieldPath = "status"
	projection.FHIRType = "string"
	projection.LogicalType = "code"
	payload := &ws.ConstructionExpandedFieldSource{Path: "status", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
	step := expandCompileStep(payload)
	step.Outputs[1].Type = "code"
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		expandCompileSnapshotWithStatus("string"))
	if err == nil || expandCompileError(err) != "STALE_CONSTRUCTION_SOURCE_FIELD" {
		t.Fatalf("scalar-as-many forged payload error = %v, want the signed-array candidate-class refusal (snapshot backing is a scalar VALUE candidate)", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Details["fieldPath"] != "status" {
		t.Fatalf("refusal must name the forged field path: %#v", err)
	}
}

func TestCompileConstructionExpandSnapshotForgedArrayBackingIsRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.FieldPath = "status"
	projection.FHIRType = "string"
	projection.LogicalType = "code"
	payload := &ws.ConstructionExpandedFieldSource{Path: "status", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
	step := expandCompileStep(payload)
	step.Outputs[1].Type = "code"
	snapshot := expandCompileSnapshotWithStatus("string")
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].FieldPath == "status" {
			snapshot.Candidates[index].Cardinality = "many"
			snapshot.Candidates[index].ProjectionModes = []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}
			snapshot.Candidates[index].RepeatedBoundaries = []capability.RepeatedBoundary{{Path: "status", MaxItems: 2}}
		}
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		snapshot)
	if err == nil || expandCompileError(err) != "STALE_EXPANDED_SOURCE_SCHEMA" {
		t.Fatalf("snapshot-forged array backing error = %v, want STALE_EXPANDED_SOURCE_SCHEMA from the generated-schema verifier", err)
	}
}

func expandCompileSnapshotWithStatus(uriLogicalType string) capability.Snapshot {
	base := expandCompileSnapshot(uriLogicalType)
	for index := range base.Candidates {
		if base.Candidates[index].FieldPath == "identifier.use" {
			base.Candidates[index] = capability.Candidate{ID: "c_procedure_status", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "status", Label: "Status", LogicalType: "code", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
		}
	}
	return base
}

func TestCompileConstructionExpandObjectKindItemsAreRefused(t *testing.T) {
	projection := expandCompileSignedProjection()
	projection.LogicalType = "object"
	payload := &ws.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: ws.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "object"}
	step := expandCompileStep(payload)
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []ws.ConstructionStep{step}),
		expandCompileSnapshot("object"))
	if err == nil || expandCompileError(err) != "UNSUPPORTED_EXPANDED_SOURCE_KIND" {
		t.Fatalf("BUG-002: object-kind items error = %v, want UNSUPPORTED_EXPANDED_SOURCE_KIND", err)
	}
}

func TestCompileConstructionExpandStyledWrongOwnerProjectionRefused(t *testing.T) {
	projection := ws.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "derive_uri_copy", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
	derive := ws.ConstructionStep{
		ID:     "derive_uri_copy",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},

		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID: "derive_uri_copy", OutputColumnID: "uri_copy", Operation: ws.ConstructionDerivedAdd,
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "uri_column"},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: expandCompileInt(1)}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_column", Name: ws.ConstructionSourceProjectionName("uri_column"), Label: "Instantiates uri", Type: "string"},
			{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		},
	}
	trailingExpand := expandCompileStep(expandCompileSignedPayload())
	trailingExpand.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_uri_copy"}}
	trailingExpand.Outputs = []ws.StageColumn{
		{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
		{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, expandCompileSignedPayload(),
			[]ws.ConstructionStep{derive, trailingExpand}),
		expandCompileSnapshot("string"))
	if err == nil || expandCompileError(err) != "STALE_CONSTRUCTION_SOURCE_FIELD" {
		t.Fatalf("wrong-owner expand-styled projection error = %v, want the scalar-lane VALUE-backing candidate refusal (proves non-credit)", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Details["fieldPath"] != "instantiatesUri[]" {
		t.Fatalf("refusal should name the forged field path in its details: %#v", err)
	}
	if !strings.Contains(refusal.Path, "sourceProjections[0]") {
		t.Fatalf("refusal should locate the forged projection slot: %q", refusal.Path)
	}
}

func expandCompileInt(value int64) *int64 { return &value }
