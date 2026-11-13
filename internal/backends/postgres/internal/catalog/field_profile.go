// Bounded field-profile records: persist the write-side catalog.Profiler's
// deterministic Documents() into PostgreSQL and read them back without
// losing completeness or truncation metadata (pg-catalog-01).
//
// The Arango adapter (internal/backends/arango/internal/catalog/fields.go, collection
// fhir_field_catalog) is the behavioral reference: identical option
// semantics, evidence statuses, digests, and scope rules. Query identity
// (project/generation/auth path/resource type/path) is normalized into
// typed columns; JSONB is reserved for the correlated structured subrecords
// (extension values and semantic observations), where each element is one
// correlated observation, so a key and the value observed on the same owning
// item can never be flattened into independently cross-combinable sets.
//
// Persistence states that must stay distinguishable, and how:
//   - Complete profile: field rows with enrichment_complete = true.
//   - Truncated profile: the profiler's own distinct/enrichment truncation
//     flags are stored per row, so a bounded summary is read as bounded and
//     the discovery result reports EvidenceTruncated, never a clean pass.
//   - Truncated-empty profile: the profiler emits a scope-level
//     profile_status record when enrichment truncated with no fields
//     retained; it is persisted and drives the discovery status exactly as
//     the Arango adapter does.
//   - Known-empty profile: Documents() emits zero documents for a clean
//     empty population, so WriteFieldProfile persists a scope-level
//     coverage marker; discovery then reports EvidenceEmpty with field
//     coverage known.
//   - Unavailable profile: a scope with no rows and no coverage marker was
//     never profiled against this store; discovery returns a typed
//     *FieldProfileUnavailableError instead of presenting absence as an
//     empty truth (the same fail-closed posture as the pg-catalog-02
//     missing-marker rule).
//
// Upsert replaces exactly the identity named by each document (full
// identity primary key, never the derived key, whose legacy derivation is
// many-to-one); the same path under another project/generation/auth path is
// a different identity and stays a separate row. Missing versus JSON-null
// versus SQL-NULL is preserved: enrichment_complete is a nullable boolean
// (*bool nil is SQL NULL), and the jsonb subrecord columns keep SQL NULL
// (missing) distinct from a populated array.
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

// ErrFieldProfileWrite reports a field-profile batch the persistence
// boundary refused; nothing from the refused batch became visible.
var ErrFieldProfileWrite = errors.New("postgres catalog: field profile write failed")

// ErrInvalidFieldProfile reports a profiler output the adapter refuses
// before any statement runs: a document whose scope diverges from the batch
// scope, a duplicate identity within one batch, inconsistent enrichment
// flags across one profiler run, or a sample count that disagrees with its
// distinct values. There is no silent correction or fallback.
var ErrInvalidFieldProfile = errors.New("postgres catalog: invalid field profile")

// ErrFieldProfileUnavailable reports that no field profile has been
// persisted for the requested scope (no rows and no coverage marker). The
// scope must be profiled against the PostgreSQL store before its field
// enrichment becomes readable.
var ErrFieldProfileUnavailable = errors.New(
	"field profile is unavailable; profile this scope against the PostgreSQL store before reading field enrichment")

// FieldProfileUnavailableError names why no persisted field profile exists
// for one scope. It matches errors.Is(err, ErrFieldProfileUnavailable).
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

// FieldProfileWriteError reports a field-profile batch that failed inside
// the persistence boundary. The batch is transactional: nothing it named was
// replaced. It matches errors.Is(err, ErrFieldProfileWrite) and chains the
// store error.
type FieldProfileWriteError struct {
	Scope storepostgres.FieldProfileScope
	Err   error
}

func (e *FieldProfileWriteError) Error() string {
	return fmt.Sprintf("postgres catalog: field profile write failed for %s/%s (auth path %q, type %s): %v; "+
		"the batch was atomic, nothing it named was replaced",
		e.Scope.Project, e.Scope.DatasetGeneration, e.Scope.AuthResourcePath, e.Scope.ResourceType, e.Err)
}

// Unwrap exposes both the class sentinel and the cause for errors.Is/As.
func (e *FieldProfileWriteError) Unwrap() []error {
	return []error{ErrFieldProfileWrite, e.Err}
}

// WriteFieldProfile persists one profiler run for exactly one scope: it
// upserts every document (replacing exactly its identity) and refreshes the
// scope's coverage marker in one transaction. Pass the documents emitted by
// Profiler.Documents() for a profiler built with this same scope;
// normalization of the empty generation follows the shared
// NormalizeDatasetGeneration contract.
//
// An empty document list persists the known-empty state of a scope whose
// profiler cleanly retained no fields: the coverage marker is written and
// discovery later reports EvidenceEmpty with field coverage known, not
// "unavailable". Any document whose scope diverges from the batch scope is
// a typed *ErrInvalidFieldProfile failure; the whole batch stays atomic.
func (s *Store) WriteFieldProfile(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, batchSize int) error {
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

// ReadFieldProfile returns the exact persisted documents for one scope --
// field records and any profile_status marker -- ordered by path in byte
// order. Every public field of catalog.FieldCatalogDocument, including
// false/empty values, the distinct-value bounds, and the completeness and
// truncation flags, round-trips unchanged from what WriteFieldProfile
// stored. A scope with nothing persisted returns an empty slice; the
// empty-versus-unavailable distinction is a discovery question answered by
// the coverage marker.
func (s *Store) ReadFieldProfile(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
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

// DiscoverFieldEnrichment reads persisted profiler rows as optional,
// fail-closed enrichment, mirroring the Arango adapter's semantics: a
// successful empty result is valid evidence, while an unavailable artifact
// is explicitly typed and a bounded summary is explicitly truncated. The
// per-field DistinctTruncated is retained on the observation without
// failing the artifact; only the profiler's own enrichment-truncation
// signal (a row or profile_status marker) marks the result truncated.
func (s *Store) DiscoverFieldEnrichment(ctx context.Context, opts catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
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
		// Restricted with no authorized path is a legitimate authorization
		// state that reads nothing — and it must not query at all.
		result.Digest, _ = catalog.FieldEnrichmentDigest(result.Values, result.FieldCoverageKnown, result.Complete, result.Truncated)
		result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
		return result, nil
	}
	// Unrestricted mode wins over stray paths, matching the Arango adapter,
	// which ignores the path bind in unrestricted mode.
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
		result.Values = append(result.Values, catalog.FieldEnrichmentObservation{
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
		})
		// DistinctTruncated is per-field suggestion metadata. It is retained
		// on the observation but does not make the complete enrichment
		// artifact unusable; only an artifact-level marker can fail the
		// snapshot.
		if doc.EnrichmentTruncated {
			result.Truncated = true
		}
		recordCoverage(doc.EnrichmentComplete)
	}
	if len(rows) == 0 {
		// No rows at all: the scope-level coverage markers decide whether
		// this is a known-empty profile or one that was never persisted.
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

// validateFieldProfileBatchScope enforces the profiler-run contract before
// any statement runs: every document must carry the batch scope exactly
// (generation normalized the same way the profiler emits it), one identity
// may appear only once per batch, the enrichment flags a single profiler
// run produced must be uniform across its documents, and SampleCount must
// agree with the retained distinct values (the profiler sets it to their
// length).
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

// prepareFieldProfileBatch orders documents by path (byte order, the
// profiler's own order) and derives the scope's coverage marker from the
// uniform flags one profiler run produced. A clean empty batch persists
// complete, untruncated coverage: the profiler emitted nothing because the
// population was empty, and its truncated-empty case always carries a
// profile_status document instead.
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
