package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestConstructionProposalOperationHistoryTraces(t *testing.T) {
	phase, tracePath := os.Getenv("BUILDER_OPERATION_PHASE"), os.Getenv("BUILDER_OPERATION_TRACES")
	if phase == "" || tracePath == "" {
		t.Skip("set BUILDER_OPERATION_PHASE and BUILDER_OPERATION_TRACES to run Builder operation-history verification")
	}
	var traces operationHistoryTraceFile
	readOperationHistoryJSON(t, tracePath, &traces)
	if traces.SchemaVersion != 1 {
		t.Fatalf("operation-history trace schema version %d, want 1", traces.SchemaVersion)
	}
	if phase == "prepare" {
		seedPath, identityPath := os.Getenv("BUILDER_OPERATION_SEEDS"), os.Getenv("BUILDER_OPERATION_SEED_IDENTITIES")
		if seedPath == "" || identityPath == "" {
			t.Fatal("prepare phase requires BUILDER_OPERATION_SEEDS and BUILDER_OPERATION_SEED_IDENTITIES")
		}
		var seeds operationHistorySeedFile
		readOperationHistoryJSON(t, seedPath, &seeds)
		prepareOperationHistorySeeds(t, traces, seeds, identityPath)
		return
	}
	if phase != "full" {
		t.Fatalf("unsupported BUILDER_OPERATION_PHASE %q; want prepare or full", phase)
	}
	candidatePath, identityPath, responsePath := os.Getenv("BUILDER_OPERATION_CANDIDATES"), os.Getenv("BUILDER_OPERATION_SEED_IDENTITIES"), os.Getenv("BUILDER_OPERATION_RESPONSES")
	if candidatePath == "" || identityPath == "" || responsePath == "" {
		t.Fatal("full phase requires BUILDER_OPERATION_CANDIDATES, BUILDER_OPERATION_SEED_IDENTITIES and BUILDER_OPERATION_RESPONSES")
	}
	var candidates operationHistoryCandidateFile
	readOperationHistoryJSON(t, candidatePath, &candidates)
	var identities operationHistorySeedIdentityFile
	readOperationHistoryJSON(t, identityPath, &identities)
	if candidates.SchemaVersion != 1 || identities.SchemaVersion != 1 {
		t.Fatalf("operation-history candidate/identity schema versions %d/%d, want 1/1", candidates.SchemaVersion, identities.SchemaVersion)
	}
	focusID := os.Getenv("BUILDER_OPERATION_CASE_ID")
	if focusID != "" {
		filteredTraces := make([]operationHistoryTrace, 0, 1)
		for _, trace := range traces.Cases {
			if trace.ID == focusID {
				filteredTraces = append(filteredTraces, trace)
			}
		}
		filteredCandidates := make([]operationHistoryCandidate, 0, 1)
		for _, candidate := range candidates.Cases {
			id := candidate.TraceID
			if id == "" {
				id = candidate.ID
			}
			if id == focusID {
				filteredCandidates = append(filteredCandidates, candidate)
			}
		}
		if len(filteredTraces) != 1 || len(filteredCandidates) != 1 {
			t.Fatalf("focused trace %q found model/frontend %d/%d times", focusID, len(filteredTraces), len(filteredCandidates))
		}
		traces.Cases, candidates.Cases = filteredTraces, filteredCandidates
		filteredIdentities := make([]operationHistorySeedIdentity, 0, 1)
		for _, identity := range identities.Cases {
			if identity.ID == focusID {
				filteredIdentities = append(filteredIdentities, identity)
			}
		}
		identities.Cases = filteredIdentities
	}
	if len(traces.Cases) == 0 || len(candidates.Cases) != len(traces.Cases) {
		t.Fatalf("operation-history trace cases=%d frontend cases=%d; require a nonempty exact case set", len(traces.Cases), len(candidates.Cases))
	}
	traceByID := map[string]operationHistoryTrace{}
	for _, trace := range traces.Cases {
		if trace.ID == "" {
			t.Fatal("model trace has empty id")
		}
		if _, exists := traceByID[trace.ID]; exists {
			t.Fatalf("duplicate model trace id %q", trace.ID)
		}
		traceByID[trace.ID] = trace
	}
	candidateByID := map[string]operationHistoryCandidate{}
	for _, candidate := range candidates.Cases {
		id := candidate.TraceID
		if id == "" {
			id = candidate.ID
		}
		if id == "" {
			t.Fatal("frontend trace has neither traceId nor id")
		}
		if _, exists := candidateByID[id]; exists {
			t.Fatalf("duplicate frontend trace id %q", id)
		}
		candidateByID[id] = candidate
	}
	if len(traceByID) != len(candidateByID) {
		t.Fatalf("frontend trace IDs %d != model trace IDs %d", len(candidateByID), len(traceByID))
	}
	identityByID := map[string]operationHistorySeedIdentity{}
	for _, identity := range identities.Cases {
		if identity.ID == "" {
			t.Fatal("seed identity has empty id")
		}
		if _, exists := identityByID[identity.ID]; exists {
			t.Fatalf("duplicate seed identity for %q", identity.ID)
		}
		identityByID[identity.ID] = identity
	}
	if len(identityByID) != len(traceByID) {
		t.Fatalf("seed identity IDs %d != trace IDs %d", len(identityByID), len(traceByID))
	}
	for id := range traceByID {
		if _, ok := identityByID[id]; !ok {
			t.Fatalf("seed identity missing trace %q", id)
		}
	}
	for id := range identityByID {
		if _, ok := traceByID[id]; !ok {
			t.Fatalf("seed identity has unknown trace %q", id)
		}
	}
	traceIDs := make([]string, 0, len(traceByID))
	for id := range traceByID {
		traceIDs = append(traceIDs, id)
	}
	sort.Strings(traceIDs)
	for id := range candidateByID {
		if _, exists := traceByID[id]; !exists {
			t.Fatalf("frontend artifact has unknown trace id %q", id)
		}
	}

	summary := operationHistoryReplaySummary{SchemaVersion: 1, TraceCaseCount: len(traces.Cases), CandidateCaseCount: len(candidates.Cases), Reloads: []operationHistoryReloadSummary{}}
	reloads := operationHistoryReloadFile{SchemaVersion: 1, Cases: []operationHistoryReloadCase{}}
	uniqueFingerprints := map[string]bool{}
	uniqueRawFingerprints := map[string]bool{}
	actualResponses := operationHistoryProposalResponseFile{SchemaVersion: 1, Cases: make([]operationHistoryProposalResponseCase, 0, len(traceIDs))}
	for _, traceID := range traceIDs {
		trace, candidate := traceByID[traceID], candidateByID[traceID]
		seedIdentity := identityByID[traceID]
		aggregateOutputID := traceAggregateOutputID(t, trace)
		expectations := map[int]operationHistoryExpectation{}
		proposeActions := map[int]bool{}
		for _, e := range trace.Expectations {
			if _, exists := expectations[e.ActionIndex]; exists {
				t.Fatalf("trace %s has duplicate expectation at action %d", traceID, e.ActionIndex)
			}
			expectations[e.ActionIndex] = e
		}
		for i, action := range trace.Actions {
			switch action.Kind {
			case "editAggregate", "editKey", "restoreKey", "addDependent", "removeDependent", "propose", "cancel", "apply", "applyAck":
			default:
				t.Fatalf("trace %s action %d has unsupported kind %q", traceID, i, action.Kind)
			}
			if action.Kind == "propose" {
				proposeActions[i] = true
				e, ok := expectations[i]
				if !ok || e.RequestFingerprint == "" {
					t.Fatalf("trace %s proposal action %d lacks model request fingerprint", traceID, i)
				}
				uniqueFingerprints[e.RequestFingerprint] = true
			}
		}
		checkpoints := map[int]operationHistoryCandidateCheckpoint{}
		for _, cp := range candidate.Checkpoints {
			if _, exists := checkpoints[cp.ActionIndex]; exists {
				t.Fatalf("trace %s has duplicate frontend checkpoint %d", traceID, cp.ActionIndex)
			}
			checkpoints[cp.ActionIndex] = cp
		}
		if len(checkpoints) != len(proposeActions) {
			t.Fatalf("trace %s proposal actions=%d frontend checkpoints=%d", traceID, len(proposeActions), len(checkpoints))
		}
		for i := range proposeActions {
			if _, exists := checkpoints[i]; !exists {
				t.Fatalf("trace %s missing frontend checkpoint for action %d", traceID, i)
			}
		}
		for i := range checkpoints {
			if !proposeActions[i] {
				t.Fatalf("trace %s has frontend checkpoint for non-propose action %d", traceID, i)
			}
		}
		if len(candidate.Checkpoints) == 0 {
			t.Fatalf("trace %s has no proposal checkpoint", traceID)
		}

		workspace := decodeTraceWorkspace(t, traceID, candidate.Checkpoints[0].AcceptedWorkspace)
		initialRequest := decodeTraceProposalRequest(t, traceID, candidate.Checkpoints[0].ProposalRequest)
		assertTraceWorkspaceMatchesRequest(t, traceID, workspace, initialRequest)
		assertTraceSeedIdentity(t, traceID, seedIdentity, workspace, initialRequest)
		assertTraceProposalContext(t, traceID, trace, candidate.Checkpoints[0].ProposalRequest, initialRequest)
		service, store, snapshot, currentWorkspace, stats := newOperationHistoryService(t, workspace, initialRequest)
		lastProposal := construction.ConstructionProposalResponse{}
		var lastPreviewResult *operationHistoryPreviewResult
		ready, previewed, awaitingAck, canceled := false, false, false, false
		replay := operationHistoryReplayCase{ID: traceID, ProposalCheckpoints: []operationHistoryReplayProposal{}, ApplyAck: []operationHistoryReplayAck{}}
		responseCase := operationHistoryProposalResponseCase{ID: traceID, Checkpoints: []operationHistoryProposalResponseEntry{}}
		for actionIndex, action := range trace.Actions {
			expectation, hasExpectation := expectations[actionIndex]
			switch action.Kind {
			case "propose":
				lastPreviewResult = nil
				cp := checkpoints[actionIndex]
				req := decodeTraceProposalRequest(t, traceID, cp.ProposalRequest)
				accepted := decodeTraceWorkspace(t, traceID, cp.AcceptedWorkspace)
				assertTraceWorkspaceMatchesRequest(t, traceID, accepted, req)
				assertTraceSeedIdentity(t, traceID, seedIdentity, accepted, req)
				assertTraceProposalContext(t, traceID, trace, cp.ProposalRequest, req)
				currentDigest, err := currentWorkspace.Digest()
				if err != nil || currentDigest != req.ExpectedDraftDigest || store.created.DraftDigest != req.ExpectedDraftDigest || store.created.DraftVersion != req.ExpectedDraftVersion {
					t.Fatalf("trace %s action %d request base differs from accepted state: digest=%q/%q version=%d/%d err=%v", traceID, actionIndex, req.ExpectedDraftDigest, currentDigest, req.ExpectedDraftVersion, store.created.DraftVersion, err)
				}
				captured := constructionFromRaw(t, traceID, actionIndex, cp.CandidateConstruction)
				if !reflect.DeepEqual(req.CandidateConstruction, captured) || req.ChangedStepID != cp.ChangedStepID || !slices.Equal(req.RemoveStepIDs, cp.RemoveStepIDs) {
					t.Fatalf("trace %s action %d full proposalRequest differs from frontend captured candidate", traceID, actionIndex)
				}
				for stepID, output := range expectation.DownstreamOutputTypes {
					if candidateType := traceStepColumnType(captured, stepID, output.ColumnID); candidateType != "" {
						t.Fatalf("trace %s action %d frontend candidate declares inferred output %s.%s type %q", traceID, actionIndex, stepID, output.ColumnID, candidateType)
					}
				}
				if cp.RequestFingerprint != "" && cp.RequestFingerprint != expectation.RequestFingerprint {
					t.Fatalf("trace %s action %d frontend/model fingerprint mismatch", traceID, actionIndex)
				}
				uniqueRawFingerprints[traceProposalRequestFingerprint(t, traceID, cp.ProposalRequest)] = true
				if !hasExpectation || cp.Expected.ProposalStatus != "" && !sameOperationHistoryExpectation(cp.Expected, expectation) {
					t.Fatalf("trace %s action %d frontend expectation differs from model", traceID, actionIndex)
				}
				if len(cp.AcceptedDocument) > 0 {
					var doc ws.Document
					if err := json.Unmarshal(cp.AcceptedDocument, &doc); err != nil {
						t.Fatalf("trace %s action %d decode acceptedDocument: %v", traceID, actionIndex, err)
					}
					if current := accepted.FindOutputDocument(req.OutputID); current == nil || !reflect.DeepEqual(doc, *current) {
						t.Fatalf("trace %s action %d acceptedDocument differs from acceptedWorkspace", traceID, actionIndex)
					}
				}
				baseCompiles, candidateCompiles := stats.base, stats.candidate
				before := append([]byte(nil), store.created.DraftConfig...)
				beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
				baseDoc := accepted.FindOutputDocument(req.OutputID)
				if baseDoc == nil || baseDoc.Construction == nil {
					t.Fatalf("trace %s action %d accepted document has no construction", traceID, actionIndex)
				}
				if _, _, err := baseDoc.AnalyzeConstructionCandidate(req.CandidateConstruction, req.ChangedStepID, req.RemoveStepIDs); err != nil {
					t.Fatalf("trace %s action %d actual Document.AnalyzeConstructionCandidate: %v", traceID, actionIndex, err)
				}
				lastProposal, err = service.OwnerServices().Construction.ProposeConstruction(context.Background(), req)
				if err != nil {
					t.Fatalf("trace %s action %d actual ProposeConstruction: %v", traceID, actionIndex, err)
				}
				if stats.base != baseCompiles+1 {
					t.Fatalf("trace %s action %d base compiler calls=%d, want one before proposal", traceID, actionIndex, stats.base-baseCompiles)
				}
				if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, before) {
					firstDiff, beforeByte, afterByte := firstDifferentByte(before, store.created.DraftConfig)
					t.Fatalf("trace %s action %d proposal changed accepted state: saves %d version %d→%d digest %q→%q lengths=%d/%d firstDiff=%d:%d/%d bytesEqual=%t", traceID, actionIndex,
						store.saveDraftCalls, beforeVersion, store.created.DraftVersion, beforeDigest, store.created.DraftDigest, len(before), len(store.created.DraftConfig), firstDiff, beforeByte, afterByte, bytes.Equal(store.created.DraftConfig, before))
				}
				switch expectation.ProposalStatus {
				case "READY":
					if lastProposal.PreviewStatus != "PREVIEW_PENDING" || lastProposal.ProposalID == "" || stats.candidate != candidateCompiles+1 || store.receipt == nil || store.receipt.ID != lastProposal.ProposalID {
						t.Fatalf("trace %s action %d READY model got status/id=%s/%s candidate compile delta=%d", traceID, actionIndex, lastProposal.PreviewStatus, lastProposal.ProposalID, stats.candidate-candidateCompiles)
					}
					summary.ReadyProposalCount++
					ready = true
					preview, previewErr := service.OwnerServices().Core.Preview(context.Background(), authoringcore.PreviewRequest{
						Project: req.Project, ExplorerID: req.ExplorerID, ReceiptID: lastProposal.ProposalID, OutputID: req.OutputID, Limit: req.Limit,
						SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
							return func(map[string]any) error { return nil }, nil
						},
					})
					if previewErr != nil || preview.Receipt == nil || preview.Receipt.ID != lastProposal.ProposalID || preview.Summary.Output != req.OutputID {
						t.Fatalf("trace %s action %d actual proposal preview receipt/output=%v/%q err=%v", traceID, actionIndex, preview.Receipt, preview.Summary.Output, previewErr)
					}
					if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, before) {
						t.Fatalf("trace %s action %d preview changed accepted state", traceID, actionIndex)
					}
					wantColumns := emittedPublicColumnsForOutput(preview.Receipt, req.OutputID)
					if !reflect.DeepEqual(preview.Summary.Columns, wantColumns) {
						t.Fatalf("trace %s action %d preview summary columns %v != compiled output %v", traceID, actionIndex, preview.Summary.Columns, wantColumns)
					}
					lastPreviewResult = &operationHistoryPreviewResult{
						ReceiptID: preview.Receipt.ID, OutputID: req.OutputID,
						Columns: append([]viewer.EmittedColumn(nil), preview.Columns...), Summary: preview.Summary,
					}
					summary.ServicePreviewCount++
					previewed = true
				case "NEEDS_REPAIR":
					if lastProposal.PreviewStatus != "NEEDS_REPAIR" || lastProposal.ProposalID != "" || stats.candidate != candidateCompiles {
						t.Fatalf("trace %s action %d NEEDS_REPAIR model got status/id=%s/%s candidate compile delta=%d", traceID, actionIndex, lastProposal.PreviewStatus, lastProposal.ProposalID, stats.candidate-candidateCompiles)
					}
					summary.NeedsRepairProposalCount++
					ready = false
					previewed = false
				default:
					t.Fatalf("trace %s action %d unsupported proposal expectation %q", traceID, actionIndex, expectation.ProposalStatus)
				}
				assertOperationHistoryProposal(t, traceID, actionIndex, aggregateOutputID, expectation, *baseDoc.Construction, lastProposal, store.receipt, req.OutputID)
				requestID := traceProposalRequestID(t, traceID, cp.ProposalRequest)
				responseCase.Checkpoints = append(responseCase.Checkpoints, operationHistoryProposalResponseEntry{
					ActionIndex: actionIndex, RequestID: requestID, RequestFingerprint: expectation.RequestFingerprint, ProposalResponse: lastProposal, PreviewResult: lastPreviewResult,
				})
				replay.ProposalCheckpoints = append(replay.ProposalCheckpoints, operationHistoryReplayProposal{
					ActionIndex: actionIndex, CandidateActionIndex: expectation.CandidateActionIndex, RequestFingerprint: expectation.RequestFingerprint,
					RequestID: requestID,
					Status:    expectation.ProposalStatus, ProposalIDPresent: lastProposal.ProposalID != "",
					StepIDs:               constructionStepIDs(lastProposal.CandidateConstruction.Steps),
					RemovedStepIDs:        append([]string(nil), lastProposal.DependencyImpact.RemovedStepIDs...),
					GroupOutputTypes:      actualTraceGroupOutputTypes(lastProposal.CandidateConstruction),
					DownstreamInputTypes:  actualTraceDownstreamInputTypes(lastProposal.CandidateConstruction, expectation),
					DownstreamOutputTypes: actualTraceDownstreamOutputTypes(store.receipt, req.OutputID, expectation),
					MissingInputCount:     len(lastProposal.DependencyImpact.MissingInputs),
				})
				summary.ProposalCheckpointCount++
				currentWorkspace = workspaceFromDraft(t, traceID, store.created.DraftConfig)
			case "apply":
				if !hasExpectation || !ready || !previewed || lastPreviewResult == nil || lastPreviewResult.ReceiptID != lastProposal.ProposalID || lastProposal.ProposalID == "" || expectation.ProposalStatus != "APPLYING" || expectation.AcceptedChangesOnApply {
					t.Fatalf("trace %s action %d apply lacks READY proposal or expects early save", traceID, actionIndex)
				}
				previewed, awaitingAck = true, true
			case "applyAck":
				if !hasExpectation || !awaitingAck || !previewed || !ready || action.Result != "success" {
					t.Fatalf("trace %s action %d unsupported/non-ready apply ack", traceID, actionIndex)
				}
				version, digest := store.created.DraftVersion, store.created.DraftDigest
				reloadService := newTestService(t, store, service.config)
				beforeReload, reloadErr := reloadService.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID})
				if reloadErr != nil {
					t.Fatalf("fresh Builder before Apply: %v", reloadErr)
				}
				beforeCanonical, reloadErr := beforeReload.Workspace.CanonicalJSON()
				if reloadErr != nil || !bytes.Equal(beforeCanonical, store.created.DraftConfig) || beforeReload.DraftVersion != version || beforeReload.DraftDigest != digest {
					t.Fatalf("fresh Builder changed initial saved Group: err=%v", reloadErr)
				}
				applied, err := service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
					CommandID: "operation-history-" + traceID + "-" + fmt.Sprint(actionIndex), SemanticsVersion: ws.CurrentSemanticsVersion,
					SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
					Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: lastProposal.OutputID, Payload: &authoringv2.ProposalPayload{ProposalID: lastProposal.ProposalID}}},
				}, "operation-history-test")
				if err != nil || applied == nil {
					t.Fatalf("trace %s action %d actual apply ack response=%#v err=%v", traceID, actionIndex, applied, err)
				}
				if expectation.ProposalStatus != "APPLIED" || expectation.ApplyAck != "SUCCESS" || !expectation.AcceptedChangesOnApply {
					t.Fatalf("trace %s action %d model ack is not APPLIED/SUCCESS", traceID, actionIndex)
				}
				if store.created.DraftVersion != version+1 || store.created.DraftDigest == digest || store.created.DraftDigest != lastProposal.CandidateWorkspaceDigest || store.saveDraftCalls != 1 {
					t.Fatalf("trace %s action %d accepted ack version/digest=%d/%q proposal=%q", traceID, actionIndex, store.created.DraftVersion, store.created.DraftDigest, lastProposal.CandidateWorkspaceDigest)
				}
				persistedWorkspace := workspaceFromDraft(t, traceID, store.created.DraftConfig)
				persistedCanonical, err := persistedWorkspace.CanonicalJSON()
				if err != nil {
					t.Fatalf("trace %s action %d canonicalize persisted workspace: %v", traceID, actionIndex, err)
				}
				appliedCanonical, err := applied.Workspace.CanonicalJSON()
				if err != nil {
					t.Fatalf("trace %s action %d canonicalize Apply reply workspace: %v", traceID, actionIndex, err)
				}
				persistedDigest, err := persistedWorkspace.Digest()
				if err != nil || persistedDigest != store.created.DraftDigest || !bytes.Equal(persistedCanonical, appliedCanonical) || !bytes.Equal(store.created.DraftConfig, persistedCanonical) {
					t.Fatalf("trace %s action %d persisted canonical workspace differs from Apply reply: storedDigest=%q persistedDigest=%q err=%v", traceID, actionIndex, store.created.DraftDigest, persistedDigest, err)
				}
				appliedDoc := applied.Workspace.FindOutputDocument(lastProposal.OutputID)
				persistedDoc := persistedWorkspace.FindOutputDocument(lastProposal.OutputID)
				if appliedDoc == nil || appliedDoc.Construction == nil || persistedDoc == nil || persistedDoc.Construction == nil {
					t.Fatalf("trace %s action %d acknowledgement omitted the applied document construction", traceID, actionIndex)
				}
				appliedConstruction, _ := json.Marshal(appliedDoc.Construction)
				persistedConstruction, _ := json.Marshal(persistedDoc.Construction)
				proposalConstruction, _ := json.Marshal(lastProposal.CandidateConstruction)
				if !bytes.Equal(appliedConstruction, proposalConstruction) || !bytes.Equal(persistedConstruction, proposalConstruction) {
					appliedJSON, _ := json.Marshal(appliedDoc)
					persistedJSON, _ := json.Marshal(persistedDoc)
					proposalJSON, _ := json.Marshal(lastProposal.CandidateConstruction)
					t.Fatalf("trace %s action %d applied/persisted construction differs from exact proposal: applied=%s persisted=%s candidate=%s", traceID, actionIndex, appliedJSON, persistedJSON, proposalJSON)
				}
				reloadService = newTestService(t, store, service.config)
				afterReload, reloadErr := reloadService.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID})
				if reloadErr != nil {
					t.Fatalf("fresh Builder after Apply: %v", reloadErr)
				}
				readCanonical, reloadErr := afterReload.Workspace.CanonicalJSON()
				if reloadErr != nil || !bytes.Equal(readCanonical, persistedCanonical) || afterReload.DraftDigest != persistedDigest || afterReload.DraftVersion != version+1 {
					t.Fatalf("fresh Builder lost saved Group: err=%v", reloadErr)
				}
				reloadCaps, reloadErr := reloadService.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token, ExpectedDraftVersion: afterReload.DraftVersion, ExpectedDraftDigest: afterReload.DraftDigest, OutputID: lastProposal.OutputID, StageID: "source_projection"})
				if reloadErr != nil {
					t.Fatalf("fresh Group capabilities: %v", reloadErr)
				}
				reloads.Cases = append(reloads.Cases, operationHistoryReloadCase{ID: traceID, ActionIndex: actionIndex, OutputID: lastProposal.OutputID, StageID: "source_projection", Before: beforeReload, After: afterReload, StoredWorkspace: persistedWorkspace, Capabilities: reloadCaps})
				summary.Reloads = append(summary.Reloads, operationHistoryReloadSummary{ID: traceID, ActionIndex: actionIndex, DraftVersion: afterReload.DraftVersion, DraftDigest: afterReload.DraftDigest})
				replay.ApplyAck = append(replay.ApplyAck, operationHistoryReplayAck{ActionIndex: actionIndex, Result: "SUCCESS", AcceptedVersionBefore: version, AcceptedVersionAfter: store.created.DraftVersion, AcceptedDigestBefore: digest, AcceptedDigestAfter: store.created.DraftDigest})
				summary.ApplyInvocationCount++
				summary.ApplyAckSuccessCount++
				currentWorkspace = applied.Workspace
				awaitingAck = false
				previewed = false
			case "cancel":
				if !hasExpectation || expectation.ProposalStatus != "CANCELLED" || expectation.AcceptedChangesOnApply || awaitingAck {
					t.Fatalf("trace %s action %d cancel is not terminal/no-save", traceID, actionIndex)
				}
				if store.saveDraftCalls != 0 || store.created.DraftVersion != initialRequest.ExpectedDraftVersion || store.created.DraftDigest != initialRequest.ExpectedDraftDigest {
					t.Fatalf("trace %s action %d cancel changed accepted state", traceID, actionIndex)
				}
				canceled = true
			case "editAggregate", "editKey", "restoreKey", "addDependent", "removeDependent":
				if canceled || awaitingAck {
					t.Fatalf("trace %s action %d edit followed terminal cancel/apply", traceID, actionIndex)
				}
			}
		}
		summary.ReplayedCaseCount++
		summary.Cases = append(summary.Cases, replay)
		actualResponses.Cases = append(actualResponses.Cases, responseCase)
	}
	summary.UniqueRequestFingerprintCount = len(uniqueFingerprints)
	summary.UniqueAbstractRequestFingerprintCount = len(uniqueFingerprints)
	summary.UniqueRawRequestFingerprintCount = len(uniqueRawFingerprints)
	if summary.ReplayedCaseCount != summary.TraceCaseCount || summary.CandidateCaseCount != summary.TraceCaseCount || summary.SkippedCaseCount != 0 || summary.ProposalCheckpointCount == 0 {
		t.Fatalf("partial operation-history replay: %#v", summary)
	}
	if summary.ServicePreviewCount != summary.ReadyProposalCount {
		t.Fatalf("operation-history READY proposals=%d, actual Service.Preview calls=%d", summary.ReadyProposalCount, summary.ServicePreviewCount)
	}
	responseJSON, err := json.MarshalIndent(actualResponses, "", "  ")
	if err != nil {
		t.Fatalf("marshal actual proposal responses: %v", err)
	}
	if err := os.WriteFile(responsePath, responseJSON, 0o600); err != nil {
		t.Fatalf("write actual proposal responses %s: %v", responsePath, err)
	}
	if path := os.Getenv("BUILDER_OPERATION_RELOADS"); path != "" {
		encodedReloads, err := json.Marshal(reloads)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encodedReloads, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Printf("BUILDER_OPERATION_RESPONSES_PATH=%s CASES=%d CHECKPOINTS=%d\n", responsePath, len(actualResponses.Cases), summary.ProposalCheckpointCount)
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal replay summary: %v", err)
	}
	fmt.Printf("BUILDER_OPERATION_REPLAY_JSON %s\n", encoded)
}

