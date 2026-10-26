package lifecycle

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestStandaloneCodedConceptChoiceResolvesSameCodingValueAndStaysOutOfFrames(t *testing.T) {
	const (
		project    = "project-a"
		generation = "generation-a"
		explorerID = "conditions"
	)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(project, generation, "snapshot-a", scope)
	snapshot.Nodes = []capability.Node{{ID: "condition", ResourceType: "Condition", RowRootEligible: true}}
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Condition", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:example:condition", Version: "2024", Code: "C1", Display: "Condition one"},
		Value: catalog.SemanticObservationValue{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", Completeness: catalog.SemanticComplete, Status: "SUPPORTED",
		RuleHint: catalog.SemanticRuleHintCodedConceptV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	entry := catalog.SemanticInventoryEntry{ConceptID: "concept-c1", BindingID: "binding-condition-code", Observation: observation}
	displayCandidate := capability.Candidate{
		ID: "condition-code-display", NodeID: "condition", ResourceType: "Condition",
		FieldPath: "code.coding[].display", Label: "Condition code display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 2}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	codeCandidate := capability.Candidate{
		ID: "condition-code-value", NodeID: "condition", ResourceType: "Condition",
		FieldPath: "code.coding[].code", Label: "Condition code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 2}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	snapshot.Candidates = []capability.Candidate{displayCandidate, codeCandidate}

	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
		t.Fatalf("standalone Coding plan = %#v", plan)
	}
	binding := plan.Source.Lookup.Binding
	if binding.ValueScope != "CODING" || binding.VersionPath != "version" || binding.OwnerPath != "" || binding.KeyPath != "code.coding[]" ||
		binding.ValuePath != "display" || !reflect.DeepEqual(binding.ValueFallback, []string{"code"}) ||
		!binding.FallbackOnBlankString || plan.Source.Lookup.Key == nil || plan.Source.Lookup.Key.System != observation.Key.System ||
		plan.Source.Lookup.Key.Code != observation.Key.Code || plan.Source.Lookup.Key.Version != observation.Key.Version {
		t.Fatalf("standalone Coding plan lost same-item identity/value scope: %#v", plan.Source)
	}

	buildID := catalog.SemanticInventoryBuildID(project, generation)
	contextToken, err := semanticInventoryContextToken(snapshot, explorerID, "Condition", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, displayCandidate, true)
	if err != nil {
		t.Fatal(err)
	}
	choiceSource, ok := choice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || choiceSource.RuleHint != catalog.SemanticRuleHintCodedConceptV1 || choiceSource.ValueSelector != "code.coding[].display" {
		t.Fatalf("standalone Coding choice source = %#v", choice.Source)
	}
	for _, option := range choice.Options {
		if option.Form == capability.ConstructionChoiceOwnerRecords {
			t.Fatalf("standalone Coding choice must not expose enclosing-owner records: %#v", choice.Options)
		}
	}
	if families := frameSourceFamilies(snapshot, []catalog.SemanticInventoryEntry{entry}); len(families) != 0 {
		t.Fatalf("standalone Coding unexpectedly became a paired coded-Pivot frame: %#v", families)
	}
	if _, _, ok := semanticEntryForFrameFamily(entry, capability.SemanticFrameFamily{}); ok {
		t.Fatal("standalone Coding was accepted as an owner-scoped paired code/value frame")
	}

	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil,
		semanticCandidateWithFallbackMust(t, snapshot, observation, displayCandidate))
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []authoringv2.CatalogCandidate{{
		ID: displayCandidate.ID, NodeID: displayCandidate.NodeID, FieldPath: displayCandidate.FieldPath,
		Cardinality: displayCandidate.Cardinality, ConstructionChoice: &fieldChoice,
	}}}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
		explorerID, "Condition", contextToken, buildID, buildID,
		map[string]catalog.SemanticInventoryEntry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry}, nil,
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceAll},
		identity, choiceSource,
	)
	if err != nil {
		t.Fatalf("resolve compiler-issued standalone Coding choice: %v", err)
	}
	if resolved.Source.Lookup == nil || resolved.Source.Lookup.Binding == nil || resolved.Source.Lookup.Binding.ValueScope != "CODING" ||
		resolved.Source.Lookup.Binding.VersionPath != "version" || resolved.Source.Lookup.Key == nil || resolved.Source.Lookup.Key.Version != "2024" ||
		resolved.Source.Lookup.Binding.KeyPath != binding.KeyPath || resolved.Source.Lookup.Binding.ValuePath != binding.ValuePath ||
		!reflect.DeepEqual(resolved.Source.Lookup.Binding.ValueFallback, binding.ValueFallback) || resolved.Source.Lookup.ProjectionMode != string(capability.ConstructionChoiceAll) {
		t.Fatalf("resolved standalone Coding source = %#v", resolved.Source)
	}
}

