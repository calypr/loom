package server

// C19 — independent row/schema semantics (docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md:69
// family row "Independent row semantics | C19, M4 | Reuse literal fixtures and real
// Arango/ClickHouse execution for types, values, nulls, duplicates and cardinality after
// edit/restore", and the scenario-floor row at :388 "Run the same authored definitions
// through compile/receipt/preview and independently evaluate expected schema and rows.
// Include a saved edit and restore. | Exact public identities, types, cardinality, values,
// nulls and duplicates match the specified fixture. Expectations are not computed by the
// compiler implementation under test.").
//
// This file owns the row/schema SEMANTICS half of C19. The protocol-identity half of the
// same scenario is covered elsewhere in the family (identity/competing save in
// core_authoring_protocol_test.go, restore admission in core_authoring_protocol_restore_test.go,
// receipt bindings in core_authoring_protocol_receipts_test.go, required-store schedules in
// core_authoring_protocol_formal_replay_test.go:35-45), so nothing here re-asserts those.
//
// Independence rule: every expectation below is a hand-written literal in this file —
// the contract labels, the public column identities, the logical types, the row values,
// the expected row counts and the expected sampled flags. No expectation is read back from
// the compiled receipt, the compiled recipe, the engine plan or any other compiler output.
// The compiled output is only ever the value under test. The authored fixtures (capability
// candidates, workspace columns and the store rows) are also literals authored here, so the
// oracle never consults the implementation under test.
//
// Server-enforced versus observed-only, recorded honestly:
//   - Enforced and asserted: preview identity/limit admission
//     (internal/explorer/lifecycle/preview.go:13-21), the public-column projection that
//     publishes only compiled public columns and only keys the store row actually carries
//     (internal/dataframe/execution/engine.go:1316-1329), the row-visitor count that becomes
//     the wire rowCount (internal/dataframe/execution/engine.go:1101-1122,1148 and
//     internal/server/explorer_preview_response.go:233-286,305-311), truncation/sampled
//     (internal/dataframe/execution/engine.go:1153-1156 and
//     internal/server/openapi_explorer_authoring_direct.go:539) and the empty-array-not-null
//     row shape (internal/server/explorer_preview_response.go:227-229,292).
//   - Observed-only, recorded with t.Log and NOT asserted as a promise: rowCount has no
//     independent second source (the wire field and the delivered rows come from the same
//     visitor counter, so "rowCount vs actual rows" can only be checked as self-agreement);
//     nothing de-duplicates rows or row identities; no negative/zero-limit validation exists
//     at the route boundary even though openapi/openapi.yaml:6465 declares minimum 1
//     (explicit 0 is rewritten to -1 at internal/server/openapi_explorer_authoring_direct.go:504-510
//     and only the engine rejects it at internal/dataframe/execution/engine.go:1256-1261).
//   - Compiler-influenced expectations: the four logical types and shapes asserted here are
//     the logical types of the capability candidates THIS file authors, which the compiler is
//     required to echo verbatim (internal/explorer/compilation/semantic_compile.go:137-142
//     rejects any mismatch with CAPABILITY_LOGICAL_TYPE_MISMATCH, and
//     internal/explorer/compilation/semantic_compile.go:483 copies the candidate type into the
//     emission). The shape/nullable literals are the fixed values that same emission records
//     carry for a root-occurrence scalar field projection (ibid. :400, :483). They are echo
//     guarantees of authored input, not values computed by the compiler, so they stay literal.

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

	// Public column identities are the authored Column values below. The compiled contract
	// must publish exactly these names (internal/explorer/compilation/semantic_compile.go:483
	// uses the authored Column as PublicColumn). Order is the canonicalized order the server
	// commits at reconcile: presentation class, then presentation order, then public column
	// name ascending (internal/explorer/authoringv2/canonical.go:95-107, applied by
	// lifecycle/authoring.go:391), so the expected sequence below is alphabetical.
	c19ColumnPatient    = "c_patient"
	c19ColumnActive     = "c_active"
	c19ColumnBirthOrder = "c_birth_order"
	c19ColumnBirthDate  = "c_birth_date"

	// Contract labels as authored; the saved-edit cases change exactly one of them.
	c19LabelPatient     = "Patient ID"
	c19LabelActive      = "Patient active"
	c19LabelBirthOrder  = "Birth order"
	c19LabelBirthDate   = "Birth date"
	c19LabelPatientEdit = "Patient Identifier (saved edit)"

	c19RowIdentityPrefix = "c19-row-"
)

