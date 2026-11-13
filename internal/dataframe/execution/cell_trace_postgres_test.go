package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func exec09AdapterRow(t *testing.T, identity any, value any, status string, hasMore bool, contributions any) map[string]any {
	t.Helper()
	return map[string]any{
		ir.PhysicalCellTraceIdentityPartsField: identity,
		ir.PhysicalCellTraceValueField:         value,
		ir.PhysicalCellTraceStatusField:        status,
		ir.PhysicalCellTraceHasMoreField:       hasMore,
		ir.PhysicalCellTraceOmissionField:      "",
		ir.PhysicalCellTraceContributionsField: contributions,
	}
}

func exec09ArmB(t *testing.T, offset, limit int) (compiler.CompiledCellTraceQuery, Resolved) {
	t.Helper()
	resolved := exec02TestResolved()
	compiled, err := compiler.CompileCellTraceOutput(exec02PostgreSQLTarget(t),
		resolved.Compiled.Outputs[0], "name", offset, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("registered arm B cell trace must compile on the PostgreSQL target: %v", err)
	}
	if compiled.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("compiled trace must carry the PostgreSQL tag, got %q", compiled.Command.Backend())
	}
	return compiled, resolved
}

func exec09PreviewRowID(t *testing.T, identity *spec.RowIdentity, row map[string]any) string {
	t.Helper()
	if err := ensureStableRowIdentity(row, identity, nil); err != nil {
		t.Fatalf("preview-side identity mint: %v", err)
	}
	published, ok := row["__loom_row_id"].(string)
	if !ok || published == "" {
		t.Fatalf("preview-side identity mint produced no published ID: %#v", row)
	}
	return published
}

func exec09Request(rowID string) CellTraceRequest {
	return CellTraceRequest{Output: "resources", RowID: rowID, Column: "name", Offset: 0, Limit: 2}
}

