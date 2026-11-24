package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func traceFieldCandidate(snapshotToken, id, field, logicalType string) capability.Candidate {
	return capability.Candidate{
		ID: id, NodeID: "node-patient", ResourceType: "Patient", FieldPath: field, Label: field,
		LogicalType: logicalType, Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
	}
}

func compileTraceReceipt(ctx context.Context, request ports.CompileReceiptRequest, snapshot capability.Snapshot, catalog ws.CatalogSnapshot) (*receiptmodel.CompilationReceipt, error) {
	prepared, err := ws.PrepareWorkspaceForCompilation(request.Workspace, catalog)
	if err != nil {
		return nil, fmt.Errorf("prepare trace workspace: %w", err)
	}
	translated, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, prepared, snapshot, request.ResolvedInputs)
	if err != nil {
		return nil, fmt.Errorf("compile trace workspace: %w", err)
	}
	bindings := recipe.RuntimeBindings{Project: request.Project, DatasetGeneration: snapshot.Identity.Generation}
	plan, err := recipeplan.BuildRecipePlan(translated.Bundle, bindings)
	if err != nil {
		return nil, fmt.Errorf("build trace semantic plan: %w", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
	if err != nil {
		return nil, fmt.Errorf("resolve trace recipe plan: %w", err)
	}
	lowered, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return nil, fmt.Errorf("lower trace recipe plan: %w", err)
	}
	workspaceDigest, err := prepared.Digest()
	if err != nil {
		return nil, err
	}
	normalized, err := prepared.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	resolvedRecipeDigest, err := translated.Bundle.Digest()
	if err != nil {
		return nil, err
	}
	contracts, err := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: translated.OutputContracts})
	if err != nil {
		return nil, err
	}
	receipt := nativeReceipt(snapshot)
	receipt.ExplorerID = request.ExplorerID
	receipt.ReceiptFormatVersion = receiptmodel.CurrentReceiptFormatVersion
	receipt.CompilerContractVersion = receiptmodel.CurrentCompilerContractVersion
	receipt.IntentDigest = workspaceDigest
	receipt.ResolvedInputsDigest = translated.ResolvedInputsDigest
	receipt.RecipeDigest = translated.RecipeDigest
	receipt.ResolvedRecipeDigest = resolvedRecipeDigest
	receipt.ResolvedSchemaDigest = resolved.ResolvedSchemaDigest
	receipt.NormalizedBundle = normalized
	receipt.Bundle = translated.Bundle
	receipt.EmittedColumns = translated.EmittedColumns
	receipt.IdentityMappings = translated.IdentityMappings
	receipt.PublicOutputContract = contracts
	receipt.OutputContractDigest, err = receiptmodel.CompilationArtifactDigest(contracts)
	if err != nil {
		return nil, err
	}
	receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
	receipt.ConstructionStages = traceReceiptStages(lowered)
	receipt.CompiledOutputSchemas = traceReceiptOutputSchemas(lowered)
	receipt.OutputColumnProvenance = traceOutputProvenance(translated.EmittedColumns)
	receipt.OutputFingerprints, err = traceOutputFingerprints(lowered)
	if err != nil {
		return nil, err
	}
	receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = receiptmodel.ReceiptID(*receipt)
	if err != nil {
		return nil, err
	}
	if err := receipt.Validate(); err != nil {
		return nil, fmt.Errorf("validate trace compiler receipt: %w", err)
	}
	return receipt, nil
}

