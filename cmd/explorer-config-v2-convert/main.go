package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type report struct {
	Source          string         `json:"source"`
	Outputs         map[string]int `json:"outputs"`
	Converted       int            `json:"convertedColumns"`
	Unsupported     []string       `json:"unsupportedColumns"`
	DroppedOutputs  []string       `json:"droppedOutputs"`
	DroppedFeatures []string       `json:"droppedFeatures"`
}

func main() {
	input := flag.String("in", "", "legacy ExplorerConfigV2 JSON")
	output := flag.String("out", "", "V2 authoring workspace JSON")
	reportPath := flag.String("report", "", "conversion report JSON")
	flag.Parse()
	if *input == "" || *output == "" {
		fatalf("-in and -out are required")
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		fatalf("read input: %v", err)
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(raw, &config); err != nil {
		fatalf("decode input: %v", err)
	}
	bundle, err := recipe.Parse(config.Recipe)
	if err != nil {
		fatalf("decode recipe: %v", err)
	}
	workspace, conversionReport, err := convert(*input, config, bundle)
	if err != nil {
		fatalf("convert: %v", err)
	}
	workspaceJSON, err := json.MarshalIndent(workspace, "", "  ")
	if err != nil {
		fatalf("encode workspace: %v", err)
	}
	workspaceJSON = append(workspaceJSON, '\n')
	if err := os.WriteFile(*output, workspaceJSON, 0o644); err != nil {
		fatalf("write workspace: %v", err)
	}
	if *reportPath != "" {
		reportJSON, _ := json.MarshalIndent(conversionReport, "", "  ")
		if err := os.WriteFile(*reportPath, append(reportJSON, '\n'), 0o644); err != nil {
			fatalf("write report: %v", err)
		}
	}
	if len(conversionReport.Unsupported) != 0 {
		fatalf("conversion left %d unsupported columns; see %s", len(conversionReport.Unsupported), *reportPath)
	}
}

func convert(source string, config explorer.ConfigV2, bundle recipe.Bundle) (authoringv2.Workspace, report, error) {
	result := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: config.Explorer.Title, Description: config.Explorer.Description}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}, SharedFilters: map[string][]authoringv2.SharedFilterBinding{}}
	conversionReport := report{Source: source, Outputs: map[string]int{}, DroppedFeatures: []string{"unreferenced discovery-expanded columns", "unreferenced recipe outputs"}}
	outputs := map[string]recipe.Output{}
	for _, output := range bundle.Outputs {
		outputs[output.Name] = output
	}
	visibleOutputs := map[string]bool{}
	for tabIndex, view := range config.Views {
		legacyOutput, ok := outputs[view.Output]
		if !ok {
			return result, conversionReport, fmt.Errorf("view %q references unknown output %q", view.ID, view.Output)
		}
		visibleOutputs[view.Output] = true
		referenced := referencedColumns(config, view)
		paths := routePaths(legacyOutput)
		document := authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: view.Output, Title: view.Title, RowLabel: view.RowLabel}, RootResourceType: legacyOutput.RootResourceType, Columns: []authoringv2.Column{}, FixedFilters: []authoringv2.FixedFilter{}, Actions: []authoringv2.Action{}}
		usedOccurrences := map[string]bool{authoringv2.RootOccurrenceID: true}
		for _, name := range referenced {
			occurrenceID, leaf := occurrenceForColumn(name, paths)
			legacyPresentation := presentationFor(view, name)
			sourceSpec, logicalType, inferErr := inferSource(config.Project, legacyOutput.RootResourceType, occurrenceID, leaf, name)
			if inferErr != nil {
				conversionReport.Unsupported = append(conversionReport.Unsupported, view.Output+":"+name+":"+inferErr.Error())
				continue
			}
			usedOccurrences[occurrenceID] = true
			document.Columns = append(document.Columns, authoringv2.Column{Column: name, Label: legacyPresentation.label, LogicalType: logicalType, OccurrenceID: occurrenceID, Source: sourceSpec, Table: legacyPresentation.table, Filter: legacyPresentation.filter, Chart: legacyPresentation.chart})
		}
		document.Route = prunedRoute(legacyOutput, paths, usedOccurrences)
		for column, values := range view.FixedFilters {
			document.FixedFilters = append(document.FixedFilters, authoringv2.FixedFilter{Column: column, Values: append([]string(nil), values...)})
		}
		sort.Slice(document.FixedFilters, func(i, j int) bool { return document.FixedFilters[i].Column < document.FixedFilters[j].Column })
		for _, action := range view.Actions {
			converted := authoringv2.Action{Type: action.Type, Title: action.Title, FileName: action.FileName}
			for _, column := range action.Columns {
				converted.Columns = append(converted.Columns, authoringv2.ActionColumn{Column: column})
			}
			document.Actions = append(document.Actions, converted)
		}
		conversionReport.Outputs[view.Output] = len(document.Columns)
		conversionReport.Converted += len(document.Columns)
		result.Documents = append(result.Documents, document)
		result.Tabs = append(result.Tabs, authoringv2.Tab{ID: view.ID, Title: view.Title, OutputID: view.Output, Order: tabIndex, Visible: true})
	}
	for _, output := range bundle.Outputs {
		if !visibleOutputs[output.Name] {
			conversionReport.DroppedOutputs = append(conversionReport.DroppedOutputs, output.Name)
		}
	}
	for name, bindings := range config.SharedFilters {
		for _, binding := range bindings {
			if visibleOutputs[binding.Output] {
				result.SharedFilters[name] = append(result.SharedFilters[name], authoringv2.SharedFilterBinding{OutputID: binding.Output, Column: binding.Column})
			}
		}
	}
	if len(config.FileActions.Extensions) != 0 || len(config.FileActions.Actions) != 0 {
		result.FileActions = &authoringv2.FileActions{Extensions: config.FileActions.Extensions, Actions: config.FileActions.Actions}
	}
	if err := result.ValidateForPublication(); err != nil && len(conversionReport.Unsupported) == 0 {
		return result, conversionReport, err
	}
	sort.Strings(conversionReport.Unsupported)
	sort.Strings(conversionReport.DroppedOutputs)
	return result, conversionReport, nil
}

