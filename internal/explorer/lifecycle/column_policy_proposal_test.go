package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func columnPolicyService(t *testing.T) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	service, store, snapshot, workspace := rowProposalService(t)
	workspace.Documents[0].Columns[0].LogicalType = "string"
	other := workspace.Documents[0].Columns[0]
	other.ColumnID = "other-column"
	other.Column = "other_id"
	other.Label = "Other ID"
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, other)
	addRowProposalSourceFilter(t, store, workspace)
	service.config.Capability.Catalog = func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := lifecycleInterpretationCatalog(snapshot, explorerID)
		catalog.Candidates[0].ProjectionModes = []string{"VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT"}
		return catalog
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		prepared, err := authoringv2.PrepareWorkspaceForCompilation(request.Workspace, service.config.Capability.Catalog(snapshot, request.ExplorerID))
		if err != nil {
			return nil, err
		}
		receipt := nativeReceipt(snapshot)
		receipt.IntentDigest, err = prepared.Digest()
		if err != nil {
			return nil, err
		}
		receipt.NormalizedBundle, err = prepared.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		receipt.ColumnPolicyProposal = cloneColumnPolicyProposalBinding(request.ColumnPolicyProposal)
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	enableRowDefinitionPreview(service)
	return service, store, snapshot
}

func columnPolicyRequest(store *fakeStore, snapshot capability.Snapshot) ColumnPolicyProposalRequest {
	return ColumnPolicyProposalRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest, OutputID: "patients", ColumnID: "row_proposal_source_0", ProjectionMode: "FIRST"}
}
func columnPolicyApply(store *fakeStore, snapshot capability.Snapshot, id, commandID string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyColumnPolicyProposal, OutputID: "patients", ProposalID: id}}}
}

func TestColumnPolicyProposalPreviewCancelApplyReloadAndStale(t *testing.T) {
	service, store, snapshot := columnPolicyService(t)
	before := append([]byte(nil), store.created.DraftConfig...)
	old, err := authoringv2.DecodeWorkspace(before)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := service.ProposeColumnPolicy(context.Background(), columnPolicyRequest(store, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateReceiptID != proposal.ProposalID || proposal.PreviewStatus != "READY" || proposal.SampledRows != 1 || proposal.Sampled {
		t.Fatalf("candidate not preview ready: %#v", proposal)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatal("Preview/Cancel wrote the saved draft")
	}
	document := proposal.CandidateDocument
	target := columnPolicyColumn(t, document, "row_proposal_source_0")
	other := columnPolicyColumn(t, document, "other-column")
	if len(document.Columns) != 2 || target.Source.Field.ProjectionMode != "FIRST" || other.Source.Field.ProjectionMode != "VALUE" || !reflect.DeepEqual(document.Construction, old.Documents[0].Construction) {
		want, _ := json.Marshal(old.Documents[0].Construction)
		got, _ := json.Marshal(document.Construction)
		t.Fatalf("policy target=%s other=%s changed downstream operation: got=%s want=%s", target.Source.Field.ProjectionMode, other.Source.Field.ProjectionMode, got, want)
	}
	bound := false
	for _, binding := range proposal.CandidateNetwork.Bindings {
		if binding.ColumnID == target.ColumnID && binding.ValueMode == "FIRST" {
			bound = true
		}
	}
	if !bound || proposal.CandidateRowIdentity.Stages[0].Identity.Kind != "RECORD" {
		t.Fatal("candidate network/row identity diverged")
	}
	apply := columnPolicyApply(store, snapshot, proposal.ProposalID, "apply-column-policy")
	applied, err := service.ApplyCommands(context.Background(), "project-a", "patients", apply, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if applied.DraftVersion != apply.ExpectedDraftVersion+1 || store.saveDraftCalls != 1 || !reflect.DeepEqual(applied.Workspace.Documents[0], document) {
		t.Fatal("Apply saved a different candidate or revision")
	}
	reloaded, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Documents[0], document) {
		t.Fatal("reload lost policy or exact plan")
	}
	saved := append([]byte(nil), store.created.DraftConfig...)
	apply.CommandID = "second-editor-old-policy"
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", apply, "bob"); err == nil {
		t.Fatal("stale policy overwrote saved draft")
	}
	if store.saveDraftCalls != 1 || !bytes.Equal(saved, store.created.DraftConfig) {
		t.Fatal("stale Apply changed saved draft")
	}
}

func TestColumnPolicyProposalRejectsCompilePreviewAndCatalogFailuresWithoutSaving(t *testing.T) {
	for _, failure := range []string{"compile", "preview", "catalog", "wrong-column", "non-field", "imported"} {
		t.Run(failure, func(t *testing.T) {
			service, store, snapshot := columnPolicyService(t)
			request := columnPolicyRequest(store, snapshot)
			switch failure {
			case "compile":
				service.config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
					return nil, errors.New("downstream schema is incompatible")
				}
			case "preview":
				service.config.PreviewReceipt = nil
			case "catalog":
				original := service.config.Capability.Catalog
				service.config.Capability.Catalog = func(s capability.Snapshot, id string) authoringv2.CatalogSnapshot {
					c := original(s, id)
					c.Candidates[0].ProjectionModes = []string{"VALUE"}
					return c
				}
			case "wrong-column":
				request.ColumnID = "missing"
			case "non-field", "imported":
				workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "non-field" {
					columnPolicyColumn(t, workspace.Documents[0], request.ColumnID).Source = authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}
				} else {
					workspace.Documents[0].Columns = nil
					workspace.Documents[0].Construction = nil
					workspace.Documents[0].ImportedRecipe = &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
				}
				store.created.DraftConfig, _ = workspace.CanonicalJSON()
				store.created.DraftDigest, _ = workspace.Digest()
				request.ExpectedDraftDigest = store.created.DraftDigest
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			proposal, err := service.ProposeColumnPolicy(context.Background(), request)
			if err == nil || proposal.ProposalID != "" {
				t.Fatal("invalid candidate was made applicable")
			}
			if failure == "imported" && lifecycleErrorCode(err) != "COLUMN_POLICY_IMPORTED_SOURCE_REQUIRED" {
				t.Fatalf("wrong imported boundary: %v", err)
			}
			if (failure == "catalog" || failure == "wrong-column" || failure == "non-field") && lifecycleErrorCode(err) != "INVALID_COLUMN_POLICY" {
				t.Fatalf("wrong source policy rejection: %v", err)
			}

			if store.saveDraftCalls != 0 || !bytes.Equal(before, store.created.DraftConfig) {
				t.Fatal("failed proposal mutated saved state")
			}
		})
	}
}

