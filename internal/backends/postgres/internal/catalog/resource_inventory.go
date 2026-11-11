// Generation-scoped resource inventory: publish-last rebuild and marker-bound
// reads for the PostgreSQL source store (pg-catalog-02).
//
// RebuildResourceInventory scans every generated resource type inside one
// project/generation, groups retained rows by authorization path into exact
// zero-inclusive counts, assigns the shared content-addressed build identity
// (catalog.NewResourceInventoryBuild), commits every count row, and only
// then publishes the completeness marker. DiscoverResourceInventory binds
// all reads to the marker's build id and re-validates the marker, so rows
// from an abandoned or partial build — even though physically committed —
// never appear, and a stale or incomplete marker surfaces a typed
// unavailable error instead of a partial truth. A failed rebuild therefore
// leaves the prior marker and its read result intact.
//
// The Arango adapter (internal/backends/arango/internal/catalog) remains the behavioral
// reference: identical option semantics, evidence statuses, digests, and
// scope rules. CursorBatch is an Arango cursor control and has no PostgreSQL
// counterpart because grouping happens inside a single grouped SQL statement;
// BatchSize bounds the count-insert chunks.
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

// ErrResourceInventoryBuildUnavailable reports that no valid, complete,
// published marker exists for the requested project/generation (or that the
// published marker does not cover a requested type). The build must be
// rebuilt before its counts become readable.
var ErrResourceInventoryBuildUnavailable = errors.New(
	"resource inventory build is unavailable; run rebuild-resource-inventory --generation ID against the PostgreSQL store")

// ErrResourceInventoryWrite reports that a rebuild could not persist its
// counts or marker. The failed build was never published; the previously
// published marker and its read result are intact.
var ErrResourceInventoryWrite = errors.New("postgres catalog: resource inventory write failed")

// ResourceInventoryUnavailableError names why no usable published build
// exists for one scope. It matches errors.Is(err,
// ErrResourceInventoryBuildUnavailable).
type ResourceInventoryUnavailableError struct {
	Project           string
	DatasetGeneration string
	Reason            string
}

func (e *ResourceInventoryUnavailableError) Error() string {
	return fmt.Sprintf("%v: project %q generation %q: %s",
		ErrResourceInventoryBuildUnavailable, e.Project, e.DatasetGeneration, e.Reason)
}

func (e *ResourceInventoryUnavailableError) Unwrap() error {
	return ErrResourceInventoryBuildUnavailable
}

// ResourceInventoryWriteError reports a rebuild that failed while persisting
// counts ("counts") or the marker ("marker"). The named build was not
// published and the prior marker still governs reads. It matches
// errors.Is(err, ErrResourceInventoryWrite) and chains the store error.
type ResourceInventoryWriteError struct {
	Op                string // "counts" or "marker"
	Project           string
	DatasetGeneration string
	BuildID           string
	Err               error
}

func (e *ResourceInventoryWriteError) Error() string {
	return fmt.Sprintf("postgres catalog: resource inventory rebuild failed while writing %s for build %s (%s/%s): %v; "+
		"the build was not published and the prior marker still governs reads",
		e.Op, e.BuildID, e.Project, e.DatasetGeneration, e.Err)
}

// Unwrap exposes both the class sentinel and the cause for errors.Is/As.
func (e *ResourceInventoryWriteError) Unwrap() []error {
	return []error{ErrResourceInventoryWrite, e.Err}
}

