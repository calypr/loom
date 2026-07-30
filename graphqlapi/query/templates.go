package queryapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframetemplate "github.com/calypr/loom/internal/dataframe/template"
)

// TemplateOptions is the transport-neutral input for guided template
// discovery. GraphQL schema ownership remains with the Part 5 coordinator.
type TemplateOptions struct {
	Project           string
	AuthResourcePaths []string
	TemplateID        string
}

type TemplateColumn struct {
	ID       string
	Label    string
	FieldRef string
	Advanced bool
}

type TemplateTraversal struct {
	ID           string
	Label        string
	SemanticRole string
	FromType     string
	EdgeLabel    string
	ToType       string
	Advanced     bool
}

type TemplatePivot struct {
	ID       string
	Label    string
	FieldRef string
	Columns  []string
	Advanced bool
}

type TemplateMissingCapability struct {
	SuggestionID string
	Kind         string
	Label        string
	Code         string
}

type TemplateStarterRequest struct {
	RootResourceType string
	RowGrain         string
	Fields           []TemplateColumn
	Traversals       []TemplateTraversal
	Pivots           []TemplatePivot
}

// TemplateAvailability is the GraphQL-dataframe service DTO. It contains no
// catalog persistence documents or raw selectors/AQL.
type TemplateAvailability struct {
	ID               string
	Version          int
	Label            string
	Description      string
	Status           string
	RootResourceType string
	CommonColumns    []TemplateColumn
	AdvancedColumns  []TemplateColumn
	Traversals       []TemplateTraversal
	Pivots           []TemplatePivot
	Missing          []TemplateMissingCapability
	Reasons          []string
	Starter          TemplateStarterRequest
}

// ListTemplates resolves all or one guided template against the current
// project's visible catalog. The active generation and effective auth scope
// are selected before any catalog reads.
func (s *Service) ListTemplates(ctx context.Context, req TemplateOptions) ([]TemplateAvailability, error) {
	if strings.TrimSpace(req.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authorizeProject(principal, req.Project, s.scopeResolver != nil); err != nil {
		return nil, err
	}
	generation, err := s.resolveActiveGeneration(ctx, req.Project)
	if err != nil {
		return nil, err
	}
	scope, err := s.resolveReadScopeForGeneration(ctx, principal, req.Project, generation, req.AuthResourcePaths)
	if err != nil {
		return nil, err
	}

	registry := dataframetemplate.DefaultRegistry()
	definitions := registry.Definitions()
	if id := strings.TrimSpace(req.TemplateID); id != "" {
		definition, ok := registry.Definition(id)
		if !ok {
			return []TemplateAvailability{}, nil
		}
		definitions = []dataframetemplate.Definition{definition}
	}
	snapshot, err := s.templateCapabilities(ctx, definitions, req.Project, generation, scope)
	if err != nil {
		return nil, err
	}
	result := make([]TemplateAvailability, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, templateAvailabilityDTO(dataframetemplate.Resolve(definition, snapshot)))
	}
	return result, nil
}

