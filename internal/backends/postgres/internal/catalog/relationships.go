// Authorization-scoped relationship evidence: exact occurrence counts
// aggregated from fhir_edge_occurrence for the PostgreSQL source store
// (pg-catalog-03).
//
// RelationshipEvidenceRepository aggregates the typed edge-occurrence table
// created by migration 000004_edge_occurrence (owned by pg-store-05) into
// one count per exact scoped relationship key
// (project, dataset_generation, auth_resource_path, storage source type,
// label, storage target type). Two rules the contract names explicitly
// shape the aggregation:
//
//   - It never deduplicates repeated occurrences. The count is COUNT(*)
//     over the rows, so two identical target/label references from
//     different source path/ordinal occurrences contribute two to the
//     aggregate — the multiplicity the endpoint-keyed Arango model
//     collapses. Identity uniqueness (constraint
//     fhir_edge_occurrence_identity_ux) is what keeps a fully identical
//     occurrence from being stored twice; the aggregation trusts the rows
//     it is given and never invents a DISTINCT that would silently flatten
//     real multiplicity.
//   - It never flattens auth paths prematurely. auth_resource_path is a
//     GROUP BY column, so a count for one authorization path is never
//     merged into a sibling path's count; restricted reads bind exactly
//     the authorized paths and cross-scope rows (other project, generation,
//     or path) never join the sum.
//
// DiscoverRelationshipObservations keeps physical storage direction and
// registry-derived Builder direction separately observable, mirroring the
// Arango adapter (internal/catalog/arango/relationships.go): every
// observation reports StorageFromType/StorageToType with
// StorageDirection OUTBOUND (fhir_edge_occurrence is always
// source -> target) and, separately, BuilderFromType/BuilderToType with
// BuilderDirection resolved from the generated traversal registry
// (fhirschema.ResolveCompilerTraversal), preferring the inverse generated
// traversal exactly as the Arango adapter does. Endpoint types are
// canonicalized with fhirschema.ConcreteResourceType before the registry
// lookup — an occurrence stores the endpoints the writer supplied, and the
// Arango catalog row they correspond to was canonicalized at ingest — so a
// casing-only variance is a canonicalization, not a misdiagnosis. A
// requested-type read filters in Go after that canonicalization — never
// with a SQL-side approximation, whose trim and fold sets differ from
// Go's (SQL btrim trims ASCII space only; strings.TrimSpace trims all
// Unicode whitespace, and case-folding is Unicode too), so a filtered
// read can never silently drop a tab-, CR-, NBSP-, or fold-variant row
// that the unfiltered read returns as evidence.
//
// Invalid generated FHIR endpoint types are diagnosed
// (INVALID_RELATIONSHIP_ENDPOINT) and excluded from Builder capability
// evidence while the raw stored occurrences remain inspectable: the
// aggregation is a read, and nothing here deletes or rewrites a row the
// registry cannot classify. Endpoints that are concrete but whose label is
// not a supported generated traversal are diagnosed
// (UNSUPPORTED_GENERATED_TRAVERSAL) the same way. A result with excluded
// rows stays available — diagnostics are data, not failure.
//
// Option semantics, evidence statuses, and digests mirror the Arango
// adapter: the empty generation is the legacy namespace, unrestricted mode
// is explicit and wins over stray paths, a restricted scope with no
// authorized path is a legitimate authorization state that reads nothing,
// and the result digest is catalog.RelationshipObservationDigest over the
// sorted observations. There is no cross-database atomicity and no backend
// fallback: every failure is a named error the caller must surface.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/jackc/pgx/v5"
)

const (
	// edgeOccurrenceTableName is the store-05 table created by migration
	// 000004_edge_occurrence.sql. This adapter reads it; writes belong to
	// storepostgres.EdgeRepository and the batch writer.
	edgeOccurrenceTableName = "fhir_edge_occurrence"
	// relationshipEvidenceCountColumn names the aggregate output column so
	// the scan never depends on positional ambiguity.
	relationshipEvidenceCountColumn = "occurrence_count"
)

// relationshipEvidenceSchemaPattern accepts the same safe lowercase
// identifiers the store package's runner accepts; the store copy is
// unexported (matching the semantic_build.go precedent).
var relationshipEvidenceSchemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ErrInvalidRelationshipScope reports a scope or scan argument this adapter
// refuses before any statement runs: a blank project, a missing unrestricted
// flag, or an unrestricted scope carrying paths. Requested resource types
// are not a repository argument any more — the adapter canonicalizes and
// filters them in Go. There is no implicit-unrestricted mode and no silent
// correction.
var ErrInvalidRelationshipScope = errors.New("postgres catalog: invalid relationship scope")

