package construction

import (
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/semantic"
)

func TestCodedPivotSourcePreservesPriorPrimaryOnlyFamily(t *testing.T) {
	observation := semanticFrameCompatibilityObservation("codeable concept")
	entry := semantic.Entry{ConceptID: "stage", BindingID: "current-binding", Observation: observation}
	currentFamily := semanticFrameCompatibilityFamily(t, entry)
	priorFamily := currentFamily
	priorFamily.BindingID = semantic.LegacySemanticBindingIDV1(observation)
	priorFamily.SchemaVersion = semantic.SemanticObservationLegacyV1SchemaVersion
	priorFamily.RuleVersion = strconv.Itoa(semantic.SemanticObservationLegacyV1RuleVersion)
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
