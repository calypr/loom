package execution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
)

func compiledCategoryScan(max int) compiler.CompiledCategoryScanQuery {
	return compiler.CompiledCategoryScanQuery{
		Query: "scan", BindVars: map[string]any{"column": "category"}, PresentColumn: "present", ValueColumn: "value",
		Proof: compiler.CategoryScanProof{Version: 1, Output: "output", Column: "category", MaxValues: max, Fingerprint: "proof"},
	}
}

func TestScanCategoriesCompiledReportsCompleteAtBoundAndOverflowAtBoundPlusOne(t *testing.T) {
	for _, test := range []struct {
		rows, wantValues   int
		complete, overflow bool
	}{
		{256, 256, true, false},
		{257, 256, false, true},
	} {
		t.Run(fmt.Sprintf("rows_%d", test.rows), func(t *testing.T) {
			engine := &Engine{queryRows: func(_ context.Context, query string, _ int, binds map[string]any, visit func(map[string]any) error) error {
				if query != "scan" || binds["column"] != "category" {
					t.Fatalf("wrong invocation: %q %#v", query, binds)
				}
				for index := 0; index < test.rows; index++ {
					if err := visit(map[string]any{"present": true, "value": index}); err != nil {
						return err
					}
				}
				return nil
			}}
			result, err := engine.ScanCategoriesCompiled(context.Background(), compiledCategoryScan(256))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Values) != test.wantValues || result.Complete != test.complete || result.Overflow != test.overflow || result.Proof.Fingerprint != "proof" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestScanCategoriesCompiledPreservesOrderedMissingNullAndFalseyValues(t *testing.T) {
	rows := []map[string]any{
		{"present": false, "value": nil},
		{"present": true, "value": nil},
		{"present": true, "value": false},
		{"present": true, "value": float64(0)},
		{"present": true, "value": ""},
	}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.ScanCategoriesCompiled(context.Background(), compiledCategoryScan(256))
	if err != nil {
		t.Fatal(err)
	}
	want := []CategoryValue{{false, nil}, {true, nil}, {true, false}, {true, float64(0)}, {true, ""}}
	if !reflect.DeepEqual(result.Values, want) || !result.Complete {
		t.Fatalf("values = %#v", result.Values)
	}
}

func TestScanCategoriesCompiledHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{queryRows: func(ctx context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		cancel()
		return visit(map[string]any{"present": true, "value": "unreachable"})
	}}
	_, err := engine.ScanCategoriesCompiled(ctx, compiledCategoryScan(256))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestScanCategoriesReturnsTypedCompilerRefusalForUnsupportedColumn(t *testing.T) {
	resolved := Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
		Name: "output",
		OutputSchema: []lower.CompiledOutputColumn{{
			Name: "structured", Kind: string(expression.KindObject), Cardinality: string(expression.OptionalOne),
		}},
	}}}}
	_, err := (&Engine{}).ScanCategories(context.Background(), resolved, CategoryScanRequest{Output: "output", Column: "structured", MaxValues: 256})
	code, ok := compiler.CategoryScanRefusalCodeOf(err)
	if !ok || code != compiler.CategoryScanColumnUnsupported {
		t.Fatalf("refusal = %q/%t, error = %v", code, ok, err)
	}
}
