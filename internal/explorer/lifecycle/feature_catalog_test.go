package lifecycle

import (
	"context"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestBrowseFeatureCatalogFieldsExposeOnlyDirectFHIRValues(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{
		featureCatalogCandidate("birth-date", "birthDate", "Patient.birthDate", "date", "optional_one", capability.ProjectionScalar),
		featureCatalogCandidate("identifier-value", "identifier[].value", "Patient.identifier.value", "string", "many", capability.ProjectionArray),
		featureCatalogCandidate("name", "name[]", "Patient.name", "object", "many", capability.ProjectionArray),
		featureCatalogCandidate("resource-id", "id", "Patient.id", "string", "optional_one", capability.ProjectionScalar),
		featureCatalogCandidate("resource-type", "resourceType", "Patient.resourceType", "string", "one", capability.ProjectionScalar),
	}
	service := &Service{config: Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}}}}
	request := BrowseFeatureCatalogRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Patient", Section: FeatureCatalogFields, Limit: 50}
	result, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Section != FeatureCatalogFields || result.State != catalog.SemanticInventoryComplete || result.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified || result.ContextToken == "" {
		t.Fatalf("catalog envelope = %#v", result)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("direct field entries = %#v", result.Entries)
	}
	paths := make(map[string]FeatureCatalogItem, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.Kind != "DIRECT_FIELD" || entry.ConstructionChoice == nil || entry.Source.Kind != capability.ConstructionChoiceSourceField || entry.Readiness.Code != "READY" {
			t.Fatalf("direct field entry = %#v", entry)
		}
		paths[entry.Source.CandidateID] = entry
	}
	if paths["birth-date"].Title != "Birth date" || paths["resource-id"].Title != "Id" || paths["resource-type"].Title != "Resource type" {
		t.Fatalf("friendly titles = %#v", paths)
	}
	if _, exists := paths["identifier-value"]; exists {
		t.Fatal("Identifier.value leaked as a direct field")
	}
	if _, exists := paths["name"]; exists {
		t.Fatal("HumanName object leaked as a direct field")
	}

	request.Limit = 1
	first, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil || len(first.Entries) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v err=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil || len(second.Entries) != 1 || second.Entries[0].FeatureID == first.Entries[0].FeatureID {
		t.Fatalf("second page = %#v err=%v", second, err)
	}
	request.Query = "changed"
	if _, err := service.BrowseFeatureCatalog(context.Background(), request); err == nil {
		t.Fatal("field cursor survived a changed query")
	}
}

func TestBrowseFeatureCatalogPartitionsReadyConceptsFromNeedsReview(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value",
		LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	ready := semanticAuthoringEntry("height", "height-binding", "http://loinc.org", "8302-2", "")
	ready.Observation.Key.Display = "Body height"
	ready.Observation.Population = 12
	needsReview := catalog.SemanticInventoryEntry{ConceptID: "unpaired", BindingID: "unpaired-binding", Observation: catalog.SemanticObservation{Source: catalog.SemanticObservationSource{Type: "Observation", Path: "component[]"}, Key: catalog.SemanticObservationKey{System: "urn:study", Code: "unpaired"}, Population: 4}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
				Entries: []catalog.SemanticInventoryEntry{*ready, needsReview},
			}, nil
		},
	}}
	request := BrowseFeatureCatalogRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Section: FeatureCatalogConcepts, Limit: 50}
	concepts, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts.Entries) != 1 || concepts.Entries[0].Title != "Body height" || concepts.Entries[0].Kind != "SEMANTIC_FEATURE" || concepts.Entries[0].ConstructionChoice == nil || concepts.Entries[0].Source.ConceptID != "height" {
		t.Fatalf("concept section = %#v", concepts)
	}
	request.Section = FeatureCatalogNeedsReview
	review, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Entries) != 1 || review.Entries[0].FeatureID != "semantic:unpaired:unpaired-binding" || review.Entries[0].ConstructionChoice != nil || review.Entries[0].Readiness.Addable() {
		t.Fatalf("needs-review section = %#v", review)
	}
}

