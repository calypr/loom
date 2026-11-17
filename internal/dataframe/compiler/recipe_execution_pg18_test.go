package compiler_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"regexp"
)

const ir03NamespacePrefix = "verify_ir03v1"

var ir03ResourceNamePattern = regexp.MustCompile(`^verify_ir03v1_it_[0-9]+_[0-9a-f]+$`)

func ir03TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the ClickHouse prefix seam PostgreSQL integration tests")
	}
	return raw
}

func ir03RandomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("read random bytes: %v", err)
	}
	return hex.EncodeToString(buf)
}

type ir03Service struct {
	ctx       context.Context
	admin     *pgxpool.Pool
	iso       *pgxpool.Pool
	client    *control.Client
	schema    string
	resources *control.ResourceRepository
	scrub     func(any) string
	fail      func(step string, err error)
}

func newIR03Service(t *testing.T) *ir03Service {
	t.Helper()
	raw := ir03TestPostgresURL(t)
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
	dbName := fmt.Sprintf(ir03NamespacePrefix+"_it_%d_%s", time.Now().UnixNano(), ir03RandomHex(t))
	if !ir03ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)
	schema := fmt.Sprintf(ir03NamespacePrefix+"_it_%d_%s", time.Now().UnixNano(), ir03RandomHex(t))
	if !ir03ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})
	client, err := control.OpenPool(ctx, control.ClientConfig{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		fail("open pg-store client (requires a PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := control.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	resources, err := control.NewResourceRepository(client, schema)
	if err != nil {
		fail("build resource repository", err)
	}
	return &ir03Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, resources: resources, scrub: scrub, fail: fail}
}

func (s *ir03Service) insertPatient(t *testing.T, project, generation, logicalID, authPath string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := s.resources.InsertResource(s.ctx, source.Resource{
		Project: project, DatasetGeneration: generation, ResourceType: "Patient",
		LogicalID: logicalID, AuthResourcePath: authPath, Payload: raw,
	}); err != nil {
		s.fail("insert patient "+logicalID, err)
	}
}

func (s *ir03Service) runCommand(t *testing.T, sqlText string, args []any) ([]string, [][]string) {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sqlText, args...)
	if err != nil {
		s.fail("execute prefix command", err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	if desc := rows.FieldDescriptions(); len(desc) > 0 {
		for _, field := range desc {
			columns = append(columns, string(field.Name))
		}
	}
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read prefix command row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = ir03StringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish prefix command", err)
	}
	return columns, out
}

func (s *ir03Service) witness(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute direct-SQL witness", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read witness row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = ir03StringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish witness", err)
	}
	return out
}

func ir03StringifyCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return "<null>"
	case string:
		return typed
	case []byte:
		return string(typed)
	case pgtype.Numeric:
		if !typed.Valid {
			return "<null>"
		}
		text, err := typed.MarshalJSON()
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(text)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func TestCompileClickHousePrefixPostgreSQLExecutesExactRows(t *testing.T) {
	service := newIR03Service(t)
	bindings := prefixTestBindings(authscope.ReadScopeUnrestricted)
	service.insertPatient(t, bindings.Project, bindings.DatasetGeneration, "patient-1", "/programs/p1", map[string]any{"gender": "male"})
	service.insertPatient(t, bindings.Project, bindings.DatasetGeneration, "patient-2", "/programs/p1", map[string]any{"gender": "male"})
	service.insertPatient(t, bindings.Project, bindings.DatasetGeneration, "patient-3", "/programs/p1", map[string]any{"gender": "female"})
	service.insertPatient(t, "other-project", bindings.DatasetGeneration, "patient-9", "/programs/p1", map[string]any{"gender": "male"})

	composite := prefixTestCompositeWithCombine(t, prefixTestFilterOnlyOutput(), ir.PhysicalPrefixSourcePostgreSQL, bindings, func(stageID string) ir.PhysicalClickHouseCombine {
		return prefixTestAppendCombineFor(stageID, "gender_id", "right_gender", "gender", "string", "String")
	})
	target, err := compiler.PostgreSQLTarget(mustPostgresBinding(t, service.schema))
	if err != nil {
		t.Fatalf("PostgreSQLTarget() error = %v", err)
	}
	compiledPrefix, err := compiler.CompileClickHousePrefix(target, composite)
	if err != nil {
		t.Fatalf("CompileClickHousePrefix() error = %v", err)
	}
	sqlText, parameters, err := compiledPrefix.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("PostgreSQLParameters() error = %v", err)
	}
	args := make([]any, len(parameters))
	for index, parameter := range parameters {
		args[index] = parameter.Value
	}
	declared, rows := service.runCommand(t, sqlText, args)
	if len(declared) != len(compiledPrefix.OutputColumns) {
		t.Fatalf("statement declares %d columns, seam reports %v", len(declared), compiledPrefix.OutputColumns)
	}
	columnIndex := map[string]int{}
	for index, name := range compiledPrefix.OutputColumns {
		columnIndex[name] = index
	}
	rowIDIndex, ok := columnIndex[composite.StageSequence.FinalRowIdentity]
	if !ok {
		t.Fatalf("row identity column %q is not declared: %v", composite.StageSequence.FinalRowIdentity, declared)
	}
	var patientIDs, genders []string
	seenRowIDs := map[string]bool{}
	for _, row := range rows {
		patientIDs = append(patientIDs, row[columnIndex["patient_id"]])
		genders = append(genders, row[columnIndex["gender"]])
		if row[rowIDIndex] == "" || row[rowIDIndex] == "<null>" || seenRowIDs[row[rowIDIndex]] {
			t.Fatalf("row identity %q is missing or duplicated across rows: %v", row[rowIDIndex], rows)
		}
		seenRowIDs[row[rowIDIndex]] = true
	}
	sort.Strings(patientIDs)
	sort.Strings(genders)
	if strings.Join(patientIDs, ",") != "patient-1,patient-2" {
		t.Fatalf("prefix selected patients %v, want patient-1,patient-2 (SQL: %s)", patientIDs, sqlText)
	}
	if strings.Join(genders, ",") != "male,male" {
		t.Fatalf("prefix selected genders %v, want male,male", genders)
	}
	witness := service.witness(t,
		`SELECT logical_id FROM "`+service.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND payload->>'gender'='male' ORDER BY logical_id`,
		bindings.Project, bindings.DatasetGeneration)
	if len(witness) != 2 {
		t.Fatalf("direct-SQL witness returned %d rows, want 2", len(witness))
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		os.Exit(code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit could not connect: %v\n", err)
		os.Exit(1)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname LIKE $1", ir03NamespacePrefix+"_%").Scan(&databases); err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit failed to count databases: %v\n", err)
		os.Exit(1)
	}
	if databases != 0 {
		fmt.Fprintf(os.Stderr, "zero-leftover audit FAILED: %d %s_* databases still exist\n", databases, ir03NamespacePrefix)
		code = 1
	}
	var schemas int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname LIKE $1", ir03NamespacePrefix+"_%").Scan(&schemas); err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit failed to count schemas: %v\n", err)
		os.Exit(1)
	}
	if schemas != 0 {
		fmt.Fprintf(os.Stderr, "zero-leftover audit FAILED: %d %s_* schemas still exist in the service database\n", schemas, ir03NamespacePrefix)
		code = 1
	}
	os.Exit(code)
}
