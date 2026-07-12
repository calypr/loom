package dataframebuilder

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func TestServiceDiscoverGuidedUsesResolvedScopeAndExistingCatalogQueries(t *testing.T) {
	var gotFieldOptions catalog.PopulatedFieldOptions
	var gotReferenceOptions catalog.PopulatedReferenceOptions
	service := NewService(Config{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			gotFieldOptions = options
			return []catalog.PopulatedField{
				{Project: options.Project, ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 3, DistinctValues: []string{"female", "male"}},
			}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			gotReferenceOptions = options
			return []catalog.PopulatedReference{
				{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 3},
			}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "user-1",
		Projects:          []string{"project-a"},
		AuthResourcePaths: []string{"scope-a", "scope-b"},
	})

	snapshot, err := service.DiscoverGuided(ctx, GuidedDiscoveryRequest{Project: " project-a "})
	if err != nil {
		t.Fatalf("DiscoverGuided() error = %v", err)
	}
	if gotFieldOptions.Project != "project-a" || gotFieldOptions.ResourceType != "" || gotFieldOptions.PivotOnly {
		t.Fatalf("field options = %+v, want all scoped catalog fields", gotFieldOptions)
	}
	if len(gotFieldOptions.AuthResourcePaths) != 2 || gotFieldOptions.AuthResourcePaths[0] != "scope-a" || gotFieldOptions.AuthResourcePaths[1] != "scope-b" {
		t.Fatalf("field auth scope = %#v", gotFieldOptions.AuthResourcePaths)
	}
	if gotReferenceOptions.Project != "project-a" || gotReferenceOptions.Mode != catalog.TraversalModeStorage || gotReferenceOptions.FromType != "" || gotReferenceOptions.NodeType != "" {
		t.Fatalf("reference options = %+v, want all scoped storage relationships", gotReferenceOptions)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("snapshot validation = %v", err)
	}
	if snapshot.Dataset.Project != "project-a" || len(snapshot.Columns) != 1 || len(snapshot.Relationships.Entries) != 1 || len(snapshot.Filters) != 1 {
		t.Fatalf("guided snapshot = %+v", snapshot)
	}
}

func TestServiceDiscoverGuidedRejectsUnauthorizedProjectAndScope(t *testing.T) {
	service := NewService(Config{
		DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return nil, nil
		},
		DiscoverReferences: func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			return nil, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "user-1",
		Projects:          []string{"project-a"},
		AuthResourcePaths: []string{"scope-a"},
	})

	if _, err := service.DiscoverGuided(ctx, GuidedDiscoveryRequest{Project: "project-b"}); err == nil {
		t.Fatal("expected unauthorized project error")
	}
	if _, err := service.DiscoverGuided(ctx, GuidedDiscoveryRequest{Project: "project-a", AuthResourcePaths: []string{"scope-b"}}); err == nil {
		t.Fatal("expected unauthorized auth scope error")
	}
}
