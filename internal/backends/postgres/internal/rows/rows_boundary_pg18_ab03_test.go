package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/execution"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	storepg "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ab03Prefix = "verify_ab03_boundary_"

var (
	ab03NamePattern = regexp.MustCompile(`^verify_ab03_boundary_[a-z0-9_]+$`)

	ab03OwnedMu  sync.Mutex
	ab03OwnedDBs []string
)

func ab03URL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 boundary-through witnesses")
	}
	return raw
}

func ab03RandomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate fixture entropy: %v", err)
	}
	return hex.EncodeToString(buf)
}

type ab03Service struct {
	ctx   context.Context
	dsn   string
	scrub func(any) string
	fail  func(step string, err error)
}

func newAB03Service(t *testing.T) *ab03Service {
	t.Helper()
	raw := ab03URL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	var versionNumText string
	var version string
	if err := admin.QueryRow(ctx, "SHOW server_version_num").Scan(&versionNumText); err != nil {
		fail("read server_version_num", err)
	}
	if err := admin.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		fail("read version()", err)
	}
	versionNum, err := strconv.ParseInt(strings.TrimSpace(versionNumText), 10, 64)
	if err != nil {
		fail("parse server_version_num", err)
	}
	if err := storepg.CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required backend: %v", err)
	}

	dbName := fmt.Sprintf("%s%s_%s", ab03Prefix, time.Now().Format("20060102150405"), ab03RandomHex(t))
	if !ab03NamePattern.MatchString(dbName) {
		t.Fatalf("owned database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create owned database", err)
	}
	ab03OwnedMu.Lock()
	ab03OwnedDBs = append(ab03OwnedDBs, dbName)
	ab03OwnedMu.Unlock()
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop owned database %s: %s", dbName, scrub(err))
		}
	})

	svcDSN := *u
	svcDSN.Path = "/" + dbName
	return &ab03Service{ctx: ctx, dsn: svcDSN.String(), scrub: scrub, fail: fail}
}

func (s *ab03Service) ab03Conn(t *testing.T) (*pgx.Conn, *Executor) {
	t.Helper()
	conn, err := pgx.Connect(s.ctx, s.dsn)
	if err != nil {
		s.fail("connect owned database", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	executor, err := NewExecutor(conn)
	if err != nil {
		s.fail("build executor", err)
	}
	return conn, executor
}

func ab03RequireTranslated(t *testing.T, err error, wantOp string, wantCondition storepg.DriverCondition, wantState string) *storepg.TranslatedError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected failure, got nil")
	}
	if !errors.Is(err, ErrQuery) {
		t.Fatalf("error %v does not match ErrQuery", err)
	}
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("error %v is not a *QueryError", err)
	}
	if !strings.HasPrefix(err.Error(), "postgres rows: "+wantOp+":") {
		t.Fatalf("error %q did not surface at seam %q", err, wantOp)
	}
	var translated *storepg.TranslatedError
	if !errors.As(err, &translated) {
		t.Fatalf("error %v lost the boundary classification", err)
	}
	if translated.Condition != wantCondition || translated.SQLState != wantState {
		t.Fatalf("boundary classification = (%s, %q), want (%s, %q) in error %v",
			translated.Condition, translated.SQLState, wantCondition, wantState, err)
	}
	return translated
}

func ab03RequireCause(t *testing.T, err error, translated *storepg.TranslatedError, wantState, wantMessageFragment string) {
	t.Helper()
	if strings.Contains(err.Error(), wantMessageFragment) {
		t.Fatalf("public error %q discloses server text %q", err, wantMessageFragment)
	}
	var userErr, isUser = dataframeerrors.AsUserError(err)
	if isUser && strings.Contains(userErr.Error(), wantMessageFragment) {
		t.Fatalf("user error rendering %q discloses server text %q", userErr.Error(), wantMessageFragment)
	}
	var pgErr *pgconn.PgError
	if !errors.As(translated.Cause(), &pgErr) {
		t.Fatalf("translated error lost the *pgconn.PgError cause through Cause(): %v", err)
	}
	if pgErr.SQLState() != wantState {
		t.Fatalf("cause SQLSTATE = %q, want %q", pgErr.SQLState(), wantState)
	}
	if !strings.Contains(pgErr.Message, wantMessageFragment) {
		t.Fatalf("cause message %q does not contain %q", pgErr.Message, wantMessageFragment)
	}
}

func ab03RequireCode(t *testing.T, err error, want dataframeerrors.ErrorCode) {
	t.Helper()
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("error %v carries no Loom user-error identity", err)
	}
	if userErr.Code() != string(want) {
		t.Fatalf("code = %s, want %s", userErr.Code(), want)
	}
}

