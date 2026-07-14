package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

// ResolvedColumn is a discovered output column whose name and logical value
// type have been frozen before execution. It is intentionally independent of
// ClickHouse or any other storage type.
type ResolvedColumn struct {
	Output      string
	DynamicName string
	Column      recipeplan.Column
}

// ResolvedRecipePlan is the only recipe representation accepted by a
// production execution/materialization adapter. Stored recipe data and
// request-scoped discovery are never mutated in place.
type ResolvedRecipePlan struct {
	SemanticPlan         RecipePlan
	ResolvedColumns      map[string][]ResolvedColumn
	ResolvedSchemaDigest string
	ScopeDigest          string
	SourceGeneration     string
}

// DiscoveryCandidate is the backend-neutral result of one scoped discovery
// query. The discovery implementation owns authorization and generation
// predicates; the resolver only freezes its deterministic result.
type DiscoveryCandidate struct {
	Key       string
	ValueType string
}

// DiscoverFunc is called once per dynamic map. It must use the same project,
// generation, and authorization scope as row execution.
type DiscoverFunc func(context.Context, OutputPlan, SemanticDynamicMap, recipe.RuntimeBindings) ([]DiscoveryCandidate, error)

// ResolveRecipePlan freezes all dynamic schemas and records the scope and
// source generation used to obtain them. A plan with no dynamic maps still
// receives a deterministic schema digest.
func ResolveRecipePlan(ctx context.Context, plan RecipePlan, scopeDigest, sourceGeneration string, discover DiscoverFunc) (ResolvedRecipePlan, error) {
	if strings.TrimSpace(sourceGeneration) == "" {
		sourceGeneration = plan.Bindings.DatasetGeneration
	}
	if strings.TrimSpace(sourceGeneration) == "" {
		return ResolvedRecipePlan{}, fmt.Errorf("source dataset generation is required")
	}
	if strings.TrimSpace(scopeDigest) == "" {
		for _, output := range plan.Outputs {
			if len(output.DynamicMaps) > 0 {
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
		for _, dynamic := range output.DynamicMaps {
			if discover == nil && len(dynamic.Columns) == 0 {
				return ResolvedRecipePlan{}, fmt.Errorf("output %q dynamic map %q requires scoped discovery", output.Name, dynamic.Name)
			}
			candidates := make([]recipeplan.Candidate, 0, len(dynamic.Columns))
			if discover != nil {
				observed, err := discover(ctx, output, dynamic, plan.Bindings)
				if err != nil {
					return ResolvedRecipePlan{}, fmt.Errorf("output %q dynamic map %q discovery: %w", output.Name, dynamic.Name, err)
				}
				for _, candidate := range observed {
					candidates = append(candidates, recipeplan.Candidate{Key: candidate.Key, ValueType: candidate.ValueType})
				}
			} else {
				for _, column := range dynamic.Columns {
					candidates = append(candidates, recipeplan.Candidate{Key: column, ValueType: "unknown"})
				}
			}
			schema, err := recipeplan.Freeze(recipeplan.DynamicSpec{
				Name: dynamic.Name, AllowedKeys: dynamic.Columns, MaxColumns: dynamic.MaxColumns, Collision: output.Collision,
			}, candidates)
			if err != nil {
				return ResolvedRecipePlan{}, fmt.Errorf("output %q dynamic map %q: %w", output.Name, dynamic.Name, err)
			}
			key := output.Name + ":" + dynamic.Name
			columns := make([]ResolvedColumn, 0, len(schema.Columns))
			for _, column := range schema.Columns {
				columns = append(columns, ResolvedColumn{Output: output.Name, DynamicName: dynamic.Name, Column: column})
			}
			resolved.ResolvedColumns[key] = columns
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
	}{plan.RecipeDigest, scopeDigest, sourceGeneration, ordered})
	if err != nil {
		return ResolvedRecipePlan{}, fmt.Errorf("resolved schema digest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	resolved.ResolvedSchemaDigest = hex.EncodeToString(sum[:])
	return resolved, nil
}