func traceReceiptStages(compiled facade.CompiledRecipe) map[string][]receiptmodel.ReceiptConstructionStage {

	var stages map[string][]receiptmodel.ReceiptConstructionStage
	for _, output := range compiled.Outputs {
		if len(output.Stages) == 0 {
			continue
		}
		if stages == nil {
			stages = make(map[string][]receiptmodel.ReceiptConstructionStage)
		}
		outputStages := make([]receiptmodel.ReceiptConstructionStage, 0, len(output.Stages))
		for index, descriptor := range output.Stages {
			stage := receiptmodel.ReceiptConstructionStage{
				ID: descriptor.ID, InputStageID: descriptor.InputStageID, Operation: descriptor.Operation,
				RowIdentityColumn: descriptor.RowIdentityColumn,
			}
			if index == 0 && descriptor.ID == recipe.ConstructionSourceProjectionID {
				stage.Operation = ""
			}
			for _, column := range descriptor.Columns {
				if column.Internal || column.Identity {
					continue
				}
				stage.Columns = append(stage.Columns, receiptmodel.ReceiptConstructionStageColumn{
					ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Kind,
					Cardinality: expression.Cardinality(column.Cardinality),
				})
			}
			for _, capability := range descriptor.Capabilities {
				reasonCode, reason := capability.ReasonCode, capability.Reason
				if capability.Supported {
					reasonCode, reason = "", ""
				}
				stage.Capabilities = append(stage.Capabilities, receiptmodel.ReceiptConstructionOperationChoice{
					Kind: string(capability.Operation), Supported: capability.Supported, ReasonCode: reasonCode, Reason: reason,
				})
			}
			outputStages = append(outputStages, stage)
		}
		stages[output.Name] = outputStages
	}
	return stages
}

func traceReceiptOutputSchemas(compiled facade.CompiledRecipe) map[string][]receiptmodel.ReceiptCompiledOutputColumn {
	schemas := make(map[string][]receiptmodel.ReceiptCompiledOutputColumn, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		columns := make([]receiptmodel.ReceiptCompiledOutputColumn, 0, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			columns = append(columns, receiptmodel.ReceiptCompiledOutputColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, LogicalType: column.Kind,
				Cardinality: column.Cardinality, Nullable: column.Nullable, Internal: column.Internal, Identity: column.Identity,
			})
		}
		schemas[output.Name] = columns
	}
	return schemas
}

func traceOutputProvenance(columns []viewer.EmittedColumn) map[string]map[string]string {
	provenance := make(map[string]map[string]string)
	for _, column := range columns {
		if provenance[column.OutputID] == nil {
			provenance[column.OutputID] = make(map[string]string)
		}
		provenance[column.OutputID][column.PublicColumn] = "EXPLICIT"
	}
	return provenance
}

func traceOutputFingerprints(compiled facade.CompiledRecipe) (map[string]string, error) {
	fingerprints := make(map[string]string, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		encoded, err := json.Marshal(output.Plan)
		if err != nil {
			return nil, fmt.Errorf("marshal lowered plan for %s fingerprint: %w", output.Name, err)
		}
		sum := sha256.Sum256(encoded)
		fingerprints[output.Name] = hex.EncodeToString(sum[:])
	}
	return fingerprints, nil
}

func traceStageColumnType(step ws.ConstructionStep, columnID string) string {
	for _, column := range step.Outputs {
		if column.ID == columnID {
			return column.Type
		}
	}
	return ""
}

func traceReceiptStageColumnType(receipt *receiptmodel.CompilationReceipt, stageID, columnID string) string {
	if receipt == nil || len(receipt.ConstructionStages["patients"]) == 0 {
		return ""
	}
	for _, stage := range receipt.ConstructionStages["patients"] {
		if stage.ID != stageID {
			continue
		}
		for _, column := range stage.Columns {
			if column.ID == columnID {
				return column.Type
			}
		}
	}
	return ""
}

func emittedPublicColumns(receipt *receiptmodel.CompilationReceipt) []string {
	columns := make([]string, 0, len(receipt.EmittedColumns))
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == "patients" {
			columns = append(columns, column.PublicColumn)
		}
	}
	return columns
}

type operationHistoryTraceFile struct {
	SchemaVersion int
	Cases         []operationHistoryTrace
}

type operationHistoryTrace struct {
	ID                 string
	TraceKind          string
	RequestFingerprint string
	Initial            json.RawMessage
	Actions            []operationHistoryAction
	Expectations       []operationHistoryExpectation
}

type operationHistoryWorkspaceContext struct {
	Project          string
	ExplorerID       string
	AuthResourcePath string
	SnapshotToken    string
	OutputID         string
}

type operationHistoryAction struct {
	Kind   string
	Result string
}

