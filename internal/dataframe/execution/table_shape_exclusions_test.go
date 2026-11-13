package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func compiledTableShapeExclusionPage(offset, limit int) compiler.CompiledTableShapeExclusionQuery {
	return compiler.CompiledTableShapeExclusionQuery{
		Command: mustAQLCommand("exclusions", map[string]any{"offset": offset, "limit": limit}), Offset: offset, Limit: limit,
		ResourceTypeField: "resourceType", ResourceIDField: "resourceId", IdentityStatusField: "identityStatus",
		CategoryValueField: "categoryValue", CategoryPresentField: "categoryPresent", CategoryTypeField: "categoryType",
		OutputRowIDField: "outputRowId", ReasonField: "reason", OmissionField: "omission",
	}
}

func TestTableShapeExclusionsCompiledPagesExactTypedRows(t *testing.T) {
	rows := []map[string]any{
		tableShapeExclusionRow("Observation", "false", true, false, "BOOLEAN", "row-bool"),
		tableShapeExclusionRow("Observation", "zero", true, float64(0), "INTEGER", "row-zero"),
		tableShapeExclusionRow("Observation", "empty", true, "", "STRING", "row-empty"),
		tableShapeExclusionRow("Observation", "null", true, nil, "STRING", "row-null"),
		tableShapeExclusionRow("Observation", "missing", false, nil, "STRING", "row-missing"),
	}
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		query, binds, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		if query != "exclusions" || binds["offset"] != 0 || binds["limit"] != 10 {
			t.Fatalf("unexpected query invocation %q %#v", query, binds)
		}
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != TableShapeExclusionComplete || !result.Complete || result.HasMore || result.NextOffset != 0 || len(result.Exclusions) != len(rows) {
		t.Fatalf("result metadata = %#v", result)
	}
	wants := []struct {
		present bool
		value   any
	}{
		{true, false}, {true, float64(0)}, {true, ""}, {true, nil}, {false, nil},
	}
	for index, want := range wants {
		got := result.Exclusions[index].Category
		if got.Present != want.present || !reflect.DeepEqual(got.Value, want.value) {
			t.Errorf("category %d = %#v, want present=%t value=%#v", index, got, want.present, want.value)
		}
	}
}

func TestTableShapeExclusionsCompiledPagesAndMarksIdentityOmissionIncomplete(t *testing.T) {
	allRows := []map[string]any{
		tableShapeExclusionRow("Observation", "one", true, "one", "STRING", "row-one"),
		tableShapeExclusionRow("Observation", "two", true, "two", "STRING", "row-two"),
		tableShapeExclusionRow("", "", false, "not-a-real-id", "STRING", "row-three"),
	}
	makeEngine := func() *Engine {
		return &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			query, binds, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			if query != "exclusions" {
				t.Fatalf("query = %q", query)
			}
			offset := binds["offset"].(int)
			for _, row := range allRows[offset:] {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		}}
	}
	first, err := makeEngine().TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != TableShapeExclusionIncomplete || first.Complete || !first.HasMore || first.NextOffset != 2 || len(first.Exclusions) != 2 {
		t.Fatalf("first page = %#v", first)
	}
	last, err := makeEngine().TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if last.Status != TableShapeExclusionIncomplete || last.Complete || last.HasMore || last.NextOffset != 2 || len(last.Exclusions) != 1 {
		t.Fatalf("identity-omission page = %#v", last)
	}
	if last.Exclusions[0].SourceIdentity != nil || last.Exclusions[0].OmissionCode != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("missing source identity must be an explicit omission, not synthesized: %#v", last.Exclusions[0])
	}
}

func TestTableShapeExclusionsCompiledRejectsMalformedRowsWithoutPartialSuccess(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		_ = visit(tableShapeExclusionRow("Observation", "good", true, "a", "STRING", "row-a"))
		return visit(map[string]any{"categoryPresent": "not-bool"})
	}}
	result, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(4, 10))
	if err == nil || result.Complete || len(result.Exclusions) > 0 {
		t.Fatalf("malformed partial page was presented as usable: result=%#v error=%v", result, err)
	}
}

