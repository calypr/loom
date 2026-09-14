package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func decodeStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}

func pointerTo[T any](value T) *T { return &value }

func v2ReceiptResponse(receipt *explorer.CompilationReceipt, workspace authoringv2.Workspace) loomapi.CompileResponse {
	if receipt != nil && len(receipt.NormalizedBundle) != 0 {
		if normalized, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle); err == nil {
			workspace = normalized
		}
	}
	outputs := make([]loomapi.ReceiptOutput, 0, len(workspace.Documents))
	for _, document := range workspace.Documents {
		rowGrain := ""
		for _, output := range receipt.Bundle.Outputs {
			if output.Name == document.Output.ID {
				rowGrain = output.RowGrain
				break
			}
		}
		columns := make([]loomapi.ContractColumn, 0)
		for _, column := range receipt.EmittedColumns {
			if column.OutputID != document.Output.ID {
				continue
			}
			label := firstNonEmpty(column.Label, column.PublicColumn)
			coordinates := make([]loomapi.RepeatedCoordinate, 0, len(column.Coordinates))
			for _, coordinate := range column.Coordinates {
				coordinates = append(coordinates, loomapi.RepeatedCoordinate{BoundaryPath: coordinate.BoundaryPath, Index: coordinate.Index, Width: coordinate.Width})
			}
			wire := loomapi.ContractColumn{
				Column: column.PublicColumn, Label: label, LogicalType: column.LogicalType,
				Filterable: column.Filterable, Chartable: column.Chartable,
				Nullable: pointerTo(column.Nullable), Shape: pointerTo(column.Shape),
				Lossless: pointerTo(column.Lossless), MlReady: pointerTo(column.MLReady),
			}
			if len(column.AuthoredColumns) > 0 {
				authoredColumns := append([]string(nil), column.AuthoredColumns...)
				wire.AuthoredColumns = &authoredColumns
			}
			if column.SourceResourceType != "" {
				wire.SourceResourceType = pointerTo(column.SourceResourceType)
			}
			if column.SourcePath != "" {
				wire.SourcePath = pointerTo(column.SourcePath)
			}
			if column.ChoiceArm != "" {
				wire.ChoiceArm = pointerTo(column.ChoiceArm)
			}
			if len(coordinates) > 0 {
				wire.Coordinates = &coordinates
			}
			columns = append(columns, wire)
		}
		rootResourceType := document.RootResourceType
		multiplication := loomapi.None
		lossless, mlReady := true, true
		for _, column := range receipt.EmittedColumns {
			if column.OutputID == document.Output.ID {
				lossless = lossless && column.Lossless
				mlReady = mlReady && column.MLReady
			}
		}
		outputs = append(outputs, loomapi.ReceiptOutput{OutputId: document.Output.ID, Title: document.Output.Title, RowGrain: rowGrain, RootResourceType: &rootResourceType, RowMultiplication: &multiplication, Lossless: &lossless, MlReady: &mlReady, Columns: columns})
	}
	return loomapi.CompileResponse{
		ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderReceipt,
		ReceiptId: receipt.ID, SnapshotToken: receipt.SnapshotToken, Generation: receipt.SourceGeneration,
		IntentDigest: receipt.IntentDigest, CompilerVersion: explorer.CurrentCompilerContractVersion + "+" + explorercompilation.TranslationVersion,
		ShapeDigest: &receipt.ShapeDigest, RecipeDigest: &receipt.RecipeDigest,
		ResolvedRecipeDigest: &receipt.ResolvedRecipeDigest, ResolvedSchemaDigest: &receipt.ResolvedSchemaDigest,
		OutputContractDigest: &receipt.OutputContractDigest, AuthorizationScopeDigest: &receipt.AuthorizationScopeDigest,
		CapabilitySchemaDigest: &receipt.CapabilitySchemaDigest,
		Builder:                workspace, Outputs: outputs, Diagnostics: []loomapi.Diagnostic{},
	}
}