func (s *Service) templateCapabilities(ctx context.Context, definitions []dataframetemplate.Definition, project, generation string, scope authscope.ReadScope) (dataframetemplate.CapabilitySnapshot, error) {
	resourceTypes := make(map[string]struct{})
	for _, definition := range definitions {
		for _, resourceType := range definition.RootCandidates {
			resourceTypes[resourceType] = struct{}{}
		}
		for _, suggestion := range definition.SuggestedTraversals {
			for _, resourceType := range suggestion.FromResourceTypes {
				resourceTypes[resourceType] = struct{}{}
			}
			for _, resourceType := range suggestion.ToResourceTypes {
				resourceTypes[resourceType] = struct{}{}
			}
		}
	}

	orderedTypes := make([]string, 0, len(resourceTypes))
	for resourceType := range resourceTypes {
		orderedTypes = append(orderedTypes, resourceType)
	}
	// Definitions are product ordered; sorting resource types makes catalog
	// call order and resulting snapshots deterministic across map iteration.
	sortStrings(orderedTypes)

	snapshot := dataframetemplate.CapabilitySnapshot{Resources: []dataframetemplate.ResourceCapability{}, Relationships: []dataframetemplate.RelationshipCapability{}}
	seenRelationships := map[string]struct{}{}
	for _, resourceType := range orderedTypes {
		fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{
			ConnectionOptions: s.connOpts, Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
			AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths), ResourceType: resourceType,
		})
		if err != nil {
			return dataframetemplate.CapabilitySnapshot{}, err
		}
		fieldCapabilities := make([]dataframetemplate.FieldCapability, 0, len(fields))
		for _, field := range fields {
			fieldCapabilities = append(fieldCapabilities, dataframetemplate.FieldCapability{
				ResourceType: field.ResourceType, FieldRef: defaultFieldRef(resourceType, field.Path),
				PivotCandidate: field.PivotCandidate, PivotColumns: cloneStrings(field.PivotColumns),
				PivotFamily: field.PivotFamily, PivotColumnSelect: field.PivotColumnSelect,
				PivotValueSelect: field.PivotValueSelect,
			})
		}
		// A resource is advertised only when catalog evidence proves it is
		// visible. Schema presence alone is intentionally insufficient.
		resource := dataframetemplate.ResourceCapability{ResourceType: resourceType, Present: len(fields) > 0, Fields: fieldCapabilities}
		refs, err := s.discoverReferences(ctx, catalog.PopulatedReferenceOptions{
			ConnectionOptions: s.connOpts, Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(scope.Unrestricted()),
			AuthResourcePaths:             cloneStrings(scope.AuthResourcePaths), NodeType: resourceType,
			Mode: catalog.TraversalModeBuilder,
		})
		if err != nil {
			return dataframetemplate.CapabilitySnapshot{}, err
		}
		for _, ref := range refs {
			if ref.FromType == resourceType {
				resource.Present = true
			}
			key := ref.FromType + "\x00" + ref.Label + "\x00" + ref.ToType
			if _, ok := seenRelationships[key]; ok {
				continue
			}
			seenRelationships[key] = struct{}{}
			snapshot.Relationships = append(snapshot.Relationships, dataframetemplate.RelationshipCapability{FromType: ref.FromType, Label: ref.Label, ToType: ref.ToType, EdgeCount: ref.EdgeCount})
		}
		snapshot.Resources = append(snapshot.Resources, resource)
	}
	return snapshot, nil
}

func templateAvailabilityDTO(in dataframetemplate.Availability) TemplateAvailability {
	out := TemplateAvailability{
		ID: in.ID, Version: in.Version, Label: in.Label, Description: in.Description,
		Status: string(in.Status), RootResourceType: in.RootResourceType,
		CommonColumns: templateColumns(in.CommonColumns), AdvancedColumns: templateColumns(in.AdvancedColumns),
		Traversals: templateTraversals(in.Traversals), Pivots: templatePivots(in.Pivots),
		Missing: make([]TemplateMissingCapability, 0, len(in.Missing)), Reasons: cloneStrings(in.Reasons),
		Starter: TemplateStarterRequest{RootResourceType: in.Starter.RootResourceType, RowGrain: in.Starter.RowGrain,
			Fields: templateColumns(in.Starter.Fields), Traversals: templateTraversals(in.Starter.Traversals), Pivots: templatePivots(in.Starter.Pivots)},
	}
	for _, missing := range in.Missing {
		out.Missing = append(out.Missing, TemplateMissingCapability{SuggestionID: missing.SuggestionID, Kind: missing.Kind, Label: missing.Label, Code: missing.Code})
	}
	return out
}

func templateColumns(in []dataframetemplate.SelectedColumn) []TemplateColumn {
	out := make([]TemplateColumn, 0, len(in))
	for _, item := range in {
		out = append(out, TemplateColumn{ID: item.ID, Label: item.Label, FieldRef: item.FieldRef, Advanced: item.Advanced})
	}
	return out
}

func templateTraversals(in []dataframetemplate.SelectedTraversal) []TemplateTraversal {
	out := make([]TemplateTraversal, 0, len(in))
	for _, item := range in {
		out = append(out, TemplateTraversal{ID: item.ID, Label: item.Label, SemanticRole: item.SemanticRole, FromType: item.FromType, EdgeLabel: item.EdgeLabel, ToType: item.ToType, Advanced: item.Advanced})
	}
	return out
}

func templatePivots(in []dataframetemplate.SelectedPivot) []TemplatePivot {
	out := make([]TemplatePivot, 0, len(in))
	for _, item := range in {
		out = append(out, TemplatePivot{ID: item.ID, Label: item.Label, FieldRef: item.FieldRef, Columns: cloneStrings(item.Columns), Advanced: item.Advanced})
	}
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
