package explorer

// This file contains the immutable boundary between Explorer authoring and
// execution. A receipt is a durable, server-owned description of one exact
// compilation. It deliberately contains a resolved semantic recipe, not
// physical plans, rendered AQL, bind variables, or storage identifiers.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

const (
	// CompilationReceiptFormatVersion changes when the persisted receipt shape
	// or its execution invariants change incompatibly.
	CompilationReceiptFormatVersion         = 6
	previousCompilationReceiptFormatVersion = 5
	legacyCompilationReceiptFormatVersion   = 3
	// CompilationReceiptCompilerContractVersion changes when compilation
	// semantics change in a way that can alter a resolved receipt.
	CompilationReceiptCompilerContractVersion          = "loom.explorer.compiler/v18"
	legacyCompilationReceiptV17CompilerContractVersion = "loom.explorer.compiler/v17"
	legacyCompilationReceiptV16CompilerContractVersion = "loom.explorer.compiler/v16"
	previousCompilationReceiptCompilerContractVersion  = "loom.explorer.compiler/v15"
	legacyCompilationReceiptB06CompilerContractVersion = "loom.explorer.compiler/v14"
	legacyCompilationReceiptV13CompilerContractVersion = "loom.explorer.compiler/v13"
	legacyCompilationReceiptCompilerContractVersion    = "loom.explorer.compiler/v12"
	legacyCompilationReceiptOlderContractVersion       = "loom.explorer.compiler/v11"
	legacyCompilationReceiptV10ContractVersion         = "loom.explorer.compiler/v10"

	// Short aliases make the current contract convenient for repositories and
	// callers that do not need to distinguish the receipt prefix.
	CurrentReceiptFormatVersion       = CompilationReceiptFormatVersion
	CurrentCompilerContractVersion    = CompilationReceiptCompilerContractVersion
	CurrentCompilationContractVersion = CompilationReceiptCompilerContractVersion
)

// ErrReceiptRecompileRequired is returned for an old receipt that has no
// resolved recipe artifact. Such a receipt cannot be safely upgraded by an
// execution request because doing so would reinterpret authoring intent.
var ErrReceiptRecompileRequired = errors.New("RECEIPT_RECOMPILE_REQUIRED")

// ReceiptContractSupportedForExecution distinguishes read compatibility from
// execution compatibility. The immediately previous v4/v14 artifact remains
// executable because it contains a complete frozen recipe; older historical
// contracts remain readable for migration/inspection but must be recompiled
// before native execution.
func ReceiptContractSupportedForExecution(r CompilationReceipt) bool {
	return (r.ReceiptFormatVersion == CompilationReceiptFormatVersion && r.CompilerContractVersion == CompilationReceiptCompilerContractVersion) ||
		(r.ReceiptFormatVersion == previousCompilationReceiptFormatVersion && r.CompilerContractVersion == previousCompilationReceiptCompilerContractVersion)
}

