package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

type projectConfigSnapshot struct {
	bundle               recipe.Bundle
	resolvedSchemaDigest string
	emitted              []explorer.EmittedColumn
	contractJSON         json.RawMessage
	publicByOutput       map[string]map[string]explorer.EmittedColumn
	physicalByOutput     map[string][]publication.PhysicalColumn
	availableByOutput    map[string]map[string]explorer.EmittedColumn
}

func PrepareProjectConfigs(configs []explorer.ProjectConfig, resolved execution.Resolved, executionValue publication.BundleExecution, sourceCommit string) ([]explorer.ProjectConfigPublication, error) {
	return prepareProjectConfigs(configs, resolved, executionValue, sourceCommit, false)
}

func PrepareProjectConfigsForPublication(configs []explorer.ProjectConfig, resolved execution.Resolved, executionValue publication.BundleExecution, sourceCommit string) ([]explorer.ProjectConfigPublication, error) {
	return prepareProjectConfigs(configs, resolved, executionValue, sourceCommit, true)
}

func prepareProjectConfigs(configs []explorer.ProjectConfig, resolved execution.Resolved, executionValue publication.BundleExecution, sourceCommit string, tolerateUnavailableBindings bool) ([]explorer.ProjectConfigPublication, error) {
	snapshot, err := prepareProjectConfigSnapshot(resolved, executionValue, sourceCommit)
	if err != nil {
		return nil, err
	}
	matching := make([]explorer.ProjectConfig, 0, len(configs))
	for _, config := range configs {
		if config.RecipeRef.Name == snapshot.bundle.Name {
			matching = append(matching, config)
		}
	}
	if len(matching) == 0 {
		return []explorer.ProjectConfigPublication{}, nil
	}
	if err := validateProjectConfigDefinitions(matching); err != nil {
		return nil, err
	}
	publications := make([]explorer.ProjectConfigPublication, 0, len(matching))
	for index, original := range matching {
		definition, err := explorer.CloneProjectConfigDefinition(original)
		if err != nil {
			return nil, fmt.Errorf("configs[%d] %q: clone definition: %w", index, original.ID, err)
		}
		definitionDigest, err := definition.Digest()
		if err != nil {
			return nil, fmt.Errorf("configs[%d] %q: %w", index, definition.ID, err)
		}
		diagnostics := []explorer.Diagnostic{}
		if tolerateUnavailableBindings {
			diagnostics = projectConfigBindingDiagnostics(definition, snapshot.availableByOutput)
		} else if err := validateProjectConfigBindings(definition, snapshot.availableByOutput); err != nil {
			return nil, fmt.Errorf("configs[%d] %q: %w", index, definition.ID, err)
		}
		configJSON, err := projectConfigPresentationJSON(definition, executionValue.Project, snapshot.emitted)
		if err != nil {
			return nil, fmt.Errorf("encode config %q presentation: %w", definition.ID, err)
		}
		revisionID, err := projectConfigRevisionID(executionValue.Project, definition.ID, definitionDigest, executionValue, sourceCommit)
		if err != nil {
			return nil, fmt.Errorf("identify config %q revision: %w", definition.ID, err)
		}
		revision := snapshot.configRevision(definition, definitionDigest, configJSON, revisionID, executionValue, diagnostics)
		if _, err := explorer.BuildViewerProjection(&revision); err != nil {
			return nil, fmt.Errorf("validate prepared config %q revision: %w", definition.ID, err)
		}
		publications = append(publications, explorer.ProjectConfigPublication{Definition: definition, Revision: revision})
	}
	return publications, nil
}

