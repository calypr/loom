package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

func TestNewActivationStoreRequiresClient(t *testing.T) {
	if _, err := NewActivationStore(nil, "ctl", "lanes", "execs"); err == nil {
		t.Fatal("nil client must be refused")
	}
}

func TestNewActivationStoreRejectsUnsafeSchemas(t *testing.T) {
	client := &pgstore.Client{}
	for _, placement := range [][3]string{
		{"Ctl", "lanes", "execs"},
		{"ctl", "1anes", "execs"},
		{"ctl", "lanes", "bad-name"},
		{"ctl", "lanes", strings.Repeat("a", 64)},
		{"", "lanes", "execs"},
	} {
		if _, err := NewActivationStore(client, placement[0], placement[1], placement[2]); err == nil {
			t.Fatalf("placement %v must be refused by the schema identifier law", placement)
		} else if !strings.Contains(err.Error(), "invalid activation schema") {
			t.Fatalf("placement %v error = %v, want the named schema refusal", placement, err)
		}
	}
}

func TestActivateRepositoryGenerationRejectsBlankInput(t *testing.T) {
	store, err := NewActivationStore(&pgstore.Client{}, "ctl", "lanes", "execs")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][3]string{
		{"", "g", "r"},
		{"p", "", "r"},
		{"p", "g", ""},
	} {
		err := store.ActivateRepositoryGeneration(context.Background(), args[0], args[1], args[2])
		if !errors.Is(err, ErrInvalidActivationInput) {
			t.Fatalf("args %v error = %v, want ErrInvalidActivationInput before any connection", args, err)
		}
	}
}

func TestActivationStatementsCarryTheCompositeGuards(t *testing.T) {
	stmts := newActivationStatements("ctl", "lanes", "execs")
	required := []struct {
		name      string
		statement string
		clauses   []string
	}{
		{"candidateLock", stmts.publish.revisionClaim, []string{
			`"ctl"."explorer_revisions"`, "WHERE id = $1 FOR UPDATE",
		}},
		{"manifestGuard", stmts.manifestState, []string{
			`"lanes"."dataset_generation_manifest"`, "WHERE project = $1 AND generation = $2 FOR SHARE",
		}},
		{"generationPointerGuard", stmts.generationPointer, []string{
			`"lanes"."dataset_active_generation"`, "WHERE project = $1 FOR SHARE",
		}},
		{"executionGuard", stmts.executionState, []string{
			`"execs"."publication_bundle_executions"`,
			"WHERE id = $1 AND project = $2 AND dataset_generation = $3 FOR SHARE",
		}},
		{"ownerGuard", stmts.publish.ownerGuard, []string{
			`"ctl"."explorers"`, "management_mode = $3", "FOR UPDATE",
		}},
		{"priorSupersede", stmts.publish.priorSupersede, []string{
			"SET status = 'SUPERSEDED'", "WHERE id = $1 AND status = 'ACTIVE'",
		}},
		{"candidateActivate", stmts.publish.candidateActive, []string{
			"SET status = 'ACTIVE', activated_at = $2, failed_at = NULL, diagnostics = $3, publication = $4",
			"WHERE id = $1 AND status = 'READY'",
		}},
		{"candidateRevive", stmts.publish.candidateRevive, []string{
			"SET status = 'ACTIVE', activated_at = $2, failed_at = NULL, diagnostics = $3, publication = $4",
			"WHERE id = $1 AND status = 'FAILED'",
		}},
		{"ownerActivate", stmts.publish.ownerActivate, []string{
			"SET active_revision_id = $3, updated_at = $4", "WHERE project = $1 AND explorer_id = $2",
		}},
	}
	for _, check := range required {
		for _, clause := range check.clauses {
			if !strings.Contains(check.statement, clause) {
				t.Fatalf("%s lost its audited clause %q:\n%s", check.name, clause, check.statement)
			}
		}
	}
	if got := strings.Count(stmts.publish.candidateActive, "UPDATE "); got != 1 {
		t.Fatalf("candidate activation must be one statement, found %d:\n%s", got, stmts.publish.candidateActive)
	}
}
