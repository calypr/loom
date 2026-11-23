package selection

import (
	"context"
	"fmt"
)

func CollectMembers(ctx context.Context, store Store, selection SelectionRevision, project string) (map[string]ResourceRef, []string, error) {
	refs := make(map[string]ResourceRef, selection.MemberCount)
	after := ""
	for {
		pageCount := 0
		next, err := store.VisitSelectionMembers(ctx, project, selection.ID, after, selectionBatchSize, func(member SelectionMember) error {
			member = member.Canonical()
			if err := member.Validate(selection.Project, selection.Generation, selection.ResourceType); err != nil {
				return err
			}
			pageCount++
			id := member.Ref.ID
			if prior, exists := refs[id]; exists && prior != member.Ref {
				return fmt.Errorf("selection contains conflicting resource identities")
			}
			refs[id] = member.Ref
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		if pageCount == 0 || pageCount < selectionBatchSize || next == after {
			break
		}
		after = next
	}
	if int64(len(refs)) != selection.MemberCount {
		return nil, nil, fmt.Errorf("selection member count changed")
	}
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	return refs, ids, nil
}
