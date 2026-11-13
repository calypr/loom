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
			passes := 1
			if !tc.filter.AllResourceTypes {
				passes = 2
			}
			for pass := 0; pass < passes; pass++ {
				filter := tc.filter
				if pass == 1 {
					filter.AllResourceTypes = false
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
