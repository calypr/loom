package arango

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
)

func TestEncodeSemanticCoverageSourceEnvelopeIsOneVersionedBoundedDocument(t *testing.T) {
	contribution := semanticCoverageEnvelopeTestContribution()
	encoded, err := EncodeSemanticCoverageSourceEnvelope(contribution.BuildID, contribution)
	if err != nil {
		t.Fatal(err)
	}
	var envelope semanticCoverageSourceEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.RecordKind != semanticCoverageEnvelopeRecordKind || envelope.CoverageStorageFormatVersion != fieldprofile.SemanticCoverageStorageFormatVersion {
		t.Fatalf("envelope version = (%q, %d)", envelope.RecordKind, envelope.CoverageStorageFormatVersion)
	}
	if envelope.SourceRecordCount != 1 || len(envelope.Paths) != 1 || len(envelope.Paths[0].Path.Facets) != 1 {
		t.Fatalf("source envelope did not keep its nested aggregate once: %+v", envelope)
	}
	if envelope.Paths[0].CanonicalPath != "code" || envelope.Paths[0].Path.Facets[0].Path != "code.coding[].code" {
		t.Fatalf("stored path/facet identity = (%q, %q)", envelope.Paths[0].CanonicalPath, envelope.Paths[0].Path.Facets[0].Path)
	}
	if envelope.Paths[0].Path.OwnerPathWitnesses != nil {
		t.Fatalf("persisted owner path witnesses: %#v", envelope.Paths[0].Path.OwnerPathWitnesses)
	}
	if bytes := string(encoded); strings.Contains(bytes, "RAW-SOURCE-CANARY") || strings.Contains(bytes, "OwnerPathWitnesses") || strings.Contains(bytes, "owner_path_witnesses") {
		t.Fatalf("envelope persisted raw source/witness data: %s", bytes)
	}
	if strings.Contains(string(encoded), "Explanation") || strings.Contains(string(encoded), "RAW-EXPLANATION-CANARY") {
		t.Fatalf("envelope persisted diagnostic explanation: %s", encoded)
	}

	changedAuth := contribution
	changedAuth.Identity.AuthResourcePath = "/alternate-auth-scope"
	changedEncoded, err := EncodeSemanticCoverageSourceEnvelope(changedAuth.BuildID, changedAuth)
	if err != nil {
		t.Fatal(err)
	}
	var changed semanticCoverageSourceEnvelope
	if err := json.Unmarshal(changedEncoded, &changed); err != nil {
		t.Fatal(err)
	}
	if envelope.Key != changed.Key {
		t.Fatalf("auth path changed physical source key: %q != %q", envelope.Key, changed.Key)
	}
	if envelope.AuthResourcePath == changed.AuthResourcePath {
		t.Fatal("auth path was not updated in the replacement envelope")
	}
}

func TestEncodeSemanticCoverageSourceEnvelopeRejectsOversizedDocument(t *testing.T) {
	contribution := semanticCoverageEnvelopeTestContribution()
	contribution.Paths[0].CanonicalPath = strings.Repeat("x", semanticCoverageMaximumRowBytes)
	_, err := EncodeSemanticCoverageSourceEnvelope(contribution.BuildID, contribution)
	if !errors.Is(err, ErrSemanticCoverageRowTooLarge) {
		t.Fatalf("oversized envelope error = %v", err)
	}
}

func semanticCoverageEnvelopeTestContribution() fieldprofile.SemanticCoverageSourceContribution {
	return fieldprofile.SemanticCoverageSourceContribution{
		Identity: fieldprofile.SemanticCoverageSourceIdentity{
			Project: "project", DatasetGeneration: "generation", AuthResourcePath: "/specimen",
			SchemaIdentity: "schema-sha256", SourceKind: "retained_vertex",
			SourceID: "retained:specimens/key-1", ResourceType: "Specimen",
		},
		BuildID: "inventory-build", SourceRecordCount: 1,
		Counts:   fieldprofile.SemanticCoverageCounts{CodeableConceptOwners: 1, CodingOccurrences: 1},
		Complete: true,
		Diagnostics: []fieldprofile.SemanticCoverageDiagnostic{{
			Code: "TEST_DIAGNOSTIC", CanonicalPath: "code", Explanation: "RAW-EXPLANATION-CANARY",
		}},
		Paths: []fieldprofile.SemanticCoveragePathAggregate{{
			CanonicalPath: "code", Kind: fieldprofile.SemanticCoverageCodeableConcept,
			Disposition:     fieldprofile.SemanticCoverageSemanticDisposition,
			OccurrenceCount: 1, OwnerCount: 1, CodingCount: 1,
			OwnerPathWitnesses: []string{"RAW-SOURCE-CANARY"},
			Facets: []fieldprofile.SemanticCoverageFacetAggregate{{
				Path: "code.coding[].code", Kind: "PRIMITIVE_CODE",
				Disposition:     fieldprofile.SemanticCoverageSemanticDisposition,
				OccurrenceCount: 1,
				References:      []fieldprofile.SemanticCoverageReference{{Kind: fieldprofile.SemanticCoverageExactReference, ConceptID: "concept-1", BindingID: "binding-1"}},
			}},
		}},
	}
}
