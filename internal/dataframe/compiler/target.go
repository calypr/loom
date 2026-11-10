package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
)

// Target is the closed, validated compile target selected once by
// composition and carried unchanged through every authoritative
// target-aware compiler function. It is the only way a compile request names
// a dialect: there is no per-request AQL default hidden behind an optional
// PostgreSQL override, and no compiler path converts AQL to SQL. A Target is
// built exactly one way per dialect — ArangoTarget or PostgreSQLTarget — and
// the zero Target is never selected: every compile entry point refuses it
// before any lowering, rendering, or I/O.
type Target struct {
	backend  command.CompiledBackend
	postgres ir.PostgresStorageBinding
	selected bool
}

// ArangoTarget selects the ArangoDB/AQL compile target. AQL plans render
// through the canonical AQL renderer with the exact payload bytes and
// named-bind contract.
func ArangoTarget() Target {
	return Target{backend: command.CompiledBackendAQL, selected: true}
}

// PostgreSQLTarget selects the PostgreSQL/SQL compile target and validates
// the complete trusted storage surface at selection time. The binding is the
// only source of SQL identifiers for this target (the compiler-owned
// allowlist in internal/dataframe/compiler/ir); plans the PostgreSQL
// renderer does not support fail explicitly with the renderer's typed
// errors and never fall back to AQL.
func PostgreSQLTarget(binding ir.PostgresStorageBinding) (Target, error) {
	if err := binding.Validate(); err != nil {
		return Target{}, fmt.Errorf("postgres compile target: %w", err)
	}
	return Target{backend: command.CompiledBackendPostgreSQL, postgres: binding, selected: true}, nil
}

// Valid reports whether a target was actually selected. The zero Target is
// invalid and every target-aware entry point refuses it.
func (t Target) Valid() bool { return t.selected }

// Backend names the backend this target renders for. It lets composition
// pin the tag its row port must receive without re-deriving dialect from
// configuration; the zero Target reports the empty backend.
func (t Target) Backend() command.CompiledBackend { return t.backend }

// ErrTargetNotSelected reports a compile request that arrived without a
// selected target. Selection is composition's obligation; execution never
// defaults a dialect per request.
var ErrTargetNotSelected = fmt.Errorf("compile target is not selected: composition must pass compiler.ArangoTarget or compiler.PostgreSQLTarget")

func (t Target) validate() error {
	if !t.selected {
		return ErrTargetNotSelected
	}
	switch t.backend {
	case command.CompiledBackendAQL, command.CompiledBackendPostgreSQL:
		return nil
	default:
		return fmt.Errorf("%w: unknown compile target backend %q", ErrTargetNotSelected, string(t.backend))
	}
}

// rejectPostgreSQL refuses a compile request for one of the AQL-only source
// tool surfaces when the selected target is PostgreSQL. The refusal carries
// the PostgreSQL renderer's typed unsupported-plan error, so every
// unsupported PostgreSQL plan fails the same way: explicitly, before any
// rendering or I/O, and never by falling back to AQL.
func (t Target) rejectPostgreSQL(surface string) error {
	if t.backend == command.CompiledBackendPostgreSQL {
		return fmt.Errorf("%s is unsupported on the PostgreSQL compile target: %w", surface, postgresrender.ErrUnsupportedPostgresPlan)
	}
	return nil
}

// renderPostgreSQLFinalPlan renders one finalized physical plan through the
// PostgreSQL renderer bound to this target and tags it as an executable
// PostgreSQL command. Unsupported plans fail explicitly with the renderer's
// typed error; there is no AQL fallback and no AQL-to-SQL conversion.
func (t Target) renderPostgreSQLFinalPlan(plan ir.PhysicalPlan) (command.CompiledCommand, command.RenderedPostgresPhysicalPlan, error) {
	if err := t.validate(); err != nil {
		return command.CompiledCommand{}, command.RenderedPostgresPhysicalPlan{}, err
	}
	if t.backend != command.CompiledBackendPostgreSQL {
		return command.CompiledCommand{}, command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("target %s cannot render a PostgreSQL plan", string(t.backend))
	}
	rendered, err := postgresrender.RenderPhysicalPlan(plan, t.postgres)
	if err != nil {
		return command.CompiledCommand{}, command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("render postgresql physical plan: %w", err)
	}
	compiledCommand, err := command.NewPostgreSQLCommand(rendered.SQL, rendered.Args)
	if err != nil {
		return command.CompiledCommand{}, command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("tag postgresql command: %w", err)
	}
	return compiledCommand, rendered, nil
}

// postgreSQLPlanFingerprint is the stable plan fingerprint for a
// PostgreSQL-rendered output, computed over the statement SQL, its ordered
// typed arguments, and the declared result column contract. The encoding is
// diagnostic-safe (no row values in the digest input beyond what the
// statement already parameterizes) and deterministic; it is the single
// authority shared by the compiler-produced CompiledQuery and its consumers.
func postgreSQLPlanFingerprint(sqlText string, args []command.PostgreSQLParameter, columns []string) string {
	payload, err := json.Marshal(struct {
		SQL     string
		Args    []command.PostgreSQLParameter
		Columns []string
	}{SQL: sqlText, Args: args, Columns: columns})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// postgreSQLPublicColumns derives the public preview projection from the
// compiler-owned ordered output schema, restricted to columns the PostgreSQL
// command actually declares; with no schema it publishes the whole declared
// contract (there is no internal marking to honor). Compiler-only internal
// projections never appear: an empty public intersection is returned as-is
// and the caller refuses the compilation rather than leaking internals.
func postgreSQLPublicColumns(schema []lower.CompiledOutputColumn, declared []string) []string {
	if len(schema) == 0 {
		return append([]string(nil), declared...)
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, column := range declared {
		declaredSet[column] = struct{}{}
	}
	public := make([]string, 0, len(declared))
	for _, column := range schema {
		if column.Internal {
			continue
		}
		if _, ok := declaredSet[column.Name]; !ok {
			continue
		}
		public = append(public, column.Name)
	}
	return public
}
