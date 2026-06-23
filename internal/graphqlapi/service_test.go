package graphqlapi

import (
	"context"
	"testing"

	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

func TestServiceIntrospectUsesCallerScope(t *testing.T) {
	var gotRefOpts proto.PopulatedReferenceOptions
	var gotFieldOpts []proto.PopulatedFieldOptions

	svc := NewService(ServiceConfig{
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			gotRefOpts = opts
			return []proto.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 7}}, nil
		},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			gotFieldOpts = append(gotFieldOpts, opts)
			if opts.PivotOnly {
				return []proto.PopulatedField{{ResourceType: opts.ResourceType, Path: "valueCodeableConcept", PivotCandidate: true, PivotKind: "codeable_concept_display_value", PivotColumns: []string{"A"}}}, nil
			}
			return []proto.PopulatedField{{ResourceType: opts.ResourceType, Path: "identifier[].value", Kind: "scalar", DistinctValues: []string{"x"}}}, nil
		},
	})

	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Subject:           "u1",
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA", "pathB"},
	})

	resp, err := svc.Introspect(ctx, IntrospectionRequest{
		Project:                "P1",
		RootResourceType:       "Patient",
		IncludePivotOnlyFields: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRefOpts.Project != "P1" || gotRefOpts.NodeType != "Patient" || gotRefOpts.Mode != proto.TraversalModeBuilder {
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
	svc := NewService(ServiceConfig{})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Subject:           "u1",
		Projects:          []string{"P1"},
		AuthResourcePaths: []string{"pathA"},
	})

	_, err := svc.Introspect(ctx, IntrospectionRequest{
		Project:           "P1",
		RootResourceType:  "Patient",
		AuthResourcePaths: []string{"pathB"},
	})
	if err == nil {
		t.Fatal("expected auth scope error")
	}
}

func TestServiceIntrospectRejectsUnauthorizedProject(t *testing.T) {
	svc := NewService(ServiceConfig{})
	ctx := writeapi.ContextWithPrincipal(context.Background(), &writeapi.Principal{
		Subject:  "u1",
		Projects: []string{"P1"},
	})

	_, err := svc.Introspect(ctx, IntrospectionRequest{
		Project:          "P2",
		RootResourceType: "Patient",
	})
	if err == nil {
		t.Fatal("expected project authorization error")
	}
}
