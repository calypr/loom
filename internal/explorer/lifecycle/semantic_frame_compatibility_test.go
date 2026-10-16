package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestSemanticEntryReauthorizesPriorPrimaryOnlyFrameFamilies(t *testing.T) {
	for _, kind := range []string{"quantity", "codeable concept"} {
		t.Run(kind, func(t *testing.T) {
			observation := semanticFrameCompatibilityObservation(kind)
			currentEntry := catalog.SemanticInventoryEntry{ConceptID: "concept", BindingID: "current-binding", Observation: observation}
			currentFamily := semanticFrameCompatibilityFamily(t, currentEntry)
			priorFamily := currentFamily
			priorFamily.BindingID = catalog.LegacySemanticBindingIDV1(observation)
			priorFamily.SchemaVersion = catalog.SemanticObservationSchemaVersion - 1
			priorFamily.RuleVersion = strconv.Itoa(catalog.SemanticObservationRuleVersion - 1)
			priorFamily.ValueFallbacks = nil
			priorFamily.FallbackOnBlankString = false

			projected, plan, ok := semanticEntryForFrameFamily(currentEntry, priorFamily)
			if !ok || !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
				t.Fatalf("prior frame did not reauthorize against the exact current inventory: ok=%v plan=%#v family=%#v", ok, plan, priorFamily)
			}
			binding := plan.Source.Lookup.Binding
			if binding.ValuePath != priorFamily.ValuePath || len(binding.ValueFallback) != 0 || binding.FallbackOnBlankString ||
				projected.Observation.Value.Selector != observation.Value.Selector || len(projected.Observation.Value.FallbackSelectors) != 0 || projected.Observation.Value.FallbackOnBlankString {
				t.Fatalf("legacy frame did not retain its primary-only binding: binding=%#v projected=%#v", binding, projected.Observation.Value)
			}
			if len(observation.Value.FallbackSelectors) > 0 && !observation.Value.FallbackOnBlankString {
				t.Fatalf("test fixture does not exercise the new opted-in fallback: %#v", observation.Value)
			}

			if !semanticFrameFamilyMatchesPinnedPrior(currentFamily, priorFamily, observation) {
				t.Fatal("current family was not recognized as a metadata-only upgrade from the pinned primary-only family")
			}

			for _, test := range []struct {
				name   string
				mutate func(*capability.SemanticFrameFamily)
			}{
				{name: "tampered legacy binding id", mutate: func(f *capability.SemanticFrameFamily) { f.BindingID = "not-the-v1-binding" }},
				{name: "legacy fallback injection", mutate: func(f *capability.SemanticFrameFamily) {
					f.ValueFallbacks = []string{"valueCodeableConcept.coding[].code"}
				}},
				{name: "legacy blank policy injection", mutate: func(f *capability.SemanticFrameFamily) { f.FallbackOnBlankString = true }},
				{name: "wrong owner", mutate: func(f *capability.SemanticFrameFamily) { f.OwningScope = "component[]" }},
				{name: "wrong key", mutate: func(f *capability.SemanticFrameFamily) { f.KeyPath = "other.coding[]" }},
				{name: "wrong value", mutate: func(f *capability.SemanticFrameFamily) { f.ValuePath += ".changed" }},
				{name: "wrong logical type", mutate: func(f *capability.SemanticFrameFamily) { f.LogicalType = "integer" }},
				{name: "wrong choice arm", mutate: func(f *capability.SemanticFrameFamily) { f.ChoiceArms = []string{"valueString"} }},
				{name: "wrong source path", mutate: func(f *capability.SemanticFrameFamily) { f.SourcePath = "other" }},
				{name: "schema only changed", mutate: func(f *capability.SemanticFrameFamily) {
					f.RuleVersion = strconv.Itoa(catalog.SemanticObservationRuleVersion)
				}},
				{name: "unknown old versions", mutate: func(f *capability.SemanticFrameFamily) { f.SchemaVersion--; f.RuleVersion = "1" }},
			} {
				t.Run(test.name, func(t *testing.T) {
					changed := priorFamily
					test.mutate(&changed)
					if _, _, accepted := semanticEntryForFrameFamily(currentEntry, changed); accepted {
						t.Fatalf("accepted tampered or unknown legacy frame identity: %#v", changed)
					}
					if semanticFrameFamilyMatchesPinnedPrior(currentFamily, changed, observation) {
						t.Fatalf("accepted tampered pinned identity: %#v", changed)
					}
				})
			}

			changedObservation := observation
			changedObservation.Key.Version = "2024"
			changedEntry := currentEntry
			changedEntry.Observation = changedObservation
			if _, _, accepted := semanticEntryForFrameFamily(changedEntry, priorFamily); accepted {
				t.Fatal("accepted a version-specific code for a prior versionless frame")
			}

			fresh, freshPlan, freshOK := semanticEntryForFrameFamily(currentEntry, currentFamily)
			if !freshOK || !freshPlan.Readiness.Addable() || !reflect.DeepEqual(fresh.Observation.Value, observation.Value) {
				t.Fatalf("current frame did not keep current fallback semantics: ok=%v plan=%#v value=%#v", freshOK, freshPlan, fresh.Observation.Value)
			}
		})
	}
}

