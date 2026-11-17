package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestSelectedSourceCompilerAndRowsContract(t *testing.T) {
	t.Run("StreamRunsExactlyTheSelectedTargetCompiledCommand", func(t *testing.T) {
		spies := &dispatchSpies{}
		resolved := exec02TestResolved()
		reference, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
			resolved.Compiled.Outputs[0],
			resolved.Semantic.SemanticPlan.Bindings,
			0, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("reference compile: %v", err)
		}
		var observed RowsRequest
		calls := 0
		config := spies.arangoEngine()
		config.CompileTarget = exec02PostgreSQLTarget(t)
		config.SourceRows = func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			calls++
			observed = request
			for _, row := range exec02Rows() {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		}
		config.PreviewSourceRows = nil
		engine := config.newEngineForTest(t)
		streams, err := engine.Streams(context.Background(), resolved)
		if err != nil {
			t.Fatalf("Streams: %v", err)
		}
		if len(streams) != 1 {
			t.Fatalf("streams: %d", len(streams))
		}
		if _, err := streams[0].Stream(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if calls != 1 {
			t.Fatalf("port invocations: %d, want exactly one for the parent-tagged command", calls)
		}
		if observed.Command.Backend() != command.CompiledBackendPostgreSQL {
			t.Fatalf("port received %q-tagged command", observed.Command.Backend())
		}
		gotSQL, gotArgs, err := observed.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("observed command: %v", err)
		}
		wantSQL, wantArgs, err := reference.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("reference command: %v", err)
		}
		if gotSQL != wantSQL || len(gotArgs) != len(wantArgs) {
			t.Fatalf("stream ran a command other than the selected target compiled")
		}
		if streams[0].planFingerprint != reference.PlanDiagnostics.Fingerprint {
			t.Fatalf("stream fingerprint %q diverged from the reference compile %q", streams[0].planFingerprint, reference.PlanDiagnostics.Fingerprint)
		}
		if len(observed.DeclaredColumns) != len(reference.Columns) {
			t.Fatalf("declared columns %v want %v", observed.DeclaredColumns, reference.Columns)
		}
		for i := range reference.Columns {
			if observed.DeclaredColumns[i] != reference.Columns[i] {
				t.Fatalf("declared column %d: %q want %q", i, observed.DeclaredColumns[i], reference.Columns[i])
			}
		}
		if observed.BatchSize <= 0 {
			t.Fatalf("batch hint must be carried on the request: %d", observed.BatchSize)
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("PreviewCoveringIndexIsArangoOnly", func(t *testing.T) {
		resolved := exec02TestResolved()
		pgQuery, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
			resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, 5, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("postgresql compile: %v", err)
		}
		if pgQuery.PreviewCoveringIndex != nil {
			t.Fatalf("PostgreSQL command carries a preview covering index: %+v", pgQuery.PreviewCoveringIndex)
		}
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		if _, err := engine.PreviewOutput(context.Background(), resolved,
			PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("preview: %v", err)
		}
		for _, control := range spies.controlSnapshot() {
			if strings.Contains(strings.ToLower(control), "index") {
				t.Fatalf("PostgreSQL preview consulted the Arango index control %q", control)
			}
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("PreviewGroupScanAndProjectionAreArangoOnly", func(t *testing.T) {
		resolved := exec02TestResolved()
		pgQuery, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
			resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, 5, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("postgresql compile: %v", err)
		}
		if pgQuery.PreviewGroupScan != nil {
			t.Fatalf("PostgreSQL command carries a group-scan spec: %+v", pgQuery.PreviewGroupScan)
		}
		if pgQuery.PreviewGroupProjection != nil {
			t.Fatalf("PostgreSQL command carries a group-projection spec: %+v", pgQuery.PreviewGroupProjection)
		}
		spies := &dispatchSpies{}
		calls := 0
		stream := OutputStream{
			Name: "resources", Columns: []string{"id", "name"},
			command:               mustExec11PostgreSQLCommand(t),
			expectedBackend:       command.CompiledBackendPostgreSQL,
			commandColumns:        []string{"id", "name"},
			projectionDecisionKey: &groupPreviewRowsKey{},
			sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
				calls++
				return nil
			},
		}
		if err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("leaked-decision stream: %v", err)
		}
		if calls != 1 {
			t.Fatalf("port invocations: %d, want one", calls)
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("RootPageBindsAreArangoOnly", func(t *testing.T) {
		pgCalls := 0
		pgStream := OutputStream{
			Name: "resources", Columns: []string{"id", "name"},
			command:         mustExec11PostgreSQLCommand(t),
			expectedBackend: command.CompiledBackendPostgreSQL,
			commandColumns:  []string{"id", "name"},
			rootPageRows:    50,
			sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
				pgCalls++
				return nil
			},
		}
		if err := pgStream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("leaked-page PostgreSQL stream: %v", err)
		}
		if pgCalls != 1 {
			t.Fatalf("PostgreSQL leaked page ran %d port calls, want the single statement", pgCalls)
		}
		aqlCommand, err := command.NewAQLCommand("FOR d IN resource RETURN d", map[string]any{"project": "P1"})
		if err != nil {
			t.Fatalf("aql command: %v", err)
		}
		keyCommand, err := command.NewAQLCommand("FOR d IN resource RETURN d._key", map[string]any{
			"project":                     "P1",
			compiler.RootPageAfterKeyBind: "",
			compiler.RootPageSizeBind:     50,
		})
		if err != nil {
			t.Fatalf("aql key command: %v", err)
		}
		rowsCommand, err := command.NewAQLCommand("FOR d IN resource FILTER d._key IN @loom_root_page_keys RETURN d", map[string]any{
			"project":                 "P1",
			compiler.RootPageKeysBind: []string{},
		})
		if err != nil {
			t.Fatalf("aql rows command: %v", err)
		}
		page := &compiler.CompiledOutputPage{RootKeys: keyCommand, Rows: rowsCommand}
		aqlCalls := 0
		expectKeys, keysServed := true, false
		aqlStream := OutputStream{
			Name: "resources", Columns: []string{"id"},
			command:         aqlCommand,
			expectedBackend: command.CompiledBackendAQL,
			rootPageRows:    1,
			page:            page,
			sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				aqlCalls++
				if aqlCalls > 8 {
					return errors.New("root page loop did not terminate")
				}
				if expectKeys {
					expectKeys = false
					if keysServed {
						return nil
					}
					keysServed = true
					return visit(map[string]any{"_key": "k1"})
				}
				expectKeys = true
				return visit(map[string]any{"id": "patient-one"})
			},
		}
		if err := aqlStream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("AQL control page loop: %v", err)
		}
		if aqlCalls < 2 {
			t.Fatalf("AQL control must exercise the root-key page loop through the port: calls=%d", aqlCalls)
		}
	})

	t.Run("CellTracePostgreSQLRunArmServesEvidenceThroughTheSharedPort", func(t *testing.T) {
		resolved := exec02TestResolved()
		compiled, err := compiler.CompileCellTraceOutput(exec02PostgreSQLTarget(t),
			resolved.Compiled.Outputs[0], "name", 0, 2, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("registered arm B cell trace must compile on the PostgreSQL target: %v", err)
		}
		if _, args, portErr := compiled.Command.PostgreSQLParameters(); portErr != nil || len(args) == 0 {
			t.Fatalf("compiled cell trace command must be a parameterized PostgreSQL command: %v / %d args", portErr, len(args))
		}
		if compiled.ValueColumn != ir.PhysicalCellTraceValueField || compiled.StatusColumn != ir.PhysicalCellTraceStatusField ||
			compiled.ContributionsColumn != ir.PhysicalCellTraceContributionsField ||
			compiled.IdentityPartsColumn != ir.PhysicalCellTraceIdentityPartsField ||
			compiled.HasMoreColumn != ir.PhysicalCellTraceHasMoreField || compiled.OmissionColumn != ir.PhysicalCellTraceOmissionField {
			t.Fatalf("compiled trace must carry the six registered contract columns: %#v", compiled)
		}

		parts := []any{"patient-one"}
		encoded, _ := json.Marshal(parts)
		digest := sha256.Sum256(encoded)
		rowID := hex.EncodeToString(digest[:])
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{
			{ir.PhysicalCellTraceIdentityPartsField: []any{"patient-two"}, ir.PhysicalCellTraceValueField: "Grace",
				ir.PhysicalCellTraceStatusField: "VALUE", ir.PhysicalCellTraceHasMoreField: false,
				ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: []any{}},
			{ir.PhysicalCellTraceIdentityPartsField: parts, ir.PhysicalCellTraceValueField: "Ada",
				ir.PhysicalCellTraceStatusField: "VALUE", ir.PhysicalCellTraceHasMoreField: false,
				ir.PhysicalCellTraceOmissionField: "", ir.PhysicalCellTraceContributionsField: []any{
					map[string]any{"resourceType": "Patient", "resourceId": "patient-one", "operation": "EXTRACT", "value": "female"},
				}},
		})
		result, err := engine.CellTrace(context.Background(), resolved,
			CellTraceRequest{Output: "resources", RowID: rowID, Column: "name", Offset: 0, Limit: 1})
		if err != nil {
			t.Fatalf("CellTrace on the registered arm B shape: %v", err)
		}
		if spies.pgRequests != 1 || len(spies.pgFull) != 1 || len(spies.pgPreview) != 0 {
			t.Fatalf("cell trace must run exactly once on the full source row port: full=%d preview=%d", len(spies.pgFull), len(spies.pgPreview))
		}
		call := spies.pgFull[0]
		for _, want := range []string{`AS "__loom_trace_value"`, `AS "__loom_trace_identity_parts"`, `AS "__loom_trace_contributions"`} {
			if !strings.Contains(call.SQL, want) {
				t.Fatalf("port call must carry the compiled trace plan (%s):\n%s", want, call.SQL)
			}
		}
		if len(call.Args) == 0 {
			t.Fatal("every scope and page fact must travel as an ordered parameter")
		}
		if !result.Complete || result.Status != CellTraceValue || result.Value != "Ada" ||
			result.HasMore || result.NextOffset != 0 || len(result.Contributions) != 1 ||
			result.Contributions[0].ResourceID != "patient-one" || result.Contributions[0].Operation != "EXTRACT" {
			t.Fatalf("the first visited row must be the identity-matched published row: %#v", result)
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("CellTracePostgreSQLRefusesComposedConstructionByNameWithZeroPortCalls", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		resolved, err := engine.CompileResolvedBundle(context.Background(), exec08GroupBundle(), recipe.RuntimeBindings{
			Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"observations"}, AuthResourcePaths: []string{"FHIR/Observation"},
		})
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		_, err = engine.CellTrace(context.Background(), resolved,
			CellTraceRequest{Output: "observations", RowID: "x", Column: "status", Limit: 25})
		if err == nil || !strings.Contains(err.Error(), "composed construction cell trace is unsupported on the PostgreSQL compile target") {
			t.Fatalf("unregistered composed-construction trace must refuse by name: %v", err)
		}
		if !errors.Is(err, postgres.ErrUnsupportedPostgresPlan) {
			t.Fatalf("cell trace refusal must carry the typed unsupported-plan error: %v", err)
		}
		if spies.pgRequests != 0 {
			t.Fatalf("refused cell trace reached the row port")
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("CellTracePostgreSQLRefusesEndpointLookupTraversalByNameWithZeroPortCalls", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "pgsql24_endpoint_control",
			TranslationVersion:  "pg-sql-24",
			Outputs: []recipe.Output{{
				Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
				RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
				Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
				Traversals: []recipe.Traversal{{
					Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
					Fields: []recipe.Field{{Name: "condition_id", ColumnID: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: recipe.ValueModeFirst}},
				}},
			}},
		}, recipe.RuntimeBindings{
			Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"patients"}, AuthResourcePaths: []string{"FHIR/Patient", "FHIR/Condition"},
		})
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		_, err = engine.CellTrace(context.Background(), resolved,
			CellTraceRequest{Output: "patients", RowID: "x", Column: "condition_id", Limit: 25})
		if err == nil || !strings.Contains(err.Error(), "endpoint-lookup") {
			t.Fatalf("the default-policy traversal trace must refuse with the spine's typed endpoint-lookup voice: %v", err)
		}
		if !errors.Is(err, postgres.ErrUnsupportedPostgresPlan) {
			t.Fatalf("cell trace refusal must carry the typed unsupported-plan error: %v", err)
		}
		if spies.pgRequests != 0 {
			t.Fatalf("refused cell trace reached the row port")
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("RowLineageSurfaceRefusesPostgreSQLTargetByName", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		_, err := engine.RowLineage(context.Background(), exec02TestResolved(),
			RowLineageRequest{Output: "resources", RowID: "patient-one"})
		if err == nil || !strings.Contains(err.Error(), "row lineage compilation is unsupported on the PostgreSQL compile target") {
			t.Fatalf("row lineage must name its refusal under the PostgreSQL target: %v", err)
		}
		if !errors.Is(err, postgres.ErrUnsupportedPostgresPlan) {
			t.Fatalf("row lineage refusal must carry the typed unsupported-plan error: %v", err)
		}
		if spies.pgRequests != 0 {
			t.Fatalf("refused row lineage reached the row port")
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("RowLineagePostgreSQLRunArmServesBoundedColumnContractThroughTheSharedPort", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{{
			"found": true, "contributors": json.RawMessage(exec08PageOneA), "hasMore": false,
		}})
		resolved, err := engine.CompileResolvedBundle(context.Background(), exec08GroupBundle(), recipe.RuntimeBindings{
			Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"observations"}, AuthResourcePaths: []string{"FHIR/Observation"},
		})
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		result, err := engine.RowLineage(context.Background(), resolved, RowLineageRequest{
			Output: "observations",
			RowID:  `[["construction","exec08_group"],["operation","GROUP"],["grouped_status_id","final"]]`,
			Offset: 0, Limit: 25,
		})
		if err != nil {
			t.Fatalf("RowLineage: %v", err)
		}
		if spies.pgRequests != 1 || len(spies.pgFull) != 1 {
			t.Fatalf("PostgreSQL lineage port calls = %d (records %d), want exactly one", spies.pgRequests, len(spies.pgFull))
		}
		if want := "found,contributors,hasMore"; strings.Join(spies.pgFull[0].Columns, ",") != want {
			t.Fatalf("declared column contract = %v, want %s", spies.pgFull[0].Columns, want)
		}
		if len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "obs-a" || result.HasMore {
			t.Fatalf("typed contributor page = %#v", result)
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("PopulationMappingPostgreSQLRunArmServesWitnessContractThroughTheSharedPort", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, []map[string]any{
			{ir.PhysicalPopulationMappingMemberField: "obs-1", ir.PhysicalPopulationMappingIdentityPartsField: []any{"P1", "obs-1"}},
			{ir.PhysicalPopulationMappingMemberField: "obs-2", ir.PhysicalPopulationMappingIdentityPartsField: []any{"P1", "obs-2"}},
		})
		resolved, err := engine.CompileResolvedBundle(context.Background(), exec07FlatBundle("exec07-contract"), recipe.RuntimeBindings{
			Project: "P1", DatasetGeneration: "G1",
			SelectionProject:           "P1",
			SelectionMembersCollection: "loom_explorer_selection_members",
			AuthResourcePaths:          []string{"FHIR/Observation"},
		})
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		result, err := engine.PopulationMapping(context.Background(), resolved,
			PopulationMappingRequest{Output: "observations", MaxUnmapped: 10},
			exec07Members("obs-1", "obs-2", "obs-3"))
		if err != nil {
			t.Fatalf("PopulationMapping: %v", err)
		}
		if spies.pgRequests != 1 || len(spies.pgFull) != 1 {
			t.Fatalf("PostgreSQL mapping port calls = %d (records %d), want exactly one", spies.pgRequests, len(spies.pgFull))
		}
		if want := ir.PhysicalPopulationMappingMemberField + "," + ir.PhysicalPopulationMappingIdentityPartsField; strings.Join(spies.pgFull[0].Columns, ",") != want {
			t.Fatalf("declared witness contract = %v, want %s", spies.pgFull[0].Columns, want)
		}
		if result.Status != PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 2 {
			t.Fatalf("typed mapping result = %#v", result)
		}
		if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "obs-3" {
			t.Fatalf("unmapped member IDs = %#v", result.UnmappedMemberIDs)
		}
		exec02AssertUntouchedArango(t, spies)
	})

	t.Run("MisTaggedPostgreSQLCommandThroughAQLFakeFailsClosed", func(t *testing.T) {
		spies := &dispatchSpies{}
		stream := OutputStream{
			Name: "resources", Columns: []string{"id", "name"},
			command:         mustExec11PostgreSQLCommand(t),
			expectedBackend: command.CompiledBackendAQL,
			commandColumns:  []string{"id", "name"},
			sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				spies.mu.Lock()
				spies.pgRequests++
				spies.mu.Unlock()
				return nil
			},
		}
		err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
		if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("mis-tagged command must fail with the typed mismatch: %v", err)
		}
		if spies.pgRequests != 0 {
			t.Fatalf("mis-tagged command reached the port; zero-call probe failed")
		}
	})

	t.Run("ZeroTaggedCommandFailsClosedBeforePort", func(t *testing.T) {
		calls := 0
		stream := OutputStream{
			Name: "resources", Columns: []string{"id"},
			command:        command.CompiledCommand{},
			commandColumns: []string{"id"},
			sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
				calls++
				return nil
			},
		}
		err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
		if !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero tag must fail with the unregistered-backend sentinel: %v", err)
		}
		if calls != 0 {
			t.Fatalf("zero-tagged command reached the port; zero-call probe failed")
		}
	})

	t.Run("EmptyDeclaredColumnsRefusedBeforePort", func(t *testing.T) {
		calls := 0
		stream := OutputStream{
			Name: "resources", Columns: []string{"id", "name"},
			command:         mustExec11PostgreSQLCommand(t),
			expectedBackend: command.CompiledBackendPostgreSQL,
			sourceRows: func(context.Context, RowsRequest, RowVisitor) error {
				calls++
				return nil
			},
		}
		err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "non-empty declared result column contract") {
			t.Fatalf("PostgreSQL stream without a declared contract must fail closed: %v", err)
		}
		if calls != 0 {
			t.Fatalf("empty declared contract reached the port; zero-call probe failed")
		}
	})

	t.Run("PreviewRunsOnPreviewLaneWhenSet", func(t *testing.T) {
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		if _, err := engine.PreviewOutput(context.Background(), exec02TestResolved(),
			PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("preview: %v", err)
		}
		if len(spies.pgPreview) != 1 || len(spies.pgFull) != 0 {
			t.Fatalf("preview lane routing: full=%d preview=%d, want preview-only", len(spies.pgFull), len(spies.pgPreview))
		}
	})

	t.Run("PreviewFallsBackToSourceRowsWhenPreviewLaneUnset", func(t *testing.T) {
		spies := &dispatchSpies{}
		config := spies.arangoEngine()
		config.CompileTarget = exec02PostgreSQLTarget(t)
		config.SourceRows = exec02pgPort(spies, &spies.pgFull, exec02Rows())
		config.PreviewSourceRows = nil
		engine := config.newEngineForTest(t)
		if _, err := engine.PreviewOutput(context.Background(), exec02TestResolved(),
			PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("preview without a preview lane: %v", err)
		}
		if len(spies.pgFull) != 1 || len(spies.pgPreview) != 0 {
			t.Fatalf("preview fallback routing: full=%d preview=%d, want SourceRows only", len(spies.pgFull), len(spies.pgPreview))
		}
	})

	t.Run("PostgreSQLCommandNeverReachesTheArangoPreviewLane", func(t *testing.T) {
		spies := &dispatchSpies{}
		config := spies.arangoEngine()
		config.CompileTarget = exec02PostgreSQLTarget(t)
		config.SourceRows = exec02pgPort(spies, &spies.pgFull, exec02Rows())
		engine := config.newEngineForTest(t)
		_, err := engine.PreviewOutput(context.Background(), exec02TestResolved(),
			PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil })
		if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
			t.Fatalf("PostgreSQL command on the AQL preview lane must fail with the typed mismatch: %v", err)
		}
		if len(spies.aqlPreviewQueries) != 0 || len(spies.aqlQueries) != 0 {
			t.Fatalf("PostgreSQL command touched the AQL adapters: preview=%v full=%v", spies.aqlPreviewQueries, spies.aqlQueries)
		}
	})
	t.Run("PrivateCaptureRunsExactlyTheSelectedTargetCompiledCommand", func(t *testing.T) {
		fixture := newExec11Capture(t, exec11PortRows("Ada"))
		captureBindings := exec11RestrictedBindings()
		captureBindings.IncludeAuthResourcePath = true
		reference, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
			fixture.resolved.Compiled.Outputs[0], captureBindings, 0, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("reference capture compile: %v", err)
		}
		artifact, err := fixture.stream.MaterializeClickHouseArtifact(context.Background(), fixture.manager, "exec11-contract", fixture.stream.stageID, fixture.columns)
		if err != nil {
			t.Fatalf("materialize PostgreSQL capture: %v", err)
		}
		defer func() {
			if err := artifact.Release(context.Background()); err != nil {
				t.Fatalf("release: %v", err)
			}
		}()
		if len(fixture.spies.pgFull) != 1 {
			t.Fatalf("capture port calls = %d, want 1", len(fixture.spies.pgFull))
		}
		wantSQL, wantArgs, err := reference.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatalf("reference command: %v", err)
		}
		if fixture.spies.pgFull[0].SQL != wantSQL || len(fixture.spies.pgFull[0].Args) != len(wantArgs) {
			t.Fatalf("capture ran a command other than the selected target compiled")
		}
		if strings.Join(fixture.spies.pgFull[0].Columns, ",") != strings.Join(reference.Columns, ",") {
			t.Fatalf("capture port declared columns %v, want the compiled contract %v", fixture.spies.pgFull[0].Columns, reference.Columns)
		}
		if !strings.Contains(wantSQL, "auth_resource_path") {
			t.Fatalf("restricted capture reference must read the typed authorization column: %q", wantSQL)
		}
	})
}

func mustExec11PostgreSQLCommand(t *testing.T) command.CompiledCommand {
	t.Helper()
	resolved := exec02TestResolved()
	query, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
		resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("reference compile: %v", err)
	}
	return query.Command
}
