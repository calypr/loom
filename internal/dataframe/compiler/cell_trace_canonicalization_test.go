package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestCanonicalExecutionPhysicalPlanPreservesValidRelatedCellTrace(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionRelatedSourceCellTraceRecipeOutput())
	physical := ir.ClonePhysicalPlan(output.Plan)
	var err error
	physical, err = withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := constructionCellTraceLineage(output, *physical.StageSequence, "observation_status")
	if err != nil {
		t.Fatal(err)
	}
	physical.BindVars["cell_trace_offset"] = 0
	physical.BindVars["cell_trace_limit"] = 10
	physical.BindVars["cell_trace_fetch_limit"] = 11
	physical.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{
		Construction: lineage, OffsetBindKey: "cell_trace_offset", LimitBindKey: "cell_trace_limit", FetchLimitBindKey: "cell_trace_fetch_limit",
	}
	if err := physical.Validate(); err != nil {
		t.Fatalf("compiled related-source trace fixture is invalid: %v", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		t.Fatalf("compiled related-source trace scope is invalid: %v", err)
	}
	if lineage.RelatedSource == nil || len(lineage.RelatedSource.Subplan.Operations) == 0 {
		t.Fatal("fixture lacks its valid related-source subplan")
	}
	wantSource := lineage.RelatedSource.Subplan.Operations[0].Source
	if wantSource == (ir.PhysicalSource{}) {
		t.Fatal("fixture related-source operation has no nonempty Source metadata")
	}
	canonical := ir.CanonicalExecutionPhysicalPlan(physical)
	gotSource := canonical.StageSequence.CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source
	if gotSource != wantSource {
		t.Fatalf("canonicalization changed valid related trace source: got=%+v want=%+v", gotSource, wantSource)
	}
	if physical.StageSequence.CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source != wantSource {
		t.Fatal("canonicalization mutated the validated source plan")
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got, want := hex.EncodeToString(sum[:]), "6f20d9b42c0ed14ccd2bdc1dbbc95cc4ddcf59145180de1f570a8a57d768ffbe"; got != want {
		t.Fatalf("valid construction related trace canonical fingerprint = %s, want unchanged %s", got, want)
	}
}
