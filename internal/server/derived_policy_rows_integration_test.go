package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type derivedArithmeticCase struct {
	ID                    string                      `json:"id"`
	Operation             recipe.DerivedOperation     `json:"operation"`
	MissingPolicy         recipe.MissingInputPolicy   `json:"missingPolicy"`
	ZeroPolicy            recipe.DivisionByZeroPolicy `json:"zeroPolicy"`
	SourceState           string                      `json:"sourceState"`
	SourceValue           int64                       `json:"sourceValue"`
	Literal               int64                       `json:"literal"`
	SourceOnRight         bool                        `json:"sourceOnRight"`
	GuardOrderConformance bool                        `json:"guardOrderConformance"`
}
type derivedArithmeticExpected struct {
	Kind        string   `json:"kind"`
	Value       *float64 `json:"value"`
	ErrorMarker string   `json:"errorMarker"`
}
type derivedArithmeticObservation struct {
	Rows         int    `json:"rows"`
	Value        any    `json:"value"`
	ErrorNumber  int    `json:"errorNumber"`
	ErrorMessage string `json:"errorMessage"`
}

func derivedArithmeticExpectedFor(c derivedArithmeticCase) derivedArithmeticExpected {
	if c.SourceState != "present" {
		if c.MissingPolicy == recipe.MissingInputError {
			return derivedArithmeticExpected{Kind: "error", ErrorMarker: "DERIVED_MISSING_INPUT"}
		}
		return derivedArithmeticExpected{Kind: "null"}
	}
	left, right := float64(c.SourceValue), float64(c.Literal)
	if c.SourceOnRight {
		left, right = right, left
	}
	if c.Operation == recipe.DerivedDivide && right == 0 {
		if c.ZeroPolicy == recipe.DivisionByZeroError {
			return derivedArithmeticExpected{Kind: "error", ErrorMarker: "DERIVED_DIVISION_BY_ZERO"}
		}
		return derivedArithmeticExpected{Kind: "null"}
	}
	var answer float64
	switch c.Operation {
	case recipe.DerivedAdd:
		answer = left + right
	case recipe.DerivedSubtract:
		answer = left - right
	case recipe.DerivedMultiply:
		answer = left * right
	case recipe.DerivedDivide:
		answer = left / right
	}
	return derivedArithmeticExpected{Kind: "value", Value: &answer}
}
func derivedArithmeticCases() []derivedArithmeticCase {
	sourceValues := []struct {
		state string
		value int64
	}{{"missing", 0}, {"null", 0}, {"present", -2}, {"present", 0}, {"present", 2}}
	cases := []derivedArithmeticCase{}
	for _, sourceOnRight := range []bool{false, true} {
		for _, source := range sourceValues {
			for _, literal := range []int64{-2, 0, 2, 4} {
				for _, operation := range []recipe.DerivedOperation{recipe.DerivedAdd, recipe.DerivedSubtract, recipe.DerivedMultiply, recipe.DerivedDivide} {
					for _, missing := range []recipe.MissingInputPolicy{recipe.MissingInputPropagateNull, recipe.MissingInputError} {
						zeros := []recipe.DivisionByZeroPolicy{""}
						if operation == recipe.DerivedDivide {
							zeros = []recipe.DivisionByZeroPolicy{recipe.DivisionByZeroNull, recipe.DivisionByZeroError}
						}
						for _, zero := range zeros {
							cases = append(cases, derivedArithmeticCase{
								ID:        fmt.Sprintf("%s_%d_literal_%d_reverse_%t_%s_%s_%s", source.state, source.value, literal, sourceOnRight, operation, missing, zero),
								Operation: operation, MissingPolicy: missing, ZeroPolicy: zero, SourceState: source.state, SourceValue: source.value, Literal: literal, SourceOnRight: sourceOnRight,
								GuardOrderConformance: source.state != "present" && !sourceOnRight && literal == 0 && operation == recipe.DerivedDivide,
							})
						}
					}
				}
			}
		}
	}
	return cases
}
func derivedArithmeticBundle(c derivedArithmeticCase) recipe.Bundle {
	column := recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "amount"}
	literal := recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &c.Literal}}
	left, right := column, literal
	if c.SourceOnRight {
		left, right = right, left
	}
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "derived-policy", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "values", RootResourceType: "Observation", RowGrain: "observation",
		Fields:         []recipe.Field{{Name: "amount", ColumnID: "amount", Expr: recipe.Expression{Select: "root.valueInteger"}}},
		DerivedColumns: []recipe.DerivedColumn{{ConstructionID: "calculate", Name: "calculated", Label: "Calculated", Operation: c.Operation, Left: left, Right: right, MissingInputPolicy: c.MissingPolicy, DivisionByZeroPolicy: c.ZeroPolicy}},
	}}}
}
func failDerivedArithmetic(t *testing.T, c derivedArithmeticCase, expected derivedArithmeticExpected, actual derivedArithmeticObservation) {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Case     derivedArithmeticCase        `json:"case"`
		Expected derivedArithmeticExpected    `json:"expected"`
		Actual   derivedArithmeticObservation `json:"actual"`
	}{c, expected, actual})
	if err != nil {
		t.Fatalf("DERIVED_ARITHMETIC_SERIALIZATION: %v", err)
	}
	marker := "DERIVED_ARITHMETIC_FAILURE_JSON"
	if c.GuardOrderConformance {
		marker = "DERIVED_ARITHMETIC_CONFORMANCE_JSON"
	}
	t.Fatalf("%s %s", marker, encoded)
}
func TestDerivedArithmeticPoliciesAgainstArango(t *testing.T) {
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("requires owned LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := "loom_derived_policy_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	graph, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		graph.Close(context.Background())
		request, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+database, nil)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Errorf("owned derived database cleanup: %v", err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("owned derived database cleanup HTTP%d", response.StatusCode)
		}
	})
	if err = graph.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	engine, err := execution.New(execution.Config{
		CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, QueryRows: func(ctx context.Context, command command.CompiledCommand, batch int, visit execution.RowVisitor) error {
			query, binds, deriveErr := command.AQLParameters()
			if deriveErr != nil {
				return deriveErr
			}

			return graph.QueryRows(ctx, query, batch, binds, storearango.RowVisitor(visit))
		}})
	if err != nil {
		t.Fatal(err)
	}
	bindings := recipe.RuntimeBindings{Project: "derived-project", DatasetGeneration: "derived-generation", AuthScopeMode: authscope.ReadScopeUnrestricted, OutputNames: []string{"values"}}
	cases := derivedArithmeticCases()
	if len(cases) != 400 {
		t.Fatalf("derived matrix authored%d inputs, want400", len(cases))
	}
	summary := struct {
		CaseCount             int `json:"caseCount"`
		Values                int `json:"values"`
		Nulls                 int `json:"nulls"`
		MissingErrors         int `json:"missingErrors"`
		ZeroErrors            int `json:"zeroErrors"`
		GuardOrderConformance int `json:"guardOrderConformance"`
	}{}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Fatalf("duplicate derived input %s", c.ID)
		}
		seen[c.ID] = true
		t.Run(c.ID, func(t *testing.T) {
			expected := derivedArithmeticExpectedFor(c)
			payload := map[string]any{"id": "operand", "resourceType": "Observation", "status": "final"}
			if c.SourceState == "present" {
				payload["valueInteger"] = c.SourceValue
			} else if c.SourceState == "null" {
				payload["valueInteger"] = nil
			}
			raw, _ := json.Marshal(map[string]any{"_key": "operand", "id": "operand", "resourceType": "Observation", "project": "derived-project", "project_id": "derived-project", "dataset_generation": "derived-generation", "payload": payload})
			if err := graph.InsertBatchRaw(ctx, "Observation", []json.RawMessage{raw}, true, "document"); err != nil {
				t.Fatal(err)
			}
			compiled, err := engine.CompileResolvedBundle(ctx, derivedArithmeticBundle(c), bindings)
			if err != nil {
				t.Fatalf("DERIVED_ARITHMETIC_COMPILE %s: %v", c.ID, err)
			}
			kind := string(expression.KindInteger)
			if c.Operation == recipe.DerivedDivide {
				kind = string(expression.KindDecimal)
			}
			nullable := c.MissingPolicy == recipe.MissingInputPropagateNull || c.Operation == recipe.DerivedDivide && c.ZeroPolicy == recipe.DivisionByZeroNull
			cardinality := string(expression.RequiredOne)
			if nullable {
				cardinality = string(expression.OptionalOne)
			}
			matches := 0
			for _, column := range compiled.Compiled.Outputs[0].OutputSchema {
				if column.Name == "calculated" {
					matches++
					if column.Kind != kind || column.Nullable != nullable || column.Cardinality != cardinality {
						t.Fatalf("DERIVED_ARITHMETIC_SCHEMA %s expected=%s/%s/%t actual=%#v", c.ID, kind, cardinality, nullable, column)
					}
				}
			}
			if matches != 1 {
				t.Fatalf("DERIVED_ARITHMETIC_SCHEMA %s columnMatches=%d", c.ID, matches)
			}
			rows, err := engine.Run(ctx, compiled)
			var backend shared.ArangoError
			actual := derivedArithmeticObservation{}
			if err != nil {
				if !errors.As(err, &backend) || backend.ErrorNum != shared.ErrQueryUserAssert {
					t.Fatalf("DERIVED_ARITHMETIC_QUERY %s: %v", c.ID, err)
				}
				actual.ErrorNumber = backend.ErrorNum
				actual.ErrorMessage = backend.ErrorMessage
				if expected.Kind != "error" || !strings.Contains(backend.ErrorMessage, expected.ErrorMarker) {
					failDerivedArithmetic(t, c, expected, actual)
				}
				if expected.ErrorMarker == "DERIVED_MISSING_INPUT" {
					summary.MissingErrors++
				} else {
					summary.ZeroErrors++
				}
			} else {
				actual.Rows = len(rows["values"])
				if actual.Rows == 1 {
					actual.Value = rows["values"][0]["calculated"]
				}
				if len(rows) != 1 || actual.Rows != 1 || expected.Kind == "error" {
					failDerivedArithmetic(t, c, expected, actual)
				}
				if expected.Kind == "null" {
					if actual.Value != nil {
						failDerivedArithmetic(t, c, expected, actual)
					}
					summary.Nulls++
				} else {
					number, e := strconv.ParseFloat(fmt.Sprint(actual.Value), 64)
					if e != nil || number != *expected.Value {
						failDerivedArithmetic(t, c, expected, actual)
					}
					summary.Values++
				}
			}
			summary.CaseCount++
			if c.GuardOrderConformance {
				summary.GuardOrderConformance++
			}
		})
	}
	if !t.Failed() {
		encoded, _ := json.Marshal(summary)
		t.Logf("DERIVED_ARITHMETIC_SUMMARY_JSON %s", encoded)
	}
}
