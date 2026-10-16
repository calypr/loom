package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func TestFieldPresentationReadySharedFixtureMatchesGeneratedWireTypes(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "semantic-field-presentation-ready.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := directAuthoringJSON[loomapi.SemanticInventoryBrowseResponse](json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode shared field-presentation fixture: %v", err)
	}
	if response.FieldPresentation == nil {
		t.Fatal("shared fixture omitted fieldPresentation")
	}
	ready, err := response.FieldPresentation.AsFieldPresentationReady()
	if err != nil || ready.Kind != loomapi.FieldPresentationReadyKindREADY {
		t.Fatalf("fieldPresentation=%#v err=%v", ready, err)
	}
	if ready.Context.Project != "project-1" || ready.Context.SnapshotToken != "snapshot-1" ||
		ready.Context.InventoryBuildId != response.BuildId || ready.Context.OutputId != "specimens" ||
		ready.Context.DraftVersion != 3 || ready.Context.DraftDigest != "draft-digest-3" ||
		ready.Context.SourceNodeId != "specimen-node" || ready.Context.OccurrenceId != "base" ||
		len(ready.Context.Route) != 0 || ready.Context.PresentationRevision != "semantic-field-presentation/v1" ||
		ready.Context.CandidateBatchDigest == "" {
		t.Fatalf("field presentation context=%#v", ready.Context)
	}
	if len(ready.Decisions) != 1 || len(ready.Replacements) != 1 {
		t.Fatalf("field presentation decisions=%#v replacements=%#v", ready.Decisions, ready.Replacements)
	}
	group, err := ready.Decisions[0].AsFieldPresentationGroupUnderSemantic()
	if err != nil || group.Kind != "GROUP_UNDER_SEMANTIC" ||
		group.CandidateId != "raw-code" || group.Access != "SOURCE_FIELDS" ||
		len(group.ReplacementIds) != 1 {
		t.Fatalf("group decision=%#v err=%v", group, err)
	}
	exact, err := group.ReplacementIds[0].AsExactSemanticInventoryItemIdentity()
	if err != nil || exact.Kind != loomapi.ExactSemanticInventoryItemIdentityKindEXACTCONCEPT ||
		exact.ConceptId != "concept-1" || exact.BindingId != "binding-1" {
		t.Fatalf("replacement identity=%#v err=%v", exact, err)
	}
	choice := ready.Replacements[0].ConstructionChoice
	if choice == nil {
		t.Fatal("replacement is missing its current construction choice")
	}
	source, err := choice.Source.AsSemanticBindingChoiceSource()
	if err != nil || source.ConceptId != exact.ConceptId || source.BindingId != exact.BindingId ||
		source.NodeId != ready.Context.SourceNodeId || source.ResourceType != "Specimen" {
		t.Fatalf("replacement choice source=%#v err=%v", source, err)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("generated Go wire round-trip changed fixture\nwant: %#v\n got: %#v", expected, actual)
	}
}
