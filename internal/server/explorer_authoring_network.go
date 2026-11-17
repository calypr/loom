package server

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
	"strings"
)

func (r importedRecipeSources) Network(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
	catalog := authoringV2Catalog(authorized.Snapshot, "")
	if document.ImportedRecipe == nil {
		return authoringv2.NetworkForDocument(document, catalog)
	}
	ref := document.ImportedRecipe
	source, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, document)
	if err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	for _, output := range source.bundle.Outputs {
		if output.Name == ref.Output {
			network, err := recipeimport.HydrateNetwork(document, output, catalog)
			if err != nil {
				return authoringv2.AuthoringNetwork{}, err
			}
			for _, compiled := range source.resolved.Compiled.Outputs {
				if compiled.Name == ref.Output {
					plan, err := recipeimport.HydrateSourcePlan(output, document.ImportedSource)
					if err != nil {
						return authoringv2.AuthoringNetwork{}, err
					}
					network, err = recipeimport.BindCompiledNetwork(network, compiled.OutputSchema, &plan)
					if err != nil {
						return authoringv2.AuthoringNetwork{}, err
					}
					network.ProjectedFields = authoringv2.ProjectedFieldsForDocument(document, network)
					if err := bindPinnedFieldRemoval(document, output, source, plan, &network); err != nil {
						return authoringv2.AuthoringNetwork{}, err
					}
					return network, nil
				}
			}
			return authoringv2.AuthoringNetwork{}, fmt.Errorf("compiled source output was not found")
		}
	}
	return authoringv2.AuthoringNetwork{}, fmt.Errorf("pinned source output was not found")
}

// RemoveOutput disables one compiler-proven pinned projection by its exact
// native output key. The key is derived from the saved columnId's current
// network binding; callers never supply it.
func (r importedRecipeSources) RemoveOutput(
	ctx context.Context,
	project string,
	authorized lifecycle.AuthorizedCapability,
	document authoringv2.Document,
	sourceStepID string,
	outputKey string,
) (authoringv2.Document, error) {
	if document.ImportedRecipe == nil || sourceStepID == "" || strings.TrimSpace(outputKey) == "" {
		return authoringv2.Document{}, fmt.Errorf("an exact pinned output identity is required")
	}
	ref := document.ImportedRecipe
	resolved, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, document)
	if err != nil {
		return authoringv2.Document{}, err
	}
	var original *recipe.Output
	for index := range resolved.bundle.Outputs {
		if resolved.bundle.Outputs[index].Name == ref.Output {
			original = &resolved.bundle.Outputs[index]
			break
		}
	}
	if original == nil {
		return authoringv2.Document{}, fmt.Errorf("pinned source output was not found")
	}
	plan, err := recipeimport.HydrateSourcePlan(*original, document.ImportedSource)
	if err != nil {
		return authoringv2.Document{}, err
	}
	var compiledSchema []lower.CompiledOutputColumn
	for _, compiled := range resolved.resolved.Compiled.Outputs {
		if compiled.Name == ref.Output {
			compiledSchema = compiled.OutputSchema
			break
		}
	}
	if len(compiledSchema) == 0 {
		return authoringv2.Document{}, fmt.Errorf("pinned output schema is unavailable")
	}
	network, err := recipeimport.HydrateNetwork(document, *original, authoringV2Catalog(authorized.Snapshot, ""))
	if err != nil {
		return authoringv2.Document{}, err
	}
	network, err = recipeimport.BindCompiledNetwork(network, compiledSchema, &plan)
	if err != nil {
		return authoringv2.Document{}, err
	}
	owners := 0
	for _, binding := range network.Bindings {
		if binding.Status == "EXACT" && binding.SourceStepID == sourceStepID && binding.OutputKey == outputKey {
			owners++
		}
	}
	if owners != 1 {
		return authoringv2.Document{}, fmt.Errorf("pinned output identity is unavailable or ambiguous")
	}
	for _, step := range plan.Steps {
		if step.ID != sourceStepID {
			continue
		}
		if !step.Enabled || !step.Editable || step.Parameters == nil {
			return authoringv2.Document{}, fmt.Errorf("pinned output source is not editable")
		}
		parameters := *step.Parameters
		switch step.Kind {
		case recipeimport.SourcePlanCatalogProjection:
			if parameters.Catalog == nil {
				return authoringv2.Document{}, fmt.Errorf("pinned catalog output parameters are unavailable")
			}
			catalog := *parameters.Catalog
			catalog.Outputs = append([]authoringv2.ImportedCatalogOutput(nil), catalog.Outputs...)
			found, err := disableCatalogOutput(&catalog, outputKey)
			if err != nil || !found {
				if err != nil {
					return authoringv2.Document{}, err
				}
				return authoringv2.Document{}, fmt.Errorf("catalog output key was not found")
			}
			parameters.Catalog = &catalog
		case recipeimport.SourcePlanPivot:
			if parameters.Pivot == nil {
				return authoringv2.Document{}, fmt.Errorf("pinned pivot output parameters are unavailable")
			}
			pivot := *parameters.Pivot
			pivot.Outputs = append([]authoringv2.ImportedPivotOutput(nil), pivot.Outputs...)
			found, err := disablePivotOutput(&pivot, outputKey)
			if err != nil || !found {
				if err != nil {
					return authoringv2.Document{}, err
				}
				return authoringv2.Document{}, fmt.Errorf("pivot output key was not found")
			}
			parameters.Pivot = &pivot
		case recipeimport.SourcePlanDynamicColumn:
			if parameters.Dynamic == nil {
				return authoringv2.Document{}, fmt.Errorf("pinned dynamic output parameters are unavailable")
			}
			dynamic := *parameters.Dynamic
			dynamic.Outputs = append([]authoringv2.ImportedDynamicOutput(nil), dynamic.Outputs...)
			found := -1
			for index, output := range dynamic.Outputs {
				if output.Key != outputKey {
					continue
				}
				if found >= 0 {
					return authoringv2.Document{}, fmt.Errorf("dynamic output key is ambiguous")
				}
				found = index
			}
			if found >= 0 {
				if !dynamic.Outputs[found].Enabled {
					return authoringv2.Document{}, fmt.Errorf("dynamic output is already disabled")
				}
				dynamic.Outputs[found].Enabled = false
			} else {
				dynamic.Outputs = append(dynamic.Outputs, authoringv2.ImportedDynamicOutput{Key: outputKey, Enabled: false})
			}
			parameters.Dynamic = &dynamic
		case recipeimport.SourcePlanExtensionColumn:
			if parameters.Extension == nil || !isExtensionOutputKey(outputKey) {
				return authoringv2.Document{}, fmt.Errorf("pinned extension output identity is unavailable")
			}
			extension := *parameters.Extension
			extension.Outputs = append([]authoringv2.ImportedExtensionOutput(nil), extension.Outputs...)
			if _, err := disableExtensionOutput(&extension, outputKey); err != nil {
				return authoringv2.Document{}, err
			}
			parameters.Extension = &extension
		default:
			return authoringv2.Document{}, fmt.Errorf("pinned source kind %q does not support per-output removal", step.Kind)
		}
		change := authoringv2.ImportedSourceStepChange{
			StepID: step.ID, Enabled: step.Enabled, MaxColumns: step.MaxColumns,
			ValueMode: step.ValueMode, Parameters: &parameters,
		}
		return r.Edit(ctx, project, authorized, document, &change)
	}
	return authoringv2.Document{}, fmt.Errorf("pinned output source step was not found")
}

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

