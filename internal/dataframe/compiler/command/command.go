// Package command owns Loom's executable compiled-command representation:
// the tagged union of rendered backend commands, their ordered parameter
// types, and the checked constructors that freeze them.
//
// It is a leaf with standard-library-only dependencies. The root compiler
// package and both dialect renderers (render/aql, render/postgres) may
// import it; renderer children must not import the compiler root. The leaf
// holds executable representation only — no plan semantics, drivers, scope
// policy, metrics, or generic repositories. Placeholder/type/null/copy checks
// run at construction here, so a hand-assembled payload can never reach an
// adapter. Dialect parameter encoding stays with each renderer: shared IR
// carries no SQL or AQL parameter shapes.
package command

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// CompiledBackend tags which storage dialect a CompiledCommand targets. The
// tag is the only routing key execution may use: an adapter may receive a
// command only when the tag names that adapter's backend. A command tagged
// for one backend must never be handed to another backend's boundary, so
// rendered PostgreSQL SQL cannot reach the AQL QueryRows(query, bindVars)
// adapter and rendered AQL cannot reach a PostgreSQL statement executor.
type CompiledBackend string

const (
	// CompiledBackendAQL tags a rendered ArangoDB AQL command with named
	// bind variables.
	CompiledBackendAQL CompiledBackend = "AQL"
	// CompiledBackendPostgreSQL tags a rendered PostgreSQL statement with
	// ordered positional arguments. Only the compiler's PostgreSQL compile
	// target produces this tag, through the PostgreSQL renderer
	// (internal/dataframe/compiler/render/postgres); no path converts AQL
	// into SQL.
	CompiledBackendPostgreSQL CompiledBackend = "POSTGRESQL"
)

// ErrCompiledCommandBackendMismatch reports that a command was dispatched to
// the adapter of a different backend than its tag. The dispatch returns this
// error before any query executes.
var ErrCompiledCommandBackendMismatch = errors.New("compiled command backend mismatch")

// ErrCompiledCommandBackendNotRegistered reports that no renderer or adapter
// is registered for a compiled-command backend. Callers must fail closed; a
// PostgreSQL request must never fall back to an AQL plan or an AQL-to-SQL
// conversion.
var ErrCompiledCommandBackendNotRegistered = errors.New("compiled command backend is not registered")

// AQLCommand is the AQL variant of a compiled command. It carries the exact
// rendered query text and the exact named bind-variable map produced by the
// canonical AQL renderer, which is what the Arango QueryRows(context, query,
// batch, bindVars, visit) adapter accepts. Execution consumes these fields
// only through CompiledCommand dispatch, never as loose strings.
type AQLCommand struct {
	Query    string
	BindVars map[string]any
}

// PostgreSQLParameterType declares the SQL type of one positional argument.
// The null type is distinct from a JSON null: SQL NULL is a parameter typed
// PostgreSQLParameterNull with a nil value, while a JSON null is a
// json.RawMessage("null") value under PostgreSQLParameterJSON or
// PostgreSQLParameterJSONB. A missing value is not an argument at all; a
// renderer omits the parameter and the SQL text, and must never flatten
// missing into JSON-null or JSON-null into SQL NULL.
type PostgreSQLParameterType string

const (
	PostgreSQLParameterNull            PostgreSQLParameterType = "null"
	PostgreSQLParameterText            PostgreSQLParameterType = "text"
	PostgreSQLParameterBigint          PostgreSQLParameterType = "bigint"
	PostgreSQLParameterDoublePrecision PostgreSQLParameterType = "double precision"
	PostgreSQLParameterBoolean         PostgreSQLParameterType = "boolean"
	PostgreSQLParameterDate            PostgreSQLParameterType = "date"
	PostgreSQLParameterTimestamptz     PostgreSQLParameterType = "timestamp with time zone"
	PostgreSQLParameterJSON            PostgreSQLParameterType = "json"
	PostgreSQLParameterJSONB           PostgreSQLParameterType = "jsonb"
)

func (t PostgreSQLParameterType) registered() bool {
	switch t {
	case PostgreSQLParameterNull, PostgreSQLParameterText, PostgreSQLParameterBigint,
		PostgreSQLParameterDoublePrecision, PostgreSQLParameterBoolean, PostgreSQLParameterDate,
		PostgreSQLParameterTimestamptz, PostgreSQLParameterJSON, PostgreSQLParameterJSONB:
		return true
	default:
		return false
	}
}

