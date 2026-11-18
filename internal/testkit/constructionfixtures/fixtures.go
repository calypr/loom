package constructionfixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

type Reader struct {
	Values map[string]published.Materialization
	Calls  []string
	Pins   []string
}

func (r *Reader) ExactExecutionMaterialization(_ context.Context, revision, output string) (published.Materialization, error) {
	key := revision + "/" + output
	r.Calls = append(r.Calls, key)
	value, ok := r.Values[key]
	if !ok {
		return published.Materialization{}, fmt.Errorf("missing exact fixture %s", key)
	}
	return value, nil
}

func (r *Reader) WithExecutionReadPins(ctx context.Context, ids []string, visit func(context.Context) error) error {
	r.Pins = append([]string(nil), ids...)
	return visit(ctx)
}

type BundleCatalog struct {
	Executions []publication.BundleExecution
	Pointers   map[string]publication.BundlePointer
	ListCalls  int
}

func (c *BundleCatalog) ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error) {
	c.ListCalls++
	return append([]publication.BundleExecution(nil), c.Executions...), nil
}

func (c *BundleCatalog) GetPointer(_ context.Context, name string) (publication.BundlePointer, error) {
	pointer, ok := c.Pointers[name]
	if !ok {
		return publication.BundlePointer{}, publication.ErrBundleNotFound
	}
	return pointer, nil
}

type Explorers struct {
	Value *explorer.Explorer
}

func (f Explorers) Get(context.Context, string, string) (*explorer.Explorer, error) {
	return f.Value, nil
}

type Revisions struct {
	Values map[string]*explorer.Revision
	Calls  []string
}

func (r *Revisions) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	r.Calls = append(r.Calls, id)
	revision, ok := r.Values[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return revision, nil
}

type Fixture struct {
	Reader       *Reader
	Catalog      *BundleCatalog
	Explorers    Explorers
	Revisions    *Revisions
	Capabilities lifecycle.CapabilityResolver
	Snapshot     capability.Snapshot
	Digest       string
}

func New(scope authscope.ReadScope) (Fixture, error) {
	snapshot := capabilityfixtures.AuthorizedCapabilitySnapshot("generation-a", resolver.ScopeDigest(scope))
	workspace, err := authoringv2.DecodeWorkspace(BaselineExplorerWorkspace())
	if err != nil {
		return Fixture{}, err
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		return Fixture{}, err
	}
	digest, err := workspace.Digest()
	if err != nil {
		return Fixture{}, err
	}
	return Fixture{
		Reader:    &Reader{Values: map[string]published.Materialization{}},
		Catalog:   &BundleCatalog{Pointers: map[string]publication.BundlePointer{}},
		Explorers: Explorers{Value: &explorer.Explorer{Project: "project-a", ExplorerID: "builder-a", DraftVersion: 1, DraftDigest: digest, DraftConfig: draft}},
		Revisions: &Revisions{Values: map[string]*explorer.Revision{}},
		Capabilities: lifecycle.CapabilityResolver{ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		Snapshot: snapshot,
		Digest:   digest,
	}, nil
}

func Execution(id string, selector dataset.DataframeSelector, project, generation string, createdAt time.Time) publication.BundleExecution {
	verifiedAt := createdAt
	execution := publication.BundleExecution{
		ID: id,
		BundleIdentity: publication.BundleIdentity{
			Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: project, DatasetGeneration: generation,
			RecipeDigest: "recipe-" + id, SchemaDigest: "schema-" + id, EngineVersion: "engine-v1",
		},
		State: publication.BundlePublished, CreatedAt: createdAt,
		Outputs: []publication.BundleOutputRecord{{
			Name: selector.Output, PhysicalTable: "physical_" + id,
			Selector: selector, State: publication.BundlePublished, VerifiedAt: &verifiedAt,
			Columns: []publication.PhysicalColumn{{ID: "stable-lab-value", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "decimal", Nullable: true}},
		}},
	}
	SetExecutionScope(&execution, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	return execution
}

func SetExecutionScope(execution *publication.BundleExecution, scope authscope.ReadScope) {
	bindings := recipe.RuntimeBindings{
		Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		AuthScopeMode: scope.Mode, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...),
	}
	execution.ReceiptID = "receipt-" + execution.ID
	execution.AuthScopeMode = string(scope.Mode)
	execution.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	execution.ScopeDigest = reciperuntime.ScopeDigest(bindings)
}

func Materialization(execution publication.BundleExecution, selector dataset.DataframeSelector, scope authscope.ReadScope) published.Materialization {
	bindings := recipe.RuntimeBindings{
		Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		AuthScopeMode: scope.Mode, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...),
	}
	return published.Materialization{
		ID: execution.ID + ":" + selector.Output, Name: "Observation", Revision: execution.ID,
		ReceiptID: "receipt-" + execution.ID, SchemaDigest: "schema-" + execution.ID, ScopeDigest: reciperuntime.ScopeDigest(bindings),
		AuthScopeMode: string(scope.Mode), Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		State: published.StateReady, ScopeUnrestricted: scope.Mode == authscope.ReadScopeUnrestricted,
		AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), PhysicalTable: "physical_" + execution.ID,
		CreatedAt: execution.CreatedAt, Selector: selector,
		SourceRow: &publication.SourceRowMetadata{ResourceType: "Observation", IDColumn: "observation_id"},
		Columns:   []published.Column{{ID: "stable-lab-value", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "decimal", Nullable: true}},
	}
}
