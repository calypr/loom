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
