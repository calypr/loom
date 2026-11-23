package ir

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLegacyPhysicalPlanKeepsUnusedExclusionSlot(t *testing.T) {
	plan := PhysicalPlan{Operations: []PhysicalOperation{{Kind: PhysicalLimitOp, Limit: &PhysicalLimit{BindKey: "limit"}}}}
	ordinary, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := MarshalLegacyPhysical(plan)
	if err != nil {
		t.Fatal(err)
	}
	slot := []byte(`,"TableShapeExclusionReturn":null`)
	if bytes.Count(legacy, slot) != 1 || !bytes.Equal(bytes.ReplaceAll(legacy, slot, nil), ordinary) {
		t.Fatalf("legacy encoding did not preserve exactly the historical slot\nordinary: %s\nlegacy: %s", ordinary, legacy)
	}
}
