package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

const (
	c1011Project      = "project-a"
	c1011ExplorerID   = "core-protocol"
	c1011PublicColumn = "c_patient"
	c1011OutputID     = "patients"
)

type coreProtocolReceiptsFixture struct {
	t             *testing.T
	app           *fiber.App
	store         *testExplorerStore
	capabilities  *testCapabilityStore
	snapshot      capability.Snapshot
	workspace     authoringv2.Workspace
	initialDigest string
	basePath      string
	headVersion   int64
	headDigest    string
}

func newCoreProtocolReceiptsFixture(t *testing.T) *coreProtocolReceiptsFixture {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer:  authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: c1011OutputID, Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{{ColumnID: "id", Column: c1011PublicColumn, Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}},
		Tabs:      []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: c1011OutputID, Order: 0, Visible: true}},
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := newTestExplorerStore()
	if _, err := store.Create(ctx, explorer.Explorer{
		Project: c1011Project, ExplorerID: c1011ExplorerID, Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	capabilities := newTestCapabilityStore()
	if _, err := capabilities.Put(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		command := request.Command
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: queryRows, PreviewSourceRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &coreProtocolReceiptsFixture{t: t, store: store, capabilities: capabilities, snapshot: snapshot, workspace: workspace, initialDigest: digest, basePath: "/api/v1/projects/" + c1011Project + "/explorers/" + c1011ExplorerID + "/authoring/v2", headVersion: 1, headDigest: digest}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(executionCtx context.Context, project, token string) (lifecycle.AuthorizedCapability, error) {
				stored, err := capabilities.GetByToken(executionCtx, token)
				if err != nil {
					return lifecycle.AuthorizedCapability{}, err
				}
				if stored.Identity.Project != project {
					return lifecycle.AuthorizedCapability{}, capability.ErrNotFound
				}
				return lifecycle.AuthorizedCapability{Snapshot: *stored, Scope: readScope}, nil
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
	return fixture
}

func (f *coreProtocolReceiptsFixture) saveLabel(commandID, label string) (testHTTPResponse, loomapi.ApplyCommandsResponse) {
	f.t.Helper()
	edited := f.workspace
	edited.Documents[0].Columns[0].Label = label
	columnValue, err := json.Marshal(edited.Documents[0].Columns[0])
	if err != nil {
		f.t.Fatal(err)
	}
	body := fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"UPDATE_COLUMN","outputId":%q,"column":%q,"columnValue":%s}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, f.snapshot.Token, f.headVersion, f.headDigest, c1011OutputID, c1011PublicColumn, columnValue)
	response := coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/commands", body, "builder-command-"+commandID)
	var ack loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &ack); err != nil {
		f.t.Fatalf("decode commands status=%d body=%s: %v", response.StatusCode, response.Body, err)
	}
	if response.StatusCode == http.StatusOK {
		f.headVersion = ack.DraftVersion
		f.headDigest = ack.DraftDigest
	}
	return response, ack
}

