package compilation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
)

const TranslationVersion = "authoring-v2-native-10"

type Error struct {
	Stage   string
	Code    string
	Path    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "compilation error"
	}
	if e.Path == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + " at " + e.Path + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func fail(stage, code, path, message string, details map[string]any, cause error) error {
	return &Error{Stage: stage, Code: code, Path: path, Message: message, Details: details, Cause: cause}
}

type PresentationConfig struct {
	OutputID string               `json:"outputId"`
	Title    string               `json:"title"`
	Columns  []PresentationColumn `json:"columns"`
}

type PresentationColumn struct {
	EmissionID    string `json:"emissionId"`
	PublicColumn  string `json:"publicColumn"`
	Label         string `json:"label"`
	Visible       bool   `json:"visible"`
	Order         int    `json:"order"`
	PhysicalOrder int    `json:"-"`
	Pinned        bool   `json:"pinned"`
	FilterLabel   string `json:"filterLabel,omitempty"`
	FilterOrder   int    `json:"filterOrder,omitempty"`
	ChartType     string `json:"chartType,omitempty"`
	ChartTitle    string `json:"chartTitle,omitempty"`
	ChartOrder    int    `json:"chartOrder,omitempty"`
}

type Result struct {
	Bundle           recipe.Bundle
	RecipeDigest     string
	EmittedColumns   []viewer.EmittedColumn
	IdentityMappings []receiptmodel.IdentityMapping
	Presentation     PresentationConfig
	OutputContract   receiptmodel.PublicOutputContract
}

type WorkspaceResult struct {
	Bundle               recipe.Bundle
	RecipeDigest         string
	ResolvedInputsDigest string
	EmittedColumns       []viewer.EmittedColumn
	IdentityMappings     []receiptmodel.IdentityMapping
	Presentations        []PresentationConfig
	OutputContracts      []receiptmodel.PublicOutputContract
	Workspace            ws.Workspace
}

type ResolvedPopulation struct {
	OutputID            string                   `json:"outputId"`
	SelectionRevisionID string                   `json:"selectionRevisionId"`
	MembershipDigest    string                   `json:"membershipDigest"`
	MemberCount         int64                    `json:"memberCount"`
	ResourceType        string                   `json:"resourceType"`
	Route               []ws.PopulationRouteStep `json:"route,omitempty"`
}

type ResolvedImportedRecipe struct {
	Reference      ws.ImportedRecipeRef           `json:"reference"`
	Bundle         recipe.Bundle                  `json:"bundle"`
	ResolvedBundle recipe.Bundle                  `json:"resolvedBundle"`
	SourceColumns  []ResolvedImportedSourceColumn `json:"sourceColumns"`
}

type ResolvedImportedSourceColumn struct {
	Column       recipe.StageColumn `json:"column"`
	Cardinality  string             `json:"cardinality,omitempty"`
	Shape        string             `json:"shape,omitempty"`
	SemanticPath string             `json:"semanticPath,omitempty"`
}

