package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMain runs this ticket's zero-leftover audit after m.Run(), so file
// order is irrelevant. An audit placed as a test function is vacuous for
// resources created by tests defined AFTER it in the same file and by
// every test in files that sort later (Go runs tests in source order
// within a file and files in name order) — the ordering trap this replaces.
//
// The LIKE patterns deliberately keep the unescaped underscore: the
// wildcard usefully also covers the verifier's verify_sql05v_* namespace;
// escaping it would narrow the audit out from under that leg.
//
// pg-sql-06's delta-scoped fixture audit (traversal_test.go
// trvRunDeltaAudit) wraps m.Run here; it snapshots the disjoint
// pgir02_it_* namespace around the run, so both audits see the full
// package run state. pg-sql-08's disjoint sets08_it_* namespace audit
// (sets_live_test.go sets08RunDeltaAudit) wraps that, so each ticket owns
// exactly its own prefix (sets_live_test.go).
func TestMain(m *testing.M) {
	code := sets08RunDeltaAudit(m)
	if err := auditNoLeftoverVerifySQL05(); err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit FAILED: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// auditNoLeftoverVerifySQL05 asserts that no verify_sql05_* database or
// schema survives the whole-package run.
func auditNoLeftoverVerifySQL05() error {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_sql05_%'").Scan(&databases); err != nil {
		return fmt.Errorf("count databases: %w", err)
	}
	if databases != 0 {
		return fmt.Errorf("%d verify_sql05_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_sql05_%'").Scan(&schemas); err != nil {
		return fmt.Errorf("count schemas: %w", err)
	}
	if schemas != 0 {
		return fmt.Errorf("%d verify_sql05_* schemas still exist in the service database", schemas)
	}
	return nil
}
