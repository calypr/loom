package unit

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	ErrUnitIdentityUnknown       = errors.New("UNIT_IDENTITY_UNKNOWN")
	ErrUnitDimensionIncompatible = errors.New("UNIT_DIMENSION_INCOMPATIBLE")
)

// UnitIdentity is the stable identity of a source or target unit. Display
// labels are intentionally absent. FHIR Quantity.unit is a human label and
// must never select a conversion rule.
type UnitIdentity struct {
	System string `json:"system"`
	Code   string `json:"code"`
}

func (u UnitIdentity) normalized() UnitIdentity {
	return UnitIdentity{System: strings.TrimSpace(u.System), Code: strings.TrimSpace(u.Code)}
}

func (u UnitIdentity) normalizedKey() string {
	n := u.normalized()
	return n.System + "\x00" + n.Code
}

func (u UnitIdentity) Valid() bool {
	n := u.normalized()
	return n.System != "" && n.Code != ""
}

func (u UnitIdentity) Equal(other UnitIdentity) bool {
	a, b := u.normalized(), other.normalized()
	return a.System == b.System && a.Code == b.Code
}

// UnitDimension is a dimensional-analysis vector. The named exponents cover
// the dimensions supported by the initial approved conversion library. A
// conversion is valid only when every exponent matches the target.
type UnitDimension struct {
	Length      int `json:"length,omitempty"`
	Mass        int `json:"mass,omitempty"`
	Time        int `json:"time,omitempty"`
	Temperature int `json:"temperature,omitempty"`
	Amount      int `json:"amount,omitempty"`
}

func (d UnitDimension) Equal(other UnitDimension) bool {
	return d == other
}

func (d UnitDimension) Zero() bool { return d == (UnitDimension{}) }

type UnitConversionKind string

const (
	UnitConversionIdentity UnitConversionKind = "IDENTITY"
	UnitConversionLinear   UnitConversionKind = "LINEAR"
	UnitConversionAffine   UnitConversionKind = "AFFINE"
)

// UnitRuleReference is the only unit rule shape accepted from an authoring
// document. The compiler resolves it against the immutable registry below;
// callers cannot supply executable coefficients or dimensions.
type UnitRuleReference struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ApprovedUnitPolicy struct {
	PolicyID string
	Version  string
	Target   UnitIdentity
	Rules    []UnitRuleReference
}

// UnitNormalization is the compiler-owned, resolved policy carried into the
// backend-neutral semantic and physical plans.
type UnitNormalization struct {
	Target    UnitIdentity
	Dimension UnitDimension
	Rules     []UnitConversionRule
}

// UnitConversionRule is an immutable, pinned interpretation rule. Scale and
// offset are applied as output = input*scale + offset. Identity and linear
// rules require zero offset; affine rules may carry a non-zero offset.
type UnitConversionRule struct {
	ID        string             `json:"id"`
	Version   string             `json:"version"`
	Kind      UnitConversionKind `json:"kind"`
	Source    UnitIdentity       `json:"source"`
	Target    UnitIdentity       `json:"target"`
	Dimension UnitDimension      `json:"dimension"`
	Scale     float64            `json:"scale"`
	Offset    float64            `json:"offset"`
}

// ValidateUnitRuleReference validates the client-supplied part of a policy.
// Coefficients and dimensions are resolved from the compiler-owned registry;
// callers cannot make an arbitrary rule executable by choosing its numbers.
func ValidateUnitRuleReference(rule UnitRuleReference) error {
	if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Version) == "" {
		return fmt.Errorf("unit conversion rule id and version are required")
	}
	return nil
}

func ValidateUnitPolicyReference(policyID, version string) error {
	if strings.TrimSpace(policyID) == "" || strings.TrimSpace(version) == "" {
		return fmt.Errorf("unit normalization policyId and version are required")
	}
	if _, ok := approvedUnitPolicies[strings.TrimSpace(policyID)+"\x00"+strings.TrimSpace(version)]; !ok {
		return fmt.Errorf("%w: policy %q@%s is not approved", ErrUnitIdentityUnknown, policyID, version)
	}
	return nil
}

