package dataframebuilder

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func TestServiceIntrospectUsesCallerScope(t *testing.T) {
	var gotRefOpts catalog.PopulatedReferenceOptions
	var gotFieldOpts []catalog.PopulatedFieldOptions

	service := NewService(Config{
		DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			gotRefOpts = opts
			return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 7}}, nil
		},
		DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			gotFieldOpts = append(gotFieldOpts, opts)
			if opts.PivotOnly {
				return []catalog.PopulatedField{{ResourceType: opts.ResourceType, Path: "valueCodeableConcept", PivotCandidate: true, PivotKind: "codeable_concept_display_value", PivotColumns: []string{"A"}}}, nil
			}
			return []catalog.PopulatedField{{ResourceType: opts.ResourceType, Path: "identifier[].value", Kind: "scalar", DistinctValues: []string{"x"}}}, nil
		},
	})

	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "u1",
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA", "pathB"},
	})

	resp, err := service.Introspect(ctx, IntrospectionRequest{
		Project:                "P1",
		RootResourceType:       "Patient",
		IncludePivotOnlyFields: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRefOpts.Project != "P1" || gotRefOpts.NodeType != "Patient" || gotRefOpts.Mode != catalog.TraversalModeBuilder {
		t.Fatalf("unexpected reference opts: %+v", gotRefOpts)
	}
	if len(gotRefOpts.AuthResourcePaths) != 2 || gotRefOpts.AuthResourcePaths[0] != "pathA" || gotRefOpts.AuthResourcePaths[1] != "pathB" {
		t.Fatalf("unexpected reference auth scope: %+v", gotRefOpts.AuthResourcePaths)
	}
	if len(gotFieldOpts) != 4 {
		t.Fatalf("discoverFields call count = %d, want 4", len(gotFieldOpts))
	}
	if len(resp.AuthResourcePaths) != 2 || resp.AuthResourcePaths[0] != "pathA" || resp.AuthResourcePaths[1] != "pathB" {
		t.Fatalf("unexpected auth scope: %#v", resp.AuthResourcePaths)
	}
	if len(resp.Traversals) != 1 || len(resp.Fields) == 0 || len(resp.PivotFields) != 1 {
		t.Fatalf("unexpected response sizes: %+v", resp)
	}
	if resp.Root.ResourceType != "Patient" || len(resp.RelatedResources) != 1 {
		t.Fatalf("unexpected structured introspection: %+v", resp)
	}
	if resp.RelatedResources[0].ViaLabel != "subject_Patient" || resp.RelatedResources[0].Target.ResourceType != "Specimen" {
		t.Fatalf("unexpected related resource hint: %+v", resp.RelatedResources[0])
	}
	if resp.Fields[0].Selector.ValuePath == "" {
		t.Fatalf("expected structured selector decomposition: %+v", resp.Fields[0])
	}
}

func TestServiceIntrospectRejectsUnauthorizedScope(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "u1",
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})

	_, err := service.Introspect(ctx, IntrospectionRequest{
		Project:           "P1",
		RootResourceType:  "Patient",
		AuthResourcePaths: []string{"pathB"},
	})
	if err == nil {
		t.Fatal("expected auth scope error")
	}
}

func TestServiceIntrospectRejectsUnauthorizedProject(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:  "u1",
		Projects: []string{"P1"},
	})

	_, err := service.Introspect(ctx, IntrospectionRequest{
		Project:          "P2",
		RootResourceType: "Patient",
	})
	if err == nil {
		t.Fatal("expected project authorization error")
	}
}
