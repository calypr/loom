package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestConstructionSemanticGroupChoicesPagesPastIneligibleFamilies(t *testing.T) {
	snapshot, base, stage, eligible := semanticGroupChoicesTestSetup(t)
	ineligible := eligible
	ineligible.Source.Path = "Observation.unadvertised.coding[]"
	ineligible.SourcePath = "Observation.unadvertised.coding[]"
	ineligible.FamilyID = catalog.SemanticInventoryFamilyID(ineligible.SemanticInventoryFamilyReference)
	buildID := catalog.SemanticInventoryBuildID("project-a", "generation-a")
	var cursors []string
	service := &Service{config: Config{
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			cursors = append(cursors, options.Cursor)
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
				options.ResourceType != "Observation" || options.SelectionMode != catalog.SemanticInventorySystemVersionCategory ||
				options.Limit != constructionSemanticGroupChoiceLimit || options.AuthResourcePathsUnrestricted == nil || !*options.AuthResourcePathsUnrestricted {
				t.Fatalf("semantic Group inventory request lost exact snapshot scope: %#v", options)
			}
			page := catalog.SemanticInventoryPage{
				State: catalog.SemanticInventoryComplete,
				Build: catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
			}
			switch options.Cursor {
			case "":
				page.Families = []catalog.SemanticInventoryFamily{ineligible}
				page.NextCursor = "next-page"
			case "next-page":
				page.Families = []catalog.SemanticInventoryFamily{eligible}
			default:
				t.Fatalf("unexpected semantic inventory cursor %q", options.Cursor)
			}
			return page, nil
		},
	}}
	base.snapshot = snapshot
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if err != nil || incomplete || len(choices) != 1 {
		t.Fatalf("choices=%#v incomplete=%t err=%v, want one complete later-page field", choices, incomplete, err)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "next-page" {
		t.Fatalf("semantic Group page cursors = %#v, want empty then next-page", cursors)
	}
	if choices[0].Label != "Observation code" || choices[0].ScopeLabel != "retained records · Observation.code.coding[] · urn:semantic-group-test · version v1" {
		t.Fatalf("semantic Group choice presentation = %#v, want field-first label with exact source/system/version detail", choices[0])
	}
	identity, err := capability.DecodeConstructionChoiceID(choices[0].ChoiceID)
	if err != nil {
		t.Fatalf("decode semantic Group choice identity: %v", err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok || identity.SnapshotToken != snapshot.Token || identity.BuildID != buildID ||
		source.GroupOutputID != "patients" || source.GroupStageID != stage.ID ||
		source.ConceptID != eligible.FamilyID || source.BindingID != eligible.BindingID ||
		source.SourceKind != catalog.SemanticInventorySourceRetained || source.ResourceType != "Observation" ||
		source.SourcePath != "Observation.code.coding[]" || source.System != eligible.System || source.Version != eligible.Version ||
		source.RuleVersion != eligible.RuleVersion {
		t.Fatalf("semantic Group choice identity = %#v, source=%#v, want exact stage/output/family scope", identity, source)
	}
}

func TestConstructionSemanticGroupChoicesRequireDirectRootCodedCapability(t *testing.T) {
	snapshot, base, stage, _ := semanticGroupChoicesTestSetup(t)
	base.snapshot = snapshot
	calls := 0
	stage.Capabilities = []explorer.ReceiptConstructionOperationChoice{
		{Kind: "GROUP", Supported: true}, {Kind: "CODED_PIVOT", Supported: false},
	}
	service := &Service{config: Config{
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			calls++
			return catalog.SemanticInventoryPage{}, nil
		},
	}}
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if err != nil || incomplete || len(choices) != 0 || calls != 0 {
		t.Fatalf("semantic Group choices=%#v incomplete=%t calls=%d err=%v, want no lookup when direct-root coded execution is unavailable", choices, incomplete, calls, err)
	}
}

func TestConstructionSemanticGroupChoicesMarksBoundedInventoryIncomplete(t *testing.T) {
	snapshot, base, stage, eligible := semanticGroupChoicesTestSetup(t)
	buildID := catalog.SemanticInventoryBuildID("project-a", "generation-a")
	calls := 0
	service := &Service{config: Config{
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			wantCursor := ""
			if calls > 0 {
				wantCursor = fmt.Sprintf("page-%d", calls)
			}
			if options.Cursor != wantCursor {
				t.Fatalf("page %d cursor = %q, want %q", calls+1, options.Cursor, wantCursor)
			}
			page := catalog.SemanticInventoryPage{
				State:      catalog.SemanticInventoryComplete,
				Build:      catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
				NextCursor: fmt.Sprintf("page-%d", calls+1),
			}
			if calls == 0 {
				page.Families = []catalog.SemanticInventoryFamily{eligible}
			}
			calls++
			return page, nil
		},
	}}
	base.snapshot = snapshot
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if err != nil || !incomplete || len(choices) != 1 || calls != constructionSemanticGroupChoicePageLimit {
		t.Fatalf("choices=%#v incomplete=%t calls=%d err=%v, want one field, five bounded pages, and explicit incomplete status", choices, incomplete, calls, err)
	}
}

func semanticGroupChoicesTestSetup(t *testing.T) (capability.Snapshot, constructionBase, explorer.ReceiptConstructionStage, catalog.SemanticInventoryFamily) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "semantic-group-snapshot", scope)
	candidate := capability.Candidate{
		ID: "observation-code-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "code.coding[].display", Label: "Code display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	codeCandidate := candidate
	codeCandidate.ID, codeCandidate.FieldPath, codeCandidate.Label = "observation-code", "code.coding[].code", "Code"
	snapshot.Candidates = []capability.Candidate{candidate, codeCandidate}
	family := testCodedCategoryFamily(
		catalog.SemanticInventorySourceRetained, "Observation", "Observation.code.coding[]", "urn:semantic-group-test", "v1",
	)
	if !semanticGroupFamilyReady(family) {
		entry := semanticInventoryEntryForFamily(family)
		t.Fatalf("test family is not a ready semantic Group family: %#v plan=%#v", family, authoringv2.ResolveSemanticSelectionPlan(entry.Observation))
	}
	if path, ok := constructionSemanticGroupCodingPath("Observation", family); !ok || path != "code.coding[]" {
		t.Fatalf("test family coding path = %q, ok=%t", path, ok)
	}
	if candidate, ok := semanticConstructionCandidate(snapshot, semanticInventoryEntryForFamily(family).Observation); !ok || candidate.ID != "observation-code-display" {
		t.Fatalf("test family did not resolve its compiler candidate: %#v ok=%t", candidate, ok)
	}
	base := constructionBase{
		explorerID: "builder",
		snapshot:   snapshot,
		authorized: AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		document: authoringv2.Document{
			RootResourceType: "Observation",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		},
	}
	stage := explorer.ReceiptConstructionStage{
		ID: "source_projection", RowIdentityColumn: "_key",
		Capabilities: []explorer.ReceiptConstructionOperationChoice{
			{Kind: "GROUP", Supported: true}, {Kind: "CODED_PIVOT", Supported: true},
		},
	}
	return snapshot, base, stage, family
}
