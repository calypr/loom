package lifecycle

import (
	"context"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
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
	service.store = featureCatalogTestStore(t, "Patient")
	request := BrowseFeatureCatalogRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogFields, Limit: 50}
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
		if entry.Coverage.State != "INDEXED" || entry.Coverage.RowsWithValue == nil || *entry.Coverage.RowsWithValue != 3 {
			t.Fatalf("root field coverage = %#v", entry.Coverage)
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

func TestBrowseFeatureCatalogFieldsIncludeObservedRelatedResources(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true},
		{ID: "observation", ResourceType: "Observation", RowRootEligible: true},
		{ID: "condition", ResourceType: "Condition", RowRootEligible: true},
	}
	snapshot.Edges = []capability.Edge{{
		ID: "observation-specimen", FromNodeID: "specimen", ToNodeID: "observation",
		SourceResourceType: "Specimen", TargetResourceType: "Observation",
		Label: "specimen_Specimen", StorageDirection: "INBOUND", ObservedEdgeCount: 3,
	}}
	snapshot.Candidates = []capability.Candidate{
		{ID: "specimen-id", NodeID: "specimen", ResourceType: "Specimen", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, ObservedDocumentCount: 3},
		{ID: "observation-id", NodeID: "observation", ResourceType: "Observation", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, ObservedDocumentCount: 2},
		{ID: "condition-id", NodeID: "condition", ResourceType: "Condition", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, ObservedDocumentCount: 2},
	}
	service := &Service{config: Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}}}}
	service.store = featureCatalogTestStore(t, "Specimen")
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogFields, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Coverage.State != "PENDING" && result.Entries[1].Coverage.State != "PENDING" {
		t.Fatalf("related field catalog = %#v", result.Entries)
	}
	for _, entry := range result.Entries {
		if entry.ResourceType == "Condition" {
			t.Fatal("disconnected resource leaked into default field catalog")
		}
		if entry.ResourceType == "Observation" && (entry.Source.CandidateID != "observation-id" || entry.Coverage.State != "PENDING") {
			t.Fatalf("related field = %#v", entry)
		}
	}
}

