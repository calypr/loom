package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
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

	rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
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

	rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
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

func TestRenderMembershipUsesOrdinaryEqualityForNullableCompositeKeys(t *testing.T) {
	for _, mode := range []string{"INCLUDE", "EXCLUDE"} {
		t.Run(mode, func(t *testing.T) {
			plan := ir.PhysicalClickHouseCombine{
				Kind: ir.PhysicalCombineMembership,
				Inputs: []ir.PhysicalCombineInputRef{
					{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
					{TableID: "right-table", RevisionID: "execution-right", OutputID: "members"},
				},
				Keys: []ir.PhysicalCombineKey{
					{LeftColumnID: "person", RightColumnID: "member"},
					{LeftColumnID: "period", RightColumnID: "member-period"},
				},
				MembershipMode: mode,
				Projections: []ir.PhysicalCombineProjection{
					{OutputColumnID: "person", InputIndex: 0, InputColumnID: "person"},
					{OutputColumnID: "period", InputIndex: 0, InputColumnID: "period"},
				},
				Outputs: []ir.PhysicalCombineOutputColumn{
					{ID: "person", Name: "person_id", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
					{ID: "period", Name: "period", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
				},
			}
			inputs := []ir.ResolvedClickHouseTable{
				resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
					{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
					{ID: "person", Name: "person", ClickHouseType: "Nullable(String)", Nullable: true},
					{ID: "period", Name: "period", ClickHouseType: "Nullable(String)", Nullable: true},
				}),
				resolvedInput("right-table", "execution-right", "members", "right_table", []ir.ResolvedClickHouseColumn{
					{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
					{ID: "member", Name: "member", ClickHouseType: "Nullable(String)", Nullable: true},
					{ID: "member-period", Name: "member_period", ClickHouseType: "Nullable(String)", Nullable: true},
				}),
			}

			rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
			if err != nil {
				t.Fatalf("RenderCombine() rejected nullable scalar keys: %v", err)
			}
			for _, expected := range []string{
				"SELECT DISTINCT",
				"__loom_left.`person` = __loom_members.`__loom_key_0`",
				"__loom_left.`period` = __loom_members.`__loom_key_1`",
				"__loom_members.`__loom_match` " + map[string]string{"INCLUDE": "IS NOT NULL", "EXCLUDE": "IS NULL"}[mode],
				"SETTINGS join_use_nulls = 1",
			} {
				if !strings.Contains(rendered.Query, expected) {
					t.Errorf("nullable membership query does not contain %q: %s", expected, rendered.Query)
				}
			}
			if strings.Contains(rendered.Query, "isNotDistinctFrom") || strings.Contains(rendered.Query, "isNotNull(__loom_left") {
				t.Fatalf("nullable membership query changed ordinary NULL-never-matches semantics: %s", rendered.Query)
			}
		})
	}
}

func TestRenderMembershipStillRejectsUnsupportedNullableKeyTypes(t *testing.T) {
	for _, rightType := range []string{"Nullable(Int64)", "Nullable(Array(String))"} {
		t.Run(rightType, func(t *testing.T) {
			plan := ir.PhysicalClickHouseCombine{
				Kind: ir.PhysicalCombineMembership,
				Inputs: []ir.PhysicalCombineInputRef{
					{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
					{TableID: "right-table", RevisionID: "execution-right", OutputID: "members"},
				},
				Keys:           []ir.PhysicalCombineKey{{LeftColumnID: "person", RightColumnID: "member"}},
				MembershipMode: "INCLUDE",
				Projections:    []ir.PhysicalCombineProjection{{OutputColumnID: "person", InputIndex: 0, InputColumnID: "person"}},
				Outputs:        []ir.PhysicalCombineOutputColumn{{ID: "person", Name: "person_id", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true}},
			}
			inputs := []ir.ResolvedClickHouseTable{
				resolvedInput("left-table", "execution-left", "left", "left_table", []ir.ResolvedClickHouseColumn{
					{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
					{ID: "person", Name: "person", ClickHouseType: "Nullable(String)", Nullable: true},
				}),
				resolvedInput("right-table", "execution-right", "members", "right_table", []ir.ResolvedClickHouseColumn{
					{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
					{ID: "member", Name: "member", ClickHouseType: rightType, Nullable: true},
				}),
			}
			if _, err := RenderCombineWithLimit(plan, inputs, "project-a", 0); err == nil || !strings.Contains(err.Error(), "same supported scalar base type") {
				t.Fatalf("membership accepted unsupported nullable key type %q: %v", rightType, err)
			}
		})
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
	rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
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

	rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
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
	if _, err := RenderCombineWithLimit(plan, inputs, "project-a", 0); err == nil || !strings.Contains(err.Error(), "incompatible source type") {
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
	rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
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
	if _, err := RenderCombineWithLimit(plan, inputs, "project-a", 0); err == nil || !strings.Contains(err.Error(), "same authorization scope") {
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
	if _, err := RenderCombineWithLimit(plan, inputs, "project-a", 0); err == nil || !strings.Contains(err.Error(), "exact table/revision/output") {
		t.Fatalf("wrong exact revision error = %v", err)
	}
	inputs[0].RevisionID = "execution-left"
	inputs[1].Columns[1].ID = "legacy-no-id"
	if _, err := RenderCombineWithLimit(plan, inputs, "project-a", 0); err == nil || !strings.Contains(err.Error(), "missing exact input column") {
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
	if _, err := RenderCombineWithLimit(combine, []ir.ResolvedClickHouseTable{right}, "project-a", 0); err == nil || !strings.Contains(err.Error(), "private stage outside a composite plan") {
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

func TestRenderNullableCompositeMembershipClickHouseSemantics(t *testing.T) {
	client := newNullableMembershipClickHouse(t)
	defer func() {
		if err := client.Close(); err != nil {
			t.Logf("close ClickHouse test client: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatalf("ensure ClickHouse test database: %v", err)
	}

	suffix := uuid.NewString()[:8]
	leftTable := "loom_membership_left_" + suffix
	rightTable := "loom_membership_right_" + suffix
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := client.DropTable(cleanupCtx, leftTable); err != nil {
			t.Logf("drop ClickHouse test table %s: %v", leftTable, err)
		}
		if err := client.DropTable(cleanupCtx, rightTable); err != nil {
			t.Logf("drop ClickHouse test table %s: %v", rightTable, err)
		}
	}()

	columns := []storeclickhouse.Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "k1", Type: "Nullable(String)"},
		{Name: "k2", Type: "Nullable(String)"},
	}
	if err := client.CreateTable(ctx, leftTable, columns); err != nil {
		t.Fatalf("create left table: %v", err)
	}
	if err := client.CreateTable(ctx, rightTable, columns); err != nil {
		t.Fatalf("create right table: %v", err)
	}
	if err := client.InsertRows(ctx, leftTable, columns, []map[string]any{
		{"__loom_row_id": "1", "k1": "alpha", "k2": "one"},
		{"__loom_row_id": "2", "k1": nil, "k2": "two"},
		{"__loom_row_id": "3", "k1": "beta", "k2": nil},
		{"__loom_row_id": "4", "k1": nil, "k2": nil},
		{"__loom_row_id": "5", "k1": "alpha", "k2": "missing"},
		{"__loom_row_id": "6", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "7", "k1": "right-null", "k2": "match"},
		{"__loom_row_id": "8", "k1": "alpha", "k2": "right-null"},
	}); err != nil {
		t.Fatalf("insert left rows: %v", err)
	}
	if err := client.InsertRows(ctx, rightTable, columns, []map[string]any{
		{"__loom_row_id": "a", "k1": "alpha", "k2": "one"},
		{"__loom_row_id": "b", "k1": "alpha", "k2": "one"},
		{"__loom_row_id": "c", "k1": nil, "k2": "two"},
		{"__loom_row_id": "d", "k1": "beta", "k2": nil},
		{"__loom_row_id": "e", "k1": nil, "k2": nil},
		{"__loom_row_id": "f", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "g", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "h", "k1": nil, "k2": "match"},
		{"__loom_row_id": "i", "k1": "alpha", "k2": nil},
	}); err != nil {
		t.Fatalf("insert right rows: %v", err)
	}

	inputColumns := func() []ir.ResolvedClickHouseColumn {
		return []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "k1", Name: "k1", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			{ID: "k2", Name: "k2", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		}
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", leftTable, inputColumns()),
		resolvedInput("right-table", "execution-right", "right", rightTable, inputColumns()),
	}
	for _, scenario := range []struct {
		name        string
		keys        []ir.PhysicalCombineKey
		includeWant []string
		excludeWant []string
	}{
		{
			name:        "composite",
			keys:        []ir.PhysicalCombineKey{{LeftColumnID: "k1", RightColumnID: "k1"}, {LeftColumnID: "k2", RightColumnID: "k2"}},
			includeWant: []string{"1", "6"},
			excludeWant: []string{"2", "3", "4", "5", "7", "8"},
		},
		{
			name:        "single",
			keys:        []ir.PhysicalCombineKey{{LeftColumnID: "k1", RightColumnID: "k1"}},
			includeWant: []string{"1", "3", "5", "6", "8"},
			excludeWant: []string{"2", "4", "7"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, mode := range []string{"INCLUDE", "EXCLUDE"} {
				t.Run(mode, func(t *testing.T) {
					plan := ir.PhysicalClickHouseCombine{
						Kind: ir.PhysicalCombineMembership,
						Inputs: []ir.PhysicalCombineInputRef{
							{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
							{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
						},
						Keys:           scenario.keys,
						MembershipMode: mode,
						Projections: []ir.PhysicalCombineProjection{
							{OutputColumnID: "k1", InputIndex: 0, InputColumnID: "k1"},
							{OutputColumnID: "k2", InputIndex: 0, InputColumnID: "k2"},
						},
						Outputs: []ir.PhysicalCombineOutputColumn{
							{ID: "k1", Name: "k1", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
							{ID: "k2", Name: "k2", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
						},
					}
					rendered, err := RenderCombineWithLimit(plan, inputs, "project-a", 0)
					if err != nil {
						t.Fatalf("render nullable membership: %v", err)
					}
					rows, err := client.QueryRowsArgs(ctx, rendered.Query, rendered.Columns, rendered.Args...)
					if err != nil {
						t.Fatalf("execute rendered membership: %v\nquery: %s", err, rendered.Query)
					}
					got := make([]string, 0, len(rows))
					for _, row := range rows {
						id, ok := row["__loom_row_id"].(string)
						if !ok {
							t.Fatalf("row identity has type %T, want string: %#v", row["__loom_row_id"], row)
						}
						got = append(got, id)
					}
					sort.Strings(got)
					want := scenario.includeWant
					if mode == "EXCLUDE" {
						want = scenario.excludeWant
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s %s result row IDs = %v, want %v", scenario.name, mode, got, want)
					}
					if mode == "INCLUDE" && len(got) != len(want) {
						t.Fatalf("duplicate right keys multiplied left rows: %s", fmt.Sprint(got))
					}
				})
			}
		})
	}
}

type nullableMembershipClickHouse interface {
	EnsureDatabase(context.Context) error
	CreateTable(context.Context, string, []storeclickhouse.Column) error
	InsertRows(context.Context, string, []storeclickhouse.Column, []map[string]any) error
	QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error)
	DropTable(context.Context, string) error
	Close() error
}

var membershipTestDatabasePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newNullableMembershipClickHouse(t *testing.T) nullableMembershipClickHouse {
	t.Helper()
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	if !membershipTestDatabasePattern.MatchString(database) {
		t.Fatalf("ClickHouse test database %q is not a safe identifier", database)
	}
	if container := os.Getenv("LOOM_CLICKHOUSE_CONTAINER"); container != "" {
		return containerMembershipClickHouse{container: container, database: database}
	}
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	client, err := storeclickhouse.New(storeclickhouse.Options{
		URL: url, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open ClickHouse: %v", err)
	}
	return client
}

type containerMembershipClickHouse struct {
	container string
	database  string
}

func (client containerMembershipClickHouse) EnsureDatabase(ctx context.Context) error {
	_, err := client.runWithoutDatabase(ctx, "CREATE DATABASE IF NOT EXISTS `"+client.database+"`", nil, "")
	return err
}

func (client containerMembershipClickHouse) CreateTable(ctx context.Context, table string, columns []storeclickhouse.Column) error {
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = fmt.Sprintf("`%s` %s", column.Name, column.Type)
	}
	query := fmt.Sprintf("CREATE TABLE `%s` (%s) ENGINE = MergeTree ORDER BY (`__loom_row_id`)", table, strings.Join(parts, ", "))
	_, err := client.run(ctx, query, nil, "")
	return err
}

func (client containerMembershipClickHouse) InsertRows(ctx context.Context, table string, _ []storeclickhouse.Column, rows []map[string]any) error {
	var input bytes.Buffer
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode ClickHouse test row: %w", err)
		}
		input.Write(encoded)
		input.WriteByte('\n')
	}
	query := fmt.Sprintf("INSERT INTO `%s` FORMAT JSONEachRow", table)
	_, err := client.run(ctx, query, &input, "")
	return err
}

func (client containerMembershipClickHouse) QueryRowsArgs(ctx context.Context, query string, _ []string, args ...any) ([]map[string]any, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("container ClickHouse test client does not accept query arguments")
	}
	output, err := client.run(ctx, query, nil, "JSONEachRow")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var rows []map[string]any
	for {
		var row map[string]any
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse JSONEachRow: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (client containerMembershipClickHouse) DropTable(ctx context.Context, table string) error {
	_, err := client.run(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table), nil, "")
	return err
}

func (containerMembershipClickHouse) Close() error { return nil }

func (client containerMembershipClickHouse) run(ctx context.Context, query string, input io.Reader, outputFormat string) ([]byte, error) {
	return client.runInDatabase(ctx, client.database, query, input, outputFormat)
}

func (client containerMembershipClickHouse) runWithoutDatabase(ctx context.Context, query string, input io.Reader, outputFormat string) ([]byte, error) {
	return client.runInDatabase(ctx, "", query, input, outputFormat)
}

func (client containerMembershipClickHouse) runInDatabase(ctx context.Context, database, query string, input io.Reader, outputFormat string) ([]byte, error) {
	args := []string{"exec"}
	if input != nil {
		args = append(args, "-i")
	}
	args = append(args, client.container, "clickhouse-client")
	if database != "" {
		args = append(args, "--database", database)
	}
	if outputFormat != "" {
		args = append(args, "--format", outputFormat)
	}
	args = append(args, "--query", query)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = input
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("docker exec ClickHouse query: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
