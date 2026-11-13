package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestVerifyStatementErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		name        string
		err         error
		wantConnect bool
	}{
		{"closed connection", net.ErrClosed, true},
		{"protocol truncation", io.ErrUnexpectedEOF, true},
		{"deadline exceeded wrapped by readLedger", fmt.Errorf("postgres: read migration ledger: %w", context.DeadlineExceeded), true},
		{"refused connection", fmt.Errorf("postgres: read migration ledger: %w", errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")), true},
		{"server-answered undefined column", &pgconn.PgError{Code: "42703", Message: "column \"checksum\" does not exist"}, false},
		{"server-answered permission denied wrapped by readLedger", fmt.Errorf("postgres: read migration ledger: %w", &pgconn.PgError{Code: "42501", Message: "permission denied for table schema_migrations"}), false},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := verifyStatementError("verify ledger", tt.err)
			if isConnect := errors.Is(got, ErrConnect); isConnect != tt.wantConnect {
				t.Fatalf("errors.Is(err, ErrConnect) = %v, want %v for %v", isConnect, tt.wantConnect, tt.err)
			}
			if tt.wantConnect {
				var ce *ConnectError
				if !errors.As(got, &ce) || ce.Op != "verify ledger" || !errors.Is(ce, tt.err) {
					t.Fatalf("connectivity fault must join the pool's *ConnectError chain with the cause inspectable, got %#v", got)
				}
			} else {
				if got != tt.err {
					t.Fatalf("server-answered fault must pass through verbatim, got %#v want %#v", got, tt.err)
				}
				var pgErr *pgconn.PgError
				if !errors.As(got, &pgErr) {
					t.Fatalf("server-answered fault must stay a *pgconn.PgError, got %#v", got)
				}
			}
		})
	}
}

func TestVerifyLedgerQueryServerFaultIsNotConnectivity(t *testing.T) {
	svc := newMigrationService(t)
	client := svc.openClient(t)
	if _, err := svc.iso.Exec(svc.ctx,
		`CREATE TABLE "`+svc.schema+`".schema_migrations (version bigint)`); err != nil {
		svc.fail("create wrong-shaped ledger table", err)
	}
	runner := svc.newRunner(t, client, nil)
	err := runner.Verify(svc.ctx)
	if err == nil {
		t.Fatal("Verify over a wrong-shaped ledger table must fail")
	}
	if errors.Is(err, ErrConnect) {
		t.Fatalf("a server-answered ledger query fault must not enter the ErrConnect chain (live connection is not connectivity_down): %s", svc.scrub(err))
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("Verify must pass a server-answered fault through inspectable as *pgconn.PgError, got %#v", err)
	}
	if pgErr.Code != "42703" {
		t.Fatalf("expected undefined-column 42703 from the wrong-shaped ledger, got %s: %s", pgErr.Code, svc.scrub(pgErr))
	}
	if err := client.Ping(svc.ctx); err != nil {
		svc.fail("ping after server-answered fault", err)
	}
}

func TestVerifyNamedArmsUnchangedByStatementClassification(t *testing.T) {
	svc := newMigrationService(t)
	client := svc.openClient(t)
	missing := svc.schema + "_absent"
	runner, err := NewMigrationRunner(client, missing, nil)
	if err != nil {
		svc.fail("construct runner for absent schema", err)
	}
	if err := runner.Verify(svc.ctx); !errors.Is(err, ErrInvalidSchemaName) {
		t.Fatalf("absent schema must classify as ErrInvalidSchemaName, got %#v", err)
	}
	migrations := []Migration{{Version: 1, Name: "pgverify01_probe", SQL: "CREATE TABLE t(x int)"}}
	pendingRunner := svc.newRunner(t, client, migrations)
	if err := pendingRunner.Verify(svc.ctx); !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("empty ledger with a pending migration must classify as ErrMigrationPending, got %#v", err)
	}
}
