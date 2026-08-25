// Package bootstrap contains the offline/admin-only conversion from the
// legacy repository default recipe/config to the V1 intent contract.  Nothing
// in this package has a runtime store dependency.
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

type Mapping struct {
	LegacyPath      string `json:"legacyPath"`
	LegacyFieldName string `json:"legacyFieldName,omitempty"`
	OutputID        string `json:"outputId"`
	CandidateID     string `json:"candidateId"`
	OccurrenceID    string `json:"occurrenceId"`
	EmissionID      string `json:"emissionId"`
}

type Report struct {
	Equivalent              bool      `json:"equivalent"`
	LegacyRecipeDigest      string    `json:"legacyRecipeDigest"`
	AuthoringDocumentDigest string    `json:"authoringDocumentDigest"`
	Mappings                []Mapping `json:"mappings"`
	Unmapped                []string  `json:"unmapped,omitempty"`
	Ambiguous               []string  `json:"ambiguous,omitempty"`
}

type ConversionError struct{ Report Report }

func (e *ConversionError) Error() string { return "legacy default conversion is not exact" }

// ConvertDefault converts every legacy recipe output into one V1 builder
// document and converts each legacy view into an ordered V1 tab. Recipe
// operations that cannot be represented by V1 intent are rejected; no field is
// guessed or silently dropped.
func ConvertDefault(rawConfig []byte, catalog explorer.Catalog, project string) (explorer.ExplorerAuthoringBundleV1, Report, error) {
	cfg, legacy, err := explorer.DecodeDefaultConfigV2(rawConfig, project)
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, Report{}, fmt.Errorf("decode legacy default: %w", err)
	}
	report := Report{}
	report.LegacyRecipeDigest, _ = legacy.Digest()
	nodeIDs, err := uniqueNodeIDs(catalog)
	if err != nil {
		report.Ambiguous = append(report.Ambiguous, "catalog.nodes")
		return explorer.ExplorerAuthoringBundleV1{}, report, &ConversionError{Report: report}
	}
	documents := make([]explorer.ExplorerBuilderDocumentV1, 0, len(legacy.Outputs))
	documentByLegacyOutput := make(map[string]int, len(legacy.Outputs))
	allMappings := make([]Mapping, 0)
	for outputIndex, output := range legacy.Outputs {
		document, mappings, conversionErr := convertOutput(cfg, output, outputIndex, catalog, nodeIDs, &report)
		if conversionErr != nil {
			return explorer.ExplorerAuthoringBundleV1{}, report, conversionErr
		}
		documentByLegacyOutput[output.Name] = len(documents)
		documents = append(documents, document)
		allMappings = append(allMappings, mappings...)
	}
	if err := mapPresentation(cfg, documentByLegacyOutput, documents, allMappings, &report); err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, report, err
	}
	tabs := make([]explorer.ExplorerTabV1, 0, len(cfg.Views))
	for index, view := range cfg.Views {
		documentIndex, ok := documentByLegacyOutput[view.Output]
		if !ok {
			report.Unmapped = append(report.Unmapped, fmt.Sprintf("views[%d].output", index))
			return explorer.ExplorerAuthoringBundleV1{}, report, &ConversionError{Report: report}
		}
		tabs = append(tabs, explorer.ExplorerTabV1{ID: explorer.StableExplorerID(view.ID), Title: view.Title, OutputID: documents[documentIndex].Output.ID, Order: index})
	}
	for i := range documents {
		sort.Strings(documents[i].CandidateIDs)
	}
	bundle := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: project, ExplorerID: "default", Title: cfg.Explorer.Title, Tabs: tabs}
	if len(documents) == 1 {
		// Keep the in-memory compatibility field populated for callers that have
		// not yet moved to the canonical multi-document representation.
		bundle.Document = documents[0]
	} else {
		bundle.Documents = documents
	}
	bundle.IntentDigest, err = bundle.DocumentDigest()
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, report, err
	}
	report.AuthoringDocumentDigest = bundle.IntentDigest
	report.Equivalent = len(report.Unmapped) == 0 && len(report.Ambiguous) == 0
	if !report.Equivalent {
		return explorer.ExplorerAuthoringBundleV1{}, report, &ConversionError{Report: report}
	}
	return bundle, report, nil
}

