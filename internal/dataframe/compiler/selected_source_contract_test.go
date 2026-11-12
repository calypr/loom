package compiler_test

// arch-backend-11 selected-source compiler contract (compiler side).
//
// Every compile entry point takes the selected Target and seals it: a
// missing target is refused before any plan work, and the AQL-only source
// tool surfaces refuse a PostgreSQL target by name. The command leaf is a
// closed tagged union whose validators, not its field layout, decide what
// may exist — the mutation probes below assert that.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	renderpostgres "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	semantic "github.com/calypr/loom/internal/dataframe/semantic"
)

func archBackend11PostgreSQLTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func TestSelectedSourceCompilerTargetSeal(t *testing.T) {
	zeroOutput := lower.CompiledRecipeOutput{}
	zeroBindings := recipe.RuntimeBindings{}
	zeroResolved := semantic.ResolvedRecipePlan{}
	policy := ir.DefaultPhysicalOptimizationPolicy()

	t.Run("ZeroTargetIsRefusedByEverySelectedEntry", func(t *testing.T) {
		entries := []struct {
			name string
			call func(compiler.Target) error
		}{
			{"CompileResolvedRecipePlan", func(target compiler.Target) error {
				_, err := compiler.CompileResolvedRecipePlan(target, zeroResolved, 0, policy)
				return err
			}},
			{"CompileRecipeOutput", func(target compiler.Target) error {
				_, err := compiler.CompileRecipeOutput(target, zeroOutput, zeroBindings, 0, policy)
				return err
			}},
			{"CompileRecipeOutputPage", func(target compiler.Target) error {
				_, err := compiler.CompileRecipeOutputPage(target, zeroOutput, zeroBindings, 50, policy)
				return err
			}},
			{"CompileCategoryScanOutput", func(target compiler.Target) error {
				_, err := compiler.CompileCategoryScanOutput(target, zeroOutput, "id", 10, policy)
				return err
			}},
			{"CompileCategoryScanStage", func(target compiler.Target) error {
				_, err := compiler.CompileCategoryScanStage(target, zeroOutput, "stage", "c", "v", 10, policy)
				return err
			}},
			{"CompileCellTraceOutput", func(target compiler.Target) error {
				_, err := compiler.CompileCellTraceOutput(target, zeroOutput, "name", 0, 10, policy)
				return err
			}},
			{"CompileRowLineageOutput", func(target compiler.Target) error {
				_, err := compiler.CompileRowLineageOutput(target, zeroOutput, "row", 0, 10, policy)
				return err
			}},
			{"CompileResolvedGraphQuery", func(target compiler.Target) error {
				_, err := compiler.CompileResolvedGraphQuery(target, zeroResolved, 10, policy)
				return err
			}},
			{"CompilePopulationMappingOutput", func(target compiler.Target) error {
				_, err := compiler.CompilePopulationMappingOutput(target, zeroOutput, zeroBindings, policy)
				return err
			}},
			{"CompileTableShapeExclusions", func(target compiler.Target) error {
				_, err := compiler.CompileTableShapeExclusions(target, zeroOutput, 0, 10, policy)
				return err
			}},
		}
		if len(entries) != 10 {
			t.Fatalf("expected the ten selected compile entries, got %d", len(entries))
		}
		for _, entry := range entries {
			err := entry.call(compiler.Target{})
			if !errors.Is(err, compiler.ErrTargetNotSelected) {
				t.Fatalf("%s must refuse an unselected target with the named sentinel: %v", entry.name, err)
			}
		}
	})

	t.Run("EachAQLOnlySurfaceRefusesThePostgreSQLTargetByName", func(t *testing.T) {
		target := archBackend11PostgreSQLTarget(t)
		surfaces := []struct {
			surface string
			call    func() error
		}{
			{"category scan compilation", func() error {
				_, err := compiler.CompileCategoryScanOutput(target, zeroOutput, "id", 10, policy)
				return err
			}},
			{"category scan stage compilation", func() error {
				_, err := compiler.CompileCategoryScanStage(target, zeroOutput, "stage", "c", "v", 10, policy)
				return err
			}},
			{"cell trace compilation", func() error {
				_, err := compiler.CompileCellTraceOutput(target, zeroOutput, "name", 0, 10, policy)
				return err
			}},
			{"row lineage compilation", func() error {
				_, err := compiler.CompileRowLineageOutput(target, zeroOutput, "row", 0, 10, policy)
				return err
			}},
			{"graph query compilation", func() error {
				_, err := compiler.CompileResolvedGraphQuery(target, zeroResolved, 10, policy)
				return err
			}},
			// "population mapping compilation" left this list with
			// pg-sql-22: the PostgreSQL target compiles it through the
			// registered mapping entry (compiler/population_mapping.go
			// compilePopulationMappingPostgreSQL), so it is no longer an
			// AQL-only surface. The unselected-target refusal for the
			// entry stays sealed above.
			{"root-key output paging", func() error {
				_, err := compiler.CompileRecipeOutputPage(target, zeroOutput, zeroBindings, 50, policy)
				return err
			}},
			{"table shape exclusion compilation", func() error {
				_, err := compiler.CompileTableShapeExclusions(target, zeroOutput, 0, 10, policy)
				return err
			}},
		}
		for _, surface := range surfaces {
			err := surface.call()
			if err == nil || !strings.Contains(err.Error(), surface.surface+" is unsupported on the PostgreSQL compile target") {
				t.Fatalf("%s must name its PostgreSQL refusal: %v", surface.surface, err)
			}
			if !errors.Is(err, renderpostgres.ErrUnsupportedPostgresPlan) {
				t.Fatalf("%s refusal must carry the typed unsupported-plan error: %v", surface.surface, err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "aql") {
				t.Fatalf("%s refusal must not offer an AQL fallback: %v", surface.surface, err)
			}
		}
	})

	t.Run("CommandsAreSealedAcrossTheSelectedTarget", func(t *testing.T) {
		output, bindings := archBackend11FingerprintFixture()
		pgQuery, err := compiler.CompileRecipeOutput(archBackend11PostgreSQLTarget(t), output, bindings, 0, policy)
		if err != nil {
			t.Fatalf("postgresql compile: %v", err)
		}
		if pgQuery.Command.Backend() != command.CompiledBackendPostgreSQL {
			t.Fatalf("selected PostgreSQL target produced %q", pgQuery.Command.Backend())
		}
		if _, _, err := pgQuery.Command.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("the AQL adapter must refuse a PostgreSQL command: %v", err)
		}
		aqlQuery, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), output, bindings, 0, policy)
		if err != nil {
			t.Fatalf("aql compile: %v", err)
		}
		if aqlQuery.Command.Backend() != command.CompiledBackendAQL {
			t.Fatalf("AQL control produced %q", aqlQuery.Command.Backend())
		}
		if _, _, err := aqlQuery.Command.PostgreSQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("the PostgreSQL adapter must refuse an AQL command: %v", err)
		}
		// The AQL control keeps the exact rendered AQL shape: the selected
		// target did not reroute or rewrite the canonical AQL bytes.
		query, binds, err := aqlQuery.Command.AQLParameters()
		if err != nil {
			t.Fatalf("AQL control parameters: %v", err)
		}
		if !strings.Contains(query, "FOR root IN") || !strings.Contains(query, "@project") {
			t.Fatalf("AQL control drifted from canonical AQL rendering: %q", query)
		}
		if _, ok := binds["project"]; !ok {
			t.Fatalf("AQL control lost named binds: %v", binds)
		}
	})
}