type operationHistoryExpectation struct {
	ActionIndex               int
	CandidateActionIndex      int
	RequestFingerprint        string
	ProposalStatus            string
	StepIDs                   []string
	GroupOutputTypes          map[string]string
	DownstreamInputTypes      map[string]traceInputType
	DownstreamOutputTypes     map[string]traceInputType
	CandidateGroupKeyOutputID string
	AcceptedGroupKeyOutputID  string
	GroupKeyOutputRelation    string
	AcceptedAggregate         string
	CandidateAggregate        string
	AcceptedChangesOnApply    bool
	ApplyAck                  string
	MissingInputs             []traceMissingInput
	RemovedStepIDs            []string
}

type traceInputType struct {
	ColumnID    string
	Type        string
	InputStepID string
}

type traceMissingInput struct {
	ID       string
	ColumnID string
}

type operationHistoryCandidateFile struct {
	SchemaVersion int
	Cases         []operationHistoryCandidate
}

type operationHistorySeedFile struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Cases         []operationHistorySeed `json:"cases"`
}

type operationHistorySeed struct {
	ID                string
	AcceptedWorkspace json.RawMessage
}

type operationHistorySeedIdentityFile struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Cases         []operationHistorySeedIdentity `json:"cases"`
}

type operationHistorySeedIdentity struct {
	ID              string `json:"id"`
	AcceptedVersion int64  `json:"acceptedVersion"`
	AcceptedDigest  string `json:"acceptedDigest"`
	SnapshotToken   string `json:"snapshotToken"`
	OutputID        string `json:"outputId"`
}

type operationHistoryCandidate struct {
	ID          string
	TraceID     string
	Checkpoints []operationHistoryCandidateCheckpoint
}

type operationHistoryCandidateCheckpoint struct {
	ActionIndex           int
	AcceptedDocument      json.RawMessage
	AcceptedWorkspace     json.RawMessage
	ProposalRequest       json.RawMessage
	CandidateConstruction json.RawMessage
	ChangedStepID         string
	RemoveStepIDs         []string
	Expected              operationHistoryExpectation
	RequestFingerprint    string
}

type operationHistoryReloadFile struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Cases         []operationHistoryReloadCase `json:"cases"`
}

type operationHistoryReloadCase struct {
	ID              string                                        `json:"id"`
	ActionIndex     int                                           `json:"actionIndex"`
	OutputID        string                                        `json:"outputId"`
	StageID         string                                        `json:"stageId"`
	Before          ws.BuilderState                               `json:"before"`
	After           ws.BuilderState                               `json:"after"`
	StoredWorkspace ws.Workspace                                  `json:"storedWorkspace"`
	Capabilities    construction.ConstructionCapabilitiesResponse `json:"capabilities"`
}

