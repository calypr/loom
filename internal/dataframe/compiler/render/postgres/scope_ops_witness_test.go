package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func scopeBlockOperations() []ir.PhysicalOperation {
	return []ir.PhysicalOperation{
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
			Right:    &ir.PhysicalValue{BindKey: "project"},
		}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "root", Path: []string{"dataset_generation"}},
			Right:    &ir.PhysicalValue{BindKey: "dataset_generation"},
		}}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
			Variable: "root_auth_allowed",
			Operator: "AUTH_RESOURCE_PATH_ALLOWED",
			Inputs: []ir.PhysicalValue{
				{Variable: "root", Path: []string{"auth_resource_path"}},
				{BindKey: "auth_resource_paths"},
				{BindKey: "auth_resource_paths_unrestricted"},
			},
		}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "root_auth_allowed"},
			Right:    &ir.PhysicalValue{BindKey: "scope_allowed"},
		}}},
	}
}

func planWithScopeBlock(mutate func(ops []ir.PhysicalOperation)) ir.PhysicalPlan {
	plan := basePlan()
	block := scopeBlockOperations()
	if mutate != nil {
		mutate(block)
	}
	operations := []ir.PhysicalOperation{plan.Operations[0]}
	operations = append(operations, block...)
	return ir.PhysicalPlan{
		Version:    plan.Version,
		Engine:     plan.Engine,
		BindVars:   plan.BindVars,
		Operations: append(operations, plan.Operations[1]),
	}
}

func TestCanonicalScopeBlockIsVerifiedThenDiscarded(t *testing.T) {
	rendered := renderOK(t, planWithScopeBlock(nil))
	for _, forbidden := range []string{"root_auth_allowed", "@", "auth_resource_paths ="} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("plan-authored scope text %q survived into SQL: %s", forbidden, rendered.SQL)
		}
	}
	for _, required := range []string{"project", "dataset_generation", "auth_resource_path", "resource_type"} {
		if !strings.Contains(rendered.SQL, required) {
			t.Fatalf("renderer-owned scope predicate %q missing from SQL: %s", required, rendered.SQL)
		}
	}
	withBlock, withoutBlock := rendered.SQL, renderOK(t, basePlan()).SQL
	if withBlock != withoutBlock {
		t.Fatalf("dropping the canonical block changed the statement:\nwith    %s\nwithout %s", withBlock, withoutBlock)
	}
}

func TestCanonicalScopeNearMissesAreScopeDefects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(ops []ir.PhysicalOperation)
	}{
		{"project filter compares the wrong field", func(ops []ir.PhysicalOperation) {
			ops[0].Filter.Predicate.Left = ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}
		}},
		{"project filter compares a nested path", func(ops []ir.PhysicalOperation) {
			ops[0].Filter.Predicate.Left = ir.PhysicalValue{Variable: "root", Path: []string{"payload", "project"}}
		}},
		{"generation filter compares the wrong field", func(ops []ir.PhysicalOperation) {
			ops[1].Filter.Predicate.Left = ir.PhysicalValue{Variable: "root", Path: []string{"generation"}}
		}},
		{"scope_allowed equality names an unestablished variable", func(ops []ir.PhysicalOperation) {
			ops[3].Filter.Predicate.Left = ir.PhysicalValue{Variable: "rogue_auth_allowed"}
		}},
		{"scope_allowed equality carries a path", func(ops []ir.PhysicalOperation) {
			ops[3].Filter.Predicate.Left = ir.PhysicalValue{Variable: "root_auth_allowed", Path: []string{"allowed"}}
		}},
		{"scope_allowed equality binds the wrong side", func(ops []ir.PhysicalOperation) {
			ops[3].Filter.Predicate.Right = &ir.PhysicalValue{Variable: "root", Path: []string{"project"}}
		}},
		{"auth LET drops the unrestricted input", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Inputs = ops[2].DerivedLet.Inputs[:2]
		}},
		{"auth LET reads the wrong row field", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Inputs[0] = ir.PhysicalValue{Variable: "root", Path: []string{"id"}}
		}},
		{"auth LET reads a nested field", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Inputs[0] = ir.PhysicalValue{Variable: "root", Path: []string{"payload", "auth_resource_path"}}
		}},
		{"auth LET reads an unregistered row", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Inputs[0] = ir.PhysicalValue{Variable: "ghost", Path: []string{"auth_resource_path"}}
		}},
		{"auth LET shadows a scope row variable", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Variable = "root"
		}},
		{"auth LET carries an unknown input", func(ops []ir.PhysicalOperation) {
			ops[2].DerivedLet.Inputs = append(ops[2].DerivedLet.Inputs,
				ir.PhysicalValue{BindKey: "auth_resource_paths", Path: []string{"extra"}})
		}},
	}
	earlyReject := map[string]bool{
		"scope_allowed equality names an unestablished variable": true,
		"auth LET reads an unregistered row":                     true,
		"auth LET shadows a scope row variable":                  true,
		"auth LET carries an unknown input":                      true,
	}
	renderSurface := map[string]bool{
		"scope_allowed equality binds the wrong side": true,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RenderPhysicalPlan(planWithScopeBlock(tc.mutate), mustBinding(t))
			if err == nil {
				t.Fatal("near-miss scope operation must never render")
			}
			switch {
			case earlyReject[tc.name]:
				if errors.Is(err, ErrUnsupportedPostgresPlan) {
					t.Fatalf("near-miss must be refused by the IR scope validator, not the unsupported surface: %v", err)
				}
			case renderSurface[tc.name]:
				if !errors.Is(err, ErrUnsupportedPostgresPlan) {
					t.Fatalf("non-scope predicate shape must die on the closed render surface: %v", err)
				}
			default:
				if !errors.Is(err, ErrInvalidPostgresPlanScope) {
					t.Fatalf("near-miss must fail closed with the scope-defect sentinel, got %v (%T)", err, err)
				}
			}
		})
	}
}

func TestAuthLETDuplicateRegistrationIsRejected(t *testing.T) {
	plan := planWithScopeBlock(func(ops []ir.PhysicalOperation) {})
	duplicate := *plan.Operations[3].DerivedLet
	operations := append([]ir.PhysicalOperation{}, plan.Operations...)
	operations = append(operations[:5], append([]ir.PhysicalOperation{{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &duplicate}}, operations[5:]...)...)
	plan.Operations = operations
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil || errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("redefining an established auth variable must fail closed, got %v", err)
	}
}

func TestNonCanonicalFilterIsNotConsumedByTheScopeSkip(t *testing.T) {
	plan := planWithScopeBlock(nil)
	plan.BindVars["status_value"] = "final"
	custom := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
		Operator: "EQUALS",
		Left:     ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
		Right:    &ir.PhysicalValue{BindKey: "status_value"},
	}}}
	operations := []ir.PhysicalOperation{plan.Operations[0], custom}
	operations = append(operations, plan.Operations[1:]...)
	plan.Operations = operations
	rendered := renderOK(t, plan)
	var sawPathArg, sawValueArg bool
	for _, arg := range rendered.Args {
		text := fmt.Sprintf("%v", arg.Value)
		if raw, ok := arg.Value.(json.RawMessage); ok {
			text = string(raw)
		}
		if text == "{status}" {
			sawPathArg = true
		}
		if strings.Contains(text, "final") {
			sawValueArg = true
		}
	}
	if !sawPathArg || !sawValueArg {
		t.Fatalf("non-canonical FILTER was consumed by the scope skip: sql=%s args=%v", rendered.SQL, rendered.Args)
	}
}
