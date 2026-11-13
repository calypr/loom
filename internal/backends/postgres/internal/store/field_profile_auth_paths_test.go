package postgres

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDistinctAuthResourcePathsSQLGoldenPins(t *testing.T) {
	sql, args, err := distinctAuthResourcePathsSQL(`"it_schema"."fhir_field_profile"`, "p1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `SELECT DISTINCT auth_resource_path COLLATE "C" FROM "it_schema"."fhir_field_profile" WHERE project = $1 AND auth_resource_path <> '' ORDER BY auth_resource_path`
	if sql != want {
		t.Fatalf("golden SQL drift:\n got %q\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{"p1"}) {
		t.Fatalf("golden args drift: %#v", args)
	}
	if strings.Contains(sql, "dataset_generation") {
		t.Fatalf("union render carries a dataset_generation predicate: %q", sql)
	}
	if !strings.Contains(sql, "auth_resource_path <> ''") {
		t.Fatalf("union render left the empty-path door open: %q", sql)
	}
	if !strings.Contains(sql, `COLLATE "C"`) {
		t.Fatalf("union render left the order to the cluster collation: %q", sql)
	}
	for _, fragment := range []string{"ORDER BY resource_type", "GROUP BY"} {
		if strings.Contains(sql, fragment) {
			t.Fatalf("union render carries unexpected fragment %q: %q", fragment, sql)
		}
	}

	for _, blank := range []string{"", "   ", "\t"} {
		sql, args, err := distinctAuthResourcePathsSQL(`"it_schema"."fhir_field_profile"`, blank)
		if err == nil {
			t.Fatalf("blank project %q rendered %q (args %#v); want the typed refusal", blank, sql, args)
		}
		if sql != "" || args != nil {
			t.Fatalf("blank project %q refusal still produced sql=%q args=%#v", blank, sql, args)
		}
		if !errors.Is(err, ErrInvalidFieldProfileScope) {
			t.Fatalf("blank project refusal %v must wrap ErrInvalidFieldProfileScope", err)
		}
	}
}

func TestListDistinctAuthResourcePathsUnionAcrossGenerations(t *testing.T) {
	svc := newMigrationService(t)
	if _, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema); err != nil {
		svc.fail("MigrateSchema", err)
	}
	repo, err := NewFieldProfileRepository(svc.iso, svc.schema)
	if err != nil {
		t.Fatalf("NewFieldProfileRepository: %v", err)
	}

	const project = "pgauth-allgens"
	fixture := []map[string]string{
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/clinical'", "path": "'obs.a1'"},
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/finance'", "path": "'obs.a2'"},
		{"dataset_generation": "'generation-b'", "auth_resource_path": "'/fhir/clinical'", "path": "'obs.b1'"},
		{"dataset_generation": "'generation-b'", "auth_resource_path": "'/fhir/archive'", "path": "'obs.b2'"},
		{"dataset_generation": "''", "auth_resource_path": "'/fhir/legacy'", "path": "'obs.legacy'"},
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/custom'", "resource_type": "'CustomCarePlan'", "path": "'obs.custom'"},
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/research'", "record_type": "'profile_status'", "path": "''"},
		{"dataset_generation": "'generation-a'", "auth_resource_path": "''", "path": "'obs.unscoped'"},
		{"project": "'pgauth-other'", "dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/foreign'", "path": "'obs.foreign'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/alpha'", "path": "'obs.c1'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/Beta'", "path": "'obs.c2'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/_x'", "path": "'obs.c3'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/Ω'", "path": "'obs.c4'"},
	}
	for _, override := range fixture {
		if _, owned := override["project"]; !owned {
			override["project"] = fpDDLText(project)
		}
		fpDDLExec(t, svc, fpDDLProfileStmt(svc, override))
	}

	paths, err := repo.ListDistinctAuthResourcePaths(svc.ctx, project)
	if err != nil {
		svc.fail("ListDistinctAuthResourcePaths", err)
	}
	want := []string{"/fhir/Beta", "/fhir/_x", "/fhir/alpha", "/fhir/archive",
		"/fhir/clinical", "/fhir/custom", "/fhir/finance", "/fhir/legacy", "/fhir/research", "/fhir/Ω"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("union = %v, want byte-ordered distinct %v", paths, want)
	}
	if paths == nil {
		t.Fatal("union returned a nil slice")
	}
	if !slices.IsSortedFunc(paths, func(a, b string) int { return strings.Compare(a, b) }) {
		t.Fatalf("union is not ascending byte order: %v", paths)
	}

	foreign, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "pgauth-other")
	if err != nil {
		svc.fail("foreign project listing", err)
	}
	if !reflect.DeepEqual(foreign, []string{"/fhir/foreign"}) {
		t.Fatalf("foreign project union = %v, want [/fhir/foreign]", foreign)
	}

	empty, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "pgauth-none")
	if err != nil {
		svc.fail("unrecorded project listing", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unrecorded project union = %#v, want a non-nil empty slice", empty)
	}

	if _, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "  "); !errors.Is(err, ErrInvalidFieldProfileScope) {
		t.Fatalf("blank project refusal = %v, want ErrInvalidFieldProfileScope", err)
	}
}