func TestAB03BoundaryThroughQueryCanceled(t *testing.T) {
	svc := newAB03Service(t)
	conn, executor := svc.ab03Conn(t)
	_, controlExecutor := svc.ab03Conn(t)

	rows := 0
	if err := controlExecutor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint AS one UNION ALL SELECT 2::bigint`), DeclaredColumns: []string{"one"}}, func(map[string]any) error { rows++; return nil }); err != nil {
		svc.fail("happy-stream control", err)
	}
	if rows != 2 {
		t.Fatalf("control streamed %d rows, want 2", rows)
	}

	if _, err := conn.Exec(svc.ctx, `SET statement_timeout = '80ms'`); err != nil {
		svc.fail("set statement_timeout", err)
	}
	err := executor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT pg_sleep(5) AS ab03_sleep_witness`), DeclaredColumns: []string{"ab03_sleep_witness"}}, func(map[string]any) error { return nil })
	translated := ab03RequireTranslated(t, err, "rows", storepg.ConditionQueryCanceled, "57014")
	ab03RequireCode(t, err, dataframeerrors.CodeQueryResourceLimitExceeded)
	ab03RequireCause(t, err, translated, "57014", "canceling statement due to statement timeout")
	if strings.Contains(err.Error(), "pg_sleep") || strings.Contains(err.Error(), "ab03_sleep_witness") {
		t.Fatalf("public error %q discloses statement text", err)
	}

	reapplied, ok := storepg.TranslateError(err)
	if !ok {
		t.Fatal("re-applying the boundary to a classified error reported not-owned")
	}
	if reapplied != err {
		t.Fatalf("re-application changed the error identity: %v", reapplied)
	}
	var again *storepg.TranslatedError
	if !errors.As(reapplied, &again) || again != translated {
		t.Fatal("re-application changed the classification identity")
	}
}

