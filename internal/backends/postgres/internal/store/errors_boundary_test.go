package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5/pgconn"
)

func boundaryTypedNilPgErrorChain(t *testing.T) map[string]error {
	t.Helper()
	typedNil := error((*pgconn.PgError)(nil))
	return map[string]error{
		"bare":        typedNil,
		"wrapped":     fmt.Errorf("driver exec: %w", typedNil),
		"double_wrap": fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", typedNil)),
		"joined":      errors.Join(errors.New("context"), typedNil),
	}
}

func TestSQLStatePredicatesClassifyDriverViolations(t *testing.T) {
	uniqueMatching := &pgconn.PgError{Code: "23505", ConstraintName: "fhir_resource_inventory_build_key_ux", Message: "duplicate key"}
	uniqueOther := &pgconn.PgError{Code: "23505", ConstraintName: "fhir_resource_inventory_count_key_ux", Message: "duplicate key"}
	foreignKey := &pgconn.PgError{Code: "23503", ConstraintName: "explorer_explicit_group_memberships_group_fkey", Message: "violates foreign key"}
	foreignKeyOther := &pgconn.PgError{Code: "23503", ConstraintName: "other_fkey", Message: "violates foreign key"}
	undefinedTable := &pgconn.PgError{Code: "42P01", Message: `relation "t" does not exist`}
	deadlock := &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
	serialization := &pgconn.PgError{Code: "40001", Message: "could not serialize"}
	plain := errors.New("connection reset by peer")

	cases := []struct {
		name string
		err  error
		got  bool
		want bool
	}{
		{"unique matching constraint", uniqueMatching,
			IsUniqueConstraintViolation(uniqueMatching, "fhir_resource_inventory_build_key_ux"), true},
		{"unique matching constraint in list", uniqueMatching,
			IsUniqueConstraintViolation(uniqueMatching, "a", "fhir_resource_inventory_build_key_ux"), true},
		{"unique other constraint", uniqueOther,
			IsUniqueConstraintViolation(uniqueOther, "fhir_resource_inventory_build_key_ux"), false},
		{"unique no constraints listed", uniqueMatching, IsUniqueConstraintViolation(uniqueMatching), false},
		{"unique wrapped", uniqueMatching,
			IsUniqueConstraintViolation(fmt.Errorf("exec: %w", uniqueMatching), "fhir_resource_inventory_build_key_ux"), true},
		{"unique joined", uniqueMatching,
			IsUniqueConstraintViolation(errors.Join(errors.New("context"), uniqueMatching), "fhir_resource_inventory_build_key_ux"), true},
		{"unique against foreign key predicate", uniqueMatching,
			IsForeignKeyConstraintViolation(uniqueMatching, "fhir_resource_inventory_build_key_ux"), false},
		{"foreign key matching", foreignKey,
			IsForeignKeyConstraintViolation(foreignKey, "explorer_explicit_group_memberships_group_fkey"), true},
		{"foreign key other constraint", foreignKeyOther,
			IsForeignKeyConstraintViolation(foreignKeyOther, "explorer_explicit_group_memberships_group_fkey"), false},
		{"foreign key against unique predicate", foreignKey,
			IsUniqueConstraintViolation(foreignKey, "explorer_explicit_group_memberships_group_fkey"), false},
		{"undefined table bare", undefinedTable, IsUndefinedTable(undefinedTable), true},
		{"undefined table wrapped", undefinedTable, IsUndefinedTable(fmt.Errorf("ledger: %w", undefinedTable)), true},
		{"unique is not undefined table", uniqueMatching, IsUndefinedTable(uniqueMatching), false},
		{"deadlock bare", deadlock, IsDeadlockDetected(deadlock), true},
		{"deadlock wrapped", deadlock, IsDeadlockDetected(fmt.Errorf("publish: %w", deadlock)), true},
		{"serialization is not deadlock", serialization, IsDeadlockDetected(serialization), false},
		{"plain error unique", plain, IsUniqueConstraintViolation(plain, "x"), false},
		{"plain error foreign key", plain, IsForeignKeyConstraintViolation(plain, "x"), false},
		{"plain error undefined table", plain, IsUndefinedTable(plain), false},
		{"plain error deadlock", plain, IsDeadlockDetected(plain), false},
	}
	translatedUnique, ok := TranslateError(uniqueMatching)
	if !ok {
		t.Fatalf("unique violation not translated: %v", uniqueMatching)
	}
	cases = append(cases,
		struct {
			name string
			err  error
			got  bool
			want bool
		}{"translated error stays outside raw predicates", translatedUnique,
			IsUniqueConstraintViolation(translatedUnique, "fhir_resource_inventory_build_key_ux"), false},
		struct {
			name string
			err  error
			got  bool
			want bool
		}{"nil unique predicate", nil, IsUniqueConstraintViolation(nil, "x"), false},
		struct {
			name string
			err  error
			got  bool
			want bool
		}{"nil deadlock predicate", nil, IsDeadlockDetected(nil), false},
	)
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestSQLStatePredicatesTypedNilDriverErrorDoesNotPanic(t *testing.T) {
	for inputName, input := range boundaryTypedNilPgErrorChain(t) {
		input := input
		t.Run(inputName+"/unique_constraint", func(t *testing.T) {
			rebNoPanic(t, "IsUniqueConstraintViolation", func() {
				if IsUniqueConstraintViolation(input, "fhir_resource_inventory_build_key_ux") {
					t.Fatal("typed-nil driver error reported as unique constraint violation")
				}
			})
		})
		t.Run(inputName+"/foreign_key", func(t *testing.T) {
			rebNoPanic(t, "IsForeignKeyConstraintViolation", func() {
				if IsForeignKeyConstraintViolation(input, "explorer_explicit_group_memberships_group_fkey") {
					t.Fatal("typed-nil driver error reported as foreign key violation")
				}
			})
		})
		t.Run(inputName+"/undefined_table", func(t *testing.T) {
			rebNoPanic(t, "IsUndefinedTable", func() {
				if IsUndefinedTable(input) {
					t.Fatal("typed-nil driver error reported as undefined table")
				}
			})
		})
		t.Run(inputName+"/deadlock", func(t *testing.T) {
			rebNoPanic(t, "IsDeadlockDetected", func() {
				if IsDeadlockDetected(input) {
					t.Fatal("typed-nil driver error reported as deadlock")
				}
			})
		})
		t.Run(inputName+"/commit_outcome", func(t *testing.T) {
			rebNoPanic(t, "CommitOutcomeUnknown", func() {
				if CommitOutcomeUnknown(input) {
					t.Fatal("typed-nil driver error reported as uncertain commit outcome")
				}
			})
		})
		t.Run(inputName+"/verify_statement", func(t *testing.T) {
			rebNoPanic(t, "verifyStatementError", func() {
				wrapped := verifyStatementError("verify ledger", input)
				if wrapped == nil {
					t.Fatal("typed-nil driver error classified as no fault")
				}
				var connect *ConnectError
				if !errors.As(wrapped, &connect) {
					t.Fatalf("typed-nil driver error passed through as a server reply: %#v", wrapped)
				}
			})
		})
		t.Run(inputName+"/inventory_write", func(t *testing.T) {
			rebNoPanic(t, "inventoryWriteError", func() {
				classified := inventoryWriteError("write inventory counts", []catalog.ResourceInventoryCount{{
					BuildID: "b1", Project: "p", DatasetGeneration: "g", ResourceType: "Patient",
					AuthResourcePath: "/fhir/Patient", Key: "k",
				}}, input)
				if classified == nil {
					t.Fatal("typed-nil driver error classified as no fault")
				}
				if errors.Is(classified, ErrInventoryCountConflict) {
					t.Fatalf("typed-nil driver error mistaken for an inventory count conflict: %v", classified)
				}
			})
		})
	}
}

func TestInventoryWriteErrorKeepsConstraintClassification(t *testing.T) {
	chunk := []catalog.ResourceInventoryCount{{
		BuildID: "b1", Project: "p", DatasetGeneration: "g", ResourceType: "Patient",
		AuthResourcePath: "/fhir/Patient", Key: "count-key-1",
	}}
	keyConflict := &pgconn.PgError{Code: "23505", ConstraintName: inventoryCountTableName + "_key_ux", Message: "duplicate key"}
	classified := inventoryWriteError("write inventory counts", chunk, keyConflict)
	var conflict *InventoryCountConflictError
	if !errors.As(classified, &conflict) {
		t.Fatalf("key constraint violation not upgraded to conflict error: %v", classified)
	}
	if conflict.BuildID != "b1" || conflict.Requested != "count-key-1" {
		t.Fatalf("conflict error lost row identity: %#v", conflict)
	}
	otherConstraint := &pgconn.PgError{Code: "23505", ConstraintName: "fhir_resource_pkey", Message: "duplicate key"}
	classified = inventoryWriteError("write inventory counts", chunk, otherConstraint)
	if !errors.Is(classified, ErrInventoryCountConflict) {
		t.Fatalf("identity unique violation not classified: %v", classified)
	}
	if !errors.Is(classified, otherConstraint) {
		t.Fatalf("driver cause not preserved for inspection: %v", classified)
	}
	plain := errors.New("connection reset by peer")
	classified = inventoryWriteError("write inventory counts", chunk, plain)
	if errors.Is(classified, ErrInventoryCountConflict) {
		t.Fatalf("ordinary error mistaken for a count conflict: %v", classified)
	}
	if !errors.Is(classified, plain) {
		t.Fatalf("ordinary cause not preserved: %v", classified)
	}
}

func TestCommitOutcomeUnknownKeepsDriverClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"connection exception 08006", &pgconn.PgError{Code: "08006", Message: "connection_failure"}, true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01", Message: "admin_shutdown"}, true},
		{"crash shutdown", &pgconn.PgError{Code: "57P02", Message: "crash_shutdown"}, true},
		{"cannot connect now", &pgconn.PgError{Code: "57P03", Message: "cannot_connect_now"}, false},
		{"unique violation", &pgconn.PgError{Code: "23505", Message: "duplicate key"}, false},
		{"wrapped admin shutdown", fmt.Errorf("commit: %w", &pgconn.PgError{Code: "57P01"}), true},
		{"sentinel", ErrCommitOutcomeUnknown, true},
		{"plain error", errors.New("server rejected query"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := CommitOutcomeUnknown(tc.err); got != tc.want {
			t.Errorf("%s: CommitOutcomeUnknown = %v, want %v", tc.name, got, tc.want)
		}
	}
}
