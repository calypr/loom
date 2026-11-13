package execution

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRowLineagePageIsBoundedAndKeepsOccurrenceIdentity(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), batchSize: 32, sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batchSize := request.BatchSize
		query, binds, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		if batchSize != 32 || query != "row-lineage-query" || binds["row_id"] != "row-7" {
			t.Fatalf("unexpected query invocation: batch=%d query=%q binds=%#v", batchSize, query, binds)
		}
		return visit(map[string]any{
			"found": true,
			"contributors": []any{
				map[string]any{"resourceType": "BodyStructure", "resourceId": "fhir-1", "occurrenceKey": "db-key-1"},
			},
			"hasMore": true,
		})
	}}
	result, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("row-lineage-query", map[string]any{"row_id": "row-7"}),
		Offset:  24, Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "fhir-1" || result.Contributors[0].OccurrenceKey != "db-key-1" || !result.HasMore || result.NextOffset != 25 {
		t.Fatalf("row lineage page = %#v", result)
	}
}

func TestRowLineageDistinguishesMissingKeyedGroupFromEmptyKeylessGroup(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": false, "contributors": []any{}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("keyed-group-query", nil), Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if !errors.Is(err, ErrRowLineageRowNotFound) {
		t.Fatalf("keyed zero-match Group error = %v, want row not found", err)
	}
	engine.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": true, "contributors": []any{}, "hasMore": false})
	}
	result, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("keyless-empty-group-query", nil), Limit: 25, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Contributors == nil || len(result.Contributors) != 0 || result.HasMore {
		t.Fatalf("keyless zero-input Group page = %#v, want empty complete page", result)
	}
}

func TestRowLineageRejectsQueryResultsLargerThanRequestedPage(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		return visit(map[string]any{"found": true, "contributors": []any{
			map[string]any{"resourceType": "Observation", "resourceId": "one", "occurrenceKey": "one"},
			map[string]any{"resourceType": "Observation", "resourceId": "two", "occurrenceKey": "two"},
		}, "hasMore": false})
	}}
	_, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: mustAQLCommand("row-lineage-query", nil), Limit: 1, FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	})
	if err == nil {
		t.Fatal("row lineage accepted more contributors than the requested page size")
	}
}

func exec08GroupBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec08_row_lineage",
		TranslationVersion:  "exec08",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
				Steps: []recipe.ConstructionStep{{
					ID:     "group_status",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID:   "exec08_group",
						MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys:             []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
						Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "grouped_status_id", Name: "status"}, {ID: "rows_id", Name: "rows", Type: "integer"}},
				}},
			},
		}},
	}
}

type exec08PortCall struct {
	tag      command.CompiledBackend
	args     []command.PostgreSQLParameter
	declared []string
	batch    int
}

