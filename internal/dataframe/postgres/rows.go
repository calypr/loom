// Package postgres adapts the execution row-visitor boundary to PostgreSQL 18.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package streams result rows for one
// parent-tagged compiler.CompiledCommand through the backend-tagged dispatch
// surface: an AQL-tagged or unregistered command is rejected before any
// statement executes, SQL arrives only through the tagged command, and the
// adapter never translates AQL into SQL or falls back to another backend on
// its own. Schema/migrations and SQL rendering are owned elsewhere.
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

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// RowVisitor consumes one decoded result row. The adapter hands each row its
// own freshly allocated map with copied byte slices, so a visitor may retain
// the map after returning without aliasing driver buffers.
type RowVisitor func(map[string]any) error

// QueryRows is the execution-visitor boundary signature for PostgreSQL result
// streaming: one parent-tagged compiled command, the fixed declared result
// column contract, and a row visitor. It mirrors the ClickHouseQueryRows
// boundary while keeping the statement and its ordered typed arguments inside
// the tagged command envelope: execution never hands loose SQL text or a
// named bind map to a PostgreSQL adapter. (*Executor).QueryRows satisfies
// this signature.
type QueryRows func(ctx context.Context, command compiler.CompiledCommand, columns []string, visit RowVisitor) error

// Querier is the narrow, pool-shaped surface the Executor needs.
// *pgxpool.Pool, *pgxpool.Conn, and *pgx.Conn all satisfy it; production
// wiring supplies connections taken from a readiness-checked
// internal/store/postgres pool. The Executor holds no transport policy of its
// own: context deadlines, cancellation, and pool bounds belong to the Querier.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Sentinel errors for classifying adapter failures without string matching.
var (
	// ErrInvalidExecution reports a rejected execution request before any
	// statement could run: a nil context, a nil row visitor, a nil Querier,
	// or a declared result column set that is empty, blank, or duplicated.
	ErrInvalidExecution = errors.New("postgres rows: invalid execution")
	// ErrMalformedStatement reports a statement/argument shape defect that
	// would bind positional parameters unsafely: placeholders outside the
	// declared argument range or an argument that never appears as a
	// placeholder.
	ErrMalformedStatement = errors.New("postgres rows: malformed statement")
	// ErrInvalidParameter reports a tagged command whose ordered argument
	// violates its declared PostgreSQL parameter type, including a non-nil
	// value on an SQL-null parameter, a value of the wrong Go type, a date
	// carrying a time-of-day component, or JSON that does not parse. The
	// three-state contract is enforced here: SQL NULL is bound only by a
	// null-typed parameter with a nil value, a JSON null is a valid JSON
	// value on a json/jsonb parameter, and a missing value is no argument
	// at all; the adapter never flattens one into another.
	ErrInvalidParameter = errors.New("postgres rows: invalid parameter")
	// ErrColumnContractMismatch reports that the server's result columns do
	// not exactly match the declared column contract (count, order, or
	// name). No row is visited when this fires.
	ErrColumnContractMismatch = errors.New("postgres rows: column contract mismatch")
	// ErrQuery classifies failures at the execution seam: statement start,
	// row scanning, or mid-stream result errors. Every QueryError also
	// unwraps to its identifiable driver or context cause for errors.Is and
	// errors.As.
	ErrQuery = errors.New("postgres rows: query failed")
)

// QueryError wraps one execution-seam failure. errors.Is(err, ErrQuery)
// classifies it as an execution failure while the cause chain stays intact:
// errors.As(err, &pgErr) reaches the *pgconn.PgError for SQL errors, and
// errors.Is(err, context.Canceled) / errors.Is(err, context.DeadlineExceeded)
// still recognize cancellation.
type QueryError struct {
	// Op is the failing seam: "query", "scan", or "rows".
	Op string
	// Err is the underlying driver, server, or context error.
	Err error
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("postgres rows: %s: %v", e.Op, e.Err)
}

// Unwrap exposes both the class sentinel and the cause for errors.Is/As.
func (e *QueryError) Unwrap() []error { return []error{ErrQuery, e.Err} }

