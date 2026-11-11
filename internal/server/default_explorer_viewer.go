package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/calypr/loom/internal/api/columncapabilities"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

var errDefaultViewerNotFound = errors.New("default Viewer not found")

type defaultViewerReleaseReader interface {
	ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error)
}

type defaultViewerExecutionReader interface {
	GetExecution(context.Context, string) (publication.BundleExecution, error)
}

type defaultViewerRecipeReader interface {
	LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error)
}

type defaultViewerScopeResolver interface {
	ResolveReadScopeForGeneration(context.Context, *authscope.Principal, string, string, []string) (authscope.ReadScope, error)
}

type repositoryDefaultViewer struct {
	releases   defaultViewerReleaseReader
	executions defaultViewerExecutionReader
	recipes    defaultViewerRecipeReader
	scopes     defaultViewerScopeResolver
	recipeName string
}

func newRepositoryDefaultViewer(
	releases defaultViewerReleaseReader,
	executions defaultViewerExecutionReader,
	recipes defaultViewerRecipeReader,
	scopes defaultViewerScopeResolver,
	recipeName string,
) defaultViewerLookup {
	if releases == nil || executions == nil || recipes == nil || strings.TrimSpace(recipeName) == "" {
		return nil
	}
	scopes = normalizeDefaultViewerScopeResolver(scopes)
	viewer := repositoryDefaultViewer{releases: releases, executions: executions, recipes: recipes, scopes: scopes, recipeName: strings.TrimSpace(recipeName)}
	return viewer.load
}

func normalizeDefaultViewerScopeResolver(scopes defaultViewerScopeResolver) defaultViewerScopeResolver {
	if scopes == nil {
		return nil
	}
	if concrete, ok := scopes.(*authscope.ScopeResolver); ok && concrete == nil {
		return nil
	}
	return scopes
}