func TestTableShapeExclusionsCompiledParserViolationsFailLoud(t *testing.T) {
	base := func() map[string]any {
		return tableShapeExclusionRow("Observation", "good", true, "a", "STRING", "row-a")
	}
	for _, test := range []struct {
		name   string
		mutate func(row map[string]any)
	}{
		{"presence as string", func(row map[string]any) {
			row["categoryPresent"] = "true"
		}},
		{"presence as SQL NULL", func(row map[string]any) {
			row["categoryPresent"] = nil
		}},
		{"presence absent", func(row map[string]any) {
			delete(row, "categoryPresent")
		}},
		{"category value absent", func(row map[string]any) {
			delete(row, "categoryValue")
		}},
		{"category type as raw JSON", func(row map[string]any) {
			row["categoryType"] = exec12JSON(`"STRING"`)
		}},
		{"category type empty", func(row map[string]any) {
			row["categoryType"] = ""
		}},
		{"row identity as number", func(row map[string]any) {
			row["outputRowId"] = 7
		}},
		{"row identity empty", func(row map[string]any) {
			row["outputRowId"] = ""
		}},
		{"reason drifted", func(row map[string]any) {
			row["reason"] = "SOMETHING_ELSE"
		}},
		{"identity status as raw JSON", func(row map[string]any) {
			row["identityStatus"] = exec12JSON(`"EXACT"`)
		}},
		{"identity status unknown", func(row map[string]any) {
			row["identityStatus"] = "SOMETHING"
		}},
		{"exact identity type not a string", func(row map[string]any) {
			row["resourceType"] = 5
		}},
		{"exact identity type empty string", func(row map[string]any) {
			row["resourceType"] = ""
		}},
		{"exact identity type SQL NULL", func(row map[string]any) {
			row["resourceType"] = nil
		}},
		{"exact identity id empty string", func(row map[string]any) {
			row["resourceId"] = ""
		}},
		{"exact identity id absent", func(row map[string]any) {
			delete(row, "resourceId")
		}},
		{"unavailable omission missing", func(row map[string]any) {
			row["identityStatus"] = ir.PhysicalTableShapeExclusionIdentityUnavailable
			row["omission"] = nil
		}},
		{"unavailable omission drifted", func(row map[string]any) {
			row["identityStatus"] = ir.PhysicalTableShapeExclusionIdentityUnavailable
			row["omission"] = "WRONG"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := base()
			test.mutate(row)
			engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, _ RowsRequest, visit RowVisitor) error {
				return visit(row)
			}}
			result, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 10))
			if err == nil {
				t.Fatalf("violating row must fail loud, got %#v", result)
			}
			if result.Exclusions != nil || result.Complete || result.Status == TableShapeExclusionComplete || result.Status == TableShapeExclusionIncomplete {
				t.Fatalf("no partial success on parse violation: %#v", result)
			}
		})
	}
}

func TestTableShapeExclusionsReportsUnsupportedAndNoPolicy(t *testing.T) {
	engine := testEngine(func(_ context.Context, request RowsRequest, _ RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		t.Fatal("refused table-shape diagnostics must not execute a query")
		return nil
	})
	for _, test := range []struct {
		name   string
		bundle recipe.Bundle
		want   TableShapeExclusionStatus
	}{
		{name: "no reshape", bundle: exclusionStatusBundle(nil), want: TableShapeExclusionUnsupported},
		{name: "missing policy", bundle: exclusionStatusBundle(&recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: stringPtr("known")}, Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}}), want: TableShapeExclusionNoExclusionPolicy},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := engine.CompileResolvedBundle(context.Background(), test.bundle, recipe.RuntimeBindings{Project: "project"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.Complete || result.HasMore || len(result.Exclusions) != 0 {
				t.Fatalf("refusal result = %#v, want status %s", result, test.want)
			}
		})
	}
}

