package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPopulationMappingCompiledDeduplicatesMembersAndRows(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(),
		batchSize: 1000,
		sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			for _, row := range []map[string]any{
				{"member": "file-001", "identity": []any{"specimen-001"}},
				{"member": "file-002", "identity": []any{"specimen-001"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}
	reader := PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		for _, id := range []string{"file-001", "file-002", "file-002", "file-004"} {
			if err := visit(id); err != nil {
				return err
			}
		}
		return nil
	})
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Command:      mustAQLCommand("RETURN witnesses", nil),
		MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{MaxUnmapped: 10}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 1 {
		t.Fatalf("population mapping result = %#v", result)
	}
	if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "file-004" {
		t.Fatalf("unmapped member IDs = %#v", result.UnmappedMemberIDs)
	}
}

func TestPopulationMappingCompiledReturnsIncompleteWithoutCountsOnLimit(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(),
		batchSize: 1000,
		sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return visit(map[string]any{"member": "file-001", "identity": []any{"specimen-001"}})
		},
	}
	reader := PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		return visit("file-001")
	})
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Command: mustAQLCommand("RETURN witnesses", nil), MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{MaxWitnessRows: 0}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete {
		t.Fatalf("default witness limit unexpectedly incomplete: %#v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	incomplete, err := engine.PopulationMappingCompiled(ctx, compiler.CompiledPopulationMappingQuery{
		Command: mustAQLCommand("RETURN witnesses", nil), MemberColumn: "member", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Status != PopulationMappingIncomplete || incomplete.SelectedCount != 0 || incomplete.MappedCount != 0 || incomplete.UnmappedCount != 0 || incomplete.EmittedRows != 0 {
		t.Fatalf("incomplete result exposed exact counts: %#v", incomplete)
	}
}

func TestPopulationWitnessIdentityRejectsWrongShape(t *testing.T) {
	_, err := populationWitnessIdentity(compiler.CompiledPopulationMappingQuery{
		IdentityPartsColumn: "identity", RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, map[string]any{"identity": "not-an-array"})
	if err == nil {
		t.Fatal("expected invalid identity shape")
	}
}

func TestPopulationWitnessIdentityCanonicalizesExplicitScalars(t *testing.T) {
	query := compiler.CompiledPopulationMappingQuery{ExplicitIdentityColumn: "identity"}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "row-1", want: `explicit:string:"row-1"`},
		{name: "number", value: float64(7), want: "explicit:number:7"},
		{name: "number equivalent", value: json.Number("7.0"), want: "explicit:number:7"},
		{name: "fraction", value: float64(0.5), want: "explicit:number:1/2"},
		{name: "fraction decimal equivalent", value: json.Number("0.50"), want: "explicit:number:1/2"},
		{name: "fraction exponent equivalent", value: json.Number("5e-1"), want: "explicit:number:1/2"},
		{name: "boolean", value: true, want: "explicit:bool:true"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := populationWitnessIdentity(query, map[string]any{"identity": test.value})
			if err != nil || got != test.want {
				t.Fatalf("identity = %q, err = %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestPopulationWitnessIdentityRejectsNullAndStructuredExplicitValues(t *testing.T) {
	query := compiler.CompiledPopulationMappingQuery{ExplicitIdentityColumn: "identity"}
	for _, value := range []any{nil, []any{"row-1"}, map[string]any{"id": "row-1"}} {
		if _, err := populationWitnessIdentity(query, map[string]any{"identity": value}); err == nil {
			t.Fatalf("identity value %#v unexpectedly accepted", value)
		}
	}
}

func TestPopulationMappingCompiledDeduplicatesCanonicalExplicitNumericIdentities(t *testing.T) {
	engine := &Engine{compileTarget: compiler.ArangoTarget(),
		batchSize: 1000,
		sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			for _, row := range []map[string]any{
				{"member": "file-001", "identity": float64(0.5)},
				{"member": "file-001", "identity": json.Number("0.50")},
				{"member": "file-001", "identity": json.Number("5e-1")},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}
	result, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Command: mustAQLCommand("RETURN witnesses", nil), MemberColumn: "member", ExplicitIdentityColumn: "identity",
	}, PopulationMappingRequest{MaxUnmapped: 10}, PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		return visit("file-001")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.MappedCount != 1 || result.EmittedRows != 1 {
		t.Fatalf("population mapping result = %#v", result)
	}
}

func exec07PostgreSQLTarget(t *testing.T) compiler.Target {
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

func exec07MappingCommand(t *testing.T) command.CompiledCommand {
	t.Helper()
	compiled, err := command.NewPostgreSQLCommand(
		`SELECT w0.member AS "c0", jsonb_build_array(s."id") AS "c1" FROM "loom"."specimen" AS s CROSS JOIN LATERAL jsonb_array_elements_text('[]'::jsonb) AS w0(member) ORDER BY w0.member COLLATE "C" ASC`,
		[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "sha256:members"}},
	)
	if err != nil {
		t.Fatalf("NewPostgreSQLCommand: %v", err)
	}
	return compiled
}

type exec07Port struct {
	calls        int
	requests     []RowsRequest
	rows         []map[string]any
	fail         error
	beforeVisit  func(index int)
	visitStarted bool
}

func (p *exec07Port) sourceRows(ctx context.Context, request RowsRequest, visit RowVisitor) error {
	if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
		return err
	}
	p.calls++
	p.requests = append(p.requests, request)
	for index, row := range p.rows {
		if p.beforeVisit != nil {
			p.beforeVisit(index)
		}
		p.visitStarted = true
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if p.fail != nil {
		return p.fail
	}
	return nil
}

func exec07Members(ids ...string) PopulationMemberReader {
	return PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		for _, id := range ids {
			if err := visit(id); err != nil {
				return err
			}
		}
		return nil
	})
}

func exec07Query(t *testing.T, engine *Engine, request PopulationMappingRequest, reader PopulationMemberReader) (PopulationMappingResult, error) {
	t.Helper()
	return engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		Command: exec07MappingCommand(t), MemberColumn: "c0", IdentityPartsColumn: "c1",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, request, reader)
}

func TestPostgresPopulationMappingExactMembershipAndDedupe(t *testing.T) {
	port := &exec07Port{rows: []map[string]any{
		{"c0": "member-1", "c1": []any{"row-a"}},
		{"c0": "member-1", "c1": []any{"row-a"}},
		{"c0": "member-1", "c1": []any{"row-b"}},
		{"c0": "member-2", "c1": []any{"row-c"}},
	}}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := exec07Query(t, engine, PopulationMappingRequest{MaxUnmapped: 10}, exec07Members("member-1", "member-2", "member-3"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 3 {
		t.Fatalf("population mapping result = %#v", result)
	}
	if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "member-3" {
		t.Fatalf("unmapped member IDs = %#v", result.UnmappedMemberIDs)
	}
	if port.calls != 1 || len(port.requests) != 1 {
		t.Fatalf("port calls = %d, requests = %d", port.calls, len(port.requests))
	}
	if got := port.requests[0].DeclaredColumns; len(got) != 2 || got[0] != "c0" || got[1] != "c1" {
		t.Fatalf("declared columns = %#v, want [c0 c1] in renderer output order", got)
	}
	if port.requests[0].BatchSize != 1000 {
		t.Fatalf("batch hint = %d", port.requests[0].BatchSize)
	}
}

func TestPostgresPopulationMappingRawMessagePartsMatchNativeIdentity(t *testing.T) {
	port := &exec07Port{rows: []map[string]any{
		{"c0": "member-1", "c1": []any{"obs-a"}},
		{"c0": "member-2", "c1": json.RawMessage(`["obs-a"]`)},
	}}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := exec07Query(t, engine, PopulationMappingRequest{MaxUnmapped: 10}, exec07Members("member-1", "member-2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.SelectedCount != 2 || result.MappedCount != 2 || result.UnmappedCount != 0 || result.EmittedRows != 1 {
		t.Fatalf("cross-shape identity equality broken: %#v, want two mapped members collapsing to one emitted identity", result)
	}
	for _, port := range []*exec07Port{
		{rows: []map[string]any{
			{"c0": "member-1", "c1": json.RawMessage(`["obs-a"]`)},
			{"c0": "member-2", "c1": json.RawMessage(`["obs-b"]`)},
		}},
		{rows: []map[string]any{
			{"c0": "member-1", "c1": []any{"obs-a"}},
			{"c0": "member-2", "c1": []any{"obs-b"}},
		}},
	} {
		engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
		result, err := exec07Query(t, engine, PopulationMappingRequest{}, exec07Members("member-1", "member-2", "member-3"))
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 2 ||
			len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "member-3" {
			t.Fatalf("adapter-shape and native-shape runs must agree exactly; got %#v", result)
		}
	}
}

func TestPostgresPopulationMappingRawMessageViolationsFailClosed(t *testing.T) {
	for _, violation := range []struct {
		name string
		row  map[string]any
	}{
		{"empty bytes", map[string]any{"c0": "member-1", "c1": json.RawMessage("")}},
		{"malformed json", map[string]any{"c0": "member-1", "c1": json.RawMessage(`["obs-a"`)}},
		{"json null scalar", map[string]any{"c0": "member-1", "c1": json.RawMessage("null")}},
		{"json object", map[string]any{"c0": "member-1", "c1": json.RawMessage(`{"project":"P1"}`)}},
		{"json scalar", map[string]any{"c0": "member-1", "c1": json.RawMessage(`"obs-a"`)}},
		{"missing key (sql null)", map[string]any{"c0": "member-1"}},
		{"explicit nil", map[string]any{"c0": "member-1", "c1": nil}},
	} {
		port := &exec07Port{rows: []map[string]any{
			{"c0": "member-1", "c1": []any{"obs-a"}},
			violation.row,
			{"c0": "member-2", "c1": []any{"obs-b"}},
		}}
		engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
		result, err := exec07Query(t, engine, PopulationMappingRequest{}, exec07Members("member-1", "member-2"))
		if err == nil || !strings.Contains(err.Error(), "invalid row identity shape") {
			t.Fatalf("%s: violation error = %v, want the typed invalid-shape refusal", violation.name, err)
		}
		if want := "population mapping witness has an invalid row identity shape"; !strings.HasSuffix(err.Error(), want) {
			t.Fatalf("%s: violation error = %v, want it to end in the decoder sentinel %q", violation.name, err, want)
		}
		if errors.Is(err, ErrPopulationMappingPostgresUnsupported) || errors.Is(err, errPopulationMappingLimit) {
			t.Fatalf("%s: violation misclassified as a capability gap or cap: %v", violation.name, err)
		}
		if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 || result.UnmappedCount != 0 || result.EmittedRows != 0 ||
			result.UnmappedMemberIDs != nil || result.HasMoreUnmapped {
			t.Fatalf("%s: violation exposed a partial mapping: %#v", violation.name, result)
		}
		if !port.visitStarted || port.calls != 1 {
			t.Fatalf("%s: refusal must run through the one port call after the witness stream started: calls=%d", violation.name, port.calls)
		}
	}
}

func TestPostgresPopulationMappingForeignWitnessRejected(t *testing.T) {
	port := &exec07Port{rows: []map[string]any{{"c0": "member-9", "c1": []any{"row-a"}}}}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	_, err := exec07Query(t, engine, PopulationMappingRequest{}, exec07Members("member-1"))
	if err == nil || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("foreign witness error = %v", err)
	}
	if errors.Is(err, errPopulationMappingLimit) {
		t.Fatalf("foreign witness misread as a cap: %v", err)
	}
}

func TestPostgresPopulationMappingCapIsIncompleteNotPartial(t *testing.T) {
	port := &exec07Port{rows: []map[string]any{
		{"c0": "member-1", "c1": []any{"row-a"}},
		{"c0": "member-2", "c1": []any{"row-b"}},
	}}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := exec07Query(t, engine, PopulationMappingRequest{MaxWitnessRows: 1}, exec07Members("member-1", "member-2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingIncomplete || result.SelectedCount != 0 || result.MappedCount != 0 || result.UnmappedCount != 0 || result.EmittedRows != 0 {
		t.Fatalf("cap produced a misleading partial result: %#v", result)
	}
	if !port.visitStarted {
		t.Fatal("cap test never streamed a witness row")
	}
}

func TestPostgresPopulationMappingCancellationIsIncompleteAtRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port := &exec07Port{
		rows: []map[string]any{
			{"c0": "member-1", "c1": []any{"row-a"}},
			{"c0": "member-2", "c1": []any{"row-b"}},
		},
	}
	port.beforeVisit = func(index int) {
		if index == 1 {
			cancel()
		}
	}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := engine.PopulationMappingCompiled(ctx, compiler.CompiledPopulationMappingQuery{
		Command: exec07MappingCommand(t), MemberColumn: "c0", IdentityPartsColumn: "c1",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{}, exec07Members("member-1", "member-2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingIncomplete || result.SelectedCount != 0 || result.MappedCount != 0 || result.EmittedRows != 0 {
		t.Fatalf("cancellation produced a misleading partial result: %#v", result)
	}
	if !port.visitStarted {
		t.Fatal("cancellation test never streamed a witness row")
	}
}

func TestPostgresPopulationMappingCapabilityRefusalBeforePortCall(t *testing.T) {
	var portCalls int
	engine := &Engine{
		compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000,
		sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
			portCalls++
			return nil
		},
	}
	compiled := compiler.CompiledPopulationMappingQuery{
		Command: exec07MappingCommand(t), MemberColumn: "c0", IdentityPartsColumn: "c1",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}
	noPort := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000}
	result, err := noPort.PopulationMappingCompiled(context.Background(), compiled, PopulationMappingRequest{}, exec07Members("member-1"))
	if !errors.Is(err, ErrPopulationMappingPostgresUnsupported) {
		t.Fatalf("nil-port error = %v, want ErrPopulationMappingPostgresUnsupported", err)
	}
	if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 {
		t.Fatalf("capability refusal exposed counts: %#v", result)
	}
	unregistered := &exec07Port{fail: fmt.Errorf("postgres rows: %w", command.ErrCompiledCommandBackendNotRegistered)}
	engine.sourceRows = unregistered.sourceRows
	result, err = engine.PopulationMappingCompiled(context.Background(), compiled, PopulationMappingRequest{}, exec07Members("member-1"))
	if !errors.Is(err, ErrPopulationMappingPostgresUnsupported) || errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("unregistered-adapter error = %v", err)
	}
	if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 {
		t.Fatalf("unregistered-adapter refusal exposed counts: %#v", result)
	}
	if unregistered.visitStarted {
		t.Fatal("unregistered adapter visited witness rows")
	}
	if portCalls != 0 {
		t.Fatalf("refusal path reached a port call: %d", portCalls)
	}
}

func TestPostgresPopulationMappingRoutingMismatchIsHardError(t *testing.T) {
	port := &exec07Port{fail: fmt.Errorf("dispatch: %w for %q", command.ErrCompiledCommandBackendMismatch, "POSTGRESQL")}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := exec07Query(t, engine, PopulationMappingRequest{}, exec07Members("member-1"))
	if err == nil || !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("routing mismatch error = %v", err)
	}
	if errors.Is(err, ErrPopulationMappingPostgresUnsupported) {
		t.Fatalf("routing mismatch misread as a capability gap: %v", err)
	}
	if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 {
		t.Fatalf("routing mismatch exposed counts: %#v", result)
	}
}

func TestPostgresPopulationMappingGenericPortFailureIsHardError(t *testing.T) {
	port := &exec07Port{rows: []map[string]any{{"c0": "member-1", "c1": []any{"row-a"}}}, fail: errors.New("connection reset")}
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: port.sourceRows}
	result, err := exec07Query(t, engine, PopulationMappingRequest{}, exec07Members("member-1"))
	if err == nil || errors.Is(err, ErrPopulationMappingPostgresUnsupported) || result.Status == PopulationMappingIncomplete || result.Status == PopulationMappingComplete {
		t.Fatalf("generic port failure was not a hard error: result=%#v err=%v", result, err)
	}
}

