package clickhouse

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRenderKeyJoinPinsExactInputsAndPreservesLeftRows(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "patients"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "scores"},
		},
		Keys:             []ir.PhysicalCombineKey{{LeftColumnID: "patient-id", RightColumnID: "subject-id"}},
		JoinType:         "LEFT",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "patient-id", InputIndex: 0, InputColumnID: "patient-id"},
			{OutputColumnID: "score", InputIndex: 1, InputColumnID: "score"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "patient-id", Name: "patient_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: "score", Name: "score", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "patients", "left_materialized", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "patient-id", Name: "patient_key", ClickHouseType: "String"},
			{ID: "auth-path", Name: "auth_resource_path", ClickHouseType: "String"},
		}),
		resolvedInput("right-table", "execution-right", "scores", "right_materialized", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "subject-id", Name: "subject_key", ClickHouseType: "String"},
			{ID: "score", Name: "score_value", ClickHouseType: "String"},
			{ID: "auth-path", Name: "auth_resource_path", ClickHouseType: "String"},
		}),
	}
	inputs[0].AuthResourcePaths = []string{"/programs/p1"}
	inputs[1].AuthResourcePaths = []string{"/programs/p1"}
	inputs[0].Unrestricted = false
	inputs[1].Unrestricted = false
	inputs[0].ScopeDigest = "scope-left"
	inputs[1].ScopeDigest = "scope-right"

	rendered, err := RenderCombine(plan, inputs, "project-a")
	if err != nil {
		t.Fatalf("RenderCombine() error = %v", err)
	}
	for _, expected := range []string{
		"ALL LEFT JOIN",
		"__loom_left.`auth_resource_path` = __loom_right.`auth_resource_path`",
		"__loom_right.`auth_resource_path` IN ?",
		"WHERE __loom_left.`auth_resource_path` IN ?",
		"SETTINGS join_use_nulls = 1",
	} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("query does not contain %q: %s", expected, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "WHERE `__loom_right`") {
		t.Fatalf("right scope filter moved into WHERE and would drop unmatched left rows: %s", rendered.Query)
	}
	wantArgs := []any{[]string{"/programs/p1"}, []string{"/programs/p1"}}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("query args = %#v, want right ON args then left WHERE args %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.Columns, []string{"__loom_row_id", "patient_id", "score", "auth_resource_path"}) {
		t.Fatalf("output query columns = %#v", rendered.Columns)
	}
}

func TestRenderKeyJoinUsesOrdinaryEqualityForNullableScalarKeys(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "patients"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "reports"},
		},
		Keys:             []ir.PhysicalCombineKey{{LeftColumnID: "patient-id", RightColumnID: "subject-id"}},
		JoinType:         "LEFT",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "patient-id", InputIndex: 0, InputColumnID: "patient-id"},
			{OutputColumnID: "report-status", InputIndex: 1, InputColumnID: "status"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "patient-id", Name: "patient_id", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			{ID: "report-status", Name: "report_status", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "patients", "left_materialized", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "patient-id", Name: "patient_id", ClickHouseType: "Nullable(String)", Nullable: true},
		}),
		resolvedInput("right-table", "execution-right", "reports", "right_materialized", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "subject-id", Name: "subject_id", ClickHouseType: "String"},
			{ID: "status", Name: "status", ClickHouseType: "String"},
		}),
	}

	rendered, err := RenderCombine(plan, inputs, "project-a")
	if err != nil {
		t.Fatalf("RenderCombine() rejected a nullable scalar key: %v", err)
	}
	if !strings.Contains(rendered.Query, "ALL LEFT JOIN") {
		t.Fatalf("nullable-key query lost LEFT join behavior: %s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "__loom_left.`patient_id` = __loom_right.`subject_id`") {
		t.Fatalf("nullable key comparison must use ordinary equality so NULL never matches NULL: %s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "isNotDistinctFrom") || strings.Contains(rendered.Query, "isNotNull(__loom_left.`patient_id`)") {
		t.Fatalf("nullable key query introduced null-safe equality or filtered left rows: %s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "SETTINGS join_use_nulls = 1") {
		t.Fatalf("LEFT join does not null-extend unmatched right projections: %s", rendered.Query)
	}
}

func TestRenderMembershipRejectsNullableKeys(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineMembership,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "members"},
		},
		Keys:           []ir.PhysicalCombineKey{{LeftColumnID: "person", RightColumnID: "member"}},
		MembershipMode: "INCLUDE",
		Projections:    []ir.PhysicalCombineProjection{{OutputColumnID: "person", InputIndex: 0, InputColumnID: "person"}},
		Outputs:        []ir.PhysicalCombineOutputColumn{{ID: "person", Name: "person_id", LogicalType: "string", ClickHouseType: "String"}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "person", Name: "person", ClickHouseType: "String"},
		}),
		resolvedInput("right-table", "execution-right", "members", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "member", Name: "member", ClickHouseType: "Nullable(String)", Nullable: true},
		}),
	}
	if _, err := RenderCombine(plan, inputs, "project-a"); err == nil || !strings.Contains(err.Error(), "supported scalar base type") {
		t.Fatalf("membership widened to nullable keys unexpectedly: %v", err)
	}
}

