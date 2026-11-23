package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
)

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
	priorSource := &authoringv2.ConstructionCodedPivotSource{
		Family: priorFamily, CandidateID: candidate.ID, NodeID: candidate.NodeID,
		FieldPath: candidate.FieldPath, Route: []capability.ConstructionRouteStep{},
	}
	prior := authoringv2.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", Source: priorSource,
		Categories: []authoringv2.ConstructionCodedPivotCategory{{
			System: "urn:stage", Code: "stage-zero", OutputColumnID: "stage-zero",
		}},
		DuplicatePolicy:   authoringv2.ConstructionPivotDuplicateError,
		MissingCellPolicy: authoringv2.ConstructionPivotMissingNull,
	}
	base := constructionBase{
		ExplorerID: "explorer-a",
		Document:   authoringv2.Document{Output: authoringv2.Output{ID: "out-observation"}, RootResourceType: "Observation", Rows: authoringv2.RecordsRowDefinition()},
		Construction: authoringv2.Construction{Steps: []authoringv2.ConstructionStep{{
			ID: "coded-pivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCodedPivot, CodedPivot: &prior},
		}}},
		Snapshot: snapshot, Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		Stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Capabilities: []explorer.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedPivotOp), Supported: true}},
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
	candidateConstruction.Steps = append([]authoringv2.ConstructionStep(nil), base.Construction.Steps...)
	candidateStep := &candidateConstruction.Steps[0]
	candidateStep.Operation.CodedPivot = &authoringv2.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", SourceChoiceID: frameChoice.ChoiceID,
		Categories: []authoringv2.ConstructionCodedPivotCategory{
			{System: "urn:stage", Code: "stage-zero", OutputColumnID: "stage-zero"},
			{ChoiceID: categoryChoice.ChoiceID, OutputColumnID: "stage-two"},
		},
		DuplicatePolicy:   authoringv2.ConstructionPivotDuplicateError,
		MissingCellPolicy: authoringv2.ConstructionPivotMissingNull,
	}
	resolved, verified, err := service.syncConstructionService().ResolveConstructionCodedPivotChoices(context.Background(), base, "out-observation", candidateConstruction)
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
	wantCategories := []authoringv2.ConstructionCodedPivotCategory{
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
	tampered.Steps = append([]authoringv2.ConstructionStep(nil), candidateConstruction.Steps...)
	tampered.Steps[0].Operation.CodedPivot = &authoringv2.ConstructionCodedPivot{
		ConstructionID: "coded-pivot", SourceChoiceID: tamperedChoice.ChoiceID,
		Categories:        candidateConstruction.Steps[0].Operation.CodedPivot.Categories,
		DuplicatePolicy:   authoringv2.ConstructionPivotDuplicateError,
		MissingCellPolicy: authoringv2.ConstructionPivotMissingNull,
	}
	_, _, err = service.syncConstructionService().ResolveConstructionCodedPivotChoices(context.Background(), base, "out-observation", tampered)
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