func CompileWorkspace(ctx context.Context, project, explorerID string, workspace ws.Workspace, snapshot capability.Snapshot, resolvedInputs ResolvedInputs) (WorkspaceResult, error) {
	project = projectid.Canonical(project)
	wire := ws.CatalogFromCapability(snapshot, explorerID)
	var migrationErr error
	workspace, migrationErr = ws.CloneCurrentWorkspace(workspace)
	if migrationErr != nil {
		return WorkspaceResult{}, fail("intent", "LEGACY_CONTRIBUTOR_MIGRATION_FAILED", "$.workspace", migrationErr.Error(), nil, migrationErr)
	}
	if err := (ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &workspace, Catalog: wire}).Validate(); err != nil {
		return WorkspaceResult{}, fail("intent", "INVALID_AUTHORING_INTENT", "$.workspace", err.Error(), nil, err)
	}
	workspace = workspace.NormalizePresentationOrders()
	if err := validateResolvedPopulationCoverage(workspace, resolvedInputs); err != nil {
		return WorkspaceResult{}, fail("intent", "POPULATION_INPUT_MISMATCH", "$.resolvedInputs.populations", err.Error(), nil, err)
	}
	if err := validateResolvedInterpretationCoverage(project, snapshot, workspace, resolvedInputs); err != nil {
		return WorkspaceResult{}, fail("intent", "INTERPRETATION_INPUT_MISMATCH", "$.resolvedInputs.interpretations", err.Error(), nil, err)
	}
	if err := validateResolvedImportedRecipeCoverage(workspace, resolvedInputs); err != nil {
		return WorkspaceResult{}, fail("intent", "IMPORTED_RECIPE_INPUT_MISMATCH", "$.resolvedInputs.importedRecipes", err.Error(), nil, err)
	}
	resolvedInputsDigest, err := ResolvedInputsDigest(workspace, snapshot, resolvedInputs)
	if err != nil {
		return WorkspaceResult{}, fail("intent", "RESOLVED_INPUTS_DIGEST_FAILED", "$.workspace", "resolved input identity could not be calculated", nil, err)
	}

	effectiveWorkspace := applyResolvedInterpretations(workspace, resolvedInputs)
	if err := (ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &effectiveWorkspace, Catalog: wire}).Validate(); err != nil {
		return WorkspaceResult{}, fail("intent", "INVALID_RESOLVED_INTERPRETATION", "$.resolvedInputs.interpretations", err.Error(), nil, err)
	}
	result := WorkspaceResult{
		Workspace:            workspace,
		ResolvedInputsDigest: resolvedInputsDigest,
		Bundle: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "explorer_" + safeName(project) + "_" + safeName(explorerID),
			TranslationVersion:  TranslationVersion,
		},
		EmittedColumns:   []viewer.EmittedColumn{},
		IdentityMappings: []receiptmodel.IdentityMapping{},
		Presentations:    []PresentationConfig{},
		OutputContracts:  []receiptmodel.PublicOutputContract{},
	}
	workspaceOutputIDs := make([]string, 0, len(effectiveWorkspace.Documents))
	for _, document := range effectiveWorkspace.Documents {
		workspaceOutputIDs = append(workspaceOutputIDs, document.Output.ID)
	}
	for i, document := range effectiveWorkspace.Documents {
		population, hasResolvedPopulation := resolvedInputs.PopulationFor(document.Output.ID)
		if document.Population == nil {
			if hasResolvedPopulation {
				return WorkspaceResult{}, fail("intent", "POPULATION_INPUT_MISMATCH", fmt.Sprintf("$.workspace.documents[%d].population", i), "resolved population input has no matching authoring population", nil, nil)
			}
		} else {
			if !hasResolvedPopulation {
				return WorkspaceResult{}, fail("intent", "POPULATION_UNRESOLVED", fmt.Sprintf("$.workspace.documents[%d].population", i), "population selection must be resolved before compilation", nil, nil)
			}
			if err := validateResolvedPopulation(document, population, snapshot); err != nil {
				return WorkspaceResult{}, fail("intent", "POPULATION_INPUT_MISMATCH", fmt.Sprintf("$.workspace.documents[%d].population", i), err.Error(), nil, err)
			}
		}
		var compiled Result
		if document.ImportedRecipe != nil {
			imported, ok := resolvedInputs.ImportedRecipeFor(*document.ImportedRecipe)
			if !ok {
				return WorkspaceResult{}, fail("intent", "IMPORTED_RECIPE_UNRESOLVED", fmt.Sprintf("$.workspace.documents[%d].importedRecipe", i), "imported recipe must be resolved before compilation", nil, nil)
			}
			compiled, err = compileImportedDocument(ctx, project, explorerID, document, wire, snapshot, imported)
		} else {
			compiled, err = compileWithWorkspaceOutputs(ctx, project, explorerID, document, snapshot, workspaceOutputIDs)
		}
		if err != nil {
			return WorkspaceResult{}, fail("compile", "DOCUMENT_COMPILE_FAILED", fmt.Sprintf("$.workspace.documents[%d]", i), err.Error(), nil, err)
		}
		if len(compiled.Bundle.Outputs) == 1 && compiled.Bundle.Fragments != nil {
			if result.Bundle.Fragments == nil {
				result.Bundle.Fragments = compiled.Bundle.Fragments
			} else if !reflect.DeepEqual(result.Bundle.Fragments, compiled.Bundle.Fragments) {
				return WorkspaceResult{}, fail("compile", "IMPORTED_FRAGMENT_CONFLICT", fmt.Sprintf("$.workspace.documents[%d].importedRecipe", i), "imported outputs contain conflicting fragment libraries", nil, nil)
			}
		}
		if hasResolvedPopulation {
			if len(compiled.Bundle.Outputs) != 1 {
				return WorkspaceResult{}, fail("compile", "POPULATION_OUTPUT_MISMATCH", fmt.Sprintf("$.workspace.documents[%d]", i), "population could not be attached to the compiled output", nil, nil)
			}
			compiled.Bundle.Outputs[0].Population = recipePopulation(population)
		}
		result.Bundle.Outputs = append(result.Bundle.Outputs, compiled.Bundle.Outputs...)
		result.EmittedColumns = append(result.EmittedColumns, compiled.EmittedColumns...)
		result.IdentityMappings = append(result.IdentityMappings, compiled.IdentityMappings...)
		result.Presentations = append(result.Presentations, compiled.Presentation)
		result.OutputContracts = append(result.OutputContracts, compiled.OutputContract)
	}
	if err := result.Bundle.Validate(); err != nil {
		return WorkspaceResult{}, err
	}
	digest, err := result.Bundle.Digest()
	if err != nil {
		return WorkspaceResult{}, err
	}
	result.RecipeDigest = digest
	return result, nil
}

