package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/dataframe/execution"
	"strings"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type scriptedRows struct {
	fds       []pgconn.FieldDescription
	rowValues [][]any
	nextIndex int
	closed    bool
	closeRuns int
	scanErr   error
	err       error
	shared    [][]byte
}

func (r *scriptedRows) Close() { r.closed = true; r.closeRuns++ }
func (r *scriptedRows) Err() error {
	return r.err
}
func (r *scriptedRows) CommandTag() pgconn.CommandTag { return pgconn.NewCommandTag("SELECT") }
func (r *scriptedRows) FieldDescriptions() []pgconn.FieldDescription {
	return r.fds
}
func (r *scriptedRows) Next() bool {
	if r.closed {
		return false
	}
	if r.nextIndex < len(r.rowValues) {
		r.nextIndex++
		return true
	}
	r.closed = true
	return false
}
func (r *scriptedRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.nextIndex < 1 || r.nextIndex > len(r.rowValues) {
		return errors.New("scriptedRows.Scan called without a current row")
	}
	values := r.rowValues[r.nextIndex-1]
	if len(values) != len(dest) {
		return errors.New("scriptedRows.Scan: destination arity mismatch")
	}
	for i, target := range dest {
		switch typed := target.(type) {
		case *any:
			*typed = values[i]
		case *json.RawMessage:
			if values[i] == nil {
				*typed = nil
				continue
			}
			raw, ok := values[i].([]byte)
			if !ok {
				return errors.New("scriptedRows.Scan: json target needs []byte")
			}
			*typed = json.RawMessage(raw)
		default:
			return errors.New("scriptedRows.Scan: unsupported destination")
		}
	}
	return nil
}
func (r *scriptedRows) Values() ([]any, error) {
	if r.nextIndex < 1 || r.nextIndex > len(r.rowValues) {
		return nil, errors.New("scriptedRows.Values called without a current row")
	}
	return r.rowValues[r.nextIndex-1], nil
}
func (r *scriptedRows) RawValues() [][]byte  { return r.shared }
func (r *scriptedRows) Conn() *pgx.Conn      { return nil }
func (r *scriptedRows) TypeMap() *pgtype.Map { return pgtype.NewMap() }

type recordingQuerier struct {
	calls           int
	lastSQL         string
	lastArgs        []any
	rows            pgx.Rows
	queryErr        error
	rowsEvenOnError bool
}

func (q *recordingQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.calls++
	q.lastSQL = sql
	q.lastArgs = args
	if q.queryErr != nil {
		if q.rowsEvenOnError {
			return q.rows, q.queryErr
		}
		return nil, q.queryErr
	}
	return q.rows, nil
}

func textFD(name string) pgconn.FieldDescription {
	return pgconn.FieldDescription{Name: name, DataTypeOID: pgtype.TextOID}
}

func jsonbFD(name string) pgconn.FieldDescription {
	return pgconn.FieldDescription{Name: name, DataTypeOID: pgtype.JSONBOID}
}

func mustPostgreSQLCommand(t *testing.T, sql string, args ...command.PostgreSQLParameter) command.CompiledCommand {
	t.Helper()
	compiledCommand, err := command.NewPostgreSQLCommand(sql, args)
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand(%q): %v", sql, err)
	}
	return compiledCommand
}

func requireNamed(t *testing.T, err error, sentinel error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected refusal matching %v, got nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("refusal %v does not match sentinel %v", err, sentinel)
	}
	for _, fragment := range want {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal %q does not name %q", err, fragment)
		}
	}
}

func TestNewExecutorRejectsNilQuerier(t *testing.T) {
	_, err := NewExecutor(nil)
	requireNamed(t, err, ErrInvalidExecution, "querier is required")
}