// CompilationArtifactDigest returns the content identity used for canonical
// JSON artifacts embedded in a receipt, such as the public output contract.
func CompilationArtifactDigest(raw json.RawMessage) (string, error) {
	canonical, err := canonicalRaw(raw)
	if err != nil {
		return "", err
	}
	if len(canonical) == 0 {
		return "", fmt.Errorf("compilation artifact is required")
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CompilationReceipt is immutable once persisted and content-addressed by
// ReceiptID. NormalizedBundle is authoring intent retained for export and
// compatibility; Bundle is the resolved recipe used by execution. Neither
// field contains a physical IR or rendered query.
type CompilationReceipt struct {
	ConstructionProposal     *ConstructionProposalBinding          `json:"constructionProposal,omitempty"`
	ID                       string                                `json:"id"`
	ReceiptFormatVersion     int                                   `json:"receiptFormatVersion"`
	CompilerContractVersion  string                                `json:"compilerContractVersion"`
	Project                  string                                `json:"project"`
	ExplorerID               string                                `json:"explorerId"`
	IntentDigest             string                                `json:"intentDigest"`
	ResolvedInputsDigest     string                                `json:"resolvedInputsDigest,omitempty"`
	ResolvedInterpretations  []ResolvedInterpretation              `json:"resolvedInterpretations,omitempty"`
	SnapshotToken            string                                `json:"snapshotToken"`
	AuthorizationScopeDigest string                                `json:"authorizationScopeDigest,omitempty"`
	CapabilitySchemaDigest   string                                `json:"capabilitySchemaDigest,omitempty"`
	ShapeDigest              string                                `json:"shapeDigest,omitempty"`
	SourceGeneration         string                                `json:"sourceGeneration"`
	CompilationKey           string                                `json:"compilationKey,omitempty"`
	RecipeDigest             string                                `json:"recipeDigest"`
	ResolvedRecipeDigest     string                                `json:"resolvedRecipeDigest,omitempty"`
	ResolvedSchemaDigest     string                                `json:"resolvedSchemaDigest,omitempty"`
	OutputContractDigest     string                                `json:"outputContractDigest,omitempty"`
	NormalizedBundle         json.RawMessage                       `json:"normalizedBundle"`
	Bundle                   recipe.Bundle                         `json:"compiledRecipe"`
	CompiledConfig           json.RawMessage                       `json:"compiledConfig,omitempty"`
	PublicOutputContract     json.RawMessage                       `json:"publicOutputContract,omitempty"`
	IdentityMappings         []IdentityMapping                     `json:"identityMappings"`
	EmittedColumns           []EmittedColumn                       `json:"emittedColumns"`
	OutputFingerprints       map[string]string                     `json:"outputFingerprints,omitempty"`
	ConstructionStages       map[string][]ReceiptConstructionStage `json:"constructionStages,omitempty"`
	// OutputColumnProvenance is the durable publication behavior for every
	// compiler output column. Recipe Discovered flags are compiler-local and
	// intentionally do not cross the authoring recipe JSON boundary.
	OutputColumnProvenance map[string]map[string]string  `json:"outputColumnProvenance,omitempty"`
	Warnings               []CompilationWarning          `json:"warnings,omitempty"`
	RowDefinitionProposal  *RowDefinitionProposalBinding `json:"rowDefinitionProposal,omitempty"`
	TableShapeProposal     *TableShapeProposalBinding    `json:"tableShapeProposal,omitempty"`
	RequestID              string                        `json:"requestId,omitempty"`
	CreatedAt              time.Time                     `json:"createdAt"`
}

// RowDefinitionProposalBinding freezes the exact draft and output from which a
// candidate row-definition workspace was compiled. It is optional because
// ordinary compilation receipts are not row-definition proposals.
type RowDefinitionProposalBinding struct {
	DraftVersion             int64  `json:"draftVersion"`
	DraftDigest              string `json:"draftDigest"`
	OutputID                 string `json:"outputId"`
	BaseDocumentDigest       string `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string `json:"candidateWorkspaceDigest"`
	SnapshotToken            string `json:"snapshotToken"`
}

func (b RowDefinitionProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if b.DraftVersion < 1 {
		return fmt.Errorf("row-definition proposal draftVersion must be positive")
	}
	for name, value := range map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID,
		"baseDocumentDigest":       b.BaseDocumentDigest,
		"candidateWorkspaceDigest": b.CandidateWorkspaceDigest,
		"snapshotToken":            b.SnapshotToken,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("row-definition proposal %s must be an exact non-empty value", name)
		}
	}
	if b.CandidateWorkspaceDigest != intentDigest {
		return fmt.Errorf("row-definition proposal candidate workspace digest does not match receipt intent")
	}
	if b.SnapshotToken != snapshotToken {
		return fmt.Errorf("row-definition proposal snapshot token does not match receipt snapshot")
	}
	return nil
}

// TableShapeProposalBinding freezes the exact draft and output from which a
// candidate table-shape workspace was compiled. It is optional because
// ordinary compilation receipts are not table-shape proposals.
type TableShapeProposalBinding struct {
	DraftVersion             int64  `json:"draftVersion"`
	DraftDigest              string `json:"draftDigest"`
	OutputID                 string `json:"outputId"`
	BaseDocumentDigest       string `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string `json:"candidateWorkspaceDigest"`
	SnapshotToken            string `json:"snapshotToken"`
}

func (b TableShapeProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if b.DraftVersion < 1 {
		return fmt.Errorf("table-shape proposal draftVersion must be positive")
	}
	for name, value := range map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID,
		"baseDocumentDigest":       b.BaseDocumentDigest,
		"candidateWorkspaceDigest": b.CandidateWorkspaceDigest,
		"snapshotToken":            b.SnapshotToken,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("table-shape proposal %s must be an exact non-empty value", name)
		}
	}
	if b.CandidateWorkspaceDigest != intentDigest {
		return fmt.Errorf("table-shape proposal candidate workspace digest does not match receipt intent")
	}
	if b.SnapshotToken != snapshotToken {
		return fmt.Errorf("table-shape proposal snapshot token does not match receipt snapshot")
	}
	return nil
}

