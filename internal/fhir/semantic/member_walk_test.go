package semantic

import (
	"bytes"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

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
