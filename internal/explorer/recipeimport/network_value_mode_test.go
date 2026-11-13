package recipeimport

import (
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"testing"
)

func TestCompiledNetworkUsesAuthoringValuePolicyVocabulary(t *testing.T) {
	plan := authoringv2.ImportedSourcePlan{Steps: []authoringv2.ImportedSourceStep{{ID: "fields", Kind: SourcePlanFieldGroup, Title: "Fields", Summary: "Pinned fields", Enabled: true, Editable: true}}}
	for _, tc := range []struct{ compiled, authored string }{{"scalar", "VALUE"}, {"first", "FIRST"}, {"array", "ALL"}, {"distinct_array", "DISTINCT"}, {"AUTO", "AUTO"}, {"ALL", "ALL"}} {
		t.Run(tc.compiled, func(t *testing.T) {
			network := authoringv2.AuthoringNetwork{Occurrences: []authoringv2.NetworkOccurrence{{OccurrenceID: "base", ResourceType: "Patient", Enabled: true}}, Bindings: []authoringv2.NetworkColumnBinding{{ColumnID: "id", Status: "UNKNOWN"}}}
			got, err := BindCompiledNetwork(network, []lower.CompiledOutputColumn{{ID: "id", Name: "id", SourceOrigin: &lower.CompiledSourceOrigin{OccurrenceID: "base", SourcePath: "fields", SourceKind: SourcePlanFieldGroup, ValueMode: tc.compiled}}}, &plan)
			if err != nil {
				t.Fatal(err)
			}
			if got.Bindings[0].ValueMode != tc.authored {
				t.Fatalf("compiled shape %q leaked as value policy %q; want authoring policy %q", tc.compiled, got.Bindings[0].ValueMode, tc.authored)
			}
		})
	}
}
