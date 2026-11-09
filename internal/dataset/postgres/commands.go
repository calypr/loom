package postgres

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CommandBackend tags the dialect a Command targets. The tag is the only
// routing key execution may use: a command may reach the PostgreSQL statement
// executor only when the tag names PostgreSQL. A command tagged for another
// backend (or the zero value) fails closed before any statement executes, so
// AQL can never be handed to pgx and this store never grows a silent
// AQL-to-SQL fallback path.
type CommandBackend string

const (
	// CommandBackendPostgreSQL tags a statement with ordered positional
	// arguments.
	CommandBackendPostgreSQL CommandBackend = "POSTGRESQL"
)

// ParameterType declares the SQL type of one ordered positional argument,
// mirroring the compiler package's compiled-command vocabulary for the
// subset this store binds. SQL NULL is deliberately absent: no column in the
// generation-manifest schema admits SQL NULL, an absent active pointer is
// expressed as a missing row, and a JSON null inside schema identity is
// distinct from both. Binding nil is a construction error, so missing,
// JSON-null, and SQL-NULL cannot be flattened into each other.
type ParameterType string

const (
	// ParameterText binds a text argument at a bare $n placeholder.
	ParameterText ParameterType = "text"
	// ParameterJSONB binds a JSON document string at a $n::jsonb
	// placeholder. The cast is required in the SQL text so the server
	// types the argument as jsonb and never as untyped text.
	ParameterJSONB ParameterType = "jsonb"
)

func (t ParameterType) registered() bool {
	switch t {
	case ParameterText, ParameterJSONB:
		return true
	default:
		return false
	}
}

// Parameter is one ordered positional argument. Args[i] binds placeholder
// $(i+1) of the statement; order is the contract.
type Parameter struct {
	Type  ParameterType
	Value any
}

// Command is one backend-tagged statement with ordered typed arguments.
// Commands are built only through NewPostgreSQLCommand, which validates the
// tag, parameter types, and placeholder/argument agreement at construction.
type Command struct {
	backend CommandBackend
	sql     string
	args    []Parameter
}

// Backend reports the command's routing tag.
func (c Command) Backend() CommandBackend { return c.backend }

// Errors for classifying command failures without string matching.
var (
	// ErrInvalidCommand reports a malformed statement: empty SQL, an
	// unregistered parameter type, a nil argument value, or a
	// placeholder/argument mismatch (missing placeholder, out-of-range
	// placeholder, unused argument, or a cast that disagrees with the
	// argument's declared type).
	ErrInvalidCommand = errors.New("dataset postgres: invalid command")
	// ErrCommandBackendMismatch reports dispatch of a command to a backend
	// other than its tag. It is returned before any statement executes.
	ErrCommandBackendMismatch = errors.New("dataset postgres: command backend mismatch")
)

var (
	commandPlaceholderPattern = regexp.MustCompile(`\$(\d+)(?:::([a-z]+))?`)
	commandAnyPlaceholder     = regexp.MustCompile(`\$[0-9]`)
)

// NewPostgreSQLCommand tags statement SQL plus ordered typed arguments as a
// PostgreSQL command. Every $n placeholder must be in 1..len(args), every
// argument must appear as a placeholder, and each placeholder's cast must
// agree with the argument type: ParameterJSONB appears only as $n::jsonb and
// ParameterText appears only bare. A nil argument value is rejected; use the
// absence of a row for absent data, never SQL NULL. The argument slice is
// copied so caller mutation cannot reorder positional bindings.
func NewPostgreSQLCommand(sql string, args []Parameter) (Command, error) {
	if strings.TrimSpace(sql) == "" {
		return Command{}, fmt.Errorf("%w: statement SQL is blank", ErrInvalidCommand)
	}
	if len(args) == 0 && commandAnyPlaceholder.MatchString(sql) {
		return Command{}, fmt.Errorf("%w: statement references $n placeholders but declares no arguments", ErrInvalidCommand)
	}
	for i, arg := range args {
		if !arg.Type.registered() {
			return Command{}, fmt.Errorf("%w: argument %d has unregistered parameter type %q", ErrInvalidCommand, i+1, string(arg.Type))
		}
		if arg.Value == nil {
			return Command{}, fmt.Errorf("%w: argument %d is typed %q but carries a nil value; this store binds no SQL NULLs", ErrInvalidCommand, i+1, string(arg.Type))
		}
		switch arg.Type {
		case ParameterText:
			if _, ok := arg.Value.(string); !ok {
				return Command{}, fmt.Errorf("%w: argument %d is typed %q but carries %T", ErrInvalidCommand, i+1, string(arg.Type), arg.Value)
			}
		case ParameterJSONB:
			if _, ok := arg.Value.(string); !ok {
				return Command{}, fmt.Errorf("%w: argument %d is typed %q but carries %T; pass a marshalled JSON string", ErrInvalidCommand, i+1, string(arg.Type), arg.Value)
			}
		}
	}
	seen := make([]bool, len(args))
	for _, match := range commandPlaceholderPattern.FindAllStringSubmatch(sql, -1) {
		n, err := strconv.Atoi(match[1])
		if err != nil || n < 1 || n > len(args) {
			return Command{}, fmt.Errorf("%w: placeholder $%s is outside the declared argument range 1..%d", ErrInvalidCommand, match[1], len(args))
		}
		cast := match[2]
		switch args[n-1].Type {
		case ParameterJSONB:
			if cast != string(ParameterJSONB) {
				return Command{}, fmt.Errorf("%w: placeholder $%d binds a %s argument and must appear as $%d::jsonb", ErrInvalidCommand, n, string(ParameterJSONB), n)
			}
		case ParameterText:
			if cast != "" {
				return Command{}, fmt.Errorf("%w: placeholder $%d binds a %s argument and must appear bare, not as $%d::%s", ErrInvalidCommand, n, string(ParameterText), n, cast)
			}
		}
		seen[n-1] = true
	}
	// A placeholder such as $1foo does not match the pattern above; after
	// removing every well-formed placeholder, no $n text may remain.
	if commandAnyPlaceholder.MatchString(commandPlaceholderPattern.ReplaceAllString(sql, "")) {
		return Command{}, fmt.Errorf("%w: statement contains a malformed placeholder", ErrInvalidCommand)
	}
	for i, used := range seen {
		if !used {
			return Command{}, fmt.Errorf("%w: argument %d is declared but never referenced by a placeholder", ErrInvalidCommand, i+1)
		}
	}
	return Command{
		backend: CommandBackendPostgreSQL,
		sql:     sql,
		args:    append([]Parameter(nil), args...),
	}, nil
}

// PostgreSQL dispatches a PostgreSQL-tagged command to statement SQL plus a
// fresh slice of ordered argument values for the pgx statement executor. Any
// other tag, including the zero value, is rejected before a statement could
// execute; the backend mismatch is returned as ErrCommandBackendMismatch so
// callers can classify routing defects separately from query failures.
func (c Command) PostgreSQL() (string, []any, error) {
	switch c.backend {
	case CommandBackendPostgreSQL:
		values := make([]any, 0, len(c.args))
		for _, arg := range c.args {
			values = append(values, arg.Value)
		}
		return c.sql, values, nil
	default:
		return "", nil, fmt.Errorf("%w: refusing to pass command with backend %q to the PostgreSQL statement executor",
			ErrCommandBackendMismatch, string(c.backend))
	}
}