// c19ContractWant is one hand-written expected preview schema. Every field is supplied as a
// literal at the call site; the type only documents that these are the four parallel lists.
type c19ContractWant struct {
	columns     []string
	labels      []string
	logicalType []string
	shapes      []string
	nullable    []bool
}

// c19StoredHead reads the persisted draft head for THIS fixture's project/explorer ids.
// The shared coreProtocolStoredHead helper at core_authoring_protocol_restore_test.go:179
// hardcodes the "core-protocol" id, so this file reads its own id through the same store
// double instead of reusing that helper.
func c19StoredHead(t *testing.T, fixture *coreProtocolFixture) explorer.Explorer {
	t.Helper()
	current, err := fixture.store.Get(context.Background(), c19Project, c19ExplorerID)
	if err != nil {
		t.Fatalf("read persisted head: %v", err)
	}
	return *current
}

// c19ExpectHead proves the persisted head still is the expected revision after a command.
func c19ExpectHead(t *testing.T, fixture *coreProtocolFixture, wantVersion int64, wantDigest, stage string) explorer.Explorer {
	t.Helper()
	stored := c19StoredHead(t, fixture)
	if stored.DraftVersion != wantVersion || stored.DraftDigest != wantDigest {
		t.Fatalf("%s changed the persisted head: got version=%d digest=%q want version=%d digest=%q", stage, stored.DraftVersion, stored.DraftDigest, wantVersion, wantDigest)
	}
	return stored
}

// c19ExpectRestoreHistory asserts the traced history semantics of a restore commit against
// whichever store the fixture runs on: the new head's pointer must be that store's snapshot
// of the pre-restore head (savedVersion/savedDigest and the edited label), while the undo
// target the command consumed must be its snapshot of the head the rows/schema were restored
// from (baselineVersion/baselineDigest and the original label). Revision ids differ per store
// (http_test_helpers_test.go:152 vs arango/drafts.go:139-141), so only content is asserted.
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

// c19AuthoredLabel reads one authored label back out of a persisted workspace by public
// column name, so the read does not depend on the canonicalized column order.
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

// c19WantBaselineContract is the authored contract, unchanged by any command: the four
// scalar columns and the logical types this file's capability snapshot advertises, listed in
// the canonical publish order (alphabetical by public column name, per the comment on the
// column constants above).
func c19WantBaselineContract() c19ContractWant {
	return c19ContractWant{
		columns:     []string{c19ColumnActive, c19ColumnBirthDate, c19ColumnBirthOrder, c19ColumnPatient},
		labels:      []string{c19LabelActive, c19LabelBirthDate, c19LabelBirthOrder, c19LabelPatient},
		logicalType: []string{"boolean", "date", "integer", "string"},
		shapes:      []string{"scalar", "scalar", "scalar", "scalar"},
		nullable:    []bool{true, true, true, true},
	}
}

// c19WantEditedContract is the baseline with exactly one label replaced by the saved edit.
func c19WantEditedContract() c19ContractWant {
	want := c19WantBaselineContract()
	want.labels = []string{c19LabelActive, c19LabelBirthDate, c19LabelBirthOrder, c19LabelPatientEdit}
	return want
}

// c19CapabilitySnapshot is a test-owned literal snapshot with one scalar candidate per
// authored column. Each candidate's logical type is the FHIR terminal primitive kind of its
// field path on Patient, so no expectation depends on the compiler inventing a type:
// Patient.active is a boolean and Patient.multipleBirthInteger an integer and
// Patient.birthDate a date-formatted string in the generated definition
// (generated/fhirschema/generated.go:10946-10950 and the Patient block from :10874), which
// internal/fhir/schema/terminal_metadata.go:31-53 classifies as boolean/integer/date.
// A floating/decimal column is NOT exercised: the generated Patient schema has no decimal
// primitive terminal, so no honest fixture exists for it — recorded, not faked.
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

