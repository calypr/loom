package compiler

// C-FREEZE witness for the moved public-column projection.
//
// arch-backend-11 moved publicPostgreSQLColumns from
//   git show 648f7c70:internal/dataframe/execution/engine.go | sed -n '936,955p'
// onto the selected compile target as postgreSQLPublicColumns (target.go).
// The moved body is source-identical to the base function; this witness
// freezes the behavior the base had, so a future edit inside the compiler
// cannot silently widen what a PostgreSQL preview publishes.

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

func frozenColumns(names ...string) []string { return append([]string(nil), names...) }

func frozenSchema(specs ...[2]any) []lower.CompiledOutputColumn {
	schema := make([]lower.CompiledOutputColumn, 0, len(specs))
	for _, spec := range specs {
		schema = append(schema, lower.CompiledOutputColumn{
			Name:     spec[0].(string),
			Internal: spec[1].(bool),
		})
	}
	return schema
}

func TestPostgreSQLPublicColumnsFreezeMatchesBase648f7c70(t *testing.T) {
	t.Run("GoldenRecipe", func(t *testing.T) {
		got := postgreSQLPublicColumns(
			frozenSchema([2]any{"id", false}, [2]any{"name", false}),
			frozenColumns("id", "name"),
		)
		want := []string{"id", "name"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("golden recipe projection: %v want %v", got, want)
		}
	})

	t.Run("SchemaOrderIsAuthoritative", func(t *testing.T) {
		got := postgreSQLPublicColumns(
			frozenSchema([2]any{"id", false}, [2]any{"name", false}),
			frozenColumns("name", "id"),
		)
		if !reflect.DeepEqual(got, []string{"id", "name"}) {
			t.Fatalf("projection must follow compiler schema order, not declared order: %v", got)
		}
	})

	t.Run("InternalProjectionEdgeCase", func(t *testing.T) {
		// A compiler-only internal projection that also appears in the
		// declared contract (the construction-leak edge) never publishes.
		got := postgreSQLPublicColumns(
			frozenSchema(
				[2]any{"id", false},
				[2]any{"__loom_source_resource_id", true},
				[2]any{"name", false},
			),
			frozenColumns("id", "__loom_source_resource_id", "name"),
		)
		if !reflect.DeepEqual(got, []string{"id", "name"}) {
			t.Fatalf("internal projection leaked into the public preview projection: %v", got)
		}
	})

	t.Run("AllInternalRefused", func(t *testing.T) {
		// The base returned the empty intersection as-is so the caller
		// refuses the compilation rather than leaking internals.
		got := postgreSQLPublicColumns(
			frozenSchema([2]any{"__loom_internal", true}),
			frozenColumns("__loom_internal"),
		)
		if len(got) != 0 {
			t.Fatalf("all-internal contract must project to empty for caller refusal: %v", got)
		}
	})

	t.Run("SchemaColumnNotDeclaredIsDropped", func(t *testing.T) {
		got := postgreSQLPublicColumns(
			frozenSchema([2]any{"id", false}, [2]any{"secret", false}),
			frozenColumns("id"),
		)
		if !reflect.DeepEqual(got, []string{"id"}) {
			t.Fatalf("undeclared schema column must not publish: %v", got)
		}
	})

	t.Run("NoSchemaPublishesDeclared", func(t *testing.T) {
		declared := frozenColumns("id", "name")
		got := postgreSQLPublicColumns(nil, declared)
		if !reflect.DeepEqual(got, declared) {
			t.Fatalf("no-schema path must publish the declared contract: %v", got)
		}
		if len(got) != 0 {
			got[0] = "mutated"
			if declared[0] != "id" {
				t.Fatal("no-schema path must copy the declared contract, not alias it")
			}
		}
	})
}
