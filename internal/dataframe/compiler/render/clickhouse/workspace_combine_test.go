package clickhouse

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRenderWorkspaceAppendCapturesExactSameBundleInputsInOrder(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{WorkspaceOutputID: "first"},
			{WorkspaceOutputID: "second"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "first-value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "second-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	first := workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeRows)
	second := workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	first.Artifact.Columns[1].ID = "first-value"
	first.Artifact.Columns[1].Name = "first_value"
	second.Artifact.Columns[1].ID = "second-value"
	second.Artifact.Columns[1].Name = "second_value"
	first.ExpectedIdentity.SchemaDigest = first.Artifact.Identity.SchemaDigest
	second.ExpectedIdentity.SchemaDigest = second.Artifact.Identity.SchemaDigest
	first.Artifact.Identity.SchemaDigest = first.ExpectedIdentity.SchemaDigest
	second.Artifact.Identity.SchemaDigest = second.ExpectedIdentity.SchemaDigest

	rendered, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{first, second}, nil, "project-a", "generation-a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FROM `loom_private_first` AS __loom_input_0") || !strings.Contains(rendered.Query, "FROM `loom_private_second` AS __loom_input_1") {
		t.Fatalf("workspace append lost authored source order: %s", rendered.Query)
	}
	if !strings.HasSuffix(rendered.Query, "LIMIT 3") {
		t.Fatalf("terminal preview limit was not applied after append: %s", rendered.Query)
	}
	if !reflect.DeepEqual(rendered.Columns, []string{"__loom_row_id", "value", "auth_resource_path"}) {
		t.Fatalf("rendered columns = %#v", rendered.Columns)
	}
}

func TestRenderWorkspaceAppendPreservesCompleteMultiPathScope(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{WorkspaceOutputID: "first"},
			{WorkspaceOutputID: "second"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "first-value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "second-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	paths := []string{"/programs/p1", "/programs/p2"}
	first := workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeRows)
	second := workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	for _, artifact := range []*WorkspaceArtifact{&first, &second} {
		artifact.ExpectedIdentity.AuthResourcePaths = append([]string(nil), paths...)
		artifact.Artifact.Identity.AuthResourcePaths = append([]string(nil), paths...)
		artifact.ExpectedIdentity.ScopeDigest = scopeTestDigest("two-path-scope")
		artifact.Artifact.Identity.ScopeDigest = artifact.ExpectedIdentity.ScopeDigest
	}
	first.Artifact.Columns[1].ID, first.Artifact.Columns[1].Name = "first-value", "first_value"
	second.Artifact.Columns[1].ID, second.Artifact.Columns[1].Name = "second-value", "second_value"

	rendered, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{first, second}, nil, "project-a", "generation-a", 0)
	if err != nil {
		t.Fatalf("RenderWorkspaceClickHouseCombine: %v", err)
	}
	wantArgs := []any{append([]string(nil), paths...), append([]string(nil), paths...)}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("multi-path authorization binds = %#v, want exact per-input paths %#v", rendered.Args, wantArgs)
	}
	if strings.Count(rendered.Query, "auth_resource_path` IN ?") != 2 {
		t.Fatalf("multi-path APPEND did not scope both complete inputs: %s", rendered.Query)
	}
}

func TestRenderWorkspaceCombineKeepsMixedPublishedScopeAndSealsGroupedOutput(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{WorkspaceOutputID: "grouped"},
			{TableID: "published-table", RevisionID: "published-revision", OutputID: "published-output"},
		},
		Keys:             []ir.PhysicalCombineKey{{LeftColumnID: "group-key", RightColumnID: "right-key"}},
		JoinType:         "LEFT",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "group-key", InputIndex: 0, InputColumnID: "group-key"},
			{OutputColumnID: "right-value", InputIndex: 1, InputColumnID: "right-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "group-key", Name: "group_key", LogicalType: "string", ClickHouseType: "String"},
			{ID: "right-value", Name: "right_value", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		},
	}
	sealed := workspaceArtifact("grouped", "loom_private_grouped", ir.ClickHouseArtifactScopeWhole)
	sealed.Artifact.Columns[1] = ir.ResolvedClickHouseColumn{ID: "group-key", Name: "group_key", LogicalType: "string", ClickHouseType: "String"}
	published := resolvedInput("published-table", "published-revision", "published-output", "published_table", []ir.ResolvedClickHouseColumn{
		{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
		{ID: "right-key", Name: "right_key", LogicalType: "string", ClickHouseType: "String"},
		{ID: "right-value", Name: "right_value", LogicalType: "string", ClickHouseType: "String"},
		{ID: "auth-path", Name: "auth_resource_path", ClickHouseType: "Nullable(String)"},
	})
	published.Unrestricted = false
	published.AuthResourcePaths = []string{"/programs/p1"}
	published.ScopeMode = ir.ClickHouseArtifactScopeRows

	rendered, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{sealed}, []ir.ResolvedClickHouseTable{published}, "project-a", "generation-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"__loom_left.`auth_resource_path` = __loom_right.`auth_resource_path`", "__loom_left.`auth_resource_path` IN ?"} {
		if strings.Contains(rendered.Query, forbidden) {
			t.Fatalf("whole-scope result was treated as a row path (%q): %s", forbidden, rendered.Query)
		}
	}
	for _, required := range []string{"CAST(NULL, 'Nullable(String)') AS `auth_resource_path`", "__loom_right.`auth_resource_path` IN ?", "LEFT JOIN"} {
		if !strings.Contains(rendered.Query, required) {
			t.Fatalf("mixed whole-scope query lacks %q: %s", required, rendered.Query)
		}
	}
	if !reflect.DeepEqual(rendered.Args, []any{[]string{"/programs/p1"}}) {
		t.Fatalf("mixed scope bind args = %#v", rendered.Args)
	}
}

