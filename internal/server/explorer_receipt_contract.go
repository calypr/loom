package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
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

func compiledExplorerWorkspaceConfigV2(project, explorerID string, compiled explorercompilation.WorkspaceResult) ([]byte, error) {
	if len(compiled.EmittedColumns) == 0 {
		return nil, nil
	}
	recipeJSON, err := json.Marshal(compiled.Bundle)
	if err != nil {
		return nil, err
	}
	byOutput := map[string]explorercompilation.PresentationConfig{}
	for _, presentation := range compiled.Presentations {
		byOutput[presentation.OutputID] = presentation
	}
	tabs := append([]authoringv2.Tab(nil), compiled.Workspace.Tabs...)
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].Order < tabs[j].Order })
	views := make([]explorer.ConfigView, 0, len(tabs))
	for _, tab := range tabs {
		if !tab.Visible {
			continue
		}
		presentation, ok := byOutput[tab.OutputID]
		if !ok {
			return nil, fmt.Errorf("missing compiled output %q", tab.OutputID)
		}
		columns := append([]explorercompilation.PresentationColumn(nil), presentation.Columns...)
		sort.SliceStable(columns, func(i, j int) bool {
			if columns[i].Order != columns[j].Order {
				return columns[i].Order < columns[j].Order
			}
			return columns[i].EmissionID < columns[j].EmissionID
		})
		view := explorer.ConfigView{ID: tab.ID, Title: tab.Title, Output: tab.OutputID, Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{}}}
		for _, column := range columns {
			view.Table.Columns = append(view.Table.Columns, explorer.ConfigColumn{Column: column.PublicColumn, Label: column.Label, Visible: column.Visible, Pinned: column.Pinned})
			if column.FilterLabel != "" {
				view.Filters = append(view.Filters, explorer.ConfigFilter{Column: column.PublicColumn, Label: column.FilterLabel})
			}
			if column.ChartType != "" {
				view.Charts = append(view.Charts, explorer.ConfigChart{Column: column.PublicColumn, Type: column.ChartType, Title: column.ChartTitle})
			}
		}
		views = append(views, view)
	}
	title := explorerID
	if len(tabs) > 0 {
		title = tabs[0].Title
	}
	config := explorer.ConfigV2{APIVersion: explorer.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: projectid.Canonical(project), Explorer: explorer.ConfigExplorer{ID: explorerID, Title: title, Management: explorer.ConfigManagementForID(explorerID)}, Recipe: recipeJSON, Views: views}
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

// compileValidatedReceiptResolution validates the complete immutable receipt
// even when the caller intends to execute only one output. OutputNames is an
// execution selector: allowing it to narrow compilation here would compare a
// partial fingerprint and column set with the receipt's full contract.
func compileValidatedReceiptResolution(ctx context.Context, recipeEngine *engine.Engine, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (engine.Resolved, error) {
	validationBindings := bindings
	validationBindings.OutputNames = nil
	resolved, err := recipeEngine.CompileResolvedBundle(ctx, receipt.Bundle, validationBindings)
	if err != nil {
		return engine.Resolved{}, err
	}
	if err := validateReceiptResolution(receipt, resolved); err != nil {
		return engine.Resolved{}, err
	}
	if err := validateReceiptEnginePublicColumns(receipt, resolved); err != nil {
		return engine.Resolved{}, err
	}
	return resolved, nil
}