// RebuildResourceInventory rebuilds the inventory for one exact
// project/generation. Empty opts.ResourceTypes means every generated
// concrete FHIR resource type, matching the Arango adapter. Every generated
// type receives an exact count row, including a zero row when it has no
// retained rows; rows are grouped per authorization path.
func (s *Store) RebuildResourceInventory(ctx context.Context, opts catalog.ResourceInventoryRebuildOptions) (catalog.ResourceInventoryRebuildSummary, error) {
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	if opts.Project == "" || datasetGeneration == "" {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild requires project and exact dataset generation")
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	if len(diagnostics) > 0 {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild has invalid resource types: %s", diagnostics[0].ResourceType)
	}
	if len(resourceTypes) == 0 {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild requires generated resource types")
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	counts := make([]catalog.ResourceInventoryCount, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		if err := ctx.Err(); err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, err
		}
		groups, err := s.inventory.ScanSourceCounts(ctx, opts.Project, datasetGeneration, resourceType)
		if err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("scan retained %s rows for resource inventory: %w", resourceType, err)
		}
		if len(groups) == 0 {
			// Zero-inclusive: an empty generated type is real evidence,
			// distinct from a missing row, with the legacy empty auth path.
			counts = append(counts, catalog.ResourceInventoryCount{
				Project: opts.Project, DatasetGeneration: datasetGeneration,
				ResourceType: resourceType, DocumentCount: 0,
			})
			continue
		}
		for _, group := range groups {
			if group.DocumentCount < 0 {
				return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("decode %s resource inventory count: negative count", resourceType)
			}
			counts = append(counts, catalog.ResourceInventoryCount{
				Project: opts.Project, DatasetGeneration: datasetGeneration,
				AuthResourcePath: group.AuthResourcePath, ResourceType: resourceType, DocumentCount: group.DocumentCount,
			})
		}
	}
	build, countRows, err := catalog.NewResourceInventoryBuild(opts.Project, datasetGeneration, resourceTypes, counts)
	if err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("prepare resource inventory build: %w", err)
	}
	// Publish-last: every count row commits first (in bounded chunks); the
	// marker — the only evidence readers trust — is written afterwards and
	// the store re-checks that these rows are durable before swapping it.
	if err := s.inventory.InsertCounts(ctx, countRows, batchSize); err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, &ResourceInventoryWriteError{
			Op: "counts", Project: opts.Project, DatasetGeneration: datasetGeneration, BuildID: build.BuildID, Err: err,
		}
	}
	if err := s.inventory.PublishBuild(ctx, build); err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, &ResourceInventoryWriteError{
			Op: "marker", Project: opts.Project, DatasetGeneration: datasetGeneration, BuildID: build.BuildID, Err: err,
		}
	}
	var documentCount int64
	for _, count := range countRows {
		documentCount += count.DocumentCount
	}
	return catalog.ResourceInventoryRebuildSummary{
		BuildID: build.BuildID, CoverageDigest: build.CoverageDigest, ResourceTypeCount: build.ResourceTypeCount,
		CountRowCount: build.CountRowCount, DocumentCount: documentCount,
	}, nil
}

// ReadResourceInventory is the explicit-read alias of
// DiscoverResourceInventory, mirroring the Arango capability boundary.
func (s *Store) ReadResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	return s.DiscoverResourceInventory(ctx, opts)
}

