package construction

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
)

func TestConstructionSemanticGroupChoicesPagesPastIneligibleFamilies(t *testing.T) {
	snapshot, base, stage, eligible := semanticGroupChoicesTestSetup(t)
	ineligible := eligible
	ineligible.Source.Path = "Observation.unadvertised.coding[]"
	ineligible.SourcePath = "Observation.unadvertised.coding[]"
	ineligible.FamilyID = semantic.FamilyID(ineligible.FamilyReference)
	buildID := semantic.BuildID("project-a", "generation-a")
	var cursors []string
	service := &Service{config: Config{
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			cursors = append(cursors, options.Cursor)
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
				options.ResourceType != "Observation" || options.SelectionMode != semantic.SystemVersionCategory ||
				options.Limit != constructionSemanticGroupChoiceLimit || options.AuthResourcePathsUnrestricted == nil || !*options.AuthResourcePathsUnrestricted {
				t.Fatalf("semantic Group inventory request lost exact snapshot scope: %#v", options)
			}
			page := semantic.Page{
				State: semantic.Complete,
				Build: semantic.Build{BuildID: buildID, State: semantic.Complete},
			}
			switch options.Cursor {
			case "":
				page.Families = []semantic.Family{ineligible}
				page.NextCursor = "next-page"
			case "next-page":
				page.Families = []semantic.Family{eligible}
			default:
				t.Fatalf("unexpected semantic inventory cursor %q", options.Cursor)
			}
			return page, nil
		},
	}}
	base.Snapshot = snapshot
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
		source.SourceKind != semantic.SourceRetained || source.ResourceType != "Observation" ||
		source.SourcePath != "Observation.code.coding[]" || source.System != eligible.System || source.Version != eligible.Version ||
		source.RuleVersion != eligible.RuleVersion {
		t.Fatalf("semantic Group choice identity = %#v, source=%#v, want exact stage/output/family scope", identity, source)
	}
}

func TestConstructionSemanticGroupChoicesRequireDirectRootCodedCapability(t *testing.T) {
	snapshot, base, stage, _ := semanticGroupChoicesTestSetup(t)
	base.Snapshot = snapshot
	calls := 0
	stage.Capabilities = []receiptmodel.ReceiptConstructionOperationChoice{
		{Kind: "GROUP", Supported: true}, {Kind: "CODED_PIVOT", Supported: false},
	}
	service := &Service{config: Config{
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			calls++
			return semantic.Page{}, nil
		},
	}}
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if err != nil || incomplete || len(choices) != 0 || calls != 0 {
		t.Fatalf("semantic Group choices=%#v incomplete=%t calls=%d err=%v, want no lookup when direct-root coded execution is unavailable", choices, incomplete, calls, err)
	}
}

func TestConstructionSemanticGroupChoicesMarksBoundedInventoryIncomplete(t *testing.T) {
	snapshot, base, stage, eligible := semanticGroupChoicesTestSetup(t)
	buildID := semantic.BuildID("project-a", "generation-a")
	calls := 0
	service := &Service{config: Config{
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			wantCursor := ""
			if calls > 0 {
				wantCursor = fmt.Sprintf("page-%d", calls)
			}
			if options.Cursor != wantCursor {
				t.Fatalf("page %d cursor = %q, want %q", calls+1, options.Cursor, wantCursor)
			}
			page := semantic.Page{
				State:      semantic.Complete,
				Build:      semantic.Build{BuildID: buildID, State: semantic.Complete},
				NextCursor: fmt.Sprintf("page-%d", calls+1),
			}
			if calls == 0 {
				page.Families = []semantic.Family{eligible}
			}
			calls++
			return page, nil
		},
	}}
	base.Snapshot = snapshot
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if err != nil || !incomplete || len(choices) != 1 || calls != constructionSemanticGroupChoicePageLimit {
		t.Fatalf("choices=%#v incomplete=%t calls=%d err=%v, want one field, five bounded pages, and explicit incomplete status", choices, incomplete, calls, err)
	}
}

func TestConstructionSemanticGroupChoicesClassifiesInventoryReadFailure(t *testing.T) {
	snapshot, base, stage, _ := semanticGroupChoicesTestSetup(t)
	base.Snapshot = snapshot
	readErr := errors.New("semantic inventory backend offline")
	service := &Service{config: Config{
		SemanticInventory: func(context.Context, semantic.PageOptions) (semantic.Page, error) {
			return semantic.Page{}, readErr
		},
	}}
	choices, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "patients", stage)
	if len(choices) != 0 || incomplete {
		t.Fatalf("choices=%#v incomplete=%t, want no choices from a failed inventory read", choices, incomplete)
	}
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnavailable || lifecycleErr.Code != "CATALOG_UNAVAILABLE" {
		t.Fatalf("inventory read error = %#v, want unavailable CATALOG_UNAVAILABLE", err)
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("inventory read error cause = %v, want original backend error preserved", err)
	}
}