func TestPostgresPopulationMappingContractRefusesBeforeSQL(t *testing.T) {
	var calls int
	ping := func(context.Context, RowsRequest, RowVisitor) error {
		calls++
		return nil
	}
	base := compiler.CompiledPopulationMappingQuery{MemberColumn: "c0", IdentityPartsColumn: "c1", RowIdentity: &spec.RowIdentity{Fields: []string{"id"}}}
	for _, mutate := range []func(q *compiler.CompiledPopulationMappingQuery){
		func(q *compiler.CompiledPopulationMappingQuery) { q.MemberColumn = "" },
		func(q *compiler.CompiledPopulationMappingQuery) { q.IdentityPartsColumn = "" },
		func(q *compiler.CompiledPopulationMappingQuery) { q.ExplicitIdentityColumn = "c2" },
	} {
		compiled := base
		compiled.Command = exec07MappingCommand(t)
		mutate(&compiled)
		engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: ping}
		if _, err := engine.PopulationMappingCompiled(context.Background(), compiled, PopulationMappingRequest{}, exec07Members("member-1")); err == nil {
			t.Fatalf("broken witness contract accepted: %#v", compiled)
		}
	}
	if calls != 0 {
		t.Fatalf("contract refusal reached the port: %d calls", calls)
	}
}

