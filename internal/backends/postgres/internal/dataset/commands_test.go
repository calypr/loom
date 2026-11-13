package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewPostgreSQLCommandValidatesOrderedParameters(t *testing.T) {
	good, err := NewPostgreSQLCommand(
		"UPDATE t SET state = $3 WHERE project = $1 AND generation = $2 AND schema_identity = $4::jsonb",
		[]Parameter{
			{Type: ParameterText, Value: "p"},
			{Type: ParameterText, Value: "g"},
			{Type: ParameterText, Value: "STAGED"},
			{Type: ParameterJSONB, Value: `{"schemaSha256":"a"}`},
		})
	if err != nil {
		t.Fatalf("good command: %v", err)
	}
	if got := good.Backend(); got != CommandBackendPostgreSQL {
		t.Fatalf("backend = %q", got)
	}
	sqlText, args, err := good.PostgreSQL()
	if err != nil {
		t.Fatalf("PostgreSQL dispatch: %v", err)
	}
	if !strings.Contains(sqlText, "$4::jsonb") {
		t.Fatalf("sql = %s", sqlText)
	}
	if len(args) != 4 || args[0] != "p" || args[3] != `{"schemaSha256":"a"}` {
		t.Fatalf("ordered args = %v", args)
	}
	args[0] = "mutated"
	_, again, err := good.PostgreSQL()
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != "p" {
		t.Fatalf("argument slice is shared with the command: %v", again)
	}

	bad := []struct {
		name string
		sql  string
		args []Parameter
	}{
		{"blank sql", "   ", nil},
		{"unregistered type", "SELECT $1", []Parameter{{Type: ParameterType("int4"), Value: "1"}}},
		{"nil value", "SELECT $1", []Parameter{{Type: ParameterText, Value: nil}}},
		{"non-string text", "SELECT $1", []Parameter{{Type: ParameterText, Value: 7}}},
		{"placeholder out of range", "SELECT $2", []Parameter{{Type: ParameterText, Value: "a"}}},
		{"unused argument", "SELECT $1", []Parameter{{Type: ParameterText, Value: "a"}, {Type: ParameterText, Value: "b"}}},
		{"jsonb without cast", "SELECT $1", []Parameter{{Type: ParameterJSONB, Value: "{}"}}},
		{"text with cast", "SELECT $1::text", []Parameter{{Type: ParameterText, Value: "a"}}},
		{"wrong jsonb cast name", "SELECT $1::json", []Parameter{{Type: ParameterJSONB, Value: "{}"}}},
		{"text placeholder carries cast", "SELECT $1::jsonb", []Parameter{{Type: ParameterText, Value: "a"}}},
	}
	for _, testCase := range bad {
		if _, err := NewPostgreSQLCommand(testCase.sql, testCase.args); !errors.Is(err, ErrInvalidCommand) {
			t.Errorf("%s: error = %v, want ErrInvalidCommand", testCase.name, err)
		}
	}
}

func TestCommandBackendDispatchFailsClosed(t *testing.T) {
	if _, _, err := (Command{}).PostgreSQL(); !errors.Is(err, ErrCommandBackendMismatch) {
		t.Fatalf("zero command: %v", err)
	}
	if _, err := execCommand(context.Background(), nil, Command{}); !errors.Is(err, ErrCommandBackendMismatch) {
		t.Fatalf("execCommand with untagged command: %v", err)
	}
	if err := queryRowCommand(context.Background(), nil, Command{}).Scan(new(string)); !errors.Is(err, ErrCommandBackendMismatch) {
		t.Fatalf("queryRowCommand with untagged command: %v", err)
	}
}

func TestNewValidatesInputs(t *testing.T) {
	if _, err := New(nil, "public"); !errors.Is(err, ErrNilConnSource) {
		t.Fatalf("New(nil) = %v", err)
	}
	for _, schema := range []string{"", "Public", "a b", "1x", strings.Repeat("a", 64), `x"; DROP TABLE y; --`} {
		if _, err := New(fakeConnSource{}, schema); !errors.Is(err, ErrInvalidSchemaName) {
			t.Errorf("New(schema %q) = %v, want ErrInvalidSchemaName", schema, err)
		}
	}
	store, err := New(fakeConnSource{}, "pgstore03_it_demo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if store.manifestFQN != `"pgstore03_it_demo"."dataset_generation_manifest"` ||
		store.pointerFQN != `"pgstore03_it_demo"."dataset_active_generation"` {
		t.Fatalf("fqns = %s / %s", store.manifestFQN, store.pointerFQN)
	}
}

type fakeConnSource struct{}

func (fakeConnSource) Acquire(context.Context) (*pgxpool.Conn, error) {
	return nil, errors.New("unused")
}

func TestValidateProjectAndActiveResolverContract(t *testing.T) {
	if err := validateProject(""); err == nil {
		t.Fatal("blank project accepted")
	}
	if err := validateProject("ok-project"); err != nil {
		t.Fatalf("valid project rejected: %v", err)
	}
	var _ publication.ActiveResolver = (*Store)(nil)
}
