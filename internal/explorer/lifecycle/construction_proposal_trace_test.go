package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func TestProposeConstructionGroupMeanRecalculatesDependentFilterWithRealCompiler(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, Populated: true}}
	idCandidate := traceFieldCandidate(snapshot.Token, "candidate-id", "id", "string")
	numericCandidate := traceFieldCandidate(snapshot.Token, "candidate-birth-count", "multipleBirthInteger", "integer")
	snapshot.Candidates = []capability.Candidate{idCandidate, numericCandidate}
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.Capability.ForExecution = service.config.Capability.ForCompilation
	service.config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := lifecycleInterpretationCatalog(got, explorerID)
		for _, candidate := range []capability.Candidate{numericCandidate} {
			choice, err := capability.NewFieldConstructionChoice(got.Token, candidate)
			if err != nil {
				t.Fatalf("make source choice for %s: %v", candidate.ID, err)
			}
			catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}

	workspace, baseDocument, baseConstruction := traceGroupMeanWorkspace(t, store)
	compileCount := 0
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileCount++
		receipt, err := compileTraceReceipt(ctx, request, snapshot, service.catalog(snapshot, request.ExplorerID))
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.ConstructionProposal == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview did not receive the proposal's exact compiled receipt")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: emittedPublicColumns(receipt), Complete: true}, nil
	}

	candidate := cloneLifecycleTestConstruction(t, baseConstruction)
	candidate.Steps[0].Operation.Group.Aggregates[0] = authoringv2.ConstructionGroupAggregate{
		Operation: authoringv2.ConstructionGroupMean, InputColumnID: "birth-count", OutputColumnID: "row_count",
	}
	candidate.Steps[0].Outputs[1].Type = "decimal"

	acceptedBefore := append([]byte(nil), store.created.DraftConfig...)
	versionBefore, digestBefore := store.created.DraftVersion, store.created.DraftDigest
	analyzed, impact, err := baseDocument.AnalyzeConstructionCandidate(candidate, "group-records", nil)
	if err != nil {
		t.Fatalf("AnalyzeConstructionCandidate: %v", err)
	}
	if impact.HasMissingInputs() {
		t.Fatalf("MEAN edit unexpectedly needs repair: %#v", impact.MissingInputs)
	}
	if got := traceStageColumnType(analyzed.Construction.Steps[0], "row_count"); got != "decimal" {
		t.Fatalf("Document.AnalyzeConstructionCandidate Group output type = %q, want decimal", got)
	}
	if got := traceStageColumnType(analyzed.Construction.Steps[1], "row_count"); got != "decimal" {
		t.Fatalf("Document.AnalyzeConstructionCandidate dependent FILTER input type = %q, want decimal", got)
	}

	proposal, err := service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: versionBefore, ExpectedDraftDigest: digestBefore, OutputID: "patients",
		ChangedStepID: "group-records", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("ProposeConstruction: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("proposal status/id = %q/%q, want a compiled preview proposal", proposal.PreviewStatus, proposal.ProposalID)
	}
	if got := traceStageColumnType(proposal.CandidateConstruction.Steps[0], "row_count"); got != "decimal" {
		t.Fatalf("proposal Group output type = %q, want decimal", got)
	}
	if got := traceStageColumnType(proposal.CandidateConstruction.Steps[1], "row_count"); got != "decimal" {
		t.Fatalf("proposal dependent FILTER input type = %q, want decimal", got)
	}
	if got := traceReceiptStageColumnType(store.receipt, "group-records", "row_count"); got != "decimal" {
		t.Fatalf("actual compiler/lowering GROUP stage output type = %q, want decimal", got)
	}
	if compileCount < 2 {
		t.Fatalf("actual compiler ran %d times, want base and proposal receipt compilation", compileCount)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != versionBefore || store.created.DraftDigest != digestBefore || string(store.created.DraftConfig) != string(acceptedBefore) {
		t.Fatal("ProposeConstruction changed the accepted draft before Apply")
	}

	preview, err := service.Preview(context.Background(), PreviewRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil || preview.Receipt == nil || preview.Receipt.ID != proposal.ProposalID {
		t.Fatalf("exact proposal preview receipt=%#v err=%v", preview.Receipt, err)
	}

	apply, err := service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "trace-apply-group-mean", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: versionBefore, ExpectedDraftDigest: digestBefore,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}, "trace-actor")
	if err != nil {
		t.Fatalf("ApplyCommands exact proposal: %v", err)
	}
	if apply.Workspace.Documents[0].Construction == nil || traceReceiptStageColumnType(store.receipt, "group-records", "row_count") != "decimal" {
		t.Fatalf("Apply acknowledgement omitted applied MEAN construction: %#v", apply.Workspace.Documents[0].Construction)
	}
	if store.created.DraftVersion != versionBefore+1 || store.created.DraftDigest == digestBefore || store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("Apply acknowledgement did not advance the accepted draft exactly once: version=%d digest=%q proposal=%q", store.created.DraftVersion, store.created.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("Apply acknowledgement saved %d times, want exactly once", store.saveDraftCalls)
	}
	appliedDocument := apply.Workspace.Documents[0]
	gotConstruction, err := json.Marshal(appliedDocument.Construction)
	if err != nil {
		t.Fatal(err)
	}
	wantConstruction, err := json.Marshal(analyzed.Construction)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConstruction) != string(wantConstruction) {
		t.Fatalf("Apply acknowledgement construction = %#v, want analyzed construction %#v", appliedDocument.Construction, analyzed.Construction)
	}
	if appliedDocument.Output != baseDocument.Output || appliedDocument.RootResourceType != baseDocument.RootResourceType || !reflect.DeepEqual(appliedDocument.Columns, baseDocument.Columns) {
		t.Fatalf("Apply acknowledgement changed non-construction document fields: output=%#v root=%q columns=%#v", appliedDocument.Output, appliedDocument.RootResourceType, appliedDocument.Columns)
	}
	_ = workspace
}

