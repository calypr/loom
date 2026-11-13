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

type Target struct {
	backend  command.CompiledBackend
	postgres ir.PostgresStorageBinding
	selected bool
}

func ArangoTarget() Target {
	return Target{backend: command.CompiledBackendAQL, selected: true}
}

func PostgreSQLTarget(binding ir.PostgresStorageBinding) (Target, error) {
	if err := binding.Validate(); err != nil {
		return Target{}, fmt.Errorf("postgres compile target: %w", err)
	}
	return Target{backend: command.CompiledBackendPostgreSQL, postgres: binding, selected: true}, nil
}

func (t Target) Valid() bool { return t.selected }

func (t Target) Backend() command.CompiledBackend { return t.backend }

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

func (t Target) rejectPostgreSQL(surface string) error {
	if t.backend == command.CompiledBackendPostgreSQL {
		return fmt.Errorf("%s is unsupported on the PostgreSQL compile target: %w", surface, postgresrender.ErrUnsupportedPostgresPlan)
	}
	return nil
}

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
