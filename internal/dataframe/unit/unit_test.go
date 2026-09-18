package unit

import (
	"errors"
	"testing"
)

func TestUnitConversionRuleValidationAcceptsIdentityLinearAndAffine(t *testing.T) {
	target := UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}
	dimension := UnitDimension{Mass: 1}
	for _, rule := range []UnitConversionRule{
		{ID: "ucum-mg-v1", Version: "1", Kind: UnitConversionIdentity, Source: target, Target: target, Dimension: dimension, Scale: 1},
		{ID: "ucum-g-mg-v1", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: target.System, Code: "g"}, Target: target, Dimension: dimension, Scale: 1000},
		{ID: "ucum-c-mg-v1", Version: "1", Kind: UnitConversionAffine, Source: UnitIdentity{System: "urn:test", Code: "celsius"}, Target: UnitIdentity{System: "urn:test", Code: "fahrenheit"}, Dimension: UnitDimension{Temperature: 1}, Scale: 1.8, Offset: 32},
	} {
		ruleTarget, ruleDimension := target, dimension
		if rule.Kind == UnitConversionAffine {
			ruleTarget, ruleDimension = rule.Target, rule.Dimension
		}
		if err := rule.Validate(ruleTarget, ruleDimension); err != nil {
			t.Fatalf("%s validation = %v", rule.Kind, err)
		}
	}
}

func TestUnitConversionRuleValidationRejectsDisplayOnlyIdentity(t *testing.T) {
	rule := UnitConversionRule{ID: "display-only", Version: "1", Kind: UnitConversionIdentity, Source: UnitIdentity{System: "", Code: "milligram"}, Target: UnitIdentity{System: "urn:test", Code: "mg"}, Dimension: UnitDimension{Mass: 1}, Scale: 1}
	if err := rule.Validate(rule.Target, rule.Dimension); err == nil {
		t.Fatal("display-only unit unexpectedly validated")
	}
}

func TestUnitConversionRuleValidationReportsIncompatibleDimension(t *testing.T) {
	rule := UnitConversionRule{ID: "wrong-dimension", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "urn:test", Code: "m"}, Target: UnitIdentity{System: "urn:test", Code: "mg"}, Dimension: UnitDimension{Length: 1}, Scale: 1}
	if err := rule.Validate(rule.Target, UnitDimension{Mass: 1}); !errors.Is(err, ErrUnitDimensionIncompatible) {
		t.Fatalf("dimension error = %v, want UNIT_DIMENSION_INCOMPATIBLE", err)
	}
}

func TestApprovedPolicyResolvesTrustedTargetAndRules(t *testing.T) {
	policy, err := ResolveApprovedUnitPolicy("to-centimeters", "1")
	if err != nil {
		t.Fatal(err)
	}
	dimension, rules, err := ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !dimension.Equal(UnitDimension{Length: 1}) || len(rules) != 2 || rules[0].Scale != 100 {
		t.Fatalf("resolved policy = dimension %#v rules %#v", dimension, rules)
	}
}

func TestApprovedTemperaturePolicyPinsAffineRule(t *testing.T) {
	policy, err := ResolveApprovedUnitPolicy("to-fahrenheit", "1")
	if err != nil {
		t.Fatal(err)
	}
	dimension, rules, err := ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !dimension.Equal(UnitDimension{Temperature: 1}) || len(rules) != 2 || rules[0].Kind != UnitConversionAffine || rules[0].Offset != 32 {
		t.Fatalf("temperature policy = dimension %#v rules %#v", dimension, rules)
	}
}

func TestApprovedPolicyRejectsUnknownAndIncompatibleReferences(t *testing.T) {
	if err := ValidateUnitPolicyReference("not-approved", "1"); !errors.Is(err, ErrUnitIdentityUnknown) {
		t.Fatalf("unknown policy error = %v", err)
	}
	_, _, err := ResolveApprovedUnitRules([]UnitRuleReference{{ID: "ucum:m-to-cm", Version: "1"}}, UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"})
	if err == nil {
		t.Fatal("incompatible target/rule unexpectedly resolved")
	}
}