func validateResolvedPopulationCoverage(workspace ws.Workspace, inputs ResolvedInputs) error {
	expected := make(map[string]struct{})
	for _, document := range workspace.Documents {
		if document.Population != nil {
			expected[document.Output.ID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(inputs.Populations))
	for index, population := range inputs.Populations {
		outputID := strings.TrimSpace(population.OutputID)
		if outputID == "" {
			return fmt.Errorf("resolvedInputs.populations[%d].outputId is required", index)
		}
		if _, duplicate := seen[outputID]; duplicate {
			return fmt.Errorf("resolvedInputs.populations contains duplicate outputId %q", outputID)
		}
		seen[outputID] = struct{}{}
		if _, known := expected[outputID]; !known {
			return fmt.Errorf("resolved population outputId %q has no matching populated document", outputID)
		}
	}
	for outputID := range expected {
		if _, resolved := seen[outputID]; !resolved {
			return fmt.Errorf("populated document %q has no resolved population input", outputID)
		}
	}
	return nil
}

func validateResolvedPopulation(document ws.Document, resolved ResolvedPopulation, snapshot capability.Snapshot) error {
	if strings.TrimSpace(resolved.OutputID) != document.Output.ID {
		return fmt.Errorf("resolved population outputId %q does not match document outputId %q", resolved.OutputID, document.Output.ID)
	}
	if strings.TrimSpace(resolved.SelectionRevisionID) == "" || resolved.SelectionRevisionID != document.Population.SelectionRevisionID {
		return fmt.Errorf("resolved population selectionRevisionId does not match authoring intent")
	}
	if strings.TrimSpace(resolved.ResourceType) == "" {
		return fmt.Errorf("resolved population resourceType is required")
	}
	if resolved.MemberCount < 0 || strings.TrimSpace(resolved.MembershipDigest) == "" {
		return fmt.Errorf("resolved population membership identity is incomplete")
	}
	if len(resolved.Route) != len(document.Population.Route) {
		return fmt.Errorf("resolved population route does not match authoring intent")
	}
	for index, authoredStep := range document.Population.Route {
		resolvedStep := resolved.Route[index]
		withoutResolvedDirection := resolvedStep
		withoutResolvedDirection.StorageDirection = authoredStep.StorageDirection
		if withoutResolvedDirection != authoredStep || (authoredStep.StorageDirection != "" && resolvedStep.StorageDirection != authoredStep.StorageDirection) {
			return fmt.Errorf("resolved population route does not match authoring intent")
		}
		if authoredStep.StorageDirection != "" {
			continue
		}
		capabilityDirection, proven := populationRouteDirectionFromSnapshot(document, document.Population.Route, index, snapshot)
		if !proven {
			if resolvedStep.StorageDirection != "" {
				return fmt.Errorf("resolved population route direction is not proven by the current capability")
			}
			continue
		}
		if capabilityDirection != resolvedStep.StorageDirection {
			return fmt.Errorf("resolved population route direction does not match the current capability")
		}
	}
	return nil
}

func populationRouteDirectionFromSnapshot(document ws.Document, route []ws.PopulationRouteStep, targetIndex int, snapshot capability.Snapshot) (string, bool) {
	if targetIndex < 0 || targetIndex >= len(route) {
		return "", false
	}
	rootID, currentType := "", strings.TrimSpace(document.RootResourceType)
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == currentType {
			if rootID != "" {
				return "", false
			}
			rootID = node.ID
		}
	}
	if rootID == "" || currentType == "" {
		return "", false
	}
	currentID := rootID
	for index := 0; index <= targetIndex; index++ {
		step := route[index]
		edge, target, ok := resolvePopulationRouteEdge(snapshot, currentID, currentType, step)
		if !ok {
			return "", false
		}
		if index == targetIndex {
			direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
			if direction != "" && direction != "INBOUND" && direction != "OUTBOUND" {
				return "", false
			}
			return direction, true
		}
		currentID, currentType = target.ID, target.ResourceType
	}
	return "", false
}

func resolvePopulationRouteEdge(snapshot capability.Snapshot, currentID, currentType string, step ws.PopulationRouteStep) (capability.Edge, capability.Node, bool) {
	valid := func(edge capability.Edge) (capability.Node, bool) {
		return snapshot.MatchPopulationRouteEdge(edge, currentID, currentType, step.ResourceType, step.Relationship, step.StorageDirection)
	}
	if step.CatalogEdgeID != "" {
		edge, found := snapshot.Edge(step.CatalogEdgeID)
		if !found {
			return capability.Edge{}, capability.Node{}, false
		}
		target, ok := valid(edge)
		return edge, target, ok
	}
	var match capability.Edge
	var target capability.Node
	for _, edge := range snapshot.Edges {
		resolvedTarget, ok := valid(edge)
		if !ok {
			continue
		}
		if match.ID != "" {
			return capability.Edge{}, capability.Node{}, false
		}
		match, target = edge, resolvedTarget
	}
	return match, target, match.ID != ""
}

func validateResolvedInterpretationCoverage(project string, snapshot capability.Snapshot, workspace ws.Workspace, inputs ResolvedInputs) error {
	type expectedInterpretation struct {
		document ws.Document
		column   ws.Column
	}
	expected := make(map[string]expectedInterpretation)
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Interpretation == nil || column.Interpretation.Kind != ws.FeatureInterpretationPinned {
				continue
			}
			key := interpretationKey(document.Output.ID, column.Column, column.OccurrenceID)
			expected[key] = expectedInterpretation{document: document, column: column}
		}
	}
	seen := make(map[string]struct{}, len(inputs.Interpretations))
	for index, resolved := range inputs.Interpretations {
		key := interpretationKey(resolved.OutputID, resolved.Column, resolved.OccurrenceID)
		if strings.TrimSpace(resolved.OutputID) == "" || strings.TrimSpace(resolved.Column) == "" || strings.TrimSpace(resolved.OccurrenceID) == "" {
			return fmt.Errorf("resolvedInputs.interpretations[%d] requires outputId, column, and occurrenceId", index)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("resolvedInputs.interpretations contains duplicate %q", key)
		}
		seen[key] = struct{}{}
		entry, known := expected[key]
		if !known {
			return fmt.Errorf("resolved interpretation %q has no matching pinned column", key)
		}
		if err := validateResolvedInterpretation(resolved, entry.column); err != nil {
			return fmt.Errorf("resolved interpretation %q: %w", key, err)
		}
		if projectid.Canonical(resolved.Revision.Project) != projectid.Canonical(project) {
			return fmt.Errorf("resolved interpretation %q belongs to a different project", key)
		}
		candidate, err := structuralCandidate(entry.document, entry.column, snapshot)
		if err != nil {
			return fmt.Errorf("resolved interpretation %q structural candidate: %w", key, err)
		}
		if !resolved.Revision.Applicability.Matches(candidate) {
			return fmt.Errorf("resolved interpretation %q is not applicable to the exact capability snapshot", key)
		}
		rule, err := resolved.Revision.SelectRule(candidate)
		if err != nil {
			return fmt.Errorf("resolved interpretation %q rule selection: %w", key, err)
		}
		if rule.ID != resolved.SelectedRuleID || !definitionEqual(rule.Definition, resolved.Definition) {
			return fmt.Errorf("resolved interpretation %q does not contain the exact selected rule", key)
		}
	}
	for key := range expected {
		if _, resolved := seen[key]; !resolved {
			return fmt.Errorf("pinned column %q has no resolved interpretation", key)
		}
	}
	return nil
}

