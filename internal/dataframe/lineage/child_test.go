package lineage

import "testing"

func TestStableSourceChildIDTracksStructureAndIgnoresWidth(t *testing.T) {
	base := SourceChild{
		Kind: IndexedValueChild, ParentColumnIDs: []string{"source-slot"}, OccurrenceID: "base",
		SourcePath: "name[].given[]", Coordinates: []Coordinate{
			{BoundaryPath: "name[]", Index: 1}, {BoundaryPath: "name[].given[]", Index: 2},
		},
	}
	first, canonical, err := StableSourceChildID(base)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := StableSourceChildID(SourceChild{
		Kind: IndexedValueChild, ParentColumnIDs: []string{"source-slot"}, OccurrenceID: "base",
		SourcePath: "name[].given[]", Coordinates: []Coordinate{
			{BoundaryPath: "name[]", Index: 1}, {BoundaryPath: "name[].given[]", Index: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same source child ID changed: %q != %q", first, second)
	}
	parsed, ok := ParseSourceChildID(first)
	if !ok || parsed.Kind != canonical.Kind || parsed.ParentColumnIDs[0] != "source-slot" || len(parsed.Coordinates) != 2 || parsed.Coordinates[1].Index != 2 {
		t.Fatalf("parsed source child = %#v, %v", parsed, ok)
	}

	changed := base
	changed.Coordinates = append([]Coordinate(nil), base.Coordinates...)
	changed.Coordinates[1].Index++
	other, _, err := StableSourceChildID(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatalf("different structural coordinate reused source child ID %q", first)
	}
	changed = base
	changed.ParentColumnIDs = []string{"other-slot"}
	other, _, err = StableSourceChildID(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Fatalf("different source slot reused source child ID %q", first)
	}
	if _, ok := ParseSourceChildID(first + "trailing"); ok {
		t.Fatal("malformed child ID parsed successfully")
	}
}

func TestRepeatedCountIdentityUsesCanonicalSharedOwner(t *testing.T) {
	child := SourceChild{
		Kind: RepeatedCountChild, ParentColumnIDs: []string{"z_owner", "a_owner"}, OccurrenceID: "base",
		SourcePath: "name[].family", BoundaryPath: "name[]",
	}
	first, canonical, err := StableSourceChildID(child)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.ParentColumnIDs[0] != "a_owner" {
		t.Fatalf("canonical parent IDs = %#v", canonical.ParentColumnIDs)
	}
	child.ParentColumnIDs = []string{"a_owner", "z_owner"}
	second, _, err := StableSourceChildID(child)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("owner ordering changed shared count ID: %q != %q", first, second)
	}
}
