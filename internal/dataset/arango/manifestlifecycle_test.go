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
	activateAndAssert(g2)
	activateAndAssert(g1)
	activateAndAssert(g3)
	activateAndAssert(g3)
}

func TestGenerationIdentityFormatRoundTripsAgainstArango(t *testing.T) {
	ctx, lifecycle := gacOpen(t, 30*time.Second)
	manifest := gacLoadStaged(t, ctx, lifecycle, "gac-identity-project", "gac-identity-generation", strings.Repeat("a", 64))
	if manifest.IdentityFormat != publication.IdentityFormatCompact {
		t.Fatalf("read identity format = %q, want %q", manifest.IdentityFormat, publication.IdentityFormatCompact)
	}
}

func TestManifestIdentityFormatCASRejectsMutationAgainstArango(t *testing.T) {
	owned := arangotest.Owned(t, "loom_verify_gac_cas_")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := owned.Client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap lifecycle collection: %v", err)
	}
	lifecycle, err := New(owned.Client)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := publication.NewRef("gac-cas-project", "gac-cas-generation")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:gac-cas", "", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	created, err := lifecycle.CreateManifest(ctx, manifest)
	if err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	setManifestMetadata := func(state publication.State, format string) {
		t.Helper()
		err := owned.Client.ExecuteAQL(ctx, `
FOR manifest IN @@collection
  FILTER manifest._key == @key
  UPDATE manifest WITH {state: @state, identityFormat: @format} IN @@collection
  RETURN NEW`, map[string]any{
			"@collection": LifecycleCollection,
			"key":         manifestDocumentKey(ref),
			"state":       string(state),
			"format":      format,
		})
		if err != nil {
			t.Fatalf("set manifest identity format %q: %v", format, err)
		}
	}
	setManifestMetadata(publication.StateLoading, publication.IdentityFormatLegacy)
	_, transitionErr := lifecycle.TransitionManifest(ctx, created, publication.StateStaged)
	if !errors.Is(transitionErr, ErrManifestTransitionConflict) {
		t.Errorf("transition with changed identity format = %v, want conflict", transitionErr)
	}
	loading, err := lifecycle.ReadManifest(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if loading.State != publication.StateLoading || loading.IdentityFormat != publication.IdentityFormatLegacy {
		t.Errorf("mismatched transition mutated manifest: state=%s identityFormat=%q", loading.State, loading.IdentityFormat)
	}

	setManifestMetadata(publication.StateLoading, publication.IdentityFormatCompact)
	loading, err = lifecycle.ReadManifest(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, loading, publication.StateStaged)
	if err != nil {
		t.Fatalf("transition after restoring identity format: %v", err)
	}
	setManifestMetadata(publication.StateStaged, publication.IdentityFormatLegacy)
	if err := lifecycle.Activate(ctx, staged); !errors.Is(err, ErrActivationConflict) {
		t.Errorf("activation with changed identity format = %v, want conflict", err)
	}
	stored, err := lifecycle.ReadManifest(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != publication.StateStaged || stored.IdentityFormat != publication.IdentityFormatLegacy {
		t.Errorf("mismatched activation mutated manifest: state=%s identityFormat=%q", stored.State, stored.IdentityFormat)
	}
	if _, err := lifecycle.ResolveActiveManifest(ctx, ref.Project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Errorf("mismatched activation advanced active pointer: %v", err)
	}
}

func TestLegacyReadyManifestRemainsActivatableAgainstArango(t *testing.T) {
	ctx, client, lifecycle := openDatasetStore(t, 45*time.Second, gacDatabasePrefix)
	const project, generation = "gac-legacy-project", "gac-legacy-generation"
	staged := gacLoadStaged(t, ctx, lifecycle, project, generation, strings.Repeat("a", 64))

	setStoredState := func(state string) {
		t.Helper()
		if err := client.ExecuteAQL(ctx, `UPDATE @key WITH {state: @state} IN @@collection`, map[string]any{
			"@collection": LifecycleCollection, "key": manifestDocumentKey(staged.Dataset), "state": state,
		}); err != nil {
			t.Fatalf("write historical state %q: %v", state, err)
		}
	}
	for _, raw := range []string{"READY", "SUPERSEDED"} {
		setStoredState(raw)
		decoded, err := lifecycle.ReadManifest(ctx, staged.Dataset)
		if err != nil || decoded.State != publication.StateReady || !decoded.IsStaged() {
			t.Fatalf("read %s manifest = %+v, %v", raw, decoded, err)
		}
	}

	setStoredState("READY")
	ready, err := lifecycle.ReadManifest(ctx, staged.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Activate(ctx, ready); err != nil {
		t.Fatalf("activate stored READY manifest: %v", err)
	}
	active, err := lifecycle.ResolveActiveManifest(ctx, project)
	if err != nil || active.Dataset != ready.Dataset || active.State != publication.StateReady {
		t.Fatalf("resolve active historical manifest = %+v, %v", active, err)
	}
}