func (v repositoryDefaultViewer) load(ctx context.Context, rawProject string, principal *authscope.Principal) (explorer.ExplorerStateV1, error) {
	project := projectid.Canonical(rawProject)
	if project == "" {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}
	active, err := v.releases.ReadActiveRelease(ctx, projectid.Legacy(project))
	if errors.Is(err, dataset.ErrNoActiveRelease) {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("read active project release: %w", err)
	}
	release := active.Release
	if projectid.Canonical(release.Project) != project || strings.TrimSpace(release.Generation) == "" {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}

	bindings := make([]dataset.ReleasePublication, 0, len(release.Publications))
	executionID := ""
	for _, binding := range release.Publications {
		if binding.Selector.Recipe != v.recipeName || binding.Stale || binding.Generation != release.Generation {
			continue
		}
		if binding.Selector.Validate() != nil || strings.TrimSpace(binding.ExecutionID) == "" {
			return explorer.ExplorerStateV1{}, fmt.Errorf("active default recipe release binding is invalid")
		}
		if executionID == "" {
			executionID = binding.ExecutionID
		} else if executionID != binding.ExecutionID {
			return explorer.ExplorerStateV1{}, fmt.Errorf("active default recipe outputs do not share one execution")
		}
		bindings = append(bindings, binding)
	}
	if len(bindings) == 0 {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}

	execution, err := v.executions.GetExecution(ctx, executionID)
	if errors.Is(err, publication.ErrBundleNotFound) {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("read active default recipe execution: %w", err)
	}
	execution = execution.CanonicalizeLegacy()
	if execution.ID != executionID || projectid.Canonical(execution.Project) != project ||
		execution.DatasetGeneration != release.Generation || execution.Name != v.recipeName || !execution.State.Successful() {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}
	if err := authscope.AuthorizeProject(principal, execution.Project, false); err != nil {
		return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
	}
	if v.scopes != nil {
		scope, scopeErr := v.scopes.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths)
		if errors.Is(scopeErr, authscope.ErrForbidden) {
			return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
		}
		if scopeErr != nil {
			return explorer.ExplorerStateV1{}, fmt.Errorf("resolve active default recipe read scope: %w", scopeErr)
		}
		switch scope.Mode {
		case authscope.ReadScopeUnrestricted:
		case authscope.ReadScopeRestricted:
			if !scope.CoversExecutionPaths(execution.AuthResourcePaths) {
				return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
			}
		default:
			return explorer.ExplorerStateV1{}, errDefaultViewerNotFound
		}
	}

	if strings.TrimSpace(execution.TranslationVersion) == "" || strings.TrimSpace(execution.RecipeDigest) == "" || strings.TrimSpace(execution.SchemaDigest) == "" {
		return explorer.ExplorerStateV1{}, fmt.Errorf("active default recipe execution identity is incomplete")
	}
	entry, err := v.recipes.LoadRecipeVersion(ctx, execution.Name, execution.TranslationVersion)
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("load pinned default recipe version: %w", err)
	}
	if entry.Bundle.Name != execution.Name || entry.Bundle.TranslationVersion != execution.TranslationVersion || entry.Digest != execution.RecipeDigest {
		return explorer.ExplorerStateV1{}, fmt.Errorf("pinned default recipe version does not match the active execution")
	}
	bundle, err := entry.Bundle.CanonicalBundle()
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("canonicalize pinned default recipe version: %w", err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("digest pinned default recipe version: %w", err)
	}
	if digest != execution.RecipeDigest {
		return explorer.ExplorerStateV1{}, fmt.Errorf("pinned default recipe digest does not match the active execution")
	}

	outputs, err := queryableBoundOutputs(bundle.Outputs, bindings, execution)
	if err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	revision, err := defaultViewerRevision(project, bundle, execution, outputs)
	if err != nil {
		return explorer.ExplorerStateV1{}, err
	}
	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil {
		return explorer.ExplorerStateV1{}, fmt.Errorf("build active default Viewer projection: %w", err)
	}
	if runtime == nil || len(runtime.Outputs) != len(bundle.Outputs) {
		return explorer.ExplorerStateV1{}, fmt.Errorf("active default recipe has no complete Viewer projection")
	}

	updatedAt := execution.UpdatedAt
	if execution.PublishedAt != nil {
		updatedAt = *execution.PublishedAt
	}
	if updatedAt.IsZero() {
		updatedAt = release.CreatedAt
	}
	return explorer.ExplorerStateV1{
		APIVersion: explorer.ExplorerStateV1APIVersion,
		Kind:       explorer.ExplorerStateV1Kind,
		Project:    project,
		ExplorerID: "default",
		Title:      "Default",
		Management: explorer.ManagementRepository,
		Active:     explorer.ExplorerStateV1Active{Status: string(explorer.RevisionActive)},
		Generated: explorer.ExplorerStateV1Generated{
			RecipeDigest:         execution.RecipeDigest,
			ResolvedSchemaDigest: execution.SchemaDigest,
			SourceGeneration:     execution.DatasetGeneration,
			EmittedColumns:       revision.EmittedColumns,
			Materializations:     revision.Materializations,
			Dataset:              revision.Dataset,
			Publication:          revision.Publication,
			QualityReports:       revision.QualityReports,
		},
		Runtime:   runtime,
		ActiveURL: "/api/v1/projects/" + url.PathEscape(project) + "/explorers/default",
		UpdatedAt: updatedAt,
	}, nil
}

type queryableDefaultOutput struct {
	binding dataset.ReleasePublication
	record  publication.BundleOutputRecord
}

