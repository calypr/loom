package ir

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPhysicalCorrelationKeyMatchValidatesOnlyBoundKeyIdentity(t *testing.T) {
	selector := func(path string) spec.Selector {
		t.Helper()
		parsed, err := spec.ParseSelector(path)
		if err != nil {
			t.Fatalf("parse selector %q: %v", path, err)
		}
		return parsed
	}
	match := PhysicalCorrelationKeyMatch{
		Source:       PhysicalValue{Variable: "observation", Path: []string{"payload"}},
		ResourceType: "Observation", KeyResource: "Coding",
		KeySelector: selector("category[].coding[]"), SystemSelector: selector("system"), CodeSelector: selector("code"),
		SystemBindKey: "selected_system", CodeBindKey: "selected_code",
	}
	defined := map[string]bool{"observation": true}
	bindVars := map[string]any{"selected_system": "urn:study:A", "selected_code": "shared"}
	predicate := PhysicalPredicate{Operator: "EQUALS", CorrelationKeyMatch: &match}
	if err := validatePhysicalPredicate(predicate, defined, bindVars); err != nil {
		t.Fatalf("valid key-match predicate rejected: %v", err)
	}

	for _, test := range []struct {
		name      string
		predicate PhysicalPredicate
		bindVars  map[string]any
		want      string
	}{
		{name: "unsupported operator", predicate: PhysicalPredicate{Operator: "IN", CorrelationKeyMatch: &match}, bindVars: bindVars, want: "operator"},
		{name: "ordinary value mixed in", predicate: PhysicalPredicate{Operator: "EQUALS", Right: &PhysicalValue{BindKey: "selected_code"}, CorrelationKeyMatch: &match}, bindVars: bindVars, want: "other predicate values"},
		{name: "missing code bind", predicate: predicate, bindVars: map[string]any{"selected_system": "urn:study:A"}, want: "not defined"},
		{name: "empty system bind", predicate: predicate, bindVars: map[string]any{"selected_system": " ", "selected_code": "shared"}, want: "non-empty string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePhysicalPredicate(test.predicate, defined, test.bindVars)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}
