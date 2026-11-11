// Read-side catalog discovery over persisted field-profile rows
// (pg-runtime-first-query-route, Ask C). The Arango adapter's
// DiscoverFields / DiscoverExistingAuthResourcePaths over collection
// fhir_field_catalog (internal/catalog/arango/fields.go, resources.go) is
// the behavioral reference: identical option semantics, the same concrete-
// type law, and the same restricted-empty no-query rule. There is no
// fallback: a store failure surfaces wrapped, never as an empty "success".
//
// Two registered divergences from the Arango reference:
//   - Ordering. populatedFieldsAQL sorts resource_type, doc_count DESC,
//     path; the persistence boundary's ReadFieldProfileRowsFiltered orders
//     resource_type, path (its declared order) and this adapter adds no
//     comparator, so PostgreSQL discovery returns that order verbatim.
//     It is a registered divergence, not a defect: the query path does not
//     contract an AQL ordering for field discovery.
//   - AllGenerations auth-path listing (F-B27 project-level write
//     authorization) is a typed refusal on this lane, not an implicit
//     result. Constructing the cross-generation union needs a dataset-
//     series generation enumeration this read lane does not own.
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

// ErrAllGenerationsWithGeneration reports the unsupported option
// combination of AllGenerations with a selected dataset generation,
// mirroring the Arango adapter's F-2 law.
var ErrAllGenerationsWithGeneration = errors.New("AllGenerations conflicts with a selected datasetGeneration")

// ErrAllGenerationsUnsupported is the typed refusal of the cross-generation
// auth-path union on the PostgreSQL read lane. It is explicit and
// fail-closed: absence of the union is never presented as an empty truth,
// because an empty list would hit the zero-path bootstrap branch of
// write authorization (the F-B27 bypass posture).
var ErrAllGenerationsUnsupported = errors.New(
	"postgres catalog: all-generations auth-path discovery is not available on the PostgreSQL read lane")

// DiscoverFields reads persisted field-profile rows as the populated-field
// view of one project/generation, mirroring the Arango adapter's option
// semantics: explicit-mode authorization (unrestricted wins over stray
// paths; restricted with no authorized path is a legitimate state that
// reads nothing and must not query at all), the same silent skip of rows
// whose resource type is not concrete/generated, and the pivot-only
// filter. CursorBatch is accepted for interface parity and has no effect:
// the persistence boundary streams through the pool's own cursor.
//
// The returned order is the persistence boundary's declared order
// (resource_type, path) verbatim; see the file comment for the registered
// divergence from populatedFieldsAQL's sort.
func (s *Store) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	fields := []catalog.PopulatedField{}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	authPathsUnrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !authPathsUnrestricted && len(opts.AuthResourcePaths) == 0 {
		// Restricted with no authorized path is a legitimate authorization
		// state that reads nothing — and it must not query at all,
		// mirroring DiscoverFieldEnrichment's no-query law.
		return fields, nil
	}
	// Unrestricted mode wins over stray paths, matching the Arango adapter,
	// which ignores the path bind in unrestricted mode.
	var authPaths []string
	if !authPathsUnrestricted {
		authPaths = append([]string(nil), opts.AuthResourcePaths...)
	}
	// The persistence boundary reads nothing for an empty declared type
	// list (there is no implicit "all types" there), so the Arango
	// "resource_type == null means all" binding maps to the full generated
	// concrete set here. A named type binds through unchanged, byte-for-
	// byte, exactly as the AQL equality compares it.
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
		// The scope-level profile_status marker is persistence metadata,
		// never a field — the same rule populatedFieldsAQL expresses with
		// FILTER d.record_type != "profile_status".
		if doc.RecordType == catalog.FieldProfileStatusRecordType {
			continue
		}
		if opts.PivotOnly && !doc.PivotCandidate {
			continue
		}
		// Silent skip, not a diagnostic: DiscoverFields in the reference
		// adapter drops non-concrete rows without comment (the enrichment
		// reader is the diagnostic-emitting surface).
		resourceType, ok := fhirschema.ConcreteResourceType(doc.ResourceType)
		if !ok {
			continue
		}
		fields = append(fields, populatedFieldFromProfileDocument(doc, resourceType))
	}
	return fields, nil
}

// DiscoverExistingAuthResourcePaths lists the distinct non-empty auth
// paths recorded for one project/generation, derived from the same
// persisted field-profile rows the Arango adapter collects over
// fhir_field_catalog. Persisted profile_status rows (the truncated-empty
// marker the profiler emits into the same table) contribute their scope's
// path, matching the reference COLLECT over every document; a known-empty
// scope persists no row on either backend and so lists nothing on either.
// The result is ascending-sorted and deduplicated.
//
// CursorBatch is accepted for parity and has no effect. AllGenerations
// with a selected generation is the F-2 conflict error; AllGenerations
// alone is the typed ErrAllGenerationsUnsupported refusal (file comment).
func (s *Store) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	if opts.AllGenerations {
		if datasetGeneration != "" {
			return nil, fmt.Errorf("%w: datasetGeneration %q must be empty when AllGenerations is set",
				ErrAllGenerationsWithGeneration, opts.DatasetGeneration)
		}
		return nil, fmt.Errorf("%w: project %q requires the dataset-series generation enumeration of pg-runtime-server-composition",
			ErrAllGenerationsUnsupported, opts.Project)
	}
	// Every recorded row of the scope is a path witness regardless of
	// record type or concrete-type law: existingAuthResourcePathsAQL has
	// no resource_type filter at all and COLLECTs auth_resource_path over
	// the whole project/generation scope, keeping paths from non-concrete
	// rows (custom, backbone, abstract) too. The read therefore uses the
	// persistence boundary's explicit AllResourceTypes mode -- restricting
	// to fhirschema.ResourceTypes() would silently drop a path that only
	// exists on non-concrete rows, and this lister feeds scope resolution
	// through wireAuth (review r4543172882). The concrete-type law stays
	// exactly where the reference applies it: the silent skip inside
	// DiscoverFields, never here.
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

// populatedFieldFromProfileDocument maps one persisted field record to the
// neutral read-side view. The concrete resource type is the adapter-
// canonicalized value; every other field is the persisted document's own,
// defensively copied for slices so a caller can never mutate the adapter's
// read result in place.
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