func TestRenderAppendMapsIndependentStableColumnIDs(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "person", InputIndex: 0, InputColumnID: "left-person-id"},
			{OutputColumnID: "person", InputIndex: 1, InputColumnID: "right-subject-id"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "person", Name: "person_id", LogicalType: "string", ClickHouseType: "String"}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "left-person-id", Name: "person", ClickHouseType: "String"},
		}),
		resolvedInput("right-table", "execution-right", "right", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "right-subject-id", Name: "subject", ClickHouseType: "String"},
		}),
	}
	rendered, err := RenderCombine(plan, inputs, "project-a")
	if err != nil {
		t.Fatalf("RenderCombine() error = %v", err)
	}
	if strings.Count(rendered.Query, " UNION ALL ") != 1 || !strings.Contains(rendered.Query, "`person` AS `person_id`") || !strings.Contains(rendered.Query, "`subject` AS `person_id`") {
		t.Fatalf("append query does not align independent source IDs: %s", rendered.Query)
	}
	if len(rendered.Args) != 0 {
		t.Fatalf("unrestricted append args = %#v", rendered.Args)
	}
}

func TestRenderAppendEmitsTypedNullForMissingNullableInput(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{{OutputColumnID: "age", InputIndex: 0, InputColumnID: "left-age"}},
		Outputs: []ir.PhysicalCombineOutputColumn{{
			ID: "age", Name: "age", LogicalType: "integer", ClickHouseType: "Nullable(Int64)", Nullable: true,
		}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: "left-age", Name: "age_years", LogicalType: "integer", ClickHouseType: "Int64"},
		}),
		resolvedInput("right-table", "execution-right", "right", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
		}),
	}

	rendered, err := RenderCombine(plan, inputs, "project-a")
	if err != nil {
		t.Fatalf("RenderCombine() error = %v", err)
	}
	if !strings.Contains(rendered.Query, "CAST(NULL, 'Nullable(Int64)') AS `age`") {
		t.Fatalf("append query does not emit a typed null for the missing input: %s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "__loom_input_0.`age_years` AS `age`") {
		t.Fatalf("append query lost the mapped source field: %s", rendered.Query)
	}
}

func TestRenderAppendRejectsMismatchedBasePhysicalTypes(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "age", InputIndex: 0, InputColumnID: "left-age"},
			{OutputColumnID: "age", InputIndex: 1, InputColumnID: "right-age"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{
			ID: "age", Name: "age", LogicalType: "integer", ClickHouseType: "Nullable(Int64)", Nullable: true,
		}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: "left-age", Name: "age", LogicalType: "integer", ClickHouseType: "Int64"},
		}),
		resolvedInput("right-table", "execution-right", "right", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: "right-age", Name: "age", LogicalType: "integer", ClickHouseType: "Int32"},
		}),
	}
	if _, err := RenderCombine(plan, inputs, "project-a"); err == nil || !strings.Contains(err.Error(), "incompatible source type") {
		t.Fatalf("mismatched base physical type error = %v", err)
	}
}

