package compilerprobe

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestTraversalGranularity(t *testing.T) {
	inputs, nodes, compiled, rejected := 0, 0, 0, 0
	seen := map[string]bool{}
	bindings := recipe.RuntimeBindings{Project: "granularity-probe", DatasetGeneration: "fixture", AuthScopeMode: authscope.ReadScopeUnrestricted}
	lowerBundle := func(bundle recipe.Bundle) ([]byte, error) {
		planned, err := recipeplan.BuildRecipePlan(bundle, bindings)
		if err != nil {
			return nil, err
		}
		resolved, err := recipeplan.ResolveRecipePlan(planned, "granularity-scope", bindings.DatasetGeneration)
		if err != nil {
			return nil, err
		}
		result, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			return nil, err
		}
		var plans []ir.PhysicalPlan
		for _, output := range result.Outputs {
			plans = append(plans, ir.CanonicalExecutionPhysicalPlan(output.Plan))
		}
		return json.Marshal(plans)
	}
	for _, raw := range researchCandidates(t) {
		original, err := decodeCandidate(raw)
		if err != nil {
			continue
		}
		key, err := original.ArchiveDigest()
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		candidate := original
		candidate.Outputs = append([]recipe.Output(nil), original.Outputs...)
		split := false
		for i := range candidate.Outputs {
			explicit := SplitTraversals(original.Outputs[i].Traversals)
			if len(explicit) == 0 {
				continue
			}
			split = true
			nodes += len(explicit)
			joined, err := JoinTraversals(explicit)
			if err != nil {
				t.Fatal(err)
			}
			candidate.Outputs[i].Traversals = joined
		}
		if !split {
			continue
		}
		inputs++
		restoredKey, err := candidate.ArchiveDigest()
		if err != nil || restoredKey != key {
			t.Fatalf("granularity round-trip changed recipe %q: %v", original.Name, err)
		}
		want, err := lowerBundle(original)
		got, gotErr := lowerBundle(candidate)
		if err != nil {
			if gotErr == nil || gotErr.Error() != err.Error() {
				t.Fatalf("rejection changed for %q: %v vs %v", original.Name, err, gotErr)
			}
			rejected++
			continue
		}
		if gotErr != nil || !bytes.Equal(want, got) {
			t.Fatalf("shared emitter diverged for %q: %v", original.Name, gotErr)
		}
		compiled++
	}
	t.Logf("unique traversal recipes=%d expanded traversal steps=%d shared-emitter canonical IR matches=%d same rejection=%d", inputs, nodes, compiled, rejected)
	if inputs == 0 {
		t.Fatal("no traversal fixture exercised")
	}
}
