// Golden-render pins for fieldProfileFilterSQL (review e22c4825,
// coordinator bindings 1 and 2 on fix r4543172882).
//
// The AllResourceTypes field is an explicit opt-in, so its ZERO value must
// keep the declared-list contract byte-identical: the golden strings below
// are copied byte-for-byte from fieldProfileFilterSQL at commit 10d2ce6d
// (git show 10d2ce6d:internal/backends/postgres/internal/store/field_profile.go),
// covering the filter shapes the pre-fix production constructors build
// (the field-enrichment reader in internal/catalog/field_profile.go and
// DiscoverFields in internal/catalog/field_discovery.go, restricted and
// unrestricted doors). Each declared-list case renders twice -- once with
// AllResourceTypes left at its zero value and once with the zero assigned
// explicitly -- and requires byte-identical output: the zero value is
// behaviorally inert, not merely "probably" so.
//
// The all-types branch is pinned to the reference scope exactly: project +
// dataset_generation and NOTHING else (auth paths shift to $3 in
// restricted mode), with an explicit no-residual-resource_type-fragment
// assertion.

package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func goldenBoolPtr(v bool) *bool { return &v }

func TestFieldProfileFilterSQLGoldenPins(t *testing.T) {
	types := []string{"Patient", "Observation"}
	paths := []string{"/fhir/clinical", "/fhir/finance"}
	cases := []struct {
		name     string
		filter   FieldProfileFilter
		wantSQL  string
		wantArgs []any
	}{
		{
			// Declared-list restricted: byte-identical to the 10d2ce6d
			// render, parameters $1..$4 in the documented order.
			name: "declared-list restricted keeps the 10d2ce6d render",
			filter: FieldProfileFilter{
				Project: "p1", DatasetGeneration: "generation-a",
				ResourceTypes:                 types,
				AuthResourcePathsUnrestricted: goldenBoolPtr(false),
				AuthResourcePaths:             paths,
			},
			wantSQL: "project = $1 AND dataset_generation = $2 AND resource_type = ANY($3::text[]) AND auth_resource_path = ANY($4::text[])",
			wantArgs: []any{"p1", "generation-a",
				append([]string(nil), types...), append([]string(nil), paths...)},
		},
		{
			// Declared-list unrestricted: byte-identical to 10d2ce6d.
			name: "declared-list unrestricted keeps the 10d2ce6d render",
			filter: FieldProfileFilter{
				Project: "p1", DatasetGeneration: "",
				ResourceTypes:                 types,
				AuthResourcePathsUnrestricted: goldenBoolPtr(true),
			},
			wantSQL:  "project = $1 AND dataset_generation = $2 AND resource_type = ANY($3::text[])",
			wantArgs: []any{"p1", "", append([]string(nil), types...)},
		},
		{
			// All-types unrestricted (the auth-path lister shape): the
			// reference scope exactly -- project + dataset_generation and
			// no residual resource_type fragment.
			name: "all-types unrestricted renders the reference scope verbatim",
			filter: FieldProfileFilter{
				Project: "p1", DatasetGeneration: "generation-a",
				AllResourceTypes:              true,
				AuthResourcePathsUnrestricted: goldenBoolPtr(true),
			},
			wantSQL:  "project = $1 AND dataset_generation = $2",
			wantArgs: []any{"p1", "generation-a"},
		},
		{
			// All-types restricted: the other door, proving the parameter
			// shift -- auth paths render at $3 with no types parameter.
			name: "all-types restricted shifts auth paths to $3",
			filter: FieldProfileFilter{
				Project: "p1", DatasetGeneration: "generation-a",
				AllResourceTypes:              true,
				AuthResourcePathsUnrestricted: goldenBoolPtr(false),
				AuthResourcePaths:             paths,
			},
			wantSQL:  "project = $1 AND dataset_generation = $2 AND auth_resource_path = ANY($3::text[])",
			wantArgs: []any{"p1", "generation-a", append([]string(nil), paths...)},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Two passes pin zero-value inertness; one pass is enough for
			// the all-types cases, whose opt-in is true either way.
			passes := 1
			if !tc.filter.AllResourceTypes {
				passes = 2
			}
			for pass := 0; pass < passes; pass++ {
				filter := tc.filter
				if pass == 1 {
					filter.AllResourceTypes = false // explicit zero == zero
				}
				sql, args, empty, err := fieldProfileFilterSQL(filter)
				if err != nil {
					t.Fatalf("render (pass %d): %v", pass, err)
				}
				if empty {
					t.Fatalf("render (pass %d): expected a rendered read, got read-nothing", pass)
				}
				if sql != tc.wantSQL {
					t.Fatalf("golden SQL drift (pass %d):\n got %q\nwant %q", pass, sql, tc.wantSQL)
				}
				if !reflect.DeepEqual(args, tc.wantArgs) {
					t.Fatalf("golden args drift (pass %d): got %#v want %#v", pass, args, tc.wantArgs)
				}
			}
		})
	}
}

