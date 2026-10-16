package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPopulationMappingCompiledDeduplicatesMembersAndRows(t *testing.T) {
	engine := &Engine{
		batchSize: 1000,
		queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{
				{"member": "file-001", "identity": []any{"specimen-001"}},
				{"member": "file-002", "identity": []any{"specimen-001"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}
	reader := PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		for _, id := range []string{"file-001", "file-002", "file-002", "file-004"} {
			if err := visit(id); err != nil {
				return err
			}
		}
		return nil
	})
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Query:        "RETURN witnesses",
		MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{MaxUnmapped: 10}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 1 {
		t.Fatalf("population mapping result = %#v", result)
	}
	if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "file-004" {
		t.Fatalf("unmapped member IDs = %#v", result.UnmappedMemberIDs)
	}
}

func TestPopulationMappingCompiledReturnsIncompleteWithoutCountsOnLimit(t *testing.T) {
	engine := &Engine{
		batchSize: 1000,
		queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"member": "file-001", "identity": []any{"specimen-001"}})
		},
	}
	reader := PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		return visit("file-001")
	})
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Query: "RETURN witnesses", MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{MaxWitnessRows: 0}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete {
		t.Fatalf("default witness limit unexpectedly incomplete: %#v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	incomplete, err := engine.PopulationMappingCompiled(ctx, compiler.CompiledPopulationMappingQuery{
		Query: "RETURN witnesses", MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Status != PopulationMappingIncomplete || incomplete.SelectedCount != 0 || incomplete.MappedCount != 0 || incomplete.UnmappedCount != 0 || incomplete.EmittedRows != 0 {
		t.Fatalf("incomplete result exposed exact counts: %#v", incomplete)
	}
}

func TestPopulationWitnessIdentityRejectsWrongShape(t *testing.T) {
	_, err := populationWitnessIdentity(compiler.CompiledPopulationMappingQuery{
		IdentityPartsColumn: "identity", RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, map[string]any{"identity": "not-an-array"})
	if err == nil {
		t.Fatal("expected invalid identity shape")
	}
}

func TestPopulationWitnessIdentityCanonicalizesExplicitScalars(t *testing.T) {
	query := compiler.CompiledPopulationMappingQuery{ExplicitIdentityColumn: "identity"}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "row-1", want: `explicit:string:"row-1"`},
		{name: "number", value: float64(7), want: "explicit:number:7"},
		{name: "number equivalent", value: json.Number("7.0"), want: "explicit:number:7"},
		{name: "fraction", value: float64(0.5), want: "explicit:number:1/2"},
		{name: "fraction decimal equivalent", value: json.Number("0.50"), want: "explicit:number:1/2"},
		{name: "fraction exponent equivalent", value: json.Number("5e-1"), want: "explicit:number:1/2"},
		{name: "boolean", value: true, want: "explicit:bool:true"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := populationWitnessIdentity(query, map[string]any{"identity": test.value})
			if err != nil || got != test.want {
				t.Fatalf("identity = %q, err = %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestPopulationWitnessIdentityRejectsNullAndStructuredExplicitValues(t *testing.T) {
	query := compiler.CompiledPopulationMappingQuery{ExplicitIdentityColumn: "identity"}
	for _, value := range []any{nil, []any{"row-1"}, map[string]any{"id": "row-1"}} {
		if _, err := populationWitnessIdentity(query, map[string]any{"identity": value}); err == nil {
			t.Fatalf("identity value %#v unexpectedly accepted", value)
		}
	}
}

func TestPopulationMappingCompiledDeduplicatesCanonicalExplicitNumericIdentities(t *testing.T) {
	engine := &Engine{
		batchSize: 1000,
		queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{
				{"member": "file-001", "identity": float64(0.5)},
				{"member": "file-001", "identity": json.Number("0.50")},
				{"member": "file-001", "identity": json.Number("5e-1")},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Query: "RETURN witnesses", MemberColumn: "member", ExplicitIdentityColumn: "identity",
	}, PopulationMappingRequest{MaxUnmapped: 10}, PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		return visit("file-001")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.MappedCount != 1 || result.EmittedRows != 1 {
		t.Fatalf("population mapping result = %#v", result)
	}
}