type operationHistoryReloadSummary struct {
	ID           string `json:"id"`
	ActionIndex  int    `json:"actionIndex"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
}

type operationHistoryReplaySummary struct {
	SchemaVersion                         int                             `json:"schemaVersion"`
	TraceCaseCount                        int                             `json:"traceCaseCount"`
	CandidateCaseCount                    int                             `json:"candidateCaseCount"`
	ReplayedCaseCount                     int                             `json:"replayedCaseCount"`
	SkippedCaseCount                      int                             `json:"skippedCaseCount"`
	ProposalCheckpointCount               int                             `json:"proposalCheckpointCount"`
	UniqueRequestFingerprintCount         int                             `json:"uniqueRequestFingerprintCount"`
	UniqueAbstractRequestFingerprintCount int                             `json:"uniqueAbstractRequestFingerprintCount"`
	UniqueRawRequestFingerprintCount      int                             `json:"uniqueRawRequestFingerprintCount"`
	ReadyProposalCount                    int                             `json:"readyProposalCount"`
	NeedsRepairProposalCount              int                             `json:"needsRepairProposalCount"`
	ApplyInvocationCount                  int                             `json:"applyInvocationCount"`
	ApplyAckSuccessCount                  int                             `json:"applyAckSuccessCount"`
	ApplyAckFailureCount                  int                             `json:"applyAckFailureCount"`
	ServicePreviewCount                   int                             `json:"servicePreviewCount"`
	Cases                                 []operationHistoryReplayCase    `json:"cases"`
	Reloads                               []operationHistoryReloadSummary `json:"reloads"`
}

type operationHistoryProposalResponseFile struct {
	SchemaVersion int                                    `json:"schemaVersion"`
	Cases         []operationHistoryProposalResponseCase `json:"cases"`
}

type operationHistoryProposalResponseCase struct {
	ID          string                                  `json:"id"`
	Checkpoints []operationHistoryProposalResponseEntry `json:"checkpoints"`
}

type operationHistoryProposalResponseEntry struct {
	ActionIndex        int                                       `json:"actionIndex"`
	RequestID          string                                    `json:"requestId"`
	RequestFingerprint string                                    `json:"requestFingerprint"`
	ProposalResponse   construction.ConstructionProposalResponse `json:"proposalResponse"`
	PreviewResult      *operationHistoryPreviewResult            `json:"previewResult,omitempty"`
}

type operationHistoryPreviewResult struct {
	ReceiptID string                            `json:"receiptId"`
	OutputID  string                            `json:"outputId"`
	Columns   []viewer.EmittedColumn            `json:"columns"`
	Summary   dataframeexecution.PreviewSummary `json:"summary"`
}

type operationHistoryReplayCase struct {
	ID                  string                           `json:"id"`
	ProposalCheckpoints []operationHistoryReplayProposal `json:"proposalCheckpoints"`
	ApplyAck            []operationHistoryReplayAck      `json:"applyAck"`
}

type operationHistoryReplayProposal struct {
	ActionIndex           int                `json:"actionIndex"`
	CandidateActionIndex  int                `json:"candidateActionIndex"`
	RequestFingerprint    string             `json:"requestFingerprint"`
	RequestID             string             `json:"requestId"`
	Status                string             `json:"status"`
	ProposalIDPresent     bool               `json:"proposalIdPresent"`
	StepIDs               []string           `json:"stepIds"`
	RemovedStepIDs        []string           `json:"removedStepIds"`
	GroupOutputTypes      map[string]*string `json:"groupOutputTypes"`
	DownstreamInputTypes  map[string]string  `json:"downstreamInputTypes"`
	DownstreamOutputTypes map[string]string  `json:"downstreamOutputTypes"`
	MissingInputCount     int                `json:"missingInputCount"`
}

type operationHistoryReplayAck struct {
	ActionIndex           int    `json:"actionIndex"`
	Result                string `json:"result"`
	AcceptedVersionBefore int64  `json:"acceptedVersionBefore"`
	AcceptedVersionAfter  int64  `json:"acceptedVersionAfter"`
	AcceptedDigestBefore  string `json:"acceptedDigestBefore"`
	AcceptedDigestAfter   string `json:"acceptedDigestAfter"`
}

func traceByIDForID(t *testing.T, traces operationHistoryTraceFile, id string) operationHistoryTrace {
	t.Helper()
	for _, trace := range traces.Cases {
		if trace.ID == id {
			return trace
		}
	}
	t.Fatalf("model trace %s not found", id)
	return operationHistoryTrace{}
}

func decodeTraceWorkspaceContext(t *testing.T, trace operationHistoryTrace) operationHistoryWorkspaceContext {
	t.Helper()
	var initial struct {
		WorkspaceContext operationHistoryWorkspaceContext
	}
	if err := json.Unmarshal(trace.Initial, &initial); err != nil {
		t.Fatalf("trace %s decode workspace context: %v", trace.ID, err)
	}
	context := initial.WorkspaceContext
	if context.Project == "" || context.ExplorerID == "" || context.SnapshotToken == "" || context.OutputID == "" {
		t.Fatalf("trace %s workspace context lacks project/explorer/snapshot/output: %#v", trace.ID, context)
	}
	return context
}

func traceAggregateOutputID(t *testing.T, trace operationHistoryTrace) string {
	t.Helper()
	var initial struct {
		AggregateOutputID string
	}
	if err := json.Unmarshal(trace.Initial, &initial); err != nil || initial.AggregateOutputID == "" {
		t.Fatalf("trace %s initial aggregateOutputId is missing or invalid: %v", trace.ID, err)
	}
	return initial.AggregateOutputID
}

func assertTraceSeedIdentity(t *testing.T, id string, identity operationHistorySeedIdentity, workspace ws.Workspace, request construction.ConstructionProposalRequest) {
	t.Helper()
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatalf("trace %s prepared seed digest: %v", id, err)
	}
	if identity.ID != id || identity.AcceptedVersion != request.ExpectedDraftVersion || identity.AcceptedDigest != digest ||
		identity.AcceptedDigest != request.ExpectedDraftDigest || identity.SnapshotToken != request.SnapshotToken || identity.OutputID != request.OutputID {
		t.Fatalf("trace %s request identity version/digest/snapshot/output %d/%q/%q/%q differs from prepared seed %d/%q/%q/%q", id,
			request.ExpectedDraftVersion, request.ExpectedDraftDigest, request.SnapshotToken, request.OutputID,
			identity.AcceptedVersion, identity.AcceptedDigest, identity.SnapshotToken, identity.OutputID)
	}
}

func decodeTraceWorkspace(t *testing.T, id string, data json.RawMessage) ws.Workspace {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("trace %s has no acceptedWorkspace", id)
	}
	w, err := ws.DecodeWorkspace(data)
	if err != nil {
		t.Fatalf("trace %s decode acceptedWorkspace: %v", id, err)
	}
	return w
}

func decodeTraceProposalRequest(t *testing.T, id string, data json.RawMessage) construction.ConstructionProposalRequest {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("trace %s has no full proposalRequest", id)
	}
	var request construction.ConstructionProposalRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("trace %s decode proposalRequest: %v", id, err)
	}
	return request
}

func traceProposalRequestID(t *testing.T, id string, data json.RawMessage) string {
	t.Helper()
	var request struct{ RequestID string }
	if err := json.Unmarshal(data, &request); err != nil || request.RequestID == "" {
		t.Fatalf("trace %s captured proposalRequest has no transport requestId (decode error %v)", id, err)
	}
	return request.RequestID
}

func assertTraceProposalContext(t *testing.T, id string, trace operationHistoryTrace, data json.RawMessage, request construction.ConstructionProposalRequest) {
	t.Helper()
	context := decodeTraceWorkspaceContext(t, trace)
	var raw struct {
		RequestID            string
		Project              string
		ExplorerID           string
		AuthResourcePath     string
		SnapshotToken        string
		ExpectedDraftVersion int64
		ExpectedDraftDigest  string
		OutputID             string
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("trace %s decode captured request context: %v", id, err)
	}
	if raw.RequestID == "" || raw.AuthResourcePath != context.AuthResourcePath ||
		raw.Project != request.Project || raw.Project != context.Project ||
		raw.ExplorerID != request.ExplorerID || raw.ExplorerID != context.ExplorerID ||
		raw.SnapshotToken != request.SnapshotToken || raw.SnapshotToken != context.SnapshotToken ||
		raw.ExpectedDraftVersion != request.ExpectedDraftVersion ||
		raw.ExpectedDraftDigest != request.ExpectedDraftDigest ||
		raw.OutputID != request.OutputID || raw.OutputID != context.OutputID {
		t.Fatalf("trace %s captured proposal request context/transport identity differs from model or typed request: raw=%#v model=%#v typed=%#v", id, raw, context, request)
	}
}

func constructionFromRaw(t *testing.T, id string, actionIndex int, data json.RawMessage) ws.Construction {
	t.Helper()
	var candidate ws.Construction
	if len(data) == 0 || json.Unmarshal(data, &candidate) != nil {
		t.Fatalf("trace %s action %d missing/invalid candidateConstruction", id, actionIndex)
	}
	return candidate
}

func assertTraceWorkspaceMatchesRequest(t *testing.T, id string, w ws.Workspace, r construction.ConstructionProposalRequest) {
	t.Helper()
	if err := w.Validate(); err != nil {
		t.Fatalf("trace %s accepted workspace validation: %v", id, err)
	}
	digest, err := w.Digest()
	if err != nil || digest != r.ExpectedDraftDigest {
		t.Fatalf("trace %s accepted workspace digest %q != request digest %q (err %v)", id, digest, r.ExpectedDraftDigest, err)
	}
	if r.Project == "" || r.ExplorerID == "" || r.SnapshotToken == "" || r.ExpectedDraftVersion < 1 || r.OutputID == "" || support.ProposalDocument(w, r.OutputID) == nil {
		t.Fatalf("trace %s proposal identity/output is incomplete: %#v", id, r)
	}
}

type operationHistoryCompileStats struct{ base, candidate int }

type traceSourceField struct{ ColumnID, FieldPath, FHIRType, LogicalType, Label string }

func traceWorkspaceSourceFields(w ws.Workspace) []traceSourceField {
	fields := map[string]traceSourceField{}
	for _, doc := range w.Documents {
		for _, col := range doc.Columns {
			if col.Source.Kind == ws.SourceField && col.Source.Field != nil {
				fields[col.ColumnID] = traceSourceField{ColumnID: col.ColumnID, FieldPath: col.Source.Field.Path, LogicalType: col.LogicalType, Label: col.Label}
			}
		}
		if doc.Construction != nil {
			for _, p := range doc.Construction.SourceProjections {
				fields[p.ColumnID] = traceSourceField{ColumnID: p.ColumnID, FieldPath: p.FieldPath, FHIRType: p.FHIRType, LogicalType: p.LogicalType, Label: p.Label}
			}
		}
	}
	ids := make([]string, 0, len(fields))
	for id := range fields {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]traceSourceField, 0, len(ids))
	for _, id := range ids {
		result = append(result, fields[id])
	}
	return result
}

func traceWorkspaceCatalog(snapshot capability.Snapshot, explorerID string, w ws.Workspace, nodeID string) ws.CatalogSnapshot {
	rootType := "Patient"
	if len(w.Documents) > 0 && w.Documents[0].RootResourceType != "" {
		rootType = w.Documents[0].RootResourceType
	}
	c := ws.CatalogSnapshot{APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: explorerID, SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true}, Nodes: []ws.CatalogNode{{ID: nodeID, ResourceType: rootType, RowRootEligible: true, RowGrain: "patient"}}, Candidates: []ws.CatalogCandidate{}}
	for _, f := range traceWorkspaceSourceFields(w) {
		candidate := capability.Candidate{ID: "trace-candidate-" + f.ColumnID, NodeID: nodeID, ResourceType: rootType, FieldPath: f.FieldPath, Label: f.Label, LogicalType: f.LogicalType, Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true}
		choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
		if err != nil {
			continue
		}
		c.Candidates = append(c.Candidates, ws.CatalogCandidate{ID: candidate.ID, NodeID: nodeID, FieldPath: f.FieldPath, Label: f.Label, LogicalType: f.LogicalType, Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice})
	}
	return c
}

func emittedPublicColumnsForOutput(receipt *receiptmodel.CompilationReceipt, outputID string) []string {
	var columns []string
	for _, col := range receipt.EmittedColumns {
		if col.OutputID == outputID {
			columns = append(columns, col.PublicColumn)
		}
	}
	return columns
}

func workspaceFromDraft(t *testing.T, id string, data []byte) ws.Workspace {
	t.Helper()
	w, err := ws.DecodeWorkspace(data)
	if err != nil {
		t.Fatalf("trace %s decode draft: %v", id, err)
	}
	return w
}

func firstDifferentByte(left, right []byte) (int, byte, byte) {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return i, left[i], right[i]
		}
	}
	if len(left) != len(right) {
		return limit, 0, 0
	}
	return -1, 0, 0
}

func traceConstructionStepPosition(construction ws.Construction, stepID string) int {
	for index, step := range construction.Steps {
		if step.ID == stepID {
			return index
		}
	}
	return -1
}

func equalTraceMissingInputs(left, right []traceMissingInput) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func traceProposalRequestFingerprint(t *testing.T, id string, request json.RawMessage) string {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(request, &raw); err != nil {
		t.Fatalf("trace %s decode request for raw fingerprint: %v", id, err)
	}
	delete(raw, "requestId")
	canonical, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("trace %s encode request for raw fingerprint: %v", id, err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func constructionGroupOutputIdentity(construction ws.Construction) (keyID, keyType, aggregateID, aggregateType string) {
	if len(construction.Steps) == 0 || construction.Steps[0].Operation.Group == nil {
		return "", "", "", ""
	}
	group := construction.Steps[0].Operation.Group
	if len(group.Keys) > 0 {
		keyID = group.Keys[0].OutputColumnID
	}
	if len(group.Aggregates) > 0 {
		aggregateID = group.Aggregates[0].OutputColumnID
	}
	for _, output := range construction.Steps[0].Outputs {
		if output.ID == keyID {
			keyType = output.Type
		}
		if output.ID == aggregateID {
			aggregateType = output.Type
		}
	}
	return keyID, keyType, aggregateID, aggregateType
}

func actualTraceGroupOutputTypes(construction ws.Construction) map[string]*string {
	result := map[string]*string{"aggregate": nil, "key": nil}
	if len(construction.Steps) == 0 || construction.Steps[0].Operation.Group == nil {
		return result
	}
	group := construction.Steps[0].Operation.Group
	for _, output := range construction.Steps[0].Outputs {
		if len(group.Keys) > 0 && output.ID == group.Keys[0].OutputColumnID {
			value := output.Type
			result["key"] = &value
		}
		if len(group.Aggregates) > 0 && output.ID == group.Aggregates[0].OutputColumnID {
			value := output.Type
			result["aggregate"] = &value
		}
	}
	return result
}

func actualTraceDownstreamInputTypes(construction ws.Construction, expected operationHistoryExpectation) map[string]string {
	result := make(map[string]string, len(expected.DownstreamInputTypes))
	for stepID, input := range expected.DownstreamInputTypes {
		result[stepID] = "MISSING"
		for _, step := range construction.Steps {
			if step.ID != stepID {
				continue
			}
			for _, output := range step.Outputs {
				if output.ID == input.ColumnID {
					result[stepID] = output.Type
					break
				}
			}
		}
	}
	return result
}

func actualTraceDownstreamOutputTypes(receipt *receiptmodel.CompilationReceipt, outputID string, expected operationHistoryExpectation) map[string]string {
	result := make(map[string]string, len(expected.DownstreamOutputTypes))
	for stepID, output := range expected.DownstreamOutputTypes {
		result[stepID] = traceReceiptStageColumnTypeForOutput(receipt, outputID, stepID, output.ColumnID)
	}
	return result
}

func traceStepColumnType(construction ws.Construction, stepID, columnID string) string {
	for _, step := range construction.Steps {
		if step.ID != stepID {
			continue
		}
		for _, column := range step.Outputs {
			if column.ID == columnID {
				return column.Type
			}
		}
	}
	return "MISSING"
}

func traceStepInputID(construction ws.Construction, stepID string) string {
	for _, step := range construction.Steps {
		if step.ID != stepID {
			continue
		}
		for _, input := range step.Inputs {
			if input.Kind == ws.ConstructionInputStepOutput {
				return input.StepID
			}
		}
	}
	return ""
}

func traceReceiptStageInputID(receipt *receiptmodel.CompilationReceipt, outputID, stageID string) string {
	if receipt == nil {
		return ""
	}
	for _, stage := range receipt.ConstructionStages[outputID] {
		if stage.ID == stageID {
			return stage.InputStageID
		}
	}
	return ""
}

func traceReceiptStageColumnTypeForOutput(receipt *receiptmodel.CompilationReceipt, outputID, stageID, columnID string) string {
	if receipt == nil {
		return "MISSING"
	}
	for _, stage := range receipt.ConstructionStages[outputID] {
		if stage.ID != stageID {
			continue
		}
		for _, column := range stage.Columns {
			if column.ID == columnID {
				return column.Type
			}
		}
	}
	return "MISSING"
}
