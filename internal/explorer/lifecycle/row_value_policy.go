package lifecycle

import (
	"context"
	"fmt"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) validateRowValuePolicyCandidate(
	ctx context.Context,
	project, explorerID string,
	request authoringv2.ApplyCommandsRequest,
	workspace authoringv2.Workspace,
	snapshot capability.Snapshot,
	compilationAuth AuthorizedCapability,
	command authoringv2.Command,
) error {
	if s.config.ValidateReceiptStream == nil || s.config.Capability.ForExecution == nil {
		return unavailable("commands", "ROW_VALUE_POLICY_VALIDATION_UNAVAILABLE", "full-stream row-value policy validation is not configured", nil)
	}
	if command.Type != authoringv2.CommandUpdateColumnRowValuePolicy || strings.TrimSpace(command.OutputID) == "" || strings.TrimSpace(command.Column) == "" {
		return malformed("commands", "UPDATE_COLUMN_ROW_VALUE_POLICY requires outputId and column", nil)
	}

	receipt, err := s.compile(ctx, compileRequest{
		Project: project, ExplorerID: explorerID, Workspace: workspace,
		SnapshotToken: request.SnapshotToken, RequestID: request.CommandID + "-row-value-policy",
	})
	if err != nil {
		return err
	}
	if err := verifyPreviewReceiptIntent(receipt, workspace); err != nil {
		return conflict("commands", "INVALID_COMPILATION_RECEIPT", "the row-value policy receipt does not represent the candidate workspace", nil, err)
	}
	if err := s.validateReceiptRoute(receipt, project, explorerID); err != nil {
		return err
	}
	if !receiptHasOutput(receipt.Bundle, command.OutputID) || validateReceiptOutputContract(receipt, command.OutputID) != nil {
		return unprocessable("commands", "INVALID_COMPILATION_RECEIPT", "the row-value policy candidate receipt does not contain its output", nil)
	}
	if err := validateRowValuePolicyReceiptSnapshot(receipt, snapshot); err != nil {
		return conflict("commands", "RECEIPT_STALE", "the row-value policy candidate no longer matches its compilation snapshot", nil, err)
	}

	executionAuth, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || executionAuth.Snapshot.ValidateToken(receipt.SnapshotToken) != nil {
		return conflict("commands", "RECEIPT_STALE", "the row-value policy candidate is no longer authorized for execution", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, executionAuth); err != nil {
		return conflict("commands", "RECEIPT_STALE", "the row-value policy candidate is no longer authorized for execution", nil, err)
	}
	if executionAuth.Snapshot.Identity.ShapeDigest != receipt.ShapeDigest {
		return conflict("commands", "RECEIPT_STALE", "the row-value policy candidate capability shape changed", nil, nil)
	}
	if err := validateSameAuthorizedReceiptSnapshot(receipt, compilationAuth); err != nil {
		return conflict("commands", "RECEIPT_STALE", "the row-value policy candidate authorization changed during compilation", nil, err)
	}

	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: 0, OutputNames: []string{command.OutputID}, IncludeRowIdentity: true, IncludeSourceIdentity: true,
	}
	applyAuthorizedScope(&bindings, executionAuth, false)
	if err := s.config.ValidateReceiptStream(ctx, receipt, bindings); err != nil {
		return rowValuePolicyValidationFailure(err)
	}
	return nil
}

func rowValuePolicyValidationFailure(err error) error {
	if err == nil {
		return nil
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		message := dataframeerrors.PublicMessage(err)
		if userErr.Code() == string(dataframeerrors.CodeConstructionRowValueMultipleValues) {
			message = "This cohort has multiple distinct values for this field. Keep All unique values."
		}
		if userErr.Retryable() {
			return unavailable("commands", userErr.Code(), message, err)
		}
		return unprocessable("commands", userErr.Code(), message, err)
	}
	return unprocessable("commands", "ROW_VALUE_POLICY_VALIDATION_FAILED", "The selected row-value policy could not be validated across all grouped records.", err)
}

func validateRowValuePolicyReceiptSnapshot(receipt *explorer.CompilationReceipt, snapshot capability.Snapshot) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	identity := snapshot.Identity
	if receipt.SnapshotToken != snapshot.Token {
		return fmt.Errorf("receipt snapshot token changed")
	}
	if projectid.Canonical(receipt.Project) != projectid.Canonical(identity.Project) {
		return fmt.Errorf("receipt project changed")
	}
	if receipt.SourceGeneration != identity.Generation {
		return fmt.Errorf("receipt source generation changed")
	}
	if receipt.CapabilitySchemaDigest != identity.SchemaDigest {
		return fmt.Errorf("receipt capability schema changed")
	}
	if receipt.AuthorizationScopeDigest != identity.AuthorizationScopeDigest {
		return fmt.Errorf("receipt authorization scope changed")
	}
	if receipt.ShapeDigest != identity.ShapeDigest {
		return fmt.Errorf("receipt capability shape changed")
	}
	return nil
}

func validateSameAuthorizedReceiptSnapshot(receipt *explorer.CompilationReceipt, authorized AuthorizedCapability) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return err
	}
	if authorized.Snapshot.Identity.ShapeDigest != receipt.ShapeDigest {
		return fmt.Errorf("receipt capability shape changed")
	}
	return nil
}
