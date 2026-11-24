package semantic

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestWalkMembersWithResourceTypesKeepsContainedOwnersAndPaths(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	payload := []byte(`{"contained":[
		{"resourceType":"Observation","status":"final","code":{"coding":[{"code":"same"}]}},
		{"resourceType":"Observation","status":"final","code":{"coding":[{"code":"same"}]}}
	]}`)

	facts, diagnostics := collectResourceFacts(t, index, "Patient", payload, "Observation")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	first := findMemberFact(t, facts, "contained[].code.coding[].code", "contained[0].code.coding[0].code", false)
	second := findMemberFact(t, facts, "contained[].code.coding[].code", "contained[1].code.coding[0].code", false)
	for _, fact := range []MemberFact{first, second} {
		if fact.RootDefinition != "Patient" {
			t.Errorf("RootDefinition = %q, want Patient", fact.RootDefinition)
		}
		if fact.OwnerPath == "contained[0].code.coding[0].code" && string(fact.RawJSON) != `"same"` {
			t.Errorf("first code = %s, want same", fact.RawJSON)
		}
		if len(fact.ResourceBoundaries) != 1 {
			t.Errorf("resource boundaries at %s = %#v, want one", fact.OwnerPath, fact.ResourceBoundaries)
			continue
		}
		boundary := fact.ResourceBoundaries[0]
		if boundary.CanonicalPath != "contained[]" || boundary.ResourceType != "Observation" {
			t.Errorf("boundary = %#v", boundary)
		}
	}
	if len(first.ResourceBoundaries) != 1 || len(second.ResourceBoundaries) != 1 {
		t.Fatalf("contained boundary counts = %d / %d", len(first.ResourceBoundaries), len(second.ResourceBoundaries))
	}
	if first.ResourceBoundaries[0].OwnerPath != "contained[0]" || second.ResourceBoundaries[0].OwnerPath != "contained[1]" {
		t.Fatalf("contained owner boundaries = %#v / %#v", first.ResourceBoundaries, second.ResourceBoundaries)
	}
}

func TestWalkMembersWithResourceTypesSelectsHeterogeneousDefinitions(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	payload := []byte(`{"contained":[
		{"resourceType":"Observation","status":"final","code":{},"valueQuantity":{"value":7}},
		{"resourceType":"Condition","clinicalStatus":{"text":"active"},"code":{},"subject":{}}
	]}`)
	facts, diagnostics := collectResourceFacts(t, index, "Patient", payload, "Observation", "Condition")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	observation := findMemberFact(t, facts, "contained[].valueQuantity.value", "contained[0].valueQuantity.value", false)
	condition := findMemberFact(t, facts, "contained[].clinicalStatus.text", "contained[1].clinicalStatus.text", false)
	if observation.DeclaringType != "Quantity" || condition.DeclaringType != "CodeableConcept" {
		t.Fatalf("distinct declaring types = %q / %q", observation.DeclaringType, condition.DeclaringType)
	}
	if len(observation.ResourceBoundaries) != 1 || len(condition.ResourceBoundaries) != 1 {
		t.Fatalf("resource boundary counts = %d / %d", len(observation.ResourceBoundaries), len(condition.ResourceBoundaries))
	}
	if observation.ResourceBoundaries[0].ResourceType != "Observation" || condition.ResourceBoundaries[0].ResourceType != "Condition" {
		t.Fatalf("distinct resource boundaries = %#v / %#v", observation.ResourceBoundaries, condition.ResourceBoundaries)
	}
}

