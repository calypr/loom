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
			columns = append(columns, loomapi.ContractColumn{Column: column.PublicColumn, Label: label, LogicalType: column.LogicalType, Filterable: column.Filterable, Chartable: column.Chartable})
		}
		outputs = append(outputs, loomapi.ReceiptOutput{OutputId: document.Output.ID, Title: document.Output.Title, RowGrain: rowGrain, Columns: columns})
	}
	return loomapi.CompileResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderReceipt, ReceiptId: receipt.ID, SnapshotToken: receipt.SnapshotToken, Generation: receipt.SourceGeneration, IntentDigest: receipt.IntentDigest, CompilerVersion: explorer.CurrentCompilerContractVersion + "+" + explorercompilation.TranslationVersion, Builder: workspace, Outputs: outputs, Diagnostics: []loomapi.Diagnostic{}}
}
