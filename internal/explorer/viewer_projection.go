package explorer

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
)

// buildViewerProjection is the single translation from immutable Explorer
// publication state to the renderer-facing contract. It intentionally does
// not require an authored ConfigView: a valid recipe is enough to produce a
// default table that a client can extend with presentation choices.
// BuildViewerProjection is the pure immutable-revision to Viewer wire
// projection. It performs no persistence lookup and has no service state.
func BuildViewerProjection(revision *Revision) *ExplorerRuntimeV1 {
	if revision == nil {
		return nil
	}

	config, hasConfig := decodeProjectionConfig(revision.Config)
	bundle := revision.Recipe
	if len(bundle.Outputs) == 0 && hasConfig {
		if parsed, err := recipe.Parse(config.Recipe); err == nil {
			bundle = parsed
		}
	}
	if len(bundle.Outputs) == 0 {
		// A revision without a recipe is not an executable query. The caller
		// still returns a valid ExplorerState response with runtime: null.
		return nil
	}

	datasetOutputs := make(map[string]DatasetOutput, len(revision.Dataset.Outputs))
	physicalColumns := make(map[string]map[string]publication.PhysicalColumn, len(revision.Dataset.Outputs))
	for _, output := range revision.Dataset.Outputs {
		datasetOutputs[output.Name] = output
		physicalColumns[output.Name] = make(map[string]publication.PhysicalColumn, len(output.Columns))
		for _, column := range output.Columns {
			physicalColumns[output.Name][column.Name] = column
		}
	}

	materializations := make(map[string]Materialization, len(revision.Materializations))
	for _, materialization := range revision.Materializations {
		outputID := firstNonEmptyString(materialization.OutputID, materialization.Output)
		if outputID == "" {
			continue
		}
		materializations[outputID] = materialization
		if physicalColumns[outputID] == nil {
			physicalColumns[outputID] = map[string]publication.PhysicalColumn{}
		}
		for _, column := range materialization.Columns {
			physicalColumns[outputID][column.Name] = column
		}
	}
	authoritativeColumns := make(map[string]bool, len(physicalColumns))
	for outputID, columns := range physicalColumns {
		authoritativeColumns[outputID] = len(columns) > 0
	}

	emitted := make(map[string]map[string]EmittedColumn)
	for _, column := range revision.EmittedColumns {
		if column.OutputID == "" || column.PublicColumn == "" {
			continue
		}
		if emitted[column.OutputID] == nil {
			emitted[column.OutputID] = map[string]EmittedColumn{}
		}
		emitted[column.OutputID][column.PublicColumn] = column
		// Emissions are a compatibility fallback for outputs whose publication
		// metadata has no columns at all. Once DatasetMetadata or Materialization
		// metadata supplies any column for an output, it is authoritative and
		// historical emissions must not manufacture additional runtime columns.
		if !authoritativeColumns[column.OutputID] {
			if physicalColumns[column.OutputID] == nil {
				physicalColumns[column.OutputID] = map[string]publication.PhysicalColumn{}
			}
			if _, exists := physicalColumns[column.OutputID][column.PublicColumn]; !exists {
				physicalColumns[column.OutputID][column.PublicColumn] = publication.PhysicalColumn{
					Name:        column.PublicColumn,
					LogicalType: column.LogicalType,
					ClickHouse:  column.LogicalType,
				}
			}
		}
	}
	// Labels are frozen by the public output contract. They are indexed by the
	// authored physical column, never reconstructed from compiler identities.
	contractLabels := map[string]map[string]string{}
	if contracts, err := DecodePublicOutputContracts(revision.PublicOutputContract); err == nil {
		for _, output := range contracts.Outputs {
			contractLabels[output.OutputID] = map[string]string{}
			for _, column := range output.Columns {
				contractLabels[output.OutputID][column.Column] = column.Label
			}
		}
	}

	status := firstNonEmptyString(revision.Publication.State, string(revision.Status), ExplorerRuntimeV1NotPublished)
	publicationState := revision.Publication
	publicationState.State = firstNonEmptyString(publicationState.State, status)
	runtime := &ExplorerRuntimeV1{
		Status:        status,
		Generation:    firstNonEmptyString(revision.Dataset.Generation, revision.SourceGeneration, revision.Publication.Generation),
		Publication:   publicationState,
		Schema:        ExplorerRuntimeSchemaV1{Digest: firstNonEmptyString(revision.Dataset.SchemaDigest, revision.ResolvedSchemaDigest), Version: ConfigV2APIVersion},
		Outputs:       []ExplorerRuntimeOutputV1{},
		SharedFilters: map[string][]ExplorerRuntimeBindingV1{},
		FileActions:   config.FileActions,
		Diagnostics:   append([]Diagnostic(nil), revision.Diagnostics...),
	}

	views := config.Views
	if !hasConfig || len(views) == 0 {
		views = defaultProjectionViews(bundle)
	}
	for _, view := range views {
		if view.Output == "" {
			continue
		}
		selector, ok := projectionSelector(view.Output, datasetOutputs, materializations, bundle)
		if !ok {
			continue
		}

		columnsByName := map[string]ExplorerRuntimeColumnV1{}
		ensureColumn := func(name string) (ExplorerRuntimeColumnV1, bool) {
			if existing, ok := columnsByName[name]; ok {
				return existing, true
			}
			published, ok := physicalColumns[view.Output][name]
			if !ok {
				return ExplorerRuntimeColumnV1{}, false
			}
			emission := emitted[view.Output][name]
			// Runtime is an intersection of compiler emissions and authoritative
			// materialized columns. Physical schema metadata is never promoted into
			// a user-visible column by itself.
			if emission.PublicColumn == "" || strings.TrimSpace(emission.EmissionID) == "" {
				return ExplorerRuntimeColumnV1{}, false
			}
			filterable, chartable := emission.Filterable, emission.Chartable
			label := contractLabels[view.Output][name]
			if strings.TrimSpace(label) == "" {
				return ExplorerRuntimeColumnV1{}, false
			}
			column := ExplorerRuntimeColumnV1{
				Column:       name,
				EmissionID:   name,
				Name:         name,
				Label:        label,
				LogicalType:  firstNonEmptyString(emission.LogicalType, published.LogicalType, published.ClickHouse, "string"),
				Repeated:     published.Repeated,
				Filterable:   filterable,
				Sortable:     !published.Repeated,
				Chartable:    chartable,
				Aggregatable: filterable,
			}
			columnsByName[name] = column
			return column, true
		}

		ordered := []string{}
		seen := map[string]bool{}
		appendColumn := func(name string) {
			if seen[name] {
				return
			}
			if _, ok := ensureColumn(name); !ok {
				return
			}
			seen[name] = true
			ordered = append(ordered, name)
		}
		for _, binding := range view.Table.Columns {
			appendColumn(binding.Column)
		}
		remaining := make([]string, 0, len(physicalColumns[view.Output]))
		for name := range physicalColumns[view.Output] {
			if !seen[name] {
				remaining = append(remaining, name)
			}
		}
		sort.Strings(remaining)
		for _, name := range remaining {
			appendColumn(name)
		}

		table := ExplorerRuntimeTableV1{Columns: []ExplorerRuntimeTableColumnV1{}}
		for index, binding := range view.Table.Columns {
			column, ok := ensureColumn(binding.Column)
			if !ok {
				continue
			}
			column.Label = firstNonEmptyString(binding.Label, column.Label)
			column.Visible, column.Order = binding.Visible, index
			columnsByName[binding.Column] = column
			table.Columns = append(table.Columns, ExplorerRuntimeTableColumnV1{Column: column.Column, EmissionID: column.Column, Visible: binding.Visible, Pinned: binding.Pinned, CellRenderer: binding.CellRenderer})
		}
		columns := make([]ExplorerRuntimeColumnV1, 0, len(ordered))
		for index, name := range ordered {
			column := columnsByName[name]
			if len(view.Table.Columns) == 0 && !runtimeColumnIsInternal(name) {
				column.Visible, column.Order = true, index
				table.Columns = append(table.Columns, ExplorerRuntimeTableColumnV1{Column: column.Column, EmissionID: column.Column, Visible: true})
			}
			columns = append(columns, column)
		}

		bindings := func(values []ConfigFilter) []ExplorerRuntimeBindingV1 {
			result := []ExplorerRuntimeBindingV1{}
			for _, binding := range values {
				if column, ok := ensureColumn(binding.Column); ok {
					result = append(result, ExplorerRuntimeBindingV1{Column: column.Column, EmissionID: column.Column, OutputID: view.Output, Label: firstNonEmptyString(binding.Label, column.Label)})
				}
			}
			return result
		}
		charts := []ExplorerRuntimeBindingV1{}
		for _, chart := range view.Charts {
			if column, ok := ensureColumn(chart.Column); ok {
				charts = append(charts, ExplorerRuntimeBindingV1{Column: column.Column, EmissionID: column.Column, OutputID: view.Output, Type: chart.Type, Title: chart.Title})
			}
		}
		fixed := map[string][]string{}
		for name, values := range view.FixedFilters {
			if column, ok := ensureColumn(name); ok {
				fixed[column.Column] = append([]string(nil), values...)
			}
		}
		output := ExplorerRuntimeOutputV1{
			OutputID:     view.Output,
			Name:         firstNonEmptyString(view.ID, view.Output),
			Title:        firstNonEmptyString(view.Title, view.Output),
			RowLabel:     firstNonEmptyString(view.RowLabel, view.Title, view.Output),
			Selector:     selector,
			Columns:      columns,
			Table:        table,
			Filters:      bindings(view.Filters),
			Charts:       charts,
			FixedFilters: fixed,
			Actions:      append([]ConfigAction(nil), view.Actions...),
		}
		if materialization, ok := materializations[view.Output]; ok {
			copy := materialization
			output.Materialization = &copy
		}
		runtime.Outputs = append(runtime.Outputs, output)
	}

	for name, filters := range config.SharedFilters {
		for _, filter := range filters {
			if _, ok := emitted[filter.Output][filter.Column]; ok {
				if _, published := physicalColumns[filter.Output][filter.Column]; published {
					runtime.SharedFilters[name] = append(runtime.SharedFilters[name], ExplorerRuntimeBindingV1{Column: filter.Column, EmissionID: filter.Column, OutputID: filter.Output})
				}
			}
		}
	}

	return runtime
}

func decodeProjectionConfig(raw json.RawMessage) (ConfigV2, bool) {
	if len(raw) == 0 {
		return ConfigV2{}, false
	}
	var config ConfigV2
	if json.Unmarshal(raw, &config) != nil {
		return ConfigV2{}, false
	}
	return config, true
}

func defaultProjectionViews(bundle recipe.Bundle) []ConfigView {
	views := make([]ConfigView, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		views = append(views, ConfigView{ID: output.Name, Title: output.Name, Output: output.Name})
	}
	return views
}

func projectionSelector(output string, datasetOutputs map[string]DatasetOutput, materializations map[string]Materialization, bundle recipe.Bundle) (dataset.DataframeSelector, bool) {
	if value := datasetOutputs[output].Selector; value != nil && value.Validate() == nil {
		return *value, true
	}
	if value := materializations[output].Selector; value != nil && value.Validate() == nil {
		return *value, true
	}
	selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output}
	return selector, selector.Validate() == nil
}

func runtimeColumnIsInternal(name string) bool {
	return name == "auth_resource_path" || strings.HasPrefix(name, "__loom_")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
