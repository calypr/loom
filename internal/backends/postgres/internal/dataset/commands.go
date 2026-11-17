package postgres

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type CommandBackend string

const (
	CommandBackendPostgreSQL CommandBackend = "POSTGRESQL"
)

type ParameterType string

const (
	ParameterText  ParameterType = "text"
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

type Parameter struct {
	Type  ParameterType
	Value any
}

type Command struct {
	backend CommandBackend
	sql     string
	args    []Parameter
}

func (c Command) Backend() CommandBackend { return c.backend }

var (
	ErrInvalidCommand         = errors.New("dataset postgres: invalid command")
	ErrCommandBackendMismatch = errors.New("dataset postgres: command backend mismatch")
)

var (
	commandPlaceholderPattern = regexp.MustCompile(`\$(\d+)(?:::([a-z]+))?`)
	commandAnyPlaceholder     = regexp.MustCompile(`\$[0-9]`)
)

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