func semanticGroupChoicesTestSetup(t *testing.T) (capability.Snapshot, constructionBase, receiptmodel.ReceiptConstructionStage, semantic.Family) {
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
		semantic.SourceRetained, "Observation", "Observation.code.coding[]", "urn:semantic-group-test", "v1",
	)
	if !semanticGroupFamilyReady(family) {
		entry := semanticInventoryEntryForFamily(family)
		t.Fatalf("test family is not a ready semantic Group family: %#v plan=%#v", family, semanticauthoring.ResolveSemanticSelectionPlan(entry.Observation))
	}
	if path, ok := constructionSemanticGroupCodingPath("Observation", family); !ok || path != "code.coding[]" {
		t.Fatalf("test family coding path = %q, ok=%t", path, ok)
	}
	if candidate, ok := semanticConstructionCandidate(snapshot, semanticInventoryEntryForFamily(family).Observation); !ok || candidate.ID != "observation-code-display" {
		t.Fatalf("test family did not resolve its compiler candidate: %#v ok=%t", candidate, ok)
	}
	base := constructionBase{
		ExplorerID: "builder",
		Snapshot:   snapshot,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		Document: ws.Document{
			RootResourceType: "Observation",
			Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"}, Rows: ws.RecordsRowDefinition(),
		},
	}
	stage := receiptmodel.ReceiptConstructionStage{
		ID: "source_projection", RowIdentityColumn: "_key",
		Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "GROUP", Supported: true}, {Kind: "CODED_PIVOT", Supported: true},
		},
	}
	return snapshot, base, stage, family
}

