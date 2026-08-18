package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
)

type explorerV2CatalogReader func(context.Context, string, string, string) (explorer.CatalogSnapshot, error)

func explorerV2Compiler(recipeEngine *engine.Engine, catalogReader explorerV2CatalogReader) ExplorerV2Compiler {
	return func(ctx context.Context, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, error) {
		cfg, bundle, canonical, digest, err := explorer.CanonicalConfigV2(request.Config, request.Project, request.ExplorerID, "interactive")
		if err != nil {
			return ExplorerV2CompileResult{}, err
		}
		var sourceGeneration string
		// Every request that reaches the production recipe engine must resolve
		// against the active dataset generation. An absent catalog capability is
		// a wiring failure, never permission to fall back to the null-generation
		// namespace.
		if catalogReader != nil {
			snapshot, err := catalogReader(ctx, request.Project, request.ExplorerID, "")
			if err != nil {
				return ExplorerV2CompileResult{}, err
			}
			if strings.TrimSpace(snapshot.Generation) == "" {
				return ExplorerV2CompileResult{}, fmt.Errorf("active dataset generation is unavailable")
			}
			sourceGeneration = snapshot.Generation
			if request.SnapshotToken != "" || len(request.SelectedCandidateIDsByNode) > 0 {
				if err := snapshot.ValidateToken(request.SnapshotToken); err != nil {
					return ExplorerV2CompileResult{}, err
				}
			}
			for nodeID, candidateIDs := range request.SelectedCandidateIDsByNode {
				lookupNodeID := nodeID
				if nodeID == "root" && len(bundle.Outputs) > 0 {
					lookupNodeID = explorer.OpaqueID("n_", bundle.Outputs[0].RootResourceType)
				}
				if _, ok := catalogNodeForREST(snapshot.Catalog, lookupNodeID); !ok {
					return ExplorerV2CompileResult{}, fmt.Errorf("unresolved catalog node %q", nodeID)
				}
				for _, candidateID := range candidateIDs {
					selection, ok := catalogSelectionForREST(snapshot.Catalog, candidateID)
					if !ok || selection.NodeID != lookupNodeID {
						return ExplorerV2CompileResult{}, fmt.Errorf("unresolved catalog candidate %q for node %q", candidateID, nodeID)
					}
				}
			}
		} else {
			return ExplorerV2CompileResult{}, fmt.Errorf("catalog discovery is not configured")
		}
		if recipeEngine == nil {
			return ExplorerV2CompileResult{Config: canonical, Bundle: bundle, RecipeDigest: digest, SourceGeneration: sourceGeneration}, nil
		}
		resolved, err := recipeEngine.ResolveBundle(ctx, bundle, recipe.RuntimeBindings{Project: request.Project, DatasetGeneration: sourceGeneration})
		if err != nil {
			return ExplorerV2CompileResult{}, err
		}
		emitted := make([]explorer.EmittedColumn, 0)
		for _, output := range resolved.Compiled.Outputs {
			for _, column := range output.OutputSchema {
				if column.Internal {
					continue
				}
				emitted = append(emitted, explorer.EmittedColumn{OutputID: output.Name, PublicColumn: column.Name, SelectionID: column.SemanticPath, LogicalType: column.Kind, Filterable: true, Chartable: true})
			}
		}
		if err := synchronizeExplorerV2Views(cfg, resolved.Compiled.Outputs); err != nil {
			return ExplorerV2CompileResult{}, err
		}
		return ExplorerV2CompileResult{Config: canonical, Bundle: bundle, RecipeDigest: resolved.StoredRecipeDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, SourceGeneration: resolved.Compiled.SourceGeneration, EmittedColumns: emitted}, nil
	}
}

// The explorer package intentionally keeps Catalog's internal lookup methods
// private. These tiny adapters keep REST wiring from exposing catalog fields.
func catalogNodeForREST(c explorer.Catalog, id string) (explorer.CatalogNode, bool) {
	for _, node := range c.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return explorer.CatalogNode{}, false
}
func catalogSelectionForREST(c explorer.Catalog, id string) (explorer.CatalogSelection, bool) {
	value, ok := c.Selections[id]
	return value, ok
}

func synchronizeExplorerV2Views(cfg explorer.ConfigV2, outputs []lower.CompiledRecipeOutput) error {
	columns := map[string]map[string]bool{}
	for _, output := range outputs {
		columns[output.Name] = map[string]bool{}
		for _, column := range output.Columns {
			columns[output.Name][column] = true
		}
	}
	for _, view := range cfg.Views {
		available, ok := columns[view.Output]
		if !ok {
			return fmt.Errorf("view %q references unsupported output %q", view.ID, view.Output)
		}
		for _, column := range view.Table.Columns {
			if !available[column.Column] {
				return fmt.Errorf("view %q references missing output column %q", view.ID, column.Column)
			}
		}
		for _, filter := range view.Filters {
			if !available[filter.Column] {
				return fmt.Errorf("view %q references missing filter column %q", view.ID, filter.Column)
			}
		}
		for _, chart := range view.Charts {
			if !available[chart.Column] {
				return fmt.Errorf("view %q references missing chart column %q", view.ID, chart.Column)
			}
		}
	}
	return nil
}