func TestWalkMembersWithResourceTypesKeepsNestedResourceBoundaryChain(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	payload := []byte(`{"contained":[{"resourceType":"Observation","status":"final","code":{},"contained":[{"resourceType":"Condition","clinicalStatus":{},"code":{},"subject":{},"onsetDateTime":"2024-01-02"}]}]}`)
	facts, diagnostics := collectResourceFacts(t, index, "Patient", payload, "Observation", "Condition")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	fact := findMemberFact(t, facts, "contained[].contained[].onsetDateTime", "contained[0].contained[0].onsetDateTime", false)
	if fact.RootDefinition != "Patient" || len(fact.ResourceBoundaries) != 2 {
		t.Fatalf("nested condition fact = %#v", fact)
	}
	want := []ResourceBoundary{
		{CanonicalPath: "contained[]", OwnerPath: "contained[0]", ResourceType: "Observation"},
		{CanonicalPath: "contained[].contained[]", OwnerPath: "contained[0].contained[0]", ResourceType: "Condition"},
	}
	for index, boundary := range want {
		if got := fact.ResourceBoundaries[index]; got != boundary {
			t.Errorf("boundary[%d] = %#v, want %#v", index, got, boundary)
		}
	}
}

func TestWalkMembersWithResourceTypesDispatchesUnfamiliarSingularAndRepeatedReferences(t *testing.T) {
	index := resourceDispatchIndex(t,
		schema.Element{Name: "opaque", JSONType: schema.JSONTypeObject, ReferencedType: "Resource"},
		schema.Element{Name: "records", JSONType: schema.JSONTypeArray, ArrayElementType: "Resource"},
	)
	payload := []byte(`{"opaque":{"resourceType":"Observation","observationOnly":"singular"},"records":[{"resourceType":"Observation","observationOnly":"repeated"}]}`)
	facts, diagnostics := collectResourceFacts(t, index, "DispatchEnvelope", payload, "Observation")
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", diagnostics)
	}
	singular := findMemberFact(t, facts, "opaque.observationOnly", "opaque.observationOnly", false)
	repeated := findMemberFact(t, facts, "records[].observationOnly", "records[0].observationOnly", false)
	if string(singular.RawJSON) != `"singular"` || string(repeated.RawJSON) != `"repeated"` {
		t.Fatalf("unfamiliar reference facts = %s / %s", singular.RawJSON, repeated.RawJSON)
	}
	if len(singular.ResourceBoundaries) != 1 || singular.ResourceBoundaries[0].CanonicalPath != "opaque" || singular.ResourceBoundaries[0].OwnerPath != "opaque" {
		t.Errorf("singular boundary = %#v", singular.ResourceBoundaries)
	}
	if len(repeated.ResourceBoundaries) != 1 || repeated.ResourceBoundaries[0].CanonicalPath != "records[]" || repeated.ResourceBoundaries[0].OwnerPath != "records[0]" {
		t.Errorf("repeated boundary = %#v", repeated.ResourceBoundaries)
	}
}

func TestWalkMembersWithResourceTypesLeavesUnresolvedResourcesAbstract(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	resources := []string{
		`{"extra":true}`,
		`{"resourceType":7,"extra":true}`,
		`{"resourceType":"MadeUp","extra":true}`,
		`{"resourceType":"Resource","extra":true}`,
		`{"resourceType":"Coding","extra":true}`,
		`{"resourceType":"observation","extra":true}`,
		`{"resourceType":" Observation","extra":true}`,
	}
	for indexInPayload, resource := range resources {
		t.Run(fmt.Sprintf("resource_%d", indexInPayload), func(t *testing.T) {
			payload := []byte(`{"contained":[` + resource + `]}`)
			facts, diagnostics := collectResourceFacts(t, index, "Patient", payload, "Observation", "Condition")
			assertUnresolvedResourceDiagnostic(t, diagnostics, "contained[]", "contained[0]")
			assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "contained[].extra", "contained[0].extra", "extra", "")
			for _, fact := range facts {
				if len(fact.ResourceBoundaries) != 0 {
					t.Fatalf("unresolved resource was given a boundary: %#v", fact)
				}
			}
		})
	}
}

