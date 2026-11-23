package publication

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	"strings"
)

func selectorsForBundle(bundle recipe.Bundle) []dataset.DataframeSelector {
	selectors := make([]dataset.DataframeSelector, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		selectors = append(selectors, dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name})
	}
	return selectors
}

func verifyQueryableOutputs(bundle recipe.Bundle, execution ports.Execution) error {
	states := make(map[string]string, len(execution.Outputs))
	for _, output := range execution.Outputs {
		states[output.Name] = strings.ToUpper(output.State)
	}
	for _, output := range bundle.Outputs {
		state := states[output.Name]
		if state != "PUBLISHED" && state != "READY" && state != "ACTIVE" {
			return fmt.Errorf("output %q is not queryable (state %q)", output.Name, state)
		}
	}
	return nil
}

func verifyActivationQuality(receipt *receiptmodel.CompilationReceipt, execution ports.Execution) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if len(execution.QualityReports) == 0 {
		return fmt.Errorf("publication produced no quality evidence")
	}
	outputs := make([]publication.BundleOutputRecord, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		outputs = append(outputs, publication.BundleOutputRecord{Name: output.Name})
	}
	identity := publication.BundleIdentity{
		ReceiptID: receipt.ID, Project: execution.Project,
		DatasetGeneration: execution.SourceGeneration, ScopeDigest: execution.ScopeDigest,
	}
	if err := publication.ValidateQualityReports(identity, outputs, execution.QualityReports); err != nil {
		return err
	}
	for _, report := range execution.QualityReports {
		if report.PolicyVersion != publication.DefaultQualityPolicyVersion {
			return fmt.Errorf("output %q uses unsupported quality policy %q", report.Output, report.PolicyVersion)
		}
	}
	return nil
}

func materializations(bundle recipe.Bundle, execution ports.Execution) []viewer.Materialization {
	out := make([]viewer.Materialization, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name}
		out = append(out, viewer.Materialization{OutputID: output.Name, Output: output.Name, MaterializationID: execution.ID, Selector: &selector, Columns: output.Columns})
	}
	return out
}

func datasetMetadataFromExecution(bundle recipe.Bundle, generation, schemaDigest string, execution ports.Execution) viewer.DatasetMetadata {
	outputs := make([]viewer.DatasetOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		state := strings.ToUpper(output.State)
		selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: output.Name}
		outputs = append(outputs, viewer.DatasetOutput{Name: output.Name, State: state, Queryable: state == "PUBLISHED" || state == "READY" || state == "ACTIVE", Selector: &selector, Columns: output.Columns})
	}
	return viewer.DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: outputs}
}