func prepareProjectConfigSnapshot(resolved execution.Resolved, executionValue publication.BundleExecution, sourceCommit string) (projectConfigSnapshot, error) {
	if err := validatePreparedExecution(resolved, executionValue, sourceCommit); err != nil {
		return projectConfigSnapshot{}, err
	}
	bundle, err := resolved.Bundle.CanonicalBundle()
	if err != nil {
		return projectConfigSnapshot{}, fmt.Errorf("canonicalize resolved recipe: %w", err)
	}
	retained, err := validateFinalPublicationSchema(resolved, executionValue)
	if err != nil {
		return projectConfigSnapshot{}, err
	}
	emitted, contracts, publicByOutput, physicalByOutput, err := projectConfigOutputMetadata(bundle, resolved.Compiled.Outputs, executionValue, retained)
	if err != nil {
		return projectConfigSnapshot{}, err
	}
	if err := contracts.ValidateAgainst(bundle, emitted); err != nil {
		return projectConfigSnapshot{}, fmt.Errorf("validate compiler-derived public output contract: %w", err)
	}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		return projectConfigSnapshot{}, fmt.Errorf("encode compiler-derived public output contract: %w", err)
	}
	return projectConfigSnapshot{
		bundle: bundle, resolvedSchemaDigest: resolved.ResolvedSchemaDigest,
		emitted: emitted, contractJSON: contractJSON,
		publicByOutput: publicByOutput, physicalByOutput: physicalByOutput,
		availableByOutput: projectConfigAvailableColumns(publicByOutput, retained),
	}, nil
}

func (snapshot projectConfigSnapshot) configRevision(definition explorer.ProjectConfig, digest string, configJSON json.RawMessage, revisionID string, executionValue publication.BundleExecution, diagnostics []explorer.Diagnostic) explorer.Revision {
	readyAt := executionValue.PublishedAt.UTC()
	return explorer.Revision{
		ID: revisionID, Project: executionValue.Project, ExplorerID: definition.ID,
		Config: configJSON, IntentDigest: digest,
		PublicOutputContract: append(json.RawMessage(nil), snapshot.contractJSON...),
		Recipe:               snapshot.bundle, RecipeDigest: executionValue.RecipeDigest,
		ResolvedSchemaDigest: snapshot.resolvedSchemaDigest, SourceGeneration: executionValue.DatasetGeneration,
		Materializations: projectConfigMaterializations(snapshot.bundle, executionValue, snapshot.physicalByOutput),
		EmittedColumns:   cloneProjectConfigEmissions(snapshot.emitted),
		Dataset: explorer.DatasetMetadata{
			Generation: executionValue.DatasetGeneration, SchemaDigest: executionValue.SchemaDigest,
			Outputs: projectConfigDatasetOutputs(snapshot.bundle, executionValue, snapshot.physicalByOutput),
		},
		QualityReports: publication.CloneQualityReports(executionValue.QualityReports),
		Publication: explorer.PublicationMetadata{
			State: string(explorer.RevisionReady), Generation: executionValue.DatasetGeneration,
			ExecutionID: executionValue.ID, RevisionID: revisionID, UpdatedAt: readyAt,
		},
		Diagnostics: append([]explorer.Diagnostic{}, diagnostics...), Status: explorer.RevisionReady,
		CreatedAt: executionValue.CreatedAt.UTC(), ReadyAt: &readyAt,
	}
}

