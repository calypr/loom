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
	"github.com/jackc/pgx/v5"
)

const (
	edgeOccurrenceTableName         = "fhir_edge_occurrence"
	relationshipEvidenceCountColumn = "occurrence_count"
)

var ErrInvalidRelationshipScope = errors.New("postgres catalog: invalid relationship scope")

type RelationshipScope struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
}

type RelationshipOccurrenceGroup struct {
	AuthResourcePath          string
	StorageSourceResourceType string
	StorageTargetResourceType string
	Label                     string
	OccurrenceCount           int64
}

type RelationshipStore interface {
	ScanRelationshipOccurrences(ctx context.Context, scope RelationshipScope) ([]RelationshipOccurrenceGroup, error)
}

var _ RelationshipStore = (*RelationshipEvidenceRepository)(nil)

type RelationshipEvidenceRepository struct {
	src      storepostgres.ConnSource
	tableFQN string
}

func NewRelationshipEvidenceRepository(src storepostgres.ConnSource, schema string) (*RelationshipEvidenceRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil relationship evidence connection source")
	}
	if !storepostgres.ValidSchemaName(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", storepostgres.ErrInvalidSchemaName, schema)
	}
	return &RelationshipEvidenceRepository{
		src:      src,
		tableFQN: pgx.Identifier{schema, edgeOccurrenceTableName}.Sanitize(),
	}, nil
}

type relationshipAggregateShape struct {
	SQL  string
	Args []any
}

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

func (s *RelationshipAdapter) DiscoverRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Diagnostics: diagnostics}
	if err := requireLeg("relationship-evidence", s.relationships != nil, "DiscoverRelationshipObservations"); err != nil {
		return unavailableRelationshipEvidence(result, err.Error()), err
	}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)

	unrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !unrestricted && len(opts.AuthResourcePaths) == 0 {
		result.Digest, _ = catalog.RelationshipObservationDigest(result.Values)
		result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
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

	counts := make(map[catalog.RelationshipEvidenceKey]int64)
	for _, group := range groups {
		storageFrom := group.StorageSourceResourceType
		label := group.Label
		storageTo := group.StorageTargetResourceType
		from, fromOK := fhirschema.ConcreteResourceType(storageFrom)
		to, toOK := fhirschema.ConcreteResourceType(storageTo)
		if !fromOK || !toOK {
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
			if _, ok := requestedSet[from]; !ok {
				continue
			}
			if _, ok := requestedSet[to]; !ok {
				continue
			}
		}
		observation, ok, diagnostic := catalog.ProjectRelationshipObservation(opts.Project, datasetGeneration, group.AuthResourcePath, from, label, to, group.OccurrenceCount)
		if !ok {
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			continue
		}
		counts[catalog.RelationshipEvidenceKeyFor(observation)] += observation.EdgeCount
	}
	for key, count := range counts {
		result.Values = append(result.Values, catalog.RelationshipObservationFromKey(key, count))
	}
	sort.Slice(result.Values, func(i, j int) bool { return catalog.RelationshipEvidenceLess(result.Values[i], result.Values[j]) })
	result.Digest, _ = catalog.RelationshipObservationDigest(result.Values)
	result.Status = catalog.EvidenceStatusFor(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

func (s *RelationshipAdapter) ReadRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.DiscoverRelationshipObservations(ctx, opts)
}

func unavailableRelationshipEvidence(result catalog.RelationshipObservationResult, message string) catalog.RelationshipObservationResult {
	result.Values = []catalog.RelationshipObservation{}
	result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
	result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RELATIONSHIP_EVIDENCE_UNAVAILABLE", Message: message})
	return result
}
