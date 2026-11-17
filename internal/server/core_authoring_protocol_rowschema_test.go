package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	c19Project    = "project-a"
	c19ExplorerID = "core-protocol-c19"
	c19OutputID   = "patients"

	c19ColumnPatient    = "c_patient"
	c19ColumnActive     = "c_active"
	c19ColumnBirthOrder = "c_birth_order"
	c19ColumnBirthDate  = "c_birth_date"

	c19LabelPatient     = "Patient ID"
	c19LabelActive      = "Patient active"
	c19LabelBirthOrder  = "Birth order"
	c19LabelBirthDate   = "Birth date"
	c19LabelPatientEdit = "Patient Identifier (saved edit)"

	c19RowIdentityPrefix = "c19-row-"
)

type c19ContractWant struct {
	columns     []string
	labels      []string
	logicalType []string
	shapes      []string
	nullable    []bool
}

func c19StoredHead(t *testing.T, fixture *coreProtocolFixture) explorer.Explorer {
	t.Helper()
	current, err := fixture.store.Get(context.Background(), c19Project, c19ExplorerID)
	if err != nil {
		t.Fatalf("read persisted head: %v", err)
	}
	return *current
}

func c19ExpectHead(t *testing.T, fixture *coreProtocolFixture, wantVersion int64, wantDigest, stage string) explorer.Explorer {
	t.Helper()
	stored := c19StoredHead(t, fixture)
	if stored.DraftVersion != wantVersion || stored.DraftDigest != wantDigest {
		t.Fatalf("%s changed the persisted head: got version=%d digest=%q want version=%d digest=%q", stage, stored.DraftVersion, stored.DraftDigest, wantVersion, wantDigest)
	}
	return stored
}

func c19ExpectRestoreHistory(t *testing.T, storeName, pointer, undoTarget string,
	savedVersion int64, savedDigest, savedLabel string,
	baselineVersion int64, baselineDigest, baselineLabel string,
	load func(string) (*explorer.DraftRevision, error),
) {
	t.Helper()
	if pointer == "" {
		t.Fatalf("%s: restored head exposes no history pointer", storeName)
	}
	preRestore, err := load(pointer)
	if err != nil {
		t.Fatalf("%s: restored head pointer %q names no draft revision: %v", storeName, pointer, err)
	}
	if preRestore.DraftVersion != savedVersion || preRestore.DraftDigest != savedDigest {
		t.Fatalf("%s: pointer revision=%d/%q want the pre-restore head %d/%q", storeName, preRestore.DraftVersion, preRestore.DraftDigest, savedVersion, savedDigest)
	}
	preRestoreWorkspace, err := authoringv2.DecodeWorkspace(preRestore.DraftConfig)
	if err != nil {
		t.Fatalf("%s: decode pointer revision bytes: %v", storeName, err)
	}
	if got := c19AuthoredLabel(preRestoreWorkspace, c19ColumnPatient); got != savedLabel {
		t.Fatalf("%s: pointer revision label=%q want the edited head %q", storeName, got, savedLabel)
	}
	restoredFrom, err := load(undoTarget)
	if err != nil {
		t.Fatalf("%s: undo target %q names no draft revision: %v", storeName, undoTarget, err)
	}
	if restoredFrom.DraftVersion != baselineVersion || restoredFrom.DraftDigest != baselineDigest {
		t.Fatalf("%s: undo revision=%d/%q want the restored-from head %d/%q", storeName, restoredFrom.DraftVersion, restoredFrom.DraftDigest, baselineVersion, baselineDigest)
	}
	restoredFromWorkspace, err := authoringv2.DecodeWorkspace(restoredFrom.DraftConfig)
	if err != nil {
		t.Fatalf("%s: decode undo revision bytes: %v", storeName, err)
	}
	if got := c19AuthoredLabel(restoredFromWorkspace, c19ColumnPatient); got != baselineLabel {
		t.Fatalf("%s: undo revision label=%q want the restored-from head %q", storeName, got, baselineLabel)
	}
	t.Logf("C19 (:69/:388) %s: restore head pointer names the pre-restore snapshot (version %d, the redo slot) while the consumed undo target names the restored-from revision (version %d); pointer and undo target are distinct store-minted ids, matching internal/explorer/lifecycle/draft_restore.go:33/47 and the store write path", storeName, savedVersion, baselineVersion)
}

func c19AuthoredLabel(workspace authoringv2.Workspace, column string) string {
	for _, document := range workspace.Documents {
		for _, candidate := range document.Columns {
			if candidate.Column == column {
				return candidate.Label
			}
		}
	}
	return ""
}

func c19WantBaselineContract() c19ContractWant {
	return c19ContractWant{
		columns:     []string{c19ColumnActive, c19ColumnBirthDate, c19ColumnBirthOrder, c19ColumnPatient},
		labels:      []string{c19LabelActive, c19LabelBirthDate, c19LabelBirthOrder, c19LabelPatient},
		logicalType: []string{"boolean", "date", "integer", "string"},
		shapes:      []string{"scalar", "scalar", "scalar", "scalar"},
		nullable:    []bool{true, true, true, true},
	}
}

