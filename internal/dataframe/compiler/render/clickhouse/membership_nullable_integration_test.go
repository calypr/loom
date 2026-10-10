package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

// Run with LOOM_CLICKHOUSE_URL and optional LOOM_CLICKHOUSE_DATABASE/
// LOOM_CLICKHOUSE_USERNAME/LOOM_CLICKHOUSE_PASSWORD, or with
// LOOM_CLICKHOUSE_CONTAINER to use clickhouse-client in an owned container.
// The ordinary unit suite stays hermetic.
func TestRenderNullableCompositeMembershipClickHouseSemantics(t *testing.T) {
	client := newNullableMembershipClickHouse(t)
	defer func() {
		if err := client.Close(); err != nil {
			t.Logf("close ClickHouse test client: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatalf("ensure ClickHouse test database: %v", err)
	}

	suffix := uuid.NewString()[:8]
	leftTable := "loom_membership_left_" + suffix
	rightTable := "loom_membership_right_" + suffix
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := client.DropTable(cleanupCtx, leftTable); err != nil {
			t.Logf("drop ClickHouse test table %s: %v", leftTable, err)
		}
		if err := client.DropTable(cleanupCtx, rightTable); err != nil {
			t.Logf("drop ClickHouse test table %s: %v", rightTable, err)
		}
	}()

	columns := []storeclickhouse.Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "k1", Type: "Nullable(String)"},
		{Name: "k2", Type: "Nullable(String)"},
	}
	if err := client.CreateTable(ctx, leftTable, columns); err != nil {
		t.Fatalf("create left table: %v", err)
	}
	if err := client.CreateTable(ctx, rightTable, columns); err != nil {
		t.Fatalf("create right table: %v", err)
	}
	if err := client.InsertRows(ctx, leftTable, columns, []map[string]any{
		{"__loom_row_id": "1", "k1": "alpha", "k2": "one"},
		{"__loom_row_id": "2", "k1": nil, "k2": "two"},
		{"__loom_row_id": "3", "k1": "beta", "k2": nil},
		{"__loom_row_id": "4", "k1": nil, "k2": nil},
		{"__loom_row_id": "5", "k1": "alpha", "k2": "missing"},
		{"__loom_row_id": "6", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "7", "k1": "right-null", "k2": "match"},
		{"__loom_row_id": "8", "k1": "alpha", "k2": "right-null"},
	}); err != nil {
		t.Fatalf("insert left rows: %v", err)
	}
	if err := client.InsertRows(ctx, rightTable, columns, []map[string]any{
		{"__loom_row_id": "a", "k1": "alpha", "k2": "one"},
		{"__loom_row_id": "b", "k1": "alpha", "k2": "one"}, // duplicate match must not multiply the left row
		{"__loom_row_id": "c", "k1": nil, "k2": "two"},
		{"__loom_row_id": "d", "k1": "beta", "k2": nil},
		{"__loom_row_id": "e", "k1": nil, "k2": nil},
		{"__loom_row_id": "f", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "g", "k1": "duplicate", "k2": "key"},
		{"__loom_row_id": "h", "k1": nil, "k2": "match"},
		{"__loom_row_id": "i", "k1": "alpha", "k2": nil},
	}); err != nil {
		t.Fatalf("insert right rows: %v", err)
	}

	inputColumns := func() []ir.ResolvedClickHouseColumn {
		return []ir.ResolvedClickHouseColumn{
			{ID: "row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "k1", Name: "k1", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
			{ID: "k2", Name: "k2", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
		}
	}
	inputs := []ir.ResolvedClickHouseTable{
		resolvedInput("left-table", "execution-left", "left", leftTable, inputColumns()),
		resolvedInput("right-table", "execution-right", "right", rightTable, inputColumns()),
	}
	for _, scenario := range []struct {
		name        string
		keys        []ir.PhysicalCombineKey
		includeWant []string
		excludeWant []string
	}{
		{
			name:        "composite",
			keys:        []ir.PhysicalCombineKey{{LeftColumnID: "k1", RightColumnID: "k1"}, {LeftColumnID: "k2", RightColumnID: "k2"}},
			includeWant: []string{"1", "6"},
			excludeWant: []string{"2", "3", "4", "5", "7", "8"},
		},
		{
			name:        "single",
			keys:        []ir.PhysicalCombineKey{{LeftColumnID: "k1", RightColumnID: "k1"}},
			includeWant: []string{"1", "3", "5", "6", "8"},
			excludeWant: []string{"2", "4", "7"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, mode := range []string{"INCLUDE", "EXCLUDE"} {
				t.Run(mode, func(t *testing.T) {
					plan := ir.PhysicalClickHouseCombine{
						Kind: ir.PhysicalCombineMembership,
						Inputs: []ir.PhysicalCombineInputRef{
							{TableID: "left-table", RevisionID: "execution-left", OutputID: "left"},
							{TableID: "right-table", RevisionID: "execution-right", OutputID: "right"},
						},
						Keys:           scenario.keys,
						MembershipMode: mode,
						Projections: []ir.PhysicalCombineProjection{
							{OutputColumnID: "k1", InputIndex: 0, InputColumnID: "k1"},
							{OutputColumnID: "k2", InputIndex: 0, InputColumnID: "k2"},
						},
						Outputs: []ir.PhysicalCombineOutputColumn{
							{ID: "k1", Name: "k1", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
							{ID: "k2", Name: "k2", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true},
						},
					}
					rendered, err := RenderCombine(plan, inputs, "project-a")
					if err != nil {
						t.Fatalf("render nullable membership: %v", err)
					}
					rows, err := client.QueryRowsArgs(ctx, rendered.Query, rendered.Columns, rendered.Args...)
					if err != nil {
						t.Fatalf("execute rendered membership: %v\nquery: %s", err, rendered.Query)
					}
					got := make([]string, 0, len(rows))
					for _, row := range rows {
						id, ok := row["__loom_row_id"].(string)
						if !ok {
							t.Fatalf("row identity has type %T, want string: %#v", row["__loom_row_id"], row)
						}
						got = append(got, id)
					}
					sort.Strings(got)
					want := scenario.includeWant
					if mode == "EXCLUDE" {
						want = scenario.excludeWant
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("%s %s result row IDs = %v, want %v", scenario.name, mode, got, want)
					}
					if mode == "INCLUDE" && len(got) != len(want) {
						t.Fatalf("duplicate right keys multiplied left rows: %s", fmt.Sprint(got))
					}
				})
			}
		})
	}
}

type nullableMembershipClickHouse interface {
	EnsureDatabase(context.Context) error
	CreateTable(context.Context, string, []storeclickhouse.Column) error
	InsertRows(context.Context, string, []storeclickhouse.Column, []map[string]any) error
	QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error)
	DropTable(context.Context, string) error
	Close() error
}

var membershipTestDatabasePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newNullableMembershipClickHouse(t *testing.T) nullableMembershipClickHouse {
	t.Helper()
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	if !membershipTestDatabasePattern.MatchString(database) {
		t.Fatalf("ClickHouse test database %q is not a safe identifier", database)
	}
	if container := os.Getenv("LOOM_CLICKHOUSE_CONTAINER"); container != "" {
		return containerMembershipClickHouse{container: container, database: database}
	}
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	client, err := storeclickhouse.New(storeclickhouse.Options{
		URL: url, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open ClickHouse: %v", err)
	}
	return client
}

type containerMembershipClickHouse struct {
	container string
	database  string
}

func (client containerMembershipClickHouse) EnsureDatabase(ctx context.Context) error {
	_, err := client.runWithoutDatabase(ctx, "CREATE DATABASE IF NOT EXISTS `"+client.database+"`", nil, "")
	return err
}

func (client containerMembershipClickHouse) CreateTable(ctx context.Context, table string, columns []storeclickhouse.Column) error {
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = fmt.Sprintf("`%s` %s", column.Name, column.Type)
	}
	query := fmt.Sprintf("CREATE TABLE `%s` (%s) ENGINE = MergeTree ORDER BY (`__loom_row_id`)", table, strings.Join(parts, ", "))
	_, err := client.run(ctx, query, nil, "")
	return err
}

func (client containerMembershipClickHouse) InsertRows(ctx context.Context, table string, _ []storeclickhouse.Column, rows []map[string]any) error {
	var input bytes.Buffer
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode ClickHouse test row: %w", err)
		}
		input.Write(encoded)
		input.WriteByte('\n')
	}
	query := fmt.Sprintf("INSERT INTO `%s` FORMAT JSONEachRow", table)
	_, err := client.run(ctx, query, &input, "")
	return err
}