func traceGroupMeanWorkspace(t *testing.T, store *fakeStore) (authoringv2.Workspace, authoringv2.Document, authoringv2.Construction) {
	t.Helper()
	workspace := lifecycleCandidatePreviewWorkspace()
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) == 0 {
		t.Fatal("trace source workspace has no source columns")
	}
	if document.Columns[0].ColumnID == "" {
		document.Columns[0].ColumnID = "patient-id"
	}
	patientIDColumnID := document.Columns[0].ColumnID
	document.Columns = append(document.Columns, authoringv2.Column{
		ColumnID: "birth-count", Column: "birth_count", Label: "Birth count", LogicalType: "integer",
		OccurrenceID: authoringv2.RootOccurrenceID,
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "multipleBirthInteger", ProjectionMode: "VALUE"}},
	})
	sort.SliceStable(document.Columns, func(left, right int) bool {
		return document.Columns[left].ColumnID < document.Columns[right].ColumnID
	})
	groupOutputs := []authoringv2.StageColumn{
		{ID: "patient-key", Name: "patient_key", Label: "Patient", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Aggregate", Type: "integer"},
	}
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID: "group-records", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
				ConstructionID: "group-records", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
				Keys:       []authoringv2.ConstructionGroupKey{{InputColumnID: patientIDColumnID, OutputColumnID: "patient-key"}},
				Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: groupOutputs,
		},
		{
			ID: "dependent-filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group-records"}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
				ColumnID: "row_count", Operator: authoringv2.ConstructionFilterExists,
			}},
			Outputs: append([]authoringv2.StageColumn(nil), groupOutputs...),
		},
	}}
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate trace workspace: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	return workspace, document, construction
}

func traceFieldCandidate(snapshotToken, id, field, logicalType string) capability.Candidate {
	return capability.Candidate{
		ID: id, NodeID: "node-patient", ResourceType: "Patient", FieldPath: field, Label: field,
		LogicalType: logicalType, Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
	}
}

