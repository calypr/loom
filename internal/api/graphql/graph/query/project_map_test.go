package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func TestServiceProjectMapReturnsOnlyPopulatedAuthorizedCatalogFacts(t *testing.T) {
	var fieldCalls []catalog.PopulatedFieldOptions
	var referenceCall catalog.PopulatedReferenceOptions
	service := NewService(Config{
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldCalls = append(fieldCalls, options)
			if options.PivotOnly {
				return []catalog.PopulatedField{{ResourceType: "Observation", Path: "component[]", Kind: "array", DocCount: 8, PivotCandidate: true}}, nil
			}
			return []catalog.PopulatedField{
				{ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 4},
				{ResourceType: "Observation", Path: "status", Kind: "scalar", DocCount: 8},
			}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCall = options
			return []catalog.PopulatedReference{
				{FromType: "Patient", Label: "subject_Patient", ToType: "Observation", EdgeCount: 8},
				{FromType: "Patient", Label: "empty", ToType: "Condition", EdgeCount: 0},
			}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject: "user", Projects: []string{"P1"}, AuthResourcePaths: []string{"/programs/p/projects/P1"},
	})
	result, err := service.ProjectMap(ctx, ProjectMapRequest{Project: "P1", IncludePivotOnlyFields: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceGeneration != "generation-1" || len(result.Resources) != 2 || len(result.Relationships) != 1 {
		t.Fatalf("unexpected project map: %+v", result)
	}
	if result.Resources[0].ResourceType != "Observation" || result.Resources[1].ResourceType != "Patient" {
		t.Fatalf("resources are not deterministic: %+v", result.Resources)
	}
	if result.Resources[0].DocumentCount != 8 || result.Resources[1].DocumentCount != 4 {
		t.Fatalf("document counts are not derived from populated facts: %+v", result.Resources)
	}
	if len(result.Resources[1].Traversals) != 1 || result.Resources[1].Traversals[0].ToType != "Observation" {
		t.Fatalf("missing resource traversal: %+v", result.Resources[1])
	}
	if len(fieldCalls) != 2 || fieldCalls[0].ResourceType != "" || fieldCalls[0].PivotOnly || !fieldCalls[1].PivotOnly {
		t.Fatalf("unexpected field discovery: %+v", fieldCalls)
	}
	if referenceCall.NodeType != "" || referenceCall.Mode != catalog.TraversalModeBuilder || referenceCall.DatasetGeneration != "generation-1" {
		t.Fatalf("unexpected relationship discovery: %+v", referenceCall)
	}
	if len(referenceCall.AuthResourcePaths) != 1 || referenceCall.AuthResourcePaths[0] != "/programs/p/projects/P1" {
		t.Fatalf("authorization scope not forwarded: %+v", referenceCall.AuthResourcePaths)
	}
}

func TestServiceProjectMapAggregatesAuthorizedAuthResourceRows(t *testing.T) {
	service := NewService(Config{
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			if options.PivotOnly {
				return nil, nil
			}
			// These are the rows a scoped catalog query can return for two
			// authorized partitions. A hidden third partition must not affect
			// the result because it is not present in the authorized response.
			rows := []catalog.PopulatedField{
				{Project: "P1", DatasetGeneration: "generation-1", AuthResourcePath: "allowed-a", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 3, SampleCount: 1, DistinctValues: []string{"a"}},
				{Project: "P1", DatasetGeneration: "generation-1", AuthResourcePath: "allowed-b", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2, SampleCount: 1, DistinctValues: []string{"b"}},
				{Project: "P1", DatasetGeneration: "generation-1", AuthResourcePath: "allowed-a", ResourceType: "Patient", Path: "name", Kind: "scalar", DocCount: 2},
				{Project: "P1", DatasetGeneration: "generation-1", AuthResourcePath: "hidden", ResourceType: "Condition", Path: "id", Kind: "scalar", DocCount: 99},
			}
			visible := make([]catalog.PopulatedField, 0, len(rows))
			for _, row := range rows {
				for _, path := range options.AuthResourcePaths {
					if row.AuthResourcePath == path {
						visible = append(visible, row)
						break
					}
				}
			}
			return visible, nil
		},
		DiscoverReferences: func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			return []catalog.PopulatedReference{
				{FromType: "Patient", Label: "subject", ToType: "Observation", EdgeCount: 3},
				{FromType: "Patient", Label: "subject", ToType: "Observation", EdgeCount: 2},
				{FromType: "Patient", Label: "empty", ToType: "Condition", EdgeCount: 0},
			}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "user", Projects: []string{"P1"}, AuthResourcePaths: []string{"allowed-a", "allowed-b"}})
	result, err := service.ProjectMap(ctx, ProjectMapRequest{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resources) != 2 || result.Resources[0].ResourceType != "Observation" || result.Resources[1].ResourceType != "Patient" {
		t.Fatalf("unauthorized or duplicate resource types leaked: %+v", result.Resources)
	}
	patient := result.Resources[1]
	if patient.DocumentCount != 5 || len(patient.Fields) != 2 || patient.Fields[0].DocCount != 5 || patient.Fields[0].DistinctValues[0] != "a" || patient.Fields[0].DistinctValues[1] != "b" {
		t.Fatalf("authorized field rows were not merged deterministically: %+v", patient)
	}
	if len(result.Relationships) != 1 || result.Relationships[0].EdgeCount != 5 {
		t.Fatalf("authorized relationship rows were not aggregated: %+v", result.Relationships)
	}
}

func TestAggregatePopulatedFieldsDropsUnpopulatedRows(t *testing.T) {
	got := aggregatePopulatedFields([]catalog.PopulatedField{
		{ResourceType: "Patient", Path: "id", DocCount: 0},
		{ResourceType: "Patient", Path: "name", DocCount: -1},
	})
	if len(got) != 0 {
		t.Fatalf("aggregatePopulatedFields() = %#v, want no unpopulated facts", got)
	}
}

func TestServiceProjectMapRejectsUnauthorizedProject(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "user", Projects: []string{"P1"}})
	if _, err := service.ProjectMap(ctx, ProjectMapRequest{Project: "P2"}); err == nil {
		t.Fatal("expected authorization error")
	}
}
