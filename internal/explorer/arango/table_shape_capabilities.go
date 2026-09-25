package arango

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer/tableshapecap"
	store "github.com/calypr/loom/internal/store/arango"
)

const TableShapeCapabilitiesCollection = "loom_explorer_table_shape_capabilities"

func TableShapeCapabilitiesCollectionSpec() store.CollectionSpec {
	return store.CollectionSpec{
		Name: TableShapeCapabilitiesCollection,
		Indexes: [][]string{
			{"binding.project", "binding.explorerId", "binding.outputId", "kind", "id"},
			{"binding.project", "binding.explorerId", "binding.outputId", "parentCatalogId", "kind"},
		},
	}
}

type TableShapeCapabilityRepository struct{ client store.RowQueryer }

func NewTableShapeCapabilityRepository(client store.RowQueryer) (*TableShapeCapabilityRepository, error) {
	if client == nil {
		return nil, fmt.Errorf("table-shape capability Arango client is required")
	}
	return &TableShapeCapabilityRepository{client: client}, nil
}

func (r *TableShapeCapabilityRepository) PutCatalog(ctx context.Context, receipt tableshapecap.CatalogReceipt) (tableshapecap.CatalogReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	doc, err := catalogDocument(receipt)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	var stored *tableshapecap.CatalogReceipt
	err = r.client.QueryRows(ctx, tableShapeCapabilityInsertAQL, 1, map[string]interface{}{"@c": TableShapeCapabilitiesCollection, "doc": doc}, func(row map[string]interface{}) error {
		if len(row) == 0 {
			return nil
		}
		existing, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return fmt.Errorf("%w: %s: %v", tableshapecap.ErrIdentityClash, receipt.ID, decodeErr)
		}
		if existing.Kind != "CATALOG" || existing.Catalog == nil {
			return tableshapecap.ErrIdentityClash
		}
		if err := sameCatalog(*existing.Catalog, receipt); err != nil {
			return err
		}
		value := *existing.Catalog
		stored = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	if stored != nil {
		return *stored, nil
	}
	// Insert-ignore can return no row for an already present short key. Resolve
	// only through the caller's full binding; an out-of-scope key is a collision.
	got, getErr := r.GetCatalog(ctx, receipt.Binding, receipt.ID)
	if getErr != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
	}
	if err := sameCatalog(got, receipt); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	return got, nil
}

