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
// exactly its own prefix (sets_live_test.go). pg-sql-15's delta-scoped
// audit (unpivot_test.go unpRunDeltaAudit) wraps that pair around the run
// over its own disjoint verify_sql15_* namespace, and pg-sql-20's
// delta-scoped audit (graph_test.go graph20RunDeltaAudit) wraps that trio
// around the run over its own disjoint verify_sql20_* namespace, composing
// four independent delta audits. pg-sql-11's delta-scoped audit
// (objects_test.go obj11RunDeltaAudit) wraps that quartet around the run
// over its own disjoint obj11_it_* namespace — and asserts the namespace
// starts empty, since this ticket owns the prefix alone (ruling D5).
// The verify_* prefixes are audited through one parameterized helper:
// verify_sql05_ (pg-sql-05) and verify_sql10_ (calls_live_test.go,
// pg-sql-10).
func TestMain(m *testing.M) {
	code := obj11RunDeltaAudit(func() int {
		return graph20RunDeltaAudit(func() int { return unpRunDeltaAudit(func() int { return sets08RunDeltaAudit(m) }) })
	})
	for _, prefix := range []string{"verify_sql05_", "verify_sql10_"} {
		if err := auditNoLeftoverVerifyNamespace(prefix); err != nil {
			fmt.Fprintf(os.Stderr, "zero-leftover audit FAILED: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

// auditNoLeftoverVerifyNamespace asserts that no database or schema under
// the given ticket prefix survives the whole-package run.
func auditNoLeftoverVerifyNamespace(prefix string) error {
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE $1", prefix+"%").Scan(&databases); err != nil {
		return fmt.Errorf("count databases: %w", err)
	}
	if databases != 0 {
		return fmt.Errorf("%d %s* databases still exist", databases, prefix)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE $1", prefix+"%").Scan(&schemas); err != nil {
		return fmt.Errorf("count schemas: %w", err)
	}
	if schemas != 0 {
		return fmt.Errorf("%d %s* schemas still exist in the service database", schemas, prefix)
	}
	return nil
}
