package publication

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	dataframepublished "github.com/calypr/loom/internal/dataframe/published"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/projectid"
)

func artifactQualityReport(revision explorer.Revision, outputID string, receipt receiptmodel.CompilationReceipt) (publication.QualityReport, error) {
	var found *publication.QualityReport
	for i := range revision.QualityReports {
		if revision.QualityReports[i].Output == outputID {
			if found != nil {
				return publication.QualityReport{}, fmt.Errorf("duplicate quality reports for output %q", outputID)
			}
			copy := revision.QualityReports[i]
			found = &copy
		}
	}
	if found == nil {
		return publication.QualityReport{}, fmt.Errorf("quality report for output %q is missing", outputID)
	}
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"receipt", found.ReceiptID, receipt.ID},
		{"project", projectid.Canonical(found.Project), projectid.Canonical(receipt.Project)},
		{"generation", found.DatasetGeneration, receipt.SourceGeneration},
		{"completeness", string(found.Completeness), string(publication.QualityComplete)},
		{"verdict", string(found.Verdict), string(publication.QualityPassed)},
		{"policy", found.PolicyVersion, publication.DefaultQualityPolicyVersion},
	}
	for _, check := range checks {
		if check.got != check.want {
			return publication.QualityReport{}, fmt.Errorf("quality report for output %q has mismatched %s", outputID, check.name)
		}
	}
	if strings.TrimSpace(found.ScopeDigest) == "" {
		return publication.QualityReport{}, fmt.Errorf("quality report for output %q has no publication scope", outputID)
	}
	return *found, nil
}

func artifactMetadata(receipt *receiptmodel.CompilationReceipt, revision *explorer.Revision, materialization dataframepublished.Materialization, outputID string, quality publication.QualityReport) (json.RawMessage, json.RawMessage, json.RawMessage, json.RawMessage, error) {
	archiveRecipe, err := receipt.Bundle.ArchiveCanonicalJSON()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	selection := struct {
		Recipe            json.RawMessage `json:"recipe"`
		DatasetGeneration string          `json:"datasetGeneration"`
	}{Recipe: archiveRecipe, DatasetGeneration: receipt.SourceGeneration}
	selectionRaw, err := json.Marshal(selection)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	interpretations, err := json.Marshal(receipt.ResolvedInterpretations)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	provenance, err := json.Marshal(struct {
		Project           string `json:"project"`
		ExplorerID        string `json:"explorerId"`
		RevisionID        string `json:"revisionId"`
		ReceiptID         string `json:"receiptId"`
		ExecutionID       string `json:"executionId"`
		OutputID          string `json:"outputId"`
		DatasetGeneration string `json:"datasetGeneration"`
		SchemaDigest      string `json:"schemaDigest"`
		ScopeDigest       string `json:"authorizationScopeDigest"`
	}{Project: receipt.Project, ExplorerID: revision.ExplorerID, RevisionID: revision.ID, ReceiptID: receipt.ID, ExecutionID: materialization.Revision, OutputID: outputID, DatasetGeneration: materialization.DatasetGeneration, SchemaDigest: materialization.SchemaDigest, ScopeDigest: receipt.AuthorizationScopeDigest})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	qualityRaw, err := json.Marshal([]publication.QualityReport{quality})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return selectionRaw, interpretations, provenance, qualityRaw, nil
}

func validateArtifactMaterialization(materialization dataframepublished.Materialization, revision explorer.Revision, receipt receiptmodel.CompilationReceipt, scope authscope.ReadScope, outputID string) error {
	if projectid.Canonical(materialization.Project) != projectid.Canonical(receipt.Project) || materialization.DatasetGeneration != receipt.SourceGeneration || materialization.ReceiptID != receipt.ID || materialization.Revision != revision.Publication.ExecutionID || materialization.Name == "" || outputID == "" {
		return fmt.Errorf("materialization identity does not match receipt/revision")
	}
	expectedPaths := append([]string(nil), scope.AuthResourcePaths...)
	actualPaths := append([]string(nil), materialization.AuthResourcePaths...)
	slices.Sort(expectedPaths)
	slices.Sort(actualPaths)
	if materialization.ScopeUnrestricted != scope.Unrestricted() || !slices.Equal(actualPaths, expectedPaths) {
		return fmt.Errorf("materialization authorization scope does not match current receipt authorization")
	}
	return nil
}

func validateArtifactReceiptIdentity(record explorer.ArtifactRecord, receipt *receiptmodel.CompilationReceipt) error {
	if receipt == nil || record.ReceiptID != receipt.ID || record.Project != projectid.Canonical(receipt.Project) || record.DatasetGeneration != receipt.SourceGeneration || record.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest || record.SnapshotToken != receipt.SnapshotToken {
		return fmt.Errorf("artifact record does not match receipt")
	}
	return nil
}

func validateArtifactRevisionReceipt(revision explorer.Revision, receipt receiptmodel.CompilationReceipt) error {
	if revision.CompilationReceiptID != receipt.ID || revision.SourceGeneration != receipt.SourceGeneration || projectid.Canonical(revision.Project) != projectid.Canonical(receipt.Project) {
		return fmt.Errorf("revision does not match receipt")
	}
	return nil
}

func validateArtifactRevisionIdentity(record explorer.ArtifactRecord, revision explorer.Revision) error {
	if record.RevisionID != revision.ID || record.Project != projectid.Canonical(revision.Project) || record.ExplorerID != revision.ExplorerID || record.ExecutionID != revision.Publication.ExecutionID || record.DatasetGeneration != revision.SourceGeneration {
		return fmt.Errorf("artifact record does not match revision")
	}
	return nil
}

func validateArtifactRecordIdentity(existing, expected explorer.ArtifactRecord) error {
	if existing.ID != expected.ID || existing.Project != expected.Project || existing.ExplorerID != expected.ExplorerID || existing.RevisionID != expected.RevisionID || existing.OutputID != expected.OutputID || existing.ReceiptID != expected.ReceiptID || existing.ExecutionID != expected.ExecutionID || existing.DatasetGeneration != expected.DatasetGeneration || existing.SchemaDigest != expected.SchemaDigest || existing.AuthorizationScopeDigest != expected.AuthorizationScopeDigest || existing.SnapshotToken != expected.SnapshotToken || existing.IdempotencyKey != expected.IdempotencyKey {
		return fmt.Errorf("artifact identities differ")
	}
	return nil
}

func artifactFailureCode(err error) string {
	if err == nil {
		return "ARTIFACT_PREPARATION_FAILED"
	}
	if strings.Contains(err.Error(), "pin") || strings.Contains(err.Error(), "PIN") {
		return "EXECUTION_READ_PIN_LOST"
	}
	return "ARTIFACT_PREPARATION_FAILED"
}