func exclusionStatusBundle(reshape *recipe.TableReshape) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "exclusion-status", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "Observations", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
			Fields: []recipe.Field{
				{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "group", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "category", Expr: recipe.Expression{Select: "root.code.text"}},
				{Name: "value", Expr: recipe.Expression{Select: "root.valueInteger"}},
			},
			TableReshape: reshape,
		}},
	}
}

func stringPtr(value string) *string { return &value }

func tableShapeExclusionRow(resourceType, resourceID string, present bool, value any, categoryType, rowID string) map[string]any {
	identityStatus := "EXACT"
	var omission any
	var outputResourceType, outputResourceID any = resourceType, resourceID
	if resourceType == "" || resourceID == "" {
		identityStatus = "UNAVAILABLE"
		omission = "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"
		outputResourceType, outputResourceID = nil, nil
	}
	return map[string]any{
		"resourceType": outputResourceType, "resourceId": outputResourceID, "identityStatus": identityStatus,
		"categoryPresent": present, "categoryValue": value, "categoryType": categoryType,
		"outputRowId": rowID, "reason": "UNLISTED_CATEGORY", "omission": omission,
	}
}

func exec12PostgreSQLTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func exec12PGCommand(offset, fetchLimit int) command.CompiledCommand {
	compiledCommand, err := command.NewPostgreSQLCommand(
		`WITH px_src AS (SELECT 1) SELECT 1 FROM px_src AS p ORDER BY 1 OFFSET $1 LIMIT $2`,
		[]command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterBigint, Value: int64(offset)},
			{Type: command.PostgreSQLParameterBigint, Value: int64(fetchLimit)},
		})
	if err != nil {
		panic(err)
	}
	return compiledCommand
}

func exec12PGCompiled(offset, limit int) compiler.CompiledTableShapeExclusionQuery {
	return compiler.CompiledTableShapeExclusionQuery{
		Command: exec12PGCommand(offset, limit+1), Offset: offset, Limit: limit,
		ResourceTypeField:    ir.PhysicalTableShapeExclusionResourceTypeField,
		ResourceIDField:      ir.PhysicalTableShapeExclusionResourceIDField,
		IdentityStatusField:  ir.PhysicalTableShapeExclusionIdentityStatusField,
		CategoryValueField:   ir.PhysicalTableShapeExclusionCategoryValueField,
		CategoryPresentField: ir.PhysicalTableShapeExclusionCategoryPresentField,
		CategoryTypeField:    ir.PhysicalTableShapeExclusionCategoryTypeField,
		OutputRowIDField:     ir.PhysicalTableShapeExclusionOutputRowIDField,
		ReasonField:          ir.PhysicalTableShapeExclusionReasonField,
		OmissionField:        ir.PhysicalTableShapeExclusionOmissionField,
	}
}

func exec12PGRow(resourceType, resourceID any, identityStatus string, present any, value any, categoryType, rowID string) map[string]any {
	omission := any(nil)
	if identityStatus == ir.PhysicalTableShapeExclusionIdentityUnavailable {
		resourceType, resourceID = nil, nil
		omission = ir.PhysicalTableShapeExclusionSourceIdentityUnavailable
	}
	return map[string]any{
		ir.PhysicalTableShapeExclusionResourceTypeField:    resourceType,
		ir.PhysicalTableShapeExclusionResourceIDField:      resourceID,
		ir.PhysicalTableShapeExclusionIdentityStatusField:  identityStatus,
		ir.PhysicalTableShapeExclusionCategoryValueField:   value,
		ir.PhysicalTableShapeExclusionCategoryPresentField: present,
		ir.PhysicalTableShapeExclusionCategoryTypeField:    categoryType,
		ir.PhysicalTableShapeExclusionOutputRowIDField:     rowID,
		ir.PhysicalTableShapeExclusionReasonField:          ir.PhysicalTableShapeExclusionReasonUnlistedCategory,
		ir.PhysicalTableShapeExclusionOmissionField:        omission,
	}
}

func exec12JSON(value string) json.RawMessage { return json.RawMessage(value) }

