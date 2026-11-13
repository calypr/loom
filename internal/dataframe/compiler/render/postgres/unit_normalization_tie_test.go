package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func sql21ScratchAmbiguousRules() []unit.UnitConversionRule {
	ucum := "http://unitsofmeasure.org"
	target := unit.UnitIdentity{System: ucum, Code: "cm"}
	dimension := unit.UnitDimension{Length: 1}
	source := unit.UnitIdentity{System: ucum, Code: "inch"}
	return []unit.UnitConversionRule{
		{ID: "scratch:pair-a", Version: "1", Kind: unit.UnitConversionLinear,
			Source: source, Target: target, Dimension: dimension, Scale: 2.54, Offset: 0},
		{ID: "scratch:pair-b", Version: "1", Kind: unit.UnitConversionLinear,
			Source: source, Target: target, Dimension: dimension, Scale: 2.541, Offset: 0},
	}
}

func TestSQL21ScratchTieCellCraftedFixture(t *testing.T) {
	rules := sql21ScratchAmbiguousRules()
	for index, rule := range rules {
		if err := rule.Validate(rules[0].Target, rules[0].Dimension); err != nil {
			t.Fatalf("crafted rule %d must individually validate: %v", index, err)
		}
	}
	err := unitRulesPairAmbiguity(rules)
	if err == nil {
		t.Fatal("crafted ambiguous pair accepted: the tie cell must refuse before any SQL exists")
	}
	for _, want := range []string{"two rules for source http://unitsofmeasure.org/inch", "at most one rule per exact source pair", "bind-array order"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("tie refusal %q does not name %q", err.Error(), want)
		}
	}
	_, _, legal := sql21CentimeterFacts(t)
	if err := unitRulesPairAmbiguity(legal); err != nil {
		t.Fatalf("legal rule set refused: %v", err)
	}
	if err := unitRulesPairAmbiguity(rules[:1]); err != nil {
		t.Fatalf("single crafted rule refused: %v", err)
	}
}

func TestSQL21ScratchTieCellRendererReachabilityBoundary(t *testing.T) {
	rules := sql21ScratchAmbiguousRules()
	projection := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		x.UnitNormalization.Rules = rules
	})
	err := sql21ContractError(t, projection, nil)
	if !errors.Is(err, unit.ErrUnitIdentityUnknown) {
		t.Fatalf("crafted ambiguous pair through the renderer = %v, want the registry gate (ErrUnitIdentityUnknown) to answer first", err)
	}
	if !strings.Contains(err.Error(), "is not approved") {
		t.Fatalf("registry gate text drifted: %v", err)
	}
}
