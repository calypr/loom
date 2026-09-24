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

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

const (
	// CompilationReceiptFormatVersion changes when the persisted receipt shape
	// or its execution invariants change incompatibly.
	CompilationReceiptFormatVersion       = 4
	legacyCompilationReceiptFormatVersion = 3
	// CompilationReceiptCompilerContractVersion changes when compilation
	// semantics change in a way that can alter a resolved receipt.
	CompilationReceiptCompilerContractVersion          = "loom.explorer.compiler/v14"
	legacyCompilationReceiptB06CompilerContractVersion = "loom.explorer.compiler/v13"
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

type ReceiptPurpose string

const ReceiptPurposePreviewOnly ReceiptPurpose = "PREVIEW_ONLY"

// ReceiptContractSupportedForExecution distinguishes read compatibility from
// execution compatibility. The immediately previous v3/v13 artifact is
// executable because it already contains a complete frozen recipe; older
// historical contracts remain readable for migration/inspection but must be
// recompiled before native execution.
func ReceiptContractSupportedForExecution(r CompilationReceipt) bool {
	return (r.ReceiptFormatVersion == CompilationReceiptFormatVersion && r.CompilerContractVersion == CompilationReceiptCompilerContractVersion) ||
		(r.ReceiptFormatVersion == legacyCompilationReceiptFormatVersion && r.CompilerContractVersion == legacyCompilationReceiptB06CompilerContractVersion)
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
	ID                       string                   `json:"id"`
	Purpose                  ReceiptPurpose           `json:"purpose,omitempty"`
	ReceiptFormatVersion     int                      `json:"receiptFormatVersion"`
	CompilerContractVersion  string                   `json:"compilerContractVersion"`
	Project                  string                   `json:"project"`
	ExplorerID               string                   `json:"explorerId"`
	IntentDigest             string                   `json:"intentDigest"`
	ResolvedInputsDigest     string                   `json:"resolvedInputsDigest,omitempty"`
	ResolvedInterpretations  []ResolvedInterpretation `json:"resolvedInterpretations,omitempty"`
	SnapshotToken            string                   `json:"snapshotToken"`
	AuthorizationScopeDigest string                   `json:"authorizationScopeDigest,omitempty"`
	CapabilitySchemaDigest   string                   `json:"capabilitySchemaDigest,omitempty"`
	ShapeDigest              string                   `json:"shapeDigest,omitempty"`
	SourceGeneration         string                   `json:"sourceGeneration"`
	CompilationKey           string                   `json:"compilationKey,omitempty"`
	RecipeDigest             string                   `json:"recipeDigest"`
	ResolvedRecipeDigest     string                   `json:"resolvedRecipeDigest,omitempty"`
	ResolvedSchemaDigest     string                   `json:"resolvedSchemaDigest,omitempty"`
	OutputContractDigest     string                   `json:"outputContractDigest,omitempty"`
	NormalizedBundle         json.RawMessage          `json:"normalizedBundle"`
	Bundle                   recipe.Bundle            `json:"compiledRecipe"`
	CompiledConfig           json.RawMessage          `json:"compiledConfig,omitempty"`
	PublicOutputContract     json.RawMessage          `json:"publicOutputContract,omitempty"`
	IdentityMappings         []IdentityMapping        `json:"identityMappings"`
	EmittedColumns           []EmittedColumn          `json:"emittedColumns"`
	OutputFingerprints       map[string]string        `json:"outputFingerprints,omitempty"`
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
		Purpose                 ReceiptPurpose                `json:"purpose,omitempty"`
		ReceiptFormatVersion    int                           `json:"receiptFormatVersion"`
		CompilerContractVersion string                        `json:"compilerContractVersion"`
		Project                 string                        `json:"project"`
		ExplorerID              string                        `json:"explorerId"`
		IntentDigest            string                        `json:"intentDigest"`
		ResolvedInputsDigest    string                        `json:"resolvedInputsDigest,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation      `json:"resolvedInterpretations,omitempty"`
		NormalizedBundle        []byte                        `json:"normalizedBundle,omitempty"`
		SnapshotToken           string                        `json:"snapshotToken"`
		AuthorizationScope      string                        `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string                        `json:"capabilitySchemaDigest,omitempty"`
		ShapeDigest             string                        `json:"shapeDigest,omitempty"`
		SourceGeneration        string                        `json:"sourceGeneration"`
		RowDefinitionProposal   *RowDefinitionProposalBinding `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding    `json:"tableShapeProposal,omitempty"`
	}{}
	normalized, err := canonicalRaw(r.NormalizedBundle)
	if err != nil {
		return "", fmt.Errorf("canonical normalized bundle: %w", err)
	}
	identity = struct {
		Purpose                 ReceiptPurpose                `json:"purpose,omitempty"`
		ReceiptFormatVersion    int                           `json:"receiptFormatVersion"`
		CompilerContractVersion string                        `json:"compilerContractVersion"`
		Project                 string                        `json:"project"`
		ExplorerID              string                        `json:"explorerId"`
		IntentDigest            string                        `json:"intentDigest"`
		ResolvedInputsDigest    string                        `json:"resolvedInputsDigest,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation      `json:"resolvedInterpretations,omitempty"`
		NormalizedBundle        []byte                        `json:"normalizedBundle,omitempty"`
		SnapshotToken           string                        `json:"snapshotToken"`
		AuthorizationScope      string                        `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string                        `json:"capabilitySchemaDigest,omitempty"`
		ShapeDigest             string                        `json:"shapeDigest,omitempty"`
		SourceGeneration        string                        `json:"sourceGeneration"`
		RowDefinitionProposal   *RowDefinitionProposalBinding `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding    `json:"tableShapeProposal,omitempty"`
	}{
		r.Purpose, r.ReceiptFormatVersion, r.CompilerContractVersion, r.Project, r.ExplorerID,
		r.IntentDigest, r.ResolvedInputsDigest, r.ResolvedInterpretations, normalized, r.SnapshotToken,
		r.AuthorizationScopeDigest, r.CapabilitySchemaDigest, r.ShapeDigest, r.SourceGeneration, r.RowDefinitionProposal, r.TableShapeProposal,
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
		Purpose                 ReceiptPurpose                `json:"purpose,omitempty"`
		CompilationKey          string                        `json:"compilationKey"`
		RecipeDigest            string                        `json:"recipeDigest"`
		ResolvedRecipeDigest    string                        `json:"resolvedRecipeDigest,omitempty"`
		ResolvedSchemaDigest    string                        `json:"resolvedSchemaDigest,omitempty"`
		OutputContractDigest    string                        `json:"outputContractDigest,omitempty"`
		Bundle                  recipe.Bundle                 `json:"compiledRecipe"`
		CompiledConfig          []byte                        `json:"compiledConfig,omitempty"`
		PublicOutputContract    []byte                        `json:"publicOutputContract,omitempty"`
		Mappings                []IdentityMapping             `json:"identityMappings"`
		Emissions               []EmittedColumn               `json:"emittedColumns"`
		Fingerprints            map[string]string             `json:"outputFingerprints,omitempty"`
		ColumnProvenance        map[string]map[string]string  `json:"outputColumnProvenance,omitempty"`
		ResolvedInterpretations []ResolvedInterpretation      `json:"resolvedInterpretations,omitempty"`
		Warnings                []CompilationWarning          `json:"warnings,omitempty"`
		RowDefinitionProposal   *RowDefinitionProposalBinding `json:"rowDefinitionProposal,omitempty"`
		TableShapeProposal      *TableShapeProposalBinding    `json:"tableShapeProposal,omitempty"`
	}{
		r.Purpose, key, r.RecipeDigest, r.ResolvedRecipeDigest, r.ResolvedSchemaDigest,
		r.OutputContractDigest, r.Bundle, compiledConfig,
		publicContract, r.IdentityMappings, r.EmittedColumns,
		r.OutputFingerprints, r.OutputColumnProvenance, r.ResolvedInterpretations, r.Warnings, r.RowDefinitionProposal, r.TableShapeProposal,
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
	if r.Purpose != "" && r.Purpose != ReceiptPurposePreviewOnly {
		return fmt.Errorf("unsupported receipt purpose %q", r.Purpose)
	}
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
	if r.ReceiptFormatVersion != 0 && r.ReceiptFormatVersion != CompilationReceiptFormatVersion && r.ReceiptFormatVersion != legacyCompilationReceiptFormatVersion {
		return fmt.Errorf("unsupported receipt format version %d", r.ReceiptFormatVersion)
	}
	if r.CompilerContractVersion != "" && r.CompilerContractVersion != CompilationReceiptCompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptB06CompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptCompilerContractVersion && r.CompilerContractVersion != legacyCompilationReceiptOlderContractVersion && r.CompilerContractVersion != legacyCompilationReceiptV10ContractVersion {
		return fmt.Errorf("unsupported compiler contract %q", r.CompilerContractVersion)
	}
	if strings.TrimSpace(r.Project) == "" || strings.TrimSpace(r.ExplorerID) == "" {
		return fmt.Errorf("receipt project and explorerId are required")
	}
	if r.Bundle.RecipeSchemaVersion <= 0 {
		return ErrReceiptRecompileRequired
	}
	if r.ReceiptFormatVersion == CompilationReceiptFormatVersion || r.ReceiptFormatVersion == legacyCompilationReceiptFormatVersion {
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
		if r.CompilerContractVersion == CompilationReceiptCompilerContractVersion && strings.TrimSpace(r.ShapeDigest) == "" {
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