func TestPostgresPopulationMappingUnknownBackendIsHardError(t *testing.T) {
	engine := &Engine{compileTarget: exec07PostgreSQLTarget(t), batchSize: 1000, sourceRows: func(context.Context, RowsRequest, RowVisitor) error { return nil }}
	_, err := engine.PopulationMappingCompiled(context.Background(), compiler.CompiledPopulationMappingQuery{
		MemberColumn: "c0", IdentityPartsColumn: "c1", RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
	}, PopulationMappingRequest{}, exec07Members("member-1"))
	if err == nil || !strings.Contains(err.Error(), "unsupported backend") {
		t.Fatalf("untagged command error = %v", err)
	}
}

func exec07RouteBundle(name string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "exec07",
		Outputs: []recipe.Output{{
			Name: "specimens", RootResourceType: "Specimen", RowGrain: "specimen",
			Fields: []recipe.Field{{Name: "id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "sel-exec07", MembershipDigest: "sha256:exec07-members", MemberCount: 3,
				ResourceType: "DocumentReference",
				Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
			},
		}},
	}
}

func exec07FlatBundle(name string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "exec07",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "sel-exec07", MembershipDigest: "sha256:exec07-members", MemberCount: 3,
				ResourceType: "Observation",
			},
		}},
	}
}