func (r *TableShapeCapabilityRepository) GetCatalog(ctx context.Context, binding tableshapecap.Binding, id string) (tableshapecap.CatalogReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	var found *tableshapecap.CatalogReceipt
	err := r.client.QueryRows(ctx, tableShapeCapabilityGetAQL, 1, tableShapeCapabilityBinds(binding, id, "CATALOG", ""), func(row map[string]interface{}) error {
		doc, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return decodeErr
		}
		if doc.Kind != "CATALOG" || doc.Catalog == nil || doc.Resolution != nil {
			return tableshapecap.ErrInvalid
		}
		if doc.Binding != binding || doc.Catalog.Binding != binding || doc.ID != id || doc.Catalog.ID != id {
			return tableshapecap.ErrNotFound
		}
		value := *doc.Catalog
		found = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	if found == nil {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return *found, nil
}

func (r *TableShapeCapabilityRepository) GetCatalogForLookup(ctx context.Context, lookup tableshapecap.CatalogLookup, id string) (tableshapecap.CatalogReceipt, error) {
	if err := lookup.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	var found *tableshapecap.CatalogReceipt
	err := r.client.QueryRows(ctx, tableShapeCapabilityGetForLookupAQL, 1, tableShapeCapabilityLookupBinds(lookup, id), func(row map[string]interface{}) error {
		doc, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return decodeErr
		}
		if doc.Kind != "CATALOG" || doc.Catalog == nil || doc.Resolution != nil {
			return tableshapecap.ErrInvalid
		}
		if doc.ID != id || doc.Catalog.ID != id || !lookup.Matches(doc.Binding) || doc.Catalog.Binding != doc.Binding {
			return tableshapecap.ErrNotFound
		}
		if err := doc.Catalog.Validate(); err != nil {
			return err
		}
		value := *doc.Catalog
		found = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	if found == nil {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return *found, nil
}

func (r *TableShapeCapabilityRepository) FindCatalogForLookup(ctx context.Context, lookup tableshapecap.CatalogLookup) (tableshapecap.CatalogReceipt, error) {
	if err := lookup.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	var found *tableshapecap.CatalogReceipt
	err := r.client.QueryRows(ctx, tableShapeCapabilityFindCatalogForLookupAQL, 2, tableShapeCapabilityFindLookupBinds(lookup), func(row map[string]interface{}) error {
		doc, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return decodeErr
		}
		if doc.Kind != "CATALOG" || doc.Catalog == nil || doc.Resolution != nil {
			return tableshapecap.ErrInvalid
		}
		if !lookup.Matches(doc.Binding) || doc.Catalog.Binding != doc.Binding || doc.Catalog.ID != doc.ID {
			return tableshapecap.ErrNotFound
		}
		if err := doc.Catalog.Validate(); err != nil {
			return err
		}
		if found != nil {
			return tableshapecap.ErrAmbiguous
		}
		value := *doc.Catalog
		found = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	if found == nil {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return *found, nil
}

func (r *TableShapeCapabilityRepository) PutCategoryScan(ctx context.Context, receipt tableshapecap.CategoryScanReceipt) (tableshapecap.CategoryScanReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	parent, err := r.GetCatalog(ctx, receipt.Binding, receipt.ParentCatalogID)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if err := tableshapecap.ValidateCategoryScanAgainstCatalog(parent, receipt); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	doc, err := categoryScanDocument(receipt)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	var stored *tableshapecap.CategoryScanReceipt
	err = r.client.QueryRows(ctx, tableShapeCapabilityInsertAQL, 1, map[string]interface{}{"@c": TableShapeCapabilitiesCollection, "doc": doc}, func(row map[string]interface{}) error {
		if len(row) == 0 {
			return nil
		}
		existing, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return fmt.Errorf("%w: %s: %v", tableshapecap.ErrIdentityClash, receipt.ID, decodeErr)
		}
		if existing.Kind != "CATEGORY_SCAN" || existing.CategoryScan == nil {
			return tableshapecap.ErrIdentityClash
		}
		if err := sameCategoryScan(*existing.CategoryScan, receipt); err != nil {
			return err
		}
		value := *existing.CategoryScan
		stored = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if stored != nil {
		return *stored, nil
	}
	got, getErr := r.GetCategoryScan(ctx, receipt.Binding, receipt.ParentCatalogID, receipt.ID)
	if getErr != nil {
		return tableshapecap.CategoryScanReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
	}
	if err := sameCategoryScan(got, receipt); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	return got, nil
}

func (r *TableShapeCapabilityRepository) GetCategoryScan(ctx context.Context, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.CategoryScanReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	var found *tableshapecap.CategoryScanReceipt
	err := r.client.QueryRows(ctx, tableShapeCapabilityGetAQL, 1, tableShapeCapabilityBinds(binding, id, "CATEGORY_SCAN", parentCatalogID), func(row map[string]interface{}) error {
		doc, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return decodeErr
		}
		if doc.Kind != "CATEGORY_SCAN" || doc.CategoryScan == nil || doc.Catalog != nil || doc.Resolution != nil {
			return tableshapecap.ErrInvalid
		}
		if doc.Binding != binding || doc.CategoryScan.Binding != binding || doc.ID != id || doc.CategoryScan.ID != id || doc.ParentCatalogID != parentCatalogID || doc.CategoryScan.ParentCatalogID != parentCatalogID {
			return tableshapecap.ErrNotFound
		}
		value := *doc.CategoryScan
		found = &value
		return nil
	})
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if found == nil {
		return tableshapecap.CategoryScanReceipt{}, tableshapecap.ErrNotFound
	}
	parent, err := r.GetCatalog(ctx, binding, parentCatalogID)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if err := tableshapecap.ValidateCategoryScanAgainstCatalog(parent, *found); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	return *found, nil
}

func (r *TableShapeCapabilityRepository) PutResolution(ctx context.Context, receipt tableshapecap.ResolutionReceipt) (tableshapecap.ResolutionReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	parent, err := r.GetCatalog(ctx, receipt.Binding, receipt.ParentCatalogID)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	prior, err := r.resolutionDependencies(ctx, receipt.Binding, receipt.ParentCatalogID, receipt)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(parent, receipt, prior...); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if receipt.Pivot != nil {
		scan, err := r.GetCategoryScan(ctx, receipt.Binding, receipt.ParentCatalogID, receipt.Pivot.CategoryDiscoveryID)
		if err != nil {
			return tableshapecap.ResolutionReceipt{}, err
		}
		if err := tableshapecap.ValidateResolutionAgainstCategoryScan(parent, scan, receipt); err != nil {
			return tableshapecap.ResolutionReceipt{}, err
		}
	}
	doc, err := resolutionDocument(receipt)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	var stored *tableshapecap.ResolutionReceipt
	err = r.client.QueryRows(ctx, tableShapeCapabilityInsertAQL, 1, map[string]interface{}{"@c": TableShapeCapabilitiesCollection, "doc": doc}, func(row map[string]interface{}) error {
		if len(row) == 0 {
			return nil
		}
		existing, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return fmt.Errorf("%w: %s: %v", tableshapecap.ErrIdentityClash, receipt.ID, decodeErr)
		}
		if existing.Kind != "RESOLUTION" || existing.Resolution == nil {
			return tableshapecap.ErrIdentityClash
		}
		if err := sameResolution(*existing.Resolution, receipt); err != nil {
			return err
		}
		value := *existing.Resolution
		stored = &value
		return nil
	})
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if stored != nil {
		return *stored, nil
	}
	got, getErr := r.GetResolution(ctx, receipt.Binding, receipt.ParentCatalogID, receipt.ID)
	if getErr != nil {
		return tableshapecap.ResolutionReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
	}
	if err := sameResolution(got, receipt); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	return got, nil
}

func (r *TableShapeCapabilityRepository) GetResolution(ctx context.Context, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	found, err := r.readResolutionRecord(ctx, binding, parentCatalogID, id)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	parent, err := r.GetCatalog(ctx, binding, parentCatalogID)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	prior, err := r.resolutionDependencies(ctx, binding, parentCatalogID, found)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(parent, found, prior...); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if found.Pivot != nil {
		scan, err := r.GetCategoryScan(ctx, binding, parentCatalogID, found.Pivot.CategoryDiscoveryID)
		if err != nil {
			return tableshapecap.ResolutionReceipt{}, err
		}
		if err := tableshapecap.ValidateResolutionAgainstCategoryScan(parent, scan, found); err != nil {
			return tableshapecap.ResolutionReceipt{}, err
		}
	}
	return found, nil
}

func (r *TableShapeCapabilityRepository) readResolutionRecord(ctx context.Context, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	var found *tableshapecap.ResolutionReceipt
	err := r.client.QueryRows(ctx, tableShapeCapabilityGetAQL, 1, tableShapeCapabilityBinds(binding, id, "RESOLUTION", parentCatalogID), func(row map[string]interface{}) error {
		doc, decodeErr := decodeTableShapeCapabilityDocument(row)
		if decodeErr != nil {
			return decodeErr
		}
		if doc.Kind != "RESOLUTION" || doc.Resolution == nil || doc.Catalog != nil {
			return tableshapecap.ErrInvalid
		}
		if doc.Binding != binding || doc.Resolution.Binding != binding || doc.ID != id || doc.Resolution.ID != id || doc.ParentCatalogID != parentCatalogID || doc.Resolution.ParentCatalogID != parentCatalogID {
			return tableshapecap.ErrNotFound
		}
		value := *doc.Resolution
		found = &value
		return nil
	})
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if found == nil {
		return tableshapecap.ResolutionReceipt{}, tableshapecap.ErrNotFound
	}
	return *found, nil
}

func (r *TableShapeCapabilityRepository) resolutionDependencies(ctx context.Context, binding tableshapecap.Binding, parentCatalogID string, current tableshapecap.ResolutionReceipt) ([]tableshapecap.ResolutionReceipt, error) {
	state := map[string]uint8{current.ID: 1}
	ordered := make([]tableshapecap.ResolutionReceipt, 0)
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return tableshapecap.ErrInvalid
		case 2:
			return nil
		}
		state[id] = 1
		previous, err := r.readResolutionRecord(ctx, binding, parentCatalogID, id)
		if err != nil {
			return err
		}
		switch previous.Kind {
		case tableshapecap.ResolutionPivot:
			if previous.Pivot == nil {
				return tableshapecap.ErrInvalid
			}
		case tableshapecap.ResolutionDerived:
			if previous.Derived == nil {
				return tableshapecap.ErrInvalid
			}
			if previous.Derived.PivotResolutionID != "" {
				if err := visit(previous.Derived.PivotResolutionID); err != nil {
					return err
				}
			}
			for _, operand := range []tableshapecap.ResolvedOperand{previous.Derived.Left, previous.Derived.Right} {
				if operand.Kind == tableshapecap.ResolvedOperandResolution {
					if err := visit(operand.ResolutionID); err != nil {
						return err
					}
				}
			}
		default:
			return tableshapecap.ErrInvalid
		}
		state[id] = 2
		ordered = append(ordered, previous)
		return nil
	}
	if current.Kind == tableshapecap.ResolutionDerived {
		if current.Derived.PivotResolutionID != "" {
			if err := visit(current.Derived.PivotResolutionID); err != nil {
				return nil, err
			}
		}
		for _, operand := range []tableshapecap.ResolvedOperand{current.Derived.Left, current.Derived.Right} {
			if operand.Kind == tableshapecap.ResolvedOperandResolution {
				if err := visit(operand.ResolutionID); err != nil {
					return nil, err
				}
			}
		}
	}
	return ordered, nil
}

type tableShapeCapabilityDocument struct {
	Key             string                             `json:"_key"`
	ArangoID        string                             `json:"_id,omitempty"`
	Revision        string                             `json:"_rev,omitempty"`
	ID              string                             `json:"id"`
	Kind            string                             `json:"kind"`
	ParentCatalogID string                             `json:"parentCatalogId"`
	Binding         tableshapecap.Binding              `json:"binding"`
	Catalog         *tableshapecap.CatalogReceipt      `json:"catalog,omitempty"`
	CategoryScan    *tableshapecap.CategoryScanReceipt `json:"categoryScan,omitempty"`
	Resolution      *tableshapecap.ResolutionReceipt   `json:"resolution,omitempty"`
}

func catalogDocument(receipt tableshapecap.CatalogReceipt) (map[string]any, error) {
	return document(tableShapeCapabilityDocument{Key: receipt.ID, ID: receipt.ID, Kind: "CATALOG", Binding: receipt.Binding, Catalog: &receipt}, receipt.ID)
}
func categoryScanDocument(receipt tableshapecap.CategoryScanReceipt) (map[string]any, error) {
	return document(tableShapeCapabilityDocument{Key: receipt.ID, ID: receipt.ID, Kind: "CATEGORY_SCAN", ParentCatalogID: receipt.ParentCatalogID, Binding: receipt.Binding, CategoryScan: &receipt}, receipt.ID)
}
func resolutionDocument(receipt tableshapecap.ResolutionReceipt) (map[string]any, error) {
	return document(tableShapeCapabilityDocument{Key: receipt.ID, ID: receipt.ID, Kind: "RESOLUTION", ParentCatalogID: receipt.ParentCatalogID, Binding: receipt.Binding, Resolution: &receipt}, receipt.ID)
}
func decodeTableShapeCapabilityDocument(row map[string]interface{}) (tableShapeCapabilityDocument, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return tableShapeCapabilityDocument{}, err
	}
	doc, err := tableshapecap.DecodeStrict[tableShapeCapabilityDocument](raw)
	if err != nil {
		return tableShapeCapabilityDocument{}, err
	}
	if doc.Key != doc.ID || doc.ID == "" {
		return tableShapeCapabilityDocument{}, tableshapecap.ErrInvalid
	}
	switch doc.Kind {
	case "CATALOG":
		if doc.Catalog == nil || doc.CategoryScan != nil || doc.Resolution != nil || doc.ParentCatalogID != "" || doc.Catalog.ID != doc.ID || doc.Catalog.Binding != doc.Binding {
			return tableShapeCapabilityDocument{}, tableshapecap.ErrInvalid
		}
	case "CATEGORY_SCAN":
		if doc.CategoryScan == nil || doc.Catalog != nil || doc.Resolution != nil || doc.CategoryScan.ID != doc.ID || doc.CategoryScan.Binding != doc.Binding || doc.CategoryScan.ParentCatalogID != doc.ParentCatalogID {
			return tableShapeCapabilityDocument{}, tableshapecap.ErrInvalid
		}
	case "RESOLUTION":
		if doc.Resolution == nil || doc.Catalog != nil || doc.CategoryScan != nil || doc.Resolution.ID != doc.ID || doc.Resolution.Binding != doc.Binding || doc.Resolution.ParentCatalogID != doc.ParentCatalogID {
			return tableShapeCapabilityDocument{}, tableshapecap.ErrInvalid
		}
	default:
		return tableShapeCapabilityDocument{}, tableshapecap.ErrInvalid
	}
	return doc, nil
}

func tableShapeCapabilityBinds(binding tableshapecap.Binding, id, kind, parent string) map[string]interface{} {
	return map[string]interface{}{
		"@c": TableShapeCapabilitiesCollection, "id": id, "kind": kind, "parentCatalogId": parent,
		"project": binding.Project, "explorerId": binding.ExplorerID, "outputId": binding.OutputID,
		"snapshotToken": binding.SnapshotToken, "authorizationScope": binding.AuthorizationScope,
		"sourceGeneration": binding.SourceGeneration, "draftVersion": binding.DraftVersion,
		"draftDigest": binding.DraftDigest, "baseDocumentDigest": binding.BaseDocumentDigest,
		"baseCompilationReceiptId": binding.BaseCompilationReceiptID, "outputFingerprint": binding.OutputFingerprint,
		"compilerSchemaDigest": binding.CompilerSchemaDigest,
	}
}

func sameCatalog(existing, want tableshapecap.CatalogReceipt) error {
	left, err := existing.CanonicalContent()
	if err != nil {
		return err
	}
	right, err := want.CanonicalContent()
	if err != nil {
		return err
	}
	if existing.ID != want.ID || existing.ContentDigest != want.ContentDigest || string(left) != string(right) {
		return fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, want.ID)
	}
	return nil
}
func sameResolution(existing, want tableshapecap.ResolutionReceipt) error {
	left, err := existing.CanonicalContent()
	if err != nil {
		return err
	}
	right, err := want.CanonicalContent()
	if err != nil {
		return err
	}
	if existing.ID != want.ID || existing.ContentDigest != want.ContentDigest || string(left) != string(right) {
		return fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, want.ID)
	}
	return nil
}