func TestSourceRowsRequestGuardsRefuseBeforeQuerier(t *testing.T) {
	pgSQL := mustPostgreSQLCommand(t, `SELECT id FROM t WHERE id = $1`,
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(1)})
	validVisit := func(map[string]any) error { return nil }

	cases := []struct {
		name         string
		ctx          context.Context
		visit        RowVisitor
		columns      []string
		wantSentinel error
		want         []string
	}{
		{"nil context", nil, validVisit, []string{"id"}, ErrInvalidExecution, []string{"context is required"}},
		{"nil visitor", context.Background(), nil, []string{"id"}, ErrInvalidExecution, []string{"row visitor is required"}},
		{"no declared columns", context.Background(), validVisit, nil, ErrInvalidExecution, []string{"at least one declared result column"}},
		{"blank declared column", context.Background(), validVisit, []string{"id", "  "}, ErrInvalidExecution, []string{"column 2 is blank"}},
		{"duplicate declared column", context.Background(), validVisit, []string{"id", "id"}, ErrInvalidExecution, []string{`declared result column "id" is declared twice`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier := &recordingQuerier{}
			executor, err := NewExecutor(querier)
			if err != nil {
				t.Fatal(err)
			}
			err = executor.SourceRows(tc.ctx, execution.RowsRequest{Command: pgSQL, DeclaredColumns: tc.columns}, tc.visit)
			requireNamed(t, err, tc.wantSentinel, tc.want...)
			if querier.calls != 0 {
				t.Fatalf("guard let the statement reach the driver (%d calls)", querier.calls)
			}
		})
	}

	querier := &recordingQuerier{rows: &scriptedRows{fds: []pgconn.FieldDescription{textFD("id")}}}
	executor, err := NewExecutor(querier)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: pgSQL, DeclaredColumns: []string{"id"}}, validVisit); err != nil {
		t.Fatalf("control request refused: %v", err)
	}
	if querier.calls != 1 {
		t.Fatalf("control executed %d times, want 1", querier.calls)
	}

	var zero Executor
	if err := zero.SourceRows(context.Background(), execution.RowsRequest{Command: pgSQL, DeclaredColumns: []string{"id"}}, validVisit); err == nil || !errors.Is(err, ErrInvalidExecution) {
		t.Fatalf("zero-value executor did not refuse: %v", err)
	}
}

func TestSourceRowsDispatchIsBackendTagged(t *testing.T) {
	visit := func(map[string]any) error { return nil }
	const sql = `SELECT id FROM t`

	t.Run("AQL-tagged command is refused by name", func(t *testing.T) {
		aql, err := command.NewAQLCommand(`FOR d IN t RETURN d`, map[string]any{"x": 1})
		if err != nil {
			t.Fatal(err)
		}
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err = executor.SourceRows(context.Background(), execution.RowsRequest{Command: aql, DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, command.ErrCompiledCommandBackendMismatch, "refusing to pass AQL query to the PostgreSQL statement executor")
		if querier.calls != 0 {
			t.Fatalf("rendered AQL reached the PostgreSQL driver (%d calls)", querier.calls)
		}
	})

	t.Run("zero-value command is refused as unregistered", func(t *testing.T) {
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: command.CompiledCommand{}, DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, command.ErrCompiledCommandBackendNotRegistered, `unregistered backend ""`)
		if querier.calls != 0 {
			t.Fatalf("unregistered tag reached the driver (%d calls)", querier.calls)
		}
	})

	querier := &recordingQuerier{rows: &scriptedRows{fds: []pgconn.FieldDescription{textFD("id")}}}
	executor, _ := NewExecutor(querier)
	if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, sql), DeclaredColumns: []string{"id"}}, visit); err != nil {
		t.Fatalf("tagged PostgreSQL control refused: %v", err)
	}
	if querier.calls != 1 || querier.lastSQL != sql {
		t.Fatalf("control dispatch wrong: calls=%d sql=%q", querier.calls, querier.lastSQL)
	}
}