// DiscoverResourceInventory reads the publish-last inventory projection.
// Every count read binds the published marker's build id, so unmarked or
// abandoned count rows are invisible, and a stale or incomplete marker is a
// typed unavailable result rather than a partial answer.
func (s *Store) DiscoverResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.ResourceInventoryResult{Values: make([]catalog.ResourceInventoryObservation, 0, len(resourceTypes)), Available: true, Complete: true, Diagnostics: diagnostics}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)

	build, found, err := s.inventory.ReadBuild(ctx, opts.Project, datasetGeneration)
	if err != nil {
		return unavailableResourceInventory(result, err.Error()), fmt.Errorf("read resource inventory marker: %w", err)
	}
	if !found {
		return unavailableResourceInventory(result, "completed resource inventory build marker is missing"),
			&ResourceInventoryUnavailableError{Project: opts.Project, DatasetGeneration: datasetGeneration, Reason: "marker is missing"}
	}
	coveredTypes := make(map[string]struct{}, len(build.ResourceTypes))
	for _, resourceType := range build.ResourceTypes {
		coveredTypes[resourceType] = struct{}{}
	}
	canonicalBuildTypes, typeDiagnostics := evidenceResourceTypes(build.ResourceTypes)
	typesAreCanonical := len(typeDiagnostics) == 0 && len(canonicalBuildTypes) == len(build.ResourceTypes)
	if typesAreCanonical {
		for i := range canonicalBuildTypes {
			if canonicalBuildTypes[i] != build.ResourceTypes[i] {
				typesAreCanonical = false
				break
			}
		}
	}
	coverageDigest, coverageErr := catalog.ResourceInventoryCoverageDigest(build.ResourceTypes)
	if build.Key != catalog.ResourceInventoryBuildKey(opts.Project, datasetGeneration) ||
		build.RecordType != catalog.ResourceInventoryBuildRecordType || build.Project != opts.Project ||
		catalog.NormalizeDatasetGeneration(build.DatasetGeneration) != datasetGeneration ||
		!strings.HasPrefix(build.BuildID, "sha256:") || len(build.BuildID) != len("sha256:")+64 ||
		build.CoverageDigest == "" || build.CoverageDigest != coverageDigest ||
		coverageErr != nil || !typesAreCanonical || build.ResourceTypeCount != len(build.ResourceTypes) ||
		build.CountRowCount < build.ResourceTypeCount || !build.Complete {
		return unavailableResourceInventory(result, "resource inventory build marker is stale or incomplete"),
			&ResourceInventoryUnavailableError{Project: opts.Project, DatasetGeneration: datasetGeneration, Reason: "stale or incomplete marker"}
	}
	for _, resourceType := range resourceTypes {
		if _, ok := coveredTypes[resourceType]; !ok {
			return unavailableResourceInventory(result, "resource inventory build does not cover requested resource type "+resourceType),
				&ResourceInventoryUnavailableError{Project: opts.Project, DatasetGeneration: datasetGeneration, Reason: "build does not cover " + resourceType}
		}
	}
	authPathsUnrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !authPathsUnrestricted && len(opts.AuthResourcePaths) == 0 {
		// Restricted with no authorized path is a legitimate authorization
		// state that reads nothing — and it must not query counts at all.
		result.Digest, _ = catalog.ResourceInventoryDigest(result.Values)
		result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
		return result, nil
	}
	// Unrestricted mode wins over stray paths, matching the Arango adapter,
	// which ignores the path bind in unrestricted mode.
	var authPaths []string
	if !authPathsUnrestricted {
		authPaths = append([]string(nil), opts.AuthResourcePaths...)
	}
	sums, err := s.inventory.ReadCountSums(ctx, storepostgres.InventoryCountScope{
		Project:                       opts.Project,
		DatasetGeneration:             datasetGeneration,
		BuildID:                       build.BuildID,
		ResourceTypes:                 append([]string(nil), resourceTypes...),
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(authPathsUnrestricted),
		AuthResourcePaths:             authPaths,
	})
	if err != nil {
		return unavailableResourceInventory(result, err.Error()), fmt.Errorf("read resource inventory counts: %w", err)
	}
	for _, resourceType := range resourceTypes {
		result.Values = append(result.Values, catalog.ResourceInventoryObservation{
			Project: opts.Project, DatasetGeneration: datasetGeneration, ResourceType: resourceType, DocumentCount: sums[resourceType],
		})
	}
	sort.Slice(result.Values, func(i, j int) bool {
		return inventoryObservationLess(result.Values[i], result.Values[j])
	})
	result.Digest, _ = catalog.ResourceInventoryDigest(result.Values)
	result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

func unavailableResourceInventory(result catalog.ResourceInventoryResult, message string) catalog.ResourceInventoryResult {
	result.Values = []catalog.ResourceInventoryObservation{}
	result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
	result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: message})
	return result
}

// evidenceResourceTypes canonicalizes a requested type list, defaulting to
// every generated concrete FHIR resource type. It mirrors the Arango
// adapter so both backends accept and reject the same inputs.
func evidenceResourceTypes(requested []string) ([]string, []catalog.EvidenceDiagnostic) {
	if len(requested) == 0 {
		return fhirschema.ResourceTypes(), nil
	}
	seen := make(map[string]struct{}, len(requested))
	diagnostics := make([]catalog.EvidenceDiagnostic, 0)
	for _, requestedType := range requested {
		canonical, ok := fhirschema.ConcreteResourceType(requestedType)
		if !ok {
			diagnostics = append(diagnostics, catalog.EvidenceDiagnostic{Code: "INVALID_RESOURCE_TYPE", Message: "requested resource type is not a concrete generated FHIR resource", ResourceType: requestedType})
			continue
		}
		seen[canonical] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for resourceType := range seen {
		result = append(result, resourceType)
	}
	sort.Strings(result)
	return result, diagnostics
}

func inventoryObservationLess(a, b catalog.ResourceInventoryObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocumentCount < b.DocumentCount
}
