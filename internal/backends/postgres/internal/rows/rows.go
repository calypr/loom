package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type RowVisitor = execution.RowVisitor

type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var (
	ErrInvalidExecution       = errors.New("postgres rows: invalid execution")
	ErrMalformedStatement     = errors.New("postgres rows: malformed statement")
	ErrInvalidParameter       = errors.New("postgres rows: invalid parameter")
	ErrColumnContractMismatch = errors.New("postgres rows: column contract mismatch")
	ErrQuery                  = errors.New("postgres rows: query failed")
)

type QueryError struct {
	Op  string
	Err error
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("postgres rows: %s: %v", e.Op, e.Err)
}

func (e *QueryError) Unwrap() []error { return []error{ErrQuery, e.Err} }

type Executor struct {
	querier Querier
}

func NewExecutor(querier Querier) (*Executor, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: querier is required", ErrInvalidExecution)
	}
	return &Executor{querier: querier}, nil
}

var _ execution.SourceRows = (*Executor)(nil).SourceRows

func (e *Executor) SourceRows(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidExecution)
	}
	if e == nil || e.querier == nil {
		return fmt.Errorf("%w: executor has no querier", ErrInvalidExecution)
	}
	if visit == nil {
		return fmt.Errorf("%w: row visitor is required", ErrInvalidExecution)
	}
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	columns := request.DeclaredColumns
	if err := validateColumns(columns); err != nil {
		return err
	}
	if err := validateStatementShape(sqlText, args); err != nil {
		return err
	}
	bound, err := bindParameters(args)
	if err != nil {
		return err
	}

	rows, err := e.querier.Query(ctx, sqlText, bound...)
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		return classifyExecution(ctx, "query", err)
	}
	if rows == nil {
		return &QueryError{Op: "query", Err: errors.New("driver returned no result set")}
	}
	return streamRows(ctx, rows, columns, visit)
}