func TestSourceRowsStatementShapeGuards(t *testing.T) {
	visit := func(map[string]any) error { return nil }
	bigint := func(v int64) command.PostgreSQLParameter {
		return command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: v}
	}
	text := func(v string) command.PostgreSQLParameter {
		return command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: v}
	}

	cases := []struct {
		name     string
		sql      string
		args     []command.PostgreSQLParameter
		wantName string
	}{
		{"placeholder without arguments", `SELECT id FROM t WHERE id = $1`, nil, "declares no arguments"},
		{"placeholder above argument range", `SELECT id FROM t WHERE id = $2`, []command.PostgreSQLParameter{bigint(1)}, "$2 is outside the declared argument range 1..1"},
		{"argument never appears", `SELECT id FROM t WHERE id = $1`, []command.PostgreSQLParameter{bigint(1), text("orphan")}, "argument 2 never appears as $2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier := &recordingQuerier{}
			executor, _ := NewExecutor(querier)
			compiledCommand := mustPostgreSQLCommand(t, tc.sql, tc.args...)
			err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"id"}}, visit)
			requireNamed(t, err, ErrMalformedStatement, tc.wantName)
			if querier.calls != 0 {
				t.Fatalf("malformed statement reached the driver (%d calls)", querier.calls)
			}
		})
	}

	for _, control := range []struct {
		sql  string
		args []command.PostgreSQLParameter
	}{
		{`SELECT $1 + $1`, []command.PostgreSQLParameter{bigint(1)}},
		{`SELECT 1`, nil},
	} {
		querier := &recordingQuerier{rows: &scriptedRows{fds: []pgconn.FieldDescription{textFD("id")}}}
		executor, _ := NewExecutor(querier)
		if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, control.sql, control.args...), DeclaredColumns: []string{"id"}}, visit); err != nil {
			t.Fatalf("well-formed statement %q refused: %v", control.sql, err)
		}
		if querier.calls != 1 {
			t.Fatalf("statement %q executed %d times, want 1", control.sql, querier.calls)
		}
	}
}

func TestBindParametersCanonicalizesEveryDeclaredType(t *testing.T) {
	midnight := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	instant := time.Date(2026, 2, 13, 10, 20, 30, 0, time.UTC)
	nonUTCMidnight := time.Date(2026, 2, 13, 0, 0, 0, 0, time.FixedZone("UTC-5", -5*3600))

	cases := []struct {
		name  string
		param command.PostgreSQLParameter
		want  any
	}{
		{"sql-null binds nil", command.PostgreSQLParameter{Type: command.PostgreSQLParameterNull, Value: nil}, any(nil)},
		{"text binds its string", command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "x'y;-- $1"}, any("x'y;-- $1")},
		{"bigint binds int64", command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(42)}, any(int64(42))},
		{"double binds float64", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDoublePrecision, Value: 1.5}, any(1.5)},
		{"boolean binds bool", command.PostgreSQLParameter{Type: command.PostgreSQLParameterBoolean, Value: true}, any(true)},
		{"date binds midnight time", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDate, Value: midnight}, any(midnight)},
		{"date accepts a midnight in its own zone", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDate, Value: nonUTCMidnight}, any(nonUTCMidnight)},
		{"timestamptz binds the instant", command.PostgreSQLParameter{Type: command.PostgreSQLParameterTimestamptz, Value: instant}, any(instant)},
		{"jsonb binds a JSON object", command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: `{"k":1}`}, any(json.RawMessage(`{"k":1}`))},
		{"jsonb binds JSON null as a value", command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage("null")}, any(json.RawMessage("null"))},
		{"json binds a string value", command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSON, Value: `"s"`}, any(json.RawMessage(`"s"`))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier := &recordingQuerier{rows: &scriptedRows{fds: []pgconn.FieldDescription{textFD("x")}}}
			executor, _ := NewExecutor(querier)
			compiledCommand := mustPostgreSQLCommand(t, `SELECT $1`, tc.param)
			if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"x"}}, func(map[string]any) error { return nil }); err != nil {
				t.Fatalf("valid parameter refused: %v", err)
			}
			if len(querier.lastArgs) != 1 {
				t.Fatalf("bound %d arguments, want 1", len(querier.lastArgs))
			}
			if !equalBoundValue(querier.lastArgs[0], tc.want) {
				t.Fatalf("canonical binding = %#v (%T), want %#v (%T)", querier.lastArgs[0], querier.lastArgs[0], tc.want, tc.want)
			}
		})
	}
}

func equalBoundValue(got, want any) bool {
	if want == nil {
		return got == nil
	}
	gotRaw, gotIsRaw := got.(json.RawMessage)
	wantRaw, wantIsRaw := want.(json.RawMessage)
	if gotIsRaw != wantIsRaw {
		return false
	}
	if gotIsRaw {
		return string(gotRaw) == string(wantRaw)
	}
	switch wantTyped := want.(type) {
	case time.Time:
		gotTyped, ok := got.(time.Time)
		return ok && gotTyped.Equal(wantTyped) && gotTyped.Location() == wantTyped.Location()
	default:
		return got == want
	}
}

