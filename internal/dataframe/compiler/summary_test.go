package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestGroupedRelatedSourceCompilesAllForms(t *testing.T) {
	for _, form := range []string{"COUNT", "PRESENCE", "ALL"} {
		t.Run(form, func(t *testing.T) {
			compileConstructionOutputQuery(t, groupedRelatedSummaryOutput(form, "registered"), "owned-project", "owned-generation")
		})
	}
}

func TestGroupedRelatedSourceCompilesAfterReshape(t *testing.T) {
	for _, shape := range []recipe.ConstructionOperationKind{recipe.ConstructionPivotOp, recipe.ConstructionUnpivotOp} {
		for _, form := range []string{"COUNT", "PRESENCE", "ALL"} {
			t.Run(string(shape)+"/"+form, func(t *testing.T) {
				compileConstructionOutputQuery(t, reshapedGroupedRelatedSummaryOutput(shape, form, "registered"), "owned-project", "owned-generation")
			})
		}
	}
}

func TestGroupedRelatedSourceDistinctRecordsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_group_related_" + uuid.NewString()
	generation := "group-related-generation"
	keys := map[string][]string{}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, ownedKeys := range keys {
			query := fmt.Sprintf("FOR d IN %s FILTER d._key IN @keys REMOVE d IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": ownedKeys}); err != nil {
				t.Errorf("clean up owned %s fixture: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents []map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			keys[collection] = append(keys[collection], document["_key"].(string))
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, encoded)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	resource := func(kind, id string, payload map[string]any) map[string]any {
		payload["id"] = id
		payload["resourceType"] = kind
		return map[string]any{"_key": project + "_" + id, "id": id, "project": project,
			"project_id": project, "dataset_generation": generation, "resourceType": kind, "payload": payload}
	}
	insert("Patient", []map[string]any{
		resource("Patient", "p1", map[string]any{"gender": "male"}),
		resource("Patient", "p2", map[string]any{"gender": "male"}),
		resource("Patient", "p3", map[string]any{"gender": "female"}),
	})
	otherGeneration := resource("Observation", "wrong_generation", map[string]any{"status": "excluded"})
	otherGeneration["dataset_generation"] = "other-generation"
	otherProject := resource("Observation", "wrong_project", map[string]any{"status": "excluded"})
	otherProject["project"] = project + "_other"
	insert("Observation", []map[string]any{
		resource("Observation", "o1", map[string]any{"status": "registered"}),
		resource("Observation", "o2", map[string]any{"status": "registered"}),
		resource("Observation", "o3", map[string]any{"status": "final"}),
		otherGeneration, otherProject,
	})
	edges := []map[string]any{}
	for i, pair := range [][2]string{{"o1", "p1"}, {"o1", "p2"}, {"o2", "p1"}, {"o2", "p2"}, {"o3", "p2"}, {"wrong_generation", "p1"}, {"wrong_project", "p1"}} {
		edges = append(edges, map[string]any{"_key": fmt.Sprintf("%s_edge_%d", project, i),
			"_from": "Observation/" + project + "_" + pair[0], "_to": "Patient/" + project + "_" + pair[1],
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient"})
	}
	insert("fhir_edge", edges)

	for _, shape := range []recipe.ConstructionOperationKind{recipe.ConstructionGroupOp, recipe.ConstructionPivotOp, recipe.ConstructionUnpivotOp} {
		for _, form := range []string{"COUNT", "PRESENCE", "ALL"} {
			for _, filter := range []string{"", "registered", "absent"} {
				t.Run(string(shape)+"/"+form+"/"+filter, func(t *testing.T) {
					output := groupedRelatedSummaryOutput(form, filter)
					if shape != recipe.ConstructionGroupOp {
						output = reshapedGroupedRelatedSummaryOutput(shape, form, filter)
					}
					query := compileConstructionOutputQuery(t, output, project, generation)
					t.Cleanup(func() {
						if t.Failed() {
							t.Logf("query:\n%s\nbindings: %#v", query.Query, query.BindVars)
						}
					})
					rows := executeReshapeOracleQuery(t, ctx, client, query)
					wantOutputRows := 2
					if shape == recipe.ConstructionPivotOp {
						wantOutputRows = 1
					}
					if len(rows) != wantOutputRows {
						t.Fatalf("rows = %#v, want %d shaped rows without related-source fanout", rows, wantOutputRows)
					}
					for _, row := range rows {
						male := row["gender"] == "male"
						wantRows := 1.0
						if male {
							wantRows = 2
						}
						switch shape {
						case recipe.ConstructionGroupOp:
							if !constructionNumericEqual(row["rows"], wantRows) {
								t.Fatalf("group row count changed: %#v", row)
							}
						case recipe.ConstructionUnpivotOp:
							if row["measure"] != "rows" || !constructionNumericEqual(row["amount"], wantRows) {
								t.Fatalf("unpivoted field/value changed: %#v", row)
							}
						case recipe.ConstructionPivotOp:
							if row["resource_kind"] != "Patient" || !constructionNumericEqual(row["female_rows"], 1) || !constructionNumericEqual(row["male_rows"], 2) {
								t.Fatalf("pivoted grouping/category values changed: %#v", row)
							}
						}
						values := []any{}
						if (male || shape == recipe.ConstructionPivotOp) && filter != "absent" {
							values = []any{"registered", "registered"}
							if filter == "" {
								values = append(values, "final")
							}
						}
						switch form {
						case "COUNT":
							if !constructionNumericEqual(row["summary"], float64(len(values))) {
								t.Errorf("distinct matching record count = %#v, want %d", row, len(values))
							}
						case "PRESENCE":
							if row["summary"] != (len(values) > 0) {
								t.Errorf("matching record presence = %#v", row)
							}
						case "ALL":
							if !reflect.DeepEqual(row["summary"], values) {
								t.Errorf("one scalar per distinct record = %#v, want %#v", row["summary"], values)
							}
						}
					}
				})
			}
		}
	}
}

func groupedRelatedSummaryOutput(form, filter string) recipe.Output {
	groupColumns := []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}, {ID: "rows", Name: "rows", Type: "integer"}}
	outputColumns := append([]recipe.StageColumn(nil), groupColumns...)
	outputColumns = append(outputColumns, recipe.StageColumn{ID: "summary", Name: "summary"})
	related := &recipe.ConstructionRelatedSource{
		AnchorColumnID: "__loom_row_id", ChoiceID: "owned-test-choice", SourceOccurrenceID: "observation-node",
		Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node",
			ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
		Route: []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
		ContributorPolicy: "ALL_MATCHES", Form: form, OutputColumnID: "summary",
	}
	if filter != "" {
		related.Predicate = &recipe.ConstructionRelatedPredicate{CandidateID: "observation-status", Operator: recipe.FilterEquals,
			Value: &recipe.FilterValue{Kind: recipe.FilterString, String: &filter}}
	}
	return recipe.Output{Name: "group_related_summary", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group",
						Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "gender", OutputColumnID: "gender"}},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}}}}, Outputs: groupColumns},
				{ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: related}, Outputs: outputColumns},
			}}}
}

