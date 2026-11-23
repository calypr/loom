package recipeplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type ResolvedColumn struct {
	Output      string
	DynamicName string
	Column      Column
}

type ResolvedRecipePlan struct {
	SemanticPlan         RecipePlan
	ResolvedColumns      map[string][]ResolvedColumn
	ResolvedSchemaDigest string
	ScopeDigest          string
	SourceGeneration     string
}

func ResolveRecipePlan(plan RecipePlan, scopeDigest, sourceGeneration string) (ResolvedRecipePlan, error) {
	if strings.TrimSpace(sourceGeneration) == "" {
		sourceGeneration = plan.Bindings.DatasetGeneration
	}

	if strings.TrimSpace(scopeDigest) == "" {
		for _, output := range plan.Outputs {
			if outputHasDynamicMaps(output.Root) || len(output.DynamicMaps) > 0 {
				return ResolvedRecipePlan{}, fmt.Errorf("scoped authorization digest is required for dynamic discovery")
			}
		}
		scopeDigest = "unscoped"
	}
	resolved := ResolvedRecipePlan{
		SemanticPlan:     plan,
		ResolvedColumns:  make(map[string][]ResolvedColumn),
		ScopeDigest:      scopeDigest,
		SourceGeneration: sourceGeneration,
	}
	for _, output := range plan.Outputs {
		var walk func(SemanticNode) error
		walk = func(node SemanticNode) error {
			for _, dynamic := range node.DynamicMaps {
				if len(dynamic.ColumnSourceKeys) > 0 && len(dynamic.OutputOverrides) > 0 && dynamic.OutputKey == "" {
					return fmt.Errorf("output %q dynamic map %q cannot apply output overrides to mapped source keys", output.Name, dynamic.Name)
				}
				if dynamic.Columns == nil {
					return fmt.Errorf("output %q dynamic map %q is unresolved; resolve it through the catalog-backed recipe resolver first", output.Name, dynamic.Name)
				}
				if len(dynamic.Columns) == 0 {

					if len(dynamic.OutputOverrides) > 0 {
						resolved.ResolvedColumns[dynamicMapKey(output.Name, dynamic)] = []ResolvedColumn{}
					}
					continue
				}
				enabledKeys, _ := dynamicOutputSelection(dynamic.Columns, dynamic.OutputOverrides)
				candidates := make([]Candidate, 0, len(enabledKeys))
				for _, column := range enabledKeys {
					valueType := dynamic.ColumnTypes[column]
					if valueType == "" {
						valueType = "unknown"
					}
					candidates = append(candidates, Candidate{Key: column, ValueType: valueType})
				}
				key := dynamicMapKey(output.Name, dynamic)
				if len(enabledKeys) == 0 {
					resolved.ResolvedColumns[key] = []ResolvedColumn{}
					continue
				}
				schema, err := Freeze(DynamicSpec{
					Name: dynamic.Name, ColumnPrefix: dynamic.ColumnPrefix, AllowedKeys: enabledKeys, MaxColumns: dynamic.MaxColumns, Collision: output.Collision,
				}, candidates)
				if err != nil {
					return fmt.Errorf("output %q dynamic map %q: %w", output.Name, dynamic.Name, err)
				}
				columns := make([]ResolvedColumn, 0, len(schema.Columns))
				for _, column := range schema.Columns {
					if sourceKey := dynamic.ColumnSourceKeys[column.SourceKey]; sourceKey != "" {
						column.SourceKey = sourceKey
					}
					columns = append(columns, ResolvedColumn{Output: output.Name, DynamicName: dynamic.Name, Column: column})
				}
				resolved.ResolvedColumns[key] = columns
			}
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(output.Root); err != nil {
			return ResolvedRecipePlan{}, err
		}
	}

	keys := make([]string, 0, len(resolved.ResolvedColumns))
	for key := range resolved.ResolvedColumns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]struct {
		Key     string           `json:"key"`
		Columns []ResolvedColumn `json:"columns"`
	}, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, struct {
			Key     string           `json:"key"`
			Columns []ResolvedColumn `json:"columns"`
		}{key, resolved.ResolvedColumns[key]})
	}
	canonical, err := json.Marshal(struct {
		RecipeDigest, ScopeDigest, Generation string
		Columns                               any `json:"columns"`
	}{plan.RecipeDigest, scopeDigest, sourceGeneration, ordered})
	if err != nil {
		return ResolvedRecipePlan{}, fmt.Errorf("resolved schema digest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	resolved.ResolvedSchemaDigest = hex.EncodeToString(sum[:])
	return resolved, nil
}

func dynamicOutputSelection(columns []string, overrides []recipe.DynamicColumnOutputOverride) (enabled, excluded []string) {
	byKey := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		byKey[override.Key] = override.Enabled
	}
	for _, key := range columns {
		if isEnabled, exists := byKey[key]; exists && !isEnabled {
			excluded = append(excluded, key)
			continue
		}
		enabled = append(enabled, key)
	}
	return enabled, excluded
}

func dynamicMapKey(output string, dynamic SemanticDynamicMap) string {
	if strings.TrimSpace(dynamic.ScopeAlias) == "" || dynamic.ScopeAlias == "root" {
		return output + ":" + dynamic.Name
	}
	return output + ":" + dynamic.ScopeAlias + ":" + dynamic.Name
}

func outputHasDynamicMaps(node SemanticNode) bool {
	if len(node.DynamicMaps) > 0 {
		return true
	}
	for _, child := range node.Children {
		if outputHasDynamicMaps(child) {
			return true
		}
	}
	return false
}