func TestWalkMembersWithResourceTypesReportsAllowedTypeMissingFromIndex(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{Name: "DispatchEnvelope", Elements: []schema.Element{{Name: "item", JSONType: schema.JSONTypeObject, ReferencedType: "Resource"}}},
		{Name: "Resource", Elements: []schema.Element{{Name: "resourceType", JSONType: "string"}}},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	facts, diagnostics := collectResourceFacts(t, index, "DispatchEnvelope", []byte(`{"item":{"resourceType":"Observation","extra":true}}`), "Observation")
	assertUnresolvedResourceDiagnostic(t, diagnostics, "item", "item")
	assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "item.extra", "item.extra", "extra", "")
	for _, fact := range facts {
		if len(fact.ResourceBoundaries) != 0 {
			t.Fatalf("missing definition was dispatched: %#v", fact)
		}
	}
}

func TestWalkMembersWithResourceTypesRequiresExplicitPermission(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	facts, diagnostics := collectResourceFacts(t, index, "Patient", []byte(`{"contained":[{"resourceType":"Condition","clinicalStatus":{}}]}`), "Observation")
	assertUnresolvedResourceDiagnostic(t, diagnostics, "contained[]", "contained[0]")
	assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "contained[].clinicalStatus", "contained[0].clinicalStatus", "clinicalStatus", "")
	for _, fact := range facts {
		if len(fact.ResourceBoundaries) != 0 {
			t.Fatalf("unpermitted resource was dispatched: %#v", fact)
		}
	}
}

func TestWalkMembersWithResourceTypesIgnoresDiscriminatorOutsideResourceReference(t *testing.T) {
	index := resourceDispatchIndex(t, schema.Element{Name: "known", JSONType: "string"})
	facts, diagnostics := collectResourceFacts(t, index, "DispatchEnvelope", []byte(`{"mystery":{"resourceType":"Observation","observationOnly":"x"}}`), "Observation")
	assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "mystery", "mystery", "mystery", "")
	for _, fact := range facts {
		if fact.CanonicalPath == "mystery.observationOnly" || len(fact.ResourceBoundaries) != 0 {
			t.Fatalf("unknown object was dispatched: %#v", fact)
		}
	}
}

func TestWalkMembersRetainsAbstractResourceBehavior(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	var facts []MemberFact
	diagnostics, err := WalkMembers(index, "Patient", []byte(`{"contained":[{"resourceType":"Observation","code":{"text":"x"}}]}`), func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkMembers: %v", err)
	}
	assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "contained[].code", "contained[0].code", "code", "")
	for _, fact := range facts {
		if fact.CanonicalPath == "contained[].code" || len(fact.ResourceBoundaries) != 0 {
			t.Fatalf("legacy walker dispatched abstract Resource: %#v", fact)
		}
	}
}

func TestWalkMembersWithResourceTypesRejectsInvalidAllowedTypes(t *testing.T) {
	index := resourceDispatchIndex(t, schema.Element{Name: "item", JSONType: schema.JSONTypeObject, ReferencedType: "Resource"})
	for _, allowed := range [][]schema.ResourceType{nil, {}, {" observation"}, {"observation"}, {"Resource"}, {"Coding"}, {"Observation", "Observation"}} {
		if _, err := WalkMembersWithResourceTypes(index, "DispatchEnvelope", []byte(`{}`), allowed, func(MemberFact) error { return nil }); err == nil {
			t.Errorf("allowed resource types %q were accepted", allowed)
		}
	}
}