func reshapedGroupedRelatedSummaryOutput(shape recipe.ConstructionOperationKind, form, filter string) recipe.Output {
	output := groupedRelatedSummaryOutput(form, filter)
	reshape := recipe.ConstructionStep{ID: "reshape", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}}}
	switch shape {
	case recipe.ConstructionPivotOp:
		female, male := "female", "male"
		output.Fields = append(output.Fields, recipe.Field{Name: "resource_kind", ColumnID: "resource_kind", Expr: recipe.Expression{Select: "root.resourceType"}})
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, recipe.StageColumn{ID: "resource_kind", Name: "resource_kind", Type: "string"})
		output.Construction.Steps[0].Operation.Group.Keys = append(output.Construction.Steps[0].Operation.Group.Keys, recipe.ConstructionGroupKey{InputColumnID: "resource_kind", OutputColumnID: "resource_kind"})
		output.Construction.Steps[0].Outputs = append(output.Construction.Steps[0].Outputs, recipe.StageColumn{ID: "resource_kind", Name: "resource_kind", Type: "string"})
		reshape.Operation = recipe.ConstructionOperation{Kind: shape, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "reshape", GroupKeyIDs: []string{"resource_kind"}, CategoryColumnID: "gender", ValueColumnID: "rows",
			Categories: []recipe.ConstructionPivotCategory{
				{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &female}, OutputColumnID: "female_rows"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &male}, OutputColumnID: "male_rows"},
			}, DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}}
		reshape.Outputs = []recipe.StageColumn{{ID: "resource_kind", Name: "resource_kind", Type: "string"}, {ID: "female_rows", Name: "female_rows", Type: "integer"}, {ID: "male_rows", Name: "male_rows", Type: "integer"}}
	case recipe.ConstructionUnpivotOp:
		key := "rows"
		reshape.Operation = recipe.ConstructionOperation{Kind: shape, Unpivot: &recipe.ConstructionUnpivot{
			ConstructionID: "reshape", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "rows", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &key}}},
			KeyOutputColumnID: "measure", ValueOutputColumnID: "amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		}}
		reshape.Outputs = []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}, {ID: "measure", Name: "measure", Type: "string"}, {ID: "amount", Name: "amount", Type: "integer"}}
	}
	summary := output.Construction.Steps[1]
	summary.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "reshape"}}
	summary.Outputs = append(append([]recipe.StageColumn(nil), reshape.Outputs...), recipe.StageColumn{ID: "summary", Name: "summary"})
	output.Construction.Steps = []recipe.ConstructionStep{output.Construction.Steps[0], reshape, summary}
	return output
}
