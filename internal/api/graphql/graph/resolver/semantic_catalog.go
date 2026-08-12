package resolver

import (
	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func semanticCatalogModel(in *semantic.CatalogResult, authPaths []string) *model.DataframeSemanticConceptCatalog {
	out := &model.DataframeSemanticConceptCatalog{SchemaVersion: in.SchemaVersion, Project: in.Project, SourceGeneration: in.SourceGeneration, AuthResourcePaths: append([]string(nil), authPaths...), Resources: []*model.DataframeSemanticResource{}, Diagnostics: []*model.DataframeSemanticDiagnostic{}, Completeness: &model.DataframeSemanticCompleteness{State: in.Completeness.State, ResourceLimit: in.Completeness.ResourceLimit, ConceptLimitPerResource: in.Completeness.ConceptLimitPerResource, ReturnedResourceCount: in.Completeness.ReturnedResourceCount, ReturnedConceptCount: in.Completeness.ReturnedConceptCount}}
	for _, resource := range in.Resources {
		item := &model.DataframeSemanticResource{ResourceType: resource.ResourceType, DocumentCount: int(resource.DocumentCount), Families: []*model.DataframeSemanticFamily{}}
		for _, family := range resource.Families {
			f := &model.DataframeSemanticFamily{ID: family.ID, Label: family.Label, RuleID: family.RuleID, Precedence: family.Precedence, Concepts: []*model.DataframeSemanticConcept{}, Trace: semanticTraceModel(family.Trace)}
			for _, concept := range family.Concepts {
				f.Concepts = append(f.Concepts, semanticConceptModel(concept))
			}
			item.Families = append(item.Families, f)
		}
		item.Families = item.Families
		out.Resources = append(out.Resources, item)
	}
	for _, diagnostic := range in.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, &model.DataframeSemanticDiagnostic{Severity: diagnostic.Severity, Code: diagnostic.Code, RuleID: diagnostic.RuleID, Path: diagnostic.Path, Message: diagnostic.Message})
	}
	return out
}

func semanticConceptModel(in semantic.Concept) *model.DataframeSemanticConcept {
	return &model.DataframeSemanticConcept{ID: in.ID, Label: in.Label, Group: in.Group, Description: in.Description, RuleID: in.RuleID, RuleVersion: in.RuleVersion, Precedence: in.Precedence, Source: semanticSourceModel(in.Source), Output: semanticOutputModel(in.Output), Examples: &model.DataframeSemanticExamples{Values: append([]string(nil), in.Examples.Values...), Limited: in.Examples.Limited}, Trace: semanticTraceModel(in.Trace)}
}

func semanticSourceModel(in semantic.SourceDescriptor) *model.DataframeSemanticSource {
	return &model.DataframeSemanticSource{Canonical: in.Canonical, ResourceType: in.ResourceType, Path: in.Path, Profile: in.Profile, SourcePath: in.SourcePath, ValuePath: in.ValuePath, KeySelector: in.KeySelector, KeySystem: in.KeySystem, KeyCode: in.KeyCode, KeyDisplay: in.KeyDisplay, RuleVersion: in.RuleVersion, Shape: in.Shape, Primitive: in.Primitive, Repeated: in.Repeated, PopulationCount: int(in.PopulationCount), DistinctTruncated: in.DistinctTruncated}
}

func semanticOutputModel(in semantic.OutputDescriptor) *model.DataframeSemanticOutput {
	selection := in.Selection
	return &model.DataframeSemanticOutput{Mode: in.Mode, ValueType: in.ValueType, Cardinality: in.Cardinality, Generic: in.Generic, Selection: &model.DataframeSemanticSelection{Mode: selection.Mode, SourcePath: selection.SourcePath, KeySelector: selection.KeySelector, ValueSelector: selection.ValueSelector, ValueFallbacks: append([]string(nil), selection.ValueFallbacks...), ItemSource: selection.ItemSource, ItemResourceType: selection.ItemResourceType, Transforms: append([]string(nil), selection.Transforms...), Key: selection.Key}}
}

func semanticTraceModel(in semantic.TraceDescriptor) *model.DataframeSemanticTrace {
	return &model.DataframeSemanticTrace{ResourceType: in.ResourceType, RawPath: in.RawPath, RawKey: in.RawKey, RawValue: in.RawValue, RawCardinality: in.RawCardinality, SourcePath: in.SourcePath, ValuePath: in.ValuePath, Reference: in.Reference, RuleID: in.RuleID, RuleVersion: in.RuleVersion, Precedence: in.Precedence, Fallback: in.Fallback}
}
