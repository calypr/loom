package server

// Offline contract cells for the full PostgreSQL server runtime
// (pg-runtime-server-composition). These run without any database: they pin
// the consumability ladder, the dispatch order, the typed explicit-unavailable
// postures, the Arango-collection honesty of the PostgreSQL authorizer, the
// neutrality of the receipt-materializer seam, and the no-Arango-import law
// of the composition file itself.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	backendsarango "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

// The receipt-materializer seam must be satisfied by BOTH registries purely
// structurally: this compile-time pair is the no-backend-decision proof.
var (
	_ receiptExecutionFinder = (*backendsarango.PublishedRegistry)(nil)
	_ receiptExecutionFinder = (*backendspostgres.BundleCatalog)(nil)
)

func fullLadderSchemas() map[string]string {
	return map[string]string{
		"source":                 "loom_source",
		"explorer_control":       "loom_explorer",
		"publication_control":    "loom_publication",
		"dataset_release":        "loom_release",
		"recipe_chains":          "loom_recipe",
		"recipe_exec_registries": "loom_exec",
	}
}

func TestPostgresRuntimeAssignmentAcceptsFullLadder(t *testing.T) {
	assignment, names, err := postgresRuntimeAssignment(PostgreSQLConfig{Schemas: fullLadderSchemas()})
	if err != nil {
		t.Fatalf("the complete six-series ladder must be consumable: %v", err)
	}
	if len(assignment) != 6 || len(names) != 6 {
		t.Fatalf("assignment must carry all six series, got %v", assignment)
	}
	for series, schema := range fullLadderSchemas() {
		if string(assignment[backendspostgres.Series(series)]) != schema {
			t.Fatalf("series %s must bind schema %s, got %v", series, schema, assignment)
		}
		if names[backendspostgres.Series(series)] != schema {
			t.Fatalf("resolved names must mirror the assignment for %s", series)
		}
	}
}

func TestPostgresRuntimeAssignmentKeepsTheSentinelForEveryOtherShape(t *testing.T) {
	cases := []struct {
		name    string
		schemas map[string]string
	}{
		{"absent", nil},
		{"empty", map[string]string{}},
		{"first-query pair is the route's own, not the ladder", map[string]string{"source": "s", "dataset_release": "r"}},
		{"ladder minus one", func() map[string]string {
			s := fullLadderSchemas()
			delete(s, "recipe_exec_registries")
			return s
		}()},
		{"ladder plus foreign", func() map[string]string {
			s := fullLadderSchemas()
			s["warehouse"] = "w"
			return s
		}()},
		{"duplicate schema across two series", func() map[string]string {
			s := fullLadderSchemas()
			s["recipe_chains"] = s["recipe_exec_registries"]
			return s
		}()},
		{"whitespace-colliding pair", func() map[string]string {
			s := fullLadderSchemas()
			s["recipe_chains"] = " loom_exec "
			return s
		}()},
		{"blank member", func() map[string]string {
			s := fullLadderSchemas()
			s["publication_control"] = "  "
			return s
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := postgresRuntimeAssignment(PostgreSQLConfig{Schemas: tc.schemas}); !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
				t.Fatalf("schemas %v must keep the byte-stable not-wired sentinel, got %v", tc.schemas, err)
			}
		})
	}
}

// TestPostgresRuntimeDispatchReachesThePostgreSQLPathPreDial proves the
// three-way dispatch selects the full runtime for a ladder config WITHOUT
// dialing: an unsafe schema identifier must surface the factory's own pre-dial
// identifier law -- never the consumability sentinel (the shape IS
// consumable) and never an ArangoDB path. 192.0.2.1 is TEST-NET-1: the pass
// condition is the named refusal arriving long before the deadline.
func TestPostgresRuntimeDispatchReachesThePostgreSQLPathPreDial(t *testing.T) {
	cfg := Config{}
	cfg.Server.Backend = sourceBackendPostgres
	cfg.Server.PostgreSQL = PostgreSQLConfig{
		Host: "192.0.2.1", Port: 60543, Database: "loom", User: "loom",
		Password: "pgcomp-Canary-Pa55w0rd", SSLMode: "require",
		MaxOpenConns: 2, MaxIdleConns: 1,
		Schemas: map[string]string{
			"source":                 "Loom Source", // unsafe: identifier law
			"explorer_control":       "loom_explorer",
			"publication_control":    "loom_publication",
			"dataset_release":        "loom_release",
			"recipe_chains":          "loom_recipe",
			"recipe_exec_registries": "loom_exec",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*1024*1024*1024)
	defer cancel()
	err := RunWithConfig(ctx, cfg)
	if err == nil {
		t.Fatal("ladder config unexpectedly started the server")
	}
	if errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
		t.Fatalf("a consumable ladder was refused by the not-wired sentinel: %v", err)
	}
	if strings.Contains(err.Error(), cfg.Server.PostgreSQL.Password) {
		t.Fatalf("refusal leaked the connection password: %v", err)
	}
	if !strings.Contains(err.Error(), "Loom Source") && !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error %q must name the unsafe schema identifier or the schema law", err)
	}
}

