// Command explorer-authoring-live-migrate performs an explicit admin-only
// conversion of the active legacy Explorer packet into a V1 multi-output,
// multi-tab draft. It never activates or materializes the result.
//
// This is intentionally a best-effort bridge for the cutover. Legacy recipe
// transforms are lowered to their underlying catalog selection when possible;
// the report names every skipped or deduplicated field so the conversion is
// not mistaken for an exact legacy equivalence proof.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

type liveField struct {
	Project      string `json:"project"`
	ResourceType string `json:"resource_type"`
	Path         string `json:"path"`
	Kind         string `json:"kind"`
}

type liveSource struct {
	Key              string                   `json:"key"`
	Project          string                   `json:"project"`
	ExplorerID       string                   `json:"explorerId"`
	Title            string                   `json:"title"`
	SourceGeneration string                   `json:"sourceGeneration"`
	DraftVersion     int64                    `json:"draftVersion"`
	Config           json.RawMessage          `json:"config"`
	Emitted          []explorer.EmittedColumn `json:"emitted"`
	Fields           []liveField              `json:"fields"`
}

type migrationReport struct {
	Project            string   `json:"project"`
	ExplorerID         string   `json:"explorerId"`
	SourceGeneration   string   `json:"sourceGeneration"`
	LegacyRevisionID   string   `json:"legacyRevisionId"`
	LegacyOutputCount  int      `json:"legacyOutputCount"`
	V1DocumentCount    int      `json:"v1DocumentCount"`
	LegacyTabCount     int      `json:"legacyTabCount"`
	V1TabCount         int      `json:"v1TabCount"`
	ConvertedFields    int      `json:"convertedFields"`
	ConvertedBindings  int      `json:"convertedBindings"`
	DeduplicatedFields []string `json:"deduplicatedFields,omitempty"`
	SkippedFields      []string `json:"skippedFields,omitempty"`
	SkippedBindings    []string `json:"skippedBindings,omitempty"`
	Exact              bool     `json:"exact"`
	IntentDigest       string   `json:"intentDigest"`
	Applied            bool     `json:"applied"`
}

type cursorResponse struct {
	Result       []json.RawMessage `json:"result"`
	Error        bool              `json:"error"`
	ErrorMessage string            `json:"errorMessage"`
}

func main() {
	url := flag.String("url", "http://127.0.0.1:18529", "ArangoDB base URL")
	database := flag.String("database", "fhir_proto", "ArangoDB database")
	project := flag.String("project", "HTAN_INT-BForePC", "Loom project")
	explorerID := flag.String("explorer", "default", "Explorer identity")
	apply := flag.Bool("apply", false, "write the V1 draft to loom_explorers")
	flag.Parse()

	client := &http.Client{Timeout: 30 * time.Second}
	source, err := loadSource(client, strings.TrimRight(*url, "/"), *database, *project, *explorerID)
	if err != nil {
		fail(err)
	}

	bundle, report, err := convert(source)
	if err != nil {
		writeReport(report)
		fail(err)
	}
	report.IntentDigest = bundle.IntentDigest
	if *apply {
		if err := applyDraft(client, strings.TrimRight(*url, "/"), *database, source, bundle); err != nil {
			writeReport(report)
			fail(err)
		}
		report.Applied = true
	}
	writeReport(report)
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		fail(err)
	}
	_, _ = os.Stdout.Write(canonical)
	_, _ = os.Stdout.Write([]byte("\n"))
}

func loadSource(client *http.Client, baseURL, database, project, explorerID string) (liveSource, error) {
	query := `LET owner = FIRST(FOR d IN loom_explorers FILTER d.project == @project AND d.explorerId == @explorerId RETURN d)
LET revision = owner == null OR owner.activeRevisionId == null ? null : DOCUMENT("loom_explorer_revisions", owner.activeRevisionId)
LET fields = (FOR d IN fhir_field_catalog FILTER d.project == @project RETURN KEEP(d, "project", "resource_type", "path", "kind"))
FILTER owner != null
RETURN {key: owner._key, project: owner.project, explorerId: owner.explorerId, title: owner.activeConfig.explorer.title, sourceGeneration: owner.sourceGeneration, draftVersion: owner.draftVersion, config: owner.activeConfig, emitted: revision == null ? [] : revision.emittedColumns, fields: fields}`
	rows, err := runAQL(client, baseURL, database, query, map[string]any{"project": project, "explorerId": explorerID})
	if err != nil {
		return liveSource{}, err
	}
	if len(rows) != 1 {
		return liveSource{}, fmt.Errorf("expected one active Explorer source, got %d", len(rows))
	}
	var source liveSource
	if err := json.Unmarshal(rows[0], &source); err != nil {
		return liveSource{}, fmt.Errorf("decode live Explorer source: %w", err)
	}
	return source, nil
}