func compileTraceReceipt(ctx context.Context, request CompileReceiptRequest, snapshot capability.Snapshot, catalog authoringv2.CatalogSnapshot) (*explorer.CompilationReceipt, error) {
	prepared, err := authoringv2.PrepareWorkspaceForCompilation(request.Workspace, catalog)
	if err != nil {
		return nil, fmt.Errorf("prepare trace workspace: %w", err)
	}
	translated, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, prepared, snapshot, request.ResolvedInputs)
	if err != nil {
		return nil, fmt.Errorf("compile trace workspace: %w", err)
	}
	bindings := recipe.RuntimeBindings{Project: request.Project, DatasetGeneration: snapshot.Identity.Generation}
	plan, err := semantic.BuildRecipePlan(translated.Bundle, bindings)
	if err != nil {
		return nil, fmt.Errorf("build trace semantic plan: %w", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
	if err != nil {
		return nil, fmt.Errorf("resolve trace recipe plan: %w", err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
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
	contracts, err := json.Marshal(explorer.PublicOutputContracts{Outputs: translated.OutputContracts})
	if err != nil {
		return nil, err
	}
	receipt := nativeReceipt(snapshot)
	receipt.ExplorerID = request.ExplorerID
	receipt.ReceiptFormatVersion = explorer.CurrentReceiptFormatVersion
	receipt.CompilerContractVersion = explorer.CurrentCompilerContractVersion
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
	receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(contracts)
	if err != nil {
		return nil, err
	}
	receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
	receipt.ConstructionStages = traceReceiptStages(lowered)
	receipt.CompiledOutputSchemas = traceReceiptOutputSchemas(lowered)
	receipt.OutputColumnProvenance = traceOutputProvenance(translated.EmittedColumns)
	receipt.OutputFingerprints, err = traceOutputFingerprints(lowered)
	if err != nil {
		return nil, err
	}
	receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(*receipt)
	if err != nil {
		return nil, err
	}
	if err := receipt.Validate(); err != nil {
		return nil, fmt.Errorf("validate trace compiler receipt: %w", err)
	}
	return receipt, nil
}

func traceReceiptStages(compiled lower.CompiledRecipe) map[string][]explorer.ReceiptConstructionStage {
	stages := make(map[string][]explorer.ReceiptConstructionStage, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		outputStages := make([]explorer.ReceiptConstructionStage, 0, len(output.Stages))
		for index, descriptor := range output.Stages {
			stage := explorer.ReceiptConstructionStage{
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
				stage.Columns = append(stage.Columns, explorer.ReceiptConstructionStageColumn{
					ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Kind,
					Cardinality: expression.Cardinality(column.Cardinality),
				})
			}
			for _, capability := range descriptor.Capabilities {
				reasonCode, reason := capability.ReasonCode, capability.Reason
				if capability.Supported {
					reasonCode, reason = "", ""
				}
				stage.Capabilities = append(stage.Capabilities, explorer.ReceiptConstructionOperationChoice{
					Kind: string(capability.Operation), Supported: capability.Supported, ReasonCode: reasonCode, Reason: reason,
				})
			}
			outputStages = append(outputStages, stage)
		}
		stages[output.Name] = outputStages
	}
	return stages
}

func traceReceiptOutputSchemas(compiled lower.CompiledRecipe) map[string][]explorer.ReceiptCompiledOutputColumn {
	schemas := make(map[string][]explorer.ReceiptCompiledOutputColumn, len(compiled.Outputs))
	for _, output := range compiled.Outputs {
		columns := make([]explorer.ReceiptCompiledOutputColumn, 0, len(output.OutputSchema))
		for _, column := range output.OutputSchema {
			columns = append(columns, explorer.ReceiptCompiledOutputColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, LogicalType: column.Kind,
				Cardinality: column.Cardinality, Nullable: column.Nullable, Internal: column.Internal, Identity: column.Identity,
			})
		}
		schemas[output.Name] = columns
	}
	return schemas
}

func traceOutputProvenance(columns []explorer.EmittedColumn) map[string]map[string]string {
	provenance := make(map[string]map[string]string)
	for _, column := range columns {
		if provenance[column.OutputID] == nil {
			provenance[column.OutputID] = make(map[string]string)
		}
		provenance[column.OutputID][column.PublicColumn] = "EXPLICIT"
	}
	return provenance
}

func traceOutputFingerprints(compiled lower.CompiledRecipe) (map[string]string, error) {
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

func traceStageColumnType(step authoringv2.ConstructionStep, columnID string) string {
	for _, column := range step.Outputs {
		if column.ID == columnID {
			return column.Type
		}
	}
	return ""
}

func traceReceiptStageColumnType(receipt *explorer.CompilationReceipt, stageID, columnID string) string {
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

func emittedPublicColumns(receipt *explorer.CompilationReceipt) []string {
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
type operationHistoryReplaySummary struct {
	SchemaVersion                         int                          `json:"schemaVersion"`
	TraceCaseCount                        int                          `json:"traceCaseCount"`
	CandidateCaseCount                    int                          `json:"candidateCaseCount"`
	ReplayedCaseCount                     int                          `json:"replayedCaseCount"`
	SkippedCaseCount                      int                          `json:"skippedCaseCount"`
	ProposalCheckpointCount               int                          `json:"proposalCheckpointCount"`
	UniqueRequestFingerprintCount         int                          `json:"uniqueRequestFingerprintCount"`
	UniqueAbstractRequestFingerprintCount int                          `json:"uniqueAbstractRequestFingerprintCount"`
	UniqueRawRequestFingerprintCount      int                          `json:"uniqueRawRequestFingerprintCount"`
	ReadyProposalCount                    int                          `json:"readyProposalCount"`
	NeedsRepairProposalCount              int                          `json:"needsRepairProposalCount"`
	ApplyInvocationCount                  int                          `json:"applyInvocationCount"`
	ApplyAckSuccessCount                  int                          `json:"applyAckSuccessCount"`
	ApplyAckFailureCount                  int                          `json:"applyAckFailureCount"`
	ServicePreviewCount                   int                          `json:"servicePreviewCount"`
	Cases                                 []operationHistoryReplayCase `json:"cases"`
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
	ActionIndex        int                            `json:"actionIndex"`
	RequestID          string                         `json:"requestId"`
	RequestFingerprint string                         `json:"requestFingerprint"`
	ProposalResponse   ConstructionProposalResponse   `json:"proposalResponse"`
	PreviewResult      *operationHistoryPreviewResult `json:"previewResult,omitempty"`
}
type operationHistoryPreviewResult struct {
	ReceiptID string                            `json:"receiptId"`
	OutputID  string                            `json:"outputId"`
	Columns   []explorer.EmittedColumn          `json:"columns"`
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

	summary := operationHistoryReplaySummary{SchemaVersion: 1, TraceCaseCount: len(traces.Cases), CandidateCaseCount: len(candidates.Cases)}
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
		lastProposal := ConstructionProposalResponse{}
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
				if !reflect.DeepEqual(req.CandidateConstruction, captured) || req.ChangedStepID != cp.ChangedStepID || !equalStringSlices(req.RemoveStepIDs, cp.RemoveStepIDs) {
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
					var doc authoringv2.Document
					if err := json.Unmarshal(cp.AcceptedDocument, &doc); err != nil {
						t.Fatalf("trace %s action %d decode acceptedDocument: %v", traceID, actionIndex, err)
					}
					if current := proposalDocument(accepted, req.OutputID); current == nil || !reflect.DeepEqual(doc, *current) {
						t.Fatalf("trace %s action %d acceptedDocument differs from acceptedWorkspace", traceID, actionIndex)
					}
				}
				baseCompiles, candidateCompiles := stats.base, stats.candidate
				before := append([]byte(nil), store.created.DraftConfig...)
				beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
				baseDoc := proposalDocument(accepted, req.OutputID)
				if baseDoc == nil || baseDoc.Construction == nil {
					t.Fatalf("trace %s action %d accepted document has no construction", traceID, actionIndex)
				}
				if _, _, err := baseDoc.AnalyzeConstructionCandidate(req.CandidateConstruction, req.ChangedStepID, req.RemoveStepIDs); err != nil {
					t.Fatalf("trace %s action %d actual Document.AnalyzeConstructionCandidate: %v", traceID, actionIndex, err)
				}
				lastProposal, err = service.ProposeConstruction(context.Background(), req)
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
					preview, previewErr := service.Preview(context.Background(), PreviewRequest{
						Project: req.Project, ExplorerID: req.ExplorerID, ReceiptID: lastProposal.ProposalID, OutputID: req.OutputID, Limit: req.Limit,
						SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
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
						Columns: append([]explorer.EmittedColumn(nil), preview.Columns...), Summary: preview.Summary,
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
				applied, err := service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
					CommandID: "operation-history-" + traceID + "-" + fmt.Sprint(actionIndex), SemanticsVersion: authoringv2.CurrentSemanticsVersion,
					SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
					Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: lastProposal.OutputID, ProposalID: lastProposal.ProposalID}},
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
				appliedDoc := proposalDocument(applied.Workspace, lastProposal.OutputID)
				persistedDoc := proposalDocument(persistedWorkspace, lastProposal.OutputID)
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
		var workspace authoringv2.Workspace
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
		prepareRequest := ConstructionProposalRequest{Project: workspaceContext.Project, ExplorerID: workspaceContext.ExplorerID, SnapshotToken: identity.SnapshotToken,
			ExpectedDraftVersion: identity.AcceptedVersion, ExpectedDraftDigest: identity.AcceptedDigest, OutputID: identity.OutputID}
		service, store, _, _, stats := newOperationHistoryService(t, workspace, prepareRequest)
		if _, err := service.loadConstructionBase(context.Background(), prepareRequest.Project, prepareRequest.ExplorerID, prepareRequest.SnapshotToken,
			prepareRequest.ExpectedDraftVersion, prepareRequest.ExpectedDraftDigest, prepareRequest.OutputID); err != nil {
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

func assertTraceSeedIdentity(t *testing.T, id string, identity operationHistorySeedIdentity, workspace authoringv2.Workspace, request ConstructionProposalRequest) {
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

func decodeTraceWorkspace(t *testing.T, id string, data json.RawMessage) authoringv2.Workspace {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("trace %s has no acceptedWorkspace", id)
	}
	w, err := authoringv2.DecodeWorkspace(data)
	if err != nil {
		t.Fatalf("trace %s decode acceptedWorkspace: %v", id, err)
	}
	return w
}

func decodeTraceProposalRequest(t *testing.T, id string, data json.RawMessage) ConstructionProposalRequest {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("trace %s has no full proposalRequest", id)
	}
	var request ConstructionProposalRequest
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

func assertTraceProposalContext(t *testing.T, id string, trace operationHistoryTrace, data json.RawMessage, request ConstructionProposalRequest) {
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

func constructionFromRaw(t *testing.T, id string, actionIndex int, data json.RawMessage) authoringv2.Construction {
	t.Helper()
	var candidate authoringv2.Construction
	if len(data) == 0 || json.Unmarshal(data, &candidate) != nil {
		t.Fatalf("trace %s action %d missing/invalid candidateConstruction", id, actionIndex)
	}
	return candidate
}

func assertTraceWorkspaceMatchesRequest(t *testing.T, id string, w authoringv2.Workspace, r ConstructionProposalRequest) {
	t.Helper()
	if err := w.Validate(); err != nil {
		t.Fatalf("trace %s accepted workspace validation: %v", id, err)
	}
	digest, err := w.Digest()
	if err != nil || digest != r.ExpectedDraftDigest {
		t.Fatalf("trace %s accepted workspace digest %q != request digest %q (err %v)", id, digest, r.ExpectedDraftDigest, err)
	}
	if r.Project == "" || r.ExplorerID == "" || r.SnapshotToken == "" || r.ExpectedDraftVersion < 1 || r.OutputID == "" || proposalDocument(w, r.OutputID) == nil {
		t.Fatalf("trace %s proposal identity/output is incomplete: %#v", id, r)
	}
}

type operationHistoryCompileStats struct{ base, candidate int }

func newOperationHistoryService(t *testing.T, w authoringv2.Workspace, request ConstructionProposalRequest) (*Service, *fakeStore, capability.Snapshot, authoringv2.Workspace, *operationHistoryCompileStats) {
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
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		return traceWorkspaceCatalog(got, explorerID, w, rootID)
	}
	stats := &operationHistoryCompileStats{}
	config.CompileReceipt = func(ctx context.Context, cr CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil || receipt.ConstructionProposal == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview has no compiled proposal receipt")
		}
		return dataframeexecution.PreviewSummary{Output: request.OutputID, Columns: emittedPublicColumnsForOutput(receipt, request.OutputID), Complete: true}, nil
	}
	return newTestService(t, store, config), store, snapshot, w, stats
}

type traceSourceField struct{ ColumnID, FieldPath, FHIRType, LogicalType, Label string }

func traceWorkspaceSourceFields(w authoringv2.Workspace) []traceSourceField {
	fields := map[string]traceSourceField{}
	for _, doc := range w.Documents {
		for _, col := range doc.Columns {
			if col.Source.Kind == authoringv2.SourceField && col.Source.Field != nil {
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

func traceWorkspaceCatalog(snapshot capability.Snapshot, explorerID string, w authoringv2.Workspace, nodeID string) authoringv2.CatalogSnapshot {
	rootType := "Patient"
	if len(w.Documents) > 0 && w.Documents[0].RootResourceType != "" {
		rootType = w.Documents[0].RootResourceType
	}
	c := authoringv2.CatalogSnapshot{APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: explorerID, SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true}, Nodes: []authoringv2.CatalogNode{{ID: nodeID, ResourceType: rootType, RowRootEligible: true, RowGrain: "patient"}}, Candidates: []authoringv2.CatalogCandidate{}}
	for _, f := range traceWorkspaceSourceFields(w) {
		candidate := capability.Candidate{ID: "trace-candidate-" + f.ColumnID, NodeID: nodeID, ResourceType: rootType, FieldPath: f.FieldPath, Label: f.Label, LogicalType: f.LogicalType, Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true}
		choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
		if err != nil {
			continue
		}
		c.Candidates = append(c.Candidates, authoringv2.CatalogCandidate{ID: candidate.ID, NodeID: nodeID, FieldPath: f.FieldPath, Label: f.Label, LogicalType: f.LogicalType, Cardinality: "optional_one", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice})
	}
	return c
}

func emittedPublicColumnsForOutput(receipt *explorer.CompilationReceipt, outputID string) []string {
	var columns []string
	for _, col := range receipt.EmittedColumns {
		if col.OutputID == outputID {
			columns = append(columns, col.PublicColumn)
		}
	}
	return columns
}

func workspaceFromDraft(t *testing.T, id string, data []byte) authoringv2.Workspace {
	t.Helper()
	w, err := authoringv2.DecodeWorkspace(data)
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

func sameOperationHistoryExpectation(a, b operationHistoryExpectation) bool {
	return a.ProposalStatus == b.ProposalStatus && equalStringSlices(a.StepIDs, b.StepIDs) && reflect.DeepEqual(a.GroupOutputTypes, b.GroupOutputTypes) && reflect.DeepEqual(a.DownstreamInputTypes, b.DownstreamInputTypes) && reflect.DeepEqual(a.DownstreamOutputTypes, b.DownstreamOutputTypes) && a.CandidateGroupKeyOutputID == b.CandidateGroupKeyOutputID && a.AcceptedGroupKeyOutputID == b.AcceptedGroupKeyOutputID && a.GroupKeyOutputRelation == b.GroupKeyOutputRelation && a.AcceptedAggregate == b.AcceptedAggregate && a.CandidateAggregate == b.CandidateAggregate && a.AcceptedChangesOnApply == b.AcceptedChangesOnApply && a.ApplyAck == b.ApplyAck && equalTraceMissingInputs(a.MissingInputs, b.MissingInputs) && equalStringSlices(a.RemovedStepIDs, b.RemovedStepIDs)
}

func assertOperationHistoryProposal(t *testing.T, id string, actionIndex int, aggregateOutputID string, expected operationHistoryExpectation, accepted authoringv2.Construction, proposal ConstructionProposalResponse, receipt *explorer.CompilationReceipt, outputID string) {
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
	acceptedSteps := make(map[string]authoringv2.ConstructionStep, len(accepted.Steps))
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
	if !equalTraceMissingInputs(missing, wantMissing) {
		t.Fatalf("trace %s action %d missing inputs %#v != %#v", id, actionIndex, missing, wantMissing)
	}
	if !equalStringSlices(proposal.DependencyImpact.RemovedStepIDs, expected.RemovedStepIDs) {
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

func traceConstructionStepPosition(construction authoringv2.Construction, stepID string) int {
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

func constructionGroupOutputIdentity(construction authoringv2.Construction) (keyID, keyType, aggregateID, aggregateType string) {
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

func actualTraceGroupOutputTypes(construction authoringv2.Construction) map[string]*string {
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

func actualTraceDownstreamInputTypes(construction authoringv2.Construction, expected operationHistoryExpectation) map[string]string {
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

func actualTraceDownstreamOutputTypes(receipt *explorer.CompilationReceipt, outputID string, expected operationHistoryExpectation) map[string]string {
	result := make(map[string]string, len(expected.DownstreamOutputTypes))
	for stepID, output := range expected.DownstreamOutputTypes {
		result[stepID] = traceReceiptStageColumnTypeForOutput(receipt, outputID, stepID, output.ColumnID)
	}
	return result
}

func traceStepColumnType(construction authoringv2.Construction, stepID, columnID string) string {
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

func traceStepInputID(construction authoringv2.Construction, stepID string) string {
	for _, step := range construction.Steps {
		if step.ID != stepID {
			continue
		}
		for _, input := range step.Inputs {
			if input.Kind == authoringv2.ConstructionInputStepOutput {
				return input.StepID
			}
		}
	}
	return ""
}

func traceReceiptStageInputID(receipt *explorer.CompilationReceipt, outputID, stageID string) string {
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

func traceReceiptStageColumnTypeForOutput(receipt *explorer.CompilationReceipt, outputID, stageID, columnID string) string {
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