func (f *coreProtocolReceiptsFixture) compileAtHead(requestID string) (loomapi.CompileResponse, explorer.CompilationReceipt) {
	f.t.Helper()
	body := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q}`, f.snapshot.Token, f.headVersion, f.headDigest)
	response := coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/reconcile", body, requestID)
	if response.StatusCode != http.StatusOK {
		f.t.Fatalf("reconcile status=%d body=%s", response.StatusCode, response.Body)
	}
	var compiled loomapi.CompileResponse
	if err := json.Unmarshal([]byte(response.Body), &compiled); err != nil {
		f.t.Fatalf("decode compile: %v body=%s", err, response.Body)
	}
	if compiled.ReceiptId == "" || len(compiled.Outputs) != 1 || compiled.Outputs[0].OutputId != c1011OutputID {
		f.t.Fatalf("compile response outputs=%+v receipt=%q", compiled.Outputs, compiled.ReceiptId)
	}
	receipt, ok := f.store.receipts[compiled.ReceiptId]
	if !ok {
		f.t.Fatalf("receipt %s was not persisted", compiled.ReceiptId)
	}
	return compiled, receipt
}

func (f *coreProtocolReceiptsFixture) compileFresh(commandID, label string) (loomapi.CompileResponse, explorer.CompilationReceipt) {
	f.t.Helper()
	saved, ack := f.saveLabel(commandID, label)
	if saved.StatusCode != http.StatusOK {
		f.t.Fatalf("commands status=%d body=%s", saved.StatusCode, saved.Body)
	}
	if ack.DraftVersion != f.headVersion {
		f.t.Fatalf("commands acknowledgement head=%d want %d", ack.DraftVersion, f.headVersion)
	}
	return f.compileAtHead(commandID + "-compile")
}

func (f *coreProtocolReceiptsFixture) preview(receiptID string) testHTTPResponse {
	f.t.Helper()
	return f.previewOutput(receiptID, c1011OutputID)
}

func (f *coreProtocolReceiptsFixture) previewOutput(receiptID, outputID string) testHTTPResponse {
	f.t.Helper()
	body := fmt.Sprintf(`{"receiptId":%q,"outputId":%q,"limit":10}`, receiptID, outputID)
	return coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/preview", body, "")
}

func (f *coreProtocolReceiptsFixture) receiptEmittedLabel(t *testing.T, receiptID, publicColumn string) string {
	t.Helper()
	receipt, ok := f.store.receipts[receiptID]
	if !ok {
		t.Fatalf("receipt %s was not persisted", receiptID)
	}
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == c1011OutputID && column.PublicColumn == publicColumn {
			return column.Label
		}
	}
	t.Fatalf("receipt %s lacks emitted column %s: %+v", receiptID, publicColumn, receipt.EmittedColumns)
	return ""
}

func (f *coreProtocolReceiptsFixture) insertReceipt(receipt explorer.CompilationReceipt) explorer.CompilationReceipt {
	f.t.Helper()
	stored, err := f.store.InsertCompilationReceipt(context.Background(), receipt)
	if err != nil {
		f.t.Fatalf("insert receipt: %v", err)
	}
	return *stored
}

func (s *testCapabilityStore) deleteAll() {
	s.byToken = make(map[string]capability.Snapshot)
	s.byIdentity = make(map[capability.SnapshotIdentity]string)
}

func (f *coreProtocolReceiptsFixture) revokeSnapshot() {
	f.t.Helper()
	f.capabilities.deleteAll()
}

func (f *coreProtocolReceiptsFixture) retainSnapshot() {
	f.t.Helper()
	if _, err := f.capabilities.Put(context.Background(), f.snapshot); err != nil {
		f.t.Fatalf("re-retain snapshot: %v", err)
	}
}

func previewResponseColumns(t *testing.T, response testHTTPResponse) []string {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.StatusCode, response.Body)
	}
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
		t.Fatalf("decode preview: %v body=%s", err, response.Body)
	}
	if preview.OutputId != c1011OutputID || preview.Rows == nil {
		t.Fatalf("preview identity not bound to receipt: %+v", preview)
	}
	if !strings.Contains(response.Body, `"rows":[]`) {
		t.Fatalf("controlled execution must emit an empty array, not null: %s", response.Body)
	}
	labels := make([]string, 0, len(preview.Columns))
	for _, column := range preview.Columns {
		labels = append(labels, column.Label)
	}
	return labels
}

func TestCoreAuthoringProtocolReceiptBindingBoundaries(t *testing.T) {
	baseline := newCoreProtocolReceiptsFixture(t)
	_, receipt := baseline.compileFresh("c10-baseline-save", "Baseline Patient")
	if receipt.Project != c1011Project || receipt.ExplorerID != c1011ExplorerID || receipt.SnapshotToken != baseline.snapshot.Token {
		t.Fatalf("genuine receipt binding identity mismatch: %+v", receipt)
	}
	if label := baseline.receiptEmittedLabel(t, receipt.ID, c1011PublicColumn); label != "Baseline Patient" {
		t.Fatalf("genuine frozen contract label=%q want Baseline Patient", label)
	}
	if labels := previewResponseColumns(t, baseline.preview(receipt.ID)); len(labels) != 1 || labels[0] != "Baseline Patient" {
		t.Fatalf("baseline preview labels=%v want [Baseline Patient]", labels)
	}

	t.Run("equivalent-identities-stay-accepted", func(t *testing.T) {
		fixture := newCoreProtocolReceiptsFixture(t)
		_, first := fixture.compileFresh("c10-equivalence-save", "Equivalent Patient")
		second, secondReceipt := fixture.compileAtHead("c10-equivalence-recompile")
		if second.ReceiptId != first.ID {
			t.Fatalf("same head produced a different receipt identity: first=%s second=%s", first.ID, second.ReceiptId)
		}
		if secondReceipt.CompilationKey != first.CompilationKey {
			t.Fatalf("repeated compile changed the compilation key: first=%s second=%s", first.CompilationKey, secondReceipt.CompilationKey)
		}
		stored := fixture.insertReceipt(first)
		if stored.ID != first.ID {
			t.Fatalf("equivalent receipt insert did not reuse the existing identity: got %s want %s", stored.ID, first.ID)
		}
		labels := previewResponseColumns(t, fixture.preview(first.ID))
		if len(labels) != 1 || labels[0] != "Equivalent Patient" {
			t.Fatalf("normalised-equivalent preview labels=%v want [Equivalent Patient]", labels)
		}
	})

	t.Run("foreign-project", func(t *testing.T) {
		fixture := newCoreProtocolReceiptsFixture(t)
		_, genuine := fixture.compileFresh("c10-project-save", "Project Binding")
		receipt := genuine
		receipt.Project = "project-b"
		recomputeReceiptIdentity(t, &receipt, true)
		if err := receipt.Validate(); err != nil {
			t.Fatalf("forged foreign-project receipt is not self-consistent, so its rejection would not prove the project binding: %v", err)
		}
		stored := fixture.insertReceipt(receipt)
		if present, err := fixture.store.GetCompilationReceipt(context.Background(), stored.ID); err != nil || present == nil || present.Project != "project-b" {
			t.Fatalf("receipt under test should exist with the foreign project: present=%+v err=%v", present, err)
		}
		coreProtocolBoundaryFailure(t, fixture.preview(stored.ID), "foreign project", http.StatusNotFound, "COMPILE_RECEIPT_NOT_FOUND", "receipt", "not found")
	})

	t.Run("foreign-explorer", func(t *testing.T) {
		fixture := newCoreProtocolReceiptsFixture(t)
		_, genuine := fixture.compileFresh("c10-explorer-save", "Explorer Binding")
		receipt := genuine
		receipt.ExplorerID = c1011ExplorerID + "-foreign"
		recomputeReceiptIdentity(t, &receipt, true)
		if err := receipt.Validate(); err != nil {
			t.Fatalf("forged foreign-explorer receipt is not self-consistent, so its rejection would not prove the explorer binding: %v", err)
		}
		stored := fixture.insertReceipt(receipt)
		if present, err := fixture.store.GetCompilationReceipt(context.Background(), stored.ID); err != nil || present == nil || present.ExplorerID != c1011ExplorerID+"-foreign" {
			t.Fatalf("receipt under test should exist with the foreign explorer: present=%+v err=%v", present, err)
		}
		coreProtocolBoundaryFailure(t, fixture.preview(stored.ID), "foreign explorer", http.StatusNotFound, "COMPILE_RECEIPT_NOT_FOUND", "receipt", "not found")
	})

	t.Run("unknown-output", func(t *testing.T) {
		fixture := newCoreProtocolReceiptsFixture(t)
		_, genuine := fixture.compileFresh("c10-output-save", "Output Binding")
		fixture.insertReceipt(genuine)
		if labels := previewResponseColumns(t, fixture.preview(genuine.ID)); len(labels) != 1 || labels[0] != "Output Binding" {
			t.Fatalf("companion preview labels=%v want [Output Binding]", labels)
		}
		coreProtocolBoundaryFailure(t, fixture.previewOutput(genuine.ID, "patients-unbound"), "unknown output", http.StatusUnprocessableEntity, "UNKNOWN_AUTHORING_OUTPUT", "preview", "not in the receipt")
	})

	for _, binding := range []struct {
		name   string
		tamper func(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot)
	}{
		{name: "snapshot-token", tamper: func(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) {
			receipt.SnapshotToken = snapshot.Token + "-unretained"
		}},
		{name: "source-generation", tamper: func(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) {
			receipt.SourceGeneration = snapshot.Identity.Generation + "-b"
		}},
		{name: "capability-schema", tamper: func(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) {
			receipt.CapabilitySchemaDigest = strings.Repeat("f", 64)
		}},
		{name: "authorization-scope", tamper: func(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) {
			receipt.AuthorizationScopeDigest = strings.Repeat("e", 64)
		}},
	} {
		t.Run("stale-"+binding.name, func(t *testing.T) {
			fixture := newCoreProtocolReceiptsFixture(t)
			_, genuine := fixture.compileFresh("c10-"+binding.name+"-save", "Authorized Binding")
			receipt := genuine
			binding.tamper(&receipt, fixture.snapshot)
			recomputeReceiptIdentity(t, &receipt, true)
			if err := receipt.Validate(); err != nil {
				t.Fatalf("forged %s receipt must remain internally valid so the capability binding is the only difference: %v", binding.name, err)
			}
			if _, err := fixture.store.GetCompilationReceipt(context.Background(), genuine.ID); err != nil {
				t.Fatalf("genuine companion missing: %v", err)
			}
			stored := fixture.insertReceipt(receipt)
			coreProtocolBoundaryFailure(t, fixture.preview(stored.ID), "stale "+binding.name, http.StatusConflict, "RECEIPT_STALE", "preview", "capability snapshot")
			if labels := previewResponseColumns(t, fixture.preview(genuine.ID)); len(labels) != 1 || labels[0] != "Authorized Binding" {
				t.Fatalf("genuine companion preview labels=%v want [Authorized Binding]", labels)
			}
		})
	}

	for _, binding := range []struct {
		name    string
		keepKey bool
		tamper  func(receipt *explorer.CompilationReceipt)
		cause   string
	}{
		{name: "output-contract-digest", keepKey: true, cause: "output contract digest mismatch", tamper: func(receipt *explorer.CompilationReceipt) {
			receipt.OutputContractDigest = "sha256:" + strings.Repeat("0", 64)
		}},
		{name: "emitted-column-label", keepKey: true, cause: "does not match emittedColumns", tamper: func(receipt *explorer.CompilationReceipt) {
			emitted := append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
			for index := range emitted {
				if emitted[index].OutputID == c1011OutputID {
					emitted[index].Label = "Tampered Frozen Label"
				}
			}
			receipt.EmittedColumns = emitted
		}},
		{name: "shape-digest", keepKey: false, cause: "compilation key mismatch", tamper: func(receipt *explorer.CompilationReceipt) {
			receipt.ShapeDigest = strings.Repeat("d", 64)
		}},
	} {
		t.Run("recompile-"+binding.name, func(t *testing.T) {
			fixture := newCoreProtocolReceiptsFixture(t)
			_, genuine := fixture.compileFresh("c10-"+binding.name+"-save", "Integrity Binding")
			receipt := genuine
			binding.tamper(&receipt)
			if err := receipt.Validate(); err == nil || !strings.Contains(err.Error(), binding.cause) {
				t.Fatalf("tampered %s must fail Validate with a cause containing %q: err=%v", binding.name, binding.cause, err)
			}
			recomputeReceiptIdentity(t, &receipt, binding.keepKey)
			if _, err := fixture.store.GetCompilationReceipt(context.Background(), genuine.ID); err != nil {
				t.Fatalf("genuine companion missing: %v", err)
			}
			stored := fixture.insertReceipt(receipt)
			if stored.ID == genuine.ID {
				t.Fatalf("tampered receipt reused the genuine id, so the tamper was never under test")
			}
			coreProtocolBoundaryFailure(t, fixture.preview(stored.ID), "recompile "+binding.name, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED", "receipt", "must be recompiled")
			if labels := previewResponseColumns(t, fixture.preview(genuine.ID)); len(labels) != 1 || labels[0] != "Integrity Binding" {
				t.Fatalf("genuine companion preview labels=%v want [Integrity Binding]", labels)
			}
		})
	}
}

func recomputeReceiptIdentity(t *testing.T, receipt *explorer.CompilationReceipt, keepKey bool) {
	t.Helper()
	key, err := explorer.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if keepKey {
		receipt.CompilationKey = key
	}
	id, err := explorer.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID = id
}

func TestCoreAuthoringProtocolHistoricalAndRevokedReceipts(t *testing.T) {
	fixture := newCoreProtocolReceiptsFixture(t)
	_, historical := fixture.compileFresh("c11-historical-save", "Historical Patient")
	_, current := fixture.compileFresh("c11-current-save", "Current Patient")
	if historical.ID == current.ID {
		t.Fatalf("newer draft did not produce a distinct receipt: %s", current.ID)
	}
	if head := fixture.store.explorers[testExplorerKey(c1011Project, c1011ExplorerID)]; head.DraftVersion != fixture.headVersion || head.DraftDigest != fixture.headDigest {
		t.Fatalf("persisted head disagrees with compiled head: %+v", head)
	}

	if labels := previewResponseColumns(t, fixture.preview(historical.ID)); len(labels) != 1 || labels[0] != "Historical Patient" {
		t.Fatalf("historical preview labels=%v want [Historical Patient]", labels)
	}
	if labels := previewResponseColumns(t, fixture.preview(current.ID)); len(labels) != 1 || labels[0] != "Current Patient" {
		t.Fatalf("current preview labels=%v want [Current Patient]", labels)
	}

	fixture.revokeSnapshot()
	coreProtocolBoundaryFailure(t, fixture.preview(historical.ID), "historical revoked", http.StatusConflict, "RECEIPT_STALE", "preview", "capability snapshot")
	coreProtocolBoundaryFailure(t, fixture.preview(current.ID), "current revoked", http.StatusConflict, "RECEIPT_STALE", "preview", "capability snapshot")

	fixture.retainSnapshot()
	if labels := previewResponseColumns(t, fixture.preview(current.ID)); len(labels) != 1 || labels[0] != "Current Patient" {
		t.Fatalf("restored snapshot should admit the current receipt again, labels=%v", labels)
	}
	if labels := previewResponseColumns(t, fixture.preview(historical.ID)); len(labels) != 1 || labels[0] != "Historical Patient" {
		t.Fatalf("restored snapshot should admit the historical receipt again, labels=%v", labels)
	}
}