func convert(source liveSource) (explorer.ExplorerAuthoringBundleV1, migrationReport, error) {
	var config explorer.ConfigV2
	if err := json.Unmarshal(source.Config, &config); err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, migrationReport{}, fmt.Errorf("decode active ExplorerConfigV2: %w", err)
	}
	var legacy recipe.Bundle
	if err := json.Unmarshal(config.Recipe, &legacy); err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, migrationReport{}, fmt.Errorf("decode active recipe: %w", err)
	}
	report := migrationReport{Project: source.Project, ExplorerID: source.ExplorerID, SourceGeneration: source.SourceGeneration, LegacyRevisionID: "active", LegacyOutputCount: len(legacy.Outputs), LegacyTabCount: len(config.Views), Exact: false}

	catalog := make(map[string]liveField, len(source.Fields))
	for _, field := range source.Fields {
		if field.ResourceType == "" || field.Path == "" {
			continue
		}
		key := field.ResourceType + "\x00" + field.Path
		if _, exists := catalog[key]; !exists {
			catalog[key] = field
		}
	}
	emittedByOutputPublic := map[string]map[string]explorer.EmittedColumn{}
	for _, emitted := range source.Emitted {
		if emittedByOutputPublic[emitted.OutputID] == nil {
			emittedByOutputPublic[emitted.OutputID] = map[string]explorer.EmittedColumn{}
		}
		emittedByOutputPublic[emitted.OutputID][emitted.PublicColumn] = emitted
	}

	documents := make([]explorer.ExplorerBuilderDocumentV1, 0, len(legacy.Outputs))
	documentsByLegacyOutput := map[string]int{}
	pathsByOutput := map[string]map[string]string{}
	for _, output := range legacy.Outputs {
		outputID := explorer.StableExplorerID(output.Name)
		nodeID := explorer.OpaqueID("n_", output.RootResourceType)
		document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: outputID, Title: output.Name}, BaseNodeID: nodeID, RowNodeID: nodeID, Presentation: map[string]explorer.ExplorerPresentationBindingV1{}}
		pathsByOutput[output.Name] = map[string]string{}
		for _, field := range output.Fields {
			path, ok := firstSelectionPath(field.Expr)
			if !ok || strings.HasPrefix(path, "route_") {
				report.SkippedFields = append(report.SkippedFields, output.Name+"."+field.Name)
				continue
			}
			path = strings.TrimPrefix(path, "root.")
			key := output.RootResourceType + "\x00" + path
			catalogField, ok := catalog[key]
			if !ok {
				report.SkippedFields = append(report.SkippedFields, output.Name+"."+field.Name)
				continue
			}
			candidateID := explorer.OpaqueID("s_", catalogField.ResourceType+"\x00"+catalogField.Path)
			if addCandidate(&document, candidateID) {
				report.ConvertedFields++
			} else {
				report.DeduplicatedFields = append(report.DeduplicatedFields, output.Name+"."+field.Name)
			}
			pathsByOutput[output.Name][field.Name] = path
		}
		documentsByLegacyOutput[output.Name] = len(documents)
		documents = append(documents, document)
	}

	tabs := make([]explorer.ExplorerTabV1, 0, len(config.Views))
	for index, view := range config.Views {
		documentIndex, ok := documentsByLegacyOutput[view.Output]
		if !ok {
			report.SkippedBindings = append(report.SkippedBindings, view.ID+":unknown-output")
			continue
		}
		document := &documents[documentIndex]
		output := legacy.Outputs[documentIndex]
		outputPaths := pathsByOutput[view.Output]
		for columnIndex, column := range view.Table.Columns {
			path, ok := outputPaths[column.Column]
			if !ok {
				if emitted, exists := emittedByOutputPublic[view.Output][column.Column]; exists {
					path, ok = selectionPath(output.RootResourceType, emitted.SelectionID)
				}
			}
			candidateID, ok := candidateForPath(catalog, output.RootResourceType, path)
			if !ok {
				report.SkippedBindings = append(report.SkippedBindings, view.ID+"."+column.Column)
				continue
			}
			if !addCandidate(document, candidateID) {
				report.DeduplicatedFields = append(report.DeduplicatedFields, view.ID+"."+column.Column)
			}
			emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00base\x00"+candidateID)
			binding := document.Presentation[emissionID]
			binding.Label = firstNonEmpty(column.Label, column.Column)
			visible := column.Visible
			binding.Visible = &visible
			order := columnIndex
			binding.Order = &order
			document.Presentation[emissionID] = binding
			report.ConvertedBindings++
		}
		for _, filter := range view.Filters {
			path := outputPaths[filter.Column]
			if path == "" {
				if emitted, exists := emittedByOutputPublic[view.Output][filter.Column]; exists {
					path, _ = selectionPath(output.RootResourceType, emitted.SelectionID)
				}
			}
			candidateID, ok := candidateForPath(catalog, output.RootResourceType, path)
			if !ok || !presentationCandidate(catalog, output.RootResourceType, path) {
				report.SkippedBindings = append(report.SkippedBindings, view.ID+".filter."+filter.Column)
				continue
			}
			addCandidate(document, candidateID)
			emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00base\x00"+candidateID)
			binding := document.Presentation[emissionID]
			binding.Filter = &explorer.ExplorerFilterBindingV1{Label: filter.Label}
			document.Presentation[emissionID] = binding
		}
		for _, chart := range view.Charts {
			path := outputPaths[chart.Column]
			if path == "" {
				if emitted, exists := emittedByOutputPublic[view.Output][chart.Column]; exists {
					path, _ = selectionPath(output.RootResourceType, emitted.SelectionID)
				}
			}
			candidateID, ok := candidateForPath(catalog, output.RootResourceType, path)
			if !ok || !presentationCandidate(catalog, output.RootResourceType, path) {
				report.SkippedBindings = append(report.SkippedBindings, view.ID+".chart."+chart.Column)
				continue
			}
			addCandidate(document, candidateID)
			emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00base\x00"+candidateID)
			binding := document.Presentation[emissionID]
			binding.Chart = &explorer.ExplorerChartBindingV1{Type: chart.Type, Title: chart.Title}
			document.Presentation[emissionID] = binding
		}
		tabs = append(tabs, explorer.ExplorerTabV1{ID: explorer.StableExplorerID(view.ID), Title: view.Title, OutputID: document.Output.ID, Order: index})
	}
	for i := range documents {
		sort.Strings(documents[i].CandidateIDs)
	}
	bundle := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: source.Project, ExplorerID: source.ExplorerID, Title: source.Title, Documents: documents, Tabs: tabs}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, report, fmt.Errorf("canonicalize manual V1 bundle: %w", err)
	}
	if err := json.Unmarshal(canonical, &bundle); err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, report, fmt.Errorf("normalize manual V1 bundle: %w", err)
	}
	bundle.IntentDigest, err = bundle.DocumentDigest()
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, report, err
	}
	report.V1DocumentCount = len(bundle.AuthoringDocuments())
	report.V1TabCount = len(bundle.Tabs)
	return bundle, report, nil
}