func TestAB03BoundaryThroughServerRejected(t *testing.T) {
	svc := newAB03Service(t)
	_, executor := svc.ab03Conn(t)

	const missingRelation = "verify_ab03_missing_relation"
	err := executor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT id FROM `+missingRelation), DeclaredColumns: []string{"id"}}, func(map[string]any) error { return nil })
	translated := ab03RequireTranslated(t, err, "query", storepg.ConditionServerRejected, "42P01")
	ab03RequireCode(t, err, dataframeerrors.CodeInternalError)
	ab03RequireCause(t, err, translated, "42P01", `relation "`+missingRelation+`" does not exist`)

	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatal("boundary error carries no user-error identity")
	}
	for _, key := range []string{"backend", "condition", "sqlstate"} {
		if _, present := userErr.Details()[key]; !present {
			t.Fatalf("public details missing %q: %#v", key, userErr.Details())
		}
	}
	if _, leaked := userErr.Details()["message"]; leaked {
		t.Fatalf("public details carry a server message: %#v", userErr.Details())
	}
}

func TestAB03BoundaryThroughAbortedTransaction(t *testing.T) {
	svc := newAB03Service(t)
	conn, executor := svc.ab03Conn(t)

	if _, err := conn.Exec(svc.ctx, `BEGIN`); err != nil {
		svc.fail("begin transaction", err)
	}
	if _, err := conn.Exec(svc.ctx, `SELECT 1/0 AS ab03_abort`); err == nil {
		t.Fatal("abort control statement unexpectedly succeeded")
	}
	err := executor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint AS one`), DeclaredColumns: []string{"one"}}, func(map[string]any) error { return nil })
	translated := ab03RequireTranslated(t, err, "query", storepg.ConditionAbortedTransaction, "25P02")
	ab03RequireCode(t, err, dataframeerrors.CodeInternalError)
	ab03RequireCause(t, err, translated, "25P02", "current transaction is aborted")
	if !storepg.IsAbortedTransaction(err) {
		t.Fatalf("adapter error not recognized by IsAbortedTransaction: %v", err)
	}

	if _, err := conn.Exec(svc.ctx, `ROLLBACK`); err != nil {
		svc.fail("rollback", err)
	}
	streamed := 0
	if err := executor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint AS one`), DeclaredColumns: []string{"one"}}, func(map[string]any) error { streamed++; return nil }); err != nil {
		svc.fail("post-rollback control", err)
	}
	if streamed != 1 {
		t.Fatalf("post-rollback control streamed %d rows, want 1", streamed)
	}
}

func TestAB03BoundaryThroughAdminShutdown(t *testing.T) {
	svc := newAB03Service(t)
	_, executor := svc.ab03Conn(t)
	terminator, err := pgx.Connect(svc.ctx, svc.dsn)
	if err != nil {
		svc.fail("connect terminator", err)
	}
	defer terminator.Close(context.Background())

	marker := fmt.Sprintf("/*ab03-terminate-%s*/", ab03RandomHex(t))
	errc := make(chan error, 1)
	go func() {
		errc <- executor.SourceRows(svc.ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT `+marker+` pg_sleep(10) AS ab03_tick`), DeclaredColumns: []string{"ab03_tick"}}, func(map[string]any) error { return nil })
	}()

	var pid int
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if err := terminator.QueryRow(svc.ctx,
			`SELECT pid FROM pg_stat_activity WHERE state = 'active' AND query LIKE $1 LIMIT 1`,
			`%`+marker+`%`).Scan(&pid); err == nil {
			break
		}
		select {
		case err := <-errc:
			t.Fatalf("in-flight query ended before it could be terminated: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("never observed the in-flight backend")
	}
	if _, err := terminator.Exec(svc.ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		svc.fail("terminate in-flight backend", err)
	}
	err = <-errc
	translated := ab03RequireTranslated(t, err, "rows", storepg.ConditionAdminShutdown, "57P01")
	ab03RequireCode(t, err, dataframeerrors.CodeBackendUnavailable)
	if !errors.Is(err, storepg.ErrConnect) {
		t.Fatalf("admin shutdown lost the ErrConnect sentinel: %v", err)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || !userErr.Retryable() {
		t.Fatalf("admin shutdown must be retryable backend-unavailable: %v", err)
	}
	ab03RequireCause(t, err, translated, "57P01", "terminating connection due to administrator command")
}

func TestAB03BoundaryThroughConnectionFailure(t *testing.T) {
	svc := newAB03Service(t)
	pool, err := pgxpool.New(svc.ctx, `postgres://ab03:ab03@127.0.0.1:1/ab03_none?connect_timeout=2`)
	if err != nil {
		svc.fail("build unreachable pool", err)
	}
	defer pool.Close()
	executor, err := NewExecutor(pool)
	if err != nil {
		svc.fail("build pool executor", err)
	}
	queryCtx, cancel := context.WithTimeout(svc.ctx, 30*time.Second)
	defer cancel()
	err = executor.SourceRows(queryCtx, execution.RowsRequest{Command: pgSQL(t, `SELECT 1::bigint AS one`), DeclaredColumns: []string{"one"}}, func(map[string]any) error { return nil })
	translated := ab03RequireTranslated(t, err, "query", storepg.ConditionConnectionFailure, "")
	ab03RequireCode(t, err, dataframeerrors.CodeBackendUnavailable)
	if !errors.Is(err, storepg.ErrConnect) {
		t.Fatalf("connection failure lost the ErrConnect sentinel: %v", err)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || !userErr.Retryable() {
		t.Fatalf("connection failure must be retryable: %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "ab03:") {
		t.Fatalf("public error %q discloses transport or credential material", err)
	}
	if translated.Cause() == nil {
		t.Fatal("connection failure lost its cause")
	}
}

func TestAB03BoundaryThroughContextCancel(t *testing.T) {
	svc := newAB03Service(t)
	_, executor := svc.ab03Conn(t)

	ctx, cancel := context.WithCancel(svc.ctx)
	errc := make(chan error, 1)
	go func() {
		errc <- executor.SourceRows(ctx, execution.RowsRequest{Command: pgSQL(t, `SELECT pg_sleep(10) AS one`), DeclaredColumns: []string{"one"}}, func(map[string]any) error { return nil })
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	err := <-errc
	if err == nil {
		t.Fatal("canceled query unexpectedly succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query not Is-compatible with context.Canceled: %v", err)
	}
	var translated *storepg.TranslatedError
	if errors.As(err, &translated) && translated.Condition != storepg.ConditionRequestCanceled {
		t.Fatalf("caller cancellation invented vendor condition %s", translated.Condition)
	}
}

func TestZZZeroLeftoverAB03Fixtures(t *testing.T) {
	raw := ab03URL(t)
	ab03OwnedMu.Lock()
	owned := append([]string(nil), ab03OwnedDBs...)
	ab03OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_ab03_boundary_* fixtures")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for _, db := range owned {
		if !strings.HasPrefix(db, ab03Prefix) {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
		}
	}
	var survivors []string
	deadline := time.Now().Add(120 * time.Second)
	for {
		survivors = nil
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1 ESCAPE '\\'", `verify\_ab03\_%`)
		if err != nil {
			t.Fatalf("prefix audit scan: %v", err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("prefix audit scan: %v", err)
			}
			known := false
			for _, db := range owned {
				if db == name {
					known = true
					break
				}
			}
			if !known {
				survivors = append(survivors, name)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("prefix audit scan: %v", err)
		}
		rows.Close()
		if len(survivors) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if len(survivors) != 0 {
		t.Errorf("verify_ab03_* survivor databases after grace period: %v", survivors)
	}
}
