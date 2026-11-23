package compilerprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestOwnedAcceptanceTransport(t *testing.T) {
	project, db := os.Getenv("LOOM_COMPILER_RESEARCH_COMPOSE"), os.Getenv("LOOM_COMPILER_RESEARCH_DATABASE")
	if project == "" && db == "" {
		t.Skip("owned acceptance containers not supplied")
	}
	if !regexp.MustCompile(`^loom-acceptance-[a-z0-9-]+$`).MatchString(project) ||
		!regexp.MustCompile(`^loom_acceptance_[a-f0-9]{16}$`).MatchString(db) {
		t.Fatal("only an owned acceptance namespace is supported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(service string, args ...string) ([]byte, error) {
		container := project + "-" + service + "-1"
		cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", container}, args...)...)
		out, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %w: %s", service, err, exit.Stderr)
		}
		return out, err
	}
	path := os.Getenv("LOOM_COMPILER_RESEARCH_RECEIPTS")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Project                  string
		SourceGeneration         string
		AuthorizationScopeDigest string
		Bundle                   recipe.Bundle `json:"compiledRecipe"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	var queries []string
	engine, err := execution.New(execution.Config{
		Registry:    noRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return receipt.AuthorizationScopeDigest },
		QueryRows: func(_ context.Context, query string, _ int, vars map[string]any, visit func(map[string]any) error) error {
			payload, err := json.Marshal(map[string]any{"query": query, "bindVars": vars, "batchSize": 10000})
			if err != nil {
				return err
			}
			queries = append(queries, string(payload))
			out, err := run("arangodb", "wget", "-qO-", "--header=Content-Type: application/json", "--post-data="+string(payload), "http://127.0.0.1:8529/_db/"+db+"/_api/cursor")
			if err != nil {
				return err
			}
			var response struct {
				Error, HasMore bool
				Result         []map[string]any
			}
			if err = json.Unmarshal(out, &response); err != nil {
				return err
			}
			if response.Error || response.HasMore {
				return fmt.Errorf("owned Arango cursor incomplete")
			}
			for _, row := range response.Result {
				if err = visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	materialize := func(bundle recipe.Bundle) map[string][]map[string]any {
		t.Helper()
		bindings := recipe.RuntimeBindings{Project: receipt.Project, DatasetGeneration: receipt.SourceGeneration, AuthScopeMode: authscope.ReadScopeUnrestricted}
		resolved, err := engine.CompileResolvedBundle(ctx, bundle, bindings)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := engine.Run(ctx, resolved)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	original := materialize(receipt.Bundle)
	v2, err := Translate(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	transported, err := RestoreTransport(v2)
	if err != nil {
		t.Fatal(err)
	}
	alternate := materialize(transported)
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(queries) != 2 || queries[0] != queries[1] {
		t.Fatal("transport round trip changed compiled queries or ordered results")
	}
	if len(original) != 1 {
		t.Fatalf("expected one acceptance output, got %d", len(original))
	}
	var arangoRows []map[string]any
	for _, rows := range original {
		arangoRows = rows
	}
	if len(arangoRows) != 100 {
		t.Fatalf("expected 100 acceptance rows, got %d", len(arangoRows))
	}
	clickhouse := func(query string) []byte {
		t.Helper()
		raw, err := run("clickhouse", "clickhouse-client", "--user", "loom_demo", "--password", "loom_demo_password", "--database", db, "--query", query)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	table := strings.TrimSpace(string(clickhouse("SHOW TABLES")))
	if !regexp.MustCompile(`^loom_bundle_[a-z0-9_]+$`).MatchString(table) {
		t.Fatalf("expected one owned publication table")
	}
	fields := make([]string, 0, len(arangoRows[0]))
	for field := range arangoRows[0] {
		if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(field) {
			t.Fatalf("invalid projected field")
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	raw = clickhouse("SELECT " + strings.Join(fields, ",") + " FROM " + table + " SETTINGS output_format_json_quote_64bit_integers=0 FORMAT JSONEachRow")
	var chRows []map[string]any
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		var row map[string]any
		if err = json.Unmarshal(scan.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		chRows = append(chRows, row)
	}
	if err = scan.Err(); err != nil {
		t.Fatal(err)
	}
	normalize := func(rows []map[string]any) []string {
		t.Helper()
		records := make([]string, 0, len(rows))
		for _, row := range rows {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, string(raw))
		}
		sort.Strings(records)
		return records
	}
	a, b := normalize(arangoRows), normalize(chRows)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		if len(a) != len(b) {
			t.Fatalf("Arango rows=%d, ClickHouse rows=%d", len(a), len(b))
		}
		for index := range a {
			if a[index] != b[index] {
				t.Fatalf("Arango/ClickHouse row mismatch at sorted position %d", index)
			}
		}
	}
	t.Logf("one receipt, %d identical Arango/ClickHouse rows; v1 and transport restored v1 produced identical ordered results and queries", len(a))
	bindings := recipe.RuntimeBindings{Project: receipt.Project, DatasetGeneration: receipt.SourceGeneration, AuthScopeMode: authscope.ReadScopeUnrestricted}
	resolved, err := engine.CompileResolvedBundle(ctx, receipt.Bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	plan := resolved.Compiled.Outputs[0].Plan
	originalIR, err := json.Marshal(ir.CanonicalExecutionPhysicalPlan(plan))
	if err != nil {
		t.Fatal(err)
	}
	control := ir.ClonePhysicalPlan(plan)
	control.BindVars["compiler_research_unused"] = "unused"
	controlIR, err := json.Marshal(ir.CanonicalExecutionPhysicalPlan(control))
	if err != nil || bytes.Equal(originalIR, controlIR) {
		t.Fatalf("IR control did not vary canonical bytes: %v", err)
	}
	forward, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := aql.RenderPhysicalPlan(control)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(v aql.RenderedPhysicalPlan) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if !bytes.Equal(encode(forward), encode(diagnostic)) {
		t.Fatal("diagnostic-only IR difference changed emitted query or binds")
	}
	var negative struct {
		Query     string         `json:"query"`
		BindVars  map[string]any `json:"bindVars"`
		BatchSize int            `json:"batchSize"`
	}
	if err := json.Unmarshal([]byte(queries[0]), &negative); err != nil {
		t.Fatal(err)
	}
	if _, exists := negative.BindVars["project"]; !exists {
		t.Fatal("acceptance query lacks the project bind required by the negative control")
	}
	negative.BindVars["project"] = "compiler-research-empty-control"
	payload, err := json.Marshal(negative)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = run("arangodb", "wget", "-qO-", "--header=Content-Type: application/json", "--post-data="+string(payload), "http://127.0.0.1:8529/_db/"+db+"/_api/cursor")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error, HasMore bool
		Result         []json.RawMessage
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Error || result.HasMore || len(result.Result) != 0 {
		t.Fatalf("project bind negative control did not remove rows: %v (rows=%d)", err, len(result.Result))
	}
	t.Log("one harmless canonical IR change kept AQL and binds identical; one project-bind semantic change returned zero rows")

}

type noRegistry struct{}

func (noRegistry) LoadRecipe(context.Context, string) (recipe.Entry, error) {
	return recipe.Entry{}, recipe.ErrRecipeNotFound
}

func (noRegistry) LoadRecipeVersion(context.Context, string, string) (recipe.Entry, error) {
	return recipe.Entry{}, recipe.ErrRecipeNotFound
}