func c19WantEditedContract() c19ContractWant {
	want := c19WantBaselineContract()
	want.labels = []string{c19LabelActive, c19LabelBirthDate, c19LabelBirthOrder, c19LabelPatientEdit}
	return want
}

func c19CapabilitySnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: c19Project, Generation: "generation-a",
			AuthorizationScopeDigest: explorerScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}),
			SchemaDigest:             strings.Repeat("a", 64),
			ResourceInventoryDigest:  "inventory", RelationshipDigest: "relationships",
			FieldDigest: "fields", ShapeDigest: strings.Repeat("b", 64),
			ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion,
			TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{
			{ID: "c19_c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "ID", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true},
			{ID: "c19_c_patient_active", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true},
			{ID: "c19_c_patient_birth_order", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "multipleBirthInteger", Label: "Birth order", LogicalType: "integer", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true},
			{ID: "c19_c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true},
		},
		nil,
	)
}

func c19Workspace(t *testing.T, snapshot capability.Snapshot) authoringv2.Workspace {
	t.Helper()
	raw := c19AuthoredWorkspace()
	prepared, err := authoringv2.PrepareWorkspaceForCompilation(raw, authoringV2Catalog(snapshot, c19ExplorerID))
	if err != nil {
		t.Fatalf("prepare seed workspace: %v", err)
	}
	return prepared
}

func c19AuthoredWorkspace() authoringv2.Workspace {
	scalar := func(path string) *authoringv2.FieldSource {
		return &authoringv2.FieldSource{Path: path, ProjectionMode: "VALUE"}
	}
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(),
			Output:           authoringv2.Output{ID: c19OutputID, Title: "Patients", RowLabel: "Patients"},
			RootResourceType: "Patient",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []authoringv2.Column{
				{ColumnID: "id", Column: c19ColumnPatient, Label: c19LabelPatient, LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: scalar("id")}},
				{ColumnID: "active", Column: c19ColumnActive, Label: c19LabelActive, LogicalType: "boolean", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: scalar("active")}},
				{ColumnID: "birth_order", Column: c19ColumnBirthOrder, Label: c19LabelBirthOrder, LogicalType: "integer", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: scalar("multipleBirthInteger")}},
				{ColumnID: "birth_date", Column: c19ColumnBirthDate, Label: c19LabelBirthDate, LogicalType: "date", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: scalar("birthDate")}},
			},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: c19OutputID, Order: 0, Visible: true}},
	}
}

type c19RowStore struct {
	mu      sync.Mutex
	rows    []map[string]any
	batches []int
	calls   int
}

func (r *c19RowStore) set(rows []map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = rows
	r.batches = nil
	r.calls = 0
}

func (r *c19RowStore) observations() (int, []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, append([]int(nil), r.batches...)
}

func (r *c19RowStore) previewRows() dataframeexecution.SourceRows {
	return func(_ context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		_, _, deriveErr := request.Command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}

		r.mu.Lock()
		r.calls++
		r.batches = append(r.batches, request.BatchSize)
		pending := append([]map[string]any(nil), r.rows...)
		r.mu.Unlock()
		for _, row := range pending {
			if err := visit(cloneC19Row(row)); err != nil {
				return err
			}
		}
		return nil
	}
}

func cloneC19Row(row map[string]any) map[string]any {
	clone := make(map[string]any, len(row))
	for key, value := range row {
		clone[key] = value
	}
	return clone
}

func c19Row(identity string, values map[string]any) map[string]any {
	row := map[string]any{"__loom_row_id": c19RowIdentityPrefix + identity}
	for key, value := range values {
		row[key] = value
	}
	return row
}

func c19NewFixture(t *testing.T) (*coreProtocolFixture, *c19RowStore) {
	t.Helper()
	snapshot := c19CapabilitySnapshot()
	workspace := c19Workspace(t, snapshot)
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: c19Project, ExplorerID: c19ExplorerID, Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rows := &c19RowStore{}
	emptyRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		command := request.Command
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: emptyRows, PreviewSourceRows: rows.previewRows()})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &coreProtocolFixture{t: t, store: store, snapshot: snapshot, workspace: workspace, initialDigest: digest, basePath: "/api/v1/projects/" + c19Project + "/explorers/" + c19ExplorerID + "/authoring/v2"}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if resolveErr != nil {
				return dataframeexecution.PreviewSummary{}, resolveErr
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, serviceConfig)
	fixture.app = app
	return fixture, rows
}

func c19Preview(t *testing.T, fixture *coreProtocolFixture, receiptID string, limit int) testHTTPResponse {
	t.Helper()
	body := fmt.Sprintf(`{"receiptId":%q,"outputId":%q}`, receiptID, c19OutputID)
	if limit != 0 {
		body = fmt.Sprintf(`{"receiptId":%q,"outputId":%q,"limit":%d}`, receiptID, c19OutputID, limit)
	}
	return coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", body, "")
}

func c19DecodePreview(t *testing.T, response testHTTPResponse) loomapi.PreviewResponse {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.StatusCode, response.Body)
	}
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
		t.Fatalf("decode preview: %v body=%s", err, response.Body)
	}
	return preview
}

