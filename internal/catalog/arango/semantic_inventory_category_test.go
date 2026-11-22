package arango

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/codedconcept"
	"github.com/calypr/loom/internal/catalog/semanticinventory"
)

func TestSemanticInventoryCategoryPageLimitsMixedSelections(t *testing.T) {
	build := semanticinventory.NewBuild("project", "generation", "")
	build.State = semanticinventory.Complete
	build.EntryIndexVersion = semanticinventory.EntryIndexVersion
	family := categoryTestFamily()
	measurement := semanticinventory.Entry{
		BindingID: "measurement-binding", ConceptID: "measurement-concept",
		Observation: codedconcept.Observation{
			RuleHint: codedconcept.RuleHintCodedValueV1,
			Key:      codedconcept.Key{System: "urn:measure", Code: "glucose"},
			Value:    codedconcept.Value{Selector: "valueQuantity.value", Type: "number"},
		},
	}
	const selectionID = `family:["Specimen","type.coding[]","urn:specimen",""]`
	familyRow := map[string]any{"binding_id": family.BindingID, "selection_id": selectionID, "family": semanticInventoryTestRow(t, family)}
	for _, firstFamily := range []bool{true, false} {
		rows := []map[string]any{familyRow, semanticInventoryTestRow(t, measurement)}
		if !firstFamily {
			rows[0], rows[1] = rows[1], rows[0]
		}
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildsAQL: {semanticInventoryTestRow(t, build)}, semanticInventoryCategoryPageAQL: rows,
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		opts := semanticinventory.PageOptions{
			Project: "project", DatasetGeneration: "generation", Limit: 1,
			SelectionMode:                 semanticinventory.SystemVersionCategory,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
			AuthResourcePaths:             []string{"scope-b", "scope-a", "scope-a"},
		}
		page, err := adapter.PageSemanticInventory(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries)+len(page.Families) != 1 || page.NextCursor == "" {
			t.Fatalf("mixed page did not retain one selection and a cursor: %#v", page)
		}
		bindingID, selectedID, err := semanticinventory.DecodeCursor(page.NextCursor, opts, build.BuildID)
		if err != nil {
			t.Fatal(err)
		}
		if firstFamily {
			if len(page.Families) != 1 || !reflect.DeepEqual(page.Families[0], family) || bindingID != family.BindingID || selectedID != selectionID {
				t.Fatalf("category page or cursor lost family: %#v, %q/%q", page, bindingID, selectedID)
			}
		} else if len(page.Entries) != 1 || !reflect.DeepEqual(page.Entries[0], measurement) || bindingID != measurement.BindingID || selectedID != measurement.ConceptID {
			t.Fatalf("category page changed the paired measurement: %#v, %q/%q", page, bindingID, selectedID)
		}
		vars := client.vars[1]
		if client.queries[1] != semanticInventoryCategoryPageAQL || vars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(vars["auth_resource_paths"], []string{"scope-a", "scope-b"}) {
			t.Fatalf("category query lost mode or authorization: %#v", vars)
		}
	}
}

func TestResolveSemanticInventoryFamilyUsesCurrentInventoryScope(t *testing.T) {
	for _, indexed := range []bool{true, false} {
		build := semanticinventory.NewBuild("project", "generation", "")
		build.State = semanticinventory.Complete
		collection := semanticinventory.Collection
		if indexed {
			build.EntryIndexVersion = semanticinventory.EntryIndexVersion
			collection = semanticinventory.EntryCollection
		}
		family := categoryTestFamily()
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildByKeyAQL:      {semanticInventoryTestRow(t, build)},
			semanticInventoryResolveFamiliesAQL: {semanticInventoryTestRow(t, family)},
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		opts := semanticinventory.ResolveOptions{
			Project: "project", DatasetGeneration: "generation",
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
			AuthResourcePaths:             []string{"scope-a"},
			FamilyReferences:              []semanticinventory.FamilyReference{family.FamilyReference},
		}
		result, err := adapter.ResolveSemanticInventorySelections(context.Background(), opts)
		if !indexed {
			if err != ErrSemanticInventoryCategoryIndexUnavailable || len(result.Families) != 0 ||
				!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildByKeyAQL}) ||
				!reflect.DeepEqual(client.collectionChecks, []string{semanticinventory.BuildCollection}) {
				t.Fatalf("unindexed family resolution did not fail before accessing inventory: %#v, %v, queries %#v, collections %#v", result, err, client.queries, client.collectionChecks)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 0 || len(result.Families) != 1 || !reflect.DeepEqual(result.Families[0], family) {
			t.Fatalf("typed family resolution changed: %#v", result)
		}
		if len(client.queries) != 2 || client.queries[1] != semanticInventoryResolveFamiliesAQL {
			t.Fatalf("family resolution queried beyond build and inventory: %#v", client.queries)
		}
		vars := client.vars[1]
		if vars["@inventory"] != collection || vars["build_id"] != build.BuildID || vars["project"] != opts.Project || vars["dataset_generation"] != opts.DatasetGeneration ||
			vars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(vars["auth_resource_paths"], opts.AuthResourcePaths) || !reflect.DeepEqual(vars["references"], opts.FamilyReferences) {
			t.Fatalf("family resolver lost immutable scope: %#v", vars)
		}
		client.rows[semanticInventoryResolveFamiliesAQL] = nil
		result, err = adapter.ResolveSemanticInventorySelections(context.Background(), opts)
		if err != nil || len(result.Families) != 0 {
			t.Fatalf("missing authorized family did not stay unresolved: %#v, %v", result, err)
		}
	}
}