// RelationshipScope binds one occurrence aggregation to exactly one
// storage scope and its authorization. It follows the explicit-mode
// convention of storepostgres.EdgeScope: AuthResourcePathsUnrestricted must
// be set deliberately.
type RelationshipScope struct {
	// Project is required and matched exactly.
	Project string
	// DatasetGeneration is matched exactly. The empty string is the legacy
	// dataset namespace, never "any generation".
	DatasetGeneration string
	// AuthResourcePathsUnrestricted must be non-nil: true selects the
	// explicit unrestricted mode (then AuthResourcePaths must be empty),
	// false selects restricted mode. A nil value is an error, not the
	// legacy "empty means unrestricted" convention — that legacy rule is
	// resolved once by the adapter via
	// catalog.EffectiveAuthResourcePathsUnrestricted before the scope is
	// built.
	AuthResourcePathsUnrestricted *bool
	// AuthResourcePaths are the authorized auth_resource_path values in
	// restricted mode. An empty list in restricted mode is a legitimate
	// authorization state; the adapter short-circuits it without a query.
	AuthResourcePaths []string
}

// RelationshipOccurrenceGroup is one exact scoped aggregate produced by the
// grouped scan: every occurrence row whose scope columns and storage
// endpoints equal these values contributes 1 to OccurrenceCount.
type RelationshipOccurrenceGroup struct {
	// AuthResourcePath is the authorization path this aggregate belongs to.
	// It is never flattened away; rows from other paths are separate
	// groups.
	AuthResourcePath string
	// StorageSourceResourceType and StorageTargetResourceType are the raw
	// stored endpoint types, exactly as the occurrence rows hold them
	// (classification into generated FHIR types is the adapter's job).
	StorageSourceResourceType string
	StorageTargetResourceType string
	// Label is the stored edge label.
	Label string
	// OccurrenceCount is COUNT(*) over the matching rows: repeated
	// occurrences from different source path/ordinal positions each count.
	OccurrenceCount int64
}

// RelationshipStore is the persistence boundary this adapter needs from the
// edge-occurrence table. *RelationshipEvidenceRepository implements it;
// tests may wrap the real repository or supply recorded groups to exercise
// the pure classification path, never to fake a live-PostgreSQL claim (the
// acceptance tests drive the real repository).
type RelationshipStore interface {
	// ScanRelationshipOccurrences aggregates every occurrence row inside
	// one exact scope into per (auth path, storage source type, label,
	// storage target type) counts, ordered by those columns. It performs
	// no endpoint classification or type filtering and never deduplicates
	// rows.
	ScanRelationshipOccurrences(ctx context.Context, scope RelationshipScope) ([]RelationshipOccurrenceGroup, error)
}

var _ RelationshipStore = (*RelationshipEvidenceRepository)(nil)

// RelationshipEvidenceRepository runs the scoped aggregation against one
// schema's fhir_edge_occurrence table. Create one with
// NewRelationshipEvidenceRepository. It holds no state beyond the
// qualified table name; the caller owns the connection source and the
// schema lifecycle.
type RelationshipEvidenceRepository struct {
	src      storepostgres.ConnSource
	tableFQN string
}

// NewRelationshipEvidenceRepository validates the connection source and
// schema name and binds the repository to <schema>.fhir_edge_occurrence.
// The connection source must be live only when a method runs, not here.
func NewRelationshipEvidenceRepository(src storepostgres.ConnSource, schema string) (*RelationshipEvidenceRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil relationship evidence connection source")
	}
	if !relationshipEvidenceSchemaPattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", storepostgres.ErrInvalidSchemaName, schema)
	}
	return &RelationshipEvidenceRepository{
		src:      src,
		tableFQN: pgx.Identifier{schema, edgeOccurrenceTableName}.Sanitize(),
	}, nil
}

// relationshipAggregateShape is the fully bound form of one aggregation
// query: the statement plus its ordered parameters.
type relationshipAggregateShape struct {
	SQL  string
	Args []any
}