func TestPostgresCellTraceOfflineArmCarriesSealedDeclaredContract(t *testing.T) {
	compiled, resolved := exec09ArmB(t, 0, 2)
	rowID := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-one"})
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, []map[string]any{exec09AdapterRow(t,
		json.RawMessage(`["patient-one"]`), json.RawMessage(`"Ada"`), "VALUE", false,
		json.RawMessage(`[{"resourceType":"Patient","resourceId":"patient-one","operation":"EXTRACT","value":"female"}]`))})
	result, err := engine.CellTraceCompiled(context.Background(), compiled, exec09Request(rowID))
	if err != nil {
		t.Fatalf("arm B trace over adapter-shaped rows: %v", err)
	}
	if !result.Complete || result.Status != CellTraceValue || result.Value != "Ada" {
		t.Fatalf("adapter-shaped VALUE = %#v", result)
	}
	if len(spies.pgFull) != 1 || spies.pgRequests != 1 {
		t.Fatalf("exactly one full-port call expected: full=%d total=%d", len(spies.pgFull), spies.pgRequests)
	}
	call := spies.pgFull[0]
	want := []string{
		ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField,
		ir.PhysicalCellTraceIdentityPartsField, ir.PhysicalCellTraceStatusField,
		ir.PhysicalCellTraceHasMoreField, ir.PhysicalCellTraceOmissionField,
	}
	if strings.Join(call.Columns, ",") != strings.Join(want, ",") {
		t.Fatalf("declared contract = %#v, want the sealed renderer order %#v", call.Columns, want)
	}
	if len(call.Args) == 0 {
		t.Fatal("every scope and page fact must travel as an ordered parameter")
	}
	for _, leak := range []string{"patient-one", "female", "Ada"} {
		if strings.Contains(call.SQL, leak) {
			t.Fatalf("traced literal %q leaked into the SQL text:\n%s", leak, call.SQL)
		}
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgresCellTraceOfflineIdentityPartsDigestConvergesWithPreview(t *testing.T) {
	compiled, resolved := exec09ArmB(t, 0, 2)
	rowID := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-one"})
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, []map[string]any{
		exec09AdapterRow(t, json.RawMessage(`["patient-two"]`), json.RawMessage(`"Grace"`), "VALUE", false, json.RawMessage(`[]`)),
		exec09AdapterRow(t, json.RawMessage(`["patient-one"]`), json.RawMessage(`"Ada"`), "VALUE", true,
			json.RawMessage(`[{"resourceType":"Patient","resourceId":"patient-one","inputStageId":"source_projection","inputColumnId":"gender_id","inputColumn":"gender","operation":"EXTRACT","value":"female"},{"resourceType":"Patient","resourceId":"patient-one","operation":"EXTRACT"}]`)),
	})
	result, err := engine.CellTraceCompiled(context.Background(), compiled, CellTraceRequest{Output: "resources", RowID: rowID, Column: "name", Offset: 3, Limit: 2})
	if err != nil {
		t.Fatalf("identity-matched trace: %v", err)
	}
	if !result.Complete || result.Status != CellTraceValue || result.Value != "Ada" || !result.HasMore || result.NextOffset != 5 {
		t.Fatalf("paged adapter-shaped VALUE = %#v", result)
	}
	if len(result.Contributions) != 2 ||
		result.Contributions[0].ResourceType != "Patient" || result.Contributions[0].ResourceID != "patient-one" ||
		result.Contributions[0].InputStageID != "source_projection" || result.Contributions[0].InputColumnID != "gender_id" ||
		result.Contributions[0].InputColumn != "gender" || result.Contributions[0].Operation != "EXTRACT" ||
		result.Contributions[0].Value != "female" ||
		result.Contributions[1].ResourceID != "patient-one" || result.Contributions[1].Value != nil {
		t.Fatalf("raw-JSON contributor page decoded wrong: %#v", result.Contributions)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgresCellTraceOfflineThreeStateValueDecode(t *testing.T) {
	compiled, resolved := exec09ArmB(t, 0, 2)
	rowID := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-one"})
	for _, test := range []struct {
		name     string
		value    any
		status   string
		want     any
		wantKind CellTraceStatus
	}{
		{name: "sql-null", value: nil, status: "NO_MATCH", want: nil, wantKind: CellTraceNoMatch},
		{name: "recorded-json-null", value: json.RawMessage(`null`), status: "RECORDED_NULL", want: nil, wantKind: CellTraceRecordedNull},
		{name: "json-number", value: json.RawMessage(`3`), status: "VALUE", want: float64(3), wantKind: CellTraceValue},
		{name: "json-boolean", value: json.RawMessage(`true`), status: "VALUE", want: true, wantKind: CellTraceValue},
		{name: "native-passthrough-convergence", value: "Ada", status: "VALUE", want: "Ada", wantKind: CellTraceValue},
	} {
		t.Run(test.name, func(t *testing.T) {
			spies := &dispatchSpies{}
			engine, _ := exec02pgEngine(t, spies, []map[string]any{exec09AdapterRow(t,
				json.RawMessage(`["patient-one"]`), test.value, test.status, false, json.RawMessage(`[]`))})
			result, err := engine.CellTraceCompiled(context.Background(), compiled, exec09Request(rowID))
			if err != nil {
				t.Fatalf("three-state decode (%s): %v", test.name, err)
			}
			if result.Value != test.want || result.Status != test.wantKind || !result.Complete {
				t.Fatalf("three-state decode (%s) = %#v, want value %#v status %s", test.name, result, test.want, test.wantKind)
			}
		})
	}
}

func TestPostgresCellTraceOfflinePoisonMatrixRefusesFailClosed(t *testing.T) {
	compiled, resolved := exec09ArmB(t, 0, 2)
	rowID := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-one"})
	poison := func(label string, poisonRow map[string]any) {
		t.Helper()
		t.Run(label, func(t *testing.T) {
			spies := &dispatchSpies{}
			engine, _ := exec02pgEngine(t, spies, []map[string]any{poisonRow})
			result, err := engine.CellTraceCompiled(context.Background(), compiled, exec09Request(rowID))
			if err == nil {
				t.Fatalf("poison shape %s was accepted: %#v", label, result)
			}
			if errors.Is(err, ErrCellTraceRowNotFound) {
				t.Fatalf("poison shape %s was skipped as row absence, not refused: %v", label, err)
			}
			if result.Status != "" || result.Complete || result.Value != nil || result.Contributions != nil {
				t.Fatalf("poison shape %s returned a partial result: %#v", label, result)
			}
			if len(spies.pgFull) != 1 {
				t.Fatalf("poison shape %s must fail after the single port call: %d", label, len(spies.pgFull))
			}
		})
	}
	good := exec09AdapterRow(t, json.RawMessage(`["patient-one"]`), json.RawMessage(`"Ada"`), "VALUE", false, json.RawMessage(`[]`))
	for _, page := range []any{nil, json.RawMessage(``), json.RawMessage(`{bad`), json.RawMessage(`null`), json.RawMessage(`{"a":1}`), "[]", 42} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		bad[ir.PhysicalCellTraceContributionsField] = page
		poison("contributions-page", bad)
	}
	for _, value := range []any{json.RawMessage(``), json.RawMessage(`{bad`)} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		bad[ir.PhysicalCellTraceValueField] = value
		poison("traced-value", bad)
	}
	for _, identity := range []any{nil, json.RawMessage(``), json.RawMessage(`{bad`), json.RawMessage(`null`),
		json.RawMessage(`["patient-one","extra"]`), json.RawMessage(`[null]`), json.RawMessage(`"patient-one"`), 7} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		bad[ir.PhysicalCellTraceIdentityPartsField] = identity
		poison("identity-parts", bad)
	}
	for _, status := range []any{nil, "", "MAYBE", json.RawMessage(`"VALUE"`)} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		bad[ir.PhysicalCellTraceStatusField] = status
		poison("status", bad)
	}
	bad := map[string]any{}
	for k, v := range good {
		bad[k] = v
	}
	bad[ir.PhysicalCellTraceContributionsField] = json.RawMessage(`["not-an-object"]`)
	poison("contribution-member", bad)
	bad[ir.PhysicalCellTraceContributionsField] = json.RawMessage(`[42]`)
	poison("contribution-member-number", bad)
	bad[ir.PhysicalCellTraceContributionsField] = json.RawMessage(`[null]`)
	poison("contribution-member-null", bad)
}

