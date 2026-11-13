package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var ErrFieldProfileWrite = errors.New("postgres catalog: field profile write failed")

var ErrInvalidFieldProfile = errors.New("postgres catalog: invalid field profile")

var ErrFieldProfileUnavailable = errors.New(
	"field profile is unavailable; profile this scope against the PostgreSQL store before reading field enrichment")

type FieldProfileUnavailableError struct {
	Project           string
	DatasetGeneration string
	Reason            string
}

func (e *FieldProfileUnavailableError) Error() string {
	return fmt.Sprintf("%v: project %q generation %q: %s",
		ErrFieldProfileUnavailable, e.Project, e.DatasetGeneration, e.Reason)
}

func (e *FieldProfileUnavailableError) Unwrap() error {
	return ErrFieldProfileUnavailable
}

type FieldProfileWriteError struct {
	Scope storepostgres.FieldProfileScope
	Err   error
}

func (e *FieldProfileWriteError) Error() string {
	return fmt.Sprintf("postgres catalog: field profile write failed for %s/%s (auth path %q, type %s): %v; "+
		"the batch was atomic, nothing it named was replaced",
		e.Scope.Project, e.Scope.DatasetGeneration, e.Scope.AuthResourcePath, e.Scope.ResourceType, e.Err)
}

func (e *FieldProfileWriteError) Unwrap() []error {
	return []error{ErrFieldProfileWrite, e.Err}
}

func (s *FieldProfileAdapter) WriteFieldProfile(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, batchSize int) error {
	if err := requireLeg("field-profile", s.profiles != nil, "WriteFieldProfile"); err != nil {
		return err
	}
	scope.DatasetGeneration = catalog.NormalizeDatasetGeneration(scope.DatasetGeneration)
	if err := validateFieldProfileBatchScope(scope, docs); err != nil {
		return err
	}
	ordered, coverage := prepareFieldProfileBatch(scope, docs)
	if err := s.profiles.UpsertFieldProfileBatch(ctx, scope, ordered, coverage, batchSize); err != nil {
		return &FieldProfileWriteError{Scope: scope, Err: err}
	}
	return nil
}

func (s *FieldProfileAdapter) ReadFieldProfile(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	if err := requireLeg("field-profile", s.profiles != nil, "ReadFieldProfile"); err != nil {
		return nil, err
	}
	scope.DatasetGeneration = catalog.NormalizeDatasetGeneration(scope.DatasetGeneration)
	if strings.TrimSpace(scope.Project) == "" || strings.TrimSpace(scope.ResourceType) == "" {
		return nil, fmt.Errorf("%w: project and resource type are required", ErrInvalidFieldProfile)
	}
	docs, err := s.profiles.ReadFieldProfileRows(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: read field profile %s/%s (auth path %q, type %s): %w",
			scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType, err)
	}
	return docs, nil
}

func (s *FieldProfileAdapter) DiscoverFieldEnrichment(ctx context.Context, opts catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: true, Diagnostics: diagnostics}
	if err := requireLeg("field-profile", s.profiles != nil, "DiscoverFieldEnrichment"); err != nil {
		return unavailableFieldEnrichment(result, err.Error()), err
	}
	sawExplicitCoverage, sawLegacyCoverage := false, false
	recordCoverage := func(complete *bool) {
		if complete != nil {
			sawExplicitCoverage = true
			if !*complete {
				result.Complete = false
			}
		} else {
			sawLegacyCoverage = true
		}
	}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	authPathsUnrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !authPathsUnrestricted && len(opts.AuthResourcePaths) == 0 {
		result.Digest, _ = catalog.FieldEnrichmentDigest(result.Values, result.FieldCoverageKnown, result.Complete, result.Truncated)
		result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
		return result, nil
	}
	var authPaths []string
	if !authPathsUnrestricted {
		authPaths = append([]string(nil), opts.AuthResourcePaths...)
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
		return unavailableFieldEnrichment(result, err.Error()), fmt.Errorf("postgres catalog: read field enrichment %s/%s: %w", opts.Project, datasetGeneration, err)
	}
	for _, doc := range rows {
		if doc.RecordType == catalog.FieldProfileStatusRecordType {
			recordCoverage(doc.EnrichmentComplete)
			if doc.EnrichmentTruncated {
				result.Truncated = true
			}
			continue
		}
		resourceType, ok := fhirschema.ConcreteResourceType(doc.ResourceType)
		if !ok {
			result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "INVALID_FIELD_RESOURCE_TYPE", Message: "field enrichment resource type is not concrete/generated", ResourceType: doc.ResourceType})
			continue
		}
		result.Values = append(result.Values, fieldObservationFromProfileDocument(doc, resourceType))
		if doc.EnrichmentTruncated {
			result.Truncated = true
		}
		recordCoverage(doc.EnrichmentComplete)
	}
	if len(rows) == 0 {
		coverage, err := s.profiles.ReadProfileCoverage(ctx, filter)
		if err != nil {
			return unavailableFieldEnrichment(result, err.Error()), fmt.Errorf("postgres catalog: read field profile coverage %s/%s: %w", opts.Project, datasetGeneration, err)
		}
		if len(coverage) == 0 {
			return unavailableFieldEnrichment(result, "field profile was never persisted for the requested scope"),
				&FieldProfileUnavailableError{Project: opts.Project, DatasetGeneration: datasetGeneration, Reason: "no persisted profile or coverage marker for the requested scope"}
		}
		for _, marker := range coverage {
			recordCoverage(marker.EnrichmentComplete)
			if marker.EnrichmentTruncated {
				result.Truncated = true
			}
		}
	}
	result.FieldCoverageKnown = sawExplicitCoverage && !sawLegacyCoverage
	if result.Truncated {
		result.Complete = false
	}
	sort.Slice(result.Values, func(i, j int) bool { return fieldEnrichmentLess(result.Values[i], result.Values[j]) })
	result.Digest, _ = catalog.FieldEnrichmentDigest(result.Values, result.FieldCoverageKnown, result.Complete, result.Truncated)
	result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

