// Witnesses for ListDistinctAuthResourcePaths, the PostgreSQL shape of the
// Arango reference union existingAuthResourcePathsAllGenerationsAQL
// (defect PG-PG-AUTH-PATH-DISCOVERY-ALLGENS, coordinator ruling: design B).
//
// The union is defined by what it reads, so the witnesses are two-sided:
//   - a golden pin of the rendered statement: one project bind, the
//     empty-path door closed by predicate, and NO dataset_generation
//     predicate (an empty generation is the legacy unscoped namespace,
//     never "any generation" -- a bind there would silently turn the union
//     into the legacy read and re-open the F-B27 zero-path bootstrap-allow
//     posture for every recorded generation);
//   - live-DDL reads through the real repository: multi-generation union,
//     legacy empty-generation rows, non-concrete-type-only rows, marker
//     rows, duplicate paths across generations deduplicated, empty paths
//     and foreign projects excluded, an honest empty for an unrecorded
//     project, and a typed refusal (never an all-projects scan) for a
//     blank project.
package postgres

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fpAuthRows inserts rows for the union fixture: path overrides carry the
// auth_resource_path column, and every non-default tuple differs on
// (dataset_generation, auth_resource_path, resource_type, path) so the
// primary key accepts the whole fixture.
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
	// The order is the contract, so the C collation is pinned explicitly:
	// a render that dropped it would make the result depend on the
	// cluster's default collation (interrogation finding M4: the adapter's
	// re-sort alone left the rendered order unguaranteed and untested).
	if !strings.Contains(sql, `COLLATE "C"`) {
		t.Fatalf("union render left the order to the cluster collation: %q", sql)
	}
	// The golden itself must be predicate-free by generation: the check
	// above would pass on a render that mentions the column in ORDER BY.
	for _, fragment := range []string{"ORDER BY resource_type", "GROUP BY"} {
		if strings.Contains(sql, fragment) {
			t.Fatalf("union render carries unexpected fragment %q: %q", fragment, sql)
		}
	}

	// Blank project is the same typed refusal as the filtered reads, at
	// BOTH blank shapes, and never degrades into an all-projects scan.
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
		// Two generations sharing one path: the union deduplicates it.
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/clinical'", "path": "'obs.a1'"},
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/finance'", "path": "'obs.a2'"},
		{"dataset_generation": "'generation-b'", "auth_resource_path": "'/fhir/clinical'", "path": "'obs.b1'"},
		{"dataset_generation": "'generation-b'", "auth_resource_path": "'/fhir/archive'", "path": "'obs.b2'"},
		// Legacy unscoped namespace: a recorded path must survive even
		// though no dataset-series manifest could enumerate "".
		{"dataset_generation": "''", "auth_resource_path": "'/fhir/legacy'", "path": "'obs.legacy'"},
		// Non-concrete type: the reference union has no resource_type
		// filter, so a path witnessed only on a custom type still lists.
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/custom'", "resource_type": "'CustomCarePlan'", "path": "'obs.custom'"},
		// Truncated-empty marker row: its scope's path is a witness.
		{"dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/research'", "record_type": "'profile_status'", "path": "''"},
		// Empty auth path (the unscoped namespace's own rows): excluded by
		// predicate. Distinct path so it is its own tuple.
		{"dataset_generation": "'generation-a'", "auth_resource_path": "''", "path": "'obs.unscoped'"},
		// Foreign project: excluded by the project bind.
		{"project": "'pgauth-other'", "dataset_generation": "'generation-a'", "auth_resource_path": "'/fhir/foreign'", "path": "'obs.foreign'"},
		// Order fixtures: these four paths sort differently under any
		// case-insensitive/ICU-style collation than under byte order
		// ('B' 0x42 < '_' 0x5F < 'a' 0x61 < 0xCE-prefixed 'Ω'), so this
		// witness is non-vacuous for the COLLATE "C" contract.
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/alpha'", "path": "'obs.c1'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/Beta'", "path": "'obs.c2'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/_x'", "path": "'obs.c3'"},
		{"dataset_generation": "'generation-c'", "auth_resource_path": "'/fhir/Ω'", "path": "'obs.c4'"},
	}
	for _, override := range fixture {
		// The DDL fixture defaults carry fpDDLProject; this witness owns
		// its project identity explicitly (the foreign row overrides it).
		if _, owned := override["project"]; !owned {
			override["project"] = fpDDLText(project)
		}
		fpDDLExec(t, svc, fpDDLProfileStmt(svc, override))
	}

	paths, err := repo.ListDistinctAuthResourcePaths(svc.ctx, project)
	if err != nil {
		svc.fail("ListDistinctAuthResourcePaths", err)
	}
	// Hand-computed ascending BYTE order (0x42 'B' < 0x5F '_' < 0x61 'a',
	// "alpha" < "archive", 0xCE 'Ω' last): a case-insensitive or ICU-style
	// cluster collation renders a different order and fails here.
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

	// The foreign project sees only its own recorded path -- the project
	// bind is the whole isolation story on both directions.
	foreign, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "pgauth-other")
	if err != nil {
		svc.fail("foreign project listing", err)
	}
	if !reflect.DeepEqual(foreign, []string{"/fhir/foreign"}) {
		t.Fatalf("foreign project union = %v, want [/fhir/foreign]", foreign)
	}

	// An unrecorded project returns an honest empty, not a refusal and
	// not nil: this empty value is what project-level write authorization
	// (F-B27) reads as "no recorded paths" for its bootstrap branch.
	empty, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "pgauth-none")
	if err != nil {
		svc.fail("unrecorded project listing", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unrecorded project union = %#v, want a non-nil empty slice", empty)
	}

	// Blank project: typed refusal through the repository, never an
	// all-projects scan.
	if _, err := repo.ListDistinctAuthResourcePaths(svc.ctx, "  "); !errors.Is(err, ErrInvalidFieldProfileScope) {
		t.Fatalf("blank project refusal = %v, want ErrInvalidFieldProfileScope", err)
	}
}