func TestRenderMembershipUsesDistinctRightKeysAndFailsClosedOnScopeMismatch(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineMembership,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "members"},
		},
		Keys:           []ir.PhysicalCombineKey{{LeftColumnID: "person", RightColumnID: "member"}},
		MembershipMode: "INCLUDE",
		Projections:    []ir.PhysicalCombineProjection{{OutputColumnID: "person", InputIndex: 0, InputColumnID: "person"}},
		Outputs:        []ir.PhysicalCombineOutputColumn{{ID: "person", Name: "person_id", LogicalType: "string", ClickHouseType: "String"}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "person", Name: "person", ClickHouseType: "String"},
			{ID: "auth-path", Name: "auth_resource_path", ClickHouseType: "String"},
		}),
		resolvedInput("right-table", "execution-right", "members", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "member", Name: "member", ClickHouseType: "String"},
			{ID: "auth-path", Name: "auth_resource_path", ClickHouseType: "String"},
		}),
	}
	for i := range inputs {
		inputs[i].Unrestricted = false
		inputs[i].AuthResourcePaths = []string{"/programs/p1"}
	}
	rendered, err := RenderCombine(plan, inputs, "project-a")
	if err != nil {
		t.Fatalf("RenderCombine() error = %v", err)
	}
	for _, expected := range []string{"SELECT DISTINCT", "LEFT ANY JOIN", "__loom_member_source.`auth_resource_path` IN ?", "WHERE __loom_left.`auth_resource_path` IN ?", "__loom_members.`__loom_match` IS NOT NULL"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("membership query does not contain %q: %s", expected, rendered.Query)
		}
	}
	if !reflect.DeepEqual(rendered.Args, []any{[]string{"/programs/p1"}, []string{"/programs/p1"}}) {
		t.Fatalf("membership args = %#v", rendered.Args)
	}
	inputs[1].AuthResourcePaths = []string{"/programs/p2"}
	if _, err := RenderCombine(plan, inputs, "project-a"); err == nil || !strings.Contains(err.Error(), "same authorization scope") {
		t.Fatalf("mismatched authorization scopes error = %v", err)
	}
}

func TestRenderCombineRejectsWrongExactRevisionAndMissingStableColumnID(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "value-left"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "value-right"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"}, {ID: "value-left", Name: "value", ClickHouseType: "String"},
		}),
		resolvedInput("right-table", "execution-right", "right", "right_table", []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"}, {ID: "value-right", Name: "value", ClickHouseType: "String"},
		}),
	}
	inputs[0].RevisionID = "different-execution"
	if _, err := RenderCombine(plan, inputs, "project-a"); err == nil || !strings.Contains(err.Error(), "exact table/revision/output") {
		t.Fatalf("wrong exact revision error = %v", err)
	}
	inputs[0].RevisionID = "execution-left"
	inputs[1].Columns[1].ID = "legacy-no-id"
	if _, err := RenderCombine(plan, inputs, "project-a"); err == nil || !strings.Contains(err.Error(), "missing exact input column") {
		t.Fatalf("missing stable column ID error = %v", err)
	}
}