func sameCategoryScan(existing, want tableshapecap.CategoryScanReceipt) error {
	left, err := existing.CanonicalContent()
	if err != nil {
		return err
	}
	right, err := want.CanonicalContent()
	if err != nil {
		return err
	}
	if existing.ID != want.ID || existing.ContentDigest != want.ContentDigest || string(left) != string(right) {
		return fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, want.ID)
	}
	return nil
}

const tableShapeCapabilityInsertAQL = `
INSERT @doc INTO @@c
  OPTIONS { overwriteMode: "ignore" }
  RETURN NEW
`

const tableShapeCapabilityGetAQL = `
FOR d IN @@c
  FILTER d.id == @id
    AND d.kind == @kind
    AND d.parentCatalogId == @parentCatalogId
    AND d.binding.project == @project
    AND d.binding.explorerId == @explorerId
    AND d.binding.outputId == @outputId
    AND d.binding.snapshotToken == @snapshotToken
    AND d.binding.authorizationScope == @authorizationScope
    AND d.binding.sourceGeneration == @sourceGeneration
    AND d.binding.draftVersion == @draftVersion
    AND d.binding.draftDigest == @draftDigest
    AND d.binding.baseDocumentDigest == @baseDocumentDigest
    AND d.binding.baseCompilationReceiptId == @baseCompilationReceiptId
    AND d.binding.outputFingerprint == @outputFingerprint
    AND d.binding.compilerSchemaDigest == @compilerSchemaDigest
  LIMIT 1
  RETURN d
`

