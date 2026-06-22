package proto

import (
	"context"

	"arangodb-proto/internal/catalog"
)

const (
	FieldCatalogCollection = catalog.FieldCatalogCollection
	TraversalModeStorage   = catalog.TraversalModeStorage
	TraversalModeBuilder   = catalog.TraversalModeBuilder
)

type AuthResourcePathOptions = catalog.AuthResourcePathOptions
type FieldCatalogDocument = catalog.FieldCatalogDocument
type PopulatedFieldOptions = catalog.PopulatedFieldOptions
type PopulatedField = catalog.PopulatedField
type PopulatedReferenceOptions = catalog.PopulatedReferenceOptions
type PopulatedReference = catalog.PopulatedReference

func DiscoverExistingAuthResourcePaths(ctx context.Context, opts AuthResourcePathOptions) ([]string, error) {
	return catalog.DiscoverExistingAuthResourcePaths(ctx, opts)
}

func DiscoverPopulatedFields(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
	return catalog.DiscoverPopulatedFields(ctx, opts)
}

func DiscoverPopulatedReferences(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
	return catalog.DiscoverPopulatedReferences(ctx, opts)
}