func TestColumnPolicyApplyRejectsRuntimeFailureAndReceiptTampering(t *testing.T) {
	for _, failure := range []string{"runtime", "binding", "ordinary-receipt", "unrelated-candidate"} {
		t.Run(failure, func(t *testing.T) {
			service, store, snapshot := columnPolicyService(t)
			_, err := service.ProposeColumnPolicy(context.Background(), columnPolicyRequest(store, snapshot))
			if err != nil {
				t.Fatal(err)
			}
			receipt := store.receipt
			switch failure {
			case "runtime":
				service.config.PreviewReceipt = nil
			case "binding":
				receipt.ColumnPolicyProposal.ColumnID = "other-column"
			case "ordinary-receipt":
				receipt.ColumnPolicyProposal = nil
			case "unrelated-candidate":
				workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
				if err != nil {
					t.Fatal(err)
				}
				workspace.Documents[0].Output.Title = "unreviewed rename"
				receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
				receipt.IntentDigest, _ = workspace.Digest()
				receipt.ColumnPolicyProposal.CandidateWorkspaceDigest = receipt.IntentDigest
			}
			receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
			receipt.ID, _ = explorer.ReceiptID(*receipt)
			request := columnPolicyApply(store, snapshot, receipt.ID, "reject-policy")
			before := append([]byte(nil), store.created.DraftConfig...)
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
				t.Fatal("unpreviewed or invalid receipt saved")
			}
			if store.saveDraftCalls != 0 || !bytes.Equal(before, store.created.DraftConfig) {
				t.Fatal("rejected Apply changed saved state")
			}
		})
	}
}

func TestColumnPolicyWireRejectsCandidateInjectionAndInvalidLimit(t *testing.T) {
	base := `{"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","outputId":"out","columnId":"column","projectionMode":"ALL"}`
	for _, extra := range []string{`,"project":"injected"`, `,"candidateDocument":{}`, `,"candidateNetwork":{}`, `,"candidateWorkspace":{}`, `,"source":{"kind":"FIELD"}`, `,"limit":null`, `,"limit":0`, `,"limit":-1`} {
		var request ColumnPolicyProposalRequest
		if err := json.Unmarshal([]byte(strings.TrimSuffix(base, "}")+extra+"}"), &request); err == nil {
			t.Fatalf("accepted injection %s", extra)
		}
	}
	for _, extra := range []string{`,"column":"other"`, `,"columnId":"other"`, `,"projectionMode":"FIRST"`, `,"source":{}`, `,"title":"rename"`} {
		raw := `{"type":"APPLY_COLUMN_POLICY_PROPOSAL","outputId":"out","proposalId":"receipt"` + extra + `}`
		var command authoringv2.Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("Apply accepted non-receipt facts: %s", raw)
		}
	}
}

func columnPolicyColumn(t *testing.T, document authoringv2.Document, id string) *authoringv2.Column {
	t.Helper()
	for i := range document.Columns {
		if document.Columns[i].ColumnID == id {
			return &document.Columns[i]
		}
	}
	t.Fatalf("missing fixture column %s", id)
	return nil
}