func TestPostgresCellTraceOfflineExplicitIdentityRawShapes(t *testing.T) {
	pgCommand := exec02PostgreSQLCommand(t)
	newQuery := func(mutate func(*compiler.CompiledCellTraceQuery)) compiler.CompiledCellTraceQuery {
		query := compiler.CompiledCellTraceQuery{
			Command: pgCommand, ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
			StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
			OmissionColumn: ir.PhysicalCellTraceOmissionField, ExplicitIdentityColumn: ir.PhysicalCellTraceExplicitIdentityField,
		}
		mutate(&query)
		return query
	}
	t.Run("scalar", func(t *testing.T) {
		query := newQuery(func(*compiler.CompiledCellTraceQuery) {})
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{{
			ir.PhysicalCellTraceExplicitIdentityField: json.RawMessage(`"patient-7"`),
			ir.PhysicalCellTraceValueField:            json.RawMessage(`"female"`),
			ir.PhysicalCellTraceStatusField:           "VALUE", ir.PhysicalCellTraceHasMoreField: false,
			ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: json.RawMessage(`[]`),
		}})
		result, err := engine.CellTraceCompiled(context.Background(), query, exec09Request("patient-7"))
		if err != nil || result.Status != CellTraceValue || result.Value != "female" {
			t.Fatalf("raw scalar explicit identity: result=%#v err=%v", result, err)
		}
	})
	t.Run("object", func(t *testing.T) {
		query := newQuery(func(q *compiler.CompiledCellTraceQuery) { q.ExplicitIdentityObject = true })
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{{
			ir.PhysicalCellTraceExplicitIdentityField: json.RawMessage(`{"group_id":"cohort-a","group_revision_id":"rev-1"}`),
			ir.PhysicalCellTraceValueField:            json.RawMessage(`"Pair"`),
			ir.PhysicalCellTraceStatusField:           "VALUE", ir.PhysicalCellTraceHasMoreField: false,
			ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: json.RawMessage(`[]`),
		}})
		result, err := engine.CellTraceCompiled(context.Background(), query, exec09Request(`{"group_id":"cohort-a","group_revision_id":"rev-1"}`))
		if err != nil || result.Status != CellTraceValue || result.Value != "Pair" {
			t.Fatalf("raw object explicit identity: result=%#v err=%v", result, err)
		}
	})
	for _, forged := range []struct {
		name  string
		raw   json.RawMessage
		rowID string
	}{
		{name: "extra-key", raw: json.RawMessage(`{"group_id":"cohort-a","group_revision_id":"rev-1","extra":"forged"}`), rowID: `{"group_id":"cohort-a","group_revision_id":"rev-1"}`},
		{name: "blank-scalar", raw: json.RawMessage(`"  "`), rowID: ""},
		{name: "json-null-scalar", raw: json.RawMessage(`null`), rowID: ""},
	} {
		t.Run(forged.name, func(t *testing.T) {
			query := newQuery(func(q *compiler.CompiledCellTraceQuery) {
				q.ExplicitIdentityObject = strings.HasPrefix(forged.name, "extra")
			})
			spies := &dispatchSpies{}
			engine, _ := exec02pgEngine(t, spies, []map[string]any{{
				ir.PhysicalCellTraceExplicitIdentityField: forged.raw,
				ir.PhysicalCellTraceValueField:            json.RawMessage(`"x"`),
				ir.PhysicalCellTraceStatusField:           "VALUE", ir.PhysicalCellTraceHasMoreField: false,
				ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: json.RawMessage(`[]`),
			}})
			if forged.rowID == "" {
				if _, err := engine.CellTraceCompiled(context.Background(), query, exec09Request("anything")); err == nil {
					t.Fatalf("forged identity %s was accepted", forged.name)
				}
				return
			}
			result, err := engine.CellTraceCompiled(context.Background(), query, exec09Request(forged.rowID))
			if err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("forged identity %s: result=%#v err=%v", forged.name, result, err)
			}
		})
	}
}