func exec12EngineForTarget(t *testing.T, target compiler.Target, port SourceRows) *Engine {
	t.Helper()
	engine, err := New(Config{
		CompileTarget: target,
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "exec12-offline-scope" },
		SourceRows:    port,
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return engine
}

func TestPostgreSQLTableShapeExclusionsDecodesCanonicalEvidenceRows(t *testing.T) {
	rows := []map[string]any{
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"patient-ada"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"zeta"`), "STRING", "row-1"),
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"patient-alan"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`null`), "STRING", "row-2"),
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"patient-grace"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`false`), nil, "STRING", "row-3"),
		exec12PGRow(nil, nil, ir.PhysicalTableShapeExclusionIdentityUnavailable, exec12JSON(`true`), exec12JSON(`"omega"`), "STRING", "row-4"),
	}
	var gotRequest RowsRequest
	engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		gotRequest = request
		if request.Command.Backend() != command.CompiledBackendPostgreSQL {
			t.Fatalf("port received %q-tagged command", request.Command.Backend())
		}
		if _, _, err := request.Command.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("the AQL view must refuse the evidence command: %v", err)
		}
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10))
	if err != nil {
		t.Fatal(err)
	}
	wantContract := []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}
	if !reflect.DeepEqual(gotRequest.DeclaredColumns, wantContract) {
		t.Fatalf("declared contract = %v, want %v", gotRequest.DeclaredColumns, wantContract)
	}
	if result.Status != TableShapeExclusionIncomplete || result.Complete || result.HasMore || result.NextOffset != 0 || len(result.Exclusions) != 4 {
		t.Fatalf("result = %#v", result)
	}
	first := result.Exclusions[0]
	if first.SourceIdentity == nil || first.SourceIdentity.ResourceType != "Patient" || first.SourceIdentity.ResourceID != "patient-ada" {
		t.Fatalf("exact identity = %#v", first)
	}
	if first.Category.Present != true || !reflect.DeepEqual(first.Category.Value, exec12JSON(`"zeta"`)) {
		t.Fatalf("string category = %#v", first.Category)
	}
	jsonNull := result.Exclusions[1].Category
	if !jsonNull.Present || !reflect.DeepEqual(jsonNull.Value, exec12JSON(`null`)) {
		t.Fatalf("explicit JSON-null category must survive as raw JSON, never flattened: %#v", jsonNull)
	}
	missing := result.Exclusions[2].Category
	if missing.Present || missing.Value != nil {
		t.Fatalf("missing category must carry SQL NULL (nil), distinct from JSON null: %#v", missing)
	}
	omitted := result.Exclusions[3]
	if omitted.SourceIdentity != nil || omitted.OmissionCode != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
		t.Fatalf("identity omission = %#v", omitted)
	}
	if omitted.Reason != ir.PhysicalTableShapeExclusionReasonUnlistedCategory || omitted.CategoryType != "STRING" {
		t.Fatalf("frozen labels = %#v", omitted)
	}
}

func TestPostgreSQLTableShapeExclusionsProbePagesAndTruncates(t *testing.T) {
	all := []map[string]any{
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"p-a"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"a"`), "STRING", "row-a"),
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"p-b"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"b"`), "STRING", "row-b"),
		exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"p-c"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"c"`), "STRING", "row-c"),
	}
	engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		_, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		offset := int(args[0].Value.(int64))
		for _, row := range all[offset:] {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	first, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != TableShapeExclusionIncomplete || first.Complete || !first.HasMore || first.NextOffset != 2 || len(first.Exclusions) != 2 {
		t.Fatalf("bounded page = %#v", first)
	}
	second, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != TableShapeExclusionComplete || !second.Complete || second.HasMore || len(second.Exclusions) != 1 {
		t.Fatalf("tail page = %#v", second)
	}
}