type routePath struct {
	id           string
	resourceType string
	relationship string
	parent       string
}

func routePaths(output recipe.Output) []routePath {
	result := []routePath{{id: authoringv2.RootOccurrenceID, resourceType: output.RootResourceType}}
	var walk func([]recipe.Traversal, string, string)
	walk = func(items []recipe.Traversal, parentID, prefix string) {
		for _, item := range items {
			alias := item.Alias
			if alias == "" {
				alias = item.Name
			}
			id := alias
			if prefix != "" {
				id = prefix + "__" + alias
			}
			result = append(result, routePath{id: id, resourceType: item.ToResourceType, relationship: item.Name, parent: parentID})
			walk(item.Traversals, id, id)
		}
	}
	walk(output.Traversals, authoringv2.RootOccurrenceID, "")
	return result
}

func occurrenceForColumn(column string, paths []routePath) (string, string) {
	best := authoringv2.RootOccurrenceID
	for _, path := range paths {
		if path.id != authoringv2.RootOccurrenceID && strings.HasPrefix(column, path.id+"__") && len(path.id) > len(best) {
			best = path.id
		}
	}
	if best == authoringv2.RootOccurrenceID {
		return best, column
	}
	return best, strings.TrimPrefix(column, best+"__")
}

func prunedRoute(output recipe.Output, paths []routePath, used map[string]bool) authoringv2.RouteNode {
	byID := map[string]routePath{}
	for _, path := range paths {
		byID[path.id] = path
	}
	for id := range used {
		for current := byID[id]; current.id != "" && current.parent != ""; current = byID[current.parent] {
			used[current.parent] = true
		}
	}
	var build func(string) authoringv2.RouteNode
	build = func(id string) authoringv2.RouteNode {
		path := byID[id]
		node := authoringv2.RouteNode{OccurrenceID: id, ResourceType: path.resourceType, Relationship: path.relationship}
		for _, child := range paths {
			if child.parent == id && used[child.id] {
				node.Children = append(node.Children, build(child.id))
			}
		}
		sort.Slice(node.Children, func(i, j int) bool { return node.Children[i].OccurrenceID < node.Children[j].OccurrenceID })
		return node
	}
	return build(authoringv2.RootOccurrenceID)
}

type columnPresentation struct {
	label  string
	table  *authoringv2.TablePresentation
	filter *authoringv2.FilterPresentation
	chart  *authoringv2.ChartPresentation
}

func presentationFor(view explorer.ConfigView, column string) columnPresentation {
	result := columnPresentation{label: column}
	for index, binding := range view.Table.Columns {
		if binding.Column == column {
			order, visible := index, binding.Visible
			result.label = first(binding.Label, result.label)
			result.table = &authoringv2.TablePresentation{Visible: &visible, Order: &order, Pinned: binding.Pinned, CellRenderer: binding.CellRenderer}
			if strings.EqualFold(binding.Label, "File Actions") {
				result.table.CellRenderer = "fileActions"
			}
		}
	}
	for index, binding := range view.Filters {
		if binding.Column == column {
			order := index
			result.label = first(binding.Label, result.label)
			result.filter = &authoringv2.FilterPresentation{Label: binding.Label, Order: &order}
		}
	}
	for index, binding := range view.Charts {
		if binding.Column == column {
			order := index
			result.chart = &authoringv2.ChartPresentation{Type: binding.Type, Title: binding.Title, Order: &order}
		}
	}
	return result
}