func validatePreparedExecution(resolved execution.Resolved, published publication.BundleExecution, sourceCommit string) error {
	if strings.TrimSpace(sourceCommit) == "" || sourceCommit != strings.TrimSpace(sourceCommit) {
		return errors.New("source commit is required")
	}
	if strings.TrimSpace(published.ID) == "" || strings.TrimSpace(published.Project) == "" || strings.TrimSpace(published.DatasetGeneration) == "" {
		return errors.New("published execution identity is incomplete")
	}
	if published.State.Canonical() != publication.BundlePublished {
		return fmt.Errorf("execution %q is not published", published.ID)
	}
	if published.CreatedAt.IsZero() || published.PublishedAt == nil || published.PublishedAt.IsZero() {
		return fmt.Errorf("execution %q is missing stable publication timestamps", published.ID)
	}

	bindings := resolved.Semantic.SemanticPlan.Bindings
	if strings.TrimSpace(bindings.Project) == "" || published.Project != bindings.Project {
		return fmt.Errorf("execution project %q does not match resolved project %q", published.Project, bindings.Project)
	}
	if resolved.Bundle.Name == "" || resolved.Bundle.Name != published.Name || resolved.Bundle.TranslationVersion != published.TranslationVersion {
		return errors.New("execution recipe identity does not match resolved recipe")
	}
	if resolved.StoredRecipeDigest == "" || published.RecipeDigest != resolved.StoredRecipeDigest {
		return errors.New("execution stored recipe digest does not match resolved recipe")
	}
	if resolved.ResolvedSchemaDigest == "" || resolved.Semantic.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest {
		return errors.New("resolved schema identity is incomplete")
	}
	if resolved.Semantic.SourceGeneration == "" || resolved.Compiled.SourceGeneration != resolved.Semantic.SourceGeneration || bindings.DatasetGeneration != resolved.Semantic.SourceGeneration || published.DatasetGeneration != resolved.Semantic.SourceGeneration {
		return errors.New("execution generation does not match resolved recipe")
	}
	if resolved.Semantic.ScopeDigest == "" || resolved.Compiled.ScopeDigest != resolved.Semantic.ScopeDigest || published.ScopeDigest != resolved.Semantic.ScopeDigest {
		return errors.New("execution authorization scope does not match resolved recipe")
	}
	if string(bindings.AuthScopeMode) != published.AuthScopeMode || !sameProjectConfigStrings(bindings.AuthResourcePaths, published.AuthResourcePaths) {
		return errors.New("execution authorization paths do not match resolved recipe")
	}
	if len(resolved.Bundle.Outputs) == 0 || len(resolved.Bundle.Outputs) != len(resolved.Compiled.Outputs) || len(resolved.Bundle.Outputs) != len(published.Outputs) {
		return errors.New("execution output set does not match resolved recipe")
	}
	if len(resolved.Compiled.Outputs) == 0 || len(resolved.Compiled.Outputs) != len(published.Outputs) {
		return errors.New("compiled output set does not match published execution")
	}

	outputs := make(map[string]publication.BundleOutputRecord, len(published.Outputs))
	for index, output := range published.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("execution outputs[%d] has an empty name", index)
		}
		if _, duplicate := outputs[output.Name]; duplicate {
			return fmt.Errorf("execution contains duplicate output %q", output.Name)
		}
		outputs[output.Name] = output
	}
	for index, compiled := range resolved.Compiled.Outputs {
		recipeOutput := resolved.Bundle.Outputs[index]
		if compiled.Name != recipeOutput.Name || compiled.RootResourceType != recipeOutput.RootResourceType || compiled.RootColumnNaming != recipeOutput.RootColumnNaming.Normalized() {
			return fmt.Errorf("compiled output %q does not match resolved recipe output %q", compiled.Name, recipeOutput.Name)
		}
		output, ok := outputs[compiled.Name]
		if !ok {
			return fmt.Errorf("execution is missing compiled output %q", compiled.Name)
		}
		expectedSelector := dataset.DataframeSelector{Recipe: resolved.Bundle.Name, TranslationVersion: resolved.Bundle.TranslationVersion, Output: compiled.Name}
		if err := expectedSelector.Validate(); err != nil {
			return fmt.Errorf("resolved output %q selector: %w", compiled.Name, err)
		}
		if output.Selector != expectedSelector || output.Selector.Validate() != nil {
			return fmt.Errorf("execution selector for output %q does not match resolved recipe", compiled.Name)
		}
		if !output.Queryable() {
			return fmt.Errorf("execution output %q is not queryable", compiled.Name)
		}
	}
	return nil
}