// TestFieldProfileFilterSQLNoResourceTypeFragmentAllTypes pins binding 2
// explicitly: the all-types render must carry no resource_type fragment
// for either auth door (a residual predicate there would silently
// re-introduce the concrete-set filter the reference does not have).
func TestFieldProfileFilterSQLNoResourceTypeFragmentAllTypes(t *testing.T) {
	for _, unrestricted := range []bool{true, false} {
		filter := FieldProfileFilter{
			Project: "p1", DatasetGeneration: "generation-a",
			AllResourceTypes:              true,
			AuthResourcePathsUnrestricted: goldenBoolPtr(unrestricted),
		}
		if !unrestricted {
			filter.AuthResourcePaths = []string{"/fhir/clinical"}
		}
		sql, _, empty, err := fieldProfileFilterSQL(filter)
		if err != nil || empty {
			t.Fatalf("all-types door (unrestricted=%v): sql=%q empty=%v err=%v", unrestricted, sql, empty, err)
		}
		if strings.Contains(sql, "resource_type") {
			t.Fatalf("all-types render (unrestricted=%v) carries a residual resource_type predicate: %q", unrestricted, sql)
		}
	}
}

// TestFieldProfileFilterSQLAllResourceTypesMutualExclusion pins the typed
// refusal at BOTH auth doors: AllResourceTypes may never accompany a
// declared list -- the caller's intent would be ambiguous.
func TestFieldProfileFilterSQLAllResourceTypesMutualExclusion(t *testing.T) {
	for _, door := range []string{"unrestricted", "restricted"} {
		filter := FieldProfileFilter{
			Project:           "p1",
			DatasetGeneration: "generation-a",
			ResourceTypes:     []string{"Patient"},
			AllResourceTypes:  true,
		}
		if door == "unrestricted" {
			filter.AuthResourcePathsUnrestricted = goldenBoolPtr(true)
		} else {
			filter.AuthResourcePathsUnrestricted = goldenBoolPtr(false)
			filter.AuthResourcePaths = []string{"/fhir/clinical"}
		}
		sql, args, empty, err := fieldProfileFilterSQL(filter)
		if err == nil {
			t.Fatalf("%s door: AllResourceTypes + declared list rendered %q (empty=%v, args=%#v); want the typed refusal", door, sql, empty, args)
		}
		if !strings.Contains(err.Error(), "AllResourceTypes must not accompany a declared resource types list") {
			t.Fatalf("%s door: refusal %q must name the conflict", door, err)
		}
		if !errors.Is(err, ErrInvalidFieldProfileScope) {
			t.Fatalf("%s door: refusal %v must wrap ErrInvalidFieldProfileScope", door, err)
		}
	}
}

// TestFieldProfileFilterSQLReadNothingDoorsUnchanged pins that the
// legitimate read-nothing doors still short-circuit in every mode, so the
// new branch cannot turn a deny-by-default state into a query.
func TestFieldProfileFilterSQLReadNothingDoorsUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		filter FieldProfileFilter
	}{
		{"declared-list empty types", FieldProfileFilter{
			Project: "p1", ResourceTypes: nil, AuthResourcePathsUnrestricted: goldenBoolPtr(true),
		}},
		{"restricted without paths (declared list)", FieldProfileFilter{
			Project: "p1", ResourceTypes: []string{"Patient"}, AuthResourcePathsUnrestricted: goldenBoolPtr(false),
		}},
		{"restricted without paths (all types)", FieldProfileFilter{
			Project: "p1", AllResourceTypes: true, AuthResourcePathsUnrestricted: goldenBoolPtr(false),
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			sql, args, empty, err := fieldProfileFilterSQL(tc.filter)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !empty || sql != "" || args != nil {
				t.Fatalf("read-nothing door returned sql=%q args=%#v empty=%v", sql, args, empty)
			}
		})
	}
}