func TestFeatureCatalogCoverageDoesNotClaimCrossResourceValuesFromProjectCounts(t *testing.T) {
	count := int64(17)
	root := BrowseFeatureCatalogRequest{rowRoot: "Patient", defaultCohort: true}
	if got := featureCatalogCoverage(root, "Observation", &count); got.State != "PENDING" || got.RowsWithValue != nil {
		t.Fatalf("cross-resource coverage = %#v", got)
	}
	if got := featureCatalogCoverage(root, "Patient", &count); got.State != "INDEXED" || got.RowsWithValue == nil || *got.RowsWithValue != 17 {
		t.Fatalf("root coverage = %#v", got)
	}
	root.defaultCohort = false
	if got := featureCatalogCoverage(root, "Patient", &count); got.State != "PENDING" || got.RowsWithValue != nil {
		t.Fatalf("selected-row coverage = %#v", got)
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
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			if options.CatalogSection == catalog.SemanticInventorySectionConcepts {
				return catalog.SemanticInventoryPage{
					State:   catalog.SemanticInventoryComplete,
					Build:   catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, EntryIndexVersion: catalog.SemanticInventoryEntryIndexVersion, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
					Entries: []catalog.SemanticInventoryEntry{*ready},
				}, nil
			}
			if options.CatalogSection != catalog.SemanticInventorySectionNeedsReview {
				t.Fatalf("catalog section = %q", options.CatalogSection)
			}
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, EntryIndexVersion: catalog.SemanticInventoryEntryIndexVersion, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
				Entries: []catalog.SemanticInventoryEntry{needsReview},
			}, nil
		},
	}}
	service.store = featureCatalogTestStore(t, "Observation")
	request := BrowseFeatureCatalogRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogConcepts, Limit: 50}
	concepts, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts.Entries) != 1 || concepts.Entries[0].Title != "Body height" || concepts.Entries[0].Kind != "SEMANTIC_FEATURE" || concepts.Entries[0].ConstructionChoice == nil || concepts.Entries[0].Source.ConceptID != "height" || !concepts.Entries[0].Readiness.Addable() {
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

func TestBrowseFeatureCatalogPagesWithinIndexedSectionWithoutInvalidatingItsCursor(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value",
		LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	readyOne := semanticAuthoringEntry("height", "height-binding", "http://loinc.org", "8302-2", "")
	readyTwo := semanticAuthoringEntry("weight", "weight-binding", "http://loinc.org", "29463-7", "")
	readyThree := semanticAuthoringEntry("temperature", "temperature-binding", "http://loinc.org", "8310-5", "")
	needsReview := catalog.SemanticInventoryEntry{
		ConceptID: "unpaired", BindingID: "unpaired-binding",
		Observation: catalog.SemanticObservation{
			Source: catalog.SemanticObservationSource{Type: "Observation", Path: "component[]"},
			Key:    catalog.SemanticObservationKey{System: "urn:study", Code: "unpaired"},
		},
	}
	const buildID = "build-a"
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
			afterBinding, _, err := catalog.DecodeSemanticInventoryCursor(options.Cursor, options, buildID)
			if err != nil {
				return catalog.SemanticInventoryPage{}, err
			}
			page := catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete, EntryIndexVersion: catalog.SemanticInventoryEntryIndexVersion, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
			}
			switch options.CatalogSection {
			case catalog.SemanticInventorySectionConcepts:
				if afterBinding == "" {
					page.Entries = []catalog.SemanticInventoryEntry{*readyOne, *readyTwo}
					page.NextCursor = catalog.EncodeSemanticInventoryCursor(options, buildID, readyTwo.BindingID, readyTwo.ConceptID)
				} else {
					page.Entries = []catalog.SemanticInventoryEntry{*readyThree}
				}
			case catalog.SemanticInventorySectionNeedsReview:
				page.Entries = []catalog.SemanticInventoryEntry{needsReview}
			default:
				t.Fatalf("catalog section = %q", options.CatalogSection)
			}
			return page, nil
		},
	}}
	service.store = featureCatalogTestStore(t, "Observation")
	request := BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogConcepts, Limit: 2,
	}
	concepts, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(concepts.Entries) != 2 || concepts.Entries[0].FeatureID != "semantic:height:height-binding" || concepts.Entries[1].FeatureID != "semantic:weight:weight-binding" {
		t.Fatalf("concepts = %#v", concepts.Entries)
	}
	request.Cursor = concepts.NextCursor
	secondConcepts, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil || len(secondConcepts.Entries) != 1 || secondConcepts.Entries[0].FeatureID != "semantic:temperature:temperature-binding" || secondConcepts.NextCursor != "" {
		t.Fatalf("second concepts page = %#v err=%v", secondConcepts, err)
	}
	if calls != 2 {
		t.Fatalf("concept section calls = %d, want one indexed read per page", calls)
	}
	request.Cursor = ""
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
			if options.Limit != 2 || options.CatalogSection != catalog.SemanticInventorySectionConcepts {
				t.Fatalf("inventory options = %#v, want limit 2 and CONCEPTS", options)
			}
			page := catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{BuildID: "build-a", State: catalog.SemanticInventoryComplete, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified},
			}
			page.Entries = []catalog.SemanticInventoryEntry{*entry, *secondEntry}
			return page, nil
		},
	}}
	service.store = featureCatalogTestStore(t, "Observation")
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogConcepts, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 || result.NextCursor != "" || calls != 1 {
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
	entry := catalog.SemanticInventoryEntry{ConceptID: "laboratory", BindingID: "laboratory-binding", SourceRecords: 2, Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Role:          catalog.SemanticRoleCategoricalSlot,
		SlotLabel:     "Observation category",
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "category[]"},
		Key:           catalog.SemanticObservationKey{Selector: "category[].coding[]", System: "urn:category"},
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
	service.store = featureCatalogTestStore(t, "Observation")
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogConcepts, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Title != "Observation category" || result.Entries[0].Cardinality != "many" || result.Entries[0].SourceRecords == nil || *result.Entries[0].SourceRecords != 2 || result.Entries[0].ConstructionChoice == nil || !result.Entries[0].Readiness.Addable() {
		t.Fatalf("categorical feature = %#v", result.Entries)
	}
}

