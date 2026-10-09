package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
	"github.com/gofiber/fiber/v3"
)

//go:embed testdata/case008_saved_builder_workspace.json
var case008SavedWorkspaceFS embed.FS

const (
	case008Project     = "loom_dev_verify_mv0q3i44-bfd8829"
	case008ExplorerID  = "verify-44-bfd8829-cohort-recode"
	case008OutputID    = "out_a8daf7635002468eb86169ed"
	case008RevisionID  = "grouprev_0003bc86ece6dec199c1d4b61530b36633ebac9ef06b1f7dc035bbae221b011e"
	case008DraftDigest = "sha256:f63cfcd8f42aa3d20f75ee344aa3e26e8a3c6820bd348a9dfdc729c8aefbde9f"
)

func TestCase008ExplicitGroupRowDefinitionPreviewCompilesCandidateReceipt(t *testing.T) {
	rawWorkspace, err := case008SavedWorkspaceFS.ReadFile("testdata/case008_saved_builder_workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var workspace authoringv2.Workspace
	if err := json.Unmarshal(rawWorkspace, &workspace); err != nil {
		t.Fatal(err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != case008DraftDigest || len(workspace.Documents) != 1 || workspace.Documents[0].Output.ID != case008OutputID ||
		workspace.Documents[0].RootResourceType != "Patient" || workspace.Documents[0].Rows.Kind != authoringv2.RowDefinitionRecords ||
		len(workspace.Documents[0].Columns) != 1 || workspace.Documents[0].Columns[0].Source.Field.Path != "id" ||
		workspace.Documents[0].Columns[0].Source.Field.ProjectionMode != "VALUE" {
		t.Fatalf("retained pre-proposal workspace does not match its digest/output/source: digest=%s document=%#v", digest, workspace.Documents)
	}

	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: projectid.Canonical(case008Project), Generation: "devloop-v1",
			AuthorizationScopeDigest: explorerScopeDigest(scope), SchemaDigest: "case008-schema",
			ResourceInventoryDigest: "case008-inventory", RelationshipDigest: "case008-relationships",
			FieldDigest: "case008-fields", ShapeDigest: "case008-shape",
			ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion,
			TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_88fd86589e77093ea1cd19a7", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", Populated: true, DocumentCount: 2, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{ID: "c_13314d7bfa5241c3464b0dbd", NodeID: "n_88fd86589e77093ea1cd19a7", ResourceType: "Patient", FieldPath: "id", Label: "id", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true}},
		nil,
	)
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.create(explorer.Explorer{
		Project: case008Project, ExplorerID: case008ExplorerID, Title: workspace.Explorer.Title,
		DraftConfig: draft, DraftVersion: 2, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}

	var queryCalls int
	var basePreviewRows []map[string]any
	var candidatePreviewRows []map[string]any
	var basePreviewColumns []string
	var candidatePreviewColumns []string
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, ScopeDigest: recipeScopeDigest,
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			if _, grouped := bindVars["group_rows_revision_id"]; grouped {
				return visit(map[string]any{
					"group_revision_id": case008RevisionID, "group_id": "group-a", "group_label": "Cohort A", "group_ordinal": 0,
					"members": []any{
						map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-001"}, "payload": map[string]any{"id": "dev-patient-001"}},
						map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-002"}, "payload": map[string]any{"id": "dev-patient-002"}},
					},
					"__loom_row_id":                map[string]any{"group_revision_id": case008RevisionID, "group_id": "group-a"},
					"__loom_root_contributor_keys": []string{"patient-storage-key-001", "patient-storage-key-002"},
				})
			}
			for _, row := range []map[string]any{
				{"id": "dev-patient-001", "col_09f81323c7d363cb25381a05": "dev-patient-001", "__loom_row_id": "patient-row-001"},
				{"id": "dev-patient-002", "col_09f81323c7d363cb25381a05": "dev-patient-002", "__loom_row_id": "patient-row-002"},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	groupResolver := case008ExplicitGroupResolver{}
	var basePreviewSummary, candidatePreviewSummary dataframeexecution.PreviewSummary
	var candidateReceipt *explorer.CompilationReceipt
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			receipt, err := compileExplorerReceipt(ctx, request, nil, engine, service, nil, nil)
			if err == nil && request.RequestID == "row-definition-proposal-candidate" {
				candidateReceipt = receipt
			}
			return receipt, err
		},
		ExplicitGroupResolver: groupResolver,
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			var capturedRows *[]map[string]any
			if receipt.RowDefinitionProposal != nil {
				capturedRows = &candidatePreviewRows
			} else {
				capturedRows = &basePreviewRows
			}
			summary, err := engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, func(row map[string]any) error {
				*capturedRows = append(*capturedRows, row)
				return visit(row)
			})
			if receipt.RowDefinitionProposal != nil {
				candidatePreviewSummary = summary
				candidatePreviewColumns = append([]string(nil), summary.Columns...)
			} else {
				basePreviewSummary = summary
				basePreviewColumns = append([]string(nil), summary.Columns...)
			}
			return summary, err
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	body := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":2,"expectedDraftDigest":%q,"outputId":%q,"selection":{"kind":"EXPLICIT_GROUP","explicitGroup":{"revisionId":%q,"unassignedMemberPolicy":"ERROR"}}}`,
		snapshot.Token, digest, case008OutputID, case008RevisionID)
	proposal := requestJSON(t, app, http.MethodPost,
		"/api/v1/projects/"+case008Project+"/explorers/"+case008ExplorerID+"/authoring/v2/row-definition-proposals", body)
	if proposal.StatusCode != http.StatusOK {
		t.Fatalf("retained CASE-008 row-definition proposal status=%d body=%s queryCalls=%d", proposal.StatusCode, proposal.Body, queryCalls)
	}
	if candidateReceipt == nil || candidateReceipt.RowDefinitionProposal == nil || candidateReceipt.RowDefinitionProposal.OutputID != case008OutputID {
		t.Fatalf("production candidate receipt was not produced/bound before preview: %#v", candidateReceipt)
	}
	var proposalResponse struct {
		ProposalID string `json:"proposalId"`
		OutputID   string `json:"outputId"`
		Mode       string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(proposal.Body), &proposalResponse); err != nil {
		t.Fatalf("decode successful proposal response: %v; body=%s", err, proposal.Body)
	}
	if proposalResponse.ProposalID != candidateReceipt.ID || proposalResponse.OutputID != case008OutputID || proposalResponse.Mode != "EXPLICIT_GROUP" {
		t.Fatalf("proposal response=%+v, candidate receipt=%s output=%s", proposalResponse, candidateReceipt.ID, case008OutputID)
	}
	if queryCalls != 2 || basePreviewSummary.RowCount != 2 || candidatePreviewSummary.RowCount != 1 {
		t.Fatalf("preview counts base=%d candidate=%d queryCalls=%d; want 2, 1, 2; body=%s", basePreviewSummary.RowCount, candidatePreviewSummary.RowCount, queryCalls, proposal.Body)
	}
	wantCandidateRows := []map[string]any{{
		"group_id":      "group-a",
		"group_label":   "Cohort A",
		"group_ordinal": 0,
		"members": []any{
			map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-001"}, "payload": map[string]any{"id": "dev-patient-001"}},
			map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-002"}, "payload": map[string]any{"id": "dev-patient-002"}},
		},
		"__loom_row_id": map[string]any{"group_revision_id": case008RevisionID, "group_id": "group-a"},
	}}
	if !reflect.DeepEqual(candidatePreviewRows, wantCandidateRows) {
		t.Fatalf("candidate preview rows=%#v, want literal group row with the two retained Patient IDs", candidatePreviewRows)
	}
	if !reflect.DeepEqual(basePreviewColumns, []string{"col_09f81323c7d363cb25381a05"}) || !reflect.DeepEqual(candidatePreviewColumns, []string{"group_id", "group_label", "group_ordinal", "members"}) {
		t.Fatalf("preview columns base=%v candidate=%v", basePreviewColumns, candidatePreviewColumns)
	}
	t.Logf("candidate receipt=%s output=%s basePreview=%#v candidatePreview=%#v", candidateReceipt.ID, case008OutputID, basePreviewRows, candidatePreviewRows)
}