func TestPostgresCellTraceOfflineWitnessCapReportsIncompleteNotAbsence(t *testing.T) {
	compiled, resolved := exec09ArmB(t, 0, 2)
	target := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-three"})
	rows := []map[string]any{
		exec09AdapterRow(t, json.RawMessage(`["patient-one"]`), json.RawMessage(`"Ada"`), "VALUE", false, json.RawMessage(`[]`)),
		exec09AdapterRow(t, json.RawMessage(`["patient-two"]`), json.RawMessage(`"Grace"`), "VALUE", false, json.RawMessage(`[]`)),
		exec09AdapterRow(t, json.RawMessage(`["patient-three"]`), json.RawMessage(`"Radium"`), "VALUE", false, json.RawMessage(`[]`)),
	}
	spies := &dispatchSpies{}
	capped, _ := exec02pgEngine(t, spies, rows)
	result, err := capped.CellTraceCompiled(context.Background(), compiled, CellTraceRequest{Output: "resources", RowID: target, Column: "name", MaxWitnessRows: 2})
	if err != nil {
		t.Fatalf("capped witness scan: %v", err)
	}
	if result.Status != CellTraceIncomplete || result.Complete || result.OmissionCode != "TRACE_WITNESS_INCOMPLETE" {
		t.Fatalf("capped scan must report incompleteness, never absence: %#v", result)
	}
	spies = &dispatchSpies{}
	uncapped, _ := exec02pgEngine(t, spies, rows)
	result, err = uncapped.CellTraceCompiled(context.Background(), compiled, exec09Request(target))
	if err != nil || result.Status != CellTraceValue || result.Value != "Radium" || !result.Complete {
		t.Fatalf("permissive-cap control must read VALUE: result=%#v err=%v", result, err)
	}
}

