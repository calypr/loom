package compilation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
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

func expandCompileDocument(projection authoringv2.ConstructionSourceProjection, payload *authoringv2.ConstructionExpandedFieldSource, steps []authoringv2.ConstructionStep) authoringv2.Document {
	return authoringv2.Document{
		Rows: authoringv2.RecordsRowDefinition(), Kind: authoringv2.Kind,
		Output: authoringv2.Output{ID: "procedures", Title: "Procedures"}, RootResourceType: "Procedure",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Procedure"},
		Columns: []authoringv2.Column{{
			ColumnID: "procedure-id", Column: "procedure_id", Label: "Procedure ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
		Construction: &authoringv2.Construction{
			Version:           authoringv2.ConstructionVersion,
			SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
			Steps:             steps,
		},
	}
}

func expandCompileSignedProjection() authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "expand_uri", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
}

func expandCompileSignedPayload() *authoringv2.ConstructionExpandedFieldSource {
	return &authoringv2.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: authoringv2.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "string"}
}

func expandCompileStep(payload *authoringv2.ConstructionExpandedFieldSource) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     "expand_uri",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationExpand, Expand: &authoringv2.ConstructionExpand{
			ConstructionID: "expand_uri", InputColumnID: "uri_column", OutputColumnID: "uri_item",
			EmptyPolicy:   authoringv2.ConstructionExpandEmptyExclude,
			ExpandedField: payload,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
		},
	}
}

func expandCompileLower(t *testing.T, document authoringv2.Document, snapshot capability.Snapshot) lower.CompiledRecipe {
	t.Helper()
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatalf("BUG-002/004: compile signed expand state: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("resolve plan: %v", err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
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
			[]authoringv2.ConstructionStep{expandCompileStep(expandCompileSignedPayload())}),
		expandCompileSnapshot("string"))
	if len(lowered.Outputs) != 1 {
		t.Fatalf("lowered outputs = %d, want one", len(lowered.Outputs))
	}
	output := lowered.Outputs[0]
	slotName := authoringv2.ConstructionSourceProjectionName("uri_column")
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
		[]authoringv2.ConstructionStep{expandCompileStep(expandCompileSignedPayload())})
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
	payload := &authoringv2.ConstructionExpandedFieldSource{Path: "status", Cardinality: authoringv2.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
	step := expandCompileStep(payload)
	step.Outputs[1].Type = "code"
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []authoringv2.ConstructionStep{step}),
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
	payload := &authoringv2.ConstructionExpandedFieldSource{Path: "status", Cardinality: authoringv2.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "code"}
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
		expandCompileDocument(projection, payload, []authoringv2.ConstructionStep{step}),
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
	payload := &authoringv2.ConstructionExpandedFieldSource{Path: "instantiatesUri[]", Cardinality: authoringv2.ConstructionExpandedFieldCardinalityMany, FHIRType: "string", LogicalType: "object"}
	step := expandCompileStep(payload)
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, payload, []authoringv2.ConstructionStep{step}),
		expandCompileSnapshot("object"))
	if err == nil || expandCompileError(err) != "UNSUPPORTED_EXPANDED_SOURCE_KIND" {
		t.Fatalf("BUG-002: object-kind items error = %v, want UNSUPPORTED_EXPANDED_SOURCE_KIND", err)
	}
}

func TestCompileConstructionExpandStyledWrongOwnerProjectionRefused(t *testing.T) {
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: "derive_uri_copy", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
	}
	derive := authoringv2.ConstructionStep{
		ID:     "derive_uri_copy",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: "derive_uri_copy", OutputColumnID: "uri_copy", Operation: authoringv2.ConstructionDerivedAdd,
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "uri_column"},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: expandCompileInt(1)}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			{ID: "uri_column", Name: authoringv2.ConstructionSourceProjectionName("uri_column"), Label: "Instantiates uri", Type: "string"},
			{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		},
	}
	trailingExpand := expandCompileStep(expandCompileSignedPayload())
	trailingExpand.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive_uri_copy"}}
	trailingExpand.Outputs = []authoringv2.StageColumn{
		{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
		{ID: "uri_copy", Name: "uri_copy", Label: "Uri copy", Type: "string"},
		{ID: "uri_item", Name: "uri_item", Label: "Uri item", Type: "string"},
	}
	_, err := Compile(context.Background(), "project-a", "explorer-a",
		expandCompileDocument(projection, expandCompileSignedPayload(),
			[]authoringv2.ConstructionStep{derive, trailingExpand}),
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