func TestBindParametersRefusesEveryTypeViolation(t *testing.T) {
	visit := func(map[string]any) error { return nil }
	cases := []struct {
		name     string
		param    command.PostgreSQLParameter
		wantName string
	}{
		{"bigint refuses int", command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int(42)},
			"must carry an int64"},
		{"bigint refuses float64 (no driver-side coercion)", command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: float64(42)},
			"must carry an int64"},
		{"text refuses a []byte", command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: []byte("x")},
			"must carry a string"},
		{"double refuses int64", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDoublePrecision, Value: int64(1)},
			"must carry a float64"},
		{"boolean refuses a string", command.PostgreSQLParameter{Type: command.PostgreSQLParameterBoolean, Value: "true"},
			"must carry a bool"},
		{"date refuses an instant with a time-of-day component", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDate, Value: time.Date(2026, 2, 13, 17, 45, 0, 0, time.UTC)},
			"midnight calendar time"},
		{"date refuses a string", command.PostgreSQLParameter{Type: command.PostgreSQLParameterDate, Value: "2026-02-13"},
			"must carry a time.Time"},
		{"timestamptz refuses a string", command.PostgreSQLParameter{Type: command.PostgreSQLParameterTimestamptz, Value: "2026-02-13T00:00:00Z"},
			"must carry a time.Time"},
		{"jsonb refuses invalid JSON", command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: `{"k":`},
			"must carry valid JSON"},
		{"json refuses a wrong Go shape", command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSON, Value: map[string]any{"k": 1}},
			"must carry a string or json.RawMessage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			querier := &recordingQuerier{}
			executor, _ := NewExecutor(querier)
			compiledCommand := mustPostgreSQLCommand(t, `SELECT $1`, tc.param)
			err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: compiledCommand, DeclaredColumns: []string{"x"}}, visit)
			requireNamed(t, err, ErrInvalidParameter, "argument 1", string(tc.param.Type), tc.wantName)
			if querier.calls != 0 {
				t.Fatalf("invalid parameter reached the driver (%d calls)", querier.calls)
			}
		})
	}
}

func TestBindParameterRefusesWhatTheCompilerConstructorAlsoRefuses(t *testing.T) {
	cases := []struct {
		name     string
		param    command.PostgreSQLParameter
		wantName string
	}{
		{"sql-null with a non-nil value is refused (no SQL-null/JSON-null flattening)",
			command.PostgreSQLParameter{Type: command.PostgreSQLParameterNull, Value: "null"},
			"must carry a nil value"},
		{"unregistered declared type is refused (wrong-name probe)", command.PostgreSQLParameter{Type: "uuid", Value: "x"},
			`unregistered parameter type "uuid"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bindParameters([]command.PostgreSQLParameter{tc.param})
			requireNamed(t, err, ErrInvalidParameter, "argument 1", string(tc.param.Type), tc.wantName)
		})
	}
}

func TestSourceRowsEnforcesTheColumnContract(t *testing.T) {
	querier := &recordingQuerier{rows: &scriptedRows{
		fds: []pgconn.FieldDescription{textFD("id"), jsonbFD("payload")},
		rowValues: [][]any{
			{[]byte("1")},
		},
	}}
	executor, _ := NewExecutor(querier)
	visits := 0
	visit := func(map[string]any) error { visits++; return nil }

	err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id, payload FROM t`), DeclaredColumns: []string{"Id", "payload"}}, visit)
	requireNamed(t, err, ErrColumnContractMismatch, `column 1 is "id", contract declares "Id"`)

	err = executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id, payload FROM t`), DeclaredColumns: []string{"id"}}, visit)
	requireNamed(t, err, ErrColumnContractMismatch, "returns 2 column(s), contract declares 1")

	if visits != 0 {
		t.Fatalf("rows were visited despite a broken contract: %d", visits)
	}
	rows := querier.rows.(*scriptedRows)
	if !rows.closed {
		t.Fatal("rows left open after a column contract refusal")
	}

	rows2 := &scriptedRows{
		fds:       []pgconn.FieldDescription{textFD("id"), jsonbFD("payload")},
		rowValues: [][]any{{[]byte("1"), []byte(`{}`)}},
	}
	querier.rows = rows2
	if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id, payload FROM t`), DeclaredColumns: []string{"id", "payload"}}, visit); err != nil {
		t.Fatalf("exact contract refused: %v", err)
	}
	if visits != 1 {
		t.Fatalf("control visited %d rows, want 1", visits)
	}
	if !rows2.closed {
		t.Fatal("control rows left open")
	}
}

