package recipeimport

import (
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validateRuleAddition(kind, resourceType string, params *ws.ImportedSourceParameters) error {
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

func validateDynamicRuleAddition(resourceType string, rule *ws.ImportedDynamicParameters) error {
	if err := validateRuleName(rule.Name); err != nil {
		return err
	}
	if err := validateDynamicItemPair(rule.Source, rule.Key, rule.Value); err != nil {
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
		_, err := fhirbinding.ValidateIdentifierBinding(resourceType, fhirbinding.IdentifierBinding{
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
		_, err := fhirbinding.ValidateCorrelatedBinding(resourceType, fhirbinding.CorrelatedBinding{
			OwnerPath: "category[]", KeyPath: "category[].coding[]", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
			SystemPath: "system", CodePath: "code", ValuePath: "display", LogicalType: "string",
		})
		return err
	default:
		return fmt.Errorf("unsupported keyed source %q", rule.Source.Select)
	}
}

func validateDynamicItemPair(source recipe.Expression, key, value *recipe.Expression) error {
	if source.Select == "" || !strings.HasSuffix(source.Select, "[]") || key == nil || value == nil {
		return fmt.Errorf("keyed columns require a repeated source and a key and value selector")
	}
	if err := validateItemExpression(*key); err != nil {
		return fmt.Errorf("column key must come from the source item: %w", err)
	}
	if err := validateItemExpression(*value); err != nil {
		return fmt.Errorf("column value must come from the same source item: %w", err)
	}
	return nil
}

func validateItemExpression(expression recipe.Expression) error {
	if expression.Select != "" {
		selector := strings.TrimSpace(expression.Select)
		if !strings.HasPrefix(selector, "item.") || strings.TrimSpace(strings.TrimPrefix(selector, "item.")) == "" {
			return fmt.Errorf("selector %q must be relative to item", selector)
		}
		if strings.Contains(strings.TrimPrefix(selector, "item."), "[]") {
			return fmt.Errorf("selector %q crosses another repeated boundary", selector)
		}
		return nil
	}
	if expression.Call == "" || len(expression.Args) != 1 || expression.Literal != nil || expression.Document != nil {
		return fmt.Errorf("expression must select an item field or normalize one item selector")
	}
	call := strings.ToLower(strings.TrimSpace(expression.Call))
	switch call {
	case "last_segment", "basename", "path_segment", "sanitize_name", "sanitize_graphql_name":
	default:
		return fmt.Errorf("call %q cannot preserve the source item", expression.Call)
	}
	return validateItemExpression(expression.Args[0])
}

func validateExtensionRuleAddition(resourceType string, rule *ws.ImportedExtensionParameters) error {
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
	return fhirbinding.ValidateExtensionOwnerPath(resourceType, path)
}

func validateCatalogRuleAddition(resourceType string, rule *ws.ImportedCatalogParameters) error {
	if err := catalogRuleProjection(rule, ws.MaxImportedRuleColumns).Validate(); err != nil {
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

func catalogRuleProjection(rule *ws.ImportedCatalogParameters, limit int) recipe.CatalogProjection {
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
