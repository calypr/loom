package importedsource

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
)

func isExtensionOutputKey(key string) bool {
	if !strings.HasPrefix(key, "ext-v1:") {
		return false
	}
	digest := strings.TrimPrefix(key, "ext-v1:")
	if len(digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func disableExtensionOutput(parameters *ws.ImportedExtensionParameters, key string) (bool, error) {
	if parameters == nil || !isExtensionOutputKey(key) {
		return false, fmt.Errorf("extension output key is invalid")
	}
	match := -1
	for index, output := range parameters.Outputs {
		if output.Key != key {
			continue
		}
		if match >= 0 {
			return false, fmt.Errorf("extension output key is ambiguous")
		}
		match = index
	}
	if match >= 0 {
		if !parameters.Outputs[match].Enabled {
			return false, fmt.Errorf("extension output is already disabled")
		}
		parameters.Outputs[match].Enabled = false
	} else {
		parameters.Outputs = append(parameters.Outputs, ws.ImportedExtensionOutput{Key: key, Enabled: false})
	}
	return true, nil
}

func disableCatalogOutput(parameters *ws.ImportedCatalogParameters, fieldPath string) (bool, error) {
	match := -1
	for index, output := range parameters.Outputs {
		if output.FieldPath != fieldPath {
			continue
		}
		if match >= 0 {
			return false, fmt.Errorf("catalog output key is ambiguous")
		}
		match = index
	}
	if match >= 0 {
		if !parameters.Outputs[match].Enabled {
			return false, fmt.Errorf("catalog output is already disabled")
		}
		parameters.Outputs[match].Enabled = false
	} else {
		parameters.Outputs = append(parameters.Outputs, ws.ImportedCatalogOutput{FieldPath: fieldPath, Enabled: false})
	}
	return true, nil
}

func disablePivotOutput(parameters *ws.ImportedPivotParameters, key string) (bool, error) {
	match := -1
	for index, output := range parameters.Outputs {
		if output.Key != key {
			continue
		}
		if match >= 0 {
			return false, fmt.Errorf("pivot output key is ambiguous")
		}
		match = index
	}
	if match >= 0 {
		if !parameters.Outputs[match].Enabled {
			return false, fmt.Errorf("pivot output is already disabled")
		}
		parameters.Outputs[match].Enabled = false
	} else {
		parameters.Outputs = append(parameters.Outputs, ws.ImportedPivotOutput{Key: key, Enabled: false})
	}
	return true, nil
}

func bindPinnedFieldRemoval(
	document ws.Document,
	original recipe.Output,
	current importedRecipeResolution,
	plan ws.ImportedSourcePlan,
	network *authoringnetwork.AuthoringNetwork,
) error {
	if network == nil {
		return fmt.Errorf("authoring network is required")
	}
	var schema []facade.CompiledOutputColumn
	if requiresPristineProjectionIdentity(original, document.ImportedSource) {
		schema = current.pinnedSchemas[original.Name]
	} else {
		for _, output := range current.resolved.Compiled.Outputs {
			if output.Name == original.Name {
				schema = output.OutputSchema
				break
			}
		}
	}
	if len(schema) == 0 {
		return nil
	}
	fieldColumnIDs, err := pinnedFieldBindingIDs(original, schema)
	fieldSlotsExact := err == nil && recipeimport.VerifyPinnedFieldBindings(plan.FieldBindings, fieldColumnIDs) == nil
	steps := map[string]ws.ImportedSourceStep{}
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	bindings := map[string]authoringnetwork.NetworkColumnBinding{}
	bindingCount := map[string]int{}
	for _, binding := range network.Bindings {
		bindingCount[binding.ColumnID]++
		if bindingCount[binding.ColumnID] == 1 {
			bindings[binding.ColumnID] = binding
		}
	}
	for index := range network.ProjectedFields {
		field := &network.ProjectedFields[index]
		if field.Owner != authoringnetwork.ProjectedFieldOwnerPinned || field.ColumnID == "" {
			continue
		}
		binding, ok := bindings[field.ColumnID]
		if !ok || bindingCount[field.ColumnID] != 1 || binding.Status != "EXACT" || binding.SourceStepID == "" {
			continue
		}
		field.SourceStepID = binding.SourceStepID
		if binding.OccurrenceID != "" {
			field.OccurrenceID = binding.OccurrenceID
		}
		step, ok := steps[binding.SourceStepID]
		if !ok || !step.Editable || step.Parameters == nil {
			continue
		}
		if binding.OutputKey != "" && supportsPinnedOutputRemoval(step, binding.OutputKey) {
			field.SourceOutputKey = binding.OutputKey
			field.Removal = authoringnetwork.ProjectedFieldRemovalDisablePinnedOutput
			field.Status = authoringnetwork.ProjectedFieldStatusExact
			continue
		}
		if step.Kind != recipeimport.SourcePlanFieldGroup || step.Parameters.FieldGroup == nil || !fieldSlotsExact {
			continue
		}
		matched := ""
		ambiguous := false
		for _, sourceField := range step.Parameters.FieldGroup.Fields {
			if !sourceField.Enabled || fieldColumnIDs[sourceField.FieldID] != field.ColumnID {
				continue
			}
			if matched != "" {
				ambiguous = true
				break
			}
			matched = sourceField.FieldID
		}
		if !ambiguous && matched != "" {
			field.SourceFieldID = matched
			field.Removal = authoringnetwork.ProjectedFieldRemovalDisablePinned
			field.Status = authoringnetwork.ProjectedFieldStatusExact
		}
	}
	return nil
}

func supportsPinnedOutputRemoval(step ws.ImportedSourceStep, outputKey string) bool {
	if outputKey == "" || step.Parameters == nil {
		return false
	}
	switch step.Kind {
	case recipeimport.SourcePlanCatalogProjection:
		return step.Parameters.Catalog != nil
	case recipeimport.SourcePlanPivot:
		return step.Parameters.Pivot != nil
	case recipeimport.SourcePlanDynamicColumn:
		return step.Parameters.Dynamic != nil
	case recipeimport.SourcePlanExtensionColumn:
		return step.Parameters.Extension != nil && isExtensionOutputKey(outputKey)
	default:
		return false
	}
}

func (r Sources) Network(ctx context.Context, project string, authorized ports.AuthorizedCapability, document ws.Document) (authoringnetwork.AuthoringNetwork, error) {
	catalog := ws.CatalogFromCapability(authorized.Snapshot, "")
	if document.ImportedRecipe == nil {
		return authoringnetwork.NetworkForDocument(document, catalog)
	}
	ref := document.ImportedRecipe
	source, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, document)
	if err != nil {
		return authoringnetwork.AuthoringNetwork{}, err
	}
	for _, output := range source.bundle.Outputs {
		if output.Name == ref.Output {
			network, err := recipeimport.HydrateNetwork(document, output, catalog)
			if err != nil {
				return authoringnetwork.AuthoringNetwork{}, err
			}
			for _, compiled := range source.resolved.Compiled.Outputs {
				if compiled.Name == ref.Output {
					plan, err := recipeimport.HydrateSourcePlan(output, document.ImportedSource)
					if err != nil {
						return authoringnetwork.AuthoringNetwork{}, err
					}
					network, err = recipeimport.BindCompiledNetwork(network, compiled.OutputSchema, &plan)
					if err != nil {
						return authoringnetwork.AuthoringNetwork{}, err
					}
					network.ProjectedFields = authoringnetwork.ProjectedFieldsForDocument(document, network)
					if err := bindPinnedFieldRemoval(document, output, source, plan, &network); err != nil {
						return authoringnetwork.AuthoringNetwork{}, err
					}
					return network, nil
				}
			}
			return authoringnetwork.AuthoringNetwork{}, fmt.Errorf("compiled source output was not found")
		}
	}
	return authoringnetwork.AuthoringNetwork{}, fmt.Errorf("pinned source output was not found")
}

func (r Sources) RemoveOutput(
	ctx context.Context,
	project string,
	authorized ports.AuthorizedCapability,
	document ws.Document,
	sourceStepID string,
	outputKey string,
) (ws.Document, error) {
	if document.ImportedRecipe == nil || sourceStepID == "" || strings.TrimSpace(outputKey) == "" {
		return ws.Document{}, fmt.Errorf("an exact pinned output identity is required")
	}
	ref := document.ImportedRecipe
	resolved, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, document)
	if err != nil {
		return ws.Document{}, err
	}
	var original *recipe.Output
	for index := range resolved.bundle.Outputs {
		if resolved.bundle.Outputs[index].Name == ref.Output {
			original = &resolved.bundle.Outputs[index]
			break
		}
	}
	if original == nil {
		return ws.Document{}, fmt.Errorf("pinned source output was not found")
	}
	plan, err := recipeimport.HydrateSourcePlan(*original, document.ImportedSource)
	if err != nil {
		return ws.Document{}, err
	}
	var compiledSchema []facade.CompiledOutputColumn
	for _, compiled := range resolved.resolved.Compiled.Outputs {
		if compiled.Name == ref.Output {
			compiledSchema = compiled.OutputSchema
			break
		}
	}
	if len(compiledSchema) == 0 {
		return ws.Document{}, fmt.Errorf("pinned output schema is unavailable")
	}
	network, err := recipeimport.HydrateNetwork(document, *original, ws.CatalogFromCapability(authorized.Snapshot, ""))
	if err != nil {
		return ws.Document{}, err
	}
	network, err = recipeimport.BindCompiledNetwork(network, compiledSchema, &plan)
	if err != nil {
		return ws.Document{}, err
	}
	owners := 0
	for _, binding := range network.Bindings {
		if binding.Status == "EXACT" && binding.SourceStepID == sourceStepID && binding.OutputKey == outputKey {
			owners++
		}
	}
	if owners != 1 {
		return ws.Document{}, fmt.Errorf("pinned output identity is unavailable or ambiguous")
	}
	for _, step := range plan.Steps {
		if step.ID != sourceStepID {
			continue
		}
		if !step.Editable || step.Parameters == nil {
			return ws.Document{}, fmt.Errorf("pinned output source is not editable")
		}
		parameters := *step.Parameters
		switch step.Kind {
		case recipeimport.SourcePlanCatalogProjection:
			if parameters.Catalog == nil {
				return ws.Document{}, fmt.Errorf("pinned catalog output parameters are unavailable")
			}
			catalog := *parameters.Catalog
			catalog.Outputs = append([]ws.ImportedCatalogOutput(nil), catalog.Outputs...)
			found, err := disableCatalogOutput(&catalog, outputKey)
			if err != nil || !found {
				if err != nil {
					return ws.Document{}, err
				}
				return ws.Document{}, fmt.Errorf("catalog output key was not found")
			}
			parameters.Catalog = &catalog
		case recipeimport.SourcePlanPivot:
			if parameters.Pivot == nil {
				return ws.Document{}, fmt.Errorf("pinned pivot output parameters are unavailable")
			}
			pivot := *parameters.Pivot
			pivot.Outputs = append([]ws.ImportedPivotOutput(nil), pivot.Outputs...)
			found, err := disablePivotOutput(&pivot, outputKey)
			if err != nil || !found {
				if err != nil {
					return ws.Document{}, err
				}
				return ws.Document{}, fmt.Errorf("pivot output key was not found")
			}
			parameters.Pivot = &pivot
		case recipeimport.SourcePlanDynamicColumn:
			if parameters.Dynamic == nil {
				return ws.Document{}, fmt.Errorf("pinned dynamic output parameters are unavailable")
			}
			dynamic := *parameters.Dynamic
			dynamic.Outputs = append([]ws.ImportedDynamicOutput(nil), dynamic.Outputs...)
			found := -1
			for index, output := range dynamic.Outputs {
				if output.Key != outputKey {
					continue
				}
				if found >= 0 {
					return ws.Document{}, fmt.Errorf("dynamic output key is ambiguous")
				}
				found = index
			}
			if found >= 0 {
				if !dynamic.Outputs[found].Enabled {
					return ws.Document{}, fmt.Errorf("dynamic output is already disabled")
				}
				dynamic.Outputs[found].Enabled = false
			} else {
				dynamic.Outputs = append(dynamic.Outputs, ws.ImportedDynamicOutput{Key: outputKey, Enabled: false})
			}
			parameters.Dynamic = &dynamic
		case recipeimport.SourcePlanExtensionColumn:
			if parameters.Extension == nil || !isExtensionOutputKey(outputKey) {
				return ws.Document{}, fmt.Errorf("pinned extension output identity is unavailable")
			}
			extension := *parameters.Extension
			extension.Outputs = append([]ws.ImportedExtensionOutput(nil), extension.Outputs...)
			if _, err := disableExtensionOutput(&extension, outputKey); err != nil {
				return ws.Document{}, err
			}
			parameters.Extension = &extension
		default:
			return ws.Document{}, fmt.Errorf("pinned source kind %q does not support per-output removal", step.Kind)
		}
		change := authoringv2.ImportedSourceStepChange{
			StepID: step.ID, MaxColumns: step.MaxColumns,
			ValueMode: step.ValueMode, Parameters: &parameters,
		}
		return r.Edit(ctx, project, authorized, document, &change)
	}
	return ws.Document{}, fmt.Errorf("pinned output source step was not found")
}