func TestPostgresRuntimeExplorerStoreTypedRefusals(t *testing.T) {
	store := pgExplorerStore{ExplorerStore: &backendspostgres.ExplorerStore{}}
	if err := store.ActivateRepositoryGeneration(context.Background(), "p", "g", "r"); err == nil {
		t.Fatal("repository-generation activation must refuse without an Arango fall-through")
	} else if !isTypedExplicitUnavailable(err, "Repository-generation activation") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
	if err := store.ValidateSelectionReferences(context.Background(), "p", "g", authscope.ReadScope{}, nil); err == nil {
		t.Fatal("selection-reference validation must refuse without an Arango fall-through")
	} else if !isTypedExplicitUnavailable(err, "Explicit-selection reference validation") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
	if _, err := store.PublishAuthoring(context.Background(), explorer.CompilationReceipt{}, explorer.Revision{}, publicationcontract.ProjectRelease{}, 0); err == nil {
		t.Fatal("publication without the dataset_release assignment must refuse")
	} else if !isTypedExplicitUnavailable(err, "Explorer publication") {
		t.Fatalf("refusal %v must be the typed explicit-unavailable posture", err)
	}
}

// isTypedExplicitUnavailable asserts the named typed posture produced by
// postgreSQLRuntimeUnavailable: BACKEND_UNAVAILABLE, retryable=false,
// postgresql-tagged details.
func isTypedExplicitUnavailable(err error, operationSubstring string) bool {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return false
	}
	if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		return false
	}
	if userErr.Retryable() {
		return false
	}
	if !strings.Contains(userErr.Error(), operationSubstring) {
		return false
	}
	backend, _ := userErr.Details()["backend"].(string)
	return backend == "postgresql"
}

// TestPostgresRuntimeAuthorizerNeverStampsTheArangoCollection is the
// wired-not-vacuous contrast: the Arango lane's authorizer stamps the Arango
// selection-members collection onto every binding; the PostgreSQL authorizer
// must leave the field empty on both read and write.
func TestPostgresRuntimeAuthorizerNeverStampsTheArangoCollection(t *testing.T) {
	pgRead, err := pgRecipeAuthorization{}.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("unrestricted read authorization: %v", err)
	}
	if pgRead.SelectionMembersCollection != "" {
		t.Fatalf("the PostgreSQL authorizer must leave SelectionMembersCollection empty, got %q", pgRead.SelectionMembersCollection)
	}
	if pgRead.SelectionProject != "p1" {
		t.Fatalf("SelectionProject must be the canonical project, got %q", pgRead.SelectionProject)
	}
	pgWrite, err := pgRecipeAuthorization{}.AuthorizeWrite(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("unrestricted write authorization: %v", err)
	}
	if pgWrite.SelectionMembersCollection != "" {
		t.Fatalf("the PostgreSQL write authorizer must leave SelectionMembersCollection empty, got %q", pgWrite.SelectionMembersCollection)
	}
	// The contrast half: same package, same input, Arango lane stamps the
	// collection. Without this half the assertion above could pass on a
	// broken authorizer that simply drops every binding field.
	arangoRead, err := recipeAuthorization{}.AuthorizeRead(context.Background(), recipe.RuntimeBindings{Project: "p1", DatasetGeneration: "g"})
	if err != nil {
		t.Fatalf("arango-lane read authorization: %v", err)
	}
	if arangoRead.SelectionMembersCollection == "" {
		t.Fatal("the Arango lane must keep stamping its selection-members collection; the contrast fixture went vacuous")
	}
}

// TestPostgresRuntimeCompositionHasNoArangoImport is the source-level proof
// of the no-fall-through law for the composition file: internal/server/
// runtime.go may not import, mention, or construct any ArangoDB adapter or
// AQL surface.
func TestPostgresRuntimeCompositionHasNoArangoImport(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatalf("read the composition source: %v", err)
	}
	text := string(source)
	importBlock := text
	if start := strings.Index(text, "import ("); start >= 0 {
		if end := strings.Index(text[start:], "\n)"); end >= 0 {
			importBlock = text[start : start+end]
		}
	}
	// One owned factory package holds every ArangoDB adapter tree
	// (arch-backend-07); the PostgreSQL composition must import none of it.
	// The AQL renderer stayed in the compiler tree and is forbidden too.
	forbidden := []string{
		"backends/arango",
		"compiler/render/aql",
	}
	for _, path := range forbidden {
		if strings.Contains(importBlock, path) {
			t.Fatalf("the PostgreSQL composition must not import %s", path)
		}
	}
	for _, token := range []string{"arangostore.", "AQLParameters"} {
		if strings.Contains(text, token) {
			t.Fatalf("the PostgreSQL composition must not mention %s (no AQL/Arango surface on the PostgreSQL lane)", token)
		}
	}
}