// c19Workspace is the authored contract under test: one records-row output over Patient with
// the four scalar columns. The authored order here is not the publish order: reconcile
// canonicalizes the column list (internal/explorer/authoringv2/canonical.go:95-107), and the
// expectations below state that canonical order explicitly.
func c19Workspace(t *testing.T, snapshot capability.Snapshot) authoringv2.Workspace {
	t.Helper()
	raw := c19AuthoredWorkspace()
	prepared, err := authoringv2.PrepareWorkspaceForCompilation(raw, authoringV2Catalog(snapshot, c19ExplorerID))
	if err != nil {
		t.Fatalf("prepare seed workspace: %v", err)
	}
	return prepared
}

// c19AuthoredWorkspace is the literal authored contract. It is deliberately NOT what the
// fixture seeds: a first head created outside the command surface must already be in the
// compilation-prepared form that every command save produces (authoring v2 PrepareWorkspace-
// ForCompilation = MigrateLegacyContributors + MigrateLosslessDefaults +
// NormalizePresentationOrders, internal/explorer/authoringv2/migration.go:88-99, applied as a
// pair in internal/server/construction_proposal_zero_column_test.go:71-75). Seeding raw bytes
// instead leaves SemanticsVersion unset, so the compile prepare fills defaults the stored
// bytes never got and the receipt intent (digest of the prepared form,
// internal/server/explorer_receipt_contract.go:142) legitimately diverges from the stored CAS
// digests — observed in a capture at
// /tmp/opencode/loom-core-protocol/evidence/c19/run-08-arango-localizer.log (intent
// sha256:5a83... vs CAS sha256:af5a..., response identical to the persisted Arango doc, so
// NOT a store-parity finding).
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

// c19RowStore is the controlled execution boundary that stands in for the dataset backend.
// It hands the engine exactly the literal rows a test sets, in order, and remembers the
// batch size each call asked for. It never reads anything from the compiled plan, so it
// cannot turn a compiler output into its own oracle.
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

// previewRows is the engine's preview lane of the single SourceRows port.
// Each row is deep-copied before the visitor sees it, because the engine
// writes the stable row identity into the map it is given; the authored
// fixture must stay unchanged so one row set can drive several previews. A
// visitor error (the engine signals its limit that way) stops the walk
// immediately, like a real backend cursor would.
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

// c19Row is a store row with a stable identity already carried, which the execution boundary
// explicitly accepts as a store-supplied identity
// (internal/dataframe/execution/engine.go:1758-1760). Row-identity GENERATION is not a C19
// claim; identity preservation through the public boundary is asserted where it matters
// (duplicate rows keep one identical identity).
func c19Row(identity string, values map[string]any) map[string]any {
	row := map[string]any{"__loom_row_id": c19RowIdentityPrefix + identity}
	for key, value := range values {
		row[key] = value
	}
	return row
}

// c19NewFixture wires the production generated strict adapter, real authoring handlers,
// lifecycle service, compile chain and persisted receipt store exactly as
// newCoreProtocolFixture does (core_authoring_protocol_test.go:124-196), with the one
// deliberate difference this row needs: the preview execution seam returns this test's
// literal rows instead of the empty set. Only the row/preview data source differs; every
// request still goes through the public routes.
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
	// Compile-time row reads stay empty: C19 claims nothing about compile-time execution,
	// and a non-empty read here would be indistinguishable from a preview read.
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

// c19Preview posts the public preview request with an explicit limit, or with the limit
// omitted when limit is zero, so the documented default is reachable.
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

// c19ExpectContract compares the published preview schema against a hand-written expected
// schema, field by field, including column order.
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

// c19ExpectRows compares the published rows against hand-written literal rows. Values are
// compared as the public JSON contract delivers them (JSON numbers decode to float64), and
// DeepEqual on the whole map makes a MISSING key distinguishable from an explicit null.
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

// c19TypedRow is the single fully-populated typed row: one string, one boolean, one integer
// and one date-shaped string, each a literal written here.
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

