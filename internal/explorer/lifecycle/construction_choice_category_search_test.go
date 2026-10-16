package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestConstructionChoiceSearchReauthorizesSignedCategoryFamily(t *testing.T) {
	store, _, snapshot, catalogSnapshot, _ := inboundPatientObservationRouteFixture(t)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	displayCandidate := capability.Candidate{
		ID: "observation-coded-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "code.coding[].display", Label: "Observation code display", LogicalType: "string", Cardinality: "many",
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionArray},
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
	}
	codeCandidate := displayCandidate
	codeCandidate.ID = "observation-coded-code"
	codeCandidate.FieldPath = "code.coding[].code"
	codeCandidate.Label = "Observation code"
	snapshot.Candidates = []capability.Candidate{displayCandidate, codeCandidate}
	family := testCodedCategoryFamily("file", "Observation", "Observation.code.coding[]", "https://example.org/codes", "v1")
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Patient", buildID)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := semanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, family, displayCandidate)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			expected := catalog.SemanticInventoryFamilyReference{
				FamilyID: family.FamilyID, BindingID: family.BindingID, SourceKind: family.SourceKind,
				ResourceType: family.ResourceType, SourcePath: family.SourcePath, System: family.System, Version: family.Version,
			}
			if !reflect.DeepEqual(options.FamilyReferences, []catalog.SemanticInventoryFamilyReference{expected}) || options.AuthResourcePathsUnrestricted == nil || !*options.AuthResourcePathsUnrestricted {
				t.Fatalf("category resolution was not bound to the signed family and authorized scope: %#v", options)
			}
			return catalog.SemanticInventoryResolveResult{
				Build: catalog.SemanticInventoryBuild{
					Project: snapshot.Identity.Project, DatasetGeneration: snapshot.Identity.Generation,
					BuildID: buildID, State: catalog.SemanticInventoryComplete,
				},
				State: catalog.SemanticInventoryComplete, Families: []catalog.SemanticInventoryFamily{family},
			}, nil
		},
	})
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: snapshot.Identity.Project, ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{
			Kind: capability.ConstructionChoiceSourceSemantic, ContextToken: contextToken, BuildID: buildID,
			ConceptID: family.FamilyID, BindingID: family.BindingID, ChoiceID: signed.ChoiceID,
			SelectionMode: catalog.SemanticInventorySystemVersionCategory,
		},
	})
	if err != nil || !result.Complete || len(result.Choices) != 1 {
		t.Fatalf("routed category choice search = %#v, %v", result, err)
	}
	choice := result.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].Relationship != "subject_Patient" || choice.Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("category route was not reauthorized through the current graph: %#v", choice.Route)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || source.RuleHint != catalog.SemanticRuleHintCodedCategoryV1 || source.ConceptID != family.FamilyID || source.System != family.System || source.Version != family.Version || source.SourceKind != family.SourceKind {
		t.Fatalf("route search returned an exact-code or unsigned source: %#v", identity.Source)
	}
	allSupported := false
	for _, option := range choice.Options {
		if option.Form == capability.ConstructionChoiceAll && option.Support == capability.ConstructionChoiceSupported {
			allSupported = true
		}
	}
	if !allSupported {
		t.Fatalf("routed category choice lost its ALL projection form: %#v", choice.Options)
	}
}
