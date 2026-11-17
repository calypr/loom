package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	execution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

func appendRowsSource(name string, offset int64) recipe.Output {
	groupColumns := []recipe.StageColumn{
		{ID: "key-id", Name: "gender", Type: "string"},
		{ID: "count-id", Name: "count", Type: "integer"},
	}
	return recipe.Output{
		Name: name, RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender-id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender-id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender-id", OutputColumnID: "key-id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "count-id"}},
					}},
					Outputs: groupColumns,
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "derive", OutputColumnID: "value-id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "count-id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &offset}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: append(append([]recipe.StageColumn(nil), groupColumns...), recipe.StageColumn{ID: "value-id", Name: "value", Type: "integer"}),
				},
			},
		},
	}
}

func appendWorkspaceRowsBundle(offset int64, filtered bool) recipe.Bundle {
	left := appendRowsSource("left", offset)
	if filtered {
		threshold := int64(2)
		left.Construction.Steps = append(left.Construction.Steps, recipe.ConstructionStep{
			ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "value-id", Operator: recipe.FilterGreaterThan, Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &threshold}}}},
			Outputs:   append([]recipe.StageColumn(nil), left.Construction.Steps[1].Outputs...),
		})
	}
	combined := recipe.Output{
		Name: "append", RootResourceType: "Patient", RowGrain: "patient",
		Construction: &recipe.Construction{
			Version: 1,
			Steps: []recipe.ConstructionStep{{
				ID: "append",
				Inputs: []recipe.ConstructionInputRef{
					{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "left"},
					{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "left_copy"},
					{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "right"},
				},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCombineOp, Combine: &recipe.ConstructionCombine{
					Kind: recipe.ConstructionCombineAppend,
					Projections: []recipe.ConstructionCombineProjection{
						{OutputColumnID: "value", InputIndex: 0, InputColumnID: "value-id"},
						{OutputColumnID: "value", InputIndex: 1, InputColumnID: "value-id"},
						{OutputColumnID: "value", InputIndex: 2, InputColumnID: "value-id"},
						{OutputColumnID: "label", InputIndex: 0, InputColumnID: "key-id"},
						{OutputColumnID: "label", InputIndex: 1, InputColumnID: "key-id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "value", Name: "value", Type: "integer", Nullable: true},
					{ID: "label", Name: "label", Type: "string", Nullable: true},
				},
			}},
		},
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "current-append", TranslationVersion: "1",
		Outputs: []recipe.Output{combined, appendRowsSource("right", 1), appendRowsSource("left_copy", 1), left},
	}
}

type appendCurrentDraftRow struct {
	Label *string `json:"label"`
	Value int64   `json:"value"`
}

func sortAppendCurrentDraftRows(rows []appendCurrentDraftRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Label == nil) != (b.Label == nil) {
			return a.Label == nil
		}
		if a.Label != nil && *a.Label != *b.Label {
			return *a.Label < *b.Label
		}
		return a.Value < b.Value
	})
}

