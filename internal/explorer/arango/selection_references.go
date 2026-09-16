package arango

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

const selectionReferenceBatchSize = 1000

// ValidateSelectionReferences verifies explicit selection references against
// the concrete resource collection for the requested immutable generation.
// It intentionally runs in the storage adapter: HTTP callers must not be
// able to turn guessed IDs into persisted members by skipping the active
// generation and auth_resource_path predicates.
func (s *Store) ValidateSelectionReferences(ctx context.Context, project, generation string, scope authscope.ReadScope, refs []explorer.ResourceRef) error {
	project = projectid.Canonical(project)
	generation = strings.TrimSpace(generation)
	if project == "" || generation == "" {
		return fmt.Errorf("selection reference project and generation are required")
	}

	type resourceGroup struct {
		resourceType string
		ids          []string
	}
	groups := make(map[string]map[string]struct{})
	for _, raw := range refs {
		ref := raw.Canonical()
		if err := ref.Validate(project, generation, ""); err != nil {
			return err
		}
		if groups[ref.ResourceType] == nil {
			groups[ref.ResourceType] = make(map[string]struct{})
		}
		groups[ref.ResourceType][ref.ID] = struct{}{}
	}
	ordered := make([]resourceGroup, 0, len(groups))
	for resourceType, ids := range groups {
		orderedIDs := make([]string, 0, len(ids))
		for id := range ids {
			orderedIDs = append(orderedIDs, id)
		}
		sort.Strings(orderedIDs)
		ordered = append(ordered, resourceGroup{resourceType: resourceType, ids: orderedIDs})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].resourceType < ordered[j].resourceType })

	for _, group := range ordered {
		found := make(map[string]struct{}, len(group.ids))
		for start := 0; start < len(group.ids); start += selectionReferenceBatchSize {
			end := start + selectionReferenceBatchSize
			if end > len(group.ids) {
				end = len(group.ids)
			}
			binds := map[string]any{
				"@resource_collection": group.resourceType,
				"project":              project,
				"generation":           generation,
				"ids":                  group.ids[start:end],
				"auth_unrestricted":    scope.Unrestricted(),
				"auth_resource_paths":  append([]string(nil), scope.AuthResourcePaths...),
			}
			err := s.client.QueryRows(ctx, selectionReferenceValidationAQL, 1000, binds, func(row map[string]any) error {
				if id := selectionStringValue(row["id"]); id != "" {
					found[id] = struct{}{}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		for _, id := range group.ids {
			if _, ok := found[id]; ok {
				continue
			}
			// Under a restricted scope, absence is deliberately reported as a
			// stale/scope error rather than silently filtering the reference.
			// This covers both unauthorized IDs and IDs from a changed active
			// generation without disclosing which predicate failed.
			if !scope.Unrestricted() {
				return fmt.Errorf("%w: resource reference is unavailable in the authorized scope", explorer.ErrResourceRefScopeMismatch)
			}
			return fmt.Errorf("%w: resource reference %s/%s was not found", explorer.ErrSelectionNotFound, group.resourceType, id)
		}
	}
	return nil
}

const selectionReferenceValidationAQL = `
FOR d IN @@resource_collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @generation
  FILTER @auth_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER d.id IN @ids
  RETURN {id: d.id}`

func selectionStringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