func TestStandaloneCodedConceptSelectionKeepsUnversionedIdentityExplicit(t *testing.T) {
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "meta.security[]"},
		Key:           catalog.SemanticObservationKey{Selector: "meta.security[]", System: "urn:example:security", Code: "S1"},
		Value: catalog.SemanticObservationValue{
			Selector: "meta.security[].display", Type: "string",
			FallbackSelectors: []string{"meta.security[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", Completeness: catalog.SemanticComplete, Status: "SUPPORTED",
		RuleHint: catalog.SemanticRuleHintCodedConceptV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
		t.Fatalf("unversioned standalone Coding plan = %#v", plan)
	}
	if plan.Source.Lookup.Binding.VersionPath != "version" || plan.Source.Lookup.Key == nil || plan.Source.Lookup.Key.Version != "" {
		t.Fatalf("unversioned Coding must explicitly match only missing/null version, plan = %#v", plan.Source.Lookup)
	}
}

func TestDirectCodingObservationIssuesAndConsumesExactCompilerChoice(t *testing.T) {
	cases := []struct {
		name, resourceType, project, explorerID                                              string
		ownerPath, sourcePath, keySelector, keyPath                                          string
		valueSelector, valueFieldPath, fallbackSelector, fallbackFieldPath, repeatedBoundary string
		payload                                                                              map[string]any
	}{
		{
			name: "direct Coding array", resourceType: "Observation", project: "project-direct-coding", explorerID: "observations",
			sourcePath: "meta.security[]", keySelector: "meta.security[]", keyPath: "meta.security[]",
			valueSelector: "meta.security[].display", valueFieldPath: "meta.security[].display",
			fallbackSelector: "meta.security[].code", fallbackFieldPath: "meta.security[].code", repeatedBoundary: "meta.security[]",
			payload: map[string]any{"resourceType": "Observation", "meta": map[string]any{"security": []any{map[string]any{
				"system": "urn:example:security", "version": "v3", "code": "restricted", "display": "Restricted",
			}}}},
		},
		{
			name: "scalar Coding inside repeated Task input", resourceType: "Task", project: "project-task-coding", explorerID: "tasks",
			ownerPath: "input[]", sourcePath: "input[].valueCoding", keySelector: "valueCoding", keyPath: "input[].valueCoding",
			valueSelector: "valueCoding.display", valueFieldPath: "input[].valueCoding.display",
			fallbackSelector: "valueCoding.code", fallbackFieldPath: "input[].valueCoding.code", repeatedBoundary: "input[]",
			payload: map[string]any{"resourceType": "Task", "input": []any{map[string]any{"valueCoding": map[string]any{
				"system": "urn:example:task-input", "version": "v7", "code": "performed", "display": "Performed",
			}}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertDirectCodingChoiceRoundTrip(t, tc) })
	}
}

func assertDirectCodingChoiceRoundTrip(t *testing.T, tc struct {
	name, resourceType, project, explorerID                                              string
	ownerPath, sourcePath, keySelector, keyPath                                          string
	valueSelector, valueFieldPath, fallbackSelector, fallbackFieldPath, repeatedBoundary string
	payload                                                                              map[string]any
}) {
	t.Helper()
	const generation = "generation-a"
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot(tc.project, generation, "snapshot-"+tc.resourceType, scope)
	snapshot.Nodes = []capability.Node{{ID: "root", ResourceType: tc.resourceType, RowRootEligible: true}}
	displayCandidate := capability.Candidate{
		ID: "coded-display", NodeID: "root", ResourceType: tc.resourceType,
		FieldPath: tc.valueFieldPath, Label: "Coded display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: tc.repeatedBoundary, MaxItems: 2}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	codeCandidate := capability.Candidate{
		ID: "coded-code", NodeID: "root", ResourceType: tc.resourceType,
		FieldPath: tc.fallbackFieldPath, Label: "Coded value", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: tc.repeatedBoundary, MaxItems: 2}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	snapshot.Candidates = []capability.Candidate{displayCandidate, codeCandidate}

	var entry catalog.SemanticInventoryEntry
	emitter := catalog.NewSemanticInventoryEmitter(tc.project, generation)
	emitter.ObservePayload(tc.payload, tc.resourceType, "scope", "resource-1", func(contribution catalog.SemanticInventoryContribution) {
		if contribution.Observation.RuleHint == catalog.SemanticRuleHintCodedConceptV1 {
			entry = catalog.SemanticInventoryEntry{ConceptID: contribution.ConceptID, BindingID: contribution.BindingID, Observation: contribution.Observation}
		}
	})
	if entry.ConceptID == "" || entry.BindingID == "" {
		t.Fatal("Coding emitter produced no standalone semantic entry")
	}
	plan := authoringv2.ResolveSemanticSelectionPlan(entry.Observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil ||
		plan.Source.Lookup.Binding.OwnerPath != tc.ownerPath || plan.Source.Lookup.Binding.KeyPath != tc.keyPath ||
		plan.Source.Lookup.Binding.VersionPath != "version" || plan.Source.Lookup.Binding.ValuePath != "display" ||
		!reflect.DeepEqual(plan.Source.Lookup.Binding.ValueFallback, []string{"code"}) ||
		plan.Source.Lookup.Key.System != entry.Observation.Key.System || plan.Source.Lookup.Key.Code != entry.Observation.Key.Code ||
		plan.Source.Lookup.Key.Version != entry.Observation.Key.Version {
		t.Fatalf("Coding selection plan = %#v", plan)
	}
	if entry.Observation.Source.Path != tc.sourcePath || entry.Observation.Key.Selector != tc.keySelector ||
		entry.Observation.Value.Selector != tc.valueSelector || entry.Observation.Value.FallbackSelectors[0] != tc.fallbackSelector {
		t.Fatalf("Coding observation paths = %#v", entry.Observation)
	}

	buildID := catalog.SemanticInventoryBuildID(tc.project, generation)
	contextToken, err := semanticInventoryContextToken(snapshot, tc.explorerID, tc.resourceType, buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, displayCandidate, true)
	if err != nil {
		t.Fatal(err)
	}
	choiceSource, ok := choice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || choiceSource.RuleHint != catalog.SemanticRuleHintCodedConceptV1 || choiceSource.ValueSelector != tc.valueFieldPath || choiceSource.Version != entry.Observation.Key.Version {
		t.Fatalf("Coding choice source = %#v", choice.Source)
	}
	for _, option := range choice.Options {
		if option.Form == capability.ConstructionChoiceOwnerRecords {
			t.Fatalf("Coding choice exposed owner records: %#v", choice.Options)
		}
	}

	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, displayCandidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []authoringv2.CatalogCandidate{{
		ID: displayCandidate.ID, NodeID: displayCandidate.NodeID, FieldPath: displayCandidate.FieldPath,
		Cardinality: displayCandidate.Cardinality, ConstructionChoice: &fieldChoice,
	}}}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
		tc.explorerID, tc.resourceType, contextToken, buildID, buildID,
		map[string]catalog.SemanticInventoryEntry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry}, nil,
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceAll},
		identity, choiceSource,
	)
	if err != nil {
		t.Fatalf("resolve Coding compiler choice: %v", err)
	}
	if resolved.Source.Lookup == nil || resolved.Source.Lookup.Binding == nil || resolved.Source.Lookup.Key == nil ||
		resolved.Source.Lookup.Binding.ValueScope != "CODING" || resolved.Source.Lookup.Binding.OwnerPath != tc.ownerPath ||
		resolved.Source.Lookup.Binding.KeyPath != tc.keyPath || resolved.Source.Lookup.Binding.VersionPath != "version" ||
		resolved.Source.Lookup.Key.Code != entry.Observation.Key.Code || resolved.Source.Lookup.Key.Version != entry.Observation.Key.Version {
		t.Fatalf("resolved Coding source = %#v", resolved.Source)
	}
}

func semanticCandidateWithFallbackMust(t *testing.T, snapshot capability.Snapshot, observation catalog.SemanticObservation, primary capability.Candidate) capability.Candidate {
	t.Helper()
	candidate, ok := semanticCandidateWithFallback(snapshot, observation, primary)
	if !ok {
		t.Fatal("standalone Coding display/code fallback did not resolve to compiler candidates")
	}
	return candidate
}