// TestCompiledCommandLeafMutationProbes exercises the closed union with
// forged inputs: the validators decide what may exist, not the struct.
func TestCompiledCommandLeafMutationProbes(t *testing.T) {
	t.Run("RenderedTextIsRequired", func(t *testing.T) {
		if _, err := command.NewAQLCommand("   ", map[string]any{"p": "1"}); err == nil {
			t.Fatal("blank AQL query must be refused")
		}
		if _, err := command.NewPostgreSQLCommand("", nil); err == nil {
			t.Fatal("empty statement SQL must be refused")
		}
	})

	t.Run("ArgumentsMustBeRegisteredAndTyped", func(t *testing.T) {
		if _, err := command.NewPostgreSQLCommand("SELECT 1", []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterType("oid"), Value: "x"},
		}); err == nil {
			t.Fatal("unregistered parameter type must be refused")
		}
		if _, err := command.NewPostgreSQLCommand("SELECT $1", []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: nil},
		}); err == nil {
			t.Fatal("nil value outside the SQL-null type must be refused")
		}
	})

	t.Run("ZeroValueDispatchesNowhere", func(t *testing.T) {
		zero := command.CompiledCommand{}
		if _, _, err := zero.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero tag must refuse the AQL adapter: %v", err)
		}
		if _, _, err := zero.PostgreSQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero tag must refuse the PostgreSQL adapter: %v", err)
		}
		aqlRan, pgRan := false, false
		if err := zero.Dispatch(
			func(command.AQLCommand) error { aqlRan = true; return nil },
			func(command.PostgreSQLCommand) error { pgRan = true; return nil },
		); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero tag must refuse dispatch: %v", err)
		}
		if aqlRan || pgRan {
			t.Fatal("zero tag must not invoke any adapter")
		}
	})

	t.Run("NamedBindOverridesAreAQLOnly", func(t *testing.T) {
		pg, err := command.NewPostgreSQLCommand("SELECT $1", []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "P1"},
		})
		if err != nil {
			t.Fatalf("NewPostgreSQLCommand: %v", err)
		}
		if _, err := pg.WithAQLBinds(map[string]any{"project": "P2"}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("named bind overrides must not reach a PostgreSQL command: %v", err)
		}
		aql, err := command.NewAQLCommand("FOR d IN @@col RETURN d", map[string]any{"col": "Patient"})
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		if _, err := aql.WithAQLBinds(map[string]any{"col": json.RawMessage(`1`)}); err == nil {
			t.Fatal("bind override must keep the rendered Go type")
		}
		if _, err := aql.WithAQLBinds(map[string]any{"missing": "x"}); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("unknown bind keys must be refused: %v", err)
		}
	})
}
