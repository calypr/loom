package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func TestRecipeCatalogDiscoverySkipsLoomMetadata(t *testing.T) {
	discovery := recipeCatalogDiscovery{read: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{
			{ResourceType: "DocumentReference", Path: "project_id", Kind: "scalar"},
			{ResourceType: "DocumentReference", Path: "status", Kind: "scalar"},
		}, nil
	}}

	fields, err := discovery.Fields(context.Background(), schema.Scope{}, "DocumentReference")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Path != "status" {
		t.Fatalf("discovered fields = %#v", fields)
	}
}