func TestRenderCompositeCombineConsumesExactPrivatePrefixArtifact(t *testing.T) {
	combine := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{PrivateStageID: "derive_status"},
			{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "status", InputIndex: 0, InputColumnID: "status-id"},
			{OutputColumnID: "status", InputIndex: 1, InputColumnID: "right-status-id"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "status", Name: "status", LogicalType: "string", ClickHouseType: "String"}},
	}
	prefix := ir.PhysicalClickHousePrefix{
		StageID: "derive_status", AuthScopeMode: "restricted", AuthResourcePaths: []string{"/programs/p1"},
		IncludeAuthResourcePath: true, AuthResourcePathBindKey: "construction_private_auth_resource_path",
	}
	expected := ir.ClickHouseArtifactIdentity{
		ExecutionID: "execution-1", OutputID: "patients", StageID: "derive_status", Project: "project-a", DatasetGeneration: "generation-a",
		RecipeDigest: "recipe-digest", PlanDigest: "prefix-plan-digest", SchemaDigest: "prefix-schema-digest",
		ScopeDigest: "scope-a", AuthScopeMode: "restricted", AuthResourcePaths: []string{"/programs/p1"},
	}
	artifact := ir.ResolvedClickHousePrivateArtifact{
		ArtifactID: "artifact-1", PhysicalTable: "loom_private_stage_1", Identity: expected,
		Columns: []ir.ResolvedClickHouseColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: "status-id", Name: "status", LogicalType: "string", ClickHouseType: "String"},
			{ID: "loom:auth_resource_path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			{ID: "loom:project_id", Name: "project_id", LogicalType: "string", ClickHouseType: "String"},
		},
	}
	right := resolvedInput("right-table", "execution-right", "right", "right_table", []ir.ResolvedClickHouseColumn{
		{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
		{ID: "right-status-id", Name: "right_status", LogicalType: "string", ClickHouseType: "String"},
		{ID: "loom:auth_resource_path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "String"},
	})
	right.Unrestricted = false
	right.AuthResourcePaths = []string{"/programs/p1"}
	right.ScopeDigest = expected.ScopeDigest

	rendered, err := RenderCompositeClickHouseCombine(combine, prefix, []ir.ResolvedClickHouseTable{right}, artifact, expected, "project-a", 0)
	if err != nil {
		t.Fatalf("RenderCompositeClickHouseCombine() error = %v", err)
	}
	for _, want := range []string{"loom_private_stage_1", "UNION ALL", "__loom_input_0.`auth_resource_path` IN ?", "__loom_input_1.`auth_resource_path` IN ?"} {
		if !strings.Contains(rendered.Query, want) {
			t.Errorf("composite SQL is missing %q: %s", want, rendered.Query)
		}
	}
	if !reflect.DeepEqual(rendered.Args, []any{[]string{"/programs/p1"}, []string{"/programs/p1"}}) {
		t.Fatalf("composite SQL args = %#v", rendered.Args)
	}

	badArtifact := artifact
	badArtifact.Identity.PlanDigest = "different-prefix"
	if _, err := RenderCompositeClickHouseCombine(combine, prefix, []ir.ResolvedClickHouseTable{right}, badArtifact, expected, "project-a", 0); err == nil || !strings.Contains(err.Error(), "exact compiled prefix identity") {
		t.Fatalf("mismatched artifact identity error = %v", err)
	}
	badArtifact = artifact
	badArtifact.Identity.ScopeDigest = "different-scope"
	if _, err := RenderCompositeClickHouseCombine(combine, prefix, []ir.ResolvedClickHouseTable{right}, badArtifact, expected, "project-a", 0); err == nil || !strings.Contains(err.Error(), "exact compiled prefix identity") {
		t.Fatalf("mismatched artifact scope identity error = %v", err)
	}
	badArtifact = artifact
	badArtifact.Identity.OutputID = "observations"
	if _, err := RenderCompositeClickHouseCombine(combine, prefix, []ir.ResolvedClickHouseTable{right}, badArtifact, expected, "project-a", 0); err == nil || !strings.Contains(err.Error(), "exact compiled prefix identity") {
		t.Fatalf("renderer accepted a same-stage artifact from a different output: %v", err)
	}
	if _, err := RenderCombine(combine, []ir.ResolvedClickHouseTable{right}, "project-a"); err == nil || !strings.Contains(err.Error(), "private stage outside a composite plan") {
		t.Fatalf("standalone Combine accepted private input: %v", err)
	}
}

func resolvedInput(tableID, revisionID, outputID, physicalTable string, columns []ir.ResolvedClickHouseColumn) ir.ResolvedClickHouseTable {
	for index := range columns {
		if columns[index].LogicalType == "" && columns[index].Name != "__loom_row_id" && columns[index].Name != "auth_resource_path" && columns[index].Name != "project_id" {
			columns[index].LogicalType = "string"
		}
	}
	return ir.ResolvedClickHouseTable{
		TableID: tableID, RevisionID: revisionID, OutputID: outputID,
		Project: "project-a", DatasetGeneration: "generation-a", ReceiptID: "receipt-" + revisionID,
		SchemaDigest: "schema-" + revisionID, ScopeDigest: "scope-a", PhysicalTable: physicalTable,
		Unrestricted: true, Columns: columns,
	}
}
