package lifecycle

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestRowIdentityForNetworkUsesEffectiveOccurrencesAndPreservesDocument(t *testing.T) {
	document := ws.Document{
		Output:           ws.Output{ID: "subjects"},
		RootResourceType: "ResearchSubject",
		Rows:             ws.RowDefinition{Kind: ws.RowDefinitionRecords},
		Route:            ws.RouteNode{OccurrenceID: "base", ResourceType: "ResearchSubject", Children: []ws.RouteNode{{OccurrenceID: "historical", ResourceType: "Patient"}}},
	}
	before := document
	network := authoringnetwork.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Occurrences = []authoringnetwork.NetworkOccurrence{
		{OccurrenceID: "base", ResourceType: "ResearchSubject", Enabled: true},
		{OccurrenceID: "subject_patient", ParentOccurrenceID: "base", ResourceType: "Patient", Enabled: true, MatchMode: ws.RouteMatchRequired},
		{OccurrenceID: "observation_patient", ParentOccurrenceID: "base", ResourceType: "Patient", Enabled: true, MatchMode: ws.RouteMatchOptional},
		{OccurrenceID: "removed", ParentOccurrenceID: "base", ResourceType: "Observation", Enabled: false, MatchMode: ws.RouteMatchRequired},
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
