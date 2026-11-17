package dataset

import "context"

type ManifestLifecycle interface {
	CreateManifest(ctx context.Context, manifest Manifest) (Manifest, error)
	ReadManifest(ctx context.Context, ref Ref) (Manifest, error)
	TransitionManifest(ctx context.Context, manifest Manifest, next State) (Manifest, error)
	ResolveActiveManifest(ctx context.Context, project string) (Manifest, error)
	ActivateGeneration(ctx context.Context, candidate Manifest, expected *Ref) error
}