func disableExtensionOutput(parameters *authoringv2.ImportedExtensionParameters, key string) (bool, error) {
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
		parameters.Outputs = append(parameters.Outputs, authoringv2.ImportedExtensionOutput{Key: key, Enabled: false})
	}
	return true, nil
}

func disableCatalogOutput(parameters *authoringv2.ImportedCatalogParameters, fieldPath string) (bool, error) {
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
		parameters.Outputs = append(parameters.Outputs, authoringv2.ImportedCatalogOutput{FieldPath: fieldPath, Enabled: false})
	}
	return true, nil
}

func disablePivotOutput(parameters *authoringv2.ImportedPivotParameters, key string) (bool, error) {
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
		parameters.Outputs = append(parameters.Outputs, authoringv2.ImportedPivotOutput{Key: key, Enabled: false})
	}
	return true, nil
}

func (r importedRecipeSources) RemoveField(
	ctx context.Context,
	project string,
	authorized lifecycle.AuthorizedCapability,
	document authoringv2.Document,
	sourceStepID string,
	sourceFieldID string,
) (authoringv2.Document, error) {
	if document.ImportedRecipe == nil {
		return authoringv2.Document{}, fmt.Errorf("an imported source is required")
	}
	ref := *document.ImportedRecipe
	if err := ref.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	if r.registry == nil || r.engine == nil || ref.Name != r.defaultName {
		return authoringv2.Document{}, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported source recipes are not configured"}
	}
	if projectid.Canonical(project) != projectid.Canonical(authorized.Snapshot.Identity.Project) {
		return authoringv2.Document{}, capability.ErrStaleSnapshot
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return authoringv2.Document{}, err
	}
	entry, err := r.registry.LoadRecipeVersion(ctx, ref.Name, ref.TranslationVersion)
	if err != nil {
		return authoringv2.Document{}, &lifecycle.Error{Class: lifecycle.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the pinned dataframe recipe could not be loaded", Cause: err}
	}
	bundle, err := entry.Bundle.CanonicalBundle()
	if err != nil {
		return authoringv2.Document{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return authoringv2.Document{}, err
	}
	if digest != ref.Digest || entry.Digest != digest {
		return authoringv2.Document{}, fmt.Errorf("the imported dataframe recipe changed; reload its exact source version")
	}
	var original *recipe.Output
	for index := range bundle.Outputs {
		if bundle.Outputs[index].Name == ref.Output {
			original = &bundle.Outputs[index]
			break
		}
	}
	if original == nil {
		return authoringv2.Document{}, fmt.Errorf("pinned source output was not found")
	}
	plan, err := recipeimport.HydrateSourcePlan(*original, document.ImportedSource)
	if err != nil {
		return authoringv2.Document{}, err
	}
	var updated authoringv2.ImportedSourceStepChange
	stepFound := false
	for _, step := range plan.Steps {
		if step.ID != sourceStepID {
			continue
		}
		if !step.Enabled || !step.Editable || step.Kind != recipeimport.SourcePlanFieldGroup || step.Parameters == nil || step.Parameters.FieldGroup == nil {
			return authoringv2.Document{}, fmt.Errorf("pinned field source is not editable")
		}
		parameters := *step.Parameters
		fieldGroup := *step.Parameters.FieldGroup
		fieldGroup.Fields = append([]authoringv2.ImportedFieldParameters(nil), step.Parameters.FieldGroup.Fields...)
		fieldFound := false
		for index := range fieldGroup.Fields {
			field := &fieldGroup.Fields[index]
			if field.FieldID != sourceFieldID {
				continue
			}
			if fieldFound {
				return authoringv2.Document{}, fmt.Errorf("pinned source field identity is ambiguous")
			}
			fieldFound = true
			if !field.Enabled {
				return authoringv2.Document{}, fmt.Errorf("pinned source field is already disabled")
			}
			field.Enabled = false
		}
		if !fieldFound {
			return authoringv2.Document{}, fmt.Errorf("pinned source field was not found")
		}
		parameters.FieldGroup = &fieldGroup
		updated = authoringv2.ImportedSourceStepChange{StepID: step.ID, Enabled: step.Enabled, Parameters: &parameters}
		stepFound = true
		break
	}
	if !stepFound {
		return authoringv2.Document{}, fmt.Errorf("pinned field source step was not found")
	}
	return r.Edit(ctx, project, authorized, document, &updated)
}

func bindPinnedFieldRemoval(
	document authoringv2.Document,
	original recipe.Output,
	current importedRecipeResolution,
	plan authoringv2.ImportedSourcePlan,
	network *authoringv2.AuthoringNetwork,
) error {
	if network == nil {
		return fmt.Errorf("authoring network is required")
	}
	var schema []lower.CompiledOutputColumn
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
	fieldSlotsExact := err == nil && verifyPinnedFieldBindings(plan.FieldBindings, fieldColumnIDs) == nil
	steps := map[string]authoringv2.ImportedSourceStep{}
	for _, step := range plan.Steps {
		steps[step.ID] = step
	}
	bindings := map[string]authoringv2.NetworkColumnBinding{}
	bindingCount := map[string]int{}
	for _, binding := range network.Bindings {
		bindingCount[binding.ColumnID]++
		if bindingCount[binding.ColumnID] == 1 {
			bindings[binding.ColumnID] = binding
		}
	}
	for index := range network.ProjectedFields {
		field := &network.ProjectedFields[index]
		if field.Owner != authoringv2.ProjectedFieldOwnerPinned || field.ColumnID == "" {
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
		if !ok || !step.Enabled || !step.Editable || step.Parameters == nil {
			continue
		}
		if binding.OutputKey != "" && supportsPinnedOutputRemoval(step, binding.OutputKey) {
			field.SourceOutputKey = binding.OutputKey
			field.Removal = authoringv2.ProjectedFieldRemovalDisablePinnedOutput
			field.Status = authoringv2.ProjectedFieldStatusExact
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
			field.Removal = authoringv2.ProjectedFieldRemovalDisablePinned
			field.Status = authoringv2.ProjectedFieldStatusExact
		}
	}
	return nil
}

func supportsPinnedOutputRemoval(step authoringv2.ImportedSourceStep, outputKey string) bool {
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