func ResolveApprovedUnitPolicy(policyID, version string) (ApprovedUnitPolicy, error) {
	if err := ValidateUnitPolicyReference(policyID, version); err != nil {
		return ApprovedUnitPolicy{}, err
	}
	policy := approvedUnitPolicies[strings.TrimSpace(policyID)+"\x00"+strings.TrimSpace(version)]
	policy.Rules = append([]UnitRuleReference(nil), policy.Rules...)
	return policy, nil
}

// ResolveApprovedUnitRules replaces rule references with immutable registry
// entries. Identity is the only target-relative rule: it derives source and
// target from the policy target and still pins a named registry version.
func ResolveApprovedUnitRules(references []UnitRuleReference, target UnitIdentity) (UnitDimension, []UnitConversionRule, error) {
	if !target.Valid() {
		return UnitDimension{}, nil, fmt.Errorf("%w: target system and code are required", ErrUnitIdentityUnknown)
	}
	dimension, ok := approvedUnitDimensions[target.normalizedKey()]
	if !ok {
		return UnitDimension{}, nil, fmt.Errorf("%w: target %s/%s is not approved", ErrUnitIdentityUnknown, target.System, target.Code)
	}
	resolved := make([]UnitConversionRule, 0, len(references))
	seen := make(map[string]struct{}, len(references))
	for index, reference := range references {
		if err := ValidateUnitRuleReference(reference); err != nil {
			return UnitDimension{}, nil, fmt.Errorf("rules[%d]: %w", index, err)
		}
		key := strings.TrimSpace(reference.ID) + "\x00" + strings.TrimSpace(reference.Version)
		if _, ok := seen[key]; ok {
			return UnitDimension{}, nil, fmt.Errorf("rules[%d] duplicates rule reference", index)
		}
		seen[key] = struct{}{}
		var rule UnitConversionRule
		if key == "identity-v1\x001" {
			rule = UnitConversionRule{ID: reference.ID, Version: reference.Version, Kind: UnitConversionIdentity, Source: target, Target: target, Dimension: dimension, Scale: 1}
		} else {
			var found bool
			rule, found = approvedUnitRules[key]
			if !found {
				return UnitDimension{}, nil, fmt.Errorf("%w: conversion rule %q@%s is not approved", ErrUnitIdentityUnknown, reference.ID, reference.Version)
			}
		}
		if err := rule.Validate(target, dimension); err != nil {
			return UnitDimension{}, nil, fmt.Errorf("rules[%d]: %w", index, err)
		}
		resolved = append(resolved, rule)
	}
	return dimension, resolved, nil
}

var approvedUnitDimensions = map[string]UnitDimension{
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}.normalizedKey():     {Mass: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "g"}.normalizedKey():      {Mass: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}.normalizedKey():     {Mass: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "m"}.normalizedKey():      {Length: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}.normalizedKey():     {Length: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "Cel"}.normalizedKey():    {Temperature: 1},
	UnitIdentity{System: "http://unitsofmeasure.org", Code: "[degF]"}.normalizedKey(): {Temperature: 1},
}