func TestSemanticFeatureCatalogUsesUnknownWhenSchemaCardinalityCannotBeResolved(t *testing.T) {
	sourceRecords := int64(3)
	known := SemanticInventoryItem{
		ConceptID: "concept", BindingID: "binding", ResourceType: "Observation",
		SourcePath: "category[]", OwningScope: "category[]", ValueSelector: "category[].coding[].code",
		ValueType: "code", Occurrences: 4, SourceRecords: &sourceRecords,
	}
	if got := semanticFeatureCatalogItem(known, BrowseFeatureCatalogRequest{}); got.Cardinality != "many" || got.SourceRecords == nil || *got.SourceRecords != 3 {
		t.Fatalf("schema-derived cardinality = %#v", got)
	}
	unknown := known
	unknown.ValueSelector = "notARealField"
	unknown.SourceRecords = nil
	if got := semanticFeatureCatalogItem(unknown, BrowseFeatureCatalogRequest{}); got.Cardinality != "unknown" || got.SourceRecords != nil {
		t.Fatalf("unknown cardinality = %#v", got)
	}
}

func TestFeatureCatalogPivotFamilyGroupsCodesOnlyAtTheSameSourceAndRoute(t *testing.T) {
	item := SemanticInventoryItem{
		Role:         catalog.SemanticRoleCodedValue,
		ResourceType: "Observation", SourcePath: "component[].code", System: "urn:measurements",
		Code: "height", ValueSelector: "component[].valueQuantity.value",
		ConstructionChoice: &capability.ConstructionChoice{
			Source: capability.SemanticBindingChoiceSource{
				Kind: capability.ConstructionChoiceSourceSemantic, ResourceType: "Observation",
				SourcePath: "component[].code", OwningScope: "component[]",
				KeySelector: "component[].code.coding[]", System: "urn:measurements",
				ValueSelector: "component[].valueQuantity.value", LogicalType: "decimal",
			},
			Route:   []capability.ConstructionRouteStep{{EdgeID: "edge-a", Relationship: "subject_Patient"}},
			Options: []capability.ConstructionChoiceOption{{Form: capability.ConstructionChoiceAll, Preservation: capability.ConstructionChoicePreserving, RowEffect: capability.ConstructionChoicePreservesRows, Support: capability.ConstructionChoiceSupported}},
		},
	}
	first := featureCatalogPivotFamily(item, nil)
	if first == nil || first.ID == "" || first.Relationship != "From related Observation records (1 connection)" || first.Form != capability.ConstructionChoiceAll || !first.MultiCodeRowsPossible {
		t.Fatalf("coded value pivot family = %#v", first)
	}
	item.Code = "weight"
	second := featureCatalogPivotFamily(item, nil)
	if second == nil || first.ID != second.ID || first.Code != "height" || second.Code != "weight" {
		t.Fatalf("distinct codes should share a family: first=%#v second=%#v", first, second)
	}
	item.ConstructionChoice.Route[0].EdgeID = "edge-b"
	third := featureCatalogPivotFamily(item, nil)
	if third == nil || third.ID == first.ID {
		t.Fatalf("a different route must not share a family: first=%#v third=%#v", first, third)
	}
	item.ConstructionChoice.Route[0].EdgeID = "edge-a"
	source := item.ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	source.ValueSelector = "component[].valueString"
	item.ConstructionChoice.Source = source
	if changedValue := featureCatalogPivotFamily(item, nil); changedValue == nil || changedValue.ID == first.ID {
		t.Fatalf("different value members cannot share a family: first=%#v changed=%#v", first, changedValue)
	}
	source.ValueSelector = "component[].valueQuantity.value"
	source.ResourceType = "Specimen"
	item.ConstructionChoice.Source = source
	if specimen := featureCatalogPivotFamily(item, nil); specimen == nil || specimen.ID == first.ID || specimen.Title != "Specimen Code values" {
		t.Fatalf("family derivation must be resource-type agnostic: first=%#v specimen=%#v", first, specimen)
	}
	item.Role = catalog.SemanticRoleCategoricalSlot
	if got := featureCatalogPivotFamily(item, nil); got != nil {
		t.Fatalf("categorical slot is a value, not a pivot key: %#v", got)
	}
}