// PostgreSQLParameter is one ordered positional argument of a PostgreSQL
// command. Args[i] binds placeholder $(i+1) of the statement.
type PostgreSQLParameter struct {
	Type  PostgreSQLParameterType
	Value any
}

// PostgreSQLCommand is the PostgreSQL variant of a compiled command. It
// carries statement SQL plus ordered typed arguments; it has no named bind
// map, and the AQL adapter has no way to interpret it.
type PostgreSQLCommand struct {
	SQL  string
	Args []PostgreSQLParameter
}

// CompiledCommand is the tagged union of rendered backend commands produced
// by the canonical compiler pipeline. Commands are built only through
// NewAQLCommand or NewPostgreSQLCommand, each of which validates the
// tag/payload pairing at construction; the zero value carries no backend and
// dispatches nowhere. Exhaustive dispatch is provided by AQLParameters,
// PostgreSQLParameters, and Dispatch, all of which reject mismatched
// backend/command pairs before any query executes.
type CompiledCommand struct {
	backend    CompiledBackend
	aql        AQLCommand
	postgreSQL PostgreSQLCommand
}

// NewAQLCommand tags a rendered AQL query and its named bind map as an AQL
// command. The bind map is carried unchanged: the same map instance flows
// from the AQL renderer through dispatch to the Arango adapter.
func NewAQLCommand(query string, bindVars map[string]any) (CompiledCommand, error) {
	if strings.TrimSpace(query) == "" {
		return CompiledCommand{}, fmt.Errorf("AQL compiled command requires rendered query text")
	}
	return CompiledCommand{
		backend: CompiledBackendAQL,
		aql:     AQLCommand{Query: query, BindVars: bindVars},
	}, nil
}

// NewPostgreSQLCommand tags statement SQL and ordered typed arguments as a
// PostgreSQL command. Each argument must declare a registered parameter
// type, and a nil value is accepted only for the SQL-null type. The argument
// slice is copied so later caller mutation cannot reorder the statement's
// positional bindings.
func NewPostgreSQLCommand(sql string, args []PostgreSQLParameter) (CompiledCommand, error) {
	if strings.TrimSpace(sql) == "" {
		return CompiledCommand{}, fmt.Errorf("PostgreSQL compiled command requires rendered statement SQL")
	}
	if err := validatePostgreSQLParameters(args); err != nil {
		return CompiledCommand{}, err
	}
	var copied []PostgreSQLParameter
	if len(args) != 0 {
		copied = append([]PostgreSQLParameter(nil), args...)
	}
	return CompiledCommand{
		backend:    CompiledBackendPostgreSQL,
		postgreSQL: PostgreSQLCommand{SQL: sql, Args: copied},
	}, nil
}

// Backend reports the command's routing tag.
func (c CompiledCommand) Backend() CompiledBackend { return c.backend }

// WithAQLBinds returns a copy of an AQL-tagged command whose named bind map
// carries the given overrides applied on top of the command's current binds;
// only existing bind keys may be overridden and only with values of the same
// Go type, so a caller can supply a per-page bind value without mutating the
// command or introducing unrendered keys into an executed query. The original
// command and its bind map are never modified: the returned command owns a
// fresh map, preserving the parameter copy/ownership rules. A non-AQL tag, a
// nil or empty override, an unknown key, a nil current value with a
// mismatched replacement type, or a nil override value is rejected before any
// command could execute; PostgreSQL statements bind positionally and have no
// named binds to override.
func (c CompiledCommand) WithAQLBinds(overrides map[string]any) (CompiledCommand, error) {
	switch c.backend {
	case CompiledBackendAQL:
	case CompiledBackendPostgreSQL:
		return CompiledCommand{}, fmt.Errorf("%w: %s statements bind positional arguments and have no named binds to override",
			ErrCompiledCommandBackendMismatch, string(CompiledBackendPostgreSQL))
	default:
		return CompiledCommand{}, fmt.Errorf("%w: cannot override binds on command with unregistered backend %q",
			ErrCompiledCommandBackendNotRegistered, string(c.backend))
	}
	if len(overrides) == 0 {
		return CompiledCommand{}, fmt.Errorf("%w: bind overrides must name at least one existing bind",
			ErrCompiledCommandBackendMismatch)
	}
	binds := make(map[string]any, len(c.aql.BindVars))
	for key, value := range c.aql.BindVars {
		binds[key] = value
	}
	for key, value := range overrides {
		current, exists := binds[key]
		if !exists {
			return CompiledCommand{}, fmt.Errorf("%w: bind %q is not part of the rendered AQL command",
				ErrCompiledCommandBackendMismatch, key)
		}
		if current == nil || value == nil || reflect.TypeOf(current) != reflect.TypeOf(value) {
			return CompiledCommand{}, fmt.Errorf("%w: bind %q override type %T does not match the rendered type %T",
				ErrCompiledCommandBackendMismatch, key, value, current)
		}
		binds[key] = value
	}
	return CompiledCommand{
		backend: CompiledBackendAQL,
		aql:     AQLCommand{Query: c.aql.Query, BindVars: binds},
	}, nil
}