func prepareOperationHistorySeeds(t *testing.T, traces operationHistoryTraceFile, seeds operationHistorySeedFile, identityPath string) {
	t.Helper()
	if seeds.SchemaVersion != 1 || len(seeds.Cases) != len(traces.Cases) {
		t.Fatalf("seed schema/case count=%d/%d; model has %d cases", seeds.SchemaVersion, len(seeds.Cases), len(traces.Cases))
	}
	traceIDs := make(map[string]bool, len(traces.Cases))
	for _, trace := range traces.Cases {
		if trace.ID == "" || traceIDs[trace.ID] {
			t.Fatalf("empty or duplicate model trace id %q", trace.ID)
		}
		traceIDs[trace.ID] = true
	}
	seedByID := make(map[string]operationHistorySeed, len(seeds.Cases))
	for _, seed := range seeds.Cases {
		if seed.ID == "" || !traceIDs[seed.ID] {
			t.Fatalf("seed has empty or unknown trace id %q", seed.ID)
		}
		if _, exists := seedByID[seed.ID]; exists {
			t.Fatalf("duplicate seed for trace %q", seed.ID)
		}
		seedByID[seed.ID] = seed
	}
	for id := range traceIDs {
		if _, exists := seedByID[id]; !exists {
			t.Fatalf("seed file missing trace %q", id)
		}
	}
	ids := make([]string, 0, len(seedByID))
	for id := range seedByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	identities := operationHistorySeedIdentityFile{SchemaVersion: 1, Cases: make([]operationHistorySeedIdentity, 0, len(ids))}
	failed := false
	for _, id := range ids {
		trace := traceByIDForID(t, traces, id)
		workspaceContext := decodeTraceWorkspaceContext(t, trace)
		var workspace ws.Workspace
		if err := json.Unmarshal(seedByID[id].AcceptedWorkspace, &workspace); err != nil {
			t.Errorf("seed %s decode acceptedWorkspace: %v", id, err)
			failed = true
			continue
		}
		if err := workspace.Validate(); err != nil {
			t.Errorf("seed %s accepted workspace validation: %v", id, err)
			failed = true
			continue
		}
		if len(workspace.Documents) == 0 || workspace.Documents[0].Output.ID == "" {
			t.Errorf("seed %s has no first document output ID", id)
			failed = true
			continue
		}
		digest, err := workspace.Digest()
		if err != nil {
			t.Errorf("seed %s workspace digest: %v", id, err)
			failed = true
			continue
		}
		identity := operationHistorySeedIdentity{ID: id, AcceptedVersion: 1, AcceptedDigest: digest, SnapshotToken: workspaceContext.SnapshotToken, OutputID: workspace.Documents[0].Output.ID}
		if identity.OutputID != workspaceContext.OutputID {
			t.Errorf("seed %s workspace output %q != model workspace context output %q", id, identity.OutputID, workspaceContext.OutputID)
			failed = true
			continue
		}
		prepareRequest := construction.ConstructionProposalRequest{Project: workspaceContext.Project, ExplorerID: workspaceContext.ExplorerID, SnapshotToken: identity.SnapshotToken,
			ExpectedDraftVersion: identity.AcceptedVersion, ExpectedDraftDigest: identity.AcceptedDigest, OutputID: identity.OutputID}
		service, store, _, _, stats := newOperationHistoryService(t, workspace, prepareRequest)
		if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
			Project: prepareRequest.Project, ExplorerID: prepareRequest.ExplorerID, SnapshotToken: prepareRequest.SnapshotToken,
			ExpectedDraftVersion: prepareRequest.ExpectedDraftVersion, ExpectedDraftDigest: prepareRequest.ExpectedDraftDigest,
			OutputID: prepareRequest.OutputID, StageID: recipe.ConstructionSourceProjectionID,
		}); err != nil {
			t.Errorf("seed %s real base compile/validate: %v", id, err)
			failed = true
			continue
		}
		if stats.base != 1 || store.saveDraftCalls != 0 || store.created.DraftDigest != digest {
			t.Errorf("seed %s base compiler/saves/digest=%d/%d/%q; want 1/0/%q", id, stats.base, store.saveDraftCalls, store.created.DraftDigest, digest)
			failed = true
			continue
		}
		identities.Cases = append(identities.Cases, identity)
	}
	if failed || len(identities.Cases) != len(traces.Cases) {
		t.Fatalf("prepared %d/%d accepted workspace identities; refusing to publish partial seed identity map", len(identities.Cases), len(traces.Cases))
	}
	encoded, err := json.MarshalIndent(identities, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed identities: %v", err)
	}
	if err := os.WriteFile(identityPath, encoded, 0o600); err != nil {
		t.Fatalf("write seed identities %s: %v", identityPath, err)
	}
	fmt.Printf("BUILDER_OPERATION_SEED_IDENTITIES_PATH=%s CASES=%d\n", identityPath, len(identities.Cases))
}

func readOperationHistoryJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace artifact %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode trace artifact %s: %v", path, err)
	}
}

func newOperationHistoryService(t *testing.T, w ws.Workspace, request construction.ConstructionProposalRequest) (*Service, *fakeStore, capability.Snapshot, ws.Workspace, *operationHistoryCompileStats) {
	t.Helper()
	encoded, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := w.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: request.Project, ExplorerID: request.ExplorerID, Title: w.Explorer.Title, DraftConfig: encoded, DraftVersion: request.ExpectedDraftVersion, DraftDigest: digest}, copyGet: true}
	snapshot := readySnapshot(request.Project, "operation-history-generation", request.SnapshotToken, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	rootID, rootType := "node-operation-history-root", "Patient"
	if len(w.Documents) > 0 && w.Documents[0].RootResourceType != "" {
		rootType = w.Documents[0].RootResourceType
	}
	fields := traceWorkspaceSourceFields(w)
	snapshot.Nodes = []capability.Node{{ID: rootID, ResourceType: rootType, RowRootEligible: true, Populated: true}}
	for _, f := range fields {
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{ID: "trace-candidate-" + f.ColumnID, NodeID: rootID, ResourceType: rootType, FieldPath: f.FieldPath, Label: f.Label, LogicalType: f.LogicalType, Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true})
	}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		c := traceWorkspaceCatalog(got, explorerID, w, rootID)
		for i := range c.Nodes {
			count := int64(2)
			c.Nodes[i].DocumentCount = &count
		}
		if c.Edges == nil {
			c.Edges = []ws.CatalogEdge{}
		}
		for i := range c.Candidates {
			if c.Candidates[i].AggregateOperations == nil {
				c.Candidates[i].AggregateOperations = []capability.AggregateOperationCapability{}
			}
			c.Candidates[i].Transformations = ws.AggregateTransformationCapabilitiesForCapability(got, c.Candidates[i].ID)
		}
		return c
	}
	stats := &operationHistoryCompileStats{}
	config.CompileReceipt = func(ctx context.Context, cr ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		if cr.ConstructionProposal == nil {
			stats.base++
		} else {
			stats.candidate++
		}
		receipt, err := compileTraceReceipt(ctx, cr, snapshot, traceWorkspaceCatalog(snapshot, cr.ExplorerID, cr.Workspace, rootID))
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.ConstructionProposal == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview has no compiled proposal receipt")
		}
		return dataframeexecution.PreviewSummary{Output: request.OutputID, Columns: emittedPublicColumnsForOutput(receipt, request.OutputID), Complete: true}, nil
	}
	return newTestService(t, store, config), store, snapshot, w, stats
}