type case008ExplicitGroupResolver struct{}

func (case008ExplicitGroupResolver) ListExplicitGroupRevisions(context.Context, lifecycle.ExplicitGroupRevisionListRequest) ([]lifecycle.ExplicitGroupRevisionChoice, error) {
	return nil, nil
}

func (case008ExplicitGroupResolver) ResolveExplicitGroupRevision(_ context.Context, request lifecycle.ExplicitGroupRevisionResolveRequest) (lifecycle.ExplicitGroupRevisionProof, error) {
	return lifecycle.ExplicitGroupRevisionProof{
		RevisionID: request.RevisionID, Project: request.Project, SourceGeneration: request.Snapshot.Identity.Generation,
		AuthorizationScopeDigest: request.Snapshot.Identity.AuthorizationScopeDigest, RootResourceType: request.RootResourceType,
		SelectionRevisionID: "selection_case008", SelectionMembershipDigest: "sha256:case008-selection-membership",
		DefinitionDigest: "sha256:case008-definition", MembershipDigest: "sha256:case008-membership", Complete: true,
	}, nil
}

func (case008ExplicitGroupResolver) ValidateCompilationReceipt(_ context.Context, proof lifecycle.ExplicitGroupRevisionProof, receipt *explorer.CompilationReceipt) error {
	workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return err
	}
	for _, document := range workspace.Documents {
		if document.Output.ID == case008OutputID && document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == authoringv2.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil &&
			document.Rows.Groups.Source.Explicit.RevisionID == proof.RevisionID &&
			document.Rows.Groups.Source.Explicit.UnassignedMemberPolicy == authoringv2.UnassignedMemberError {
			return nil
		}
	}
	return fmt.Errorf("receipt does not carry exact explicit group revision and ERROR policy")
}
