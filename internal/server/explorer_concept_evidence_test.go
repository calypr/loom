package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestCapabilityEvidencePreservesConceptIdentityAndUnresolvedStatus(t *testing.T) {
	input := catalog.CapabilityEvidence{FieldEnrichment: catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{{
		ResourceType: "Observation", Path: "component[]", Kind: "array", DocCount: 2,
		SemanticObservations: []catalog.SemanticObservation{{
			Source:      catalog.SemanticObservationSource{Type: "Observation", Path: "component[].code"},
			OwningScope: "component[]", Key: catalog.SemanticObservationKey{Selector: "code.coding[]", Code: "shared"},
			Value:     catalog.SemanticObservationValue{Selector: "valueQuantity.value"},
			ChoiceArm: "valueQuantity", LogicalType: "decimal", ObservedUnits: []string{"cm"},
			ExtensionURLPath: []string{"urn:parent", "urn:leaf"}, Completeness: catalog.SemanticPartial,
			Status: "UNRESOLVED_SYSTEM", Population: 2, Examples: []string{"333"},
		}},
	}, {
		ResourceType: "Observation", Path: "component[].valueQuantity.value", Kind: "scalar", DocCount: 2,
	}}}}
	evidence := capabilityEvidenceFromCatalog(input)
	if len(evidence.Fields) != 1 {
		t.Fatalf("fields = %d, want only the proven scalar value path", len(evidence.Fields))
	}
	want := []capability.ConceptCandidate{{
		SourceResourceType: "Observation", SourcePath: "component[].code", OwningScope: "component[]",
		KeySelector: "code.coding[]", Code: "shared", ValueSelector: "valueQuantity.value",
		ChoiceArm: "valueQuantity", LogicalType: "decimal", ObservedUnits: []string{"cm"},
		ExtensionURLPath: []string{"urn:parent", "urn:leaf"}, Completeness: "partial", Status: "UNRESOLVED_SYSTEM",
		Population: 2, Examples: []string{"333"},
	}}
	var scalar *capability.FieldObservation
	for index := range evidence.Fields {
		if evidence.Fields[index].Path == "component[].valueQuantity.value" {
			scalar = &evidence.Fields[index]
		}
		if evidence.Fields[index].Path == "component[]" {
			t.Fatalf("object owner was emitted as a projectable capability field: %+v", evidence.Fields[index])
		}
	}
	if scalar == nil || !reflect.DeepEqual(scalar.ConceptCandidates, want) {
		t.Fatalf("scalar value concept evidence = %+v", scalar)
	}
	input.FieldEnrichment.Values[0].SemanticObservations[0].ObservedUnits[0] = "changed"
	if scalar.ConceptCandidates[0].ObservedUnits[0] != "cm" {
		t.Fatal("capability evidence aliases mutable catalog evidence")
	}
}

func TestCapabilityConceptsSurviveBuilderProofOnScalarValuePath(t *testing.T) {
	concept := func(system, code, status string, population int64) catalog.SemanticObservation {
		return catalog.SemanticObservation{
			Source:      catalog.SemanticObservationSource{Type: "Observation", Canonical: "Observation.component", Path: "component[]"},
			OwningScope: "component[]",
			Key:         catalog.SemanticObservationKey{Selector: "code.coding[]", System: system, Code: code},
			Value:       catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
			ChoiceArm:   "valueQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete,
			Status: status, Population: population, Examples: []string{code},
		}
	}
	input := catalog.CapabilityEvidence{
		ResourceInventory: catalog.ResourceInventoryResult{Available: true, Complete: true, Values: []catalog.ResourceInventoryObservation{{ResourceType: "Observation", DocumentCount: 3}}},
		Relationships:     catalog.RelationshipObservationResult{Available: true, Complete: true},
		FieldEnrichment: catalog.FieldEnrichmentResult{Available: true, Complete: true, Values: []catalog.FieldEnrichmentObservation{{
			ResourceType: "Observation", Path: "component[]", Kind: "array", DocCount: 3,
			SemanticObservations: []catalog.SemanticObservation{
				concept("urn:study:A", "shared", "SUPPORTED", 1),
				concept("urn:study:B", "shared", "SUPPORTED", 1),
				concept("", "shared", "UNRESOLVED_SYSTEM", 1),
			},
		}}},
	}
	evidence := capabilityEvidenceFromCatalog(input)
	compiler := explorerCapabilityCompiler{scope: compilerprobe.Scope{Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted}}
	identity := capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", CompilerVersion: "test", ProtocolVersion: "test"}
	snapshot, err := capability.NewBuilder(identity, evidence, capability.CompilerCallbacks{
		Node: compiler.ProbeNode, Edge: compiler.ProbeEdge, Candidate: compiler.ProbeCandidate,
	}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Usable() {
		t.Fatalf("snapshot is not usable: %#v", snapshot)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].FieldPath != "component[].valueQuantity.value" {
		t.Fatalf("candidates = %#v, want one proven scalar value candidate", snapshot.Candidates)
	}
	if len(snapshot.Candidates[0].ConceptCandidates) != 3 {
		t.Fatalf("proven scalar concepts = %#v", snapshot.Candidates[0].ConceptCandidates)
	}
	statusBySystem := map[string]string{}
	for _, candidate := range snapshot.Candidates[0].ConceptCandidates {
		if candidate.OwningScope != "component[]" || candidate.ValueSelector != "valueQuantity.value" {
			t.Fatalf("concept lost owner/value identity: %#v", candidate)
		}
		statusBySystem[candidate.System] = candidate.Status
	}
	if statusBySystem["urn:study:A"] != "SUPPORTED" || statusBySystem["urn:study:B"] != "SUPPORTED" || statusBySystem[""] != "UNRESOLVED_SYSTEM" {
		t.Fatalf("concept statuses = %#v", statusBySystem)
	}
	authoring := authoringV2Catalog(snapshot, "observations")
	if len(authoring.Candidates) != 1 || len(authoring.Candidates[0].ConceptCandidates) != 3 {
		t.Fatalf("authoring catalog concepts = %#v", authoring.Candidates)
	}
	authoringStatusBySystem := map[string]string{}
	for _, candidate := range authoring.Candidates[0].ConceptCandidates {
		if candidate.OwningScope != "component[]" || candidate.ValueSelector != "valueQuantity.value" {
			t.Fatalf("authoring catalog lost owner/value identity: %#v", candidate)
		}
		authoringStatusBySystem[candidate.System] = candidate.Status
	}
	if authoringStatusBySystem["urn:study:A"] != "SUPPORTED" || authoringStatusBySystem["urn:study:B"] != "SUPPORTED" || authoringStatusBySystem[""] != "UNRESOLVED_SYSTEM" {
		t.Fatalf("authoring catalog statuses = %#v", authoringStatusBySystem)
	}
}