func TestPivotFamilyRecommendationRequiresPotentiallyMultipleOwnersPerRow(t *testing.T) {
	if pivotFamilyCanReachMultipleOwners("root", nil, nil) {
		t.Fatal("a single root code owner cannot produce two independent code values on one row")
	}
	if !pivotFamilyCanReachMultipleOwners("component[]", nil, nil) {
		t.Fatal("a repeated nested owner can contribute multiple codes to one row")
	}
	route := []capability.ConstructionRouteStep{{EdgeID: "observation-edge"}}
	snapshot := capability.Snapshot{Edges: []capability.Edge{{ID: "observation-edge", AllowsRepeatedTarget: true}}}
	if !pivotFamilyCanReachMultipleOwners("root", route, &snapshot) {
		t.Fatal("a repeated related target can contribute multiple coded resources to one row")
	}
	snapshot.Edges[0].AllowsRepeatedTarget = false
	if pivotFamilyCanReachMultipleOwners("root", route, &snapshot) {
		t.Fatal("a single related target is not an evidenced multi-code pivot target")
	}
}

func TestBrowseFeatureCatalogKeepsRecognizedConceptWhenCompilerProofIsUnavailable(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	entry := semanticAuthoringEntry("height", "height-binding", "http://loinc.org", "8302-2", "")
	calls := 0
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			calls++
			if options.CatalogSection != catalog.SemanticInventorySectionConcepts {
				t.Fatalf("catalog section = %q, want CONCEPTS", options.CatalogSection)
			}
			return catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{
					BuildID: "build-a", State: catalog.SemanticInventoryComplete,
					EntryIndexVersion: catalog.SemanticInventoryEntryIndexVersion,
				},
				Entries: []catalog.SemanticInventoryEntry{*entry},
			}, nil
		},
	}}
	service.store = featureCatalogTestStore(t, "Observation")
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token",
		OutputID: "test_output", Section: FeatureCatalogConcepts, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(result.Entries) != 1 || result.Entries[0].Source.ConceptID != "height" || result.Entries[0].Readiness.Addable() || result.Entries[0].Readiness.Code != "COMPILER_PROOF_UNAVAILABLE" || result.Entries[0].ConstructionChoice != nil {
		t.Fatalf("recognized concept with unavailable proof = %#v calls=%d", result.Entries, calls)
	}
}

func TestBrowseFeatureCatalogMarksDisconnectedDirectFieldUnavailable(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "observation", ResourceType: "Observation"},
	}
	snapshot.Candidates = []capability.Candidate{{
		ID: "observation-id", NodeID: "observation", ResourceType: "Observation", FieldPath: "id",
		Label: "Observation.id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	service := &Service{config: Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}}}}
	service.store = featureCatalogTestStore(t, "Patient")
	result, err := service.BrowseFeatureCatalog(context.Background(), BrowseFeatureCatalogRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, OutputID: "test_output", ResourceType: "Observation", Section: FeatureCatalogFields, Limit: 50,
	})
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("disconnected direct field catalog = %#v, %v", result, err)
	}
	entry := result.Entries[0]
	if entry.Readiness.Status != authoringv2.SemanticReadinessUnsupported || entry.Readiness.Code != "SOURCE_NOT_CONNECTED" || entry.Readiness.Message == "" || entry.ConstructionChoice != nil {
		t.Fatalf("disconnected direct field = %#v", entry)
	}
}

func featureCatalogCandidate(id, path, label, logicalType, cardinality string, projection capability.ProjectionMode) capability.Candidate {
	return capability.Candidate{
		ID: id, NodeID: "patient", ResourceType: "Patient", FieldPath: path, Label: label,
		LogicalType: logicalType, Cardinality: cardinality, ProjectionModes: []capability.ProjectionMode{projection},
		Observed: true, Populated: true, ObservedDocumentCount: 3,
	}
}

func featureCatalogTestStore(t *testing.T, root string) *explorer.Service {
	t.Helper()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Test"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "test_output", Title: "Test"},
			RootResourceType: root,
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: root},
			Rows:             authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}},
		}},
		Tabs: []authoringv2.Tab{{ID: "test_tab", Title: "Test", OutputID: "test_output", Visible: true}},
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store, err := explorer.NewService(&fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "explorer", DraftConfig: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