func TestSourceRowsColumnContractPositionalAliasException(t *testing.T) {
	run := func(t *testing.T, contract []string, aliasNames []string) error {
		t.Helper()
		fds := make([]pgconn.FieldDescription, 0, len(aliasNames))
		for _, name := range aliasNames {
			fds = append(fds, textFD(name))
		}
		querier := &recordingQuerier{rows: &scriptedRows{fds: fds}}
		executor, _ := NewExecutor(querier)
		return executor.SourceRows(context.Background(),
			execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT c0, c1 FROM t`), DeclaredColumns: contract},
			func(map[string]any) error { return nil })
	}

	t.Run("PositionalAliasForItsOwnPositionIsAccepted", func(t *testing.T) {
		if err := run(t, []string{"a", "b"}, []string{"c0", "c1"}); err != nil {
			t.Fatalf("positional alias set refused: %v", err)
		}
	})
	t.Run("SwappedAliasPositionsAreRefused", func(t *testing.T) {
		err := run(t, []string{"a", "b"}, []string{"c1", "c0"})
		requireNamed(t, err, ErrColumnContractMismatch, `result column 1 is "c1", contract declares "a"`)
	})
	t.Run("ShiftedAliasPositionsAreRefused", func(t *testing.T) {
		err := run(t, []string{"a", "b"}, []string{"c1", "c2"})
		requireNamed(t, err, ErrColumnContractMismatch, `result column 1 is "c1", contract declares "a"`)
	})
	t.Run("ZeroPaddedAliasIsRefused", func(t *testing.T) {
		err := run(t, []string{"a", "b"}, []string{"c00", "c1"})
		requireNamed(t, err, ErrColumnContractMismatch, `result column 1 is "c00", contract declares "a"`)
	})
	t.Run("ForeignNameStillRefused", func(t *testing.T) {
		err := run(t, []string{"a", "b"}, []string{"x0", "c1"})
		requireNamed(t, err, ErrColumnContractMismatch, `result column 1 is "x0", contract declares "a"`)
	})
}

func TestSourceRowsRoutesByBackendTagBeforeDialectValidation(t *testing.T) {
	visit := func(map[string]any) error { return nil }

	t.Run("AQLTaggedCommandWithoutDeclaredColumnsReportsMismatchNotDialectError", func(t *testing.T) {
		aql, err := command.NewAQLCommand(`FOR d IN t RETURN d`, nil)
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err = executor.SourceRows(context.Background(), execution.RowsRequest{Command: aql}, visit)
		requireNamed(t, err, command.ErrCompiledCommandBackendMismatch, "refusing to pass AQL query to the PostgreSQL statement executor")
		if querier.calls != 0 {
			t.Fatalf("misrouted command reached the querier %d time(s)", querier.calls)
		}
	})

	t.Run("AQLTaggedCommandWithWellFormedColumnsStillReportsMismatch", func(t *testing.T) {
		aql, err := command.NewAQLCommand(`FOR d IN t RETURN d`, nil)
		if err != nil {
			t.Fatalf("NewAQLCommand: %v", err)
		}
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err = executor.SourceRows(context.Background(), execution.RowsRequest{Command: aql, DeclaredColumns: []string{"a", "b"}}, visit)
		requireNamed(t, err, command.ErrCompiledCommandBackendMismatch)
		if querier.calls != 0 {
			t.Fatalf("misrouted command reached the querier %d time(s)", querier.calls)
		}
	})

	t.Run("ZeroTagReportsNotRegisteredBeforeColumnsAreInspected", func(t *testing.T) {
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: command.CompiledCommand{}}, visit)
		requireNamed(t, err, command.ErrCompiledCommandBackendNotRegistered)
		if querier.calls != 0 {
			t.Fatalf("zero-tag command reached the querier %d time(s)", querier.calls)
		}
	})

	t.Run("RoutedPostgreSQLCommandStillValidatesDialect", func(t *testing.T) {
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT $1`, command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "x"})}, visit)
		requireNamed(t, err, ErrInvalidExecution, "at least one declared result column is required")
		if querier.calls != 0 {
			t.Fatalf("dialect refusal still reached the querier %d time(s)", querier.calls)
		}
	})
}