func TestPostgresCellTraceOfflineCrossTagAndUnregisteredRefusals(t *testing.T) {
	t.Run("postgres-tag-into-aql-port", func(t *testing.T) {
		compiled, _ := exec09ArmB(t, 0, 2)
		calls := 0
		engine := &Engine{compileTarget: exec02PostgreSQLTarget(t), sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			calls++
			_, _, err := request.Command.AQLParameters()
			return err
		}}
		_, err := engine.CellTraceCompiled(context.Background(), compiled, exec09Request("whatever"))
		if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("postgres-tagged command through the AQL port = %v, want the typed mismatch", err)
		}
		if calls != 1 {
			t.Fatalf("port call count = %d, want 1", calls)
		}
	})
	t.Run("aql-command-into-postgres-port", func(t *testing.T) {
		resolved := exec02TestResolved()
		aqlTrace, err := compiler.CompileCellTraceOutput(compiler.ArangoTarget(),
			resolved.Compiled.Outputs[0], "name", 0, 2, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("AQL control compile: %v", err)
		}
		engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			_, _, err := request.Command.PostgreSQLParameters()
			return err
		}}
		if _, err := engine.CellTraceCompiled(context.Background(), aqlTrace, exec09Request("whatever")); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("AQL command through the PostgreSQL port = %v, want the typed mismatch", err)
		}
	})
	t.Run("unregistered-zero-tag", func(t *testing.T) {
		calls := 0
		query := compiler.CompiledCellTraceQuery{
			Command:     command.CompiledCommand{},
			ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
			StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
			OmissionColumn: ir.PhysicalCellTraceOmissionField, IdentityPartsColumn: ir.PhysicalCellTraceIdentityPartsField,
			RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		}
		engine := &Engine{compileTarget: exec02PostgreSQLTarget(t), sourceRows: func(_ context.Context, _ RowsRequest, _ RowVisitor) error {
			calls++
			return nil
		}}
		if _, err := engine.CellTraceCompiled(context.Background(), query, exec09Request("whatever")); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero tag = %v, want the named not-registered refusal", err)
		}
		if calls != 0 {
			t.Fatalf("zero-tag command reached the port %d times", calls)
		}
	})
}

func TestPostgresCellTraceOfflineSealedContractRefusals(t *testing.T) {
	pgCommand := exec02PostgreSQLCommand(t)
	base := func() compiler.CompiledCellTraceQuery {
		return compiler.CompiledCellTraceQuery{
			Command: pgCommand, ValueColumn: ir.PhysicalCellTraceValueField, ContributionsColumn: ir.PhysicalCellTraceContributionsField,
			StatusColumn: ir.PhysicalCellTraceStatusField, HasMoreColumn: ir.PhysicalCellTraceHasMoreField,
			OmissionColumn: ir.PhysicalCellTraceOmissionField, IdentityPartsColumn: ir.PhysicalCellTraceIdentityPartsField,
			RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		}
	}
	t.Run("duplicate-contract-name", func(t *testing.T) {
		query := base()
		query.HasMoreColumn = query.StatusColumn
		calls := 0
		engine := &Engine{compileTarget: exec02PostgreSQLTarget(t), sourceRows: func(_ context.Context, _ RowsRequest, _ RowVisitor) error {
			calls++
			return nil
		}}
		if _, err := engine.CellTraceCompiled(context.Background(), query, exec09Request("whatever")); err == nil ||
			!strings.Contains(err.Error(), "missing typed result columns") || calls != 0 {
			t.Fatalf("duplicate contract name: err=%v calls=%d", err, calls)
		}
	})
	t.Run("identity-pick-law", func(t *testing.T) {
		query := base()
		query.ExplicitIdentityColumn = ir.PhysicalCellTraceExplicitIdentityField
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{{
			ir.PhysicalCellTraceExplicitIdentityField: json.RawMessage(`"row-a"`),
			ir.PhysicalCellTraceValueField:            json.RawMessage(`"x"`),
			ir.PhysicalCellTraceStatusField:           "VALUE", ir.PhysicalCellTraceHasMoreField: false,
			ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: json.RawMessage(`[]`),
		}})
		if _, err := engine.CellTraceCompiled(context.Background(), query, exec09Request("row-a")); err != nil {
			t.Fatalf("both-identity forged query: %v", err)
		}
		if len(spies.pgFull) != 1 {
			t.Fatalf("port calls = %d", len(spies.pgFull))
		}
		got := spies.pgFull[0].Columns
		if len(got) != 6 || got[2] != ir.PhysicalCellTraceExplicitIdentityField {
			t.Fatalf("contract identity slot = %#v, want the explicit pick at index 2", got)
		}
	})
}