func firstSelectionPath(expression recipe.Expression) (string, bool) {
	if expression.Select != "" {
		return expression.Select, true
	}
	for _, argument := range expression.Args {
		if argument.Select != "" {
			return argument.Select, true
		}
	}
	return "", false
}

func selectionPath(rootResourceType, selectionID string) (string, bool) {
	selectionID = strings.TrimSpace(strings.SplitN(selectionID, "#", 2)[0])
	prefix, path, ok := strings.Cut(selectionID, ".")
	if !ok || prefix != rootResourceType || path == "" {
		return "", false
	}
	return path, true
}

func candidateForPath(catalog map[string]liveField, resourceType, path string) (string, bool) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	if path == "" {
		return "", false
	}
	field, ok := catalog[resourceType+"\x00"+path]
	if !ok {
		return "", false
	}
	return explorer.OpaqueID("s_", field.ResourceType+"\x00"+field.Path), true
}

func presentationCandidate(catalog map[string]liveField, resourceType, path string) bool {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	field, ok := catalog[resourceType+"\x00"+path]
	return ok && field.Kind != "object" && field.Kind != "array"
}

func addCandidate(document *explorer.ExplorerBuilderDocumentV1, candidateID string) bool {
	for _, existing := range document.CandidateIDs {
		if existing == candidateID {
			return false
		}
	}
	document.CandidateIDs = append(document.CandidateIDs, candidateID)
	return true
}

func applyDraft(client *http.Client, baseURL, database string, source liveSource, bundle explorer.ExplorerAuthoringBundleV1) error {
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return err
	}
	var bundleObject map[string]any
	if err := json.Unmarshal(canonical, &bundleObject); err != nil {
		return err
	}
	query := `FOR d IN loom_explorers
FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId AND d.draftVersion == @expectedVersion
UPDATE d WITH {draftVersion: d.draftVersion + 1, draftAuthoringBundle: @bundle, draftIntentDigest: @digest, draftDigest: @digest, draftReceiptId: null, draftIdentityMappings: [], diagnostics: @diagnostics, updatedBy: @actor, updatedAt: @updatedAt} IN loom_explorers
RETURN NEW`
	rows, err := runAQL(client, baseURL, database, query, map[string]any{
		"key": source.Key, "project": source.Project, "explorerId": source.ExplorerID,
		"expectedVersion": source.DraftVersion, "bundle": bundleObject, "digest": bundle.IntentDigest,
		"diagnostics": []map[string]any{{"severity": "WARN", "stage": "migration", "code": "MANUAL_AUTHORING_MIGRATION", "message": "draft created by the live best-effort legacy conversion; inspect the migration report before compiling or publishing"}},
		"actor":       "manual-authoring-migration", "updatedAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("draft changed during manual migration; no write was applied")
	}
	return nil
}

func runAQL(client *http.Client, baseURL, database, query string, bindVars map[string]any) ([]json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"query": query, "bindVars": bindVars})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/_db/"+database+"/_api/cursor", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Arango query returned %s: %s", response.Status, strings.TrimSpace(string(raw)))
	}
	var result cursorResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Error {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}

func writeReport(report migrationReport) {
	raw, _ := json.MarshalIndent(report, "", "  ")
	_, _ = os.Stderr.Write(append(raw, '\n'))
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
