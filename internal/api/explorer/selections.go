package explorerapi

import (
	loomapi "github.com/calypr/loom/api/loomapi"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

func selectionRefs(refs []loomapi.SelectionResourceRef) []explorerselection.ResourceRef {
	result := make([]explorerselection.ResourceRef, len(refs))
	for i, ref := range refs {
		result[i] = explorerselection.ResourceRef{Project: ref.Project, Generation: ref.Generation, ResourceType: ref.ResourceType, ID: ref.Id}
	}
	return result
}
