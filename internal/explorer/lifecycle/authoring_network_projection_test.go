package lifecycle

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestRowIdentityForNetworkUsesEffectiveOccurrencesAndPreservesDocument(t *testing.T) {
	document := authoringv2.Document{
		Output:           authoringv2.Output{ID: "subjects"},
		RootResourceType: "ResearchSubject",
		Rows:             authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords},
		Route:            authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "ResearchSubject", Children: []authoringv2.RouteNode{{OccurrenceID: "historical", ResourceType: "Patient"}}},
	}
	before := document
	network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Occurrences = []authoringv2.NetworkOccurrence{
		{OccurrenceID: "base", ResourceType: "ResearchSubject", Enabled: true},
		{OccurrenceID: "subject_patient", ParentOccurrenceID: "base", ResourceType: "Patient", Enabled: true, MatchMode: authoringv2.RouteMatchRequired},
		{OccurrenceID: "observation_patient", ParentOccurrenceID: "base", ResourceType: "Patient", Enabled: true, MatchMode: authoringv2.RouteMatchOptional},
		{OccurrenceID: "removed", ParentOccurrenceID: "base", ResourceType: "Observation", Enabled: false, MatchMode: authoringv2.RouteMatchRequired},
	}
	projected := rowIdentityForNetwork(document, network)
	if !reflect.DeepEqual(document, before) {
		t.Fatal("projection changed authoritative document")
	}
	if len(projected.Relationships) != 2 || projected.Relationships[0].OccurrenceID != "subject_patient" || !projected.Relationships[0].MayExcludeRows || projected.Relationships[1].OccurrenceID != "observation_patient" || projected.Relationships[1].MayExcludeRows {
		t.Fatalf("effective occurrences or match policy lost: %#v", projected.Relationships)
	}
	if len(projected.Stages) != 1 || projected.Stages[0].Identity.Kind != "RECORD" || !projected.Stages[0].MayExcludeRows {
		t.Fatalf("required matching became expansion or lost filtering: %#v", projected.Stages)
	}
}
