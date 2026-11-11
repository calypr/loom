package dataset

import "context"

// ManifestLifecycle is the consumer-facing contract over dataset generation
// manifest state: creation, readback, CAS transition, active resolution, and
// the activation compare-and-set. Declared in the neutral dataset package
// (arch-backend-06) so internal/ingest and cmd/loom-data drive the lifecycle
// through the public postgres factory's Runtime.Manifests port without
// importing any backend implementation. The PostgreSQL store in
// internal/backends/postgres/internal/dataset satisfies it (compile-time
// assertion in the factory assembly); the Arango release store remains
// separate and untouched.
type ManifestLifecycle interface {
	CreateManifest(ctx context.Context, manifest Manifest) (Manifest, error)
	ReadManifest(ctx context.Context, ref Ref) (Manifest, error)
	TransitionManifest(ctx context.Context, manifest Manifest, next State) (Manifest, error)
	ResolveActiveManifest(ctx context.Context, project string) (Manifest, error)
	ActivateGeneration(ctx context.Context, candidate Manifest, expected *Ref) error
}
