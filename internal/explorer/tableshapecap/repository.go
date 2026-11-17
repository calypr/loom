package tableshapecap

import "context"

type Repository interface {
	PutCatalog(context.Context, CatalogReceipt) (CatalogReceipt, error)
	GetCatalog(context.Context, Binding, string) (CatalogReceipt, error)
	GetCatalogForLookup(context.Context, CatalogLookup, string) (CatalogReceipt, error)
	PutCategoryScan(context.Context, CategoryScanReceipt) (CategoryScanReceipt, error)
	GetCategoryScan(context.Context, Binding, string, string) (CategoryScanReceipt, error)
	PutResolution(context.Context, ResolutionReceipt) (ResolutionReceipt, error)
	GetResolution(context.Context, Binding, string, string) (ResolutionReceipt, error)
}
