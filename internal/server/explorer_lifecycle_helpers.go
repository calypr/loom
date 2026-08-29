package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
)

func resolvedOutputArtifacts(resolved engine.Resolved) (map[string]string, map[string]map[string]string, error) {
	result := make(map[string]string, len(resolved.Compiled.Outputs))
	provenance := make(map[string]map[string]string, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		columns := make([]struct {
			Name, SemanticPath, Kind, Cardinality string
			Nullable, Internal, Identity          bool
		}, 0, len(output.OutputSchema))
		provenance[output.Name] = make(map[string]string, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			columns = append(columns, struct {
				Name, SemanticPath, Kind, Cardinality string
				Nullable, Internal, Identity          bool
			}{column.Name, column.SemanticPath, column.Kind, column.Cardinality, column.Nullable, column.Internal, column.Identity})
			value := "EXPLICIT"
			if column.Discovered {
				value = "DISCOVERED"
			}
			provenance[output.Name][column.Name] = value
		}
		dynamic := make([]struct {
			Name, SemanticPath, DynamicName, SourceKey, ValueType string
			AllowUnknownKeys                                      bool
		}, 0, len(output.DynamicColumns))
		for _, column := range output.DynamicColumns {
			dynamic = append(dynamic, struct {
				Name, SemanticPath, DynamicName, SourceKey, ValueType string
				AllowUnknownKeys                                      bool
			}{column.Name, column.SemanticPath, column.DynamicName, column.SourceKey, column.ValueType, column.AllowUnknownKeys})
		}
		payload, err := json.Marshal(struct {
			Version, CompilerVersion         int
			Name, RootResourceType, RowGrain string
			Scope, Generation                string
			Columns                          []string
			OutputSchema                     any
			RowIdentity                      any
			DynamicColumns                   any
			Plan                             ir.PhysicalPlan
		}{2, resolved.Compiled.Version, output.Name, output.RootResourceType, string(output.RowGrain), resolved.Semantic.ScopeDigest, resolved.Compiled.SourceGeneration, append([]string(nil), output.Columns...), columns, output.RowIdentity, dynamic, ir.CanonicalExecutionPhysicalPlan(output.Plan)})
		if err != nil {
			return nil, nil, fmt.Errorf("fingerprint output %q: %w", output.Name, err)
		}
		sum := sha256.Sum256(payload)
		result[output.Name] = hex.EncodeToString(sum[:])
	}
	return result, provenance, nil
}

func applyReceiptColumnProvenance(output *lower.CompiledRecipeOutput, values map[string]string) error {
	if output == nil || len(values) != len(output.OutputSchema) {
		return fmt.Errorf("output column provenance set changed")
	}
	for index := range output.OutputSchema {
		value, ok := values[output.OutputSchema[index].Name]
		if !ok || (value != "EXPLICIT" && value != "DISCOVERED") {
			return fmt.Errorf("output column provenance missing for %q", output.OutputSchema[index].Name)
		}
		output.OutputSchema[index].Discovered = value == "DISCOVERED"
	}
	for index := range output.DynamicColumns {
		value, ok := values[output.DynamicColumns[index].Name]
		if !ok {
			return fmt.Errorf("dynamic column provenance missing for %q", output.DynamicColumns[index].Name)
		}
		output.DynamicColumns[index].Discovered = value == "DISCOVERED"
	}
	return nil
}