func TestPostgreSQLTableShapeExclusionsParserViolationsFailLoud(t *testing.T) {
	base := func() map[string]any {
		return exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"p-a"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"a"`), "STRING", "row-a")
	}
	for _, test := range []struct {
		name   string
		mutate func(row map[string]any)
	}{
		{"presence as Go bool", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryPresentField] = true
		}},
		{"presence as quoted string", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryPresentField] = exec12JSON(`"true"`)
		}},
		{"presence as JSON null", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryPresentField] = exec12JSON(`null`)
		}},
		{"presence as SQL NULL", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryPresentField] = nil
		}},
		{"presence absent", func(row map[string]any) {
			delete(row, ir.PhysicalTableShapeExclusionCategoryPresentField)
		}},
		{"category value absent", func(row map[string]any) {
			delete(row, ir.PhysicalTableShapeExclusionCategoryValueField)
		}},
		{"category type as raw JSON", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryTypeField] = exec12JSON(`"STRING"`)
		}},
		{"category type empty", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionCategoryTypeField] = ""
		}},
		{"row identity empty", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionOutputRowIDField] = ""
		}},
		{"reason drifted", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionReasonField] = "SOMETHING_ELSE"
		}},
		{"identity status as raw JSON", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionIdentityStatusField] = exec12JSON(`"EXACT"`)
		}},
		{"identity status unknown", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionIdentityStatusField] = "SOMETHING"
		}},
		{"exact identity type not a string", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionResourceTypeField] = exec12JSON(`5`)
		}},
		{"exact identity type empty string", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionResourceTypeField] = exec12JSON(`""`)
		}},
		{"exact identity type SQL NULL", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionResourceTypeField] = nil
		}},
		{"exact identity id JSON null", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionResourceIDField] = exec12JSON(`null`)
		}},
		{"unavailable omission missing", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionIdentityStatusField] = ir.PhysicalTableShapeExclusionIdentityUnavailable
			row[ir.PhysicalTableShapeExclusionOmissionField] = nil
		}},
		{"unavailable omission drifted", func(row map[string]any) {
			row[ir.PhysicalTableShapeExclusionIdentityStatusField] = ir.PhysicalTableShapeExclusionIdentityUnavailable
			row[ir.PhysicalTableShapeExclusionOmissionField] = "WRONG"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := base()
			test.mutate(row)
			engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(_ context.Context, _ RowsRequest, visit RowVisitor) error {
				return visit(row)
			}}
			result, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10))
			if err == nil {
				t.Fatalf("violating row must fail loud, got %#v", result)
			}
			if result.Exclusions != nil || result.Complete || result.Status == TableShapeExclusionComplete || result.Status == TableShapeExclusionIncomplete {
				t.Fatalf("no partial success on parse violation: %#v", result)
			}
		})
	}
}

func TestPostgreSQLTableShapeExclusionsPortErrorsReportHonestStatuses(t *testing.T) {
	t.Run("backend-not-registered is the capability gap UNSUPPORTED", func(t *testing.T) {
		engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			return fmt.Errorf("%w: no postgres adapter installed", command.ErrCompiledCommandBackendNotRegistered)
		}}
		result, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10))
		if err != nil {
			t.Fatalf("capability gap must report, not fail: %v", err)
		}
		if result.Status != TableShapeExclusionUnsupported || result.Complete || len(result.Exclusions) != 0 {
			t.Fatalf("result = %#v", result)
		}
	})
	t.Run("backend-mismatch stays a fail-loud routing defect", func(t *testing.T) {
		engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			return fmt.Errorf("%w: sql routed to AQL", command.ErrCompiledCommandBackendMismatch)
		}}
		if _, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10)); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("routing defect must stay a hard error, got %v", err)
		}
	})
	t.Run("generic port failure never reports complete evidence", func(t *testing.T) {
		engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			return errors.New("server went away")
		}}
		result, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10))
		if err == nil || result.Status == TableShapeExclusionComplete {
			t.Fatalf("execution failure must fail loud, got err=%v result=%#v", err, result)
		}
	})
	t.Run("cancellation reports INCOMPLETE at the requested offset", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(_ context.Context, _ RowsRequest, visit RowVisitor) error {
			if err := visit(exec12PGRow(exec12JSON(`"Patient"`), exec12JSON(`"p-a"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"a"`), "STRING", "row-a")); err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		}}
		result, err := engine.TableShapeExclusionsCompiled(ctx, exec12PGCompiled(5, 10))
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != TableShapeExclusionIncomplete || result.Complete || len(result.Exclusions) != 0 || result.NextOffset != 5 {
			t.Fatalf("cancellation result = %#v", result)
		}
	})
	t.Run("AQL arm keeps its hard-error law unchanged", func(t *testing.T) {
		engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			return fmt.Errorf("%w: arango down", command.ErrCompiledCommandBackendNotRegistered)
		}}
		if _, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 10)); err == nil {
			t.Fatal("AQL arm must not map port errors to UNSUPPORTED")
		}
	})
	t.Run("nil port is UNSUPPORTED for PostgreSQL and unchanged for AQL", func(t *testing.T) {
		pg := &Engine{compileTarget: exec12PostgreSQLTarget(t)}
		result, err := pg.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10))
		if err != nil || result.Status != TableShapeExclusionUnsupported || result.Complete {
			t.Fatalf("PG nil port = %#v, %v", result, err)
		}
		aql := &Engine{compileTarget: compiler.ArangoTarget()}
		if _, err := aql.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 10)); err == nil || err.Error() != "table-shape exclusion query executor is required" {
			t.Fatalf("AQL nil port error drifted: %v", err)
		}
	})
	t.Run("unregistered or zero tag keeps the typed metadata error", func(t *testing.T) {
		portCalls := 0
		engine := &Engine{compileTarget: exec12PostgreSQLTarget(t), sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			portCalls++
			return nil
		}}
		if _, err := engine.TableShapeExclusionsCompiled(context.Background(), exec12PGCompiled(0, 10)); err != nil {
			t.Fatal(err)
		}
		portCalls = 0
		broken := exec12PGCompiled(0, 10)
		broken.ReasonField = ""
		if _, err := engine.TableShapeExclusionsCompiled(context.Background(), broken); err == nil || err.Error() != "table-shape exclusion query is missing typed result metadata" {
			t.Fatalf("missing-metadata error drifted: %v", err)
		}
		zero := exec12PGCompiled(0, 10)
		zero.Command = command.CompiledCommand{}
		if _, err := engine.TableShapeExclusionsCompiled(context.Background(), zero); err == nil || err.Error() != "table-shape exclusion query is missing typed result metadata" {
			t.Fatalf("zero-tag command must be refused with the typed metadata error: %v", err)
		}
		if portCalls != 0 {
			t.Fatalf("refused commands must never reach the port: %d calls", portCalls)
		}
	})
}

func TestTableShapeExclusionsPostgreSQLTargetEntryRefusalsStayDistinct(t *testing.T) {
	evidenceReshape := func() *recipe.TableReshape {
		return &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: stringPtr("known")}, Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		}}
	}
	noPolicyReshape := func() *recipe.TableReshape {
		reshape := evidenceReshape()
		reshape.GroupedPivot.UnlistedCategoryPolicy = recipe.PivotUnlistedCategoryError
		return reshape
	}
	filteredBundle := func() recipe.Bundle {
		bundle := exclusionStatusBundle(evidenceReshape())
		status := "final"
		bundle.Outputs[0].Filters = append(bundle.Outputs[0].Filters, recipe.Filter{
			Select: "root.status", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &status}},
		})
		return bundle
	}
	bindings := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"}

	statements := 0
	pgEngine := exec12EngineForTarget(t, exec12PostgreSQLTarget(t), func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		statements++
		if len(request.DeclaredColumns) != 9 {
			t.Fatalf("registered evidence read must carry the nine-field contract, got %v", request.DeclaredColumns)
		}
		return visit(exec12PGRow(exec12JSON(`"Observation"`), exec12JSON(`"obs-1"`), ir.PhysicalTableShapeExclusionIdentityExact, exec12JSON(`true`), exec12JSON(`"zeta"`), "STRING", "row-1"))
	})

	t.Run("no-exclusion policy keeps its own status with zero statements", func(t *testing.T) {
		statements = 0
		resolved, err := pgEngine.CompileResolvedBundle(context.Background(), exclusionStatusBundle(noPolicyReshape()), bindings)
		if err != nil {
			t.Fatal(err)
		}
		result, err := pgEngine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != TableShapeExclusionNoExclusionPolicy || result.Complete || len(result.Exclusions) != 0 || statements != 0 {
			t.Fatalf("no-policy result = %#v after %d statements", result, statements)
		}
	})
	t.Run("outside-slice shape reports UNSUPPORTED with zero statements", func(t *testing.T) {
		statements = 0
		resolved, err := pgEngine.CompileResolvedBundle(context.Background(), filteredBundle(), bindings)
		if err != nil {
			t.Fatal(err)
		}
		result, err := pgEngine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != TableShapeExclusionUnsupported || result.Complete || len(result.Exclusions) != 0 || statements != 0 {
			t.Fatalf("outside-slice result = %#v after %d statements", result, statements)
		}
	})
	t.Run("registered shape executes real evidence: UNSUPPORTED is shape-derived, not blanket", func(t *testing.T) {
		statements = 0
		resolved, err := pgEngine.CompileResolvedBundle(context.Background(), exclusionStatusBundle(evidenceReshape()), bindings)
		if err != nil {
			t.Fatal(err)
		}
		result, err := pgEngine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != TableShapeExclusionComplete || !result.Complete || len(result.Exclusions) != 1 || statements != 1 {
			t.Fatalf("registered control = %#v after %d statements", result, statements)
		}
	})
	t.Run("AQL twin keeps all three current statuses (no reroute)", func(t *testing.T) {
		aqlStatements := 0
		aqlEngine := exec12EngineForTarget(t, compiler.ArangoTarget(), func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			aqlStatements++
			if len(request.DeclaredColumns) != 0 {
				t.Fatalf("the AQL arm stays contract-free, got %v", request.DeclaredColumns)
			}
			return visit(map[string]any{
				ir.PhysicalTableShapeExclusionResourceTypeField:    "Observation",
				ir.PhysicalTableShapeExclusionResourceIDField:      "obs-1",
				ir.PhysicalTableShapeExclusionIdentityStatusField:  ir.PhysicalTableShapeExclusionIdentityExact,
				ir.PhysicalTableShapeExclusionCategoryValueField:   "zeta",
				ir.PhysicalTableShapeExclusionCategoryPresentField: true,
				ir.PhysicalTableShapeExclusionCategoryTypeField:    "STRING",
				ir.PhysicalTableShapeExclusionOutputRowIDField:     "row-1",
				ir.PhysicalTableShapeExclusionReasonField:          ir.PhysicalTableShapeExclusionReasonUnlistedCategory,
				ir.PhysicalTableShapeExclusionOmissionField:        any(nil),
			})
		})
		resolved, err := aqlEngine.CompileResolvedBundle(context.Background(), exclusionStatusBundle(noPolicyReshape()), bindings)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := aqlEngine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10}); err != nil || result.Status != TableShapeExclusionNoExclusionPolicy {
			t.Fatalf("AQL no-policy twin drifted: %#v %v", result, err)
		}
		filteredResolved, err := aqlEngine.CompileResolvedBundle(context.Background(), filteredBundle(), bindings)
		if err != nil {
			t.Fatal(err)
		}
		aqlStatements = 0
		result, err := aqlEngine.TableShapeExclusions(context.Background(), filteredResolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
		if err != nil || result.Status != TableShapeExclusionComplete || len(result.Exclusions) != 1 || aqlStatements != 1 {
			t.Fatalf("AQL outside-slice twin drifted: %#v %v after %d statements", result, err, aqlStatements)
		}
	})
}