func TestResolveConstructionCodedPivotChoicesReauthorizesSavedPrimarySource(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "coded-pivot-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	candidate := capability.Candidate{
		ID: "observation-value-text", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.text", Label: "Value text", LogicalType: "string",
		Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	fallbackCandidate := capability.Candidate{
		ID: "observation-value-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.coding[].display", Label: "Value coding display", LogicalType: "string",
		Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst},
	}
	snapshot.Candidates = []capability.Candidate{candidate, fallbackCandidate}
	anchor := codedPivotSemanticEntry("concept-stage-one", "stage-binding", "urn:stage", "stage-one", "Stage I")
	added := codedPivotSemanticEntry("concept-stage-two", "stage-binding", "urn:stage", "stage-two", "Stage II")
	entries := map[string]semantic.Entry{
		anchor.ConceptID: anchor,
		added.ConceptID:  added,
	}
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "explorer-a", "Observation", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choiceCandidate, ok := semanticCandidateWithFallback(snapshot, anchor.Observation, candidate)
	if !ok {
		t.Fatal("current fallback selectors must resolve to exact compiler-proved candidates")
	}
	family := semanticFrameCompatibilityFamily(t, anchor)
	if anchor.Observation.Key.Display != "Stage I" || anchor.Observation.Value.Selector != "valueCodeableConcept.text" {
		t.Fatalf("anchor must preserve the current CodeableConcept text + coding.display choice: %#v", anchor.Observation)
	}

	frameChoice, err := capability.NewSemanticFrameConstructionChoice(snapshot.Token, contextToken, buildID,
		capability.SemanticFrameChoiceSource{
			Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: anchor.ConceptID,
			Family: family, CandidateID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
		}, nil, choiceCandidate)
	if err != nil {
		t.Fatal(err)
	}
	categoryChoice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, added, candidate, false)
	if err != nil {
		t.Fatal(err)
	}

	priorFamily := family
	priorFamily.BindingID = semantic.LegacySemanticBindingIDV1(anchor.Observation)
	priorFamily.SchemaVersion = 3
	priorFamily.RuleVersion = "4"
	priorFamily.ValueFallbacks = nil
	priorFamily.FallbackOnBlankString = false
	priorSource := &ws.ConstructionCodedPivotSource{
		Family: priorFamily, CandidateID: candidate.ID, NodeID: candidate.NodeID,
		FieldPath: candidate.FieldPath, Route: []capability.ConstructionRouteStep{},
	}
	prior := ws.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", Source: priorSource,
		Categories: []ws.ConstructionCodedPivotCategory{{
			System: "urn:stage", Code: "stage-zero", OutputColumnID: "stage-zero",
		}},
		DuplicatePolicy:   ws.ConstructionPivotDuplicateError,
		MissingCellPolicy: ws.ConstructionPivotMissingNull,
	}
	base := constructionBase{
		ExplorerID: "explorer-a",
		Document:   ws.Document{Output: ws.Output{ID: "out-observation"}, RootResourceType: "Observation", Rows: ws.RecordsRowDefinition()},
		Construction: ws.Construction{Steps: []ws.ConstructionStep{{
			ID: "coded-pivot", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCodedPivot, CodedPivot: &prior},
		}}},
		Snapshot: snapshot, Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		Stages: []receiptmodel.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedPivotOp), Supported: true}},
		}},
	}
	service := &Service{config: Config{

		ResolveSemanticInventorySelections: func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || options.AuthResourcePathsUnrestricted == nil || !*options.AuthResourcePathsUnrestricted ||
				len(options.References) != 1 || options.References[0].BindingID != "stage-binding" {
				t.Fatalf("semantic reauthorization scope = %#v", options)
			}
			entry, ok := entries[options.References[0].ConceptID]
			if !ok {
				t.Fatalf("unexpected semantic inventory reference: %#v", options.References[0])
			}
			return semanticAuthoringInventory(snapshot, []semantic.Entry{entry}), nil
		}}}

	candidateConstruction := base.Construction
	candidateConstruction.Steps = append([]ws.ConstructionStep(nil), base.Construction.Steps...)
	candidateStep := &candidateConstruction.Steps[0]
	candidateStep.Operation.CodedPivot = &ws.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", SourceChoiceID: frameChoice.ChoiceID,
		Categories: []ws.ConstructionCodedPivotCategory{
			{System: "urn:stage", Code: "stage-zero", OutputColumnID: "stage-zero"},
			{ChoiceID: categoryChoice.ChoiceID, OutputColumnID: "stage-two"},
		},
		DuplicatePolicy:   ws.ConstructionPivotDuplicateError,
		MissingCellPolicy: ws.ConstructionPivotMissingNull,
	}
	resolved, verified, err := service.resolveConstructionCodedPivotChoices(context.Background(), base, "out-observation", candidateConstruction)
	if err != nil {
		t.Fatalf("reauthorize saved coded Pivot source and editable categories: %v", err)
	}
	if !verified["coded-pivot"] {
		t.Fatalf("coded Pivot step was not marked reauthorized: %#v", verified)
	}
	got := resolved.Steps[0].Operation.CodedPivot
	if got.SourceChoiceID != "" || !reflect.DeepEqual(got.Source, priorSource) {
		t.Fatalf("reauthorized saved primary source changed: got=%#v want=%#v", got.Source, priorSource)
	}
	wantCategories := []ws.ConstructionCodedPivotCategory{
		{System: "urn:stage", Code: "stage-zero", OutputColumnID: "stage-zero"},
		{System: "urn:stage", Code: "stage-two", OutputColumnID: "stage-two"},
	}
	if !reflect.DeepEqual(got.Categories, wantCategories) {
		t.Fatalf("edited coded categories = %#v, want %#v", got.Categories, wantCategories)
	}

	tamperedFamily := family
	tamperedFamily.KeyPath = "code.coding"
	tamperedChoice, err := capability.NewSemanticFrameConstructionChoice(snapshot.Token, contextToken, buildID,
		capability.SemanticFrameChoiceSource{
			Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: anchor.ConceptID,
			Family: tamperedFamily, CandidateID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
		}, nil, choiceCandidate)
	if err != nil {
		t.Fatal(err)
	}
	tampered := candidateConstruction
	tampered.Steps = append([]ws.ConstructionStep(nil), candidateConstruction.Steps...)
	tampered.Steps[0].Operation.CodedPivot = &ws.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", SourceChoiceID: tamperedChoice.ChoiceID,
		Categories:        candidateConstruction.Steps[0].Operation.CodedPivot.Categories,
		DuplicatePolicy:   ws.ConstructionPivotDuplicateError,
		MissingCellPolicy: ws.ConstructionPivotMissingNull,
	}
	_, _, err = service.resolveConstructionCodedPivotChoices(context.Background(), base, "out-observation", tampered)
	assertLifecycleError(t, err, ports.ClassUnprocessable, "INVALID_FRAME_SOURCE")
}

func codedPivotSemanticEntry(conceptID, bindingID, system, code, display string) semantic.Entry {
	observation := semanticFrameCompatibilityObservation("codeable concept")
	observation.Key.System = system
	observation.Key.Code = code
	observation.Key.Display = display
	return semantic.Entry{
		ConceptID: conceptID, BindingID: bindingID,
		Observation: observation,
	}
}