// bindRelationshipAggregate validates the scope and renders the ordered,
// parameterized aggregation. Parameters are ordered: $1 project, $2
// dataset generation, $3 authorized auth paths (restricted mode only). The
// SELECT casts every bound array so PostgreSQL never infers a type it
// cannot see.
//
// There is deliberately no endpoint-type predicate here: stored endpoint
// columns are writer-supplied raw strings and the only canonicalizer is
// Go's fhirschema.ConcreteResourceType (TrimSpace + case-fold). Any
// SQL-side approximation (raw equality, or lower(btrim(...)), which trims
// ASCII space only) would silently drop the trim/fold variants the Go
// canonicalizer accepts, so the adapter filters in Go after scanning and
// classifying — one source of truth for both the filtered and unfiltered
// read.
//
// auth_resource_path is a GROUP BY column, never dropped; the count is
// COUNT(*) over rows, never COUNT(DISTINCT ...) over endpoints.
func bindRelationshipAggregate(tableFQN string, scope RelationshipScope) (relationshipAggregateShape, error) {
	if strings.TrimSpace(scope.Project) == "" {
		return relationshipAggregateShape{}, fmt.Errorf("%w: project is blank", ErrInvalidRelationshipScope)
	}
	if scope.AuthResourcePathsUnrestricted == nil {
		return relationshipAggregateShape{}, fmt.Errorf(
			"%w: AuthResourcePathsUnrestricted must be set explicitly; there is no implicit unrestricted mode",
			ErrInvalidRelationshipScope)
	}
	if *scope.AuthResourcePathsUnrestricted {
		if len(scope.AuthResourcePaths) > 0 {
			return relationshipAggregateShape{}, fmt.Errorf(
				"%w: unrestricted mode must not carry auth resource paths (%d given)",
				ErrInvalidRelationshipScope, len(scope.AuthResourcePaths))
		}
	}
	sql := "SELECT auth_resource_path, source_resource_type, label, target_resource_type," +
		" COUNT(*) AS " + relationshipEvidenceCountColumn +
		" FROM " + tableFQN +
		" WHERE project = $1 AND dataset_generation = $2"
	args := []any{scope.Project, scope.DatasetGeneration}
	if !*scope.AuthResourcePathsUnrestricted {
		sql += " AND auth_resource_path = ANY($" + fmt.Sprint(len(args)+1) + "::text[])"
		args = append(args, scope.AuthResourcePaths)
	}
	sql += " GROUP BY auth_resource_path, source_resource_type, label, target_resource_type" +
		" ORDER BY auth_resource_path, source_resource_type, label, target_resource_type"
	return relationshipAggregateShape{SQL: sql, Args: args}, nil
}

// ScanRelationshipOccurrences runs the one grouped aggregation. A
// restricted scope with no authorized paths reads nothing: the adapter
// short-circuits that state, and this method refuses to widen it either
// way (binding an empty array would match no rows, and it never falls back
// to an unfiltered scan).
func (r *RelationshipEvidenceRepository) ScanRelationshipOccurrences(
	ctx context.Context, scope RelationshipScope) ([]RelationshipOccurrenceGroup, error) {
	bound, err := bindRelationshipAggregate(r.tableFQN, scope)
	if err != nil {
		return nil, err
	}
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire relationship evidence connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, bound.SQL, bound.Args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: aggregate relationship occurrences: %w", err)
	}
	defer rows.Close()

	var out []RelationshipOccurrenceGroup
	for rows.Next() {
		var group RelationshipOccurrenceGroup
		if err := rows.Scan(&group.AuthResourcePath, &group.StorageSourceResourceType,
			&group.Label, &group.StorageTargetResourceType, &group.OccurrenceCount); err != nil {
			return nil, fmt.Errorf("postgres: aggregate relationship occurrences: %w", err)
		}
		if group.OccurrenceCount < 1 {
			// COUNT(*) over grouped rows is at least 1; anything else is a
			// decoding fault, never a value to promote to evidence.
			return nil, fmt.Errorf("postgres: aggregate relationship occurrences: group %s/%s/%s at path %q has count %d",
				group.StorageSourceResourceType, group.Label, group.StorageTargetResourceType,
				group.AuthResourcePath, group.OccurrenceCount)
		}
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: aggregate relationship occurrences: %w", err)
	}
	return out, nil
}

// relationshipObservationKey identifies one Builder projection of one
// physical storage aggregate. It separates storage provenance from the
// Builder view exactly as the Arango adapter does: identical storage facts
// that map to different Builder directions stay separate.
type relationshipObservationKey struct {
	project, generation, auth                string
	storageFrom, label, storageTo            string
	builderFrom, builderTo, builderDirection string
}

