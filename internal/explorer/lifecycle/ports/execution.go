package ports

import (
	"context"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type ArtifactPublishedReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (dataframepublished.Materialization, error)
	StreamExactExport(context.Context, dataframepublished.ExactExportRequest, dataframepublished.ExactExportVisitor) (dataframepublished.ExactExportResult, error)
}

type Execution struct {
	ID                   string
	Name                 string
	Project              string
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	ScopeDigest          string
	State                string
	Outputs              []ExecutionOutput
	QualityReports       []publication.QualityReport
}

type ExecutionOutput struct {
	Name     string
	State    string
	RowCount *int
	Columns  []publication.PhysicalColumn
}

type ReceiptMaterializer func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (Execution, error)
