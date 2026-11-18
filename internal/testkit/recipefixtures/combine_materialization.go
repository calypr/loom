package recipefixtures

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
)

type CombineMaterializationReader struct {
	Values map[string]published.Materialization
	Calls  []string
}

func (r *CombineMaterializationReader) ExactExecutionMaterialization(_ context.Context, revision, output string) (published.Materialization, error) {
	r.Calls = append(r.Calls, revision+"/"+output)
	materialization, ok := r.Values[revision+"/"+output]
	if !ok {
		return published.Materialization{}, errors.New("materialization not found")
	}
	return materialization, nil
}

func CombineInputReference(recipeName, output, revision string) ir.PhysicalCombineInputRef {
	selector := dataset.DataframeSelector{Recipe: recipeName, TranslationVersion: "v1", Output: output}
	return ir.PhysicalCombineInputRef{TableID: selector.Key(), RevisionID: revision, OutputID: output}
}

func CombineInputMaterialization(reference ir.PhysicalCombineInputRef, bindings recipe.RuntimeBindings, scopeDigest string) published.Materialization {
	selector := dataset.DataframeSelector{Recipe: "labs", TranslationVersion: "v1", Output: reference.OutputID}
	if reference.TableID == CombineInputReference("visits", "Encounter", reference.RevisionID).TableID {
		selector = dataset.DataframeSelector{Recipe: "visits", TranslationVersion: "v1", Output: reference.OutputID}
	}
	return published.Materialization{
		ID: reference.RevisionID + ":" + reference.OutputID, Revision: reference.RevisionID,
		ReceiptID: "receipt-" + reference.RevisionID, SchemaDigest: "schema-" + reference.RevisionID,
		ScopeDigest: scopeDigest, AuthScopeMode: string(bindings.AuthScopeMode),
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		State: published.StateReady, ScopeUnrestricted: bindings.AuthScopeMode == authscope.ReadScopeUnrestricted,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...), PhysicalTable: "physical_" + reference.OutputID,
		Selector: selector,
		Columns: []published.Column{
			{ID: "stable-row-id", Name: "__loom_row_id", ClickHouse: "String", LogicalType: "string"},
			{ID: "stable-labs-value", Name: "value", ClickHouse: "Float64", LogicalType: "decimal"},
		},
	}
}
