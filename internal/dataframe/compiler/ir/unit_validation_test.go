package ir

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func TestPhysicalUnitNormalizationValidatesPinnedAffineRules(t *testing.T) {
	policy, err := unit.ResolveApprovedUnitPolicy("to-fahrenheit", "1")
	if err != nil {
		t.Fatal(err)
	}
	dimension, rules, err := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatal(err)
	}
	value := spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "value"}}}
	extract := PhysicalExtract{
		Source: irRootPayload(), ResourceType: "Observation", Selector: value,
		ExecutionMode: PhysicalSelectorGeneric,
		UnitNormalization: &PhysicalUnitNormalization{
			OriginalValue: value,
			SourceSystem:  spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "system"}}},
			SourceCode:    spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}},
			Target:        policy.Target, Dimension: dimension, Rules: rules,
		},
	}
	if err := validatePhysicalExtract(extract, map[string]bool{"root": true}, nil); err != nil {
		t.Fatal(err)
	}
	mismatched := extract
	mismatchedNormalization := *extract.UnitNormalization
	mismatched.UnitNormalization = &mismatchedNormalization
	mismatched.UnitNormalization.SourceSystem = spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}}
	if err := validatePhysicalExtract(mismatched, map[string]bool{"root": true}, nil); err == nil || !strings.Contains(err.Error(), "must be siblings") {
		t.Fatalf("unpaired unit identity selector accepted: %v", err)
	}
	extract.UnitNormalization.Rules[0].Dimension = unit.UnitDimension{Length: 1}
	if err := validatePhysicalExtract(extract, map[string]bool{"root": true}, nil); err == nil || !strings.Contains(err.Error(), "UNIT_DIMENSION_INCOMPATIBLE") {
		t.Fatalf("tampered affine rule accepted: %v", err)
	}
}

func irRootPayload() PhysicalValue {
	return PhysicalValue{Variable: "root", Path: []string{"payload"}}
}
