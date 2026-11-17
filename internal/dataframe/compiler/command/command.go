package command

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type CompiledBackend string

const (
	CompiledBackendAQL        CompiledBackend = "AQL"
	CompiledBackendPostgreSQL CompiledBackend = "POSTGRESQL"
)

var ErrCompiledCommandBackendMismatch = errors.New("compiled command backend mismatch")

var ErrCompiledCommandBackendNotRegistered = errors.New("compiled command backend is not registered")

type AQLCommand struct {
	Query    string
	BindVars map[string]any
}

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

type PostgreSQLParameter struct {
	Type  PostgreSQLParameterType
	Value any
}

type PostgreSQLCommand struct {
	SQL  string
	Args []PostgreSQLParameter
}

type CompiledCommand struct {
	backend    CompiledBackend
	aql        AQLCommand
	postgreSQL PostgreSQLCommand
}

func NewAQLCommand(query string, bindVars map[string]any) (CompiledCommand, error) {
	if strings.TrimSpace(query) == "" {
		return CompiledCommand{}, fmt.Errorf("AQL compiled command requires rendered query text")
	}
	return CompiledCommand{
		backend: CompiledBackendAQL,
		aql:     AQLCommand{Query: query, BindVars: bindVars},
	}, nil
}

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

func (c CompiledCommand) Backend() CompiledBackend { return c.backend }

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