func convertOutput(cfg explorer.ConfigV2, output recipe.Output, outputIndex int, catalog explorer.Catalog, nodeIDs map[string]string, report *Report) (explorer.ExplorerBuilderDocumentV1, []Mapping, error) {
	baseNode, ok := nodeIDs[output.RootResourceType]
	if !ok {
		report.Unmapped = append(report.Unmapped, fmt.Sprintf("outputs[%d].rootResourceType", outputIndex))
		return explorer.ExplorerBuilderDocumentV1{}, nil, &ConversionError{Report: *report}
	}
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: explorer.StableExplorerID(output.Name), Title: titleForOutput(cfg, output.Name)}, BaseNodeID: baseNode, RowNodeID: baseNode, Presentation: map[string]explorer.ExplorerPresentationBindingV1{}}
	currentNode := baseNode
	mappingsBefore := len(report.Mappings)
	var walkTraversals func([]recipe.Traversal, string, string, int) error
	walkTraversals = func(traversals []recipe.Traversal, parentNode, parentOccurrence string, depth int) error {
		if len(traversals) > 1 {
			report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("traversal[%d]", depth))
			return &ConversionError{Report: *report}
		}
		for index, traversal := range traversals {
			if len(traversal.Traversals) > 1 {
				report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("traversal[%d].traversals", depth))
				return &ConversionError{Report: *report}
			}
			childNode, edgeID, err := exactEdge(catalog, parentNode, traversal.Name, traversal.ToResourceType)
			if err != nil {
				report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("traversal[%d]", depth))
				return &ConversionError{Report: *report}
			}
			occurrenceID := fmt.Sprintf("route_%d", len(document.RouteEdgeIDs))
			document.RouteEdgeIDs = append(document.RouteEdgeIDs, edgeID)
			document.RouteOccurrences = append(document.RouteOccurrences, explorer.ExplorerRouteOccurrenceV1{ID: occurrenceID, Index: len(document.RouteEdgeIDs) - 1, NodeID: childNode, IncomingEdgeID: edgeID})
			if err := mapFields(traversal.Fields, childNode, occurrenceID, traversalPath(parentOccurrence, index), catalog, &document, report); err != nil {
				return err
			}
			if err := walkTraversals(traversal.Traversals, childNode, occurrenceID, depth+1); err != nil {
				return err
			}
			currentNode = childNode
		}
		return nil
	}
	if err := mapFields(output.Fields, baseNode, "base", fmt.Sprintf("outputs[%d]", outputIndex), catalog, &document, report); err != nil {
		return explorer.ExplorerBuilderDocumentV1{}, nil, err
	}
	if err := walkTraversals(output.Traversals, baseNode, "root", 0); err != nil {
		return explorer.ExplorerBuilderDocumentV1{}, nil, err
	}
	document.RowNodeID = currentNode
	for i := mappingsBefore; i < len(report.Mappings); i++ {
		report.Mappings[i].OutputID = document.Output.ID
	}
	return document, append([]Mapping(nil), report.Mappings[mappingsBefore:]...), nil
}

func uniqueNodeIDs(catalog explorer.Catalog) (map[string]string, error) {
	out := map[string]string{}
	for _, node := range catalog.Nodes {
		if old, ok := out[node.ResourceType]; ok && old != node.ID {
			return nil, fmt.Errorf("resource type %q has multiple node IDs", node.ResourceType)
		}
		out[node.ResourceType] = node.ID
	}
	return out, nil
}
func exactEdge(catalog explorer.Catalog, parentNode, label, childType string) (string, string, error) {
	var child, edge string
	for _, candidate := range catalog.Edges {
		fromMatches := candidate.FromNodeID == parentNode
		toMatches := candidate.ToNodeID == parentNode
		other := ""
		if fromMatches {
			other = candidate.ToNodeID
		}
		if toMatches {
			other = candidate.FromNodeID
		}
		if (fromMatches || toMatches) && candidate.Label == label {
			node, ok := catalogNodeByID(catalog, other)
			if !ok || node.ResourceType != childType {
				continue
			}
			if child != "" {
				return "", "", fmt.Errorf("ambiguous edge")
			}
			child, edge = other, candidate.ID
		}
	}
	if edge == "" {
		return "", "", fmt.Errorf("unmapped edge")
	}
	return child, edge, nil
}
func catalogNodeByID(catalog explorer.Catalog, id string) (explorer.CatalogNode, bool) {
	for _, node := range catalog.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return explorer.CatalogNode{}, false
}

