package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataset"
)

func selectorsForBundle(bundle recipe.Bundle) []dataset.DataframeSelector {
	selectors := make([]dataset.DataframeSelector, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		selectors = append(selectors, dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name})
	}
	return selectors
}

func verifyQueryableOutputs(bundle recipe.Bundle, execution graphresolver.RecipeExecution) error {
	states := map[string]string{}
	for _, output := range execution.Outputs {
		states[output.Name] = strings.ToUpper(output.State)
	}
	for _, output := range bundle.Outputs {
		state := states[output.Name]
		if state != "PUBLISHED" && state != "READY" && state != "ACTIVE" {
			return fmt.Errorf("output %q is not queryable (state %q)", output.Name, state)
		}
	}
	return nil
}

func resolvedOutputFingerprints(resolved engine.Resolved) map[string]string {
	result := make(map[string]string, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		payload, err := json.Marshal(struct {
			Version    int    `json:"version"`
			Name       string `json:"name"`
			Scope      string `json:"scope"`
			Generation string `json:"generation"`
			Output     any    `json:"output"`
		}{Version: 1, Name: output.Name, Scope: resolved.Semantic.ScopeDigest, Generation: resolved.Compiled.SourceGeneration, Output: output})
		if err != nil {
			continue
		}
		sum := sha256.Sum256(payload)
		result[output.Name] = hex.EncodeToString(sum[:])
	}
	return result
}
