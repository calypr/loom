package authoringv2

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestImportedSourcePlanMigratesLegacyDisabledSteps(t *testing.T) {
	var plan ImportedSourcePlan
	err := json.Unmarshal([]byte(`{
		"steps":[
			{"id":"output","enabled":true},
			{"id":"dynamicColumns[0]","enabled":false},
			{"id":"ruleAdditions[0]","enabled":false},
			{"id":"ruleAdditions[1]","enabled":true}
		],
		"ruleAdditions":[
			{"stepId":"ruleAdditions[0]","enabled":false},
			{"stepId":"ruleAdditions[1]","enabled":true}
		]
	}`), &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "output" || plan.Steps[1].ID != "ruleAdditions[1]" ||
		len(plan.RuleAdditions) != 1 || plan.RuleAdditions[0].StepID != "ruleAdditions[1]" ||
		plan.NextRuleIndex != 2 || !reflect.DeepEqual(plan.RemovedStepIDs, []string{"dynamicColumns[0]"}) {
		t.Fatalf("unexpected migrated plan: %#v", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"enabled"`)) || bytes.Contains(encoded, []byte(`"ruleAdditions[0]"`)) {
		t.Fatalf("legacy state leaked into current contract: %s", encoded)
	}
	var roundTrip ImportedSourcePlan
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(plan, roundTrip) {
		t.Fatalf("current plan failed round trip: %v", err)
	}
}

func TestImportedSourceStepRemovalRejectsEnablement(t *testing.T) {
	var change ImportedSourceStepChange
	for _, wire := range []string{
		`{"stepId":"fields","enabled":true}`,
		`{"stepId":"fields","enabled":false}`,
		`{"stepId":"fields","remove":true,"maxColumns":1}`,
		`{"stepId":"fields","remove":false}`,
	} {
		if err := json.Unmarshal([]byte(wire), &change); err == nil {
			t.Fatalf("accepted %s", wire)
		}
	}
	if err := json.Unmarshal([]byte(`{"stepId":"fields","remove":true}`), &change); err != nil || !change.Remove {
		t.Fatalf("removal did not decode: %v", err)
	}
}
