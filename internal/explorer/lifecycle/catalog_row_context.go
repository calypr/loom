package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// catalogRowContext identifies the saved table rows whose values a catalog
// choice would describe. Presentation and unrelated outputs do not affect it.
type catalogRowContext struct {
	OutputID     string
	RootResource string
	Digest       string
}

func savedCatalogRowContext(workspace authoringv2.Workspace, outputID string) (catalogRowContext, error) {
	document := findSemanticOutput(workspace, outputID)
	if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType || strings.TrimSpace(document.RootResourceType) == "" {
		return catalogRowContext{}, fmt.Errorf("outputId does not identify a valid row-rooted table")
	}
	// A filter can refer to a column on any saved occurrence. Include the
	// route and column sources so a changed filter meaning invalidates choices.
	rowDefinition := struct {
		Version      string                                       `json:"version"`
		Root         string                                       `json:"root"`
		Route        authoringv2.RouteNode                        `json:"route"`
		Rows         authoringv2.RowDefinition                    `json:"rows"`
		Population   *authoringv2.Population                      `json:"population,omitempty"`
		FixedFilters []authoringv2.FixedFilter                    `json:"fixedFilters,omitempty"`
		Columns      []authoringv2.Column                         `json:"columns,omitempty"`
		TableShape   *authoringv2.TableShape                      `json:"tableShape,omitempty"`
		Shared       map[string][]authoringv2.SharedFilterBinding `json:"sharedFilters,omitempty"`
	}{
		Version: "catalog-row-set/v1", Root: document.RootResourceType,
		Route: document.Route, Rows: document.Rows, Population: document.Population,
		FixedFilters: document.FixedFilters, Columns: document.Columns,
		TableShape: document.TableShape, Shared: workspace.SharedFilters,
	}
	if defaultCatalogRecordCohort(workspace, *document) {
		// Projecting or renaming columns does not change an unfiltered record cohort.
		rowDefinition.Route = authoringv2.RouteNode{OccurrenceID: document.Route.OccurrenceID, ResourceType: document.RootResourceType}
		rowDefinition.Columns = nil
		rowDefinition.Shared = nil
	}
	raw, err := json.Marshal(rowDefinition)
	if err != nil {
		return catalogRowContext{}, fmt.Errorf("encode saved table row definition: %w", err)
	}
	sum := sha256.Sum256(raw)
	return catalogRowContext{OutputID: outputID, RootResource: document.RootResourceType, Digest: hex.EncodeToString(sum[:])}, nil
}