func TestWalkMembersWithResourceTypesReturnsTypedLimitErrorsAndPartialDiagnostics(t *testing.T) {
	if WalkMembersWithResourceTypesMaxFacts != 100_000 || WalkMembersWithResourceTypesMaxDiagnostics != 4_096 || WalkMembersWithResourceTypesMaxDepth != 128 {
		t.Fatalf("production limits = %d facts, %d diagnostics, depth %d", WalkMembersWithResourceTypesMaxFacts, WalkMembersWithResourceTypesMaxDiagnostics, WalkMembersWithResourceTypesMaxDepth)
	}
	index := resourceDispatchIndex(t,
		schema.Element{Name: "a", JSONType: "string"},
		schema.Element{Name: "b", JSONType: "string"},
		schema.Element{Name: "c", JSONType: "string"},
	)
	facts, diagnostics, err := walkWithSmallResourceLimits(t, index, []byte(`{"a":"a","b":"b","c":"c"}`), memberWalkLimits{maxFacts: 2, maxDiagnostics: 10, maxDepth: 10})
	assertWalkLimit(t, err, WalkLimitVisitedFacts, 2)
	if len(facts) != 2 || len(diagnostics) != 0 {
		t.Fatalf("fact limit returned %d facts and diagnostics %#v", len(facts), diagnostics)
	}

	index = resourceDispatchIndex(t, schema.Element{Name: "known", JSONType: "string"})
	facts, diagnostics, err = walkWithSmallResourceLimits(t, index, []byte(`{"z":0,"x":0,"y":0}`), memberWalkLimits{maxFacts: 10, maxDiagnostics: 2, maxDepth: 10})
	assertWalkLimit(t, err, WalkLimitDiagnostics, 2)
	if len(facts) != 0 || len(diagnostics) != 2 || diagnostics[0].Member != "x" || diagnostics[1].Member != "y" {
		t.Fatalf("diagnostic limit result = facts %#v, diagnostics %#v", facts, diagnostics)
	}

	index = recursiveDispatchIndex(t)
	facts, diagnostics, err = walkWithSmallResourceLimits(t, index, []byte(`{"child":{"child":{"child":{"value":"deep"}}}}`), memberWalkLimits{maxFacts: 10, maxDiagnostics: 10, maxDepth: 1})
	assertWalkLimit(t, err, WalkLimitDepth, 1)
	if len(facts) != 2 || len(diagnostics) != 0 {
		t.Fatalf("depth limit result = %d facts, diagnostics %#v", len(facts), diagnostics)
	}
}

func TestWalkMembersDoesNotTreatVisitorLimitErrorAsWalkerLimit(t *testing.T) {
	index := resourceDispatchIndex(t, schema.Element{Name: "value", JSONType: "string"})
	visitorLimit := &WalkLimitError{Kind: WalkLimitVisitedFacts, Limit: 1}
	diagnostics, err := WalkMembers(index, "DispatchEnvelope", []byte(`{"value":"x"}`), func(MemberFact) error { return visitorLimit })
	if !errors.Is(err, visitorLimit) || diagnostics != nil {
		t.Fatalf("legacy visitor limit result = %#v, %v", diagnostics, err)
	}
	diagnostics, err = WalkMembersWithResourceTypes(index, "DispatchEnvelope", []byte(`{"value":"x"}`), []schema.ResourceType{"Observation"}, func(MemberFact) error { return visitorLimit })
	if !errors.Is(err, visitorLimit) || diagnostics != nil {
		t.Fatalf("resource-aware visitor limit result = %#v, %v", diagnostics, err)
	}
}

func collectResourceFacts(t *testing.T, index *schema.Index, root schema.DefinitionName, payload []byte, allowed ...schema.ResourceType) ([]MemberFact, []WalkDiagnostic) {
	t.Helper()
	facts := make([]MemberFact, 0)
	diagnostics, err := WalkMembersWithResourceTypes(index, root, payload, allowed, func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkMembersWithResourceTypes: %v", err)
	}
	return facts, diagnostics
}

func resourceDispatchIndex(t *testing.T, rootElements ...schema.Element) *schema.Index {
	t.Helper()
	definitions := []schema.Definition{
		{Name: "DispatchEnvelope", Elements: rootElements},
		{Name: "Resource", Elements: []schema.Element{
			{Name: "resourceType", JSONType: "string"},
			{Name: "id", JSONType: "string"},
		}},
		{Name: "Observation", Elements: []schema.Element{{Name: "observationOnly", JSONType: "string"}}},
	}
	definitions[len(definitions)-1].Elements = append(definitions[len(definitions)-1].Elements, schema.Element{Name: "resourceType", JSONType: "string"})
	index, err := schema.NewIndex(definitions)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return index
}