func queryableBoundOutputs(recipeOutputs []recipe.Output, bindings []dataset.ReleasePublication, execution publication.BundleExecution) (map[string]queryableDefaultOutput, error) {
	if len(recipeOutputs) == 0 || len(recipeOutputs) != len(bindings) || len(execution.Outputs) != len(recipeOutputs) {
		return nil, errDefaultViewerNotFound
	}
	bindingsByOutput := make(map[string]dataset.ReleasePublication, len(bindings))
	for _, binding := range bindings {
		if binding.Generation != execution.DatasetGeneration || binding.ExecutionID != execution.ID ||
			binding.Selector.Recipe != execution.Name || binding.Selector.TranslationVersion != execution.TranslationVersion {
			return nil, errDefaultViewerNotFound
		}
		if _, exists := bindingsByOutput[binding.Selector.Output]; exists {
			return nil, errDefaultViewerNotFound
		}
		bindingsByOutput[binding.Selector.Output] = binding
	}
	outputsByName := make(map[string]publication.BundleOutputRecord, len(execution.Outputs))
	for _, output := range execution.Outputs {
		if _, exists := outputsByName[output.Name]; exists {
			return nil, fmt.Errorf("active default recipe execution has duplicate output records")
		}
		outputsByName[output.Name] = output
	}
	result := make(map[string]queryableDefaultOutput, len(recipeOutputs))
	for _, recipeOutput := range recipeOutputs {
		binding, ok := bindingsByOutput[recipeOutput.Name]
		if !ok {
			return nil, errDefaultViewerNotFound
		}
		output, ok := outputsByName[recipeOutput.Name]
		if !ok || !output.Queryable() || output.Selector != binding.Selector {
			return nil, errDefaultViewerNotFound
		}
		result[recipeOutput.Name] = queryableDefaultOutput{binding: binding, record: output}
	}
	return result, nil
}

func defaultViewerRevision(project string, bundle recipe.Bundle, execution publication.BundleExecution, outputs map[string]queryableDefaultOutput) (explorer.Revision, error) {
	revision := explorer.Revision{
		Project:              project,
		ExplorerID:           "default",
		Recipe:               bundle,
		RecipeDigest:         execution.RecipeDigest,
		ResolvedSchemaDigest: execution.SchemaDigest,
		SourceGeneration:     execution.DatasetGeneration,
		Dataset: explorer.DatasetMetadata{
			Generation:   execution.DatasetGeneration,
			SchemaDigest: execution.SchemaDigest,
			Outputs:      make([]explorer.DatasetOutput, 0, len(bundle.Outputs)),
		},
		Materializations: make([]explorer.Materialization, 0, len(bundle.Outputs)),
		EmittedColumns:   make([]explorer.EmittedColumn, 0),
		Publication: explorer.PublicationMetadata{
			State: "ACTIVE", Generation: execution.DatasetGeneration, ExecutionID: execution.ID,
		},
		QualityReports: publication.CloneQualityReports(execution.QualityReports),
		Status:         explorer.RevisionActive,
		CreatedAt:      execution.CreatedAt,
		ReadyAt:        execution.PublishedAt,
	}
	contracts := explorer.PublicOutputContracts{Outputs: make([]explorer.PublicOutputContract, 0, len(bundle.Outputs))}
	for _, recipeOutput := range bundle.Outputs {
		bound, ok := outputs[recipeOutput.Name]
		if !ok {
			return explorer.Revision{}, errDefaultViewerNotFound
		}
		selector := bound.binding.Selector
		columns, contract := defaultViewerOutputColumns(recipeOutput, bound.record.Columns)
		publicColumns := physicalColumnsForDefaultViewerEmissions(bound.record.Columns, columns)
		revision.EmittedColumns = append(revision.EmittedColumns, columns...)
		contracts.Outputs = append(contracts.Outputs, contract)
		revision.Dataset.Outputs = append(revision.Dataset.Outputs, explorer.DatasetOutput{
			Name: recipeOutput.Name, State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: publicColumns,
		})
		revision.Materializations = append(revision.Materializations, explorer.Materialization{
			OutputID: recipeOutput.Name, Output: recipeOutput.Name, MaterializationID: execution.ID,
			Selector: &selector, Columns: append([]publication.PhysicalColumn(nil), publicColumns...),
		})
	}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("encode default Viewer output contract: %w", err)
	}
	revision.PublicOutputContract = contractJSON
	return revision, nil
}