func mapFields(fields []recipe.Field, nodeID, occurrenceID, path string, catalog explorer.Catalog, document *explorer.ExplorerBuilderDocumentV1, report *Report) error {
	for index, field := range fields {
		if field.Expr.Select == "" || field.Expr.Call != "" || field.Expr.Literal != nil || field.Expr.Document != nil {
			report.Unmapped = append(report.Unmapped, fmt.Sprintf("%s.fields[%d].expr", path, index))
			return &ConversionError{Report: *report}
		}
		selectPath := strings.TrimSpace(field.Expr.Select)
		if dot := strings.IndexByte(selectPath, '.'); dot > 0 {
			prefix := selectPath[:dot]
			if prefix == "root" || strings.HasPrefix(prefix, "route_") {
				selectPath = selectPath[dot+1:]
			}
		}
		matches := []explorer.CatalogSelection{}
		for _, selection := range catalog.Selections {
			if selection.NodeID != nodeID {
				continue
			}
			if field.FieldRef != "" && selection.FieldRef == field.FieldRef {
				matches = append(matches, selection)
				continue
			}
			if field.FieldRef == "" && selection.Select == selectPath {
				matches = append(matches, selection)
			}
		}
		if len(matches) != 1 {
			if len(matches) == 0 {
				report.Unmapped = append(report.Unmapped, fmt.Sprintf("%s.fields[%d]", path, index))
			} else {
				report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("%s.fields[%d]", path, index))
			}
			return &ConversionError{Report: *report}
		}
		candidate := matches[0].ID
		found := false
		for _, old := range document.CandidateIDs {
			if old == candidate {
				found = true
			}
		}
		if !found {
			document.CandidateIDs = append(document.CandidateIDs, candidate)
		}
		if occurrenceID != "base" {
			for _, ref := range document.CandidateOccurrences {
				if ref.CandidateID == candidate && ref.OccurrenceID == occurrenceID {
					report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("%s.fields[%d]", path, index))
					return &ConversionError{Report: *report}
				}
			}
			document.CandidateOccurrences = append(document.CandidateOccurrences, explorer.ExplorerCandidateOccurrenceV1{CandidateID: candidate, OccurrenceID: occurrenceID})
		}
		emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00"+occurrenceID+"\x00"+candidate)
		report.Mappings = append(report.Mappings, Mapping{LegacyPath: fmt.Sprintf("%s.fields[%d]", path, index), LegacyFieldName: field.Name, CandidateID: candidate, OccurrenceID: occurrenceID, EmissionID: emissionID})
	}
	return nil
}

func mapPresentation(cfg explorer.ConfigV2, documentByLegacyOutput map[string]int, documents []explorer.ExplorerBuilderDocumentV1, mappings []Mapping, report *Report) error {
	for viewIndex, view := range cfg.Views {
		documentIndex, ok := documentByLegacyOutput[view.Output]
		if !ok {
			report.Unmapped = append(report.Unmapped, fmt.Sprintf("views[%d].output", viewIndex))
			return &ConversionError{Report: *report}
		}
		document := &documents[documentIndex]
		for index, column := range view.Table.Columns {
			mapping, ok, ambiguous := findMappingByFieldName(mappings, document.Output.ID, column.Column)
			if ambiguous {
				report.Ambiguous = append(report.Ambiguous, fmt.Sprintf("views[%s].table.columns[%d]", view.ID, index))
				return &ConversionError{Report: *report}
			}
			if !ok {
				report.Unmapped = append(report.Unmapped, fmt.Sprintf("views[%s].table.columns[%d]", view.ID, index))
				return &ConversionError{Report: *report}
			}
			binding := document.Presentation[mapping.EmissionID]
			binding.Label = firstNonEmpty(column.Label, column.Column)
			visible := column.Visible
			binding.Visible = &visible
			document.Presentation[mapping.EmissionID] = binding
		}
		for _, filter := range view.Filters {
			mapping, ok, ambiguous := findMappingByFieldName(mappings, document.Output.ID, filter.Column)
			if ambiguous {
				report.Ambiguous = append(report.Ambiguous, "views.filters")
				return &ConversionError{Report: *report}
			}
			if !ok {
				report.Unmapped = append(report.Unmapped, "views.filters")
				return &ConversionError{Report: *report}
			}
			binding := document.Presentation[mapping.EmissionID]
			if filter.Label != "" {
				binding.Filter = &explorer.ExplorerFilterBindingV1{Label: filter.Label}
			}
			document.Presentation[mapping.EmissionID] = binding
		}
		for _, chart := range view.Charts {
			mapping, ok, ambiguous := findMappingByFieldName(mappings, document.Output.ID, chart.Column)
			if ambiguous {
				report.Ambiguous = append(report.Ambiguous, "views.charts")
				return &ConversionError{Report: *report}
			}
			if !ok {
				report.Unmapped = append(report.Unmapped, "views.charts")
				return &ConversionError{Report: *report}
			}
			binding := document.Presentation[mapping.EmissionID]
			binding.Chart = &explorer.ExplorerChartBindingV1{Type: chart.Type, Title: chart.Title}
			document.Presentation[mapping.EmissionID] = binding
		}
	}
	return nil
}

func findMappingByFieldName(mappings []Mapping, outputID, name string) (Mapping, bool, bool) {
	var found Mapping
	count := 0
	for _, mapping := range mappings {
		if mapping.OutputID == outputID && mapping.LegacyFieldName == name {
			found = mapping
			count++
		}
	}
	return found, count == 1, count > 1
}
func titleForOutput(cfg explorer.ConfigV2, output string) string {
	for _, view := range cfg.Views {
		if view.Output == output {
			return view.Title
		}
	}
	return output
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}
func traversalPath(parent string, index int) string {
	return fmt.Sprintf("%s.traversals[%d]", parent, index)
}

func (r Report) CanonicalJSON() ([]byte, error) { return json.Marshal(r) }
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