func recursiveDispatchIndex(t *testing.T) *schema.Index {
	t.Helper()
	index, err := schema.NewIndex([]schema.Definition{{
		Name: "DispatchEnvelope",
		Elements: []schema.Element{
			{Name: "child", JSONType: schema.JSONTypeObject, ReferencedType: "DispatchEnvelope"},
			{Name: "value", JSONType: "string"},
		},
	}})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return index
}

func walkWithSmallResourceLimits(t *testing.T, index *schema.Index, payload []byte, limits memberWalkLimits) ([]MemberFact, []WalkDiagnostic, error) {
	t.Helper()
	facts := make([]MemberFact, 0)
	diagnostics, err := walkMembersWithResourceTypesAndLimits(index, "DispatchEnvelope", payload, []schema.ResourceType{"Observation"}, func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	}, limits)
	return facts, diagnostics, err
}

func assertUnresolvedResourceDiagnostic(t *testing.T, diagnostics []WalkDiagnostic, canonicalPath, ownerPath string) {
	t.Helper()
	assertWalkDiagnostic(t, diagnostics, WalkUnresolvedResourceType, canonicalPath, ownerPath, "resourceType", "")
}

func assertWalkLimit(t *testing.T, err error, kind WalkLimitKind, limit int) {
	t.Helper()
	var limitErr *WalkLimitError
	if !errors.As(err, &limitErr) || limitErr.Kind != kind || limitErr.Limit != limit {
		t.Fatalf("limit error = %v, want *WalkLimitError{%s, %d}", err, kind, limit)
	}
}

func TestWalkMemberValuesWithResourceTypesResolvesContainedResource(t *testing.T) {
	index := resourceDispatchIndex(t, schema.Element{
		Name: "contained", JSONType: schema.JSONTypeArray, ArrayElementType: "Resource",
		ArrayElements: []schema.Element{{Name: "resourceType", JSONType: "string"}},
	})
	payload := map[string]any{"contained": []any{
		map[string]any{"resourceType": "Observation", "observationOnly": "seen"},
		map[string]any{"resourceType": "Patient"},
	}}
	var facts []MemberFact
	diagnostics, err := WalkMemberValuesWithResourceTypes(index, "DispatchEnvelope", payload, []schema.ResourceType{"Observation"}, func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != WalkUnresolvedResourceType || diagnostics[0].OwnerPath != "contained[1]" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, fact := range facts {
		if fact.OwnerPath == "contained[0].observationOnly" {
			if fact.DeclaringType != "Observation" || fact.Value != "seen" || len(fact.ResourceBoundaries) != 1 {
				t.Fatalf("contained observation fact = %#v", fact)
			}
			return
		}
	}
	t.Fatalf("contained observation fact missing from %#v", facts)
}

func TestWalkMembersResolvesUnfamiliarRepeatedOwnersAndChoiceArms(t *testing.T) {
	index := signalPacketIndex(t)
	payload := []byte(`{
  "measurements": [
    {
      "concept": {"text": "zero reading"},
      "valueQuantity": {"value": 0, "unit": "mg"},
      "unexpected": true
    },
    {
      "concept": {"text": "missing result"}
    }
  ]
}`)

	var facts []MemberFact
	diagnostics, err := WalkMembers(index, "SignalPacket", payload, func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkMembers: %v", err)
	}

	firstOwner := findMemberFact(t, facts, "measurements[]", "measurements[0]", true)
	if firstOwner.DeclaringType != "SignalPacket" || firstOwner.ReferencedType != "SignalReading" || firstOwner.ItemIndex == nil || *firstOwner.ItemIndex != 0 {
		t.Fatalf("first owner fact = %#v", firstOwner)
	}
	if len(firstOwner.RepeatedBoundaries) != 1 || firstOwner.RepeatedBoundaries[0].Index != 0 {
		t.Fatalf("first owner boundaries = %#v", firstOwner.RepeatedBoundaries)
	}

	quantity := findMemberFact(t, facts, "measurements[].valueQuantity", "measurements[0].valueQuantity", false)
	if quantity.DeclaringType != "SignalReading" || quantity.ReferencedType != "Quantity" {
		t.Fatalf("quantity fact = %#v", quantity)
	}
	if !bytes.Contains(quantity.RawJSON, []byte(`"value": 0`)) || !bytes.Contains(quantity.RawJSON, []byte(`"unit": "mg"`)) {
		t.Fatalf("quantity raw JSON lost zero or unit: %s", quantity.RawJSON)
	}
	value := findMemberFact(t, facts, "measurements[].valueQuantity.value", "measurements[0].valueQuantity.value", false)
	if value.DeclaringType != "Quantity" || string(value.RawJSON) != "0" || value.Null {
		t.Fatalf("quantity value fact = %#v", value)
	}
	conceptText := findMemberFact(t, facts, "measurements[].concept.text", "measurements[1].concept.text", false)
	if conceptText.DeclaringType != "CodeableConcept" || string(conceptText.RawJSON) != `"missing result"` {
		t.Fatalf("concept text fact = %#v", conceptText)
	}

	assertWalkDiagnostic(t, diagnostics, WalkMissingRequiredElement, "id", "id", "id", "")
	assertWalkDiagnostic(t, diagnostics, WalkUnknownMember, "measurements[].unexpected", "measurements[0].unexpected", "unexpected", "")
	assertWalkDiagnostic(t, diagnostics, WalkMissingRequiredChoice, "measurements[]", "measurements[1]", "", "result")
	missingChoices := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == WalkMissingRequiredChoice {
			missingChoices++
		}
	}
	if missingChoices != 1 {
		t.Fatalf("missing choice diagnostics = %d, want 1: %#v", missingChoices, diagnostics)
	}
}