// DiscoverRelationshipObservations derives authorization-scoped relationship
// counts from the raw edge-occurrence table and returns both the physical
// storage view and the Builder-facing projection of each aggregate. The
// catalog stores physical fhir_edge_occurrence direction; Builder usually
// traverses its inverse, so dropping the storage provenance here is a
// correctness bug rather than a presentation detail — both halves are kept.
//
// The read is one grouped SQL statement over fhir_edge_occurrence (there is
// no PostgreSQL counterpart to CursorBatch: grouping happens inside the
// database). Invalid generated FHIR endpoint types are diagnosed and
// excluded from the Builder-facing values; the raw occurrences stay stored
// and inspectable. A restricted scope with no authorized path returns an
// empty, complete result without running any query.
func (s *Store) DiscoverRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Diagnostics: diagnostics}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)

	unrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !unrestricted && len(opts.AuthResourcePaths) == 0 {
		// Restricted with no authorized path is a legitimate authorization
		// state that reads nothing — and it must not query at all.
		result.Digest, _ = catalog.RelationshipObservationDigest(result.Values)
		result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
		return result, nil
	}

	scope := RelationshipScope{
		Project:                       opts.Project,
		DatasetGeneration:             datasetGeneration,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(unrestricted),
	}
	if !unrestricted {
		scope.AuthResourcePaths = append([]string(nil), opts.AuthResourcePaths...)
	}

	// The requested-type filter is evaluated in Go against the canonical
	// form produced by the very function that classifies evidence
	// (fhirschema.ConcreteResourceType), never against a SQL-side
	// approximation: stored endpoint strings keep every writer-supplied
	// trim/fold variant (store-05 validates only non-blank), and the Go
	// canonicalizer — not the database — defines which variants collapse.
	// A requested set of only-invalid types canonicalizes to the empty
	// set and matches nothing, the same posture as the Arango adapter's
	// "IN []" filter; request-level INVALID_RESOURCE_TYPE diagnostics
	// were already recorded above.
	requestedTypes := len(opts.ResourceTypes) > 0
	var requestedSet map[string]struct{}
	if requestedTypes {
		requestedSet = make(map[string]struct{}, len(resourceTypes))
		for _, resourceType := range resourceTypes {
			requestedSet[resourceType] = struct{}{}
		}
	}

	groups, err := s.relationships.ScanRelationshipOccurrences(ctx, scope)
	if err != nil {
		return unavailableRelationshipEvidence(result, err.Error()),
			fmt.Errorf("read relationship occurrence evidence: %w", err)
	}

	counts := make(map[relationshipObservationKey]int64)
	for _, group := range groups {
		storageFrom := group.StorageSourceResourceType
		label := group.Label
		storageTo := group.StorageTargetResourceType
		from, fromOK := fhirschema.ConcreteResourceType(storageFrom)
		to, toOK := fhirschema.ConcreteResourceType(storageTo)
		if !fromOK || !toOK {
			// The endpoint is not a concrete generated FHIR resource. It is
			// excluded from Builder capability evidence but stays a stored,
			// inspectable occurrence row: nothing here rewrites it. Under a
			// requested-type read such a row can never be in the set (only
			// concrete canonical names are), so it is skipped without a
			// diagnostic — the same posture as the Arango adapter's AQL
			// `FILTER ... IN @resource_types`, where rows outside the
			// requested types are neither evidence nor diagnostics.
			if !requestedTypes {
				result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{
					Code:            "INVALID_RELATIONSHIP_ENDPOINT",
					Message:         "relationship endpoint is not a concrete generated FHIR resource",
					StorageFromType: storageFrom, Label: label, StorageToType: storageTo,
					Count: group.OccurrenceCount,
				})
			}
			continue
		}
		if requestedTypes {
			// Canonical-side membership: both endpoints are compared to
			// the requested set through the same canonicalizer that
			// produced their values above, so a filtered read can never
			// drop a row the unfiltered read would classify into the
			// requested types — no trim/fold variant is ever silently
			// lost between the two paths.
			if _, ok := requestedSet[from]; !ok {
				continue
			}
			if _, ok := requestedSet[to]; !ok {
				continue
			}
		}
		observation, ok, diagnostic := relationshipObservation(opts.Project, datasetGeneration, group.AuthResourcePath, from, label, to, group.OccurrenceCount)
		if !ok {
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			continue
		}
		key := relationshipObservationKey{
			project: observation.Project, generation: observation.DatasetGeneration, auth: observation.AuthResourcePath,
			storageFrom: observation.StorageFromType, label: observation.Label, storageTo: observation.StorageToType,
			builderFrom: observation.BuilderFromType, builderTo: observation.BuilderToType, builderDirection: observation.BuilderDirection,
		}
		counts[key] += observation.EdgeCount
	}
	for key, count := range counts {
		result.Values = append(result.Values, catalog.RelationshipObservation{
			Project: key.project, DatasetGeneration: key.generation, AuthResourcePath: key.auth,
			FromType: key.builderFrom, Label: key.label, ToType: key.builderTo, EdgeCount: count,
			StorageFromType: key.storageFrom, StorageToType: key.storageTo, StorageDirection: "OUTBOUND",
			BuilderFromType: key.builderFrom, BuilderToType: key.builderTo, BuilderDirection: key.builderDirection,
		})
	}
	sort.Slice(result.Values, func(i, j int) bool { return relationshipEvidenceLess(result.Values[i], result.Values[j]) })
	result.Digest, _ = catalog.RelationshipObservationDigest(result.Values)
	result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