func (client containerMembershipClickHouse) QueryRowsArgs(ctx context.Context, query string, _ []string, args ...any) ([]map[string]any, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("container ClickHouse test client does not accept query arguments")
	}
	output, err := client.run(ctx, query, nil, "JSONEachRow")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var rows []map[string]any
	for {
		var row map[string]any
		if err := decoder.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode ClickHouse JSONEachRow: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (client containerMembershipClickHouse) DropTable(ctx context.Context, table string) error {
	_, err := client.run(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table), nil, "")
	return err
}

func (containerMembershipClickHouse) Close() error { return nil }

func (client containerMembershipClickHouse) run(ctx context.Context, query string, input io.Reader, outputFormat string) ([]byte, error) {
	return client.runInDatabase(ctx, client.database, query, input, outputFormat)
}

func (client containerMembershipClickHouse) runWithoutDatabase(ctx context.Context, query string, input io.Reader, outputFormat string) ([]byte, error) {
	return client.runInDatabase(ctx, "", query, input, outputFormat)
}

func (client containerMembershipClickHouse) runInDatabase(ctx context.Context, database, query string, input io.Reader, outputFormat string) ([]byte, error) {
	args := []string{"exec"}
	if input != nil {
		args = append(args, "-i")
	}
	args = append(args, client.container, "clickhouse-client")
	if database != "" {
		args = append(args, "--database", database)
	}
	if outputFormat != "" {
		args = append(args, "--format", outputFormat)
	}
	args = append(args, "--query", query)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = input
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("docker exec ClickHouse query: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