func exec08pgPort(calls *[]exec08PortCall, batches *[]int, serve func(callIndex int) []map[string]any) func(context.Context, RowsRequest, RowVisitor) error {
	return func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		_, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		*calls = append(*calls, exec08PortCall{tag: request.Command.Backend(), args: args, declared: append([]string(nil), request.DeclaredColumns...), batch: request.BatchSize})
		for _, row := range serve(len(*calls) - 1) {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func exec08aqlPort(calls *int) func(context.Context, RowsRequest, RowVisitor) error {
	return func(_ context.Context, request RowsRequest, _ RowVisitor) error {
		if _, _, err := request.Command.AQLParameters(); err != nil {
			return err
		}
		*calls++
		return nil
	}
}

func exec08taggedLineageCommand(t *testing.T) command.CompiledCommand {
	t.Helper()
	compiled, err := command.NewPostgreSQLCommand(
		`SELECT true AS "found", '[]'::jsonb AS "contributors", false AS "hasMore" WHERE $1 = 'row-7'`,
		[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "row-7"}},
	)
	if err != nil {
		t.Fatalf("tag lineage command: %v", err)
	}
	return compiled
}

func exec08compiledQuery(commandValue command.CompiledCommand, offset, limit int) compiler.CompiledRowLineageQuery {
	return compiler.CompiledRowLineageQuery{
		Command: commandValue, Offset: offset, Limit: limit,
		FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "hasMore",
	}
}

const (
	exec08PageAB   = `[{"resourceType":"Observation","resourceId":"obs-a","occurrenceKey":"obs-a"},{"resourceType":"Observation","resourceId":"obs-b","occurrenceKey":"obs-b"}]`
	exec08PageOneA = `[{"resourceType":"Observation","resourceId":"obs-a","occurrenceKey":"obs-a"}]`
)

func TestRowLineagePostgreSQLPageRunsThroughTheRowPort(t *testing.T) {
	spies := &dispatchSpies{}
	var calls []exec08PortCall
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	config.PreviewSourceRows = func(context.Context, RowsRequest, RowVisitor) error {
		return errors.New("row lineage must never run on the preview lane")
	}
	config.SourceRows = exec08pgPort(&calls, nil, func(int) []map[string]any {
		return []map[string]any{{
			"found":        true,
			"contributors": json.RawMessage(exec08PageAB),
			"hasMore":      true,
		}}
	})
	engine, err := New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	rowID := `[["construction","exec08_group"],["operation","GROUP"],["grouped_status_id","final"]]`
	resolved, err := engine.CompileResolvedBundle(context.Background(), exec08GroupBundle(), recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"observations"}, AuthResourcePaths: []string{"FHIR/Observation"},
	})
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %v", err)
	}
	result, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{Output: "observations", RowID: rowID, Offset: 24, Limit: 2})
	if err != nil {
		t.Fatalf("RowLineage: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("port invocations: %d, want exactly one", len(calls))
	}
	call := calls[0]
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("port received %q-tagged command", call.tag)
	}
	if want := []string{"found", "contributors", "hasMore"}; !reflect.DeepEqual(call.declared, want) {
		t.Fatalf("declared column contract = %v, want %v", call.declared, want)
	}
	if call.batch <= 0 {
		t.Fatalf("batch hint must be carried on the request: %d", call.batch)
	}
	identityParameters := 0
	for _, arg := range call.args {
		if arg.Type == command.PostgreSQLParameterText {
			if text, ok := arg.Value.(string); ok && text == rowID {
				identityParameters++
			}
		}
	}
	if identityParameters != 1 {
		t.Fatalf("row identity bound as text parameter %d times, want exactly once", identityParameters)
	}
	if len(result.Contributors) != 2 ||
		result.Contributors[0] != (RowLineageContributor{ResourceType: "Observation", ResourceID: "obs-a", OccurrenceKey: "obs-a"}) ||
		result.Contributors[1] != (RowLineageContributor{ResourceType: "Observation", ResourceID: "obs-b", OccurrenceKey: "obs-b"}) {
		t.Fatalf("typed contributor page = %#v", result)
	}
	if !result.HasMore || result.NextOffset != 26 {
		t.Fatalf("paging summary = %#v, want HasMore=true NextOffset=26", result)
	}
	if len(spies.aqlQueries) != 0 || len(spies.aqlPreviewQueries) != 0 || len(spies.controlSnapshot()) != 0 || spies.clickHouseQueries != 0 {
		t.Fatalf("PostgreSQL lineage touched Arango/ClickHouse controls: aql=%v preview=%v controls=%v clickhouse=%d", spies.aqlQueries, spies.aqlPreviewQueries, spies.controlSnapshot(), spies.clickHouseQueries)
	}
}

func TestRowLineagePostgreSQLContributorPageFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name         string
		contributors any
		want         string
	}{
		{"malformed json", json.RawMessage(`[{"resourceType":"Observation",`), "returned an invalid contributor page"},
		{"empty bytes", json.RawMessage(``), "returned an invalid contributor page"},
		{"json null", json.RawMessage(`null`), "returned an invalid contributor page"},
		{"json object", json.RawMessage(`{"a":1}`), "returned an invalid contributor page"},
		{"json scalar", json.RawMessage(`3`), "returned an invalid contributor page"},
		{"sql null", nil, "returned an invalid contributor page"},
		{"loose string page", "[]", "returned an invalid contributor page"},
		{"element not an object", json.RawMessage(`["obs-a"]`), "returned an invalid contributor"},
		{"blank resource id", json.RawMessage(`[{"resourceType":"Observation","resourceId":"","occurrenceKey":"obs-a"}]`), "without a FHIR resource identity"},
		{"missing occurrence key", json.RawMessage(`[{"resourceType":"Observation","resourceId":"obs-a"}]`), "without a FHIR resource identity"},
		{"non-string resource type", json.RawMessage(`[{"resourceType":7,"resourceId":"obs-a","occurrenceKey":"obs-a"}]`), "without a FHIR resource identity"},
		{"page exceeds limit", json.RawMessage(exec08PageAB), "exceeded the requested contributor page size"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			engine := &Engine{batchSize: 32, sourceRows: exec08pgPort(&[]exec08PortCall{}, nil, func(int) []map[string]any {
				calls++
				return []map[string]any{{"found": true, "contributors": test.contributors, "hasMore": false}}
			})}
			result, err := engine.rowLineageCompiled(context.Background(), exec08compiledQuery(exec08taggedLineageCommand(t), 0, 1))
			if err == nil || !strings.HasSuffix(err.Error(), test.want) {
				t.Fatalf("poisoned page accepted: result=%#v err=%v, want suffix %q", result, err, test.want)
			}
			if result.Contributors != nil || result.HasMore || result.NextOffset != 0 {
				t.Fatalf("poisoned page leaked a partial result: %#v", result)
			}
			if calls != 1 {
				t.Fatalf("port invocations = %d, want one", calls)
			}
		})
	}
}

func TestRowLineagePostgreSQLFoundSemanticsOverJSONB(t *testing.T) {
	run := func(row map[string]any) (RowLineageResult, error) {
		engine := &Engine{batchSize: 32, sourceRows: exec08pgPort(&[]exec08PortCall{}, nil, func(int) []map[string]any {
			return []map[string]any{row}
		})}
		return engine.rowLineageCompiled(context.Background(), exec08compiledQuery(exec08taggedLineageCommand(t), 0, 25))
	}
	if _, err := run(map[string]any{"found": false, "contributors": json.RawMessage(`[]`), "hasMore": false}); !errors.Is(err, ErrRowLineageRowNotFound) {
		t.Fatalf("keyed group miss over jsonb = %v, want the typed not-found", err)
	}
	if _, err := run(map[string]any{"found": false, "contributors": json.RawMessage(`nonsense`), "hasMore": false}); !errors.Is(err, ErrRowLineageRowNotFound) {
		t.Fatalf("found=false with unreadable page = %v, want the typed not-found", err)
	}
	result, err := run(map[string]any{"found": true, "contributors": json.RawMessage(`[]`), "hasMore": false})
	if err != nil {
		t.Fatalf("empty keyless group page: %v", err)
	}
	if result.Contributors == nil || len(result.Contributors) != 0 || result.HasMore {
		t.Fatalf("empty keyless group page over jsonb = %#v, want complete empty page", result)
	}
	if _, err := run(map[string]any{"found": nil, "contributors": json.RawMessage(`[]`), "hasMore": false}); err == nil || !strings.Contains(err.Error(), "invalid found flag") {
		t.Fatalf("SQL NULL found flag = %v, want the typed flag error", err)
	}
	if _, err := run(map[string]any{"found": true, "contributors": json.RawMessage(`[]`), "hasMore": nil}); err == nil || !strings.Contains(err.Error(), "invalid hasMore flag") {
		t.Fatalf("SQL NULL hasMore flag = %v, want the typed flag error", err)
	}
}

