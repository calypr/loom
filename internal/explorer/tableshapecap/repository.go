package tableshapecap

import "context"

// Repository persists immutable, tenant- and parent-scoped artifacts. Put is
// create-once: an existing ID is returned only when its canonical content is
// identical. GetCatalog verifies a complete binding; GetCatalogForLookup
// scopes retrieval to the current request identity known before loading the
// persisted base receipt referenced by that binding.
type Repository interface {
	PutCatalog(context.Context, CatalogReceipt) (CatalogReceipt, error)
	GetCatalog(context.Context, Binding, string) (CatalogReceipt, error)
	GetCatalogForLookup(context.Context, CatalogLookup, string) (CatalogReceipt, error)
	PutCategoryScan(context.Context, CategoryScanReceipt) (CategoryScanReceipt, error)
	GetCategoryScan(context.Context, Binding, string, string) (CategoryScanReceipt, error)
	PutResolution(context.Context, ResolutionReceipt) (ResolutionReceipt, error)
	GetResolution(context.Context, Binding, string, string) (ResolutionReceipt, error)
}
