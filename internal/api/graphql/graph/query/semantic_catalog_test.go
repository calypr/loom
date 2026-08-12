package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func TestServiceSemanticCatalogIsRootAndAuthScoped(t *testing.T) {
	var got catalog.PopulatedFieldOptions
	service := NewService(Config{
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			got = opts
			return []catalog.PopulatedField{{Project: "P1", ResourceType: opts.ResourceType, Path: "code", DocCount: 3, SemanticObservations: []catalog.SemanticObservation{{Source: catalog.SemanticObservationSource{Canonical: "Observation.code", Type: "Observation", Path: "code"}, Key: catalog.SemanticObservationKey{Selector: "code.coding[]", Code: "x"}, Value: catalog.SemanticObservationValue{Selector: "valueString", Type: "string"}, Cardinality: "single", Population: 2, Examples: []string{"safe"}, RuleHint: "OBSERVATION_CODE_VALUE", RuleVersion: "1"}}}}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"a"}})
	resp, err := service.SemanticCatalog(ctx, SemanticCatalogRequest{Project: "P1", RootResourceType: "Observation"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceType != "Observation" || len(got.AuthResourcePaths) != 1 || got.AuthResourcePaths[0] != "a" {
		t.Fatalf("scope/root disclosure: %+v", got)
	}
	if len(resp.Resources) != 1 || resp.Resources[0].ResourceType != "Observation" || resp.Resources[0].Families[0].Concepts[0].Examples.Values[0] != "safe" {
		t.Fatalf("unexpected catalog: %#v", resp)
	}
}

func TestServiceSemanticCatalogRejectsUnauthorizedProjectAndPath(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"a"}})
	if _, err := service.SemanticCatalog(ctx, SemanticCatalogRequest{Project: "P2", RootResourceType: "Observation"}); err == nil {
		t.Fatal("expected project authorization error")
	}
	if _, err := service.SemanticCatalog(ctx, SemanticCatalogRequest{Project: "P1", RootResourceType: "Observation", AuthResourcePaths: []string{"secret"}}); err == nil {
		t.Fatal("expected path authorization error")
	}
}