func validateResolvedInterpretation(resolved interpretation.ResolvedInterpretation, column ws.Column) error {
	if resolved.Revision.ID == "" || resolved.SelectedRuleID == "" {
		return fmt.Errorf("revision and selectedRuleId are required")
	}
	if resolved.Revision.Project == "" {
		return fmt.Errorf("revision project is required")
	}
	if err := resolved.Revision.Validate(); err != nil {
		return fmt.Errorf("revision is invalid: %w", err)
	}
	if column.Interpretation == nil || column.Interpretation.Pinned == nil || string(resolved.Revision.ID) != strings.TrimSpace(column.Interpretation.Pinned.RevisionID) {
		return fmt.Errorf("revision does not match pinned authoring reference")
	}
	for _, rule := range resolved.Revision.Rules {
		if rule.ID == resolved.SelectedRuleID {
			if !definitionEqual(rule.Definition, resolved.Definition) {
				return fmt.Errorf("selected definition does not match selected rule")
			}
			return nil
		}
	}
	return fmt.Errorf("selected rule %q is not present in revision", resolved.SelectedRuleID)
}

func interpretationKey(outputID, column, occurrenceID string) string {
	return outputID + "\x00" + column + "\x00" + occurrenceID
}

func cloneResolvedInterpretation(input interpretation.ResolvedInterpretation) interpretation.ResolvedInterpretation {
	out := input
	out.Revision.Applicability.ResourceTypes = append([]string(nil), input.Revision.Applicability.ResourceTypes...)
	out.Revision.Applicability.SourceProfiles = append([]string(nil), input.Revision.Applicability.SourceProfiles...)
	out.Revision.Applicability.SourceCanonical = append([]string(nil), input.Revision.Applicability.SourceCanonical...)
	out.Revision.Applicability.LogicalTypes = append([]string(nil), input.Revision.Applicability.LogicalTypes...)
	out.Revision.Applicability.Cardinalities = append([]string(nil), input.Revision.Applicability.Cardinalities...)
	out.Revision.Applicability.SchemaDigests = append([]string(nil), input.Revision.Applicability.SchemaDigests...)
	out.Revision.Rules = append([]interpretation.InterpretationRule(nil), input.Revision.Rules...)
	for index := range out.Revision.Rules {
		out.Revision.Rules[index].Match.ExtensionURLPath = append([]string(nil), input.Revision.Rules[index].Match.ExtensionURLPath...)
		if input.Revision.Rules[index].Priority != nil {
			priority := *input.Revision.Rules[index].Priority
			out.Revision.Rules[index].Priority = &priority
		}
		out.Revision.Rules[index].Definition = cloneInterpretationDefinition(input.Revision.Rules[index].Definition)
	}
	out.Definition = cloneInterpretationDefinition(input.Definition)
	if input.Revision.ParentRevisionID != nil {
		value := *input.Revision.ParentRevisionID
		out.Revision.ParentRevisionID = &value
	}
	if input.Revision.ParentDigest != nil {
		value := *input.Revision.ParentDigest
		out.Revision.ParentDigest = &value
	}
	return out
}

func cloneInterpretationDefinition(input interpretation.InterpretationFeatureDefinition) interpretation.InterpretationFeatureDefinition {
	out := input
	out.Source = input.Source.Normalized()
	if input.Contributor != nil {
		value := input.Contributor.Normalized()
		out.Contributor = &value
	}
	return out
}

