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

func TestMain(m *testing.M) {
	code := sql24RunDeltaAudit(func() int {
		return sql22RunDeltaAudit(func() int {
			return sql23RunDeltaAudit(func() int {
				return excl26RunDeltaAudit(func() int {
					return sql25RunDeltaAudit(func() int {
						return sql16RunDeltaAudit(func() int {
							return pivot14RunDeltaAudit(func() int {
								return group13RunDeltaAudit(func() int {
									return sql21RunDeltaAudit(func() int {
										return coded19RunDeltaAudit(func() int {
											return obj11RunDeltaAudit(func() int {
												return graph20RunDeltaAudit(func() int { return unpRunDeltaAudit(func() int { return sets08RunDeltaAudit(m) }) })
											})
										})
									})
								})
							})
						})
					})
				})
			})
		})
	})
	for _, prefix := range []string{"verify_sql05_", "verify_sql10_", "verify_sql10v7_", "verify_sql21v1_", "verify_sql13v1_", "verify_sql14v1_", "verify_sql16v1_", "verify_sql25v1_", "verify_sql26v1_", "verify_sql23v1_", "verify_sql22v1_", "verify_sql24v1_"} {
		if err := auditNoLeftoverVerifyNamespace(prefix); err != nil {
			fmt.Fprintf(os.Stderr, "zero-leftover audit FAILED: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

func auditNoLeftoverVerifyNamespace(prefix string) error {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
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