func TestLegacyCodeableConceptFrameCategoryEditKeepsPrimaryOnlyBinding(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "legacy-frame-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	observation := semanticFrameCompatibilityObservation("codeable concept")
	entry := catalog.SemanticInventoryEntry{ConceptID: "stage-concept", BindingID: "current-binding", Observation: observation}
	currentFamily := semanticFrameCompatibilityFamily(t, entry)
	priorFamily := currentFamily
	priorFamily.BindingID = catalog.LegacySemanticBindingIDV1(observation)
	priorFamily.SchemaVersion = catalog.SemanticObservationSchemaVersion - 1
	priorFamily.RuleVersion = strconv.Itoa(catalog.SemanticObservationRuleVersion - 1)
	priorFamily.ValueFallbacks = nil
	priorFamily.FallbackOnBlankString = false
	frame, err := authoringv2.NewFrameDefinition(
		"Stage values", "Stages selected from the existing primary text binding.", priorFamily, nil, capability.ConstructionChoiceValue,
	)
	if err != nil {
		t.Fatal(err)
	}

	store := legacyFrameCompatibilityStore(t, frame)
	primary := capability.Candidate{
		ID: "observation-stage-text", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.text", Label: "Stage text", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	fallback := capability.Candidate{
		ID: "observation-stage-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.coding[].display", Label: "Stage display", LogicalType: "string", Cardinality: "many",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst},
	}
	snapshot.Candidates = []capability.Candidate{primary, fallback}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, primary)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "explorer",
		SourceGeneration: "generation-a", AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, Nodes: []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: primary.ID, NodeID: primary.NodeID, FieldPath: primary.FieldPath, Label: primary.Label, LogicalType: primary.LogicalType,
			Cardinality: primary.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}}, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
	}
	buildID := catalog.SemanticInventoryBuildID("project-a", "generation-a")
	unrelated := entry
	unrelated.ConceptID = "unrelated-concept"
	unrelated.BindingID = "unrelated-current-binding"
	unrelated.Observation.Source.Path = "anotherValue"
	unrelated.Observation.Source.Canonical = "Observation.anotherValue"
	resolver := func(_ context.Context, options catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
		if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
			options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) ||
			!reflect.DeepEqual(options.References, []catalog.SemanticInventoryReference{{ConceptID: entry.ConceptID, BindingID: entry.BindingID}}) {
			t.Fatalf("category reauthorization changed project/generation/auth/selection scope: %#v", options)
		}
		return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{entry}), nil
	}
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
				options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
				!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) || options.ResourceType != "Observation" || options.BindingID != "" {
				t.Fatalf("frame browse lost its exact project/generation/auth/binding scope: %#v", options)
			}
			return catalog.SemanticInventoryPage{
				State:   catalog.SemanticInventoryComplete,
				Build:   catalog.SemanticInventoryBuild{BuildID: buildID, State: catalog.SemanticInventoryComplete},
				Entries: []catalog.SemanticInventoryEntry{entry, unrelated},
			}, nil
		},
		ResolveSemanticInventorySelections: resolver,
	})

	browsed, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation",
		OutputID: "out", FrameID: frame.ID,
	})
	if err != nil || browsed.FrameID != frame.ID || len(browsed.Entries) != 1 || browsed.Entries[0].ConstructionChoice == nil {
		t.Fatalf("legacy category browse = %#v, err=%v", browsed, err)
	}
	choiceSource, ok := browsed.Entries[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || browsed.Entries[0].ValueSelector != observation.Value.Selector || choiceSource.ValueSelector != observation.Value.Selector ||
		choiceSource.SchemaVersion != catalog.SemanticObservationSchemaVersion || choiceSource.RuleVersion != strconv.Itoa(catalog.SemanticObservationRuleVersion) ||
		len(choiceSource.ValueFallbacks) != 0 || choiceSource.FallbackOnBlankString {
		t.Fatalf("legacy category choice acquired current fallback behavior: item=%#v source=%#v", browsed.Entries[0], browsed.Entries[0].ConstructionChoice.Source)
	}

	request := authoringv2.ApplyCommandsRequest{
		CommandID: "legacy-frame-category", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{
			Type: authoringv2.CommandApplyConstructionChoice, OutputID: "out", InitialPresentation: authoringv2.InitialPresentationTable,
			ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: browsed.Entries[0].ConstructionChoice.ChoiceID, Form: capability.ConstructionChoiceValue, FrameID: frame.ID},
		}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "explorer", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Workspace.Documents) != 1 || len(response.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("legacy category edit produced workspace=%#v", response.Workspace)
	}
	document := response.Workspace.Documents[0]
	column := document.Columns[0]
	if column.FrameID != frame.ID || column.Source.Kind != authoringv2.SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil ||
		column.Source.Lookup.Binding.ValuePath != priorFamily.ValuePath || len(column.Source.Lookup.Binding.ValueFallback) != 0 || column.Source.Lookup.Binding.FallbackOnBlankString ||
		len(document.Frames) != 1 || document.Frames[0].ID != frame.ID || !reflect.DeepEqual(document.Frames[0].Source, priorFamily) {
		t.Fatalf("saved category changed the primary-only frame identity: frame=%#v column=%#v", document.Frames, column)
	}
}

