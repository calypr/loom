package arango

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

const gacDatabasePrefix = "loom_verify_gac_"

func gacOpen(t *testing.T, budget time.Duration) (context.Context, *Store) {
	t.Helper()
	database := arangotest.Owned(t, gacDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	if err := database.Client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	lifecycle, err := New(database.Client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, lifecycle
}

// gacLoadStaged creates and reads back a staged manifest through the production
// store so Activate receives a persisted candidate with valid metadata.
func gacLoadStaged(t *testing.T, ctx context.Context, lifecycle *Store, project, generation, digest string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:gac-"+generation, "", digest, []string{"Patient"})
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", generation, err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("new manifest %s: %v", generation, err)
	}
	if _, err := lifecycle.CreateManifest(ctx, manifest); err != nil {
		t.Fatalf("CreateManifest %s: %v", generation, err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, manifest, publication.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
	if !staged.IsStaged() {
		t.Fatalf("manifest %s not staged after transition: %s", generation, staged.State)
	}
	read, err := lifecycle.ReadManifest(ctx, ref)
	if err != nil || !read.IsStaged() {
		t.Fatalf("persisted %s manifest is not staged: manifest=%+v err=%v", generation, read, err)
	}
	return read
}

// TestGenerationActivationBlindlyFlipsSharedPointerAgainstArango exercises the
// manifest-store contract: an explicit activation selects its requested staged
// generation, switching generations is allowed, and activating the current
// generation again is idempotent.
func TestGenerationActivationBlindlyFlipsSharedPointerAgainstArango(t *testing.T) {
	ctx, lifecycle := gacOpen(t, 240*time.Second)
	const project = "gac-activation-project"
	const g1 = "gac-activation-gen-1"
	const g2 = "gac-activation-gen-2"
	const g3 = "gac-activation-gen-3"

	staged := map[string]publication.Manifest{
		g1: gacLoadStaged(t, ctx, lifecycle, project, g1, strings.Repeat("a", 64)),
		g2: gacLoadStaged(t, ctx, lifecycle, project, g2, strings.Repeat("b", 64)),
		g3: gacLoadStaged(t, ctx, lifecycle, project, g3, strings.Repeat("c", 64)),
	}
	if active, err := lifecycle.ResolveActiveManifest(ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("before activation, ResolveActiveManifest = generation %q, err %v; want no active generation", active.Dataset.Generation, err)
	}

	activateAndAssert := func(generation string) {
		t.Helper()
		if err := lifecycle.Activate(ctx, staged[generation]); err != nil {
			t.Fatalf("Activate(%s): %v", generation, err)
		}
		active, err := lifecycle.ResolveActiveManifest(ctx, project)
		if err != nil || active.Dataset.Generation != generation {
			t.Fatalf("Activate(%s) resolved generation %q, err %v", generation, active.Dataset.Generation, err)
		}
	}

	activateAndAssert(g1)
	activateAndAssert(g2) // Explicit cross-generation switching is supported.
	activateAndAssert(g1) // Switching back to a previously active generation is supported.
	activateAndAssert(g3)
	activateAndAssert(g3) // Reactivating the current generation is idempotent.
}