const tableShapeCapabilityGetForLookupAQL = `
FOR d IN @@c
  FILTER d._key == @id
    AND d.id == @id
    AND d.kind == @kind
    AND d.binding.project == @project
    AND d.binding.explorerId == @explorerId
    AND d.binding.outputId == @outputId
    AND d.binding.snapshotToken == @snapshotToken
    AND d.binding.authorizationScope == @authorizationScope
    AND d.binding.sourceGeneration == @sourceGeneration
    AND d.binding.draftVersion == @draftVersion
    AND d.binding.draftDigest == @draftDigest
    AND d.binding.baseDocumentDigest == @baseDocumentDigest
  LIMIT 1
  RETURN d
`

const tableShapeCapabilityFindCatalogForLookupAQL = `
FOR d IN @@c
  FILTER d.kind == @kind
    AND d.binding.project == @project
    AND d.binding.explorerId == @explorerId
    AND d.binding.outputId == @outputId
    AND d.binding.snapshotToken == @snapshotToken
    AND d.binding.authorizationScope == @authorizationScope
    AND d.binding.sourceGeneration == @sourceGeneration
    AND d.binding.draftVersion == @draftVersion
    AND d.binding.draftDigest == @draftDigest
    AND d.binding.baseDocumentDigest == @baseDocumentDigest
  LIMIT 2
  RETURN d
`

