package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

func compiledExplorerConfigV2(project, explorerID string, compiled explorercompilation.Result) ([]byte, error) {
	if len(compiled.EmittedColumns) == 0 {
		// An empty selection is a valid mutable editor state and may receive a
		// receipt, but there is no valid interactive ExplorerConfig until the user
		// selects a public column. Publication rejects this state explicitly.
		return nil, nil
	}
	recipeJSON, err := json.Marshal(compiled.Bundle)
	if err != nil {
		return nil, fmt.Errorf("marshal compiled recipe: %w", err)
	}
	columns := append([]explorercompilation.PresentationColumn(nil), compiled.Presentation.Columns...)
	sort.SliceStable(columns, func(i, j int) bool {
		if columns[i].Order != columns[j].Order {
			return columns[i].Order < columns[j].Order
		}
		return columns[i].EmissionID < columns[j].EmissionID
	})
	table := make([]explorer.ConfigColumn, 0, len(columns))
	for _, column := range columns {
		table = append(table, explorer.ConfigColumn{Column: column.PublicColumn, Label: column.Label, Visible: column.Visible})
	}
	sum := sha256.Sum256([]byte(compiled.Presentation.OutputID))
	viewID := "view-" + hex.EncodeToString(sum[:])[:12]
	config := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    projectid.Canonical(project),
		Explorer: explorer.ConfigExplorer{
			ID: explorerID, Title: compiled.Presentation.Title,
			Management: explorer.ConfigManagementForID(explorerID),
		},
		Recipe: recipeJSON,
		Views: []explorer.ConfigView{{
			ID: viewID, Title: compiled.Presentation.Title,
			Output: compiled.Presentation.OutputID,
			Table:  explorer.ConfigTable{Columns: table},
		}},
	}
	return json.Marshal(config)
}

func validateReceiptCapability(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if snapshot.Identity.Project != receipt.Project || snapshot.Identity.Generation != receipt.SourceGeneration {
		return fmt.Errorf("receipt capability project or generation changed")
	}
	if snapshot.Identity.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("receipt authorization scope changed")
	}
	if snapshot.Identity.SchemaDigest != receipt.CapabilitySchemaDigest {
		return fmt.Errorf("receipt capability schema changed")
	}
	return nil
}

// validateReceiptResolution proves that deterministic runtime lowering still
// describes the exact resolved semantic artifact frozen in the receipt. It
// intentionally compares no AQL or physical IR because those are
// request-scoped implementation details.
func validateReceiptResolution(receipt *explorer.CompilationReceipt, resolved engine.Resolved) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	digest, err := resolved.Bundle.Digest()
	if err != nil {
		return err
	}
	if digest != receipt.ResolvedRecipeDigest || resolved.StoredRecipeDigest != receipt.RecipeDigest {
		return fmt.Errorf("receipt recipe digest does not match deterministic lowering")
	}
	if resolved.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest {
		return fmt.Errorf("receipt resolved schema digest does not match deterministic lowering")
	}
	want := resolvedOutputFingerprints(resolved)
	if len(want) != len(receipt.OutputFingerprints) {
		return fmt.Errorf("receipt output fingerprint set changed")
	}
	for output, fingerprint := range want {
		if strings.TrimSpace(receipt.OutputFingerprints[output]) != fingerprint {
			return fmt.Errorf("receipt output %q fingerprint changed", output)
		}
	}
	return nil
}
