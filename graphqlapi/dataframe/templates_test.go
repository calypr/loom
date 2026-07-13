package dataframeapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

func TestListTemplatesUsesCatalogEvidenceAndScope(t *testing.T) {
	var fieldOptions []catalog.PopulatedFieldOptions
	var referenceOptions []catalog.PopulatedReferenceOptions
	service := NewService(Config{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldOptions = append(fieldOptions, options)
			switch options.ResourceType {
			case "Patient":
				return []catalog.PopulatedField{
					{ResourceType: "Patient", Path: "identifier[].value"},
					{ResourceType: "Patient", Path: "gender"},
					{ResourceType: "Patient", Path: "birthDate"},
				}, nil
			default:
				return []catalog.PopulatedField{}, nil
			}
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceOptions = append(referenceOptions, options)
			if options.NodeType == "Patient" {
				return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 3}}, nil
			}
			return []catalog.PopulatedReference{}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"scope/a"}})
	got, err := service.ListTemplates(ctx, TemplateOptions{Project: "P1", AuthResourcePaths: []string{"scope/a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0].ID != "patient-cohort" || got[5].ID != "study-enrollment" {
		t.Fatalf("unexpected template order/result: %#v", got)
	}
	patient := got[0]
	if patient.Status != "PARTIAL" || patient.RootResourceType != "Patient" {
		t.Fatalf("patient availability = %#v", patient)
	}
	if len(patient.Starter.Fields) != 3 || patient.Starter.Fields[0].FieldRef != "Patient.identifier_value" {
		t.Fatalf("patient starter = %#v", patient.Starter)
	}
	if len(fieldOptions) == 0 || len(referenceOptions) == 0 {
		t.Fatalf("expected catalog calls, fields=%d references=%d", len(fieldOptions), len(referenceOptions))
	}
	for _, options := range fieldOptions {
		if options.Project != "P1" || options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted {
			t.Fatalf("field scope was not explicit/restricted: %+v", options)
		}
	}
	for _, options := range referenceOptions {
		if options.Project != "P1" || options.Mode != catalog.TraversalModeBuilder || options.AuthResourcePaths[0] != "scope/a" {
			t.Fatalf("reference scope was not propagated: %+v", options)
		}
	}
}

func TestListTemplatesFiltersUnknownTemplateWithoutCatalogReads(t *testing.T) {
	fieldCalls := 0
	service := NewService(Config{
		DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldCalls++
			return nil, nil
		},
		DiscoverReferences: func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			t.Fatal("unexpected reference discovery for unknown template")
			return nil, nil
		},
	})
	got, err := service.ListTemplates(context.Background(), TemplateOptions{Project: "P1", TemplateID: "not-a-template"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || fieldCalls != 0 {
		t.Fatalf("unknown template result=%#v fieldCalls=%d", got, fieldCalls)
	}
}

func TestListTemplatesRejectsUnauthorizedProject(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "u1", Projects: []string{"P1"}})
	if _, err := service.ListTemplates(ctx, TemplateOptions{Project: "P2"}); err == nil {
		t.Fatal("expected project authorization error")
	}
}