func exec07Bindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		SelectionProject:           "project-a",
		SelectionMembersCollection: "loom_explorer_selection_members",
		AuthResourcePaths:          []string{"FHIR/Observation", "FHIR/Specimen", "FHIR/DocumentReference"},
	}
}

func exec07Engine(t *testing.T, port SourceRows) *Engine {
	t.Helper()
	config := (&dispatchSpies{}).arangoEngine()
	config.CompileTarget = exec07PostgreSQLTarget(t)
	config.SourceRows = port
	return config.newEngineForTest(t)
}

func TestPostgresPopulationMappingEngineDispatchesFlatWitnessTerminal(t *testing.T) {
	var portCalls int
	var requests []RowsRequest
	engine := exec07Engine(t, func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		portCalls++
		requests = append(requests, request)
		for _, row := range []map[string]any{
			{ir.PhysicalPopulationMappingMemberField: "obs-1", ir.PhysicalPopulationMappingIdentityPartsField: []any{"project-a", "obs-1"}},
			{ir.PhysicalPopulationMappingMemberField: "obs-2", ir.PhysicalPopulationMappingIdentityPartsField: []any{"project-a", "obs-2"}},
		} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	})
	resolved, err := engine.CompileResolvedBundle(context.Background(), exec07FlatBundle("exec07-flat"), exec07Bindings("project-a", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %v", err)
	}
	result, err := engine.PopulationMapping(context.Background(), resolved, PopulationMappingRequest{Output: "observations", MaxUnmapped: 10}, exec07Members("obs-1", "obs-2", "obs-3"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 2 {
		t.Fatalf("flat mapping result = %#v", result)
	}
	if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "obs-3" {
		t.Fatalf("unmapped member IDs = %#v", result.UnmappedMemberIDs)
	}
	if portCalls != 1 || len(requests) != 1 {
		t.Fatalf("port calls = %d", portCalls)
	}
	if got := requests[0].DeclaredColumns; len(got) != 2 ||
		got[0] != ir.PhysicalPopulationMappingMemberField ||
		got[1] != ir.PhysicalPopulationMappingIdentityPartsField {
		t.Fatalf("engine-declared witness contract = %#v, want the compiler-owned member and identity-parts fields", got)
	}
}

func TestPostgresPopulationMappingNamedCapabilityGapRefusesAtZeroStatements(t *testing.T) {
	expansion := exec07FlatBundle("exec07-expand")
	expansion.Outputs[0].Expand = &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"}
	expansion.Outputs[0].RowGrain = "expanded"
	expansion.Outputs[0].Identity = &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}}
	for _, test := range []struct {
		name   string
		bundle recipe.Bundle
		output string
	}{
		{"expansion on the mapping root", expansion, "observations"},
		{"endpoint-lookup route", exec07RouteBundle("exec07-route"), "specimens"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var portCalls int
			engine := exec07Engine(t, func(context.Context, RowsRequest, RowVisitor) error {
				portCalls++
				return nil
			})
			resolved, err := engine.CompileResolvedBundle(context.Background(), test.bundle, exec07Bindings("project-a", "gen-a"))
			if err != nil {
				t.Fatalf("CompileResolvedBundle: %v", err)
			}
			result, err := engine.PopulationMapping(context.Background(), resolved, PopulationMappingRequest{Output: test.output, MaxUnmapped: 10}, exec07Members("obs-1", "obs-2", "obs-3"))
			if !errors.Is(err, ErrPopulationMappingPostgresUnsupported) {
				t.Fatalf("error = %v, want ErrPopulationMappingPostgresUnsupported", err)
			}
			if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 {
				t.Fatalf("capability refusal exposed counts: %#v", result)
			}
			if !strings.Contains(err.Error(), "unsupported physical plan") {
				t.Fatalf("refusal lost the renderer's typed gap evidence: %v", err)
			}
			if portCalls != 0 {
				t.Fatalf("refusal executed %d statements, want the statement-count-0 witness", portCalls)
			}
		})
	}
}
