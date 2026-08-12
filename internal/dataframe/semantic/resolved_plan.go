package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ResolvedColumn is a discovered output column whose name and logical value
// type have been frozen before execution. It is intentionally independent of
// ClickHouse or any other storage type.
type ResolvedColumn struct {
	Output      string
	DynamicName string
	Column      Column
}

// ResolvedRecipePlan is the only recipe representation accepted by a
// production execution/materialization adapter. Stored recipe data and
// request-scoped discovery are never mutated in place.
type ResolvedRecipePlan struct {
	SemanticPlan    RecipePlan
	ResolvedColumns map[string][]ResolvedColumn
	// ConceptColumns is carried independently of dynamic discovery so
	// publication adapters can retain authored concept identity even when a
	// release has no dynamic families.
	ConceptColumns       map[string][]ConceptColumn
	ResolvedSchemaDigest string
	ScopeDigest          string
	SourceGeneration     string
}

// ResolveRecipePlan freezes all dynamic schemas and records the scope and
// source generation used to obtain them. A plan with no dynamic maps still
// receives a deterministic schema digest. Dynamic schemas must already have
// been resolved by the catalog-backed recipe resolver before this boundary.
func ResolveRecipePlan(plan RecipePlan, scopeDigest, sourceGeneration string) (ResolvedRecipePlan, error) {
	if strings.TrimSpace(sourceGeneration) == "" {
		sourceGeneration = plan.Bindings.DatasetGeneration
	}
	// An empty generation is the intentional legacy-null dataset namespace. It
	// remains a precise scope when the catalog resolver used the same empty
	// generation during discovery; physical lowering renders the nil bind.
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
		ConceptColumns:   make(map[string][]ConceptColumn),
		ScopeDigest:      scopeDigest,
		SourceGeneration: sourceGeneration,
	}
	for _, output := range plan.Outputs {
		if len(output.ConceptColumns) > 0 {
			resolved.ConceptColumns[output.Name] = append([]ConceptColumn(nil), output.ConceptColumns...)
		}
		var walk func(SemanticNode) error
		walk = func(node SemanticNode) error {
			for _, dynamic := range node.DynamicMaps {
				if dynamic.Columns == nil {
					return fmt.Errorf("output %q dynamic map %q is unresolved; resolve it through the catalog-backed recipe resolver first", output.Name, dynamic.Name)
				}
				if len(dynamic.Columns) == 0 {
					// An empty catalog result is a valid optional family. The
					// resolver has already frozen it to zero columns, so there is
					// no schema or physical lookup to attach to this node.
					continue
				}
				candidates := make([]Candidate, 0, len(dynamic.Columns))
				for _, column := range dynamic.Columns {
					valueType := dynamic.ColumnTypes[column]
					if valueType == "" {
						valueType = "unknown"
					}
					candidates = append(candidates, Candidate{Key: column, ValueType: valueType})
				}
				schema, err := Freeze(DynamicSpec{
					Name: dynamic.Name, ColumnPrefix: dynamic.ColumnPrefix, AllowedKeys: dynamic.Columns, MaxColumns: dynamic.MaxColumns, Collision: output.Collision,
				}, candidates)
				if err != nil {
					return fmt.Errorf("output %q dynamic map %q: %w", output.Name, dynamic.Name, err)
				}
				key := dynamicMapKey(output.Name, dynamic)
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
	// Marshal map keys deterministically by normalizing through sorted keys.
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
		ConceptColumns                        any `json:"conceptColumns"`
	}{plan.RecipeDigest, scopeDigest, sourceGeneration, ordered, resolved.ConceptColumns})
	if err != nil {
		return ResolvedRecipePlan{}, fmt.Errorf("resolved schema digest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	resolved.ResolvedSchemaDigest = hex.EncodeToString(sum[:])
	return resolved, nil
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
