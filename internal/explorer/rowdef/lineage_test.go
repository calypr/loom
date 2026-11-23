package rowdef

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestRowLineageBindsReceiptAndDefaultsToBoundedPage(t *testing.T) {
	snapshot := rowLineageSnapshot("project-a", "generation-a", "token")
	receipt := rowLineageReceipt(snapshot)
	service := rowLineageService(t, snapshot, receipt)
	service.config.RowLineage = func(_ context.Context, gotReceipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		if gotReceipt.ID != receipt.ID || bindings.DatasetGeneration != receipt.SourceGeneration || request.Output != "patients" || request.RowID != "row-7" || request.Offset != 0 || request.Limit != facade.DefaultRowLineageLimit {
			t.Fatalf("unexpected lineage invocation: receipt=%#v bindings=%#v request=%#v", gotReceipt, bindings, request)
		}
		return dataframeexecution.RowLineageResult{Contributors: []dataframeexecution.RowLineageContributor{}, HasMore: true, NextOffset: request.Limit}, nil
	}
	result, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != receipt.ID || result.Status != "COMPLETE" || !result.HasMore || result.NextOffset == nil || *result.NextOffset != facade.DefaultRowLineageLimit || result.Contributors == nil {
		t.Fatalf("unexpected row lineage page: %#v", result)
	}
}

func TestRowLineageMapsMissingFinalRowToNotFound(t *testing.T) {
	snapshot := rowLineageSnapshot("project-a", "generation-a", "token")
	receipt := rowLineageReceipt(snapshot)
	service := rowLineageService(t, snapshot, receipt)
	service.config.RowLineage = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		return dataframeexecution.RowLineageResult{}, dataframeexecution.ErrRowLineageRowNotFound
	}
	_, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "missing-row",
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassNotFound || lifecycleErr.Code != "PREVIEW_ROW_NOT_FOUND" {
		t.Fatalf("missing output row error = %v", err)
	}
}

func TestRowLineageMapsUnsupportedConstructionCapability(t *testing.T) {
	snapshot := rowLineageSnapshot("project-a", "generation-a", "token")
	receipt := rowLineageReceipt(snapshot)
	service := rowLineageService(t, snapshot, receipt)
	service.config.RowLineage = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		return dataframeexecution.RowLineageResult{}, &facade.RowLineageUnsupportedError{Capability: facade.RowLineageCapability{
			ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: "PIVOT",
		}}
	}
	_, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7",
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != "ROW_LINEAGE_OPERATION_UNSUPPORTED" {
		t.Fatalf("unsupported operation error = %v", err)
	}
}

func rowLineageSnapshot(project, generation, token string) capability.Snapshot {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return capability.Snapshot{Identity: capability.SnapshotIdentity{Project: project, Generation: generation, AuthorizationScopeDigest: hex.EncodeToString(sum[:]), SchemaDigest: "schema", ShapeDigest: "shape"}, Status: capability.StatusReady, Complete: true, Token: token}
}

func rowLineageReceipt(snapshot capability.Snapshot) *receiptmodel.CompilationReceipt {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "native-test",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
	}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	return &receiptmodel.CompilationReceipt{
		ReceiptFormatVersion:     receiptmodel.CurrentReceiptFormatVersion,
		CompilerContractVersion:  receiptmodel.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               "patients",
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		ID:                       "receipt-row-lineage",
	}
}

func rowLineageService(t *testing.T, snapshot capability.Snapshot, receipt *receiptmodel.CompilationReceipt) *Service {
	t.Helper()
	service, err := NewService(Config{
		Store: &explorer.Service{},
		Capability: ports.CapabilityResolver{
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
		},
		Core: lineageAuthoringOperations{receipt: receipt},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type lineageAuthoringOperations struct {
	authoringOperations
	receipt *receiptmodel.CompilationReceipt
}

func (c lineageAuthoringOperations) LookupReceipt(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error) {
	return c.receipt, nil
}

func (c lineageAuthoringOperations) ValidateExecutableReceiptRoute(*receiptmodel.CompilationReceipt, string, string) error {
	return nil
}
