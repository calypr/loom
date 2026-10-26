package lower

import (
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestWorkspaceCombineScopeEvidenceComposesExactOutputAndPublishedOperands(t *testing.T) {
	source := compileWholeRelationTestOutput(t, wholeRelationGroupOutput(), recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted,
	})
	if source.ScopeEvidence == nil || source.ScopeEvidenceIdentity == nil {
		t.Fatal("source output has no compiler-issued scope evidence")
	}

	outputRef := ir.PhysicalCombineInputRef{WorkspaceOutputID: source.Name}
	publishedRef := ir.PhysicalCombineInputRef{TableID: "4:r1:1v7:published", RevisionID: "revision-1", OutputID: "published"}
	plan := testWorkspaceCombinePlan("group_gender_id", outputRef, "source_value", publishedRef)
	identity := ir.WholeRelationScopeIdentity{
		OutputID: "combined", Project: "project", DatasetGeneration: "generation",
		AuthScopeMode: "unrestricted", SchemaDigest: "combined-schema",
	}
	outputOperand, err := ir.NewWorkspaceOutputScopeOperand(0, outputRef, source.Plan, *source.ScopeEvidenceIdentity, *source.ScopeEvidence)
	if err != nil {
		t.Fatalf("workspace output operand: %v", err)
	}
	published := testResolvedPublishedInput(publishedRef)
	publishedOperand, err := ir.NewPublishedWorkspaceScopeOperand(1, publishedRef, published, identity)
	if err != nil {
		t.Fatalf("published operand: %v", err)
	}
	evidence, err := ir.NewWorkspaceCombineScopeEvidence(plan, identity, []ir.WorkspaceCombineScopeOperand{outputOperand, publishedOperand})
	if err != nil {
		t.Fatalf("workspace Combine evidence: %v", err)
	}
	if evidence.Digest() == "" || !evidence.Matches(plan, identity) {
		t.Fatal("workspace Combine evidence does not match its exact optimized plan and output identity")
	}

	reordered := []ir.WorkspaceCombineScopeOperand{publishedOperand, outputOperand}
	if _, err := ir.NewWorkspaceCombineScopeEvidence(plan, identity, reordered); err == nil {
		t.Fatal("workspace Combine evidence accepted reordered input provenance")
	}
	wrongPublishedSchema := published
	wrongPublishedSchema.Columns = append([]ir.ResolvedClickHouseColumn(nil), published.Columns...)
	wrongPublishedSchema.Columns[1].ID = "substituted_source_column"
	wrongPublishedOperand, err := ir.NewPublishedWorkspaceScopeOperand(1, publishedRef, wrongPublishedSchema, identity)
	if err != nil {
		t.Fatalf("construct substituted-schema operand: %v", err)
	}
	if _, err := ir.NewWorkspaceCombineScopeEvidence(plan, identity, []ir.WorkspaceCombineScopeOperand{outputOperand, wrongPublishedOperand}); err == nil {
		t.Fatal("workspace Combine evidence accepted a resolved schema that substitutes a referenced source column")
	}
	wrongScope := identity
	wrongScope.DatasetGeneration = "other-generation"
	if _, err := ir.NewWorkspaceCombineScopeEvidence(plan, wrongScope, []ir.WorkspaceCombineScopeOperand{outputOperand, publishedOperand}); err == nil {
		t.Fatal("workspace Combine evidence accepted an operand from another dataset generation")
	}
	mutated := ir.ClonePhysicalPlan(plan)
	mutated.ClickHouseCombine.Inputs[1].RevisionID = "other-revision"
	if evidence.Matches(mutated, identity) {
		t.Fatal("workspace Combine evidence matched a substituted immutable revision")
	}
}

func TestWorkspaceCombineScopeOperandsRejectSourceAndResolverSubstitution(t *testing.T) {
	source := compileDerivedTestOutput(t, recipe.Output{
		Name: "base", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "source_value", ColumnID: "source_value", Expr: recipe.Expression{Select: "root.id"}}},
	})
	if source.ScopeEvidence == nil || source.ScopeEvidenceIdentity == nil {
		t.Fatal("source output has no compiler-issued scope evidence")
	}
	ref := ir.PhysicalCombineInputRef{WorkspaceOutputID: source.Name}
	wrongSourceIdentity := *source.ScopeEvidenceIdentity
	wrongSourceIdentity.SchemaDigest += "-substituted"
	if _, err := ir.NewWorkspaceOutputScopeOperand(0, ref, source.Plan, wrongSourceIdentity, *source.ScopeEvidence); err == nil {
		t.Fatal("workspace output operand accepted a substituted source schema identity")
	}
	wrongRef := ref
	wrongRef.WorkspaceOutputID = "different-output"
	if _, err := ir.NewWorkspaceOutputScopeOperand(0, wrongRef, source.Plan, *source.ScopeEvidenceIdentity, *source.ScopeEvidence); err == nil {
		t.Fatal("workspace output operand accepted a different sibling output ID")
	}

	publishedRef := ir.PhysicalCombineInputRef{TableID: "4:r1:1v7:published", RevisionID: "revision-1", OutputID: "published"}
	identity := ir.WholeRelationScopeIdentity{
		OutputID: "combined", Project: "project", DatasetGeneration: "generation",
		AuthScopeMode: "unrestricted", SchemaDigest: "combined-schema",
	}
	resolved := testResolvedPublishedInput(publishedRef)
	wrongRevision := publishedRef
	wrongRevision.RevisionID = "revision-2"
	if _, err := ir.NewPublishedWorkspaceScopeOperand(0, wrongRevision, resolved, identity); err == nil {
		t.Fatal("published operand accepted a substituted immutable revision")
	}
	wrongPublishedScope := resolved
	wrongPublishedScope.DatasetGeneration = "other-generation"
	if _, err := ir.NewPublishedWorkspaceScopeOperand(0, publishedRef, wrongPublishedScope, identity); err == nil {
		t.Fatal("published operand accepted a different dataset generation")
	}
	wrongPublishedSchema := resolved
	wrongPublishedSchema.SchemaDigest = ""
	if _, err := ir.NewPublishedWorkspaceScopeOperand(0, publishedRef, wrongPublishedSchema, identity); err == nil {
		t.Fatal("published operand accepted missing immutable schema provenance")
	}
}