func TestRenderWorkspaceKeyJoinKeepsRowScopeFilterOnCorrectOperand(t *testing.T) {
	for _, tc := range []struct {
		name          string
		wholeOnLeft   bool
		wantPredicate string
	}{
		{name: "whole left, row right", wholeOnLeft: true, wantPredicate: "__loom_right.`auth_resource_path` IN ?"},
		{name: "row left, whole right", wholeOnLeft: false, wantPredicate: "__loom_left.`auth_resource_path` IN ?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := workspaceKeyJoinPlan()
			whole := workspaceArtifact("grouped", "loom_private_grouped", ir.ClickHouseArtifactScopeWhole)
			whole.Artifact.Columns[1] = ir.ResolvedClickHouseColumn{ID: "group-key", Name: "group_key", LogicalType: "string", ClickHouseType: "String"}
			rows := resolvedInput("published-table", "published-revision", "published-output", "published_table", []ir.ResolvedClickHouseColumn{
				{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
				{ID: "right-key", Name: "right_key", LogicalType: "string", ClickHouseType: "String"},
				{ID: "right-value", Name: "right_value", LogicalType: "string", ClickHouseType: "String"},
				{ID: "auth-path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			})
			rows.Unrestricted = false
			rows.AuthResourcePaths = []string{"/programs/p1"}
			rows.ScopeMode = ir.ClickHouseArtifactScopeRows
			var private []WorkspaceArtifact
			var published []ir.ResolvedClickHouseTable
			if tc.wholeOnLeft {
				private = []WorkspaceArtifact{whole}
				published = []ir.ResolvedClickHouseTable{rows}
			} else {
				private = []WorkspaceArtifact{whole}
				published = []ir.ResolvedClickHouseTable{rows}
				plan.Inputs[0] = ir.PhysicalCombineInputRef{TableID: "published-table", RevisionID: "published-revision", OutputID: "published-output"}
				plan.Inputs[1] = ir.PhysicalCombineInputRef{WorkspaceOutputID: "grouped"}
				plan.Keys = []ir.PhysicalCombineKey{{LeftColumnID: "right-key", RightColumnID: "group-key"}}
				plan.Projections[0].InputIndex = 1
				plan.Projections[0].InputColumnID = "group-key"
				plan.Projections[1].InputIndex = 0
				plan.Projections[1].InputColumnID = "right-value"
				plan.Outputs[0].Nullable = true
				plan.Outputs[0].ClickHouseType = "Nullable(String)"
				plan.Outputs[1].Nullable = false
				plan.Outputs[1].ClickHouseType = "String"
			}
			rendered, err := RenderWorkspaceClickHouseCombine(plan, private, published, "project-a", "generation-a", 0)
			if err != nil {
				t.Fatalf("RenderWorkspaceClickHouseCombine: %v", err)
			}
			if !strings.Contains(rendered.Query, tc.wantPredicate) {
				t.Fatalf("row-scoped input predicate is not bound on its SQL operand: %s", rendered.Query)
			}
			if strings.Count(rendered.Query, "auth_resource_path` IN ?") != 1 || !reflect.DeepEqual(rendered.Args, []any{[]string{"/programs/p1"}}) {
				t.Fatalf("whole/row scope filtering emitted wrong placeholders: args=%#v query=%s", rendered.Args, rendered.Query)
			}
		})
	}
}

func TestRenderWorkspaceAppendEmitsTypedNullForMissingNullableInput(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{WorkspaceOutputID: "measured"},
			{WorkspaceOutputID: "unmeasured"},
		},
		Projections: []ir.PhysicalCombineProjection{{OutputColumnID: "age", InputIndex: 0, InputColumnID: "measured-age"}},
		Outputs: []ir.PhysicalCombineOutputColumn{{
			ID: "age", Name: "age", LogicalType: "integer", ClickHouseType: "Nullable(Int64)", Nullable: true,
		}},
	}
	measured := workspaceArtifact("measured", "loom_private_measured", ir.ClickHouseArtifactScopeWhole)
	measured.Artifact.Columns[1] = ir.ResolvedClickHouseColumn{ID: "measured-age", Name: "age_years", LogicalType: "integer", ClickHouseType: "Int64"}
	unmeasured := workspaceArtifact("unmeasured", "loom_private_unmeasured", ir.ClickHouseArtifactScopeRows)

	rendered, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{measured, unmeasured}, nil, "project-a", "generation-a", 0)
	if err != nil {
		t.Fatalf("RenderWorkspaceClickHouseCombine: %v", err)
	}
	if !strings.Contains(rendered.Query, "CAST(NULL, 'Nullable(Int64)') AS `age`") {
		t.Fatalf("workspace APPEND did not emit a typed null for the absent input: %s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "__loom_input_0.`age_years` AS `age`") {
		t.Fatalf("workspace APPEND lost the mapped source value: %s", rendered.Query)
	}
}

func TestRenderWorkspaceCombineRejectsStaleArtifactOutputAndScopeEvidence(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind:   ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{{WorkspaceOutputID: "first"}, {WorkspaceOutputID: "second"}},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	first, second := workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeRows), workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	second.ExpectedIdentity.OutputID = "wrong-output"
	if _, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{first, second}, nil, "project-a", "generation-a", 0); err == nil || !strings.Contains(err.Error(), "exact output ID") {
		t.Fatalf("renderer did not reject the stale output identity specifically: %v", err)
	}

	second = workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	whole := workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeWhole)
	whole.Artifact.Identity.ScopeEvidenceDigest = scopeTestDigest("different evidence")
	if _, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{whole, second}, nil, "project-a", "generation-a", 0); err == nil || !strings.Contains(err.Error(), "scope evidence differs") {
		t.Fatalf("renderer did not reject mismatched whole-scope evidence specifically: %v", err)
	}
	second = workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	whole = workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeWhole)
	whole.ExpectedIdentity.ScopeEvidenceDigest = "not-a-sha256-digest"
	whole.Artifact.Identity.ScopeEvidenceDigest = "not-a-sha256-digest"
	if _, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{whole, second}, nil, "project-a", "generation-a", 0); err == nil || !strings.Contains(err.Error(), "valid compiler evidence digest") {
		t.Fatalf("renderer did not reject malformed whole-scope evidence specifically: %v", err)
	}
	first = workspaceArtifact("first", "loom_private_first", ir.ClickHouseArtifactScopeRows)
	second = workspaceArtifact("second", "loom_private_second", ir.ClickHouseArtifactScopeRows)
	second.ExpectedIdentity.AuthResourcePaths = []string{"/programs/p2"}
	second.Artifact.Identity.AuthResourcePaths = []string{"/programs/p2"}
	if _, err := RenderWorkspaceClickHouseCombine(plan, []WorkspaceArtifact{first, second}, nil, "project-a", "generation-a", 0); err == nil {
		t.Fatal("renderer accepted same-bundle artifacts from different authorization scopes")
	}
}