func TestSourceRowsDecodesRowsIntoCopiedMaps(t *testing.T) {
	midnight := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	sharedJSON := []byte(`{"k": 1}`)
	sharedBytes := []byte("raw")

	rows := &scriptedRows{
		fds: []pgconn.FieldDescription{
			textFD("label"),
			{Name: "count", DataTypeOID: pgtype.Int8OID},
			{Name: "ratio", DataTypeOID: pgtype.Float8OID},
			{Name: "flag", DataTypeOID: pgtype.BoolOID},
			{Name: "day", DataTypeOID: pgtype.DateOID},
			jsonbFD("payload"),
			{Name: "note", DataTypeOID: pgtype.JSONOID},
			{Name: "blob", DataTypeOID: pgtype.ByteaOID},
		},
		rowValues: [][]any{
			{"alpha", int64(7), 2.5, true, midnight, sharedJSON, nil, sharedBytes},
			{"beta", int64(0), 0.0, false, midnight, []byte("null"), []byte("null"), nil},
		},
	}
	querier := &recordingQuerier{rows: rows}
	executor, _ := NewExecutor(querier)

	var visited []map[string]any
	err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT label, count, ratio, flag, day, payload, note, blob FROM t`), DeclaredColumns: []string{"label", "count", "ratio", "flag", "day", "payload", "note", "blob"}}, func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if len(visited) != 2 {
		t.Fatalf("visited %d rows, want 2", len(visited))
	}

	first := visited[0]
	if len(first) != 8 {
		t.Fatalf("row key count = %d, want 8", len(first))
	}
	if first["label"] != "alpha" || first["count"] != int64(7) || first["ratio"] != 2.5 || first["flag"] != true {
		t.Fatalf("first row scalars wrong: %#v", first)
	}
	if day, ok := first["day"].(time.Time); !ok || !day.Equal(midnight) {
		t.Fatalf("day decoded as %#v", first["day"])
	}
	payload, ok := first["payload"].(json.RawMessage)
	if !ok || string(payload) != `{"k": 1}` {
		t.Fatalf("payload decoded as %#v", first["payload"])
	}
	if _, isRaw := first["note"].(json.RawMessage); isRaw || first["note"] != nil {
		t.Fatalf("SQL NULL json column decoded as %#v, want nil", first["note"])
	}
	blob, ok := first["blob"].([]byte)
	if !ok || string(blob) != "raw" {
		t.Fatalf("blob decoded as %#v", first["blob"])
	}

	second := visited[1]
	if note, ok := second["note"].(json.RawMessage); !ok || string(note) != "null" {
		t.Fatalf("JSON null decoded as %#v, want json.RawMessage(\"null\")", second["note"])
	}
	if payload, ok := second["payload"].(json.RawMessage); !ok || string(payload) != "null" {
		t.Fatalf("JSONB null decoded as %#v, want json.RawMessage(\"null\")", second["payload"])
	}

	sharedJSON[1] = 'X'
	sharedBytes[0] = 'X'
	if string(visited[0]["payload"].(json.RawMessage)) != `{"k": 1}` {
		t.Fatal("visited payload aliases the driver buffer")
	}
	if string(visited[0]["blob"].([]byte)) != "raw" {
		t.Fatal("visited blob aliases the driver buffer")
	}
}

func TestSourceRowsVisitorErrorStopsIterationAndClosesRows(t *testing.T) {
	boom := errors.New("visitor boom")
	rows := &scriptedRows{
		fds:       []pgconn.FieldDescription{textFD("id")},
		rowValues: [][]any{{[]byte("1")}, {[]byte("2")}, {[]byte("3")}},
	}
	querier := &recordingQuerier{rows: rows}
	executor, _ := NewExecutor(querier)

	visits := 0
	err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, func(map[string]any) error {
		visits++
		if visits == 2 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || err != boom {
		t.Fatalf("visitor error was altered: %#v", err)
	}
	if visits != 2 {
		t.Fatalf("visited %d rows after the visitor stopped; iteration did not stop", visits)
	}
	if !rows.closed {
		t.Fatal("rows left open after a visitor error")
	}

	rows2 := &scriptedRows{fds: rows.fds, rowValues: rows.rowValues}
	querier.rows = rows2
	full := 0
	if err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, func(map[string]any) error { full++; return nil }); err != nil {
		t.Fatalf("control stream failed: %v", err)
	}
	if full != 3 || !rows2.closed {
		t.Fatalf("control drained %d rows (want 3), closed=%v", full, rows2.closed)
	}
}

func TestSourceRowsSeamErrorsKeepClassAndCause(t *testing.T) {
	visit := func(map[string]any) error { return nil }
	fds := []pgconn.FieldDescription{textFD("id")}

	t.Run("mid-stream rows error keeps the cause", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "57014", Message: "query_canceled"}
		querier := &recordingQuerier{rows: &scriptedRows{fds: fds, err: pgErr}}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, ErrQuery, "rows:")
		var translated *storepostgres.TranslatedError
		if !errors.As(err, &translated) || translated.Condition != storepostgres.ConditionQueryCanceled || translated.SQLState != "57014" {
			t.Fatalf("rows error lost the boundary classification: %v", err)
		}
		var got *pgconn.PgError
		if !errors.As(translated.Cause(), &got) || got.SQLState() != "57014" {
			t.Fatalf("translated error lost the server cause through Cause(): %v", err)
		}
	})

	t.Run("scan failure keeps class and cause", func(t *testing.T) {
		scanErr := errors.New("cannot scan")
		querier := &recordingQuerier{rows: &scriptedRows{fds: fds, rowValues: [][]any{{"x"}}, scanErr: scanErr}}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, ErrQuery, "scan:")
		if !errors.Is(err, scanErr) {
			t.Fatalf("scan error lost the cause: %v", err)
		}
	})

	t.Run("cancellation during rows is errors.Is-compatible", func(t *testing.T) {
		querier := &recordingQuerier{rows: &scriptedRows{fds: fds, err: context.Canceled}}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
			t.Fatalf("cancellation not classifiable: %v", err)
		}
	})

	t.Run("non-matching driver error after cancel is pinned to the context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		transportErr := errors.New("conn closed")
		querier := &recordingQuerier{rows: &scriptedRows{fds: fds, err: transportErr}}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(ctx, execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
			t.Fatalf("post-cancel transport error not pinned to the context error: %v", err)
		}
	})

	t.Run("query start error keeps class and cause and closes rows", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "42P01", Message: `relation "t" does not exist`}
		rows := &scriptedRows{fds: fds}
		querier := &recordingQuerier{rows: rows, queryErr: pgErr, rowsEvenOnError: true}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, ErrQuery, "query:")
		var translated *storepostgres.TranslatedError
		if !errors.As(err, &translated) || translated.Condition != storepostgres.ConditionServerRejected || translated.SQLState != "42P01" {
			t.Fatalf("query start error lost the boundary classification: %v", err)
		}
		var got *pgconn.PgError
		if !errors.As(translated.Cause(), &got) || got.SQLState() != "42P01" {
			t.Fatalf("query start error lost the server cause through Cause(): %v", err)
		}
		if !rows.closed {
			t.Fatal("erroring rows handed back by Query were not closed")
		}
	})

	t.Run("defensive: nil rows with nil error is a query failure", func(t *testing.T) {
		querier := &recordingQuerier{}
		executor, _ := NewExecutor(querier)
		err := executor.SourceRows(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT id FROM t`), DeclaredColumns: []string{"id"}}, visit)
		requireNamed(t, err, ErrQuery, "no result set")
	})
}

func TestSourceRowsBoundarySignature(t *testing.T) {
	executor, err := NewExecutor(&recordingQuerier{rows: &scriptedRows{fds: fdsFor("x")}})
	if err != nil {
		t.Fatal(err)
	}
	var boundary execution.SourceRows = executor.SourceRows
	if err := boundary(context.Background(), execution.RowsRequest{Command: mustPostgreSQLCommand(t, `SELECT 1`), DeclaredColumns: []string{"x"}},
		func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("boundary call failed: %v", err)
	}
}

func fdsFor(names ...string) []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(names))
	for i, name := range names {
		out[i] = textFD(name)
	}
	return out
}