func c19ExpectContract(t *testing.T, preview loomapi.PreviewResponse, want c19ContractWant) {
	t.Helper()
	if len(preview.Columns) != len(want.columns) {
		t.Fatalf("published %d columns, want %d: %#v", len(preview.Columns), len(want.columns), preview.Columns)
	}
	for index, column := range preview.Columns {
		if column.Column != want.columns[index] || column.Label != want.labels[index] || column.LogicalType != want.logicalType[index] {
			t.Fatalf("column %d = column=%q label=%q logicalType=%q, want column=%q label=%q logicalType=%q",
				index, column.Column, column.Label, column.LogicalType, want.columns[index], want.labels[index], want.logicalType[index])
		}
		if column.Shape == nil || *column.Shape != want.shapes[index] {
			t.Fatalf("column %d %s shape=%v, want %q", index, column.Column, column.Shape, want.shapes[index])
		}
		if column.Nullable == nil || *column.Nullable != want.nullable[index] {
			t.Fatalf("column %d %s nullable=%v, want %v", index, column.Column, column.Nullable, want.nullable[index])
		}
	}
	if preview.OutputId != c19OutputID {
		t.Fatalf("preview outputId=%q want %q", preview.OutputId, c19OutputID)
	}
}

func c19ExpectRows(t *testing.T, preview loomapi.PreviewResponse, body string, want []map[string]any, wantRowCount int, wantSampled bool) {
	t.Helper()
	if preview.RowCount != wantRowCount {
		t.Fatalf("rowCount=%d want %d body=%s", preview.RowCount, wantRowCount, body)
	}
	if len(preview.Rows) != len(want) {
		t.Fatalf("published %d rows, want %d: %s", len(preview.Rows), len(want), body)
	}
	if preview.Sampled != wantSampled {
		t.Fatalf("sampled=%v want %v body=%s", preview.Sampled, wantSampled, body)
	}
	if len(preview.RowSources) != len(want) {
		t.Fatalf("rowSources has %d entries, want %d aligned with rows: %s", len(preview.RowSources), len(want), body)
	}
	for index := range want {
		if !reflect.DeepEqual(preview.Rows[index], want[index]) {
			t.Fatalf("row %d mismatch\nwant=%#v\ngot =%#v\nbody=%s", index, want[index], preview.Rows[index], body)
		}
	}
	if strings.Contains(body, `"rows":null`) {
		t.Fatalf("rows published as null instead of an array: %s", body)
	}
}

func c19TypedRow() map[string]any {
	return c19Row("typed", map[string]any{
		c19ColumnPatient:    "patient-alpha",
		c19ColumnActive:     true,
		c19ColumnBirthOrder: 2,
		c19ColumnBirthDate:  "2001-02-03",
	})
}

func c19TypedRowExpectation() map[string]any {
	return map[string]any{
		"__loom_row_id":     c19RowIdentityPrefix + "typed",
		c19ColumnPatient:    "patient-alpha",
		c19ColumnActive:     true,
		c19ColumnBirthOrder: float64(2),
		c19ColumnBirthDate:  "2001-02-03",
	}
}

