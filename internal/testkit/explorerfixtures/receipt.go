package explorerfixtures

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func PersistTestNativeReceipt(ctx context.Context, t *testing.T, service *explorer.Service, request ports.CompileReceiptRequest, snapshot capability.Snapshot) (*receiptmodel.CompilationReceipt, error) {
	t.Helper()
	workspace := request.Workspace
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "native-test", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		return nil, err
	}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"c_patient","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	contractDigest, err := receiptmodel.CompilationArtifactDigest(contract)
	if err != nil {
		return nil, err
	}
	receipt := receiptmodel.CompilationReceipt{
		ReceiptFormatVersion: receiptmodel.CurrentReceiptFormatVersion, CompilerContractVersion: receiptmodel.CurrentCompilerContractVersion,
		Project: snapshot.Identity.Project, ExplorerID: request.ExplorerID, IntentDigest: intentDigest, SnapshotToken: snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest,
		ShapeDigest:      snapshot.Identity.ShapeDigest,
		SourceGeneration: snapshot.Identity.Generation, RecipeDigest: bundleDigest, ResolvedRecipeDigest: bundleDigest,
		ResolvedSchemaDigest: "resolved-schema", OutputContractDigest: contractDigest,
		NormalizedBundle: normalized, Bundle: bundle, CompiledConfig: json.RawMessage(`{"apiVersion":"` + explorerconfig.ConfigV2APIVersion + `","kind":"ExplorerConfig"}`), PublicOutputContract: contract,
		RowDefinitionProposal: request.RowDefinitionProposal,
		ConstructionProposal:  request.ConstructionProposal,
		EmittedColumns:        []viewer.EmittedColumn{{EmissionID: "em_patient", OutputID: "patients", CandidateID: "c_patient_id", OccurrenceID: ws.RootOccurrenceID, ProjectionMode: "FIRST", PublicColumn: "c_patient", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: true}},
		OutputFingerprints:    map[string]string{"patients": "fingerprint"}, OutputColumnProvenance: map[string]map[string]string{"patients": {"c_patient": "EXPLICIT"}}, CreatedAt: time.Now().UTC(),
	}
	receipt.CompilationKey, err = receiptmodel.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = receiptmodel.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	return service.StoreCompilationReceipt(ctx, receipt)
}
