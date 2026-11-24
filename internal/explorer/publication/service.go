package publication

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

type GenerationValidator func(context.Context, string, string) error
type ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error
type ReleasePreparer func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error)

type RevisionWriter interface {
	InsertRevision(context.Context, explorer.Revision) (*explorer.Revision, error)
}

type Config struct {
	Core                       *authoringcore.Core
	Store                      *explorer.Service
	ExplorerStore              explorer.ExplorerStore
	RevisionWriter             RevisionWriter
	Capability                 ports.CapabilityResolver
	SelectionMembersCollection string
	CompileReceipt             ports.ReceiptCompiler
	MaterializeReceipt         ports.ReceiptMaterializer
	ArtifactStore              explorer.ArtifactStore
	PublishedReader            ports.ArtifactPublishedReader
	ArtifactTTL                time.Duration
	ArtifactMaxRows            int64
	ArtifactMaxBytes           int64
	ValidateReleaseGeneration  GenerationValidator
	ActivateRelease            ReleaseActivator
	PrepareRelease             ReleasePreparer
	PersistPublishedWorkspace  func(context.Context, string, string, []byte) error
	Now                        func() time.Time
}

type Service struct {
	core   *authoringcore.Core
	store  *explorer.Service
	config Config
	now    func() time.Time
}

func NewService(config Config) (*Service, error) {
	if config.Core == nil || config.Store == nil {
		return nil, fmt.Errorf("authoring core and Explorer store are required")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{core: config.Core, store: config.Store, config: config, now: now}, nil
}

type PublishRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	Actor      string
}

type PublishResult struct {
	Receipt   *receiptmodel.CompilationReceipt
	Revision  *explorer.Revision
	Execution ports.Execution
}

type RepositoryPublishRequest struct {
	Project    string
	Generation string
	Workspace  ws.Workspace
	Commit     string
	Actor      string
}

type RepositoryPublishResult struct {
	Receipt          *receiptmodel.CompilationReceipt
	Owner            *explorer.Explorer
	Revision         *explorer.Revision
	Execution        ports.Execution
	Materializations []viewer.Materialization
	Dataset          viewer.DatasetMetadata
	Publication      viewer.PublicationMetadata
}

type ArtifactRequest struct {
	Project        string
	ExplorerID     string
	RevisionID     string
	OutputID       string
	IdempotencyKey string
}

type ArtifactResult struct{ Record *explorer.ArtifactRecord }

type ArtifactOpenRequest struct {
	Project    string
	ExplorerID string
	ArtifactID string
}