func TestCoreAuthoringProtocolRowSchemaSemantics(t *testing.T) {

	t.Run("saved-edit-contract-and-typed-values", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		if hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
			t.Fatalf("baseline hydration identity mismatch: %+v", hydrated)
		}
		rows.set([]map[string]any{c19TypedRow()})

		saved, ack := fixture.saveLabel("c19-types-save", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		if ack.DraftVersion != 2 || ack.DraftDigest == "" || ack.DraftDigest == fixture.initialDigest {
			t.Fatalf("save did not acknowledge a new revision: %+v", ack)
		}

		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-types-reconcile"))
		response := c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview := c19DecodePreview(t, response)
		if preview.ReceiptId != compiled.ReceiptId {
			t.Fatalf("preview is not bound to the compiled receipt: %+v", preview)
		}
		c19ExpectContract(t, preview, c19WantEditedContract())
		c19ExpectRows(t, preview, response.Body, []map[string]any{c19TypedRowExpectation()}, 1, false)

		stored := c19StoredHead(t, fixture)
		if !strings.Contains(string(stored.DraftConfig), c19LabelPatientEdit) {
			t.Fatalf("persisted draft lacks the saved label: %s", stored.DraftConfig)
		}
		emitted := fixture.store.receipts[compiled.ReceiptId]
		if emitted.IntentDigest != ack.DraftDigest {
			t.Fatalf("receipt intentDigest=%q does not name the saved head digest=%q", emitted.IntentDigest, ack.DraftDigest)
		}

		for _, want := range []string{`"c_patient":"patient-alpha"`, `"c_active":true`, `"c_birth_order":2`, `"c_birth_date":"2001-02-03"`} {
			if !strings.Contains(response.Body, want) {
				t.Fatalf("preview body is missing the typed encoding %s: %s", want, response.Body)
			}
		}
		if strings.Contains(response.Body, `"c_birth_order":"2"`) || strings.Contains(response.Body, `"c_active":"true"`) {
			t.Fatalf("preview stringified a typed value: %s", response.Body)
		}
	})

	t.Run("saved-edit-null-handling", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		saved, ack := fixture.saveLabel("c19-nulls-save", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-nulls-reconcile"))

		rows.set([]map[string]any{
			c19Row("missing", map[string]any{
				c19ColumnPatient:    "patient-beta",
				c19ColumnActive:     nil,
				c19ColumnBirthOrder: 1,
			}),
			c19Row("explicit-null", map[string]any{
				c19ColumnPatient:    "patient-gamma",
				c19ColumnActive:     true,
				c19ColumnBirthOrder: 3,
				c19ColumnBirthDate:  nil,
			}),
			c19Row("empty-string", map[string]any{
				c19ColumnPatient:    "patient-delta",
				c19ColumnActive:     false,
				c19ColumnBirthOrder: 4,
				c19ColumnBirthDate:  "",
			}),
		})

		response := c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview := c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{
			{
				"__loom_row_id":     c19RowIdentityPrefix + "missing",
				c19ColumnPatient:    "patient-beta",
				c19ColumnActive:     nil,
				c19ColumnBirthOrder: float64(1),
			},
			{
				"__loom_row_id":     c19RowIdentityPrefix + "explicit-null",
				c19ColumnPatient:    "patient-gamma",
				c19ColumnActive:     true,
				c19ColumnBirthOrder: float64(3),
				c19ColumnBirthDate:  nil,
			},
			{
				"__loom_row_id":     c19RowIdentityPrefix + "empty-string",
				c19ColumnPatient:    "patient-delta",
				c19ColumnActive:     false,
				c19ColumnBirthOrder: float64(4),
				c19ColumnBirthDate:  "",
			},
		}, 3, false)

		if _, absent := preview.Rows[0][c19ColumnBirthDate]; absent {
			t.Fatalf("a row whose store value was absent published a %s member anyway: %s", c19ColumnBirthDate, response.Body)
		}
		if !strings.Contains(response.Body, `"c_active":null`) {
			t.Fatalf("an explicit null store value did not stay an explicit null: %s", response.Body)
		}
		if !strings.Contains(response.Body, `"c_birth_date":null`) {
			t.Fatalf("an explicit null date did not stay an explicit null: %s", response.Body)
		}
		if !strings.Contains(response.Body, `"c_birth_date":""`) {
			t.Fatalf("an empty-string date was not preserved as an empty string: %s", response.Body)
		}

		t.Log("C19 (:69/:388) observed-only: the server publishes store values without validating them against the published logicalType/nullable; fidelity is asserted, enforcement is not claimed")

		rows.set([]map[string]any{{"__loom_row_id": c19RowIdentityPrefix + "identity-only"}})
		identityOnly := c19Preview(t, fixture, compiled.ReceiptId, 10)
		identityOnlyPreview := c19DecodePreview(t, identityOnly)
		if len(identityOnlyPreview.Rows) != 1 || len(identityOnlyPreview.Rows[0]) != 1 {
			t.Fatalf("a key-less store row should publish only its identity, got %#v body=%s", identityOnlyPreview.Rows, identityOnly.Body)
		}
		if identityOnlyPreview.RowCount != 1 || !strings.Contains(identityOnly.Body, `"rows":[{`) {
			t.Fatalf("key-less row was not published as an object: rowCount=%d body=%s", identityOnlyPreview.RowCount, identityOnly.Body)
		}
	})

	t.Run("saved-edit-duplicate-rows", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		saved, ack := fixture.saveLabel("c19-duplicates-save", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-duplicates-reconcile"))

		typed := c19TypedRow()
		rows.set([]map[string]any{typed, cloneC19Row(typed)})
		response := c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview := c19DecodePreview(t, response)
		wantRow := c19TypedRowExpectation()
		c19ExpectRows(t, preview, response.Body, []map[string]any{wantRow, wantRow}, 2, false)
		if preview.Rows[0]["__loom_row_id"] != preview.Rows[1]["__loom_row_id"] {
			t.Fatalf("identical store rows were given different public identities: %#v", preview.Rows)
		}
		t.Log("C19 (:69/:388) observed-only: duplicate rows and duplicate row identities are preserved as delivered; the preview path has no de-duplication step to assert")
	})

	t.Run("saved-edit-cardinality-and-limits", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		saved, ack := fixture.saveLabel("c19-cardinality-save", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-cardinality-reconcile"))

		five := []map[string]any{
			c19Row("one", map[string]any{c19ColumnPatient: "patient-one", c19ColumnActive: true, c19ColumnBirthOrder: 1, c19ColumnBirthDate: "2001-01-01"}),
			c19Row("two", map[string]any{c19ColumnPatient: "patient-two", c19ColumnActive: false, c19ColumnBirthOrder: 2, c19ColumnBirthDate: "2002-02-02"}),
			c19Row("three", map[string]any{c19ColumnPatient: "patient-three", c19ColumnActive: true, c19ColumnBirthOrder: 3, c19ColumnBirthDate: "2003-03-03"}),
		}

		rows.set(five)
		response := c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview := c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{
			{"__loom_row_id": c19RowIdentityPrefix + "one", c19ColumnPatient: "patient-one", c19ColumnActive: true, c19ColumnBirthOrder: float64(1), c19ColumnBirthDate: "2001-01-01"},
			{"__loom_row_id": c19RowIdentityPrefix + "two", c19ColumnPatient: "patient-two", c19ColumnActive: false, c19ColumnBirthOrder: float64(2), c19ColumnBirthDate: "2002-02-02"},
			{"__loom_row_id": c19RowIdentityPrefix + "three", c19ColumnPatient: "patient-three", c19ColumnActive: true, c19ColumnBirthOrder: float64(3), c19ColumnBirthDate: "2003-03-03"},
		}, 3, false)

		response = c19Preview(t, fixture, compiled.ReceiptId, 2)
		preview = c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{
			{"__loom_row_id": c19RowIdentityPrefix + "one", c19ColumnPatient: "patient-one", c19ColumnActive: true, c19ColumnBirthOrder: float64(1), c19ColumnBirthDate: "2001-01-01"},
			{"__loom_row_id": c19RowIdentityPrefix + "two", c19ColumnPatient: "patient-two", c19ColumnActive: false, c19ColumnBirthOrder: float64(2), c19ColumnBirthDate: "2002-02-02"},
		}, 2, true)
		if strings.Contains(response.Body, "patient-three") {
			t.Fatalf("rows beyond the requested limit were published: %s", response.Body)
		}
		calls, batches := rows.observations()
		t.Logf("C19 (:69/:388) cardinality: limit=2 stopped after %d preview reads with backend batch hints %v; the engine, not this fixture, enforced it", calls, batches)

		rows.set(nil)
		response = c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview = c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{}, 0, false)
		if !strings.Contains(response.Body, `"rows":[]`) {
			t.Fatalf("an empty result must publish an empty array: %s", response.Body)
		}

		twentySix := make([]map[string]any, 0, 26)
		for index := 0; index < 26; index++ {
			twentySix = append(twentySix, c19Row(fmt.Sprintf("default-%02d", index), map[string]any{
				c19ColumnPatient: fmt.Sprintf("patient-%02d", index), c19ColumnActive: index%2 == 0, c19ColumnBirthOrder: index, c19ColumnBirthDate: "2010-01-01",
			}))
		}
		rows.set(twentySix)
		response = c19Preview(t, fixture, compiled.ReceiptId, 0)
		preview = c19DecodePreview(t, response)
		if preview.RowCount != 25 || len(preview.Rows) != 25 {
			t.Fatalf("omitted limit published rowCount=%d rows=%d, want the documented default 25", preview.RowCount, len(preview.Rows))
		}
		if !preview.Sampled {
			t.Fatalf("a preview that stopped at the default limit must report sampled=true: %s", response.Body)
		}
		if first, last := preview.Rows[0][c19ColumnPatient], preview.Rows[24][c19ColumnPatient]; first != "patient-00" || last != "patient-24" {
			t.Fatalf("default-limit preview lost deterministic ordering, endpoints=%v %v want patient-00 and patient-24", first, last)
		}
		if strings.Contains(response.Body, "patient-25") {
			t.Fatalf("the row past the documented default limit was published: %s", response.Body)
		}

		rows.set(five)
		over := c19Preview(t, fixture, compiled.ReceiptId, dataframeexecution.MaxPreviewLimit+1)
		coreProtocolBoundaryFailure(t, over, "preview above the maximum limit", http.StatusUnprocessableEntity, "INVALID_PREVIEW_LIMIT", "preview", "limit must be between 1 and 1000")

		consistent := c19Preview(t, fixture, compiled.ReceiptId, 10)
		consistentPreview := c19DecodePreview(t, consistent)
		if consistentPreview.RowCount != len(consistentPreview.Rows) {
			t.Fatalf("rowCount disagrees with the delivered rows: %d vs %d body=%s", consistentPreview.RowCount, len(consistentPreview.Rows), consistent.Body)
		}
		var wireEnvelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(consistent.Body), &wireEnvelope); err != nil {
			t.Fatal(err)
		}
		if _, carried := wireEnvelope["rowCount"]; !carried {
			t.Fatalf("the public response stopped carrying rowCount: %s", consistent.Body)
		}
		t.Log("C19 (:69/:388) observed-only: rowCount is derived from the same visitor count that produces rows, so the plan's 'rowCount vs actual rows' comparison has a single source; no independent authoritative count exists to compare against and the request carries no rowCount field to tamper with")

		for _, limitCase := range []struct {
			name  string
			value int
		}{{"explicit zero", 0}, {"negative", -1}} {
			body := fmt.Sprintf(`{"receiptId":%q,"outputId":%q,"limit":%d}`, compiled.ReceiptId, c19OutputID, limitCase.value)
			response := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", body, "")
			if response.StatusCode == http.StatusOK {
				t.Fatalf("an out-of-range limit (%s) published rows: status=%d body=%s", limitCase.name, response.StatusCode, response.Body)
			}
			if strings.Contains(response.Body, `"rows"`) || strings.Contains(response.Body, `"rowCount"`) {
				t.Fatalf("an out-of-range limit (%s) published row state: %s", limitCase.name, response.Body)
			}
			var envelope struct {
				Error struct {
					Code       string `json:"code"`
					Diagnostic struct {
						Code  string `json:"code"`
						Stage string `json:"stage"`
					} `json:"diagnostic"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
				t.Fatalf("out-of-range limit (%s) body is not an error envelope: %v body=%s", limitCase.name, err, response.Body)
			}
			if envelope.Error.Diagnostic.Stage != "preview" {
				t.Fatalf("out-of-range limit (%s) failure left the preview stage: %s", limitCase.name, response.Body)
			}
			t.Logf("C19 (:69/:388) recorded finding: limit=%s (%s) reached the engine instead of route validation (status=%d code=%q); the OpenAPI-declared minimum is not enforced by the strict adapter, so an out-of-range limit surfaces as a preview failure rather than a 4xx input rejection", fmt.Sprint(limitCase.value), limitCase.name, response.StatusCode, envelope.Error.Diagnostic.Code)
		}
	})

	t.Run("restore-rows-and-schema-follow-the-restored-head", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		if hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
			t.Fatalf("baseline hydration identity mismatch: %+v", hydrated)
		}

		typed := c19TypedRow()
		duplicate := cloneC19Row(typed)
		nullRow := c19Row("null-date", map[string]any{c19ColumnPatient: "patient-gamma", c19ColumnActive: true, c19ColumnBirthOrder: 3, c19ColumnBirthDate: nil})
		emptyRow := c19Row("empty-date", map[string]any{c19ColumnPatient: "patient-delta", c19ColumnActive: false, c19ColumnBirthOrder: 4, c19ColumnBirthDate: ""})
		rows.set([]map[string]any{typed, duplicate, nullRow, emptyRow})
		wantFullRows := []map[string]any{
			c19TypedRowExpectation(),
			c19TypedRowExpectation(),
			{"__loom_row_id": c19RowIdentityPrefix + "null-date", c19ColumnPatient: "patient-gamma", c19ColumnActive: true, c19ColumnBirthOrder: float64(3), c19ColumnBirthDate: nil},
			{"__loom_row_id": c19RowIdentityPrefix + "empty-date", c19ColumnPatient: "patient-delta", c19ColumnActive: false, c19ColumnBirthOrder: float64(4), c19ColumnBirthDate: ""},
		}

		saved, ack := fixture.saveLabel("c19-restore-edit", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		edited := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-restore-reconcile-edited"))
		editedResponse := c19Preview(t, fixture, edited.ReceiptId, 10)
		editedPreview := c19DecodePreview(t, editedResponse)
		c19ExpectContract(t, editedPreview, c19WantEditedContract())
		c19ExpectRows(t, editedPreview, editedResponse.Body, wantFullRows, 4, false)

		_, headAfterEdit := fixture.getBuilder()
		restoredVersion, restoredDigest, undoRevision := fixture.MustRestoreRevision("c19-restore-1", headAfterEdit)
		if restoredVersion != ack.DraftVersion+1 {
			t.Fatalf("restore advanced the head by != 1: got %d want %d", restoredVersion, ack.DraftVersion+1)
		}
		stored := c19ExpectHead(t, fixture, restoredVersion, restoredDigest, "restore of "+undoRevision)
		if strings.Contains(string(stored.DraftConfig), c19LabelPatientEdit) {
			t.Fatalf("the restored head still carries the edited label: %s", stored.DraftConfig)
		}
		if !strings.Contains(string(stored.DraftConfig), c19LabelPatient) {
			t.Fatalf("the restored head lost the original label: %s", stored.DraftConfig)
		}

		c19ExpectRestoreHistory(t, "map store", stored.PreviousDraftRevisionID, undoRevision,
			ack.DraftVersion, ack.DraftDigest, c19LabelPatientEdit,
			hydrated.DraftVersion, hydrated.DraftDigest, c19LabelPatient,
			func(revisionID string) (*explorer.DraftRevision, error) {
				return fixture.store.GetDraftRevision(context.Background(), c19Project, c19ExplorerID, revisionID)
			})

		restoredReceipt := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "c19-restore-reconcile-restored"))
		if restoredReceipt.ReceiptId == edited.ReceiptId {
			t.Fatal("the restored head reused the edited head's receipt identity")
		}
		if restoredDigest != hydrated.DraftDigest {
			t.Fatalf("restored head CAS digest=%q must republish the restored-from revision digest %q", restoredDigest, hydrated.DraftDigest)
		}
		if restoredReceipt.IntentDigest != restoredDigest {
			t.Fatalf("restored receipt intentDigest=%q want the CAS head digest %q (heads enter compile already prepared, so the two define the same value)", restoredReceipt.IntentDigest, restoredDigest)
		}
		persistedRestored, ok := fixture.store.receipts[restoredReceipt.ReceiptId]
		if !ok || persistedRestored.IntentDigest != restoredReceipt.IntentDigest {
			t.Fatalf("map-store copy of the restored receipt diverges from the response: %+v", persistedRestored)
		}
		restoredResponse := c19Preview(t, fixture, restoredReceipt.ReceiptId, 10)
		restoredPreview := c19DecodePreview(t, restoredResponse)
		if restoredPreview.ReceiptId != restoredReceipt.ReceiptId {
			t.Fatalf("restored preview is not bound to the restored receipt: %+v", restoredPreview)
		}
		c19ExpectContract(t, restoredPreview, c19WantBaselineContract())
		c19ExpectRows(t, restoredPreview, restoredResponse.Body, wantFullRows, 4, false)

		limitedResponse := c19Preview(t, fixture, restoredReceipt.ReceiptId, 2)
		limited := c19DecodePreview(t, limitedResponse)
		c19ExpectRows(t, limited, limitedResponse.Body, wantFullRows[:2], 2, true)
		c19ExpectContract(t, limited, c19WantBaselineContract())

		if _, present := restoredPreview.Rows[0][c19ColumnBirthDate]; !present {
			t.Fatalf("the typed row lost its date after restore: %#v", restoredPreview.Rows[0])
		}
		if value, present := limited.Rows[0][c19ColumnActive]; !present || value != true {
			t.Fatalf("boolean fidelity changed after restore: %#v", limited.Rows[0])
		}
		if !reflect.DeepEqual(restoredPreview.Rows[0], restoredPreview.Rows[1]) {
			t.Fatalf("the duplicate pair stopped being identical after restore: %#v", restoredPreview.Rows[:2])
		}

		supersededResponse := c19Preview(t, fixture, edited.ReceiptId, 10)
		superseded := c19DecodePreview(t, supersededResponse)
		c19ExpectContract(t, superseded, c19WantEditedContract())
		c19ExpectRows(t, superseded, supersededResponse.Body, wantFullRows, 4, false)

		again := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "c19-restore-reconcile-again"))
		if again.ReceiptId != restoredReceipt.ReceiptId {
			t.Fatalf("recompiling the identical restored head changed the receipt identity: %s vs %s", again.ReceiptId, restoredReceipt.ReceiptId)
		}
	})

	t.Run("persisted-state-against-arango", func(t *testing.T) {
		endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
		if endpoint == "" {
			t.Skip("LOOM_TEST_ARANGO_URL is unset: the persisted-head/schema-semantics variant is skipped, not simulated. The map-store cases above are the executed evidence.")
		}
		c19AgainstArango(t, endpoint)
	})
}

func c19AgainstArango(t *testing.T, endpoint string) {
	t.Helper()
	database := "loom_core_rowschema_c19_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+database, nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("remove owned C19 database status=%d", response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, arangofactory.ExplorerBootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	store, err := arangofactory.NewExplorerStore(client)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := c19CapabilitySnapshot()
	workspace := c19Workspace(t, snapshot)
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, explorer.Explorer{
		Project: c19Project, ExplorerID: c19ExplorerID, Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rows := &c19RowStore{}
	emptyRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		command := request.Command
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: emptyRows, PreviewSourceRows: rows.previewRows()})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &coreProtocolFixture{t: t, snapshot: snapshot, workspace: workspace, initialDigest: digest, basePath: "/api/v1/projects/" + c19Project + "/explorers/" + c19ExplorerID + "/authoring/v2"}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if resolveErr != nil {
				return dataframeexecution.PreviewSummary{}, resolveErr
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, serviceConfig)
	fixture.app = app

	typed := c19TypedRow()
	rows.set([]map[string]any{typed, cloneC19Row(typed), c19Row("null-date", map[string]any{c19ColumnPatient: "patient-gamma", c19ColumnActive: true, c19ColumnBirthOrder: 3, c19ColumnBirthDate: nil})})
	wantRows := []map[string]any{
		c19TypedRowExpectation(),
		c19TypedRowExpectation(),
		{"__loom_row_id": c19RowIdentityPrefix + "null-date", c19ColumnPatient: "patient-gamma", c19ColumnActive: true, c19ColumnBirthOrder: float64(3), c19ColumnBirthDate: nil},
	}

	_, hydrated := fixture.getBuilder()
	saved, ack := fixture.saveLabel("c19-arango-edit", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("commands status=%d body=%s", saved.StatusCode, saved.Body)
	}

	persisted, err := store.Get(ctx, c19Project, c19ExplorerID)
	if err != nil {
		t.Fatalf("read persisted head from Arango: %v", err)
	}
	if persisted.DraftVersion != 2 || persisted.DraftDigest != ack.DraftDigest {
		t.Fatalf("persisted Arango head disagrees with the acknowledgement: version=%d digest=%q ack=%+v", persisted.DraftVersion, persisted.DraftDigest, ack)
	}
	persistedWorkspace, err := authoringv2.DecodeWorkspace(persisted.DraftConfig)
	if err != nil {
		t.Fatalf("decode persisted Arango draft: %v raw=%s", err, persisted.DraftConfig)
	}
	if got := c19AuthoredLabel(persistedWorkspace, c19ColumnPatient); got != c19LabelPatientEdit {
		t.Fatalf("persisted Arango head label=%q want %q", got, c19LabelPatientEdit)
	}

	edited := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-arango-reconcile-edited"))
	persistedReceipt, err := store.GetCompilationReceiptForExplorer(ctx, c19Project, c19ExplorerID, edited.ReceiptId)
	if err != nil {
		t.Fatalf("receipt %s not persisted in Arango: %v", edited.ReceiptId, err)
	}
	if persistedReceipt.IntentDigest != ack.DraftDigest {
		t.Fatalf("persisted receipt intentDigest=%q does not name the saved head digest=%q", persistedReceipt.IntentDigest, ack.DraftDigest)
	}
	editedResponse := c19Preview(t, fixture, edited.ReceiptId, 10)
	editedPreview := c19DecodePreview(t, editedResponse)
	c19ExpectContract(t, editedPreview, c19WantEditedContract())
	c19ExpectRows(t, editedPreview, editedResponse.Body, wantRows, 3, false)

	_, headAfterEdit := fixture.getBuilder()
	restoredVersion, restoredDigest, undoRevision := fixture.MustRestoreRevision("c19-arango-restore", headAfterEdit)
	restoredReceipt := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "c19-arango-reconcile-restored"))
	if restoredDigest != hydrated.DraftDigest {
		t.Fatalf("restored head CAS digest=%q must republish the restored-from revision digest %q", restoredDigest, hydrated.DraftDigest)
	}
	if restoredReceipt.IntentDigest != restoredDigest {
		t.Fatalf("restored receipt intentDigest=%q want the CAS head digest %q (heads enter compile already prepared, so the two define the same value)", restoredReceipt.IntentDigest, restoredDigest)
	}
	restoredResponse := c19Preview(t, fixture, restoredReceipt.ReceiptId, 10)
	restoredPreview := c19DecodePreview(t, restoredResponse)
	c19ExpectContract(t, restoredPreview, c19WantBaselineContract())
	c19ExpectRows(t, restoredPreview, restoredResponse.Body, wantRows, 3, false)

	restartBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	restartClient := restartBackend.Client
	t.Cleanup(func() { _ = restartClient.Close(context.Background()) })
	restartStore, err := arangofactory.NewExplorerStore(restartClient)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := restartStore.Get(ctx, c19Project, c19ExplorerID)
	if err != nil {
		t.Fatalf("fresh repository lost the restored head: %v", err)
	}
	if restarted.DraftVersion != restoredVersion || restarted.DraftDigest != restoredDigest {
		t.Fatalf("fresh repository head=%d/%q want %d/%q", restarted.DraftVersion, restarted.DraftDigest, restoredVersion, restoredDigest)
	}
	restartedWorkspace, err := authoringv2.DecodeWorkspace(restarted.DraftConfig)
	if err != nil {
		t.Fatalf("decode restored Arango draft: %v raw=%s", err, restarted.DraftConfig)
	}
	if got := c19AuthoredLabel(restartedWorkspace, c19ColumnPatient); got != c19LabelPatient {
		t.Fatalf("persisted Arango head after restore carries label=%q want %q", got, c19LabelPatient)
	}
	c19ExpectRestoreHistory(t, "arango restart", restarted.PreviousDraftRevisionID, undoRevision,
		ack.DraftVersion, ack.DraftDigest, c19LabelPatientEdit,
		hydrated.DraftVersion, hydrated.DraftDigest, c19LabelPatient,
		func(revisionID string) (*explorer.DraftRevision, error) {
			return restartStore.GetDraftRevision(ctx, c19Project, c19ExplorerID, revisionID)
		})
	if restarted.PreviousDraftRevisionID == undoRevision {
		t.Fatalf("the two history slots collapsed to one id: %q", restarted.PreviousDraftRevisionID)
	}
	liveHead, err := store.Get(ctx, c19Project, c19ExplorerID)
	if err != nil {
		t.Fatalf("reread head after restart: %v", err)
	}
	if liveHead.PreviousDraftRevisionID != restarted.PreviousDraftRevisionID {
		t.Fatalf("second repository disagrees on the restored head's history pointer: live=%q restart=%q", liveHead.PreviousDraftRevisionID, restarted.PreviousDraftRevisionID)
	}
	persistedRestored, err := store.GetCompilationReceiptForExplorer(ctx, c19Project, c19ExplorerID, restoredReceipt.ReceiptId)
	if err != nil {
		t.Fatalf("Arango lost the restored receipt: %v", err)
	}
	restartedReceipt, err := restartStore.GetCompilationReceiptForExplorer(ctx, c19Project, c19ExplorerID, restoredReceipt.ReceiptId)
	if err != nil {
		t.Fatalf("fresh repository lost the restored receipt: %v", err)
	}
	if persistedRestored.IntentDigest != restoredReceipt.IntentDigest || restartedReceipt.IntentDigest != restoredReceipt.IntentDigest {
		t.Fatalf("persisted restored receipt intent live=%q restart=%q response=%q", persistedRestored.IntentDigest, restartedReceipt.IntentDigest, restoredReceipt.IntentDigest)
	}
	t.Log("C19 (:69/:388) arango mode: draft head, draft history and receipts are real Arango bytes and were re-read through a second repository; the preview rows still come from this test's controlled execution seam (no dataset ingest), so dataset-resident value claims remain unproven here")
}