func TestWorkspaceCombineScopeEvidenceBindsExactPrivateAQLPrefix(t *testing.T) {
	source := compileDerivedTestOutput(t, recipe.Output{
		Name: "expanded", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_tags", InputColumnID: "tags_id", OutputColumnID: "tag_id", OrdinalColumnID: "ordinal_id",
					EmptyPolicy: recipe.ExpansionPreserveParent,
				}},
				Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
			}},
		},
	})
	if source.Plan.StageSequence == nil || source.ScopeEvidence == nil || source.ScopeEvidenceIdentity == nil {
		t.Fatal("unbounded AQL prefix lacks its typed stage or scope evidence")
	}
	bindings := recipe.RuntimeBindings{
		Project: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	privateRef := ir.PhysicalCombineInputRef{PrivateStageID: source.Plan.StageSequence.FinalStageID}
	publishedRef := ir.PhysicalCombineInputRef{TableID: "4:r1:1v7:published", RevisionID: "revision-1", OutputID: "published"}
	combine := testWorkspaceCombinePayload("tag_id", privateRef, "source_value", publishedRef)
	actualPlan, err := ComposeClickHouseCombine(source.Plan, *combine, bindings)
	if err != nil {
		t.Fatalf("compose exact AQL prefix and Combine: %v", err)
	}
	identity := *source.ScopeEvidenceIdentity
	identity.OutputID = "combined"
	identity.SchemaDigest = "combined-schema"
	privateOperand, err := ir.NewPrivateAQLPrefixScopeOperand(0, privateRef, source.Plan, *source.ScopeEvidenceIdentity, *source.ScopeEvidence)
	if err != nil {
		t.Fatalf("private AQL prefix operand: %v", err)
	}
	publishedOperand, err := ir.NewPublishedWorkspaceScopeOperand(1, publishedRef, testResolvedPublishedInput(publishedRef), identity)
	if err != nil {
		t.Fatalf("published operand: %v", err)
	}
	evidence, err := ir.NewWorkspaceCombineScopeEvidence(actualPlan, identity, []ir.WorkspaceCombineScopeOperand{privateOperand, publishedOperand})
	if err != nil {
		t.Fatalf("composite workspace evidence: %v", err)
	}
	if !evidence.Matches(actualPlan, identity) {
		t.Fatal("composite workspace evidence does not match exact private-prefix plan")
	}

	changedPrefix := ir.ClonePhysicalPlan(actualPlan)
	changedPrefix.BindVars["dataset_generation"] = "other-generation"
	if evidence.Matches(changedPrefix, identity) {
		t.Fatal("composite workspace evidence matched a private prefix with a substituted generation")
	}
}

func testWorkspaceCombinePlan(firstColumn string, first ir.PhysicalCombineInputRef, secondColumn string, second ir.PhysicalCombineInputRef) ir.PhysicalPlan {
	return ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: testWorkspaceCombinePayload(firstColumn, first, secondColumn, second)}
}

func testWorkspaceCombinePayload(firstColumn string, first ir.PhysicalCombineInputRef, secondColumn string, second ir.PhysicalCombineInputRef) *ir.PhysicalClickHouseCombine {
	return &ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend, Inputs: []ir.PhysicalCombineInputRef{first, second},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "left_value", InputIndex: 0, InputColumnID: firstColumn},
			{OutputColumnID: "right_value", InputIndex: 1, InputColumnID: secondColumn},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "left_value", Name: "left_value", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			{ID: "right_value", Name: "right_value", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		},
	}
}

func testResolvedPublishedInput(ref ir.PhysicalCombineInputRef) ir.ResolvedClickHouseTable {
	return ir.ResolvedClickHouseTable{
		TableID: ref.TableID, RevisionID: ref.RevisionID, OutputID: ref.OutputID,
		Project: "project", DatasetGeneration: "generation", ReceiptID: "receipt-1",
		SchemaDigest: "published-schema", ScopeDigest: "published-scope", PhysicalTable: "published_table",
		Unrestricted: true, Columns: []ir.ResolvedClickHouseColumn{{
			ID: "", Name: "__loom_row_id", ClickHouseType: "String",
		}, {
			ID: "source_value", Name: "source_value", LogicalType: "string", ClickHouseType: "String",
		}},
	}
}
