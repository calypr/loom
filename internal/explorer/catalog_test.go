package explorer

import "testing"

func TestCatalogSnapshotBindsIdentityAndBlocksIncompleteDiscovery(t *testing.T) {
	catalog := Catalog{Nodes: []CatalogNode{{ID: "n", ResourceType: "Patient"}}, Selections: map[string]CatalogSelection{"s": {ID: "s", NodeID: "n"}}}
	a, err := NewCatalogSnapshot("p", "g", "scope", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateToken(a.Token); err != nil {
		t.Fatal(err)
	}
	b, _ := NewCatalogSnapshot("p", "other", "scope", catalog, true, false, nil)
	if a.Token == b.Token {
		t.Fatal("generation did not affect snapshot")
	}
	incomplete, _ := NewCatalogSnapshot("p", "g", "scope", catalog, false, true, nil)
	if err := incomplete.ValidateToken(incomplete.Token); err != ErrIncompleteCatalog {
		t.Fatalf("err=%v", err)
	}
}

func TestCatalogSnapshotTokenBindsResolvedSchemaDigest(t *testing.T) {
	catalog := Catalog{Selections: map[string]CatalogSelection{}}
	a, err := NewCatalogSnapshotWithSchema("p", "g", "scope", "schema-a", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCatalogSnapshotWithSchema("p", "g", "scope", "schema-b", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Token == b.Token {
		t.Fatalf("schema-specific snapshots reused token %q", a.Token)
	}
	if a.ResolvedSchemaDigest != "schema-a" || b.ResolvedSchemaDigest != "schema-b" {
		t.Fatalf("schema digests were not retained: a=%q b=%q", a.ResolvedSchemaDigest, b.ResolvedSchemaDigest)
	}
}