func definitionEqual(left, right interpretation.InterpretationFeatureDefinition) bool {
	leftRaw, leftErr := json.Marshal(cloneInterpretationDefinition(left))
	rightRaw, rightErr := json.Marshal(cloneInterpretationDefinition(right))
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func applyResolvedInterpretations(workspace ws.Workspace, inputs ResolvedInputs) ws.Workspace {
	effective := workspace.NormalizePresentationOrders()
	byKey := make(map[string]interpretation.ResolvedInterpretation, len(inputs.Interpretations))
	for _, input := range inputs.Interpretations {
		byKey[interpretationKey(input.OutputID, input.Column, input.OccurrenceID)] = input
	}
	for documentIndex := range effective.Documents {
		document := &effective.Documents[documentIndex]
		for columnIndex := range document.Columns {
			column := &document.Columns[columnIndex]
			input, ok := byKey[interpretationKey(document.Output.ID, column.Column, column.OccurrenceID)]
			if !ok {
				continue
			}
			column.Source = input.Definition.Source.Normalized()
			if input.Definition.Contributor == nil {
				column.Contributor = nil
			} else {
				value := input.Definition.Contributor.Normalized()
				column.Contributor = &value
			}

			column.Interpretation = nil
		}
	}
	return effective
}

type ResolvedInputs struct {
	Populations     []ResolvedPopulation                    `json:"populations,omitempty"`
	Interpretations []interpretation.ResolvedInterpretation `json:"interpretations,omitempty"`
	ImportedRecipes []ResolvedImportedRecipe                `json:"importedRecipes,omitempty"`
}

func (r ResolvedInputs) ImportedRecipeFor(reference ws.ImportedRecipeRef) (ResolvedImportedRecipe, bool) {
	for _, imported := range r.ImportedRecipes {
		if imported.Reference == reference {
			return imported, true
		}
	}
	return ResolvedImportedRecipe{}, false
}

func (r ResolvedInputs) PopulationFor(outputID string) (ResolvedPopulation, bool) {
	for _, population := range r.Populations {
		if population.OutputID == outputID {
			return population, true
		}
	}
	return ResolvedPopulation{}, false
}

func validateResolvedImportedRecipeCoverage(workspace ws.Workspace, inputs ResolvedInputs) error {
	expected := make(map[ws.ImportedRecipeRef]struct{})
	for _, document := range workspace.Documents {
		if document.ImportedRecipe == nil {
			continue
		}
		ref := *document.ImportedRecipe
		if _, duplicate := expected[ref]; duplicate {
			return fmt.Errorf("duplicate imported recipe reference for output %q", ref.Output)
		}
		expected[ref] = struct{}{}
	}
	seen := make(map[ws.ImportedRecipeRef]struct{}, len(inputs.ImportedRecipes))
	for index, imported := range inputs.ImportedRecipes {
		if _, ok := expected[imported.Reference]; !ok {
			return fmt.Errorf("resolved imported recipe %d has no exact authoring reference", index)
		}
		if _, duplicate := seen[imported.Reference]; duplicate {
			return fmt.Errorf("duplicate resolved imported recipe %q output %q", imported.Reference.Name, imported.Reference.Output)
		}
		seen[imported.Reference] = struct{}{}
		digest, err := imported.Bundle.Digest()
		if err != nil {
			return fmt.Errorf("resolved imported recipe %d source bundle: %w", index, err)
		}
		if imported.Bundle.Name != imported.Reference.Name || imported.Bundle.TranslationVersion != imported.Reference.TranslationVersion || digest != imported.Reference.Digest {
			return fmt.Errorf("resolved imported recipe %d does not match its exact pinned source", index)
		}
		if imported.ResolvedBundle.Name != imported.Bundle.Name || imported.ResolvedBundle.TranslationVersion != imported.Bundle.TranslationVersion {
			return fmt.Errorf("resolved imported recipe %d changed immutable bundle identity", index)
		}
		if _, ok := recipeOutput(imported.Bundle, imported.Reference.Output); !ok {
			return fmt.Errorf("pinned source output %q was not found", imported.Reference.Output)
		}
		if _, ok := recipeOutput(imported.ResolvedBundle, imported.Reference.Output); !ok {
			return fmt.Errorf("resolved source output %q was not found", imported.Reference.Output)
		}
		if err := validateImportedSourceColumns(imported); err != nil {
			return fmt.Errorf("resolved imported recipe %d: %w", index, err)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("not every imported recipe reference was resolved")
	}
	return nil
}

func recipeOutput(bundle recipe.Bundle, name string) (recipe.Output, bool) {
	for _, output := range bundle.Outputs {
		if output.Name == name {
			return output, true
		}
	}
	return recipe.Output{}, false
}

func validateImportedSourceColumns(imported ResolvedImportedRecipe) error {
	seenNames, seenIDs := map[string]bool{}, map[string]bool{}
	for index, source := range imported.SourceColumns {
		column := source.Column
		if strings.TrimSpace(column.ID) == "" || strings.TrimSpace(column.Name) == "" {
			return fmt.Errorf("sourceColumns[%d] requires stable id and name", index)
		}
		if seenNames[column.Name] || seenIDs[column.ID] {
			return fmt.Errorf("sourceColumns[%d] duplicates column identity", index)
		}
		seenNames[column.Name], seenIDs[column.ID] = true, true
	}
	return nil
}

func compileImportedDocument(ctx context.Context, project, explorerID string, document ws.Document, catalog ws.CatalogSnapshot, snapshot capability.Snapshot, imported ResolvedImportedRecipe) (Result, error) {
	if imported.Reference != *document.ImportedRecipe {
		return Result{}, fmt.Errorf("resolved pin does not match imported document")
	}
	sourceOutput, ok := recipeOutput(imported.Bundle, imported.Reference.Output)
	if !ok {
		return Result{}, fmt.Errorf("pinned source output %q was not found", imported.Reference.Output)
	}
	sourceOutput, err := recipeimport.ApplySourcePlan(sourceOutput, document.ImportedSource)
	if err != nil {
		return Result{}, fmt.Errorf("imported source plan: %w", err)
	}
	if document.ArchiveRetiredShape != nil || document.Population != nil || !reflect.DeepEqual(document.Rows, recipeimport.ImportRows(sourceOutput)) || len(document.FixedFilters) != 0 || len(document.Actions) != 0 {
		return Result{}, fmt.Errorf("imported recipe route, rows, and current overlay are not supported by this compile path")
	}
	if sourceOutput.Construction != nil || sourceOutput.ArchiveRetiredReshape != nil || len(sourceOutput.DerivedColumns) != 0 {
		return Result{}, fmt.Errorf("imported recipe already has a post-source construction that conflicts with the Builder overlay")
	}
	expectedRoute, err := recipeimport.ImportRoute(sourceOutput, catalog)
	if err != nil {
		return Result{}, err
	}
	if !reflect.DeepEqual(document.Route, expectedRoute) || document.RootResourceType != sourceOutput.RootResourceType {
		return Result{}, fmt.Errorf("imported recipe route was edited")
	}
	resolvedBase, ok := recipeOutput(imported.ResolvedBundle, imported.Reference.Output)
	if !ok {
		return Result{}, fmt.Errorf("resolved source output %q was not found", imported.Reference.Output)
	}
	resolvedRoute, err := recipeimport.ImportRoute(resolvedBase, catalog)
	if err != nil {
		return Result{}, err
	}
	if !reflect.DeepEqual(document.Route, resolvedRoute) || resolvedBase.RootResourceType != sourceOutput.RootResourceType ||
		resolvedBase.RowGrain != sourceOutput.RowGrain || !reflect.DeepEqual(resolvedBase.Expand, sourceOutput.Expand) ||
		!reflect.DeepEqual(resolvedBase.Identity, sourceOutput.Identity) {
		return Result{}, fmt.Errorf("resolved imported output does not match the pinned source")
	}
	resolvedOutput, err := cloneRecipeOutput(resolvedBase)
	if err != nil {
		return Result{}, fmt.Errorf("clone resolved imported output: %w", err)
	}
	additive := make([]ws.Column, 0)
	for _, authored := range document.Columns {
		if authored.Source.Kind == ws.SourceImported && authored.Source.Imported != nil {
			continue
		}
		additive = append(additive, authored)
	}
	var additiveResult Result
	if len(additive) > 0 {
		synthetic := ws.Document{
			Kind:             ws.Kind,
			Output:           ws.Output{ID: document.Output.ID, Title: document.Output.Title},
			RootResourceType: document.RootResourceType,
			Route:            document.Route,
			Rows:             document.Rows,
			Columns:          additive,
		}
		compileSnapshot, compileRoute := importedCompileInputs(snapshot, document.Route)
		synthetic.Route = compileRoute
		var err error
		additiveResult, err = compileSemanticDocument(ctx, project, explorerID, synthetic, compileSnapshot, nil)
		if err != nil {
			return Result{}, fmt.Errorf("compile imported additive columns: %w", err)
		}
	}
	publicColumns := make([]receiptmodel.PublicOutputColumn, 0, len(document.Columns))
	emittedColumns := make([]viewer.EmittedColumn, 0, len(document.Columns))
	presentationColumns := make([]PresentationColumn, 0, len(document.Columns))
	seenImported := 0
	for index, authored := range document.Columns {
		if authored.Source.Kind != ws.SourceImported || authored.Source.Imported == nil {
			continue
		}
		if authored.ValueTransformation != nil || authored.Contributor != nil || authored.Interpretation != nil || authored.Filter != nil || authored.Chart != nil {
			return Result{}, fmt.Errorf("imported recipe column %d contains an unsupported semantic overlay", index)
		}
		seenImported++
		var source *ResolvedImportedSourceColumn
		for sourceIndex := range imported.SourceColumns {
			if imported.SourceColumns[sourceIndex].Column.Name == authored.Source.Imported.OutputColumn {
				source = &imported.SourceColumns[sourceIndex]
				break
			}
		}
		if source == nil || authored.Source.Imported.OutputColumn != authored.Column || authored.ColumnID != source.Column.ID {
			return Result{}, fmt.Errorf("imported recipe column %d source identity was edited", index)
		}
		column := source.Column
		emission := viewer.EmittedColumn{EmissionID: column.ID, OutputID: document.Output.ID, OccurrenceID: authored.OccurrenceID, AuthoredColumns: []string{authored.Column}, PublicColumn: column.Name, Label: authored.Label, LogicalType: column.Type, Cardinality: source.Cardinality, Nullable: column.Nullable, Shape: source.Shape, SourcePath: source.SemanticPath, StructuralSuitability: "requires-review", LossReasons: []string{"IMPORTED_RECIPE_QUALITY_UNASSESSED"}}
		emittedColumns = append(emittedColumns, emission)
		publicColumn := publicColumnContract(emission)
		publicColumn.Cardinality = emission.Cardinality
		publicColumns = append(publicColumns, publicColumn)
	}
	if seenImported != len(imported.SourceColumns) {
		return Result{}, fmt.Errorf("imported recipe source columns were added or removed")
	}
	if len(additive) > 0 {
		additiveOutput := additiveResult.Bundle.Outputs[0]
		rewriteAdditiveAliases(&additiveOutput, importedTraversalAliases(resolvedOutput))
		mergeAdditiveOutput(&resolvedOutput, additiveOutput)
		for _, emission := range additiveResult.EmittedColumns {
			emittedColumns = append(emittedColumns, emission)
			publicColumn := publicColumnContract(emission)
			publicColumn.Cardinality = emission.Cardinality
			publicColumns = append(publicColumns, publicColumn)
		}
	}
	presentationOrder := 0
	for index, authored := range document.Columns {
		for _, emission := range emittedColumns {
			owned := false
			for _, name := range emission.AuthoredColumns {
				if name == authored.Column {
					owned = true
					break
				}
			}
			if !owned {
				continue
			}
			presentationColumns = append(presentationColumns, presentationColumn(authored, index, presentationOrder, emission))
			presentationOrder++
		}
	}
	resolvedOutput.Name = document.Output.ID
	constructionIntent := document.Construction
	if constructionIntent == nil {
		constructionIntent = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}}
	}
	construction, err := recipeConstruction(constructionIntent, document.Columns, emittedColumns)
	if err != nil {
		return Result{}, fmt.Errorf("translate imported output overlay: %w", err)
	}
	resolvedOutput.Construction = construction
	contract := receiptmodel.PublicOutputContract{OutputID: document.Output.ID, RootResourceType: sourceOutput.RootResourceType, RowGrain: resolvedOutput.RowGrain, RowMultiplication: "none", Lossless: true, MLReady: true, Columns: publicColumns}
	if resolvedOutput.Expand != nil {
		contract.RowMultiplication = "expand"
	}
	if len(emittedColumns) > 0 {
		contract.StructuralSuitability = emittedColumns[0].StructuralSuitability
	}
	for _, emission := range emittedColumns {
		mergeContractQuality(&contract, emission)
	}
	result := Result{Bundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: imported.ResolvedBundle.Name, TranslationVersion: imported.ResolvedBundle.TranslationVersion, Fragments: imported.ResolvedBundle.Fragments, Outputs: []recipe.Output{resolvedOutput}}, EmittedColumns: emittedColumns, IdentityMappings: additiveResult.IdentityMappings, Presentation: PresentationConfig{OutputID: document.Output.ID, Title: document.Output.Title, Columns: presentationColumns}, OutputContract: contract}
	return result, nil
}

