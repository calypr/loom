package publication

import "context"

type PublicationIdentity struct {
	Name               string
	TranslationVersion string

	OutputName        string
	Project           string
	DatasetGeneration string
	RecipeDigest      string
	SchemaDigest      string
	ReceiptID         string
	ScopeDigest       string
	EngineVersion     string
	AuthScopeMode     string
	AuthResourcePaths []string
}

type PublishedOutput struct {
	Name         string
	PhysicalName string
	RowCount     int64
	ByteCount    int64
}

type Target interface {
	Begin(context.Context, PublicationIdentity, []OutputSchema) (Transaction, error)
	SupportsObjectValues() bool
}

type Transaction interface {
	WriteBatch(context.Context, string, []map[string]any) error
	FinalizeSchema(context.Context, []OutputSchema) error
	SetFinalSchemaDigest(string) error
	SetQualityReports(context.Context, []QualityReport) error
	Commit(context.Context) ([]PublishedOutput, error)
	Abort(context.Context, error) error
	Idempotent() bool
	ExistingPublishedOutputs() []PublishedOutput
	ExistingQualityReports() []QualityReport
}

type Result struct {
	Outputs        []PublishedOutput
	QualityReports []QualityReport
}
