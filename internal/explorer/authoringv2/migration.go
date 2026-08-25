package authoringv2

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer"
)

// MigrateV1Document is the only V1-to-V2 direction. It imports authoring
// intent, never a compiled recipe or a server-resolved binding. V1's row node
// is intentionally ignored: V2 derives its tail from routeSteps.
func MigrateV1Document(input explorer.ExplorerBuilderDocumentV1) (Document, error) {
	if err := input.Validate(); err != nil {
		return Document{}, fmt.Errorf("migrate V1 document: %w", err)
	}
	title := input.Output.Title
	if title == "" {
		title = input.Output.ID
	}
	out := Document{
		APIVersion:   APIVersion,
		Kind:         APIVersionKind(),
		Output:       Output{ID: input.Output.ID, Title: title},
		RootNodeID:   input.BaseNodeID,
		RouteSteps:   make([]RouteStep, 0, len(input.RouteEdgeIDs)),
		Selections:   make([]Selection, 0, len(input.CandidateIDs)),
		Presentation: make(map[string]Presentation, len(input.Presentation)),
	}
	for index, edgeID := range input.RouteEdgeIDs {
		occurrenceIndex := index
		if len(input.RouteOccurrences) == len(input.RouteEdgeIDs)+1 {
			occurrenceIndex++
		}
		occurrenceID := ""
		if occurrenceIndex < len(input.RouteOccurrences) {
			occurrenceID = input.RouteOccurrences[occurrenceIndex].ID
		}
		out.RouteSteps = append(out.RouteSteps, RouteStep{EdgeID: edgeID, OccurrenceID: occurrenceID})
	}
	if len(input.CandidateOccurrences) > 0 {
		for _, occurrence := range input.CandidateOccurrences {
			mode := occurrence.ProjectionMode
			if mode == "" {
				mode = "SCALAR"
			}
			out.Selections = append(out.Selections, Selection{CandidateID: occurrence.CandidateID, OccurrenceID: occurrence.OccurrenceID, ProjectionMode: mode})
		}
	} else {
		for _, candidateID := range input.CandidateIDs {
			out.Selections = append(out.Selections, Selection{CandidateID: candidateID, OccurrenceID: RootOccurrenceID, ProjectionMode: "SCALAR"})
		}
	}
	for key, binding := range input.Presentation {
		out.Presentation[key] = Presentation{Label: binding.Label, Visible: binding.Visible, Order: binding.Order}
	}
	if err := out.Validate(); err != nil {
		return Document{}, fmt.Errorf("migrated V1 document is not valid V2 intent: %w", err)
	}
	return out, nil
}

// MigrateV1Bundle imports every V1 output document in order. Catalog
// resolution is deliberately a later operation, so migration cannot invent
// stale IDs or accidentally create a compilation cycle.
func MigrateV1Bundle(input explorer.ExplorerAuthoringBundleV1) ([]Document, error) {
	documents := input.AuthoringDocuments()
	out := make([]Document, 0, len(documents))
	for i, document := range documents {
		migrated, err := MigrateV1Document(document)
		if err != nil {
			return nil, fmt.Errorf("migrate V1 document %d: %w", i, err)
		}
		out = append(out, migrated)
	}
	return out, nil
}

// APIVersionKind exists to keep migration call sites readable while making
// the protocol kind a single constant in the wire package.
func APIVersionKind() string { return Kind }