func TestLegacyPrimaryOnlyFrameCompatibilityDoesNotChangeExtensionBindings(t *testing.T) {
	lookup := &authoringv2.LookupSource{ProjectionMode: "FIRST", Extension: &fhirschema.ExtensionBinding{
		OwnerPath: "extension[]", URLPath: []string{"urn:study:stage"}, ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
	}}
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"}, Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{Column: "stage", Label: "Stage", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{
			Kind: authoringv2.SourceExtensionByURL, Lookup: lookup,
		}}},
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder"}, Documents: []authoringv2.Document{document},
		Tabs: []authoringv2.Tab{{ID: "tab-out", Title: "Output", OutputID: "out", Visible: true}},
	}
	beforeSource, err := json.Marshal(document.Columns[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := authoringv2.DecodeWorkspace(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Documents) != 1 || len(reloaded.Documents[0].Columns) != 1 {
		t.Fatalf("existing Extension binding did not reload: %#v", reloaded.Documents)
	}
	afterSource, err := json.Marshal(reloaded.Documents[0].Columns[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterSource, beforeSource) {
		t.Fatalf("existing Extension binding wire identity changed across canonical reload: before=%s after=%s", beforeSource, afterSource)
	}
}

func TestCodedPivotSourcePreservesPriorPrimaryOnlyFamily(t *testing.T) {
	observation := semanticFrameCompatibilityObservation("codeable concept")
	entry := catalog.SemanticInventoryEntry{ConceptID: "stage", BindingID: "current-binding", Observation: observation}
	currentFamily := semanticFrameCompatibilityFamily(t, entry)
	priorFamily := currentFamily
	priorFamily.BindingID = catalog.LegacySemanticBindingIDV1(observation)
	priorFamily.SchemaVersion = catalog.SemanticObservationLegacyV1SchemaVersion
	priorFamily.RuleVersion = strconv.Itoa(catalog.SemanticObservationLegacyV1RuleVersion)
	priorFamily.ValueFallbacks = nil
	priorFamily.FallbackOnBlankString = false
	route := []capability.ConstructionRouteStep{{
		EdgeID: "patient-observations", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject", MatchMode: "optional",
	}}
	current := &authoringv2.ConstructionCodedPivotSource{
		Family: currentFamily, CandidateID: "candidate-stage", NodeID: "observation", FieldPath: "valueCodeableConcept.text", Route: route,
	}
	pinned := &authoringv2.ConstructionCodedPivotSource{
		Family: priorFamily, CandidateID: current.CandidateID, NodeID: current.NodeID, FieldPath: current.FieldPath, Route: append([]capability.ConstructionRouteStep(nil), route...),
	}
	if !codedPivotSourceMatchesPinnedPrior(current, pinned, observation) {
		t.Fatal("current catalog reauthorization did not preserve the exact primary-only saved Pivot family")
	}

	mutations := []struct {
		name   string
		mutate func(*authoringv2.ConstructionCodedPivotSource)
	}{
		{name: "route", mutate: func(source *authoringv2.ConstructionCodedPivotSource) { source.Route[0].Relationship = "encounter" }},
		{name: "candidate", mutate: func(source *authoringv2.ConstructionCodedPivotSource) { source.CandidateID = "other-candidate" }},
		{name: "field path", mutate: func(source *authoringv2.ConstructionCodedPivotSource) { source.FieldPath = "valueQuantity.value" }},
		{name: "legacy fallback", mutate: func(source *authoringv2.ConstructionCodedPivotSource) {
			source.Family.ValueFallbacks = []string{"valueCodeableConcept.coding[].display"}
		}},
		{name: "legacy binding id", mutate: func(source *authoringv2.ConstructionCodedPivotSource) { source.Family.BindingID = "tampered" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			changed := *pinned
			changed.Route = append([]capability.ConstructionRouteStep(nil), pinned.Route...)
			changed.Family.ValueFallbacks = append([]string(nil), pinned.Family.ValueFallbacks...)
			test.mutate(&changed)
			if codedPivotSourceMatchesPinnedPrior(current, &changed, observation) {
				t.Fatalf("accepted changed saved Pivot identity: %#v", changed)
			}
		})
	}
}

func semanticFrameCompatibilityObservation(kind string) catalog.SemanticObservation {
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Canonical: "Observation.code", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
		Value: catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
	}
	if kind == "codeable concept" {
		observation.Value = catalog.SemanticObservationValue{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		}
		observation.ChoiceArm = "valueCodeableConcept"
		observation.LogicalType = "string"
	}
	return observation
}

func semanticFrameCompatibilityFamily(t *testing.T, entry catalog.SemanticInventoryEntry) capability.SemanticFrameFamily {
	t.Helper()
	plan := authoringv2.ResolveSemanticSelectionPlan(entry.Observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
		t.Fatalf("semantic compatibility fixture is not compiler-addable: %#v", plan)
	}
	binding := plan.Source.Lookup.Binding
	return capability.SemanticFrameFamily{
		BindingID: entry.BindingID, ResourceType: entry.Observation.Source.Type,
		SourcePath: entry.Observation.Source.Path, SourceCanonical: entry.Observation.Source.Canonical, SourceProfile: entry.Observation.Source.Profile,
		OwningScope: binding.OwnerPath, KeyPath: binding.KeyPath, ValuePath: binding.ValuePath,
		ValueFallbacks: append([]string(nil), binding.ValueFallback...), FallbackOnBlankString: binding.FallbackOnBlankString,
		ChoiceArms: append([]string(nil), binding.ChoiceArms...), LogicalType: binding.LogicalType,
		RuleVersion: entry.Observation.RuleVersion, SchemaVersion: entry.Observation.SchemaVersion,
	}
}

func legacyFrameCompatibilityStore(t *testing.T, frame authoringv2.FrameDefinition) *fakeStore {
	t.Helper()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Observations"}, RootResourceType: "Observation",
			Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
			Rows:  authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{}, Frames: []authoringv2.FrameDefinition{frame},
		}},
		Tabs: []authoringv2.Tab{{ID: "tab-out", Title: "Observations", OutputID: "out", Visible: true}},
	}
	config, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "explorer", Title: "Builder", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: config, DraftVersion: 1, DraftDigest: digest,
	}}
}
