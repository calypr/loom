package support

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/projectid"
)

func ReceiptHasOutput(bundle recipe.Bundle, id string) bool {
	for _, output := range bundle.Outputs {
		if output.Name == id {
			return true
		}
	}
	return false
}

func ValidateReceiptOutputContract(receipt *receiptmodel.CompilationReceipt, outputID string) error {
	if receipt == nil {
		return fmt.Errorf("receipt is required")
	}
	outputID = strings.TrimSpace(outputID)
	if outputID == "" {
		return fmt.Errorf("output is required")
	}
	contract, err := receiptmodel.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		return fmt.Errorf("public output contract is invalid: %v", err)
	}
	if _, ok := contract.Output(outputID); !ok {
		return fmt.Errorf("requested output %q is not in the receipt public contract", outputID)
	}
	if !ReceiptHasOutput(receipt.Bundle, outputID) {
		return fmt.Errorf("requested output %q is not in the receipt recipe", outputID)
	}
	return nil
}

func ApplyAuthorizedScope(bindings *recipe.RuntimeBindings, authorized ports.AuthorizedCapability, includeAuthResourcePath bool) {
	if bindings == nil {
		return
	}
	bindings.AuthResourcePaths = append([]string(nil), authorized.Scope.AuthResourcePaths...)
	bindings.AuthScopeMode = authorized.Scope.Mode
	bindings.IncludeAuthResourcePath = includeAuthResourcePath
}

func ValidateAuthorizedReceiptExecution(receipt *receiptmodel.CompilationReceipt, authorized ports.AuthorizedCapability) error {
	if receipt == nil {
		return fmt.Errorf("receipt is required")
	}
	snapshot := authorized.Snapshot
	if strings.TrimSpace(receipt.SnapshotToken) == "" || snapshot.Token != receipt.SnapshotToken {
		return fmt.Errorf("capability token does not exactly match receipt token")
	}
	if err := snapshot.ValidateToken(receipt.SnapshotToken); err != nil {
		return fmt.Errorf("capability token is invalid: %v", err)
	}
	if snapshot.Identity.Project != receipt.Project {
		return fmt.Errorf("capability project changed")
	}
	if snapshot.Identity.Generation != receipt.SourceGeneration {
		return fmt.Errorf("capability generation changed")
	}
	if snapshot.Identity.SchemaDigest != receipt.CapabilitySchemaDigest {
		return fmt.Errorf("capability schema changed")
	}
	if snapshot.Identity.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("capability scope digest changed")
	}
	return ValidateAuthorizedReadScope(authorized.Scope, receipt.AuthorizationScopeDigest)
}

type executableReceiptReader interface {
	LookupReceipt(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error)
	ValidateExecutableReceiptRoute(*receiptmodel.CompilationReceipt, string, string) error
}

type ReceiptOutputRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Stage      string
}

type AuthorizedReceiptOutput struct {
	Receipt    *receiptmodel.CompilationReceipt
	OutputID   string
	Capability ports.AuthorizedCapability
}

func PrepareReceiptOutput(ctx context.Context, reader executableReceiptReader, resolve func(context.Context, string, string) (ports.AuthorizedCapability, error), request ReceiptOutputRequest) (AuthorizedReceiptOutput, error) {
	receipt, err := reader.LookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return AuthorizedReceiptOutput{}, err
	}
	if err := reader.ValidateExecutableReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return AuthorizedReceiptOutput{}, err
	}
	const staleMessage = "the receipt's capability snapshot is no longer authorized or retained"
	authorized, err := resolve(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil {
		return AuthorizedReceiptOutput{}, ReceiptCapabilityFailure(request.Stage, staleMessage, err)
	}
	if authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return AuthorizedReceiptOutput{}, Conflict(request.Stage, "RECEIPT_STALE", staleMessage, nil, nil)
	}
	if err := ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return AuthorizedReceiptOutput{}, Conflict(request.Stage, "RECEIPT_STALE", staleMessage, nil, err)
	}
	if !ReceiptHasOutput(receipt.Bundle, request.OutputID) || ValidateReceiptOutputContract(receipt, request.OutputID) != nil {
		return AuthorizedReceiptOutput{}, Unprocessable(request.Stage, "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	return AuthorizedReceiptOutput{Receipt: receipt, OutputID: request.OutputID, Capability: authorized}, nil
}

func (o AuthorizedReceiptOutput) Bindings(selectionMembersCollection string) recipe.RuntimeBindings {
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(o.Receipt.Project), SelectionProject: projectid.Canonical(o.Receipt.Project),
		DatasetGeneration: o.Receipt.SourceGeneration, SelectionMembersCollection: selectionMembersCollection,
	}
	ApplyAuthorizedScope(&bindings, o.Capability, false)
	return bindings
}