func streamRows(ctx context.Context, rows pgx.Rows, columns []string, visit RowVisitor) error {
	defer rows.Close()

	var targets []any
	for rows.Next() {
		if targets == nil {
			descriptions := rows.FieldDescriptions()
			if err := checkColumnContract(columns, descriptions); err != nil {
				return err
			}
			targets = make([]any, len(columns))
			for i, description := range descriptions {
				switch description.DataTypeOID {
				case pgtype.JSONOID, pgtype.JSONBOID:
					targets[i] = new(json.RawMessage)
				default:
					targets[i] = new(any)
				}
			}
		}
		if err := rows.Scan(targets...); err != nil {
			return classifyExecution(ctx, "scan", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = decodeColumn(targets[i])
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return classifyExecution(ctx, "rows", err)
	}
	if targets == nil {
		if err := checkColumnContract(columns, rows.FieldDescriptions()); err != nil {
			return err
		}
	}
	return nil
}

func validateColumns(columns []string) error {
	if len(columns) == 0 {
		return fmt.Errorf("%w: at least one declared result column is required", ErrInvalidExecution)
	}
	seen := make(map[string]struct{}, len(columns))
	for i, column := range columns {
		if strings.TrimSpace(column) == "" {
			return fmt.Errorf("%w: declared result column %d is blank", ErrInvalidExecution, i+1)
		}
		if _, duplicate := seen[column]; duplicate {
			return fmt.Errorf("%w: declared result column %q is declared twice", ErrInvalidExecution, column)
		}
		seen[column] = struct{}{}
	}
	return nil
}

func checkColumnContract(columns []string, descriptions []pgconn.FieldDescription) error {
	if len(descriptions) != len(columns) {
		return fmt.Errorf("%w: statement returns %d column(s), contract declares %d",
			ErrColumnContractMismatch, len(descriptions), len(columns))
	}
	for i, column := range columns {
		got := descriptions[i].Name
		if got != column && got != "c"+strconv.Itoa(i) {
			return fmt.Errorf("%w: result column %d is %q, contract declares %q",
				ErrColumnContractMismatch, i+1, got, column)
		}
	}
	return nil
}

var statementPlaceholderRE = regexp.MustCompile(`\$\d+`)

func validateStatementShape(sqlText string, args []command.PostgreSQLParameter) error {
	matches := statementPlaceholderRE.FindAllString(sqlText, -1)
	if len(args) == 0 {
		if len(matches) > 0 {
			return fmt.Errorf("%w: statement uses $n placeholders but declares no arguments", ErrMalformedStatement)
		}
		return nil
	}
	used := make([]bool, len(args))
	for _, match := range matches {
		n, err := strconv.Atoi(match[1:])
		if err != nil || n < 1 || n > len(args) {
			return fmt.Errorf("%w: placeholder %s is outside the declared argument range 1..%d",
				ErrMalformedStatement, match, len(args))
		}
		used[n-1] = true
	}
	for i, appears := range used {
		if !appears {
			return fmt.Errorf("%w: argument %d never appears as $%d in the statement",
				ErrMalformedStatement, i+1, i+1)
		}
	}
	return nil
}

func bindParameters(args []command.PostgreSQLParameter) ([]any, error) {
	bound := make([]any, 0, len(args))
	for i, arg := range args {
		value, err := bindParameter(arg)
		if err != nil {
			return nil, fmt.Errorf("%w: argument %d declared %q carries %T: %w",
				ErrInvalidParameter, i+1, string(arg.Type), arg.Value, err)
		}
		bound = append(bound, value)
	}
	return bound, nil
}

func bindParameter(arg command.PostgreSQLParameter) (any, error) {
	switch arg.Type {
	case command.PostgreSQLParameterNull:
		if arg.Value != nil {
			return nil, errors.New("an sql-null parameter must carry a nil value; a JSON null belongs on a json or jsonb parameter")
		}
		return nil, nil
	case command.PostgreSQLParameterText:
		value, ok := arg.Value.(string)
		if !ok {
			return nil, errors.New("a text parameter must carry a string")
		}
		return value, nil
	case command.PostgreSQLParameterBigint:
		value, ok := arg.Value.(int64)
		if !ok {
			return nil, errors.New("a bigint parameter must carry an int64")
		}
		return value, nil
	case command.PostgreSQLParameterDoublePrecision:
		value, ok := arg.Value.(float64)
		if !ok {
			return nil, errors.New("a double precision parameter must carry a float64")
		}
		return value, nil
	case command.PostgreSQLParameterBoolean:
		value, ok := arg.Value.(bool)
		if !ok {
			return nil, errors.New("a boolean parameter must carry a bool")
		}
		return value, nil
	case command.PostgreSQLParameterDate:
		value, ok := arg.Value.(time.Time)
		if !ok {
			return nil, errors.New("a date parameter must carry a time.Time")
		}
		if value.Hour() != 0 || value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
			return nil, errors.New("a date parameter must carry a midnight calendar time; absolute instants belong on the timestamp with time zone type")
		}
		return value, nil
	case command.PostgreSQLParameterTimestamptz:
		value, ok := arg.Value.(time.Time)
		if !ok {
			return nil, errors.New("a timestamp with time zone parameter must carry a time.Time")
		}
		return value, nil
	case command.PostgreSQLParameterJSON, command.PostgreSQLParameterJSONB:
		raw, ok := jsonBytes(arg.Value)
		if !ok {
			return nil, errors.New("a json or jsonb parameter must carry a string or json.RawMessage")
		}
		if !json.Valid(raw) {
			return nil, errors.New("a json or jsonb parameter must carry valid JSON")
		}
		return json.RawMessage(raw), nil
	default:
		return nil, fmt.Errorf("unregistered parameter type %q", string(arg.Type))
	}
}

func jsonBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case json.RawMessage:
		return []byte(typed), true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}

func decodeColumn(target any) any {
	switch typed := target.(type) {
	case *json.RawMessage:
		raw := *typed
		if raw == nil {
			return nil
		}
		buf := make([]byte, len(raw))
		copy(buf, raw)
		return json.RawMessage(buf)
	case *any:
		value := *typed
		if raw, ok := value.([]byte); ok {
			buf := make([]byte, len(raw))
			copy(buf, raw)
			return buf
		}
		return value
	default:
		return nil
	}
}

func classifyExecution(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if cause := ctx.Err(); cause != nil && !errors.Is(err, cause) {
			return &QueryError{Op: op, Err: cause}
		}
	}
	if translated, ok := storepostgres.TranslateError(err); ok {
		return &QueryError{Op: op, Err: translated}
	}
	return &QueryError{Op: op, Err: err}
}
