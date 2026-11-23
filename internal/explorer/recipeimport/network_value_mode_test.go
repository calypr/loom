package recipeimport

import (
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"testing"
)

func TestCompiledNetworkUsesAuthoringValuePolicyVocabulary(t *testing.T) {
	plan := ws.ImportedSourcePlan{Steps: []ws.ImportedSourceStep{{ID: "fields", Kind: SourcePlanFieldGroup, Title: "Fields", Summary: "Pinned fields", Editable: true}}}
	for _, tc := range []struct{ compiled, authored string }{{"scalar", "VALUE"}, {"first", "FIRST"}, {"array", "ALL"}, {"distinct_array", "DISTINCT"}, {"AUTO", "AUTO"}, {"ALL", "ALL"}} {
		t.Run(tc.compiled, func(t *testing.T) {
			network := ws.AuthoringNetwork{Occurrences: []ws.NetworkOccurrence{{OccurrenceID: "base", ResourceType: "Patient", Enabled: true}}, Bindings: []ws.NetworkColumnBinding{{ColumnID: "id", Status: "UNKNOWN"}}}
			got, err := BindCompiledNetwork(network, []facade.CompiledOutputColumn{{ID: "id", Name: "id", SourceOrigin: &facade.CompiledSourceOrigin{OccurrenceID: "base", SourcePath: "fields", SourceKind: SourcePlanFieldGroup, ValueMode: tc.compiled}}}, &plan)
			if err != nil {
				t.Fatal(err)
			}
			if got.Bindings[0].ValueMode != tc.authored {
				t.Fatalf("compiled shape %q leaked as value policy %q; want authoring policy %q", tc.compiled, got.Bindings[0].ValueMode, tc.authored)
			}
		})
	}
}