func TestImportedEmptyRootSkipsSourceChoicesButKeepsStageOperations(t *testing.T) {
	document := ws.Document{
		RootResourceType: "MedicationAdministration",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "MedicationAdministration"},
		ImportedRecipe:   &ws.ImportedRecipeRef{Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "MedicationAdministration"}, Rows: ws.RecordsRowDefinition(),
	}
	base := constructionBase{Document: document, Snapshot: capability.Snapshot{Nodes: []capability.Node{}}}
	stage := receiptmodel.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "GROUP", Supported: true}, {Kind: "PIVOT", Supported: true},
			{Kind: "DERIVE", Supported: true}, {Kind: "FILTER", Supported: true},
			{Kind: "UNPIVOT", Supported: true}, {Kind: "EXPAND", Supported: true},
			{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true},
		},
		GroupFieldSourceContext: &receiptmodel.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "imported-source-shape",
			SourceIdentityColumn: "_key", FieldOwners: []receiptmodel.ReceiptConstructionGroupFieldOwner{{
				Role: "ROOT", OccurrenceID: ws.RootOccurrenceID,
				OwnerResourceType: "MedicationAdministration", ResourceType: "MedicationAdministration",
			}},
		},
	}
	ordinaryOperations := append([]receiptmodel.ReceiptConstructionOperationChoice(nil), stage.Capabilities...)
	service := &Service{}
	coded, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage)
	if err != nil {
		t.Fatalf("coded-group choices on empty imported root: %v", err)
	}
	semantic, incomplete, err := service.constructionSemanticGroupChoices(context.Background(), base, "MedicationAdministration", stage)
	if err != nil {
		t.Fatalf("semantic group choices on empty imported root: %v", err)
	}
	group, groupIncomplete, err := service.constructionGroupFieldChoices(context.Background(), base, stage, semantic, incomplete, "", "")
	if err != nil {
		t.Fatalf("Group field choices on empty imported root: %v", err)
	}
	if len(coded) != 0 || len(semantic) != 0 || len(group) != 0 || groupIncomplete || groupIncomplete != incomplete {
		t.Fatalf("empty imported root returned invented source choices: coded=%#v semantic=%#v group=%#v incomplete=(%t,%t)", coded, semantic, group, incomplete, groupIncomplete)
	}
	if len(stage.Capabilities) != len(ordinaryOperations) {
		t.Fatalf("source-choice fallback changed the stage operation count: got %#v, want %#v", stage.Capabilities, ordinaryOperations)
	}
	for _, want := range ordinaryOperations {
		found := false
		for _, got := range stage.Capabilities {
			if got.Kind == want.Kind && got.Supported == want.Supported {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ordinary stage operation %q was removed from %#v", want.Kind, stage.Capabilities)
		}
	}
}

func TestGenericEmptyRootStillFailsClosedForCodedGroupChoices(t *testing.T) {
	base := constructionBase{
		Document: ws.Document{RootResourceType: "MedicationAdministration", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "MedicationAdministration"}, Rows: ws.RecordsRowDefinition()},
		Snapshot: capability.Snapshot{Nodes: []capability.Node{}},
	}
	stage := receiptmodel.ReceiptConstructionStage{ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key", Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true}}}
	if _, err := constructionCodedGroupChoices(base, "MedicationAdministration", stage); err == nil {
		t.Fatal("generic missing root should remain a hard coded-group route failure")
	}
}

type importedSourceChoicePlanner struct{ calls int }

func (planner *importedSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	planner.calls++
	return nil, errors.New("generic FHIR route planner must not run for imported source choices")
}

func TestImportedSourceGroupAndPivotCapabilitiesSkipGenericRowChoicePlanner(t *testing.T) {
	planner := &importedSourceChoicePlanner{}
	service := &Service{config: Config{RowChoicePlanner: planner}}
	base := constructionBase{Document: ws.Document{ImportedRecipe: &ws.ImportedRecipeRef{
		Name: "seed", TranslationVersion: "v1", Digest: "0000000000000000000000000000000000000000000000000000000000000000", Output: "DocumentReference",
	}, Rows: ws.RecordsRowDefinition(),
	}}
	group, err := service.constructionSources().constructionGroupSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
	if err != nil || !group.Supported || group.ReasonCode != "" || len(group.Choices) != 0 {
		t.Fatalf("imported group capability = %#v, %v", group, err)
	}
	pivot, err := service.constructionSources().constructionPivotSourceCapability(context.Background(), base, "DocumentReference", recipe.ConstructionSourceProjectionID)
	if err != nil || !pivot.Supported || pivot.ReasonCode != "" || len(pivot.Choices) != 0 {
		t.Fatalf("imported pivot capability = %#v, %v", pivot, err)
	}
	if planner.calls != 0 {
		t.Fatalf("generic row-choice planner calls = %d, want 0", planner.calls)
	}
}