func projectConfigOutputMetadata(
	bundle recipe.Bundle,
	compiledOutputs []lower.CompiledRecipeOutput,
	executionValue publication.BundleExecution,
	retained map[string]map[string]struct{},
) ([]explorer.EmittedColumn, explorer.PublicOutputContracts, map[string]map[string]explorer.EmittedColumn, map[string][]publication.PhysicalColumn, error) {
	emitted := make([]explorer.EmittedColumn, 0)
	contracts := explorer.PublicOutputContracts{Outputs: make([]explorer.PublicOutputContract, 0, len(bundle.Outputs))}
	publicByOutput := make(map[string]map[string]explorer.EmittedColumn, len(bundle.Outputs))
	physicalByOutput := make(map[string][]publication.PhysicalColumn, len(bundle.Outputs))
	physicalOutputs := make(map[string]publication.BundleOutputRecord, len(executionValue.Outputs))
	for _, output := range executionValue.Outputs {
		physicalOutputs[output.Name] = output
	}
	for index, recipeOutput := range bundle.Outputs {
		compiled := compiledOutputs[index]
		physicalOutput := physicalOutputs[recipeOutput.Name]
		contract := explorer.PublicOutputContract{
			OutputID: recipeOutput.Name, RootResourceType: recipeOutput.RootResourceType,
			RowGrain: recipeOutput.RowGrain, Lossless: true, MLReady: true,
			Columns: make([]explorer.PublicOutputColumn, 0, len(compiled.OutputSchema)),
		}
		columnsByName := make(map[string]explorer.EmittedColumn, len(compiled.OutputSchema))
		seenEmissionIDs := make(map[string]struct{}, len(compiled.OutputSchema))
		for columnIndex, schemaColumn := range compiled.OutputSchema {
			if schemaColumn.Internal || schemaColumn.Identity {
				continue
			}
			name, err := projectConfigPublicColumnName(recipeOutput, schemaColumn)
			if err != nil {
				return nil, explorer.PublicOutputContracts{}, nil, nil, fmt.Errorf("compiled output %q schema[%d]: %w", recipeOutput.Name, columnIndex, err)
			}
			emissionID := strings.TrimSpace(schemaColumn.ID)
			if emissionID == "" {
				emissionID = strings.TrimSpace(schemaColumn.SemanticPath)
			}
			if emissionID == "" {
				return nil, explorer.PublicOutputContracts{}, nil, nil, fmt.Errorf("compiled output %q column %q has no stable compiler emission identity", recipeOutput.Name, name)
			}
			if _, duplicate := seenEmissionIDs[emissionID]; duplicate {
				return nil, explorer.PublicOutputContracts{}, nil, nil, fmt.Errorf("compiled output %q duplicates emission identity %q", recipeOutput.Name, emissionID)
			}
			seenEmissionIDs[emissionID] = struct{}{}
			if strings.TrimSpace(schemaColumn.Label) == "" || !validProjectConfigLogicalType(schemaColumn.Kind) || !validProjectConfigCardinality(schemaColumn.Cardinality) {
				return nil, explorer.PublicOutputContracts{}, nil, nil, fmt.Errorf("compiled output %q column %q is missing supported label/type/cardinality metadata", recipeOutput.Name, name)
			}
			if _, duplicate := columnsByName[name]; duplicate {
				return nil, explorer.PublicOutputContracts{}, nil, nil, fmt.Errorf("compiled output %q duplicates public column %q", recipeOutput.Name, name)
			}
			filterable, chartable := projectConfigScalarCapabilities(schemaColumn)
			shape := projectConfigColumnShape(schemaColumn)
			emission := explorer.EmittedColumn{
				EmissionID: emissionID, OutputID: recipeOutput.Name, PublicColumn: name,
				Label: schemaColumn.Label, LogicalType: schemaColumn.Kind,
				Cardinality: schemaColumn.Cardinality, Nullable: schemaColumn.Nullable,
				ResultUnit: cloneProjectConfigUnit(schemaColumn.NormalizedUnit), Shape: shape,
				SourcePath: schemaColumn.SemanticPath,

				Lossless: false, MLReady: false,
				Filterable: filterable, Chartable: chartable,
			}
			columnsByName[name] = emission
			contract.Columns = append(contract.Columns, publicOutputColumnFromProjectConfigEmission(emission))
			contract.Lossless = contract.Lossless && emission.Lossless
			contract.MLReady = contract.MLReady && emission.MLReady
			emitted = append(emitted, emission)
		}
		physicalColumns, err := projectConfigPhysicalColumns(recipeOutput.Name, columnsByName, retained[recipeOutput.Name], physicalOutput.Columns)
		if err != nil {
			return nil, explorer.PublicOutputContracts{}, nil, nil, err
		}
		publicByOutput[recipeOutput.Name] = columnsByName
		physicalByOutput[recipeOutput.Name] = physicalColumns
		contracts.Outputs = append(contracts.Outputs, contract)
	}
	return emitted, contracts, publicByOutput, physicalByOutput, nil
}