// ConstructionProposalBinding freezes the saved base and exact typed candidate
// for an operation proposal. CandidateWorkspaceDigest covers all stage inputs,
// including immutable table revisions.
type ConstructionProposalBinding struct {
	DraftVersion             int64    `json:"draftVersion"`
	DraftDigest              string   `json:"draftDigest"`
	OutputID                 string   `json:"outputId"`
	ChangedStepID            string   `json:"changedStepId"`
	RemoveStepIDs            []string `json:"removeStepIds,omitempty"`
	BaseDocumentDigest       string   `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string   `json:"candidateWorkspaceDigest"`
	SnapshotToken            string   `json:"snapshotToken"`
	PreviewLimit             int      `json:"previewLimit"`
}

func (b ConstructionProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if b.DraftVersion < 1 {
		return fmt.Errorf("construction proposal draftVersion must be positive")
	}
	for name, value := range map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID,
		"baseDocumentDigest": b.BaseDocumentDigest, "candidateWorkspaceDigest": b.CandidateWorkspaceDigest,
		"snapshotToken": b.SnapshotToken,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("construction proposal %s must be an exact non-empty value", name)
		}
	}
	if b.ChangedStepID == "" {
		if len(b.RemoveStepIDs) == 0 {
			return fmt.Errorf("construction proposal requires changedStepId or removeStepIds")
		}
	} else if strings.TrimSpace(b.ChangedStepID) != b.ChangedStepID {
		return fmt.Errorf("construction proposal changedStepId must be exact")
	}
	if b.CandidateWorkspaceDigest != intentDigest {
		return fmt.Errorf("construction proposal candidate workspace digest does not match receipt intent")
	}
	if b.SnapshotToken != snapshotToken {
		return fmt.Errorf("construction proposal snapshot token does not match receipt snapshot")
	}
	if b.PreviewLimit < 1 {
		return fmt.Errorf("construction proposal previewLimit must be positive")
	}
	seenRemovals := make(map[string]struct{}, len(b.RemoveStepIDs))
	for _, id := range b.RemoveStepIDs {
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("construction proposal removeStepIds must contain exact non-empty values")
		}
		if _, exists := seenRemovals[id]; exists {
			return fmt.Errorf("construction proposal removeStepIds contains duplicate %q", id)
		}
		if id == b.ChangedStepID {
			return fmt.Errorf("construction proposal changedStepId cannot also be removed")
		}
		seenRemovals[id] = struct{}{}
	}
	return nil
}

// ReceiptConstructionStage preserves the exact inferred schema and supported
// operation set for a stable stage in a compiled output.
type ReceiptConstructionStage struct {
	ID                   string                                   `json:"id"`
	InputStageID         string                                   `json:"inputStageId"`
	Operation            string                                   `json:"operation,omitempty"`
	RowIdentityColumn    string                                   `json:"rowIdentityColumn,omitempty"`
	Columns              []ReceiptConstructionStageColumn         `json:"columns"`
	Capabilities         []ReceiptConstructionOperationChoice     `json:"capabilities"`
	RelatedExpandAnchors []ReceiptConstructionRelatedExpandAnchor `json:"relatedExpandAnchors,omitempty"`
	RelatedExpand        *ReceiptConstructionRelatedExpand        `json:"relatedExpand,omitempty"`
	ActiveRelatedRecord  *ReceiptConstructionActiveRelatedRecord  `json:"activeRelatedRecord,omitempty"`
	CodedGroupChoices    []ReceiptConstructionCodedGroupChoice    `json:"codedGroupChoices,omitempty"`
}

// ReceiptConstructionCodedGroupChoice is attached only to a current
// construction-capabilities response. Its choice ID is snapshot-bound; the
// persisted operation stores the exact source path facts separately.
type ReceiptConstructionCodedGroupChoice struct {
	ChoiceID     string `json:"choiceId"`
	OccurrenceID string `json:"occurrenceId"`
	ResourceType string `json:"resourceType"`
	CodingPath   string `json:"codingPath"`
	Label        string `json:"label"`
}

type ReceiptConstructionRelatedExpandAnchor struct {
	AnchorColumnID string `json:"anchorColumnId"`
	Kind           string `json:"kind"`
	NodeID         string `json:"nodeId,omitempty"`
	ResourceType   string `json:"resourceType"`
	Label          string `json:"label"`
}

type ReceiptConstructionRelatedExpand struct {
	AnchorColumnID         string                                `json:"anchorColumnId"`
	AnchorColumn           string                                `json:"anchorColumn"`
	AnchorKind             string                                `json:"anchorKind"`
	AnchorNodeID           string                                `json:"anchorNodeId,omitempty"`
	AnchorResourceType     string                                `json:"anchorResourceType"`
	RelatedRecordColumnID  string                                `json:"relatedRecordColumnId"`
	ParentIdentityColumnID string                                `json:"parentIdentityColumnId"`
	ParentIdentityColumn   string                                `json:"parentIdentityColumn"`
	TerminalIdentityColumn string                                `json:"terminalIdentityColumn"`
	TargetNodeID           string                                `json:"targetNodeId"`
	TargetResourceType     string                                `json:"targetResourceType"`
	Route                  []recipe.ConstructionRelatedRouteStep `json:"route"`
}

type ReceiptConstructionActiveRelatedRecord struct {
	TargetNodeID           string `json:"targetNodeId"`
	TargetResourceType     string `json:"targetResourceType"`
	TerminalIdentityColumn string `json:"terminalIdentityColumn"`
}

type ReceiptConstructionStageColumn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
	// Cardinality is empty only on receipts created before stage cardinality was recorded.
	Cardinality expression.Cardinality `json:"cardinality,omitempty"`
}

type ReceiptConstructionOperationChoice struct {
	Kind       string `json:"kind"`
	Supported  bool   `json:"supported"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func validateReceiptConstructionStages(stagesByOutput map[string][]ReceiptConstructionStage) error {
	for outputID, stages := range stagesByOutput {
		if strings.TrimSpace(outputID) == "" || outputID != strings.TrimSpace(outputID) {
			return fmt.Errorf("constructionStages requires exact output IDs")
		}
		if len(stages) == 0 {
			return fmt.Errorf("constructionStages[%q] must include the source projection", outputID)
		}
		seenStageIDs := make(map[string]struct{}, len(stages))
		priorStageID := ""
		for index, stage := range stages {
			if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) {
				return fmt.Errorf("constructionStages[%q][%d] requires an exact id", outputID, index)
			}
			if _, exists := seenStageIDs[stage.ID]; exists {
				return fmt.Errorf("constructionStages[%q][%d] duplicates stage id %q", outputID, index, stage.ID)
			}
			seenStageIDs[stage.ID] = struct{}{}
			if index == 0 {
				if stage.ID != recipe.ConstructionSourceProjectionID || stage.InputStageID != "" || stage.Operation != "" {
					return fmt.Errorf("constructionStages[%q][0] must be the implicit source projection", outputID)
				}
			} else if stage.InputStageID != priorStageID || stage.ID == recipe.ConstructionSourceProjectionID || strings.TrimSpace(stage.Operation) == "" {
				return fmt.Errorf("constructionStages[%q][%d] does not follow its previous stage", outputID, index)
			}
			priorStageID = stage.ID
			seenColumns := make(map[string]struct{}, len(stage.Columns))
			seenNames := make(map[string]struct{}, len(stage.Columns))
			for columnIndex, column := range stage.Columns {
				if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) ||
					strings.TrimSpace(column.Name) == "" || column.Name != strings.TrimSpace(column.Name) ||
					strings.TrimSpace(column.Label) == "" || column.Label != strings.TrimSpace(column.Label) {
					return fmt.Errorf("constructionStages[%q][%d].columns[%d] requires exact id, name, and label", outputID, index, columnIndex)
				}
				if _, exists := seenColumns[column.ID]; exists {
					return fmt.Errorf("constructionStages[%q][%d] duplicates column id %q", outputID, index, column.ID)
				}
				if _, exists := seenNames[column.Name]; exists {
					return fmt.Errorf("constructionStages[%q][%d] duplicates public column name %q", outputID, index, column.Name)
				}
				if column.Cardinality != "" && !column.Cardinality.Valid() {
					return fmt.Errorf("constructionStages[%q][%d].columns[%d] has unsupported cardinality %q", outputID, index, columnIndex, column.Cardinality)
				}
				seenColumns[column.ID] = struct{}{}
				seenNames[column.Name] = struct{}{}
			}
			seenOperations := make(map[string]struct{}, len(stage.Capabilities))
			for choiceIndex, choice := range stage.Capabilities {
				switch choice.Kind {
				case "PIVOT", "DERIVE", "FILTER", "UNPIVOT", "GROUP", "CODED_GROUP", "EXPAND", "RELATED_SOURCE", "RELATED_EXPAND", "RELATED_ELIGIBILITY", "RELATED_FIELD":
				default:
					return fmt.Errorf("constructionStages[%q][%d].capabilities[%d] has unsupported operation %q", outputID, index, choiceIndex, choice.Kind)
				}
				if _, exists := seenOperations[choice.Kind]; exists {
					return fmt.Errorf("constructionStages[%q][%d] duplicates capability for %q", outputID, index, choice.Kind)
				}
				seenOperations[choice.Kind] = struct{}{}
				if !choice.Supported && (strings.TrimSpace(choice.ReasonCode) == "" || strings.TrimSpace(choice.Reason) == "") {
					return fmt.Errorf("constructionStages[%q][%d].capabilities[%d] needs a reason for unsupported operation %q", outputID, index, choiceIndex, choice.Kind)
				}
			}
			anchorIDs := make(map[string]ReceiptConstructionRelatedExpandAnchor, len(stage.RelatedExpandAnchors))
			for anchorIndex, anchor := range stage.RelatedExpandAnchors {
				if strings.TrimSpace(anchor.AnchorColumnID) == "" || anchor.AnchorColumnID != strings.TrimSpace(anchor.AnchorColumnID) ||
					strings.TrimSpace(anchor.ResourceType) == "" || anchor.ResourceType != strings.TrimSpace(anchor.ResourceType) ||
					strings.TrimSpace(anchor.Label) == "" || anchor.Label != strings.TrimSpace(anchor.Label) {
					return fmt.Errorf("constructionStages[%q][%d].relatedExpandAnchors[%d] requires exact column, type, and label", outputID, index, anchorIndex)
				}
				if _, exists := anchorIDs[anchor.AnchorColumnID]; exists {
					return fmt.Errorf("constructionStages[%q][%d] duplicates related expansion anchor %q", outputID, index, anchor.AnchorColumnID)
				}
				switch anchor.Kind {
				case "root":
					if anchor.AnchorColumnID != "_key" || anchor.NodeID != "" {
						return fmt.Errorf("constructionStages[%q][%d] root expansion anchor must be _key without a node override", outputID, index)
					}
				case "activeRelatedRecord":
					if strings.TrimSpace(anchor.NodeID) == "" || anchor.NodeID != strings.TrimSpace(anchor.NodeID) {
						return fmt.Errorf("constructionStages[%q][%d] active related-record anchor requires an exact node id", outputID, index)
					}
				default:
					return fmt.Errorf("constructionStages[%q][%d] has unsupported related expansion anchor kind %q", outputID, index, anchor.Kind)
				}
				anchorIDs[anchor.AnchorColumnID] = anchor
			}
			if stage.Operation == "RELATED_EXPAND" {
				if stage.RelatedExpand == nil || stage.RelatedExpand.AnchorColumnID == "" || stage.RelatedExpand.AnchorColumn != stage.RelatedExpand.AnchorColumnID ||
					stage.RelatedExpand.AnchorKind == "" || stage.RelatedExpand.AnchorResourceType == "" ||
					stage.RelatedExpand.RelatedRecordColumnID == "" || stage.RelatedExpand.ParentIdentityColumnID == "" ||
					stage.RelatedExpand.ParentIdentityColumn == "" || stage.RelatedExpand.TerminalIdentityColumn == "" ||
					stage.RelatedExpand.TargetNodeID == "" || stage.RelatedExpand.TargetResourceType == "" || len(stage.RelatedExpand.Route) == 0 {
					return fmt.Errorf("constructionStages[%q][%d] RELATED_EXPAND lacks its exact target and identity metadata", outputID, index)
				}
				var inputStage *ReceiptConstructionStage
				for inputIndex := range stages {
					if stages[inputIndex].ID == stage.InputStageID {
						inputStage = &stages[inputIndex]
						break
					}
				}
				var inputAnchor *ReceiptConstructionRelatedExpandAnchor
				if inputStage != nil {
					for anchorIndex := range inputStage.RelatedExpandAnchors {
						if inputStage.RelatedExpandAnchors[anchorIndex].AnchorColumnID == stage.RelatedExpand.AnchorColumnID {
							inputAnchor = &inputStage.RelatedExpandAnchors[anchorIndex]
							break
						}
					}
				}
				if inputAnchor == nil || inputAnchor.Kind != stage.RelatedExpand.AnchorKind || inputAnchor.ResourceType != stage.RelatedExpand.AnchorResourceType ||
					(inputAnchor.Kind == "activeRelatedRecord" && inputAnchor.NodeID != stage.RelatedExpand.AnchorNodeID) {
					return fmt.Errorf("constructionStages[%q][%d] RELATED_EXPAND anchor differs from the compiler-proven input stage anchors", outputID, index)
				}
				foundOutput := false
				for _, column := range stage.Columns {
					if column.ID == stage.RelatedExpand.RelatedRecordColumnID {
						foundOutput = true
						break
					}
				}
				if !foundOutput {
					return fmt.Errorf("constructionStages[%q][%d] RELATED_EXPAND output ID is absent from stage columns", outputID, index)
				}
				priorNode, priorResource := stage.RelatedExpand.Route[0].FromNodeID, stage.RelatedExpand.Route[0].FromResourceType
				for routeIndex, hop := range stage.RelatedExpand.Route {
					if hop.EdgeID == "" || hop.FromNodeID != priorNode || hop.FromResourceType != priorResource || hop.ToNodeID == "" ||
						hop.ToResourceType == "" || hop.Relationship == "" || (hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
						(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
						return fmt.Errorf("constructionStages[%q][%d].relatedExpand.route[%d] is invalid or discontinuous", outputID, index, routeIndex)
					}
					priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
				}
				if priorNode != stage.RelatedExpand.TargetNodeID || priorResource != stage.RelatedExpand.TargetResourceType {
					return fmt.Errorf("constructionStages[%q][%d] RELATED_EXPAND route terminal differs from target", outputID, index)
				}
			} else if stage.RelatedExpand != nil {
				return fmt.Errorf("constructionStages[%q][%d] has related-expansion metadata for %q", outputID, index, stage.Operation)
			}
			if stage.ActiveRelatedRecord != nil {
				active := stage.ActiveRelatedRecord
				if strings.TrimSpace(active.TargetNodeID) == "" || active.TargetNodeID != strings.TrimSpace(active.TargetNodeID) ||
					strings.TrimSpace(active.TargetResourceType) == "" || active.TargetResourceType != strings.TrimSpace(active.TargetResourceType) ||
					strings.TrimSpace(active.TerminalIdentityColumn) == "" || active.TerminalIdentityColumn != strings.TrimSpace(active.TerminalIdentityColumn) {
					return fmt.Errorf("constructionStages[%q][%d].activeRelatedRecord requires exact target and terminal identity fields", outputID, index)
				}
				switch stage.Operation {
				case "RELATED_EXPAND":
					if active.TargetNodeID != stage.RelatedExpand.TargetNodeID || active.TargetResourceType != stage.RelatedExpand.TargetResourceType || active.TerminalIdentityColumn != stage.RelatedExpand.TerminalIdentityColumn {
						return fmt.Errorf("constructionStages[%q][%d] active terminal identity differs from RELATED_EXPAND metadata", outputID, index)
					}
				case "FILTER", "DERIVE", "RELATED_SOURCE", "RELATED_ELIGIBILITY", "RELATED_FIELD":
				default:
					return fmt.Errorf("constructionStages[%q][%d] cannot carry an active related record through %q", outputID, index, stage.Operation)
				}
				activeAnchor, foundActiveAnchor := anchorIDs[active.TerminalIdentityColumn]
				if !foundActiveAnchor || activeAnchor.Kind != "activeRelatedRecord" || activeAnchor.NodeID != active.TargetNodeID || activeAnchor.ResourceType != active.TargetResourceType {
					return fmt.Errorf("constructionStages[%q][%d] active related record is absent from compiler-proven expansion anchors", outputID, index)
				}
			} else if stage.Operation == "RELATED_FIELD" {
				return fmt.Errorf("constructionStages[%q][%d] RELATED_FIELD lacks its active exact terminal record", outputID, index)
			}
		}
	}
	return nil
}