func fieldObservationFromProfileDocument(doc catalog.FieldCatalogDocument, resourceType string) catalog.FieldEnrichmentObservation {
	return catalog.FieldEnrichmentObservation{
		Project: doc.Project, DatasetGeneration: doc.DatasetGeneration, AuthResourcePath: doc.AuthResourcePath,
		ResourceType: resourceType, Path: doc.Path, Kind: doc.Kind,
		DocCount: doc.DocCount, MaxItems: doc.MaxItems, SampleCount: doc.SampleCount,
		DistinctValues: append([]string(nil), doc.DistinctValues...), DistinctTruncated: doc.DistinctTruncated,
		ExtensionValues:      append([]catalog.ExtensionValueObservation(nil), doc.ExtensionValues...),
		SemanticObservations: append([]catalog.SemanticObservation(nil), doc.SemanticObservations...),
		PivotCandidate:       doc.PivotCandidate, PivotKind: doc.PivotKind,
		PivotColumns: append([]string(nil), doc.PivotColumns...),
		PivotFamily:  doc.PivotFamily, PivotColumnSelect: doc.PivotColumnSelect, PivotValueSelect: doc.PivotValueSelect,
		PivotItemSource: doc.PivotItemSource, PivotItemResourceType: doc.PivotItemResourceType,
		PivotValueSelectors: append([]string(nil), doc.PivotValueSelectors...),
	}
}

func validateFieldProfileBatchScope(scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument) error {
	if strings.TrimSpace(scope.Project) == "" || strings.TrimSpace(scope.ResourceType) == "" {
		return fmt.Errorf("%w: scope project and resource type are required", ErrInvalidFieldProfile)
	}
	seen := make(map[string]struct{}, len(docs))
	var (
		firstSeen      bool
		firstTruncated bool
		firstComplete  *bool
	)
	for i := range docs {
		doc := docs[i]
		if doc.Project != scope.Project || doc.DatasetGeneration != scope.DatasetGeneration ||
			doc.AuthResourcePath != scope.AuthResourcePath || doc.ResourceType != scope.ResourceType {
			return fmt.Errorf("%w: document %d scope (%s/%s/%s/%s) diverges from batch scope (%s/%s/%s/%s); one batch persists exactly one profiler scope",
				ErrInvalidFieldProfile, i,
				doc.Project, doc.DatasetGeneration, doc.AuthResourcePath, doc.ResourceType,
				scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType)
		}
		identity := doc.RecordType + "\x00" + doc.Path
		if _, dup := seen[identity]; dup {
			return fmt.Errorf("%w: document %d repeats identity path %q in one batch", ErrInvalidFieldProfile, i, doc.Path)
		}
		seen[identity] = struct{}{}
		if doc.SampleCount != len(doc.DistinctValues) {
			return fmt.Errorf("%w: document %d (path %q) sample_count is %d but retains %d distinct values; the profiler sets sample_count to the retained value count",
				ErrInvalidFieldProfile, i, doc.Path, doc.SampleCount, len(doc.DistinctValues))
		}
		if !firstSeen {
			firstSeen = true
			firstTruncated = doc.EnrichmentTruncated
			firstComplete = doc.EnrichmentComplete
		} else if firstTruncated != doc.EnrichmentTruncated ||
			(firstComplete == nil) != (doc.EnrichmentComplete == nil) ||
			(firstComplete != nil && doc.EnrichmentComplete != nil && *firstComplete != *doc.EnrichmentComplete) {
			return fmt.Errorf("%w: document %d enrichment flags (complete=%v truncated=%v) disagree with the batch's first document (complete=%v truncated=%v); one profiler run reports one completeness state",
				ErrInvalidFieldProfile, i, boolPtr(doc.EnrichmentComplete), doc.EnrichmentTruncated, boolPtr(firstComplete), firstTruncated)
		}
	}
	return nil
}

func prepareFieldProfileBatch(scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument) ([]catalog.FieldCatalogDocument, storepostgres.FieldProfileCoverage) {
	ordered := append([]catalog.FieldCatalogDocument(nil), docs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].RecordType != ordered[j].RecordType {
			return ordered[i].RecordType < ordered[j].RecordType
		}
		return ordered[i].Path < ordered[j].Path
	})
	coverage := storepostgres.FieldProfileCoverage{FieldProfileScope: scope}
	if len(ordered) == 0 {
		complete := true
		coverage.EnrichmentComplete = &complete
		return ordered, coverage
	}
	coverage.EnrichmentComplete = ordered[0].EnrichmentComplete
	coverage.EnrichmentTruncated = ordered[0].EnrichmentTruncated
	return ordered, coverage
}

func unavailableFieldEnrichment(result catalog.FieldEnrichmentResult, message string) catalog.FieldEnrichmentResult {
	result.Values = []catalog.FieldEnrichmentObservation{}
	result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
	result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "FIELD_ENRICHMENT_UNAVAILABLE", Message: message})
	return result
}

func fieldEnrichmentLess(a, b catalog.FieldEnrichmentObservation) bool {
	for _, pair := range [][2]string{
		{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration},
		{a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType},
		{a.Path, b.Path}, {a.Kind, b.Kind},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocCount < b.DocCount
}

func boolPtr(value *bool) bool {
	return value != nil && *value
}
