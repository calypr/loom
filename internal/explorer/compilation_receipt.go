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
)

const (
	// CompilationReceiptFormatVersion changes when the persisted receipt shape
	// or its execution invariants change incompatibly.
	CompilationReceiptFormatVersion = 2
	// CompilationReceiptCompilerContractVersion changes when compilation
	// semantics change in a way that can alter a resolved receipt.
	CompilationReceiptCompilerContractVersion = "loom.explorer.compiler/v4"

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
	ID                       string               `json:"id"`
	ReceiptFormatVersion     int                  `json:"receiptFormatVersion"`
	CompilerContractVersion  string               `json:"compilerContractVersion"`
	Project                  string               `json:"project"`
	ExplorerID               string               `json:"explorerId"`
	IntentDigest             string               `json:"intentDigest"`
	SnapshotToken            string               `json:"snapshotToken"`
	AuthorizationScopeDigest string               `json:"authorizationScopeDigest,omitempty"`
	CapabilitySchemaDigest   string               `json:"capabilitySchemaDigest,omitempty"`
	SourceGeneration         string               `json:"sourceGeneration"`
	CompilationKey           string               `json:"compilationKey,omitempty"`
	RecipeDigest             string               `json:"recipeDigest"`
	ResolvedRecipeDigest     string               `json:"resolvedRecipeDigest,omitempty"`
	ResolvedSchemaDigest     string               `json:"resolvedSchemaDigest,omitempty"`
	OutputContractDigest     string               `json:"outputContractDigest,omitempty"`
	NormalizedBundle         json.RawMessage      `json:"normalizedBundle"`
	Bundle                   recipe.Bundle        `json:"compiledRecipe"`
	CompiledConfig           json.RawMessage      `json:"compiledConfig,omitempty"`
	PublicOutputContract     json.RawMessage      `json:"publicOutputContract,omitempty"`
	IdentityMappings         []IdentityMapping    `json:"identityMappings"`
	EmittedColumns           []EmittedColumn      `json:"emittedColumns"`
	OutputFingerprints       map[string]string    `json:"outputFingerprints,omitempty"`
	Warnings                 []CompilationWarning `json:"warnings,omitempty"`
	RequestID                string               `json:"requestId,omitempty"`
	CreatedAt                time.Time            `json:"createdAt"`
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
		ReceiptFormatVersion    int    `json:"receiptFormatVersion"`
		CompilerContractVersion string `json:"compilerContractVersion"`
		Project                 string `json:"project"`
		ExplorerID              string `json:"explorerId"`
		IntentDigest            string `json:"intentDigest"`
		NormalizedBundle        []byte `json:"normalizedBundle,omitempty"`
		SnapshotToken           string `json:"snapshotToken"`
		AuthorizationScope      string `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string `json:"capabilitySchemaDigest,omitempty"`
		SourceGeneration        string `json:"sourceGeneration"`
	}{}
	normalized, err := canonicalRaw(r.NormalizedBundle)
	if err != nil {
		return "", fmt.Errorf("canonical normalized bundle: %w", err)
	}
	identity = struct {
		ReceiptFormatVersion    int    `json:"receiptFormatVersion"`
		CompilerContractVersion string `json:"compilerContractVersion"`
		Project                 string `json:"project"`
		ExplorerID              string `json:"explorerId"`
		IntentDigest            string `json:"intentDigest"`
		NormalizedBundle        []byte `json:"normalizedBundle,omitempty"`
		SnapshotToken           string `json:"snapshotToken"`
		AuthorizationScope      string `json:"authorizationScopeDigest,omitempty"`
		CapabilitySchema        string `json:"capabilitySchemaDigest,omitempty"`
		SourceGeneration        string `json:"sourceGeneration"`
	}{
		r.ReceiptFormatVersion, r.CompilerContractVersion, r.Project, r.ExplorerID,
		r.IntentDigest, normalized, r.SnapshotToken,
		r.AuthorizationScopeDigest, r.CapabilitySchemaDigest, r.SourceGeneration,
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
		CompilationKey       string               `json:"compilationKey"`
		RecipeDigest         string               `json:"recipeDigest"`
		ResolvedRecipeDigest string               `json:"resolvedRecipeDigest,omitempty"`
		ResolvedSchemaDigest string               `json:"resolvedSchemaDigest,omitempty"`
		OutputContractDigest string               `json:"outputContractDigest,omitempty"`
		Bundle               recipe.Bundle        `json:"compiledRecipe"`
		CompiledConfig       []byte               `json:"compiledConfig,omitempty"`
		PublicOutputContract []byte               `json:"publicOutputContract,omitempty"`
		Mappings             []IdentityMapping    `json:"identityMappings"`
		Emissions            []EmittedColumn      `json:"emittedColumns"`
		Fingerprints         map[string]string    `json:"outputFingerprints,omitempty"`
		Warnings             []CompilationWarning `json:"warnings,omitempty"`
	}{
		key, r.RecipeDigest, r.ResolvedRecipeDigest, r.ResolvedSchemaDigest,
		r.OutputContractDigest, r.Bundle, compiledConfig,
		publicContract, r.IdentityMappings, r.EmittedColumns,
		r.OutputFingerprints, r.Warnings,
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
	if r.ReceiptFormatVersion != 0 && r.ReceiptFormatVersion != CompilationReceiptFormatVersion {
		return fmt.Errorf("unsupported receipt format version %d", r.ReceiptFormatVersion)
	}
	if r.CompilerContractVersion != "" && r.CompilerContractVersion != CompilationReceiptCompilerContractVersion {
		return fmt.Errorf("unsupported compiler contract %q", r.CompilerContractVersion)
	}
	if strings.TrimSpace(r.Project) == "" || strings.TrimSpace(r.ExplorerID) == "" {
		return fmt.Errorf("receipt project and explorerId are required")
	}
	if r.Bundle.RecipeSchemaVersion <= 0 {
		return ErrReceiptRecompileRequired
	}
	if r.ReceiptFormatVersion == CompilationReceiptFormatVersion {
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

// ValidateID is also available as a package helper for repository code that
// prefers functional validation.
func ValidateID(r CompilationReceipt) error { return r.ValidateID() }

// CloneCompilationReceipt returns a deep copy suitable for memory stores and
// tests. JSON round-tripping also clones nested recipe slices and maps while
// retaining the persisted wire shape.
func CloneCompilationReceipt(in CompilationReceipt) (CompilationReceipt, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return CompilationReceipt{}, err
	}
	var out CompilationReceipt
	if err := json.Unmarshal(raw, &out); err != nil {
		return CompilationReceipt{}, err
	}
	return out, nil
}