var approvedUnitRules = map[string]UnitConversionRule{
	"ucum:g-to-mg\x001":  {ID: "ucum:g-to-mg", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "g"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}, Dimension: UnitDimension{Mass: 1}, Scale: 1000},
	"ucum:kg-to-mg\x001": {ID: "ucum:kg-to-mg", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}, Dimension: UnitDimension{Mass: 1}, Scale: 1_000_000},
	"ucum:kg-to-g\x001":  {ID: "ucum:kg-to-g", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "g"}, Dimension: UnitDimension{Mass: 1}, Scale: 1000},
	"ucum:mg-to-g\x001":  {ID: "ucum:mg-to-g", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "g"}, Dimension: UnitDimension{Mass: 1}, Scale: 0.001},
	"ucum:g-to-kg\x001":  {ID: "ucum:g-to-kg", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "g"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, Dimension: UnitDimension{Mass: 1}, Scale: 0.001},
	"ucum:mg-to-kg\x001": {ID: "ucum:mg-to-kg", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "mg"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, Dimension: UnitDimension{Mass: 1}, Scale: 0.000001},
	"ucum:m-to-cm\x001":  {ID: "ucum:m-to-cm", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "m"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, Dimension: UnitDimension{Length: 1}, Scale: 100},
	"ucum:cm-to-m\x001":  {ID: "ucum:cm-to-m", Version: "1", Kind: UnitConversionLinear, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "m"}, Dimension: UnitDimension{Length: 1}, Scale: 0.01},
	"ucum:c-to-f\x001":   {ID: "ucum:c-to-f", Version: "1", Kind: UnitConversionAffine, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "Cel"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "[degF]"}, Dimension: UnitDimension{Temperature: 1}, Scale: 1.8, Offset: 32},
	"ucum:f-to-c\x001":   {ID: "ucum:f-to-c", Version: "1", Kind: UnitConversionAffine, Source: UnitIdentity{System: "http://unitsofmeasure.org", Code: "[degF]"}, Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "Cel"}, Dimension: UnitDimension{Temperature: 1}, Scale: 5.0 / 9.0, Offset: -160.0 / 9.0},
}

var approvedUnitPolicies = map[string]ApprovedUnitPolicy{
	"to-centimeters\x001": {PolicyID: "to-centimeters", Version: "1", Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, Rules: []UnitRuleReference{{ID: "ucum:m-to-cm", Version: "1"}, {ID: "identity-v1", Version: "1"}}},
	"to-kilograms\x001":   {PolicyID: "to-kilograms", Version: "1", Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, Rules: []UnitRuleReference{{ID: "ucum:g-to-kg", Version: "1"}, {ID: "ucum:mg-to-kg", Version: "1"}, {ID: "identity-v1", Version: "1"}}},
	"to-celsius\x001":     {PolicyID: "to-celsius", Version: "1", Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "Cel"}, Rules: []UnitRuleReference{{ID: "ucum:f-to-c", Version: "1"}, {ID: "identity-v1", Version: "1"}}},
	"to-fahrenheit\x001":  {PolicyID: "to-fahrenheit", Version: "1", Target: UnitIdentity{System: "http://unitsofmeasure.org", Code: "[degF]"}, Rules: []UnitRuleReference{{ID: "ucum:c-to-f", Version: "1"}, {ID: "identity-v1", Version: "1"}}},
}

func (r UnitConversionRule) Validate(policyTarget UnitIdentity, policyDimension UnitDimension) error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Version) == "" {
		return fmt.Errorf("unit conversion rule id and version are required")
	}
	if !r.Source.Valid() || !r.Target.Valid() {
		return fmt.Errorf("unit conversion rule %q requires source and target system/code", r.ID)
	}
	if !r.Dimension.Equal(policyDimension) {
		return fmt.Errorf("%w: conversion rule %q has incompatible dimension", ErrUnitDimensionIncompatible, r.ID)
	}
	if !r.Target.Equal(policyTarget) {
		return fmt.Errorf("unit conversion rule %q targets %s/%s, want %s/%s", r.ID, r.Target.System, r.Target.Code, policyTarget.System, policyTarget.Code)
	}
	if !isFinite(r.Scale) || !isFinite(r.Offset) || r.Scale == 0 {
		return fmt.Errorf("unit conversion rule %q has invalid scale or offset", r.ID)
	}
	switch r.Kind {
	case UnitConversionIdentity:
		if !r.Source.Equal(r.Target) || r.Scale != 1 || r.Offset != 0 {
			return fmt.Errorf("identity unit conversion rule %q must have equal units, scale 1, and offset 0", r.ID)
		}
	case UnitConversionLinear:
		if r.Offset != 0 {
			return fmt.Errorf("linear unit conversion rule %q must have offset 0", r.ID)
		}
	case UnitConversionAffine:
	default:
		return fmt.Errorf("unit conversion rule %q has unsupported kind %q", r.ID, r.Kind)
	}
	return nil
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