func validateFinalPublicationSchema(
	resolved execution.Resolved,
	published publication.BundleExecution,
) (map[string]map[string]struct{}, error) {
	outputs := make(map[string]publication.BundleOutputRecord, len(published.Outputs))
	for _, output := range published.Outputs {
		outputs[output.Name] = output
	}

	schemas := make([]publication.OutputSchema, 0, len(resolved.Bundle.Outputs))
	retainedByOutput := make(map[string]map[string]struct{}, len(resolved.Bundle.Outputs))
	for _, recipeOutput := range resolved.Bundle.Outputs {
		publishedOutput := outputs[recipeOutput.Name]
		columns, err := publication.WithPublicationMetadata(execution.PublicationColumns(resolved, recipeOutput.Name))
		if err != nil {
			return nil, fmt.Errorf("prepare publication schema for output %q: %w", recipeOutput.Name, err)
		}
		physicalByName := make(map[string]publication.PhysicalColumn, len(publishedOutput.Columns))
		for _, column := range publishedOutput.Columns {
			if _, duplicate := physicalByName[column.Name]; duplicate {
				return nil, fmt.Errorf("published output %q has duplicate physical column %q", recipeOutput.Name, column.Name)
			}
			physicalByName[column.Name] = column
		}

		retained := make([]publication.LogicalColumn, 0, len(columns))
		retainedNames := make(map[string]struct{}, len(columns))
		expectedNames := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			if _, duplicate := expectedNames[column.Name]; duplicate {
				return nil, fmt.Errorf("publication schema for output %q duplicates column %q", recipeOutput.Name, column.Name)
			}
			expectedNames[column.Name] = struct{}{}
			physical, ok := physicalByName[column.Name]
			if !ok {
				if column.Provenance == publication.ColumnDiscovered && !column.LoomOwned && !column.IsIdentity {
					continue
				}
				return nil, fmt.Errorf("published output %q is missing explicit compiler column %q", recipeOutput.Name, column.Name)
			}
			if err := validateProjectConfigPhysicalColumn(recipeOutput.Name, column, physical); err != nil {
				return nil, err
			}
			retained = append(retained, column)
			retainedNames[column.Name] = struct{}{}
		}
		for name := range physicalByName {
			if _, ok := expectedNames[name]; !ok {
				return nil, fmt.Errorf("published output %q contains column %q outside the compiler publication schema", recipeOutput.Name, name)
			}
		}
		var sourceRow *publication.SourceRowMetadata
		if publishedOutput.SourceRow != nil {
			copy := *publishedOutput.SourceRow
			sourceRow = &copy
		}
		schemas = append(schemas, publication.OutputSchema{Name: recipeOutput.Name, Columns: retained, SourceRow: sourceRow})
		retainedByOutput[recipeOutput.Name] = retainedNames
	}
	identity := publication.PublicationIdentity{
		Name: published.Name, TranslationVersion: published.TranslationVersion,
		Project: published.Project, DatasetGeneration: published.DatasetGeneration,
		RecipeDigest: published.RecipeDigest, ScopeDigest: published.ScopeDigest,
		EngineVersion: published.EngineVersion, AuthScopeMode: published.AuthScopeMode,
		AuthResourcePaths: append([]string(nil), published.AuthResourcePaths...),
	}
	expectedDigest := publication.FinalSchemaDigest(identity, schemas)
	if expectedDigest != published.SchemaDigest {
		return nil, fmt.Errorf("published final schema digest %q does not match compiler-derived publication schema %q", published.SchemaDigest, expectedDigest)
	}
	return retainedByOutput, nil
}

func validateProjectConfigPhysicalColumn(output string, logical publication.LogicalColumn, physical publication.PhysicalColumn) error {
	wantLoomOwned := logical.LoomOwned || logical.IsIdentity || strings.HasPrefix(logical.Name, "__loom_") || logical.Name == "auth_resource_path" || logical.Name == "project_id"
	if physical.ID != logical.ID || physical.SemanticPath != logical.SemanticPath || physical.LogicalType != logical.Kind || physical.Nullable != logical.Nullable || physical.Repeated != logical.Repeated || physical.Provenance != logical.Provenance || physical.LoomOwned != wantLoomOwned {
		return fmt.Errorf("published output %q column %q metadata does not match the compiler publication schema", output, logical.Name)
	}
	return nil
}