// ReadRelationshipObservations is the explicit-read alias of
// DiscoverRelationshipObservations, mirroring the Arango capability
// boundary.
func (s *Store) ReadRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.DiscoverRelationshipObservations(ctx, opts)
}

func unavailableRelationshipEvidence(result catalog.RelationshipObservationResult, message string) catalog.RelationshipObservationResult {
	result.Values = []catalog.RelationshipObservation{}
	result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
	result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RELATIONSHIP_EVIDENCE_UNAVAILABLE", Message: message})
	return result
}

// relationshipObservation maps one physical storage aggregate to its
// Builder projection through the generated traversal registry. It mirrors
// the Arango adapter (internal/catalog/arango/evidence.go) so both backends
// accept and reject the same evidence. from/to arrive already canonicalized
// (see the file header); the count comes from COUNT(*) over occurrence rows
// and is therefore always positive — the non-positive branch is defensive
// parity with the Arango path, where a catalog document could carry a
// malformed count.
func relationshipObservation(project, datasetGeneration, auth, storageFrom, label, storageTo string, count int64) (catalog.RelationshipObservation, bool, catalog.EvidenceDiagnostic) {
	diagnostic := catalog.EvidenceDiagnostic{StorageFromType: storageFrom, Label: label, StorageToType: storageTo, Count: count}
	if count <= 0 {
		diagnostic.Code = "INVALID_RELATIONSHIP_COUNT"
		diagnostic.Message = "relationship evidence count must be positive"
		return catalog.RelationshipObservation{}, false, diagnostic
	}
	// Stored occurrences are always source -> target. Builder's normal
	// projection is the inverse generated traversal, which is physically
	// INBOUND from the stored target. Keep a direct generated traversal as a
	// valid fallback for labels whose generated schema only exposes the
	// storage orientation.
	if _, found, err := fhirschema.ResolveCompilerTraversal(storageTo, label, storageFrom); found && err == nil {
		return catalog.RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageTo, Label: label, ToType: storageFrom, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageTo, BuilderToType: storageFrom, BuilderDirection: "INBOUND"}, true, diagnostic
	}
	if _, found, err := fhirschema.ResolveCompilerTraversal(storageFrom, label, storageTo); found && err == nil {
		return catalog.RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageFrom, Label: label, ToType: storageTo, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageFrom, BuilderToType: storageTo, BuilderDirection: "OUTBOUND"}, true, diagnostic
	}
	diagnostic.Code = "UNSUPPORTED_GENERATED_TRAVERSAL"
	diagnostic.Message = "relationship endpoints and label are not a supported generated traversal"
	return catalog.RelationshipObservation{}, false, diagnostic
}

// relationshipEvidenceLess orders observations for the digest exactly as
// the Arango adapter's relationshipLess does.
func relationshipEvidenceLess(a, b catalog.RelationshipObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.FromType, b.FromType}, {a.Label, b.Label}, {a.ToType, b.ToType}, {a.StorageFromType, b.StorageFromType}, {a.StorageToType, b.StorageToType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.EdgeCount < b.EdgeCount
}