func TestRowLineageCrossTagCommandsFailBeforeThePort(t *testing.T) {
	aqlCalls := 0
	aqlEngine := &Engine{batchSize: 32, sourceRows: exec08aqlPort(&aqlCalls)}
	if _, err := aqlEngine.rowLineageCompiled(context.Background(), exec08compiledQuery(exec08taggedLineageCommand(t), 0, 25)); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("PostgreSQL command on the AQL port = %v, want the typed mismatch", err)
	}
	if aqlCalls != 0 {
		t.Fatalf("cross-tag command reached the AQL port: %d calls", aqlCalls)
	}
	pgCalls := 0
	aqlCommand := mustAQLCommand("row-lineage-query", map[string]any{"row_id": "row-7"})
	pgEngine := &Engine{batchSize: 32, sourceRows: exec08pgPort(&[]exec08PortCall{}, nil, func(int) []map[string]any {
		pgCalls++
		return nil
	})}
	if _, err := pgEngine.rowLineageCompiled(context.Background(), exec08compiledQuery(aqlCommand, 0, 25)); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL command on the PostgreSQL port = %v, want the typed mismatch", err)
	}
	if pgCalls != 0 {
		t.Fatalf("cross-tag command reached the PostgreSQL port: %d calls", pgCalls)
	}
	nativeEngine := &Engine{batchSize: 32, sourceRows: exec08aqlPort(&aqlCalls)}
	nativeEngine.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		if _, _, err := request.Command.AQLParameters(); err != nil {
			return err
		}
		aqlCalls++
		return visit(map[string]any{"found": true, "contributors": []any{
			map[string]any{"resourceType": "Observation", "resourceId": "obs-a", "occurrenceKey": "obs-a"},
		}, "hasMore": false})
	}
	result, err := nativeEngine.rowLineageCompiled(context.Background(), exec08compiledQuery(aqlCommand, 0, 25))
	if err != nil || len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "obs-a" {
		t.Fatalf("native []any arm regressed: result=%#v err=%v", result, err)
	}
}

func TestRowLineageAdmissionRefusalsRunZeroStatements(t *testing.T) {
	spies := &dispatchSpies{}
	var calls []exec08PortCall
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	config.PreviewSourceRows = nil
	config.SourceRows = exec08pgPort(&calls, nil, func(int) []map[string]any { return nil })
	engine, err := New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), exec08GroupBundle(), recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"observations"}, AuthResourcePaths: []string{"FHIR/Observation"},
	})
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %v", err)
	}
	if _, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{Output: "missing-output", RowID: "row-7"}); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("unknown output = %v, want refusal", err)
	}
	if _, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{Output: "observations", RowID: "row-7", Offset: -1}); err == nil || !strings.Contains(err.Error(), "outside the supported range") {
		t.Fatalf("negative offset = %v, want refusal", err)
	}
	if _, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{Output: "observations", RowID: "row-7", Limit: compiler.MaxRowLineageContributors + 1}); err == nil || !strings.Contains(err.Error(), "outside the supported range") {
		t.Fatalf("over-cap limit = %v, want refusal", err)
	}
	if _, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{Output: "observations", RowID: "   "}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("blank row id = %v, want refusal", err)
	}
	if _, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: exec08taggedLineageCommand(t), Limit: 25,
		FoundColumn: "found", ContributorsColumn: "found", HasMoreColumn: "hasMore",
	}); err == nil || !strings.Contains(err.Error(), "missing its bounded result contract") {
		t.Fatalf("duplicated contract name = %v, want refusal", err)
	}
	if _, err := engine.rowLineageCompiled(context.Background(), compiler.CompiledRowLineageQuery{
		Command: exec08taggedLineageCommand(t), Limit: 25,
		FoundColumn: "found", ContributorsColumn: "contributors", HasMoreColumn: "",
	}); err == nil || !strings.Contains(err.Error(), "missing its bounded result contract") {
		t.Fatalf("blank contract name = %v, want refusal", err)
	}
	if len(calls) != 0 {
		t.Fatalf("admission refusals reached the port: %d statements", len(calls))
	}
}
