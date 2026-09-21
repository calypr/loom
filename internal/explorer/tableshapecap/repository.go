package tableshapecap

import "context"

// Repository persists immutable, tenant- and parent-scoped artifacts. Put is
// create-once: an existing ID is returned only when its canonical content is
// identical. Get always receives the complete binding to prevent a short ID
// from becoming a cross-output bearer token.
type Repository interface {
	PutCatalog(context.Context, CatalogReceipt) (CatalogReceipt, error)
	GetCatalog(context.Context, Binding, string) (CatalogReceipt, error)
	PutResolution(context.Context, ResolutionReceipt) (ResolutionReceipt, error)
	GetResolution(context.Context, Binding, string, string) (ResolutionReceipt, error)
}