func workspaceArtifact(outputID, physicalTable, scopeMode string) WorkspaceArtifact {
	identity := ir.ClickHouseArtifactIdentity{
		ExecutionID: "exec-1", OutputID: outputID, StageID: "source_projection", Project: "project-a",
		DatasetGeneration: "generation-a", RecipeDigest: "recipe-digest", PlanDigest: "plan-" + outputID,
		SchemaDigest: "schema-" + outputID, ScopeDigest: scopeTestDigest("scope-exact"), AuthScopeMode: "restricted",
		AuthResourcePaths: []string{"/programs/p1"}, ScopeMode: scopeMode,
	}
	if scopeMode == ir.ClickHouseArtifactScopeWhole {
		identity.ScopeEvidenceDigest = scopeTestDigest("proof-" + outputID)
	}
	columns := []ir.ResolvedClickHouseColumn{
		{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
		{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"},
		{ID: "loom:auth_resource_path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		{ID: "loom:project_id", Name: "project_id", LogicalType: "string", ClickHouseType: "String"},
	}
	artifact := ir.ResolvedClickHousePrivateArtifact{ArtifactID: "artifact-" + outputID, Identity: identity, PhysicalTable: physicalTable, Columns: columns}
	return WorkspaceArtifact{OutputID: outputID, ExpectedIdentity: identity, Artifact: artifact}
}

func workspaceKeyJoinPlan() ir.PhysicalClickHouseCombine {
	return ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{WorkspaceOutputID: "grouped"},
			{TableID: "published-table", RevisionID: "published-revision", OutputID: "published-output"},
		},
		Keys:             []ir.PhysicalCombineKey{{LeftColumnID: "group-key", RightColumnID: "right-key"}},
		JoinType:         "LEFT",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "group-key", InputIndex: 0, InputColumnID: "group-key"},
			{OutputColumnID: "right-value", InputIndex: 1, InputColumnID: "right-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "group-key", Name: "group_key", LogicalType: "string", ClickHouseType: "String"},
			{ID: "right-value", Name: "right_value", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		},
	}
}

func scopeTestDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