func TestSemanticInventoryCategoryPageRequiresEntryIndexAndPreservesExactFallback(t *testing.T) {
	build := semanticinventory.NewBuild("project", "generation", "")
	build.State = semanticinventory.Complete
	entry := semanticinventory.Entry{BindingID: "binding", ConceptID: "concept"}
	for _, mode := range []semanticinventory.SelectionMode{"", semanticinventory.ExactConcept, semanticinventory.SystemVersionCategory} {
		client := &evidenceClient{rows: map[string][]map[string]any{
			semanticInventoryBuildsAQL: {semanticInventoryTestRow(t, build)},
			semanticInventoryPageAQL:   {semanticInventoryTestRow(t, entry)},
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}
		page, err := adapter.PageSemanticInventory(context.Background(), semanticinventory.PageOptions{
			Project: "project", DatasetGeneration: "generation", SelectionMode: mode,
		})
		if mode == semanticinventory.SystemVersionCategory {
			if err != ErrSemanticInventoryCategoryIndexUnavailable || len(page.Entries)+len(page.Families) != 0 ||
				!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildsAQL}) ||
				!reflect.DeepEqual(client.collectionChecks, []string{semanticinventory.BuildCollection}) {
				t.Fatalf("unindexed category page did not fail before accessing inventory: %#v, %v, queries %#v, collections %#v", page, err, client.queries, client.collectionChecks)
			}
			continue
		}
		if err != nil || len(page.Entries) != 1 || page.Entries[0].ConceptID != entry.ConceptID ||
			!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildsAQL, semanticInventoryPageAQL}) ||
			client.vars[1]["@inventory"] != semanticinventory.Collection {
			t.Fatalf("exact mode %q lost its legacy query and contribution fallback: %#v, %v, queries %#v", mode, page, err, client.queries)
		}
	}
}

func TestResolveSemanticInventoryMixedSelectionsRequireEntryIndex(t *testing.T) {
	build := semanticinventory.NewBuild("project", "generation", "")
	build.State = semanticinventory.Complete
	family := categoryTestFamily()
	client := &evidenceClient{rows: map[string][]map[string]any{
		semanticInventoryBuildByKeyAQL: {semanticInventoryTestRow(t, build)},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ResolveSemanticInventorySelections(context.Background(), semanticinventory.ResolveOptions{
		Project: "project", DatasetGeneration: "generation",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		References:                    []semanticinventory.Reference{{BindingID: "measurement-binding", ConceptID: "measurement-concept"}},
		FamilyReferences:              []semanticinventory.FamilyReference{family.FamilyReference},
	})
	if err != ErrSemanticInventoryCategoryIndexUnavailable ||
		!reflect.DeepEqual(client.queries, []string{semanticInventoryBuildByKeyAQL}) ||
		!reflect.DeepEqual(client.collectionChecks, []string{semanticinventory.BuildCollection}) {
		t.Fatalf("mixed category resolution reached contribution fallback: %v, queries %#v, collections %#v", err, client.queries, client.collectionChecks)
	}
}

func TestResolveSemanticInventoryFamilyRejectsTamperedReferences(t *testing.T) {
	family := categoryTestFamily()
	for name, change := range map[string]func(*semanticinventory.FamilyReference){
		"version": func(ref *semanticinventory.FamilyReference) { ref.Version = "v1" },
		"system":  func(ref *semanticinventory.FamilyReference) { ref.System = "urn:other" },
		"source":  func(ref *semanticinventory.FamilyReference) { ref.SourcePath = "category.coding[]" },
		"empty system": func(ref *semanticinventory.FamilyReference) {
			ref.System = ""
			ref.FamilyID = semanticinventory.FamilyID(*ref)
		},
		"unknown source kind": func(ref *semanticinventory.FamilyReference) {
			ref.SourceKind = "unknown"
			ref.FamilyID = semanticinventory.FamilyID(*ref)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ref := family.FamilyReference
			change(&ref)
			client := &evidenceClient{}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.ResolveSemanticInventorySelections(context.Background(), semanticinventory.ResolveOptions{
				Project: "project", DatasetGeneration: "generation",
				AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
				FamilyReferences:              []semanticinventory.FamilyReference{ref},
			})
			if err != ErrInvalidSemanticInventorySelection || len(client.queries) != 0 {
				t.Fatalf("invalid reference reached the inventory: %v, %#v", err, client.queries)
			}
		})
	}
}

func categoryTestFamily() semanticinventory.Family {
	family := semanticinventory.Family{
		FamilyReference: semanticinventory.FamilyReference{
			BindingID: "binding", SourceKind: semanticinventory.SourceFile,
			ResourceType: "Specimen", SourcePath: "type.coding[]", System: "urn:specimen", Version: "",
		},
		Source:      codedconcept.Source{Type: "Specimen", Path: "type.coding[]"},
		KeySelector: "type.coding[]",
		Value: codedconcept.Value{
			Selector: "type.coding[].display", Type: "string", FallbackSelectors: []string{"type.coding[].code"}, FallbackOnBlankString: true,
		},
		Population: 2, Examples: []string{"Normal"}, ExamplesTruncated: true,
	}
	family.FamilyID = semanticinventory.FamilyID(family.FamilyReference)
	return family
}