func referencedColumns(config explorer.ConfigV2, view explorer.ConfigView) []string {
	seen := map[string]bool{}
	add := func(value string) {
		if value != "" {
			seen[value] = true
		}
	}
	for _, item := range view.Table.Columns {
		add(item.Column)
	}
	for _, item := range view.Filters {
		add(item.Column)
	}
	for _, item := range view.Charts {
		add(item.Column)
	}
	for item := range view.FixedFilters {
		add(item)
	}
	for _, action := range view.Actions {
		for _, item := range action.Columns {
			add(item)
		}
	}
	for _, bindings := range config.SharedFilters {
		for _, item := range bindings {
			if item.Output == view.Output {
				add(item.Column)
			}
		}
	}
	result := make([]string, 0, len(seen))
	for item := range seen {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func inferSource(project, rootResourceType, occurrenceID, leaf, physical string) (authoringv2.ColumnSource, string, error) {
	if physical == "project_id" {
		return authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID, ProjectionMode: "FIRST"}, "string", nil
	}
	if marker := "observation_component_values__"; strings.Contains(leaf, marker) {
		return authoringv2.ColumnSource{Kind: authoringv2.SourceObservationComponentByCode, Match: leaf[strings.Index(leaf, marker)+len(marker):], FieldPath: "component[]", ProjectionMode: "FIRST"}, "string", nil
	}
	if marker := "identifier_by_system_"; strings.Contains(leaf, marker) {
		encoded := leaf[strings.Index(leaf, marker)+len(marker):]
		match, ok := decodeKnownURL(project, encoded)
		if !ok {
			return authoringv2.ColumnSource{}, "", fmt.Errorf("cannot recover identifier system %q", encoded)
		}
		return authoringv2.ColumnSource{Kind: authoringv2.SourceIdentifierBySystem, Match: match, FieldPath: "identifier[]", ProjectionMode: "FIRST"}, "string", nil
	}
	if marker := "extension_by_url_"; strings.Contains(leaf, marker) {
		encoded := leaf[strings.Index(leaf, marker)+len(marker):]
		match, ok := decodeKnownURL(project, encoded)
		if !ok {
			return authoringv2.ColumnSource{}, "", fmt.Errorf("cannot recover extension URL %q", encoded)
		}
		return authoringv2.ColumnSource{Kind: authoringv2.SourceExtensionByURL, Match: match, FieldPath: "extension[]", ProjectionMode: "FIRST"}, "string", nil
	}
	if rootResourceType == "DocumentReference" && strings.HasPrefix(leaf, "document_reference_") {
		name := strings.TrimPrefix(leaf, "document_reference_")
		switch name {
		case "title":
			return field("content[].attachment.title", "string"), "string", nil
		case "size":
			return field("content[].attachment.size", "integer"), "integer", nil
		case "identifier":
			return field("identifier[].value", "string"), "string", nil
		default:
			return authoringv2.ColumnSource{Kind: authoringv2.SourceCodingBySystem, Match: "https://humantumoratlas.org/" + name, FieldPath: "category[].coding[]", ProjectionMode: "FIRST"}, "string", nil
		}
	}
	switch leaf {
	case "research_subject_identifier", "specimen_identifier":
		return field("identifier[].value", "string"), "string", nil
	case "specimen_id":
		return field("id", "string"), "string", nil
	case "specimen_type_text":
		return field("type.text", "string"), "string", nil
	case "specimen_processing_method_text":
		return field("processing[].method.text", "string"), "string", nil
	case "deceasedBoolean":
		return field("deceasedBoolean", "boolean"), "boolean", nil
	case "code_coding_code":
		return field("code.coding[].code", "string"), "string", nil
	}
	return authoringv2.ColumnSource{}, "", fmt.Errorf("no fixed source mapping for occurrence %q leaf %q", occurrenceID, leaf)
}

func field(path, _ string) authoringv2.ColumnSource {
	return authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: path, ProjectionMode: "FIRST"}
}

func decodeKnownURL(project, encoded string) (string, bool) {
	projectKey := sanitize(project)
	aced := "https___aced_idp_org_" + projectKey
	if encoded == aced {
		return "https://aced-idp.org/" + project, true
	}
	if strings.HasPrefix(encoded, aced+"_") {
		return "https://aced-idp.org/" + project + "/" + strings.TrimPrefix(encoded, aced+"_"), true
	}
	htan := "https___humantumoratlas_org_"
	if strings.HasPrefix(encoded, htan) {
		return "https://humantumoratlas.org/" + strings.TrimPrefix(encoded, htan), true
	}
	core := "http___hl7_org_fhir_us_core_StructureDefinition_us_core_"
	if strings.HasPrefix(encoded, core) {
		name := strings.TrimPrefix(encoded, core)
		if name == "birthsex" {
			return "http://hl7.org/fhir/us/core/StructureDefinition-us-core-birthsex", true
		}
		return "http://hl7.org/fhir/us/core/StructureDefinition/us-core-" + name, true
	}
	return "", false
}

func sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, value)
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func fatalf(format string, values ...any) { fmt.Fprintf(os.Stderr, format+"\n", values...); os.Exit(1) }