func TestBrowseFeatureCatalogPagesAcrossMixedReadinessWithoutInvalidatingItsCursor(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value",
		LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	readyOne := semanticAuthoringEntry("height", "height-binding", "http://loinc.org", "8302-2", "")
	readyTwo := semanticAuthoringEntry("weight", "weight-binding", "http://loinc.org", "29463-7", "")
	needsReview := catalog.SemanticInventoryEntry{
		ConceptID: "unpaired", BindingID: "unpaired-binding",
		Observation: catalog.SemanticObservation{
			Source: catalog.SemanticObservationSource{Type: "Observation", Path: "component[]"},
			Key:    catalog.SemanticObservationKey{System: "urn:study", Code: "unpaired"},
		},
	}
	const buildID = "build-a"
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			afterBinding, _, err := catalog.DecodeSemanticInventoryCursor(options.Cursor, options, buildID)
			if err != nil {
				return catalog.SemanticInventoryPage{}, err
			}
			page := catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
			}
			if afterBinding == "" {
				page.Entries = []catalog.SemanticInventoryEntry{needsReview, *readyOne}
				page.NextCursor = catalog.EncodeSemanticInventoryCursor(options, buildID, readyOne.BindingID, readyOne.ConceptID)
				return page, nil
			}
			page.Entries = []catalog.SemanticInventoryEntry{*readyTwo}
			return page, nil
		},
	}}
	request := BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Section: FeatureCatalogConcepts, Limit: 2,
	}
	concepts, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts.Entries) != 2 || concepts.Entries[0].FeatureID != "semantic:height:height-binding" || concepts.Entries[1].FeatureID != "semantic:weight:weight-binding" {
		t.Fatalf("concepts = %#v", concepts.Entries)
	}
	request.Section = FeatureCatalogNeedsReview
	review, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Entries) != 1 || review.Entries[0].FeatureID != "semantic:unpaired:unpaired-binding" {
		t.Fatalf("needs review = %#v", review.Entries)
	}
}

func TestBrowseFeatureCatalogOmitsTrailingCursorForExactSemanticPage(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value",
		LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	entry := semanticAuthoringEntry("height", "height-binding", "http://loinc.org", "8302-2", "")
	secondEntry := semanticAuthoringEntry("weight", "weight-binding", "http://loinc.org", "29463-7", "")
	calls := 0
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			calls++
			if options.Limit != 2 {
				t.Fatalf("inventory page limit = %d, want 2", options.Limit)
			}
			page := catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
			}
			if options.Cursor == "" {
				page.Entries = []catalog.SemanticInventoryEntry{*entry, *secondEntry}
				page.NextCursor = "after-height"
			}
			return page, nil
		},
	}}
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Section: FeatureCatalogConcepts, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || result.NextCursor != "" || calls != 2 {
		t.Fatalf("exact semantic page = %#v calls=%d", result, calls)
	}
}

func TestBrowseFeatureCatalogOffersSchemaDerivedCategoricalConcept(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-category-code", NodeID: "observation", ResourceType: "Observation", FieldPath: "category[].coding[].code",
		LogicalType: "code", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}}
	entry := catalog.SemanticInventoryEntry{ConceptID: "laboratory", BindingID: "laboratory-binding", Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "category[]"},
		Key:           catalog.SemanticObservationKey{Selector: "category[].coding[]", System: "http://terminology.hl7.org/CodeSystem/observation-category", Code: "laboratory", Display: "Laboratory"},
		Value:         catalog.SemanticObservationValue{Selector: "category[].coding[].code", Type: "code"},
		OwningScope:   "category[]", LogicalType: "code", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCategoricalCodeV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion), Population: 8,
	}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
				Entries: []catalog.SemanticInventoryEntry{entry},
			}, nil
		},
	}}
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Section: FeatureCatalogConcepts, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Title != "Laboratory" || result.Entries[0].ConstructionChoice == nil || !result.Entries[0].Readiness.Addable() {
		t.Fatalf("categorical feature = %#v", result.Entries)
	}
}

func featureCatalogCandidate(id, path, label, logicalType, cardinality string, projection capability.ProjectionMode) capability.Candidate {
	return capability.Candidate{
		ID: id, NodeID: "patient", ResourceType: "Patient", FieldPath: path, Label: label,
		LogicalType: logicalType, Cardinality: cardinality, ProjectionModes: []capability.ProjectionMode{projection},
		Observed: true, Populated: true, ObservedDocumentCount: 3,
	}
}
