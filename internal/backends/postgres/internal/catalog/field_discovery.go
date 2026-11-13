package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var ErrAllGenerationsWithGeneration = errors.New("AllGenerations conflicts with a selected datasetGeneration")

func (s *Store) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	if err := requireLeg("field-profile", s.profiles != nil, "DiscoverFields"); err != nil {
		return nil, err
	}
	fields := []catalog.PopulatedField{}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	authPathsUnrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !authPathsUnrestricted && len(opts.AuthResourcePaths) == 0 {
		return fields, nil
	}
	var authPaths []string
	if !authPathsUnrestricted {
		authPaths = append([]string(nil), opts.AuthResourcePaths...)
	}
	resourceTypes := []string{opts.ResourceType}
	if opts.ResourceType == "" {
		resourceTypes = fhirschema.ResourceTypes()
	}
	filter := storepostgres.FieldProfileFilter{
		Project:                       opts.Project,
		DatasetGeneration:             datasetGeneration,
		ResourceTypes:                 append([]string(nil), resourceTypes...),
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authPathsUnrestricted),
		AuthResourcePaths:             authPaths,
	}
	rows, err := s.profiles.ReadFieldProfileRowsFiltered(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: discover fields %s/%s: %w", opts.Project, datasetGeneration, err)
	}
	for _, doc := range rows {
		if doc.RecordType == catalog.FieldProfileStatusRecordType {
			continue
		}
		if opts.PivotOnly && !doc.PivotCandidate {
			continue
		}
		resourceType, ok := fhirschema.ConcreteResourceType(doc.ResourceType)
		if !ok {
			continue
		}
		fields = append(fields, populatedFieldFromProfileDocument(doc, resourceType))
	}
	return fields, nil
}

func (s *Store) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	if err := requireLeg("field-profile", s.profiles != nil, "DiscoverExistingAuthResourcePaths"); err != nil {
		return nil, err
	}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	if opts.AllGenerations {
		if datasetGeneration != "" {
			return nil, fmt.Errorf("%w: datasetGeneration %q must be empty when AllGenerations is set",
				ErrAllGenerationsWithGeneration, opts.DatasetGeneration)
		}
		paths, err := s.profiles.ListDistinctAuthResourcePaths(ctx, opts.Project)
		if err != nil {
			return nil, fmt.Errorf("postgres catalog: discover existing auth paths %s (all generations): %w", opts.Project, err)
		}
		sort.Strings(paths)
		return paths, nil
	}
	filter := storepostgres.FieldProfileFilter{
		Project:                       opts.Project,
		DatasetGeneration:             datasetGeneration,
		AllResourceTypes:              true,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	}
	rows, err := s.profiles.ReadFieldProfileRowsFiltered(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: discover existing auth paths %s/%s: %w", opts.Project, datasetGeneration, err)
	}
	result := make([]string, 0, 16)
	seen := make(map[string]struct{}, 16)
	for _, doc := range rows {
		path := doc.AuthResourcePath
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func populatedFieldFromProfileDocument(doc catalog.FieldCatalogDocument, resourceType string) catalog.PopulatedField {
	return catalog.PopulatedField{
		Project:               doc.Project,
		DatasetGeneration:     doc.DatasetGeneration,
		AuthResourcePath:      doc.AuthResourcePath,
		ResourceType:          resourceType,
		Path:                  doc.Path,
		Kind:                  doc.Kind,
		DocCount:              doc.DocCount,
		MaxItems:              doc.MaxItems,
		SampleCount:           doc.SampleCount,
		DistinctValues:        append([]string(nil), doc.DistinctValues...),
		DistinctTruncated:     doc.DistinctTruncated,
		ExtensionValues:       append([]catalog.ExtensionValueObservation(nil), doc.ExtensionValues...),
		SemanticObservations:  append([]catalog.SemanticObservation(nil), doc.SemanticObservations...),
		PivotCandidate:        doc.PivotCandidate,
		PivotKind:             doc.PivotKind,
		PivotColumns:          append([]string(nil), doc.PivotColumns...),
		PivotFamily:           doc.PivotFamily,
		PivotColumnSelect:     doc.PivotColumnSelect,
		PivotValueSelect:      doc.PivotValueSelect,
		PivotItemSource:       doc.PivotItemSource,
		PivotItemResourceType: doc.PivotItemResourceType,
		PivotValueSelectors:   append([]string(nil), doc.PivotValueSelectors...),
	}
}