// TestCoreAuthoringProtocolRowSchemaSemantics is the C19 row/schema semantics witness
// (docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md:69, :388) on the saved-draft path
// and the restore path, with the persisted-store variant opt-in through
// LOOM_TEST_ARANGO_URL exactly like TestCoreAuthoringProtocolAgainstArango
// (core_authoring_protocol_arango_conformance_test.go:38-42).
func TestCoreAuthoringProtocolRowSchemaSemantics(t *testing.T) {

	// ------------------------------------------------------------------
	// Saved-draft path: one accepted edit (UPDATE_COLUMN on the output's
	// column contract) then reconcile + receipt-bound preview.
	// ------------------------------------------------------------------

	// C19 "types ... values": the published schema carries the authored logical types in
	// authored order and the delivered row carries each value with its own JSON type.
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

		// The saved head really carries the edit, and the receipt describes that head.
		stored := c19StoredHead(t, fixture)
		if !strings.Contains(string(stored.DraftConfig), c19LabelPatientEdit) {
			t.Fatalf("persisted draft lacks the saved label: %s", stored.DraftConfig)
		}
		emitted := fixture.store.receipts[compiled.ReceiptId]
		if emitted.IntentDigest != ack.DraftDigest {
			t.Fatalf("receipt intentDigest=%q does not name the saved head digest=%q", emitted.IntentDigest, ack.DraftDigest)
		}

		// Wire-level type check: the integer stays a JSON number and the boolean stays a
		// JSON boolean rather than becoming a string.
		for _, want := range []string{`"c_patient":"patient-alpha"`, `"c_active":true`, `"c_birth_order":2`, `"c_birth_date":"2001-02-03"`} {
			if !strings.Contains(response.Body, want) {
				t.Fatalf("preview body is missing the typed encoding %s: %s", want, response.Body)
			}
		}
		if strings.Contains(response.Body, `"c_birth_order":"2"`) || strings.Contains(response.Body, `"c_active":"true"`) {
			t.Fatalf("preview stringified a typed value: %s", response.Body)
		}
	})

	// C19 "nulls": a key the store row omits, a key the store row sets to null, and a key
	// set to the empty string are three distinguishable public outcomes.
	t.Run("saved-edit-null-handling", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		saved, ack := fixture.saveLabel("c19-nulls-save", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-nulls-reconcile"))

		rows.set([]map[string]any{
			// (a) birthDate key ABSENT from the store row; active explicitly null.
			c19Row("missing", map[string]any{
				c19ColumnPatient:    "patient-beta",
				c19ColumnActive:     nil,
				c19ColumnBirthOrder: 1,
			}),
			// (b) birthDate explicitly null.
			c19Row("explicit-null", map[string]any{
				c19ColumnPatient:    "patient-gamma",
				c19ColumnActive:     true,
				c19ColumnBirthOrder: 3,
				c19ColumnBirthDate:  nil,
			}),
			// (c) birthDate present but the empty string, which is not null.
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

		// Missing-vs-null is a wire-visible distinction, asserted on the bytes too: a missing
		// key publishes no member at all (internal/dataframe/execution/engine.go:1316-1321
		// copies only keys the store row carries), an explicit null stays an explicit JSON
		// null, and an empty string stays an empty JSON string.
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

		// Observed-only, recorded not asserted: the schema still says nullable
		// (internal/explorer/compilation/semantic_compile.go:483 records Nullable on the
		// emission), but nothing in the preview path validates a published value against the
		// published logical type. A row whose c_active value is a string would still be
		// published; C19 therefore claims type FIDELITY for the values supplied, not type
		// ENFORCEMENT against the contract, and this run supplies type-correct values.
		t.Log("C19 (:69/:388) observed-only: the server publishes store values without validating them against the published logicalType/nullable; fidelity is asserted, enforcement is not claimed")

		// A row the store hands over with no public keys at all still becomes an object (the
		// encoder only guarantees an object per visited row,
		// internal/server/explorer_preview_response.go:237-239), never a null element.
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

	// C19 "duplicates": two identical store rows are published twice, byte-identical, and the
	// server neither de-duplicates them nor assigns them distinct identities.
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

	// C19 "cardinality": what the caller asks for, what the store can supply, and what the
	// response claims must line up, and the claimed count must agree with the delivered rows.
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

		// (a) Store supplies fewer rows than the requested limit: all three arrive, nothing is
		// padded, sampled stays false (the preview proved it exhausted the output).
		rows.set(five)
		response := c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview := c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{
			{"__loom_row_id": c19RowIdentityPrefix + "one", c19ColumnPatient: "patient-one", c19ColumnActive: true, c19ColumnBirthOrder: float64(1), c19ColumnBirthDate: "2001-01-01"},
			{"__loom_row_id": c19RowIdentityPrefix + "two", c19ColumnPatient: "patient-two", c19ColumnActive: false, c19ColumnBirthOrder: float64(2), c19ColumnBirthDate: "2002-02-02"},
			{"__loom_row_id": c19RowIdentityPrefix + "three", c19ColumnPatient: "patient-three", c19ColumnActive: true, c19ColumnBirthOrder: float64(3), c19ColumnBirthDate: "2003-03-03"},
		}, 3, false)

		// (b) Store supplies more rows than the requested limit: the limit is enforced
		// (internal/dataframe/execution/engine.go:1098-1101,1124-1126), the first requested
		// rows arrive in store order, the excess never appears, and sampled flips true
		// (internal/dataframe/execution/engine.go:1153-1156 →
		// internal/server/openapi_explorer_authoring_direct.go:539).
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

		// (c) An empty store result is an empty array with rowCount 0, never null and never a
		// missing field (internal/server/explorer_preview_response.go:227-229,292,305-311).
		rows.set(nil)
		response = c19Preview(t, fixture, compiled.ReceiptId, 10)
		preview = c19DecodePreview(t, response)
		c19ExpectRows(t, preview, response.Body, []map[string]any{}, 0, false)
		if !strings.Contains(response.Body, `"rows":[]`) {
			t.Fatalf("an empty result must publish an empty array: %s", response.Body)
		}

		// (d) The documented default applies when the caller omits the limit
		// (internal/explorer/lifecycle/preview.go:16-18 with
		// internal/dataframe/execution/engine.go:47-50): 26 available rows yield exactly the
		// documented 25 and sampled=true.
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

		// (e) A limit above the documented maximum is refused with the typed contract failure
		// the handler promises (internal/explorer/lifecycle/preview.go:19-21), and no rows are
		// published.
		rows.set(five)
		over := c19Preview(t, fixture, compiled.ReceiptId, dataframeexecution.MaxPreviewLimit+1)
		coreProtocolBoundaryFailure(t, over, "preview above the maximum limit", http.StatusUnprocessableEntity, "INVALID_PREVIEW_LIMIT", "preview", "limit must be between 1 and 1000")

		// (f) A row count the client cannot forge: rowCount is server-derived only, so there
		// is no client-side surface to make it disagree with the delivered rows, and the two
		// fields come from one counter (internal/server/explorer_preview_response.go:233-286
		// increments once per visited row and :305-311 emits that same counter). The check
		// below is therefore self-agreement, not an independent cross-check.
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

		// (g) Zero/negative limits have NO route-level validation even though the public
		// contract declares minimum 1 (openapi/openapi.yaml:6465): the handler rewrites an
		// explicit 0 to -1 (internal/server/openapi_explorer_authoring_direct.go:504-510), the
		// lifecycle default only covers an absent value
		// (internal/explorer/lifecycle/preview.go:16-18), and the rejection happens deep in the
		// engine (internal/dataframe/execution/engine.go:1256-1261). Only what the traced
		// handler promises is asserted — no rows, and the failure stays a typed preview-stage
		// error (internal/server/explorer_preview_response.go:44-104 stamps Stage "preview").
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

	// ------------------------------------------------------------------
	// Restore path: after a draft restore, the published rows and schema must
	// belong to the restored head's contract, not the head that was restored
	// away from. Preview admits on the receipt and never requires head
	// equality (internal/explorer/lifecycle/preview.go:28-44), so the older
	// edited-contract receipt stays executable and stays distinguishable.
	// ------------------------------------------------------------------

	t.Run("restore-rows-and-schema-follow-the-restored-head", func(t *testing.T) {
		fixture, rows := c19NewFixture(t)
		_, hydrated := fixture.getBuilder()
		if hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
			t.Fatalf("baseline hydration identity mismatch: %+v", hydrated)
		}

		// Full literal row set reused across every state: typed values, an explicit null, an
		// absent key, an empty string, a duplicate, and rows beyond any requested limit.
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

		// Saved edit, then reconcile: edited contract over the same rows.
		saved, ack := fixture.saveLabel("c19-restore-edit", c19LabelPatientEdit, hydrated.DraftVersion, hydrated.DraftDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("save status=%d body=%s", saved.StatusCode, saved.Body)
		}
		edited := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "c19-restore-reconcile-edited"))
		editedResponse := c19Preview(t, fixture, edited.ReceiptId, 10)
		editedPreview := c19DecodePreview(t, editedResponse)
		c19ExpectContract(t, editedPreview, c19WantEditedContract())
		c19ExpectRows(t, editedPreview, editedResponse.Body, wantFullRows, 4, false)

		// Restore through the public command surface (core_authoring_protocol_restore_test.go:112-128).
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

		// History-pointer semantics read from the WRITE path, not from fixture bookkeeping:
		// every accepted draft CAS snapshots the head as it stood BEFORE the command and
		// publishes that snapshot's store-minted id as the new head's history pointer
		// (map double internal/server/http_test_helpers_test.go:152-161; Arango
		// internal/backends/arango/internal/explorer/drafts.go:52-77). service.go:324 never touches the
		// pointer, and draft_restore.go:33/47 validates the consumed undo target against the
		// OLD head's pointer instead of copying it forward. So the restored head's pointer is
		// the snapshot of the EDITED head (the next Undo slot), distinct from the revision the
		// restore read. Ids are store-spelled, so the assertion goes through each store's own
		// GetDraftRevision content, never the id string.
		c19ExpectRestoreHistory(t, "map store", stored.PreviousDraftRevisionID, undoRevision,
			ack.DraftVersion, ack.DraftDigest, c19LabelPatientEdit,
			hydrated.DraftVersion, hydrated.DraftDigest, c19LabelPatient,
			func(revisionID string) (*explorer.DraftRevision, error) {
				return fixture.store.GetDraftRevision(context.Background(), c19Project, c19ExplorerID, revisionID)
			})

		// Reconcile the restored head: the published schema is the ORIGINAL contract again,
		// the rows are the same literal rows, and this is a distinct receipt from the edited
		// compilation.
		restoredReceipt := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "c19-restore-reconcile-restored"))
		if restoredReceipt.ReceiptId == edited.ReceiptId {
			t.Fatal("the restored head reused the edited head's receipt identity")
		}
		// Same traced digest provenance as the Arango leg (see the block in
		// c19AgainstArango): on a restored head the CAS digest republishes the restored-from
		// revision digest while the receipt intents the compilation-prepare form; the map
		// double stores the response verbatim, so double and Arango agree here.
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

		// Cardinality after restore: the limit is still honoured and truncation still reports
		// sampled, so restore did not quietly change the row contract.
		limitedResponse := c19Preview(t, fixture, restoredReceipt.ReceiptId, 2)
		limited := c19DecodePreview(t, limitedResponse)
		c19ExpectRows(t, limited, limitedResponse.Body, wantFullRows[:2], 2, true)
		c19ExpectContract(t, limited, c19WantBaselineContract())

		// Null/duplicate semantics survive restore unchanged: one absent key, one explicit
		// null, one empty string, and the duplicate pair keep their exact public shape.
		if _, present := restoredPreview.Rows[0][c19ColumnBirthDate]; !present {
			t.Fatalf("the typed row lost its date after restore: %#v", restoredPreview.Rows[0])
		}
		if value, present := limited.Rows[0][c19ColumnActive]; !present || value != true {
			t.Fatalf("boolean fidelity changed after restore: %#v", limited.Rows[0])
		}
		if !reflect.DeepEqual(restoredPreview.Rows[0], restoredPreview.Rows[1]) {
			t.Fatalf("the duplicate pair stopped being identical after restore: %#v", restoredPreview.Rows[:2])
		}

		// The superseded edited receipt stays executable and keeps describing ITS OWN frozen
		// contract (immutable-receipt contract,
		// internal/explorer/lifecycle/preview.go:28-44 admits on the receipt without head
		// equality), which is what makes "the current head's rows/schema" a meaningful claim:
		// the current receipt shows the restored label, the retained receipt still shows the
		// saved-edit label.
		supersededResponse := c19Preview(t, fixture, edited.ReceiptId, 10)
		superseded := c19DecodePreview(t, supersededResponse)
		c19ExpectContract(t, superseded, c19WantEditedContract())
		c19ExpectRows(t, superseded, supersededResponse.Body, wantFullRows, 4, false)

		// And a receipt published for the ORIGINAL head recompiles to the same content-addressed
		// receipt the restored head uses, so "restored" means the same contract identity rather
		// than a look-alike.
		again := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "c19-restore-reconcile-again"))
		if again.ReceiptId != restoredReceipt.ReceiptId {
			t.Fatalf("recompiling the identical restored head changed the receipt identity: %s vs %s", again.ReceiptId, restoredReceipt.ReceiptId)
		}
	})

	// ------------------------------------------------------------------
	// Opt-in real-store variant, same pattern as
	// TestCoreAuthoringProtocolAgainstArango.
	// ------------------------------------------------------------------

	t.Run("persisted-state-against-arango", func(t *testing.T) {
		endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
		if endpoint == "" {
			t.Skip("LOOM_TEST_ARANGO_URL is unset: the persisted-head/schema-semantics variant is skipped, not simulated. The map-store cases above are the executed evidence.")
		}
		c19AgainstArango(t, endpoint)
	})
}