func sameOperationHistoryExpectation(a, b operationHistoryExpectation) bool {
	return a.ProposalStatus == b.ProposalStatus && slices.Equal(a.StepIDs, b.StepIDs) && reflect.DeepEqual(a.GroupOutputTypes, b.GroupOutputTypes) && reflect.DeepEqual(a.DownstreamInputTypes, b.DownstreamInputTypes) && reflect.DeepEqual(a.DownstreamOutputTypes, b.DownstreamOutputTypes) && a.CandidateGroupKeyOutputID == b.CandidateGroupKeyOutputID && a.AcceptedGroupKeyOutputID == b.AcceptedGroupKeyOutputID && a.GroupKeyOutputRelation == b.GroupKeyOutputRelation && a.AcceptedAggregate == b.AcceptedAggregate && a.CandidateAggregate == b.CandidateAggregate && a.AcceptedChangesOnApply == b.AcceptedChangesOnApply && a.ApplyAck == b.ApplyAck && slices.Equal(a.MissingInputs, b.MissingInputs) && slices.Equal(a.RemovedStepIDs, b.RemovedStepIDs)
}

func assertOperationHistoryProposal(t *testing.T, id string, actionIndex int, aggregateOutputID string, expected operationHistoryExpectation, accepted ws.Construction, proposal construction.ConstructionProposalResponse, receipt *receiptmodel.CompilationReceipt, outputID string) {
	t.Helper()
	if got := constructionStepIDs(proposal.CandidateConstruction.Steps); !reflect.DeepEqual(got, expected.StepIDs) {
		t.Fatalf("trace %s action %d steps %v != model %v", id, actionIndex, got, expected.StepIDs)
	}
	actualKeyID, actualKeyType, actualAggregateID, actualAggregateType := constructionGroupOutputIdentity(proposal.CandidateConstruction)
	acceptedKeyID, _, acceptedAggregateID, _ := constructionGroupOutputIdentity(accepted)
	modelAggregateType, modelHasAggregate := expected.GroupOutputTypes[aggregateOutputID]
	if aggregateOutputID == "" || acceptedAggregateID != aggregateOutputID || !modelHasAggregate || modelAggregateType == "" || actualAggregateID != aggregateOutputID {
		t.Fatalf("trace %s action %d Group aggregate output id %q != accepted/model output id %q (accepted %q, model type %q present=%t)", id, actionIndex, actualAggregateID, aggregateOutputID, acceptedAggregateID, modelAggregateType, modelHasAggregate)
	}
	if expected.GroupKeyOutputRelation == "FRESH_FROM_ACCEPTED" && (actualKeyID == "" || actualKeyID == acceptedKeyID) {
		t.Fatalf("trace %s action %d restored Group key ID %q is not fresh from accepted ID %q", id, actionIndex, actualKeyID, acceptedKeyID)
	}
	if expected.GroupKeyOutputRelation == "SAME_AS_ACCEPTED" && actualKeyID != acceptedKeyID {
		t.Fatalf("trace %s action %d Group key ID %q != accepted ID %q", id, actionIndex, actualKeyID, acceptedKeyID)
	}
	if expected.GroupKeyOutputRelation == "ABSENT" && actualKeyID != "" {
		t.Fatalf("trace %s action %d model expects absent Group key, got %q", id, actionIndex, actualKeyID)
	}
	if expected.CandidateAggregate != "" && proposal.CandidateConstruction.Steps[0].Operation.Group != nil &&
		string(proposal.CandidateConstruction.Steps[0].Operation.Group.Aggregates[0].Operation) != expected.CandidateAggregate {
		t.Fatalf("trace %s action %d candidate aggregate %q != model %q", id, actionIndex, proposal.CandidateConstruction.Steps[0].Operation.Group.Aggregates[0].Operation, expected.CandidateAggregate)
	}
	wantKeyType := expected.GroupOutputTypes[expected.CandidateGroupKeyOutputID]
	if expected.CandidateGroupKeyOutputID != "" && (wantKeyType == "" || actualKeyType != wantKeyType) {
		t.Fatalf("trace %s action %d candidate key output %s type %q != model %q", id, actionIndex, expected.CandidateGroupKeyOutputID, actualKeyType, wantKeyType)
	}
	wantAggregateType := expected.GroupOutputTypes[actualAggregateID]
	if wantAggregateType != "" && actualAggregateType != wantAggregateType {
		t.Fatalf("trace %s action %d aggregate output %s type %q != model %q", id, actionIndex, actualAggregateID, actualAggregateType, wantAggregateType)
	}
	acceptedPositions := make(map[string]int, len(accepted.Steps))
	acceptedSteps := make(map[string]ws.ConstructionStep, len(accepted.Steps))
	for index, step := range accepted.Steps {
		acceptedPositions[step.ID] = index
		acceptedSteps[step.ID] = step
	}
	changedIndex := traceConstructionStepPosition(accepted, proposal.ChangedStepID)
	for _, candidateStep := range proposal.CandidateConstruction.Steps {
		acceptedStep, existed := acceptedSteps[candidateStep.ID]
		if !existed || acceptedPositions[candidateStep.ID] <= changedIndex {
			continue
		}
		acceptedStep.Inputs = candidateStep.Inputs
		acceptedStep.Outputs = candidateStep.Outputs
		if !reflect.DeepEqual(acceptedStep, candidateStep) {
			t.Fatalf("trace %s action %d surviving downstream step %s changed fields beyond Inputs/Outputs", id, actionIndex, candidateStep.ID)
		}
	}
	for stepID, input := range expected.DownstreamInputTypes {
		index := -1
		for i, step := range proposal.CandidateConstruction.Steps {
			if step.ID == stepID {
				index = i
				break
			}
		}
		if index < 0 {
			t.Fatalf("trace %s action %d missing downstream %s", id, actionIndex, stepID)
		}
		actual := "MISSING"
		for _, col := range proposal.CandidateConstruction.Steps[index].Outputs {
			if col.ID == input.ColumnID {
				actual = col.Type
				break
			}
		}
		if actual != input.Type {
			t.Fatalf("trace %s action %d dependent %s input %s type %q != %q", id, actionIndex, stepID, input.ColumnID, actual, input.Type)
		}
		if input.InputStepID != "" {
			actualInputStep := traceStepInputID(proposal.CandidateConstruction, stepID)
			if actualInputStep != input.InputStepID {
				t.Fatalf("trace %s action %d dependent %s input stage %q != model %q", id, actionIndex, stepID, actualInputStep, input.InputStepID)
			}
			if proposal.PreviewStatus == "PREVIEW_PENDING" {
				compiledInputStep := traceReceiptStageInputID(receipt, outputID, stepID)
				if compiledInputStep != input.InputStepID {
					t.Fatalf("trace %s action %d lowered dependent %s input stage %q != model %q", id, actionIndex, stepID, compiledInputStep, input.InputStepID)
				}
			}
		}
		if proposal.PreviewStatus == "PREVIEW_PENDING" {
			compiledType := "MISSING"
			for _, stage := range receipt.ConstructionStages[outputID] {
				if stage.ID != stepID {
					continue
				}
				for _, column := range stage.Columns {
					if column.ID == input.ColumnID {
						compiledType = column.Type
						break
					}
				}
			}
			if compiledType != input.Type {
				t.Fatalf("trace %s action %d compiled dependent %s input %s type %q != model %q", id, actionIndex, stepID, input.ColumnID, compiledType, input.Type)
			}
		}
	}
	for stepID, output := range expected.DownstreamOutputTypes {
		actualType := traceStepColumnType(proposal.CandidateConstruction, stepID, output.ColumnID)
		if actualType != "" {
			t.Fatalf("trace %s action %d proposal declared inferred output %s.%s type %q", id, actionIndex, stepID, output.ColumnID, actualType)
		}
		if proposal.PreviewStatus == "PREVIEW_PENDING" {
			compiledType := traceReceiptStageColumnTypeForOutput(receipt, outputID, stepID, output.ColumnID)
			if compiledType != output.Type {
				t.Fatalf("trace %s action %d lowered dependent %s output %s type %q != model %q", id, actionIndex, stepID, output.ColumnID, compiledType, output.Type)
			}
		}
	}
	missing := make([]traceMissingInput, 0, len(proposal.DependencyImpact.MissingInputs))
	for _, m := range proposal.DependencyImpact.MissingInputs {
		missing = append(missing, traceMissingInput{ID: m.StepID, ColumnID: m.ColumnID})
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].ID == missing[j].ID {
			return missing[i].ColumnID < missing[j].ColumnID
		}
		return missing[i].ID < missing[j].ID
	})
	wantMissing := append([]traceMissingInput(nil), expected.MissingInputs...)
	sort.Slice(wantMissing, func(i, j int) bool {
		if wantMissing[i].ID == wantMissing[j].ID {
			return wantMissing[i].ColumnID < wantMissing[j].ColumnID
		}
		return wantMissing[i].ID < wantMissing[j].ID
	})
	if !slices.Equal(missing, wantMissing) {
		t.Fatalf("trace %s action %d missing inputs %#v != %#v", id, actionIndex, missing, wantMissing)
	}
	if !slices.Equal(proposal.DependencyImpact.RemovedStepIDs, expected.RemovedStepIDs) {
		t.Fatalf("trace %s action %d removed steps %v != model %v", id, actionIndex, proposal.DependencyImpact.RemovedStepIDs, expected.RemovedStepIDs)
	}
	if proposal.PreviewStatus == "PREVIEW_PENDING" {
		if receipt == nil || receipt.ID != proposal.ProposalID {
			t.Fatalf("trace %s action %d lacks exact candidate compiler receipt", id, actionIndex)
		}
		stageTypes := map[string]string{}
		for _, stage := range receipt.ConstructionStages[outputID] {
			if stage.ID == proposal.ChangedStepID {
				for _, col := range stage.Columns {
					stageTypes[col.ID] = col.Type
				}
			}
		}
		if expected.CandidateGroupKeyOutputID != "" && stageTypes[actualKeyID] != wantKeyType {
			t.Fatalf("trace %s action %d lowered key output %s type %q != model %q", id, actionIndex, actualKeyID, stageTypes[actualKeyID], wantKeyType)
		}
		if wantAggregateType != "" && stageTypes[actualAggregateID] != wantAggregateType {
			t.Fatalf("trace %s action %d lowered aggregate output %s type %q != model %q", id, actionIndex, actualAggregateID, stageTypes[actualAggregateID], wantAggregateType)
		}
	}
}
