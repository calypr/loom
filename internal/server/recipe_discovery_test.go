package server

import (
	"context"
	"testing"

	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func TestRecipeFieldDiscoverySkipsLoomMetadata(t *testing.T) {
	discovery := queryapi.NewRecipeFieldDiscovery(func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{
			{ResourceType: "DocumentReference", Path: "project_id", Kind: "scalar"},
			{ResourceType: "DocumentReference", Path: "status", Kind: "scalar"},
		}, nil
	})

	fields, err := discovery.Fields(context.Background(), schema.Scope{}, "DocumentReference")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Path != "status" {
		t.Fatalf("discovered fields = %#v", fields)
	}
}

func TestRecipeFieldDiscoveryCarriesExtensionObservations(t *testing.T) {
	discovery := queryapi.NewRecipeFieldDiscovery(func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{{ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", ExtensionValues: []catalog.ExtensionValueObservation{{URL: "http://example.org/source_path", SourcePath: "content[].attachment.extension[]", ValuePath: "valueUrl", ValueType: "string"}}}}, nil
	})
	fields, err := discovery.Fields(context.Background(), schema.Scope{}, "DocumentReference")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || len(fields[0].ExtensionValues) != 1 || fields[0].ExtensionValues[0].ValuePath != "valueUrl" {
		t.Fatalf("extension observations = %#v", fields)
	}
}