func (r Sources) RemoveField(
	ctx context.Context,
	project string,
	authorized ports.AuthorizedCapability,
	document ws.Document,
	sourceStepID string,
	sourceFieldID string,
) (ws.Document, error) {
	if document.ImportedRecipe == nil {
		return ws.Document{}, fmt.Errorf("an imported source is required")
	}
	ref := *document.ImportedRecipe
	if err := ref.Validate(); err != nil {
		return ws.Document{}, err
	}
	if r.registry == nil || r.engine == nil || ref.Name != r.defaultName {
		return ws.Document{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported source recipes are not configured"}
	}
	if projectid.Canonical(project) != projectid.Canonical(authorized.Snapshot.Identity.Project) {
		return ws.Document{}, capability.ErrStaleSnapshot
	}
	if err := capabilityresolver.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return ws.Document{}, err
	}
	entry, err := r.registry.LoadRecipeVersion(ctx, ref.Name, ref.TranslationVersion)
	if err != nil {
		return ws.Document{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the pinned dataframe recipe could not be loaded", Cause: err}
	}
	bundle, err := entry.Bundle.CanonicalBundle()
	if err != nil {
		return ws.Document{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return ws.Document{}, err
	}
	if digest != ref.Digest || entry.Digest != digest {
		return ws.Document{}, fmt.Errorf("the imported dataframe recipe changed; reload its exact source version")
	}
	var original *recipe.Output
	for index := range bundle.Outputs {
		if bundle.Outputs[index].Name == ref.Output {
			original = &bundle.Outputs[index]
			break
		}
	}
	if original == nil {
		return ws.Document{}, fmt.Errorf("pinned source output was not found")
	}
	plan, err := recipeimport.HydrateSourcePlan(*original, document.ImportedSource)
	if err != nil {
		return ws.Document{}, err
	}
	var updated authoringv2.ImportedSourceStepChange
	stepFound := false
	for _, step := range plan.Steps {
		if step.ID != sourceStepID {
			continue
		}
		if !step.Editable || step.Kind != recipeimport.SourcePlanFieldGroup || step.Parameters == nil || step.Parameters.FieldGroup == nil {
			return ws.Document{}, fmt.Errorf("pinned field source is not editable")
		}
		parameters := *step.Parameters
		fieldGroup := *step.Parameters.FieldGroup
		fieldGroup.Fields = append([]ws.ImportedFieldParameters(nil), step.Parameters.FieldGroup.Fields...)
		fieldFound := false
		for index := range fieldGroup.Fields {
			field := &fieldGroup.Fields[index]
			if field.FieldID != sourceFieldID {
				continue
			}
			if fieldFound {
				return ws.Document{}, fmt.Errorf("pinned source field identity is ambiguous")
			}
			fieldFound = true
			if !field.Enabled {
				return ws.Document{}, fmt.Errorf("pinned source field is already disabled")
			}
			field.Enabled = false
		}
		if !fieldFound {
			return ws.Document{}, fmt.Errorf("pinned source field was not found")
		}
		parameters.FieldGroup = &fieldGroup
		updated = authoringv2.ImportedSourceStepChange{StepID: step.ID, Parameters: &parameters}
		stepFound = true
		break
	}
	if !stepFound {
		return ws.Document{}, fmt.Errorf("pinned field source step was not found")
	}
	return r.Edit(ctx, project, authorized, document, &updated)
}