func physicalColumnsForDefaultViewerEmissions(columns []publication.PhysicalColumn, emitted []explorer.EmittedColumn) []publication.PhysicalColumn {
	publicNames := make(map[string]struct{}, len(emitted))
	for _, column := range emitted {
		publicNames[column.PublicColumn] = struct{}{}
	}
	result := make([]publication.PhysicalColumn, 0, len(publicNames))
	for _, column := range columns {
		if _, ok := publicNames[column.Name]; ok {
			result = append(result, column)
		}
	}
	return result
}

func defaultViewerOutputColumns(output recipe.Output, physicalColumns []publication.PhysicalColumn) ([]explorer.EmittedColumn, explorer.PublicOutputContract) {
	emitted := make([]explorer.EmittedColumn, 0, len(physicalColumns))
	contract := explorer.PublicOutputContract{
		OutputID: output.Name, RootResourceType: output.RootResourceType, RowGrain: output.RowGrain,
		Columns: make([]explorer.PublicOutputColumn, 0, len(physicalColumns)),
	}
	for _, physical := range physicalColumns {
		if defaultViewerInternalColumn(physical) {
			continue
		}
		capability := columncapabilities.FromClickHouse(physical.ClickHouse)
		logicalType := strings.TrimSpace(physical.LogicalType)
		if logicalType == "" {
			logicalType = capability.Logical
		}
		if logicalType == "" {
			logicalType = "string"
		}
		if logicalType == "object" || logicalType == "json" || capability.Logical == "json" {
			continue
		}
		repeated := physical.Repeated || capability.Repeated
		nullable := physical.Nullable || capability.Nullable
		label := defaultViewerColumnLabel(output, physical)
		emissionID := strings.TrimSpace(physical.ID)
		if emissionID == "" {
			emissionID = physical.Name
		}
		filterable := !repeated
		cardinality, shape := "required_one", "scalar"
		if repeated {
			cardinality, shape, filterable = "many", "array", false
		} else if nullable {
			cardinality = "optional_one"
		}
		column := explorer.EmittedColumn{
			EmissionID: emissionID, OutputID: output.Name, PublicColumn: physical.Name,
			Label: label, LogicalType: logicalType, Cardinality: cardinality,
			Nullable: nullable, Shape: shape, Filterable: filterable,
		}
		emitted = append(emitted, column)
		contract.Columns = append(contract.Columns, publicOutputColumnFromEmission(column))
	}
	if len(emitted) == 0 {
		contract.Lossless, contract.MLReady = true, true
	}
	for _, column := range emitted {
		contract.Lossless = contract.Lossless && column.Lossless
		contract.MLReady = contract.MLReady && column.MLReady
	}
	return emitted, contract
}

func defaultViewerInternalColumn(column publication.PhysicalColumn) bool {
	return column.LoomOwned || column.Name == "_key" || column.Name == "auth_resource_path" ||
		column.Name == "project_id" || strings.HasPrefix(column.Name, "__loom_")
}

func defaultViewerColumnLabel(output recipe.Output, physical publication.PhysicalColumn) string {
	for _, field := range output.Fields {
		matchesID := field.ColumnID != "" && field.ColumnID == physical.ID
		name := field.Name
		if output.RootColumnNaming != recipe.RootColumnNamingExact {
			name = publication.FlatColumnName(output.RootResourceType, name)
		}
		if matchesID || name == physical.Name {
			if strings.TrimSpace(field.Label) != "" {
				return field.Label
			}
		}
	}
	return physical.Name
}

func defaultViewerLegacyFailure(err error) error {
	if errors.Is(err, errDefaultViewerNotFound) || errors.Is(err, authscope.ErrForbidden) {
		return legacyOperationFailure(404, "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	return legacyOperationFailure(500, "DEFAULT_VIEWER_UNAVAILABLE", "The default Viewer is unavailable", err)
}

func missingDefaultExplorer(err error) bool {
	var lifecycleErr *lifecycle.Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Class == lifecycle.ClassNotFound && lifecycleErr.Code == "EXPLORER_NOT_FOUND"
}
