package publication

import "context"

type PublicationIdentity = BundleIdentity

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
	SetSourceRowMetadata(context.Context, string, SourceRowMetadata) error
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