var _ tableshapecap.Repository = (*TableShapeCapabilityRepository)(nil)

func tableShapeCapabilityLookupBinds(lookup tableshapecap.CatalogLookup, id string) map[string]interface{} {
	return map[string]interface{}{
		"@c": TableShapeCapabilitiesCollection, "id": id, "kind": "CATALOG",
		"project": lookup.Project, "explorerId": lookup.ExplorerID, "outputId": lookup.OutputID,
		"snapshotToken": lookup.SnapshotToken, "authorizationScope": lookup.AuthorizationScope,
		"sourceGeneration": lookup.SourceGeneration, "draftVersion": lookup.DraftVersion,
		"draftDigest": lookup.DraftDigest, "baseDocumentDigest": lookup.BaseDocumentDigest,
	}
}

func tableShapeCapabilityFindLookupBinds(lookup tableshapecap.CatalogLookup) map[string]interface{} {
	return map[string]interface{}{
		"@c": TableShapeCapabilitiesCollection, "kind": "CATALOG",
		"project": lookup.Project, "explorerId": lookup.ExplorerID, "outputId": lookup.OutputID,
		"snapshotToken": lookup.SnapshotToken, "authorizationScope": lookup.AuthorizationScope,
		"sourceGeneration": lookup.SourceGeneration, "draftVersion": lookup.DraftVersion,
		"draftDigest": lookup.DraftDigest, "baseDocumentDigest": lookup.BaseDocumentDigest,
	}
}
