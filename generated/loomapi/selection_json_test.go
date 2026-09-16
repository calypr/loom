package loomapi

import (
	"encoding/json"
	"testing"
)

func TestSelectionRequestRejectsUntrustedAndAmbiguousSources(t *testing.T) {
	for _, raw := range []string{
		`{"idempotencyKey":"one","snapshotToken":"snapshot","scopeDigest":"trusted","source":{"kind":"resources","resources":{"refs":[]}}}`,
		`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"publishedOutput","publishedOutput":{"revisionId":"r","outputId":"o","executionId":"forged"}}}`,
		`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"resources","resources":{"refs":[]},"publishedOutput":{"revisionId":"r","outputId":"o"}}}`,
		`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"resources"}}`,
		`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"other"}}`,
		`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"resources","resources":{"refs":[{"project":"p","generation":"g","resourceType":"Specimen","id":"s","rowNumber":3}]}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var request SelectionCreateRequest
			if err := json.Unmarshal([]byte(raw), &request); err == nil {
				t.Fatal("invalid selection request was accepted")
			}
		})
	}
}

func TestSelectionRequestPreservesTypedReferenceAndFilterValues(t *testing.T) {
	var explicit SelectionCreateRequest
	if err := json.Unmarshal([]byte(`{"idempotencyKey":"one","snapshotToken":"snapshot","source":{"kind":"resources","resources":{"refs":[{"project":"p","generation":"g","resourceType":"Specimen","id":"s"}]}}}`), &explicit); err != nil {
		t.Fatal(err)
	}
	if got := explicit.Source.Resources.Refs[0]; got != (SelectionResourceRef{Project: "p", Generation: "g", ResourceType: "Specimen", Id: "s"}) {
		t.Fatalf("reference changed: %+v", got)
	}
	var published SelectionCreateRequest
	if err := json.Unmarshal([]byte(`{"idempotencyKey":"two","snapshotToken":"snapshot","source":{"kind":"publishedOutput","publishedOutput":{"revisionId":"r","outputId":"o","filters":[{"column":"quantity","op":"GT","value":12}]}}}`), &published); err != nil {
		t.Fatal(err)
	}
	filter := (*published.Source.PublishedOutput.Filters)[0]
	if filter.Column != "quantity" || filter.Op != "GT" || filter.Value != float64(12) {
		t.Fatalf("filter changed: %+v", filter)
	}
}