func TestWalkMembersReportsNullRequiredMemberAndCopiesRawJSON(t *testing.T) {
	index := signalPacketIndex(t)
	payload := []byte(`{"id":null,"measurements":[]}`)
	var idFact MemberFact
	diagnostics, err := WalkMembers(index, "SignalPacket", payload, func(fact MemberFact) error {
		if fact.CanonicalPath == "id" {
			idFact = fact
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkMembers: %v", err)
	}
	if !idFact.Null || string(idFact.RawJSON) != "null" {
		t.Fatalf("null id fact = %#v", idFact)
	}
	assertWalkDiagnostic(t, diagnostics, WalkMissingRequiredElement, "id", "id", "id", "")
	payload[6] = 'x'
	if string(idFact.RawJSON) != "null" {
		t.Fatal("member fact raw JSON aliases the payload")
	}
}

func TestWalkMembersRejectsInvalidInputsAndPropagatesVisitorErrors(t *testing.T) {
	index := signalPacketIndex(t)
	visitorErr := errors.New("stop")
	if _, err := WalkMembers(nil, "SignalPacket", []byte(`{}`), func(MemberFact) error { return nil }); err == nil {
		t.Fatal("nil index was accepted")
	}
	if _, err := WalkMembers(index, "Missing", []byte(`{}`), func(MemberFact) error { return nil }); err == nil {
		t.Fatal("missing root was accepted")
	}
	if _, err := WalkMembers(index, "SignalPacket", []byte(`{}`), nil); err == nil {
		t.Fatal("nil visitor was accepted")
	}
	_, err := WalkMembers(index, "SignalPacket", []byte(`{"id":"packet-1"}`), func(MemberFact) error {
		return visitorErr
	})
	if !errors.Is(err, visitorErr) {
		t.Fatalf("visitor error = %v, want %v", err, visitorErr)
	}
}

func TestWalkMembersCachesReferencedScopeWithParentRequirements(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "Root",
			Elements: []schema.Element{
				{Name: "first", JSONType: schema.JSONTypeObject, ReferencedType: "Child", RequiredChildren: []string{"alpha"}},
				{Name: "second", JSONType: schema.JSONTypeObject, ReferencedType: "Child", RequiredChildren: []string{"beta"}},
			},
		},
		{Name: "Child", Elements: []schema.Element{{Name: "value", JSONType: "string"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"first":{},"second":{}}`)
	for attempt := 0; attempt < 3; attempt++ {
		diagnostics, err := WalkMembers(index, "Root", payload, func(MemberFact) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 2 {
			t.Fatalf("diagnostics = %#v", diagnostics)
		}
		for position, expected := range []string{"first.alpha", "second.beta"} {
			diagnostic := diagnostics[position]
			if diagnostic.Code != WalkMissingRequiredElement || diagnostic.CanonicalPath != expected {
				t.Fatalf("diagnostic[%d] = %#v, want missing required %q", position, diagnostic, expected)
			}
		}
	}
}

func signalPacketIndex(t *testing.T) *schema.Index {
	t.Helper()
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name:             "SignalPacket",
			RequiredElements: []string{"id"},
			Elements: []schema.Element{
				{Name: "id", JSONType: "string", ElementRequired: true},
				{Name: "measurements", JSONType: schema.JSONTypeArray, ArrayElementType: "SignalReading"},
			},
		},
		{
			Name:             "SignalReading",
			RequiredElements: []string{"concept"},
			Elements: []schema.Element{
				{Name: "concept", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept", ElementRequired: true},
				{Name: "valueQuantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "result", ChoiceGroupRequired: true, ElementRequired: true},
				{Name: "valueString", JSONType: "string", ChoiceGroup: "result", ChoiceGroupRequired: true, ElementRequired: true},
			},
		},
		{Name: "CodeableConcept", Elements: []schema.Element{{Name: "text", JSONType: "string"}}},
		{Name: "Quantity", Elements: []schema.Element{
			{Name: "value", JSONType: "number"},
			{Name: "unit", JSONType: "string"},
		}},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return index
}

func findMemberFact(t *testing.T, facts []MemberFact, canonicalPath, ownerPath string, arrayItem bool) MemberFact {
	t.Helper()
	for _, fact := range facts {
		if fact.CanonicalPath == canonicalPath && fact.OwnerPath == ownerPath && fact.ArrayItem == arrayItem {
			return fact
		}
	}
	t.Fatalf("member fact %q at %q (array item %v) not found in %#v", canonicalPath, ownerPath, arrayItem, facts)
	return MemberFact{}
}

func assertWalkDiagnostic(
	t *testing.T,
	diagnostics []WalkDiagnostic,
	code WalkDiagnosticCode,
	canonicalPath string,
	ownerPath string,
	member string,
	choiceGroup string,
) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.CanonicalPath == canonicalPath && diagnostic.OwnerPath == ownerPath && diagnostic.Member == member && diagnostic.ChoiceGroup == choiceGroup {
			return
		}
	}
	t.Fatalf("diagnostic %s at %s/%s member=%q choice=%q not found in %#v", code, canonicalPath, ownerPath, member, choiceGroup, diagnostics)
}

func TestWalkMemberValuesTreatsTypedNilObjectAndArrayAsNull(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{{Name: "SignalPacket", Elements: []schema.Element{
		{Name: "object", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
		{Name: "items", JSONType: schema.JSONTypeArray, ArrayElementType: "Quantity"},
	}}, {Name: "Quantity", Elements: []schema.Element{{Name: "value", JSONType: "number"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	var items []any
	var facts []MemberFact
	if _, err := WalkMemberValues(index, "SignalPacket", map[string]any{
		"object": object,
		"items":  items,
	}, func(fact MemberFact) error {
		facts = append(facts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 || !facts[0].Null || !facts[1].Null {
		t.Fatalf("typed nil facts = %#v", facts)
	}
}
