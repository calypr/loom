package ingest

import (
	"context"
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// PrepareGenerationAvailability uses the same compiler traversal proof as the
// Builder snapshot. The physical graph is read once while the generation is
// staged; only compact positive witnesses survive activation.
// If resources is nil, the retained generation inventory supplies root types.
func PrepareGenerationAvailability(ctx context.Context, store *catalogarango.Store, project, generation string, resources map[string]int) error {
	if resources == nil {
		inventory, err := store.ReadResourceInventory(ctx, catalog.ResourceInventoryOptions{
			Project: project, DatasetGeneration: generation,
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		})
		if err != nil {
			return fmt.Errorf("read availability roots: %w", err)
		}
		if !inventory.Complete || inventory.Truncated {
			return fmt.Errorf("availability root inventory is incomplete")
		}
		resources = make(map[string]int, len(inventory.Values))
		for _, row := range inventory.Values {
			if row.DocumentCount > 0 {
				resources[row.ResourceType] = 1
			}
		}
	}
	observed, err := store.ReadRelationshipObservations(ctx, catalog.RelationshipObservationOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		return fmt.Errorf("read availability relationships: %w", err)
	}
	if !observed.Complete || observed.Truncated || (!observed.Available && observed.Status != catalog.EvidenceEmpty) {
		return fmt.Errorf("availability relationship observations are incomplete")
	}
	scope := compilerprobe.Scope{Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeUnrestricted}
	seen := make(map[catalog.AvailabilityRelation]struct{}, len(observed.Values))
	for _, edge := range observed.Values {
		if edge.EdgeCount <= 0 {
			continue
		}
		for _, relation := range catalog.RelationshipTraversalCandidates(edge) {
			proof, err := compilerprobe.ProbeTraversal(ctx, compilerprobe.TraversalRequest{
				Scope: scope, RootResourceType: relation.FromResourceType,
				Traversal: compilerprobe.Traversal{FromResourceType: relation.FromResourceType, EdgeLabel: relation.Relationship, ToResourceType: relation.ToResourceType, MatchMode: spec.TraversalMatchOptional},
			})
			if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != relation.StorageDirection {
				continue
			}
			seen[relation] = struct{}{}
		}
	}
	relations := make([]catalog.AvailabilityRelation, 0, len(seen))
	for relation := range seen {
		relations = append(relations, relation)
	}
	sort.Slice(relations, func(i, j int) bool {
		a, b := relations[i], relations[j]
		if a.FromResourceType != b.FromResourceType {
			return a.FromResourceType < b.FromResourceType
		}
		if a.ToResourceType != b.ToResourceType {
			return a.ToResourceType < b.ToResourceType
		}
		if a.Relationship != b.Relationship {
			return a.Relationship < b.Relationship
		}
		return a.StorageDirection < b.StorageDirection
	})
	roots := make([]string, 0, len(resources))
	for resourceType, count := range resources {
		if count > 0 {
			roots = append(roots, resourceType)
		}
	}
	return store.PrepareAvailableColumns(ctx, project, generation, roots, relations)
}
