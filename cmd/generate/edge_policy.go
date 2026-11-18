package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type loomEdgePolicy struct {
	FHIRVersion       string                                   `json:"fhirVersion"`
	Definitions       []string                                 `json:"definitions"`
	ResourceTypes     []string                                 `json:"resourceTypes"`
	Edges             map[string][]loomEdgePolicyLink          `json:"edges"`
	CustomDefinitions map[string]*Definition                   `json:"customDefinitions"`
	Properties        map[string]map[string]*Property          `json:"properties"`
	Aliases           map[string]map[string]string             `json:"aliases"`
	OmitProperties    map[string][]string                      `json:"omitProperties"`
	Required          map[string][]string                      `json:"required"`
	Validation        map[string]map[string]validationProperty `json:"validation"`
}

type validationProperty struct {
	Pattern          string              `json:"pattern"`
	Format           string              `json:"format"`
	MinLength        *int                `json:"minLength"`
	MaxLength        *int                `json:"maxLength"`
	Minimum          *float64            `json:"minimum"`
	ExclusiveMinimum *float64            `json:"exclusiveMinimum"`
	Const            json.RawMessage     `json:"const"`
	Items            *validationProperty `json:"items"`
}

type loomEdgePolicyLink struct {
	Label        string `json:"label"`
	Path         string `json:"path"`
	TargetType   string `json:"targetType"`
	Backref      string `json:"backref"`
	Direction    string `json:"direction"`
	Multiplicity string `json:"multiplicity"`
}

func applyEdgePolicy(schema *Schema, path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read edge policy %q: %w", path, err)
	}
	var policy loomEdgePolicy
	if err := json.Unmarshal(source, &policy); err != nil {
		return fmt.Errorf("decode edge policy %q: %w", path, err)
	}
	if policy.FHIRVersion != "5.0.0" {
		return fmt.Errorf("edge policy FHIR version is %q, want 5.0.0", policy.FHIRVersion)
	}
	if len(policy.Definitions) == 0 || len(policy.ResourceTypes) == 0 {
		return fmt.Errorf("edge policy must name generated definitions and resource types")
	}
	allowedDefinitions := make(map[string]struct{}, len(policy.Definitions))
	for _, name := range policy.Definitions {
		allowedDefinitions[name] = struct{}{}
	}
	for name, definition := range policy.CustomDefinitions {
		schema.Defs[name] = definition
		allowedDefinitions[name] = struct{}{}
	}
	resourceTypes := make(map[string]struct{}, len(policy.ResourceTypes))
	for _, name := range policy.ResourceTypes {
		resourceTypes[name] = struct{}{}
	}
	for name, definition := range schema.Defs {
		if !isFHIRRootResourceDefinition(name, definition) {
			continue
		}
		if _, allowed := resourceTypes[name]; !allowed {
			delete(schema.Defs, name)
		}
	}
	for resource, properties := range policy.Properties {
		definition := schema.Defs[resource]
		if definition == nil {
			return fmt.Errorf("edge policy custom properties refer to missing definition %q", resource)
		}
		for name, property := range properties {
			definition.Properties[name] = property
		}
	}
	for resource, aliases := range policy.Aliases {
		definition := schema.Defs[resource]
		if definition == nil {
			return fmt.Errorf("edge policy aliases refer to missing definition %q", resource)
		}
		for source, alias := range aliases {
			property := definition.Properties[source]
			if property == nil {
				return fmt.Errorf("edge policy alias %s.%s has no source property", resource, source)
			}
			if definition.Properties[alias] != nil {
				return fmt.Errorf("edge policy alias %s.%s collides with an existing property", resource, alias)
			}
			definition.Properties[alias] = property
			delete(definition.Properties, source)
			for i, required := range definition.Required {
				if required == source {
					definition.Required[i] = alias
				}
			}
		}
	}
	for resource, properties := range policy.OmitProperties {
		definition := schema.Defs[resource]
		if definition == nil {
			return fmt.Errorf("edge policy omitted properties refer to missing definition %q", resource)
		}
		for _, name := range properties {
			delete(definition.Properties, name)
		}
	}
	for name := range schema.Defs {
		if _, allowed := allowedDefinitions[name]; !allowed {
			delete(schema.Defs, name)
		}
	}
	for resource, rules := range policy.Edges {
		definition := schema.Defs[resource]
		if definition == nil {
			return fmt.Errorf("edge policy refers to missing definition %q", resource)
		}
		definition.Links = make([]Link, 0, len(rules))
		for _, rule := range rules {
			hints := TargetHints{}
			if rule.Backref != "" {
				hints.Backref = []string{rule.Backref}
			}
			if rule.TargetType != "" {
				hints.RegexMatch = []string{rule.TargetType + "/*"}
			}
			if rule.Direction != "" {
				hints.Direction = []string{rule.Direction}
			}
			if rule.Multiplicity != "" {
				hints.Multiplicity = []string{rule.Multiplicity}
			}
			link := Link{Rel: rule.Label, TargetHints: hints, TemplatePointers: TemplatePointers{ID: rule.Path}}
			if rule.TargetType != "" {
				link.TargetSchema.Ref = fhirDefinitionBase + "/" + rule.TargetType
			}
			definition.Links = append(definition.Links, link)
		}
	}
	for name, required := range policy.Required {
		definition := schema.Defs[name]
		if definition == nil {
			return fmt.Errorf("edge policy required fields refer to missing definition %q", name)
		}
		definition.Required = required
	}
	for definitionName, properties := range policy.Validation {
		definition := schema.Defs[definitionName]
		if definition == nil {
			return fmt.Errorf("edge policy validation refers to missing definition %q", definitionName)
		}
		for propertyName, validation := range properties {
			property := definition.Properties[propertyName]
			if property == nil {
				return fmt.Errorf("edge policy validation refers to missing property %s.%s", definitionName, propertyName)
			}
			property.Pattern = validation.Pattern
			property.Format = validation.Format
			property.MinLength = validation.MinLength
			property.MaxLength = validation.MaxLength
			property.Minimum = validation.Minimum
			property.ExclusiveMinimum = validation.ExclusiveMinimum
			if len(validation.Const) > 0 {
				if err := json.Unmarshal(validation.Const, &property.Const); err != nil {
					return fmt.Errorf("edge policy constant %s.%s: %w", definitionName, propertyName, err)
				}
			}
			if validation.Items != nil && property.Items != nil {
				property.Items.Pattern = validation.Items.Pattern
				property.Items.Format = validation.Items.Format
				property.Items.MinLength = validation.Items.MinLength
				property.Items.MaxLength = validation.Items.MaxLength
				property.Items.Minimum = validation.Items.Minimum
				property.Items.ExclusiveMinimum = validation.Items.ExclusiveMinimum
				if len(validation.Items.Const) > 0 {
					if err := json.Unmarshal(validation.Items.Const, &property.Items.Const); err != nil {
						return fmt.Errorf("edge policy item constant %s.%s: %w", definitionName, propertyName, err)
					}
				}
			}
		}
	}
	return nil
}