// AQLParameters dispatches an AQL-tagged command to the Arango adapter
// parameters. Any other tag, including the zero value, is rejected with
// ErrCompiledCommandBackendMismatch or ErrCompiledCommandBackendNotRegistered
// before a query could execute; PostgreSQL SQL never reaches the AQL
// QueryRows(query, bindVars) adapter through this method.
func (c CompiledCommand) AQLParameters() (string, map[string]any, error) {
	switch c.backend {
	case CompiledBackendAQL:
		return c.aql.Query, c.aql.BindVars, nil
	case CompiledBackendPostgreSQL:
		return "", nil, fmt.Errorf("%w: refusing to pass %s statement SQL to the AQL QueryRows(query, bindVars) adapter",
			ErrCompiledCommandBackendMismatch, string(CompiledBackendPostgreSQL))
	default:
		return "", nil, fmt.Errorf("%w: cannot dispatch command with unregistered backend %q to AQL",
			ErrCompiledCommandBackendNotRegistered, string(c.backend))
	}
}

// PostgreSQLParameters dispatches a PostgreSQL-tagged command to statement
// SQL plus ordered typed arguments. Unlike the AQL bind map, which is a
// documented shared instance, the returned argument slice is a fresh copy:
// callers cannot mutate the command's positional bindings through the
// accessor. Any other tag is rejected before a statement could execute.
func (c CompiledCommand) PostgreSQLParameters() (string, []PostgreSQLParameter, error) {
	switch c.backend {
	case CompiledBackendPostgreSQL:
		var copied []PostgreSQLParameter
		if len(c.postgreSQL.Args) != 0 {
			copied = append([]PostgreSQLParameter(nil), c.postgreSQL.Args...)
		}
		return c.postgreSQL.SQL, copied, nil
	case CompiledBackendAQL:
		return "", nil, fmt.Errorf("%w: refusing to pass %s query to the PostgreSQL statement executor",
			ErrCompiledCommandBackendMismatch, string(CompiledBackendAQL))
	default:
		return "", nil, fmt.Errorf("%w: cannot dispatch command with unregistered backend %q to PostgreSQL",
			ErrCompiledCommandBackendNotRegistered, string(c.backend))
	}
}

// Dispatch routes one command to the adapter registered for its tag and
// invokes it exactly once; a mismatched pair never executes a query. A
// missing adapter for the matching tag, or an unregistered or unknown tag,
// is the fail-closed capability gap and reports
// ErrCompiledCommandBackendNotRegistered, so callers may report the backend
// as unsupported. Reporting ErrCompiledCommandBackendMismatch is reserved
// for routing defects: a command tagged for one backend sent to the other
// backend's boundary through AQLParameters or PostgreSQLParameters.
func (c CompiledCommand) Dispatch(aqlAdapter func(AQLCommand) error, postgreSQLAdapter func(PostgreSQLCommand) error) error {
	switch c.backend {
	case CompiledBackendAQL:
		if aqlAdapter == nil {
			return fmt.Errorf("%w: no AQL adapter registered for %s command",
				ErrCompiledCommandBackendNotRegistered, string(CompiledBackendAQL))
		}
		return aqlAdapter(c.aql)
	case CompiledBackendPostgreSQL:
		if postgreSQLAdapter == nil {
			return fmt.Errorf("%w: no PostgreSQL adapter registered for %s command",
				ErrCompiledCommandBackendNotRegistered, string(CompiledBackendPostgreSQL))
		}
		return postgreSQLAdapter(c.postgreSQL)
	default:
		return fmt.Errorf("%w: cannot dispatch command with unregistered backend %q",
			ErrCompiledCommandBackendNotRegistered, string(c.backend))
	}
}