func recipePopulation(input ResolvedPopulation) *recipe.PopulationConstraint {
	route := make([]recipe.PopulationRouteStep, len(input.Route))
	for index, step := range input.Route {
		route[index] = recipe.PopulationRouteStep{
			ResourceType: step.ResourceType, Relationship: step.Relationship,
			StorageDirection: step.StorageDirection,
		}
	}
	return &recipe.PopulationConstraint{SelectionRevisionID: input.SelectionRevisionID, MembershipDigest: input.MembershipDigest, MemberCount: input.MemberCount, ResourceType: input.ResourceType, Route: route}
}

func ResolvedInputsDigest(workspace ws.Workspace, snapshot capability.Snapshot, resolvedInputs ResolvedInputs) (string, error) {
	resolvedInputs = resolvedInputs.Canonical()
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return "", err
	}
	identity := struct {
		Workspace                json.RawMessage `json:"workspace"`
		ResolvedInputs           ResolvedInputs  `json:"resolvedInputs"`
		SourceGeneration         string          `json:"sourceGeneration"`
		CapabilitySchemaDigest   string          `json:"capabilitySchemaDigest"`
		CapabilityShapeDigest    string          `json:"capabilityShapeDigest"`
		AuthorizationScopeDigest string          `json:"authorizationScopeDigest"`
	}{
		Workspace: normalized, ResolvedInputs: resolvedInputs, SourceGeneration: snapshot.Identity.Generation,
		CapabilitySchemaDigest: snapshot.Identity.SchemaDigest, CapabilityShapeDigest: snapshot.Identity.ShapeDigest,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (r ResolvedInputs) Canonical() ResolvedInputs {
	copy := ResolvedInputs{Populations: append([]ResolvedPopulation(nil), r.Populations...), Interpretations: append([]interpretation.ResolvedInterpretation(nil), r.Interpretations...), ImportedRecipes: append([]ResolvedImportedRecipe(nil), r.ImportedRecipes...)}
	for index := range copy.Populations {
		copy.Populations[index].Route = append([]ws.PopulationRouteStep(nil), copy.Populations[index].Route...)
	}
	sort.SliceStable(copy.Populations, func(i, j int) bool {
		if copy.Populations[i].OutputID != copy.Populations[j].OutputID {
			return copy.Populations[i].OutputID < copy.Populations[j].OutputID
		}
		return copy.Populations[i].SelectionRevisionID < copy.Populations[j].SelectionRevisionID
	})
	for index := range copy.Interpretations {
		copy.Interpretations[index] = cloneResolvedInterpretation(copy.Interpretations[index])
	}
	sort.SliceStable(copy.Interpretations, func(i, j int) bool {
		left, right := copy.Interpretations[i], copy.Interpretations[j]
		if left.OutputID != right.OutputID {
			return left.OutputID < right.OutputID
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.OccurrenceID < right.OccurrenceID
	})
	sort.SliceStable(copy.ImportedRecipes, func(i, j int) bool {
		left, right := copy.ImportedRecipes[i].Reference, copy.ImportedRecipes[j].Reference
		if left.Output != right.Output {
			return left.Output < right.Output
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.TranslationVersion < right.TranslationVersion
	})
	return copy
}

func Compile(ctx context.Context, project, explorerID string, document ws.Document, snapshot capability.Snapshot) (Result, error) {
	return compileWithWorkspaceOutputs(ctx, project, explorerID, document, snapshot, nil)
}

func compileWithWorkspaceOutputs(ctx context.Context, project, explorerID string, document ws.Document, snapshot capability.Snapshot, workspaceOutputIDs []string) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	project = projectid.Canonical(project)
	explorerID = strings.TrimSpace(explorerID)
	if project == "" {
		return Result{}, fail("protocol", "PROJECT_REQUIRED", "$.project", "project is required", nil, nil)
	}
	if explorerID == "" {
		return Result{}, fail("protocol", "EXPLORER_REQUIRED", "$.explorerId", "explorerId is required", nil, nil)
	}
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return Result{}, fail("capability", "CAPABILITY_SNAPSHOT_UNUSABLE", "$.snapshot", "capability snapshot is unavailable or stale", map[string]any{"token": snapshot.Token}, err)
	}
	if projectid.Canonical(snapshot.Identity.Project) != project {
		return Result{}, fail("capability", "PROJECT_MISMATCH", "$.project", "capability snapshot belongs to a different project", map[string]any{"snapshotProject": snapshot.Identity.Project, "project": project}, nil)
	}
	if err := document.Validate(); err != nil {
		return Result{}, fail("intent", "INVALID_DOCUMENT", "$", err.Error(), nil, err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	return compileSemanticDocument(ctx, project, explorerID, document, snapshot, workspaceOutputIDs)
}

func validateSnapshot(snapshot capability.Snapshot) error {
	nodes := map[string]capability.Node{}
	for i, node := range snapshot.Nodes {
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.ResourceType) == "" {
			return fail("capability", "INVALID_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "node id and resourceType are required", nil, nil)
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			return fail("capability", "DUPLICATE_NODE", fmt.Sprintf("$.snapshot.nodes[%d]", i), "duplicate node id", map[string]any{"nodeId": node.ID}, nil)
		}
		nodes[node.ID] = node
	}
	edges := map[string]bool{}
	for i, edge := range snapshot.Edges {
		if edge.ID == "" || nodes[edge.FromNodeID].ID == "" || nodes[edge.ToNodeID].ID == "" || strings.TrimSpace(edge.Label) == "" {
			return fail("capability", "INVALID_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "edge has stale endpoints or empty label", nil, nil)
		}
		if edges[edge.ID] {
			return fail("capability", "DUPLICATE_EDGE", fmt.Sprintf("$.snapshot.edges[%d]", i), "duplicate edge id", map[string]any{"edgeId": edge.ID}, nil)
		}
		edges[edge.ID] = true
	}
	candidates := map[string]bool{}
	for i, candidate := range snapshot.Candidates {
		if candidate.ID == "" || nodes[candidate.NodeID].ID == "" || strings.TrimSpace(candidate.FieldPath) == "" {
			return fail("capability", "INVALID_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "candidate has stale node or empty field path", nil, nil)
		}
		if candidates[candidate.ID] {
			return fail("capability", "DUPLICATE_CANDIDATE", fmt.Sprintf("$.snapshot.candidates[%d]", i), "duplicate candidate id", map[string]any{"candidateId": candidate.ID}, nil)
		}
		candidates[candidate.ID] = true
	}
	return nil
}

func supportsOperation(items []capability.Operation, want capability.Operation) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func projectionValueMode(mode string) recipe.ValueMode {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case string(capability.ProjectionFirst):
		return recipe.ValueModeFirst
	case "ALL", string(capability.ProjectionArray):
		return recipe.ValueModeAll
	case string(capability.ProjectionDistinctArray):
		return recipe.ValueModeDistinct
	default:
		return recipe.ValueModeAuto
	}
}

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func recipeName(label, id string) string {
	if namePattern.MatchString(label) {
		return label
	}
	return "edge_" + shortHash(id)
}

func safeName(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	value = strings.Trim(builder.String(), "_")
	if value == "" {
		return "x"
	}
	if value[0] >= '0' && value[0] <= '9' {
		return "x_" + value
	}
	return value
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fail("compile", "CONTEXT_CANCELED", "$", ctx.Err().Error(), nil, ctx.Err())
	default:
		return nil
	}
}