func projectConfigPhysicalColumns(output string, public map[string]explorer.EmittedColumn, retained map[string]struct{}, columns []publication.PhysicalColumn) ([]publication.PhysicalColumn, error) {
	physical := make(map[string]publication.PhysicalColumn, len(columns))
	for _, column := range columns {
		if _, duplicate := physical[column.Name]; duplicate {
			return nil, fmt.Errorf("published output %q has duplicate physical column %q", output, column.Name)
		}
		physical[column.Name] = column
	}
	filtered := make([]publication.PhysicalColumn, 0, len(retained))
	for name := range public {
		if _, present := retained[name]; !present {
			continue
		}
		column, ok := physical[name]
		if !ok {
			return nil, fmt.Errorf("published output %q is missing compiler public column %q", output, name)
		}
		if column.LoomOwned || name == "auth_resource_path" || name == "project_id" || name == "_key" || strings.HasPrefix(name, "__loom_") {
			return nil, fmt.Errorf("published output %q marks compiler public column %q as internal", output, name)
		}
		filtered = append(filtered, column)
	}

	ordered := make([]publication.PhysicalColumn, 0, len(filtered))
	for _, column := range columns {
		if _, ok := public[column.Name]; ok {
			ordered = append(ordered, column)
		}
	}
	return ordered, nil
}

func projectConfigAvailableColumns(public map[string]map[string]explorer.EmittedColumn, retained map[string]map[string]struct{}) map[string]map[string]explorer.EmittedColumn {
	available := make(map[string]map[string]explorer.EmittedColumn, len(public))
	for output, columns := range public {
		available[output] = make(map[string]explorer.EmittedColumn, len(columns))
		for name, column := range columns {
			if _, ok := retained[output][name]; ok {
				available[output][name] = column
			}
		}
	}
	return available
}

func publicOutputColumnFromProjectConfigEmission(emitted explorer.EmittedColumn) explorer.PublicOutputColumn {
	return explorer.PublicOutputColumn{
		Column: emitted.PublicColumn, Label: emitted.Label, LogicalType: emitted.LogicalType,
		Cardinality: emitted.Cardinality, Nullable: emitted.Nullable,
		ResultUnit: cloneProjectConfigUnit(emitted.ResultUnit), Shape: emitted.Shape,
		SourcePath: emitted.SourcePath, Lossless: emitted.Lossless, MLReady: emitted.MLReady,
		Filterable: emitted.Filterable, Chartable: emitted.Chartable,
	}
}

func validateProjectConfigBindings(config explorer.ProjectConfig, public map[string]map[string]explorer.EmittedColumn) error {
	for _, view := range config.Views {
		columns, ok := public[view.Output]
		if !ok {
			return fmt.Errorf("view %q references unknown output %q", view.ID, view.Output)
		}
		for _, column := range view.Table.Columns {
			if _, ok := columns[column.Column]; !ok {
				return fmt.Errorf("view %q table references unknown or internal column %q", view.ID, column.Column)
			}
		}
		for _, filter := range view.Filters {
			emitted, ok := columns[filter.Column]
			if !ok || !emitted.Filterable {
				return fmt.Errorf("view %q filter references unsupported column %q", view.ID, filter.Column)
			}
		}
		for _, chart := range view.Charts {
			emitted, ok := columns[chart.Column]
			if !ok || !emitted.Chartable {
				return fmt.Errorf("view %q chart references unsupported column %q", view.ID, chart.Column)
			}
		}
		for name := range view.FixedFilters {
			emitted, ok := columns[name]
			if !ok || !emitted.Filterable {
				return fmt.Errorf("view %q fixed filter references unsupported column %q", view.ID, name)
			}
		}
		for index, action := range view.Actions {
			output := view.Output
			if action.Output != "" {
				output = action.Output
			}
			actionColumns, ok := public[output]
			if !ok {
				return fmt.Errorf("view %q action %d references unknown output %q", view.ID, index, output)
			}
			for _, name := range action.Columns {
				if _, ok := actionColumns[name]; !ok {
					return fmt.Errorf("view %q action %d references unknown or internal column %q", view.ID, index, name)
				}
			}
			for name := range action.ExportHeaders {
				if _, ok := actionColumns[name]; !ok {
					return fmt.Errorf("view %q action %d export header references unknown or internal column %q", view.ID, index, name)
				}
			}
		}
	}
	for name, filters := range config.SharedFilters {
		for _, filter := range filters {
			column, ok := public[filter.Output][filter.Column]
			if !ok || !column.Filterable {
				return fmt.Errorf("shared filter %q references unsupported column %s.%s", name, filter.Output, filter.Column)
			}
		}
	}
	return nil
}