type IdentityMapping struct {
	OutputID       string   `json:"outputId,omitempty"`
	CandidateID    string   `json:"candidateId"`
	OccurrenceID   string   `json:"occurrenceId"`
	ProjectionMode string   `json:"projectionMode,omitempty"`
	EmissionIDs    []string `json:"emissionIds"`
}

// CompilationWarning is the deterministic, request-independent diagnostic
// subset that can be frozen in a receipt. Request IDs and timestamps must not
// be included here because they would make equivalent receipts differ.
type CompilationWarning struct {
	Severity  string         `json:"severity,omitempty"`
	Code      string         `json:"code"`
	Stage     string         `json:"stage,omitempty"`
	FieldPath string         `json:"fieldPath,omitempty"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// CompilationKey returns the idempotency identity for one authoring request.
// It covers semantic inputs and compiler contracts, but not resolved output;
// this permits a repository lookup before doing the expensive compilation.
func CompilationKey(r CompilationReceipt) (string, error) {
	identity := struct {
		ReceiptFormatVersion    int                                   `json:"receiptFormatVersion"`
		CompilerContractVersion string                                `json:"compilerContractVersion"`
		Project                 string                                `json:"project"`
		ExplorerID              string                                `json:"explorerId"`
		IntentDigest            string                                `json:"intentDigest"`
		ResolvedInputsDigest    string                                `json:"resolvedInputsDigest,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation              `json:"resolvedInterpretations,omitempty"`
		NormalizedBundle        []byte                                `json:"normalizedBundle,omitempty"`
		SnapshotToken           string                                `json:"snapshotToken"`
		AuthorizationScope      string                                `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string                                `json:"capabilitySchemaDigest,omitempty"`
		ShapeDigest             string                                `json:"shapeDigest,omitempty"`
		SourceGeneration        string                                `json:"sourceGeneration"`
		RowDefinitionProposal   *RowDefinitionProposalBinding         `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding            `json:"tableShapeProposal,omitempty"`
		ConstructionProposal    *ConstructionProposalBinding          `json:"constructionProposal,omitempty"`
		ConstructionStages      map[string][]ReceiptConstructionStage `json:"constructionStages,omitempty"`
	}{}
	normalized, err := canonicalRaw(r.NormalizedBundle)
	if err != nil {
		return "", fmt.Errorf("canonical normalized bundle: %w", err)
	}
	identity = struct {
		ReceiptFormatVersion    int                                   `json:"receiptFormatVersion"`
		CompilerContractVersion string                                `json:"compilerContractVersion"`
		Project                 string                                `json:"project"`
		ExplorerID              string                                `json:"explorerId"`
		IntentDigest            string                                `json:"intentDigest"`
		ResolvedInputsDigest    string                                `json:"resolvedInputsDigest,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation              `json:"resolvedInterpretations,omitempty"`
		NormalizedBundle        []byte                                `json:"normalizedBundle,omitempty"`
		SnapshotToken           string                                `json:"snapshotToken"`
		AuthorizationScope      string                                `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string                                `json:"capabilitySchemaDigest,omitempty"`
		ShapeDigest             string                                `json:"shapeDigest,omitempty"`
		SourceGeneration        string                                `json:"sourceGeneration"`
		RowDefinitionProposal   *RowDefinitionProposalBinding         `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding            `json:"tableShapeProposal,omitempty"`
		ConstructionProposal    *ConstructionProposalBinding          `json:"constructionProposal,omitempty"`
		ConstructionStages      map[string][]ReceiptConstructionStage `json:"constructionStages,omitempty"`
	}{
		r.ReceiptFormatVersion, r.CompilerContractVersion, r.Project, r.ExplorerID,
		r.IntentDigest, r.ResolvedInputsDigest, r.ResolvedInterpretations, normalized, r.SnapshotToken,
		r.AuthorizationScopeDigest, r.CapabilitySchemaDigest, r.ShapeDigest, r.SourceGeneration, r.RowDefinitionProposal, r.TableShapeProposal,
		r.ConstructionProposal, r.ConstructionStages,
	}
	return digestIdentity("compile_", identity)
}

// ReceiptID returns the content identity for the complete immutable artifact.
// ID, RequestID, and CreatedAt are intentionally excluded.
func ReceiptID(r CompilationReceipt) (string, error) {
	key, err := CompilationKey(r)
	if err != nil {
		return "", err
	}
	compiledConfig, err := canonicalRaw(r.CompiledConfig)
	if err != nil {
		return "", fmt.Errorf("canonical compiled config: %w", err)
	}
	publicContract, err := canonicalRaw(r.PublicOutputContract)
	if err != nil {
		return "", fmt.Errorf("canonical public output contract: %w", err)
	}
	identity := struct {
		CompilationKey          string                                `json:"compilationKey"`
		RecipeDigest            string                                `json:"recipeDigest"`
		ResolvedRecipeDigest    string                                `json:"resolvedRecipeDigest,omitempty"`
		ResolvedSchemaDigest    string                                `json:"resolvedSchemaDigest,omitempty"`
		OutputContractDigest    string                                `json:"outputContractDigest,omitempty"`
		Bundle                  recipe.Bundle                         `json:"compiledRecipe"`
		CompiledConfig          []byte                                `json:"compiledConfig,omitempty"`
		PublicOutputContract    []byte                                `json:"publicOutputContract,omitempty"`
		Mappings                []IdentityMapping                     `json:"identityMappings"`
		Emissions               []EmittedColumn                       `json:"emittedColumns"`
		Fingerprints            map[string]string                     `json:"outputFingerprints,omitempty"`
		ColumnProvenance        map[string]map[string]string          `json:"outputColumnProvenance,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation              `json:"resolvedInterpretations,omitempty"`
		Warnings                []CompilationWarning                  `json:"warnings,omitempty"`
		RowDefinitionProposal   *RowDefinitionProposalBinding         `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding            `json:"tableShapeProposal,omitempty"`
		ConstructionProposal    *ConstructionProposalBinding          `json:"constructionProposal,omitempty"`
		ConstructionStages      map[string][]ReceiptConstructionStage `json:"constructionStages,omitempty"`
	}{
		key, r.RecipeDigest, r.ResolvedRecipeDigest, r.ResolvedSchemaDigest,
		r.OutputContractDigest, r.Bundle, compiledConfig,
		publicContract, r.IdentityMappings, r.EmittedColumns,
		r.OutputFingerprints, r.OutputColumnProvenance, r.ResolvedInterpretations, r.Warnings, r.RowDefinitionProposal, r.TableShapeProposal,
		r.ConstructionProposal, r.ConstructionStages,
	}
	return digestIdentity("receipt_", identity)
}

func digestIdentity(prefix string, identity any) (string, error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalJSONBytes(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return prefix + hex.EncodeToString(sum[:]), nil
}

func canonicalRaw(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	return canonicalJSONBytes(raw)
}

// Validate checks that a receipt is a supported, executable artifact.
func (r CompilationReceipt) Validate() error {
	if r.RowDefinitionProposal != nil {
		if err := r.RowDefinitionProposal.Validate(r.IntentDigest, r.SnapshotToken); err != nil {
			return err
		}
	}
	if r.TableShapeProposal != nil {
		if err := r.TableShapeProposal.Validate(r.IntentDigest, r.SnapshotToken); err != nil {
			return err
		}
	}
	if r.ConstructionProposal != nil {
		if err := r.ConstructionProposal.Validate(r.IntentDigest, r.SnapshotToken); err != nil {
			return err
		}
		if r.ReceiptFormatVersion != CompilationReceiptFormatVersion || r.CompilerContractVersion != CompilationReceiptCompilerContractVersion {
			return fmt.Errorf("construction proposals require the current receipt and compiler contract")
		}
	}
	if err := validateReceiptConstructionStages(r.ConstructionStages); err != nil {
		return err
	}
	if r.ReceiptFormatVersion != 0 && r.ReceiptFormatVersion != CompilationReceiptFormatVersion && r.ReceiptFormatVersion != previousCompilationReceiptFormatVersion && r.ReceiptFormatVersion != legacyCompilationReceiptFormatVersion {
		return fmt.Errorf("unsupported receipt format version %d", r.ReceiptFormatVersion)
	}
	if r.CompilerContractVersion != "" && r.CompilerContractVersion != CompilationReceiptCompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptV17CompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptV16CompilerContractVersion && r.CompilerContractVersion != previousCompilationReceiptCompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptB06CompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptV13CompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptCompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptOlderContractVersion && r.CompilerContractVersion != legacyCompilationReceiptV10ContractVersion {
		return fmt.Errorf("unsupported compiler contract %q", r.CompilerContractVersion)
	}
	if strings.TrimSpace(r.Project) == "" || strings.TrimSpace(r.ExplorerID) == "" {
		return fmt.Errorf("receipt project and explorerId are required")
	}
	if r.Bundle.RecipeSchemaVersion <= 0 {
		return ErrReceiptRecompileRequired
	}
	if r.ReceiptFormatVersion == CompilationReceiptFormatVersion || r.ReceiptFormatVersion == previousCompilationReceiptFormatVersion || r.ReceiptFormatVersion == legacyCompilationReceiptFormatVersion {
		required := []struct {
			name  string
			value string
		}{
			{"compilerContractVersion", r.CompilerContractVersion},
			{"intentDigest", r.IntentDigest},
			{"snapshotToken", r.SnapshotToken},
			{"authorizationScopeDigest", r.AuthorizationScopeDigest},
			{"capabilitySchemaDigest", r.CapabilitySchemaDigest},
			{"sourceGeneration", r.SourceGeneration},
			{"compilationKey", r.CompilationKey},
			{"recipeDigest", r.RecipeDigest},
			{"resolvedRecipeDigest", r.ResolvedRecipeDigest},
			{"resolvedSchemaDigest", r.ResolvedSchemaDigest},
			{"outputContractDigest", r.OutputContractDigest},
		}
		for _, field := range required {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("receipt %s is required", field.name)
			}
		}
		if (r.CompilerContractVersion == CompilationReceiptCompilerContractVersion || r.CompilerContractVersion == previousCompilationReceiptCompilerContractVersion) && strings.TrimSpace(r.ShapeDigest) == "" {
			return fmt.Errorf("receipt shapeDigest is required")
		}
		key, err := CompilationKey(r)
		if err != nil {
			return err
		}
		if r.CompilationKey != key {
			return fmt.Errorf("receipt compilation key mismatch: got %q want %q", r.CompilationKey, key)
		}
		resolvedDigest, err := r.Bundle.Digest()
		if err != nil {
			return fmt.Errorf("digest resolved recipe: %w", err)
		}
		if r.ResolvedRecipeDigest != resolvedDigest {
			return fmt.Errorf("receipt resolved recipe digest mismatch: got %q want %q", r.ResolvedRecipeDigest, resolvedDigest)
		}
		contract, contractErr := DecodePublicOutputContracts(r.PublicOutputContract)
		if contractErr != nil {
			return contractErr
		}
		if contractErr := contract.ValidateAgainst(r.Bundle, r.EmittedColumns); contractErr != nil {
			return contractErr
		}
		contractDigest, err := CompilationArtifactDigest(r.PublicOutputContract)
		if err != nil {
			return fmt.Errorf("%w: digest public output contract: %v", ErrReceiptRecompileRequired, err)
		}
		if r.OutputContractDigest != contractDigest {
			return fmt.Errorf("%w: receipt output contract digest mismatch: got %q want %q", ErrReceiptRecompileRequired, r.OutputContractDigest, contractDigest)
		}
		if err := validateOutputColumnProvenance(r.OutputColumnProvenance); err != nil {
			return err
		}
		if len(r.OutputColumnProvenance) != len(r.Bundle.Outputs) {
			return fmt.Errorf("receipt output column provenance output set changed")
		}
		for _, output := range r.Bundle.Outputs {
			if _, ok := r.OutputColumnProvenance[output.Name]; !ok {
				return fmt.Errorf("receipt output column provenance is missing output %q", output.Name)
			}
		}
	}
	if err := validateResolvedInterpretations(r.Project, r.ResolvedInterpretations); err != nil {
		return err
	}
	if r.ID != "" {
		if err := r.ValidateID(); err != nil {
			return err
		}
	}
	if len(r.NormalizedBundle) > 0 {
		if _, err := canonicalJSONBytes(r.NormalizedBundle); err != nil {
			return fmt.Errorf("invalid normalized bundle: %w", err)
		}
	}
	if len(r.CompiledConfig) > 0 {
		if _, err := canonicalJSONBytes(r.CompiledConfig); err != nil {
			return fmt.Errorf("invalid compiled config: %w", err)
		}
	}
	if len(r.PublicOutputContract) > 0 {
		if _, err := canonicalJSONBytes(r.PublicOutputContract); err != nil {
			return fmt.Errorf("invalid public output contract: %w", err)
		}
	}
	return nil
}

func validateOutputColumnProvenance(values map[string]map[string]string) error {
	if len(values) == 0 {
		return fmt.Errorf("receipt output column provenance is required")
	}
	for output, columns := range values {
		if strings.TrimSpace(output) == "" || len(columns) == 0 {
			return fmt.Errorf("receipt output column provenance contains an empty output")
		}
		for column, provenance := range columns {
			if strings.TrimSpace(column) == "" || (provenance != "EXPLICIT" && provenance != "DISCOVERED") {
				return fmt.Errorf("receipt output column provenance is invalid for %q/%q", output, column)
			}
		}
	}
	return nil
}

func validateResolvedInterpretations(project string, values []ResolvedInterpretation) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value.OutputID) == "" || strings.TrimSpace(value.Column) == "" || strings.TrimSpace(value.OccurrenceID) == "" {
			return fmt.Errorf("receipt resolved interpretation %d requires outputId, column, and occurrenceId", index)
		}
		key := value.OutputID + "\x00" + value.Column + "\x00" + value.OccurrenceID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("receipt resolved interpretations contain duplicate %q", key)
		}
		seen[key] = struct{}{}
		if err := value.Revision.Validate(); err != nil {
			return fmt.Errorf("receipt resolved interpretation %q has invalid revision: %w", key, err)
		}
		if projectid.Canonical(value.Revision.Project) != projectid.Canonical(project) {
			return fmt.Errorf("receipt resolved interpretation %q belongs to a different project", key)
		}
		if value.SelectedRuleID == "" {
			return fmt.Errorf("receipt resolved interpretation %q is missing selectedRuleId", key)
		}
		found := false
		for _, rule := range value.Revision.Rules {
			if rule.ID != value.SelectedRuleID {
				continue
			}
			found = true
			left, leftErr := json.Marshal(rule.Definition)
			right, rightErr := json.Marshal(value.Definition)
			if leftErr != nil || rightErr != nil || string(left) != string(right) {
				return fmt.Errorf("receipt resolved interpretation %q selected definition mismatch", key)
			}
			break
		}
		if !found {
			return fmt.Errorf("receipt resolved interpretation %q selected rule %q is absent", key, value.SelectedRuleID)
		}
	}
	return nil
}

// ValidateID verifies the stored ID against the receipt's content identity.
func (r CompilationReceipt) ValidateID() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("receipt id is required")
	}
	expected, err := ReceiptID(r)
	if err != nil {
		return fmt.Errorf("calculate receipt id: %w", err)
	}
	if r.ID != expected {
		return fmt.Errorf("receipt id mismatch: got %q want %q", r.ID, expected)
	}
	return nil
}
