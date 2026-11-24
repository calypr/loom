package ir

import "testing"

func TestPhysicalCategoryTerminalRequiresBoundedPresenceContract(t *testing.T) {
	for _, test := range []PhysicalCategoryScanReturn{
		{CategoryColumn: "category", PresenceMarkerColumn: "marker", MaxValues: 0},
		{CategoryColumn: "category", PresenceMarkerColumn: "marker", MaxValues: 257},
		{CategoryColumn: "category", MaxValues: 4},
		{CategoryColumn: "category", PresenceMarkerColumn: "marker", MaxValues: 4, RootIndexHint: "index\" FILTER true"},
		{CategoryColumn: "category", MaxValues: 4, ValueColumn: "value", IndexedRoot: &PhysicalIndexedCategoryScan{RootVariable: "root", ValuePath: []string{"payload", "code"}}},
		{CategoryColumn: "category", MaxValues: 4, IndexedRoot: &PhysicalIndexedCategoryScan{RootVariable: "root", ValuePath: []string{"payload", "bad[]"}}},
	} {
		if err := test.Validate(); err == nil {
			t.Fatalf("invalid category terminal accepted: %#v", test)
		}
	}
	for _, test := range []PhysicalCategoryScanReturn{
		{CategoryColumn: "category", PresenceMarkerColumn: "marker", MaxValues: 256},
		{CategoryColumn: "category", PresenceCompanionColumn: "present", MaxValues: 1},
	} {
		if err := test.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