// c19AgainstArango runs the same literal row/schema expectations while the draft head, the
// draft history and the compile receipts live in a real owned Arango database, and reads the
// persisted bytes back through a second repository instance so the claim does not rest on the
// service's in-process objects.
//
// Honest bound: this leg verifies the AUTHORING-side persistence (head bytes after the saved
// edit and after the restore, receipt identity) behind the same rows/schema. The row VALUES
// still come from the controlled preview execution seam, because no dataset ingest runs here;
// claims about dataset-resident values/types belong to the real-store rows work that the
// family notes at core_authoring_protocol_formal_replay_test.go:35-45 and the plan M4 row
// assign to owned Arango/ClickHouse fixtures, and are NOT claimed by this test.
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

	// The edited head is the committed Arango document, byte for byte.
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
	// Digest provenance on a RESTORED head, traced to the write and compile paths:
	//   - the CAS head digest is the restored revision's own digest, because the restore
	//     republishes those stored bytes (draft_restore.go:66-68 admits the revision only when
	//     its re-derived digest matches; service.go:296-301/324 stores them), so it equals the
	//     baseline head digest this fixture started from, NOT a freshly derived value;
	//   - the receipt intent is the digest of the compilation-prepare form,
	//     PrepareWorkspaceForCompilation = migrations followed by NormalizePresentationOrders
	//     (internal/explorer/authoringv2/migration.go:97, canonical.go:95-107), digested at
	//     internal/server/explorer_receipt_contract.go:142. Because this fixture seeds its
	//     first head through that same preparation (see c19Workspace), preparing an already
	//     prepared head is a no-op, so intent and CAS name the same value here and both digests
	//     are pinned. Seeding raw bytes instead splits them (the migration fills defaults the
	//     stored bytes never received) — that harness artifact, not a store difference, was the
	//     original failure.
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

	// Restart-equivalent read: a fresh repository instance reproduces the restored head from
	// persisted bytes, so the restored rows/schema rest on committed state.
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
	// Same traced semantics as the map-store leg: the pointer names the store's snapshot of
	// the head as it stood before the restore (the edited head at ack.DraftVersion), and the
	// consumed undo target names the revision whose bytes were restored. Arango mints those
	// ids as "draft_revision_" + sha256-keyed parts (drafts.go:139-141 with store/arango
	// store.go:27-34), a different spelling from the map double, so BOTH sides are read back
	// through a second repository instance and asserted on content.
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
	// Store fidelity, asserted the same way on both repositories: what Arango persisted is the
	// reconcile response byte for byte — intent and CAS stay distinct. (This is why the
	// restored-head mismatch is expectation drift, not a store-parity finding: the double and
	// Arango returned identical bytes for the same request.)
	if persistedRestored.IntentDigest != restoredReceipt.IntentDigest || restartedReceipt.IntentDigest != restoredReceipt.IntentDigest {
		t.Fatalf("persisted restored receipt intent live=%q restart=%q response=%q", persistedRestored.IntentDigest, restartedReceipt.IntentDigest, restoredReceipt.IntentDigest)
	}
	t.Log("C19 (:69/:388) arango mode: draft head, draft history and receipts are real Arango bytes and were re-read through a second repository; the preview rows still come from this test's controlled execution seam (no dataset ingest), so dataset-resident value claims remain unproven here")
}
