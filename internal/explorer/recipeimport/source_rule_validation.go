package recipeimport

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validateRuleAddition(kind, resourceType string, params *authoringv2.ImportedSourceParameters) error {
	switch kind {
	case SourcePlanDynamicColumn:
		return validateDynamicRuleAddition(resourceType, params.Dynamic)
	case SourcePlanExtensionColumn:
		return validateExtensionRuleAddition(resourceType, params.Extension)
	case SourcePlanCatalogProjection:
		return validateCatalogRuleAddition(resourceType, params.Catalog)
	default:
		return fmt.Errorf("source rule kind %q is unavailable", kind)
	}
}

func validateDynamicRuleAddition(resourceType string, rule *authoringv2.ImportedDynamicParameters) error {
	if err := validateRuleName(rule.Name); err != nil {
		return err
	}
	system := recipe.Expression{Select: "item.system"}
	lastSystem := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{system}}
	switch {
	case reflect.DeepEqual(rule.Source, recipe.Expression{Select: "identifier[]"}):
		value := recipe.Expression{Select: "item.value"}
		if !reflect.DeepEqual(rule.Value, &value) || (!reflect.DeepEqual(rule.Key, &system) && !reflect.DeepEqual(rule.Key, &lastSystem)) {
			return fmt.Errorf("identifier columns must use the system and value of the same Identifier item")
		}
		_, err := fhirschema.ValidateIdentifierBinding(resourceType, fhirschema.IdentifierBinding{
			OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value",
			SystemURI: "urn:loom:identifier-system", LogicalType: "string",
		})
		return err
	case reflect.DeepEqual(rule.Source, recipe.Expression{Select: "category[].coding[]"}):
		value := recipe.Expression{Select: "item.display"}
		if !reflect.DeepEqual(rule.Key, &lastSystem) || !reflect.DeepEqual(rule.Value, &value) {
			return fmt.Errorf("category columns must use the system and display of the same Coding item")
		}
		owner, ok := fhirschema.ResolvePath(resourceType, "category[]")
		if !ok || owner.Property.ItemRef != "CodeableConcept" {
			return fmt.Errorf("%s.category[] must contain CodeableConcept items", resourceType)
		}
		_, err := fhirschema.ValidateCorrelatedBinding(resourceType, fhirschema.CorrelatedBinding{
			OwnerPath: "category[]", KeyPath: "category[].coding[]", ValueScope: fhirschema.CorrelatedValueScopeCoding,
			SystemPath: "system", CodePath: "code", ValuePath: "display", LogicalType: "string",
		})
		return err
	default:
		return fmt.Errorf("unsupported keyed source %q", rule.Source.Select)
	}
}

func validateExtensionRuleAddition(resourceType string, rule *authoringv2.ImportedExtensionParameters) error {
	if err := validateRuleName(rule.Name); err != nil {
		return err
	}
	path := rule.Source.Select
	if path == "" || !reflect.DeepEqual(rule.Source, recipe.Expression{Select: path}) {
		return fmt.Errorf("extension columns require a repeated Extension selector")
	}
	for _, segment := range strings.Split(path, ".") {
		if segment != "extension[]" {
			return fmt.Errorf("extension columns require extension[] or a nested extension[] path")
		}
	}
	return fhirschema.ValidateExtensionOwnerPath(resourceType, path)
}

func validateCatalogRuleAddition(resourceType string, rule *authoringv2.ImportedCatalogParameters) error {
	if err := catalogRuleProjection(rule, authoringv2.MaxImportedRuleColumns).Validate(); err != nil {
		return err
	}
	for _, paths := range [][]string{rule.IncludePaths, rule.ExcludePaths} {
		for _, path := range paths {
			if _, ok := fhirschema.ResolvePath(resourceType, path); path == "" || path != fhirschema.CanonicalizePath(path) || !ok {
				return fmt.Errorf("catalog path %q is not represented in %s", path, resourceType)
			}
		}
	}
	return nil
}

func catalogRuleProjection(rule *authoringv2.ImportedCatalogParameters, limit int) recipe.CatalogProjection {
	projection := recipe.CatalogProjection{
		Name: rule.Name, IncludePaths: rule.IncludePaths, ExcludePaths: rule.ExcludePaths,
		Kinds: rule.Kinds, Naming: rule.Naming, MaxColumns: limit,
	}
	for _, override := range rule.Outputs {
		projection.OutputOverrides = append(projection.OutputOverrides, recipe.CatalogProjectionOutputOverride{
			FieldPath: override.FieldPath, Enabled: override.Enabled, Name: override.Name,
		})
	}
	return projection
}

func validateRuleName(name string) error {
	if name == "" || name != strings.TrimSpace(name) {
		return fmt.Errorf("source rule requires a trimmed name")
	}
	return nil
}