func projectConfigPresentationJSON(config explorer.ProjectConfig, project string, emitted []explorer.EmittedColumn) (json.RawMessage, error) {
	views := append([]explorerconfig.ConfigView(nil), config.Views...)
	for index := range views {
		if len(views[index].Table.Columns) == 0 {
			continue
		}
		configured := make(map[string]explorerconfig.ConfigColumn, len(views[index].Table.Columns))
		for _, column := range views[index].Table.Columns {
			configured[column.Column] = column
		}
		columns := make([]explorerconfig.ConfigColumn, 0, len(configured))
		for _, column := range emitted {
			if column.OutputID != views[index].Output {
				continue
			}
			binding, ok := configured[column.PublicColumn]
			if !ok {
				binding = explorerconfig.ConfigColumn{Column: column.PublicColumn, Visible: false}
			}
			columns = append(columns, binding)
		}
		views[index].Table.Columns = columns
	}
	configV2 := explorerconfig.ConfigV2{
		APIVersion: explorerconfig.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: projectid.Canonical(project),
		Explorer: explorerconfig.ConfigExplorer{ID: config.ID, Title: config.Title, Management: "repository"},
		Views:    views, SharedFilters: config.SharedFilters, FileActions: config.FileActions,
	}

	type presentationConfig struct {
		explorerconfig.ConfigV2
		Recipe json.RawMessage `json:"recipe,omitempty"`
	}
	raw, err := json.Marshal(presentationConfig{ConfigV2: configV2})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func cloneProjectConfigEmissions(values []explorer.EmittedColumn) []explorer.EmittedColumn {
	cloned := append([]explorer.EmittedColumn(nil), values...)
	for index := range cloned {
		cloned[index].ResultUnit = cloneProjectConfigUnit(cloned[index].ResultUnit)
	}
	return cloned
}

func cloneProjectConfigUnit(value *unit.UnitIdentity) *unit.UnitIdentity {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func projectConfigRevisionID(project, configID, configDigest string, executionValue publication.BundleExecution, sourceCommit string) (string, error) {
	identity := struct {
		Project, ConfigID, ConfigDigest, ExecutionID, RecipeDigest, SchemaDigest, Generation, SourceCommit string
	}{
		Project: project, ConfigID: configID, ConfigDigest: configDigest,
		ExecutionID: executionValue.ID, RecipeDigest: executionValue.RecipeDigest,
		SchemaDigest: executionValue.SchemaDigest, Generation: executionValue.DatasetGeneration,
		SourceCommit: sourceCommit,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "project_config_" + hex.EncodeToString(sum[:]), nil
}

func projectConfigMaterializations(bundle recipe.Bundle, executionValue publication.BundleExecution, physical map[string][]publication.PhysicalColumn) []explorer.Materialization {
	materializations := make([]explorer.Materialization, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		selector := executionValue.Selector(output.Name)
		materializations = append(materializations, explorer.Materialization{
			OutputID: output.Name, Output: output.Name, MaterializationID: executionValue.ID,
			Selector: &selector, Columns: append([]publication.PhysicalColumn(nil), physical[output.Name]...),
		})
	}
	return materializations
}

func projectConfigDatasetOutputs(bundle recipe.Bundle, executionValue publication.BundleExecution, physical map[string][]publication.PhysicalColumn) []explorer.DatasetOutput {
	outputsByName := make(map[string]publication.BundleOutputRecord, len(executionValue.Outputs))
	for _, output := range executionValue.Outputs {
		outputsByName[output.Name] = output
	}
	outputs := make([]explorer.DatasetOutput, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		executionOutput := outputsByName[output.Name]
		selector := executionOutput.Selector
		outputs = append(outputs, explorer.DatasetOutput{
			Name: output.Name, State: string(executionOutput.State.Canonical()), Queryable: true,
			Selector: &selector, Columns: append([]publication.PhysicalColumn(nil), physical[output.Name]...),
		})
	}
	return outputs
}

func sameProjectConfigStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