func TestCurrentDraftAppendRowsAgainstArangoClickHouse(t *testing.T) {
	arangoURL, clickhouseURL := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_CLICKHOUSE_URL")
	if arangoURL == "" || clickhouseURL == "" {
		t.Skip("requires owned Arango and ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database := "loom_append_rows_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: arangoURL, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	graph := arangoBackend.Client
	t.Cleanup(func() {
		graph.Close(context.Background())
		request, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(arangoURL, "/")+"/_api/database/"+database, nil)
		response, e := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if e != nil {
			t.Error(e)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("private database cleanup HTTP%d", response.StatusCode)
		}
	})
	for _, spec := range []arangofactory.BootstrapSpec{arangofactory.ArtifactBootstrapSpec(), {Collections: []arangofactory.CollectionSpec{{Name: "Patient"}}}} {
		if err = graph.Bootstrap(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	var docs []json.RawMessage
	for _, row := range []struct{ key, gender, project, generation string }{{"f1", "F", "project-a", "generation-a"}, {"f2", "F", "project-a", "generation-a"}, {"m1", "M", "project-a", "generation-a"}, {"foreign", "F", "project-b", "generation-a"}, {"old", "F", "project-a", "generation-old"}} {
		raw, _ := json.Marshal(map[string]any{"_key": row.key, "id": row.key, "resourceType": "Patient", "project": row.project, "project_id": row.project, "dataset_generation": row.generation, "payload": map[string]any{"id": row.key, "resourceType": "Patient", "gender": row.gender}})
		docs = append(docs, raw)
	}
	if err = graph.InsertBatchRaw(ctx, "Patient", docs, false, "document"); err != nil {
		t.Fatal(err)
	}
	ch, err := storeclickhouse.New(storeclickhouse.Options{URL: clickhouseURL, Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"), Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"), Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ch.Close(); err != nil {
			t.Error(err)
		}
	})
	catalog, err := arangofactory.NewArtifactCatalog(graph)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := execution.New(execution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		command := request.Command
		batch := request.BatchSize
		query, vars, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}

		return graph.QueryRows(ctx, query, batch, vars, arangofactory.RowVisitor(visit))
	}, ClickHouseQueryRows: ch.QueryRowsArgsVisit, PrivateClickHouseArtifacts: manager})
	if err != nil {
		t.Fatal(err)
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted, OutputNames: []string{"append"}}
	var digests []string
	var summary struct {
		Phases            []string `json:"phases"`
		RowCounts         []int    `json:"rowCounts"`
		ArtifactManifests []int    `json:"artifactManifests"`
	}
	female, male := "F", "M"
	for _, step := range []struct {
		phase    string
		offset   int64
		filtered bool
		expected []appendCurrentDraftRow
	}{
		{"before", 1, false, []appendCurrentDraftRow{{&female, 3}, {&female, 3}, {&male, 2}, {&male, 2}, {nil, 3}, {nil, 2}}},
		{"edited", 2, false, []appendCurrentDraftRow{{&female, 4}, {&female, 3}, {&male, 3}, {&male, 2}, {nil, 3}, {nil, 2}}},
		{"filtered", 1, true, []appendCurrentDraftRow{{&female, 3}, {&female, 3}, {&male, 2}, {nil, 3}, {nil, 2}}},
		{"restored", 1, false, []appendCurrentDraftRow{{&female, 3}, {&female, 3}, {&male, 2}, {&male, 2}, {nil, 3}, {nil, 2}}},
	} {
		resolved, e := engine.CompileResolvedBundle(ctx, appendWorkspaceRowsBundle(step.offset, step.filtered), bindings)
		if e != nil {
			t.Fatal(e)
		}
		digests = append(digests, resolved.StoredRecipeDigest)
		if len(resolved.Compiled.Outputs) != 1 || resolved.Compiled.Outputs[0].Name != "append" {
			t.Fatalf("APPEND_CURRENT_DRAFT_SCHEMA: wrong selected output: %#v", resolved.Compiled.Outputs)
		}
		outputs := resolved.Compiled.Outputs[0].Plan.ClickHouseCombine.Outputs
		if len(outputs) != 2 || outputs[0].ID != "value" || outputs[0].LogicalType != "integer" || outputs[0].ClickHouseType != "Nullable(Int64)" || outputs[1].ID != "label" || outputs[1].LogicalType != "string" || outputs[1].ClickHouseType != "Nullable(String)" {
			t.Fatalf("APPEND_CURRENT_DRAFT_SCHEMA: wrong typed output: %#v", outputs)
		}

		result, e := engine.Run(ctx, resolved)
		if e != nil {
			t.Fatal(e)
		}
		actual := []appendCurrentDraftRow{}
		for _, row := range result["append"] {
			value, err := strconv.ParseInt(fmt.Sprint(row["value"]), 10, 64)
			if err != nil {
				t.Fatalf("APPEND_CURRENT_DRAFT_DECODE: numeric value %#v: %v", row["value"], err)
			}
			decoded := appendCurrentDraftRow{Value: value}
			if row["label"] != nil {
				label, ok := row["label"].(string)
				if !ok {
					t.Fatalf("APPEND_CURRENT_DRAFT_DECODE: label %#v", row["label"])
				}
				decoded.Label = &label
			}
			actual = append(actual, decoded)
		}
		sortAppendCurrentDraftRows(actual)
		sortAppendCurrentDraftRows(step.expected)
		var manifests int
		if e = graph.QueryRows(ctx, "FOR d IN @@c RETURN d", 10, map[string]any{"@c": arangofactory.PrivateArtifactsCollection}, func(map[string]any) error { manifests++; return nil }); e != nil || manifests != 0 {
			t.Fatalf("capture cleanup: manifests=%d err=%v", manifests, e)
		}
		if !reflect.DeepEqual(actual, step.expected) {
			evidence, err := json.Marshal(struct {
				Phase    string                  `json:"phase"`
				Expected []appendCurrentDraftRow `json:"expected"`
				Actual   []appendCurrentDraftRow `json:"actual"`
			}{step.phase, step.expected, actual})
			if err != nil {
				t.Fatal(err)
			}
			t.Fatalf("APPEND_CURRENT_DRAFT_ROWS_JSON %s", evidence)
		}
		summary.Phases = append(summary.Phases, step.phase)
		summary.RowCounts = append(summary.RowCounts, len(actual))
		summary.ArtifactManifests = append(summary.ArtifactManifests, manifests)
	}
	if digests[0] == digests[1] || digests[0] == digests[2] || digests[1] == digests[2] || digests[0] != digests[3] {
		t.Fatalf("upstream edit/restore identity=%v", digests)
	}
	encodedSummary, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("APPEND_CURRENT_DRAFT_SUMMARY_JSON %s", encodedSummary)
}