// Executor streams result rows of parent-tagged PostgreSQL compiled commands
// through the execution visitor boundary. The zero value is not usable; build
// one with NewExecutor.
type Executor struct {
	querier Querier
}

// NewExecutor returns an Executor that obtains result streams from querier.
// It fails closed on a nil Querier rather than inventing a connection.
func NewExecutor(querier Querier) (*Executor, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: querier is required", ErrInvalidExecution)
	}
	return &Executor{querier: querier}, nil
}

var _ QueryRows = (*Executor)(nil).QueryRows

// QueryRows dispatches exactly one parent-tagged PostgreSQL command, streams
// its result rows, and visits decoded rows as they arrive.
//
// Dispatch is backend-tagged: a command tagged for AQL is rejected with the
// compiler's ErrCompiledCommandBackendMismatch and an unregistered tag with
// ErrCompiledCommandBackendNotRegistered, both before the Querier is touched;
// this adapter never executes rendered AQL and performs no AQL-to-SQL
// translation. SQL text and ordered typed parameters arrive only through
// command.PostgreSQLParameters and are passed to the driver unchanged; bound
// values are parameterized, so SQL punctuation inside a value remains a
// value and cannot change query shape.
//
// The server's result columns must match columns exactly (count, order, and
// name) before the first row is visited. Each visited row map is keyed by
// the declared column names and carries decoded values: SQL NULL decodes to
// nil, a JSON null inside a json/jsonb column decodes to a non-nil
// json.RawMessage("null"), and the two are never flattened into each other.
//
// A row visitor error stops iteration immediately, closes the rows, and is
// returned unchanged so callers keep its identity. Context cancellation or
// deadline expiry during execution returns an error that satisfies
// errors.Is against the matching context error in addition to ErrQuery.
// Statement, scan, and server errors return a *QueryError that matches
// ErrQuery and retains the identifiable cause (for example
// errors.As(err, &*pgconn.PgError{})).
func (e *Executor) QueryRows(ctx context.Context, command compiler.CompiledCommand, columns []string, visit RowVisitor) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidExecution)
	}
	if e == nil || e.querier == nil {
		return fmt.Errorf("%w: executor has no querier", ErrInvalidExecution)
	}
	if visit == nil {
		return fmt.Errorf("%w: row visitor is required", ErrInvalidExecution)
	}
	if err := validateColumns(columns); err != nil {
		return err
	}

	// Backend-tagged dispatch fails closed on an AQL-tagged command, an
	// unregistered tag, and the zero value before anything touches the
	// Querier. The returned argument slice is already a copy.
	sqlText, args, err := command.PostgreSQLParameters()
	if err != nil {
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

// streamRows validates the column contract against the server's field
// descriptions as soon as the first row is available, then decodes and
// visits rows until exhaustion, a visitor error, or an execution-seam
// failure. A stream that fails before producing a row is judged by its
// error, not by its (never-received) field descriptions.
func streamRows(ctx context.Context, rows pgx.Rows, columns []string, visit RowVisitor) error {
	defer rows.Close()

	var targets []any
	for rows.Next() {
		if targets == nil {
			descriptions := rows.FieldDescriptions()
			if err := checkColumnContract(columns, descriptions); err != nil {
				return err
			}
			// json/jsonb columns decode to raw JSON bytes so a JSON null
			// stays distinguishable from SQL NULL; every other column uses
			// the driver's default decoded type (int64, string, float64,
			// bool, time.Time, ...).
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
			// The visitor's error is returned unchanged; the deferred
			// Close stops iteration and releases the rows.
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return classifyExecution(ctx, "rows", err)
	}
	if targets == nil {
		// The stream completed without a row; the contract is still a
		// positive requirement, verified against the server's descriptions.
		if err := checkColumnContract(columns, rows.FieldDescriptions()); err != nil {
			return err
		}
	}
	return nil
}

// validateColumns checks the declared result column contract syntactically
// so a broken contract fails before any statement runs.
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

// checkColumnContract requires the server's returned columns to equal the
// declared contract exactly, in count, order, and name.
func checkColumnContract(columns []string, descriptions []pgconn.FieldDescription) error {
	if len(descriptions) != len(columns) {
		return fmt.Errorf("%w: statement returns %d column(s), contract declares %d",
			ErrColumnContractMismatch, len(descriptions), len(columns))
	}
	for i, column := range columns {
		if got := descriptions[i].Name; got != column {
			return fmt.Errorf("%w: result column %d is %q, contract declares %q",
				ErrColumnContractMismatch, i+1, got, column)
		}
	}
	return nil
}

var statementPlaceholderRE = regexp.MustCompile(`\$\d+`)

// validateStatementShape keeps positional binding honest: every $n in the
// rendered statement must address a declared argument, and every declared
// argument must appear as a placeholder. Placeholders may repeat; a hole in
// the numbering means the renderer and the argument list disagree and the
// command is refused before execution.
func validateStatementShape(sqlText string, args []compiler.PostgreSQLParameter) error {
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

// bindParameters canonicalizes every ordered argument according to its
// declared compiler parameter type. This is the adapter's single canonical
// source for the three-state value contract; nothing is approximated on the
// SQL side and the driver receives canonical Go values only.
func bindParameters(args []compiler.PostgreSQLParameter) ([]any, error) {
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

func bindParameter(arg compiler.PostgreSQLParameter) (any, error) {
	switch arg.Type {
	case compiler.PostgreSQLParameterNull:
		if arg.Value != nil {
			return nil, errors.New("an sql-null parameter must carry a nil value; a JSON null belongs on a json or jsonb parameter")
		}
		return nil, nil
	case compiler.PostgreSQLParameterText:
		value, ok := arg.Value.(string)
		if !ok {
			return nil, errors.New("a text parameter must carry a string")
		}
		return value, nil
	case compiler.PostgreSQLParameterBigint:
		value, ok := arg.Value.(int64)
		if !ok {
			return nil, errors.New("a bigint parameter must carry an int64")
		}
		return value, nil
	case compiler.PostgreSQLParameterDoublePrecision:
		value, ok := arg.Value.(float64)
		if !ok {
			return nil, errors.New("a double precision parameter must carry a float64")
		}
		return value, nil
	case compiler.PostgreSQLParameterBoolean:
		value, ok := arg.Value.(bool)
		if !ok {
			return nil, errors.New("a boolean parameter must carry a bool")
		}
		return value, nil
	case compiler.PostgreSQLParameterDate:
		value, ok := arg.Value.(time.Time)
		if !ok {
			return nil, errors.New("a date parameter must carry a time.Time")
		}
		if value.Hour() != 0 || value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
			return nil, errors.New("a date parameter must carry a midnight calendar time; absolute instants belong on the timestamp with time zone type")
		}
		return value, nil
	case compiler.PostgreSQLParameterTimestamptz:
		value, ok := arg.Value.(time.Time)
		if !ok {
			return nil, errors.New("a timestamp with time zone parameter must carry a time.Time")
		}
		return value, nil
	case compiler.PostgreSQLParameterJSON, compiler.PostgreSQLParameterJSONB:
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

// decodeColumn converts one scan target into the visited row value, copying
// driver-owned byte buffers so a retained row map never aliases a connection
// read buffer. A SQL NULL decodes to nil; a JSON null decodes to a non-nil
// json.RawMessage("null").
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

// classifyExecution wraps one execution-seam failure. When the caller's
// context is already done, the verdict is pinned to the context error so a
// cancellation always satisfies errors.Is against context.Canceled or
// context.DeadlineExceeded even when the driver surfaces an unrelated
// transport error after the abort; otherwise the driver/server error is
// retained as the cause.
func classifyExecution(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if cause := ctx.Err(); cause != nil && !errors.Is(err, cause) {
			return &QueryError{Op: op, Err: cause}
		}
	}
	return &QueryError{Op: op, Err: err}
}