func TestPostgresCellTraceOfflineAdmissionRefusalsRunZeroStatements(t *testing.T) {
	compiled, _ := exec09ArmB(t, 0, 2)
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, nil)
	if _, err := engine.CellTrace(context.Background(), exec02TestResolved(), CellTraceRequest{Output: "  ", RowID: "r", Column: "name"}); err == nil ||
		!strings.Contains(err.Error(), "output is required") {
		t.Fatalf("blank output: %v", err)
	}
	if _, err := engine.CellTraceCompiled(context.Background(), compiled, exec09Request("   ")); err == nil ||
		!strings.Contains(err.Error(), "row ID and column are required") {
		t.Fatalf("blank row ID: %v", err)
	}
	if _, err := engine.CellTrace(context.Background(), exec02TestResolved(), CellTraceRequest{Output: "no-such-output", RowID: "r", Column: "name"}); err == nil ||
		!strings.Contains(err.Error(), "was not found") {
		t.Fatalf("unknown output: %v", err)
	}
	if spies.pgRequests != 0 || len(spies.aqlQueries) != 0 {
		t.Fatalf("admission refusals touched the ports: pg=%d aql=%v", spies.pgRequests, spies.aqlQueries)
	}
}

func TestPostgresCellTraceOfflineFailureClassificationOnPostgresArm(t *testing.T) {
	compiled, _ := exec09ArmB(t, 0, 2)
	for _, test := range []struct {
		code   dataframeerrors.ErrorCode
		status CellTraceStatus
	}{
		{code: dataframeerrors.CodeTemporalTieAmbiguous, status: CellTraceAmbiguous},
		{code: dataframeerrors.CodeRelationshipCardinalityViolation, status: CellTraceAmbiguous},
		{code: dataframeerrors.CodeUnitIdentityUnknown, status: CellTraceInvalidUnit},
		{code: dataframeerrors.CodeUnitDimensionIncompatible, status: CellTraceInvalidUnit},
		{code: dataframeerrors.CodeInvalidData, status: CellTraceInvalidType},
		{code: dataframeerrors.CodeRecipeContractViolation, status: CellTraceInvalidType},
	} {
		t.Run(string(test.code), func(t *testing.T) {
			pgFailing := &Engine{compileTarget: exec02PostgreSQLTarget(t), sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
					return err
				}
				return dataframeerrors.NewError(test.code, "")
			}}
			result, err := pgFailing.CellTraceCompiled(context.Background(), compiled, exec09Request("row-1"))
			if err != nil || !result.Complete || result.Status != test.status || result.OmissionCode != string(test.code) || result.Contributions == nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestPostgresCellTraceOfflineAQLControlStaysContractFree(t *testing.T) {
	resolved := exec02TestResolved()
	aqlTrace, err := compiler.CompileCellTraceOutput(compiler.ArangoTarget(),
		resolved.Compiled.Outputs[0], "name", 0, 2, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("AQL control compile: %v", err)
	}
	if aqlTrace.Command.Backend() != command.CompiledBackendAQL {
		t.Fatalf("AQL control tag = %q", aqlTrace.Command.Backend())
	}
	var captured []string
	capturedSet := false
	engine := &Engine{compileTarget: compiler.ArangoTarget(), sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		if _, _, err := request.Command.AQLParameters(); err != nil {
			return err
		}
		captured = request.DeclaredColumns
		capturedSet = request.DeclaredColumns != nil
		return visit(map[string]any{
			aqlTrace.IdentityPartsColumn: []any{"patient-one"}, aqlTrace.ValueColumn: "Ada",
			aqlTrace.StatusColumn: "VALUE", aqlTrace.HasMoreColumn: false, aqlTrace.OmissionColumn: "",
			aqlTrace.ContributionsColumn: nil,
		})
	}}
	rowID := exec09PreviewRowID(t, resolved.Compiled.Outputs[0].RowIdentity, map[string]any{"id": "patient-one"})
	result, err := engine.CellTraceCompiled(context.Background(), aqlTrace, exec09Request(rowID))
	if err != nil || result.Status != CellTraceValue || result.Value != "Ada" {
		t.Fatalf("AQL control trace: result=%#v err=%v", result, err)
	}
	if capturedSet || captured != nil {
		t.Fatalf("AQL arm must travel contract-free: %#v", captured)
	}
	if len(result.Contributions) != 0 {
		t.Fatalf("AQL nil-page leniency changed: %#v", result.Contributions)
	}
}
