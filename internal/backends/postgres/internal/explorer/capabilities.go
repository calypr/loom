package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

type capabilityConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ capabilityConn = (*pgxpool.Conn)(nil)

const (
	CapabilitySnapshotsTable    = "capability_snapshots"
	TableShapeCapabilitiesTable = "table_shape_capabilities"
)

const (
	kindCatalog      = "CATALOG"
	kindCategoryScan = "CATEGORY_SCAN"
	kindResolution   = "RESOLUTION"
)

var ErrStoreInconsistency = errors.New("postgres explorer store: capability artifact stored inconsistently")

var snapshotColumns = []string{
	"token", "project", "generation", "authorization_scope_digest", "cache_version",
	"schema_digest", "resource_inventory_digest", "relationship_digest", "field_digest",
	"shape_digest", "protocol_version", "compiler_version", "traversal_policy_version",
	"projection_policy_version", "status", "complete", "truncated", "payload",
}

type CapabilitySnapshotStore struct {
	client   *pgstore.Client
	insert   string
	getToken string
	getIdent string
}

var _ capability.Repository = (*CapabilitySnapshotStore)(nil)

func NewCapabilitySnapshotStore(client *pgstore.Client, schema string) (*CapabilitySnapshotStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: capability snapshot client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	table := pgx.Identifier{schema, CapabilitySnapshotsTable}.Sanitize()
	cols := strings.Join(snapshotColumns, ", ")
	placeholders := make([]string, len(snapshotColumns))
	for i := range snapshotColumns {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	return &CapabilitySnapshotStore{
		client: client,
		insert: "INSERT INTO " + table + " (" + cols + ") VALUES (" + strings.Join(placeholders, ", ") + ") " +
			"ON CONFLICT (token) DO NOTHING RETURNING payload",
		getToken: "SELECT payload FROM " + table + " WHERE token = $1",
		getIdent: "SELECT payload FROM " + table + " WHERE project = $1 AND generation = $2 " +
			"AND authorization_scope_digest = $3 AND cache_version = $4 AND schema_digest = $5 " +
			"AND resource_inventory_digest = $6 AND relationship_digest = $7 AND field_digest = $8 " +
			"AND protocol_version = $9 AND compiler_version = $10 AND traversal_policy_version = $11 " +
			"AND projection_policy_version = $12 AND shape_digest = $13 AND status IN ('READY', 'BUILDING', 'FAILED') " +
			"ORDER BY (status = 'READY') DESC, token LIMIT 1",
	}, nil
}

func capabilitySnapshotDocument(snapshot capability.Snapshot) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(snapshot.CanonicalPayload(), &doc); err != nil {
		return nil, fmt.Errorf("postgres explorer store: build canonical snapshot document: %w", err)
	}
	doc["token"] = snapshot.Token
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode canonical snapshot document: %w", err)
	}
	return raw, nil
}

func snapshotArgs(snapshot capability.Snapshot, document []byte) []any {
	id := snapshot.Identity
	return []any{
		snapshot.Token,
		id.Project, id.Generation, id.AuthorizationScopeDigest, id.CacheVersion,
		id.SchemaDigest, id.ResourceInventoryDigest, id.RelationshipDigest, id.FieldDigest,
		id.ShapeDigest, id.ProtocolVersion, id.CompilerVersion, id.TraversalPolicyVersion,
		id.ProjectionPolicyVersion,
		string(snapshot.Status), snapshot.Complete, snapshot.Truncated,
		string(document),
	}
}

func decodeSnapshot(raw []byte) (capability.Snapshot, error) {
	var value capability.Snapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		return capability.Snapshot{}, fmt.Errorf("postgres explorer store: decode capability snapshot: %w", err)
	}
	if err := capability.ValidateSnapshot(value); err != nil {
		return capability.Snapshot{}, err
	}
	return value, nil
}

func (s *CapabilitySnapshotStore) Put(ctx context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	if err := capability.ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}
	document, err := capabilitySnapshotDocument(snapshot)
	if err != nil {
		return nil, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire snapshot put connection: %w", err)
	}
	defer conn.Release()

	var stored []byte
	err = conn.QueryRow(ctx, s.insert, snapshotArgs(snapshot, document)...).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := s.getByTokenRaw(ctx, conn, snapshot.Token)
		if getErr != nil {
			return nil, getErr
		}
		if existing.Token != snapshot.Token ||
			string(existing.CanonicalPayload()) != string(snapshot.CanonicalPayload()) {
			return nil, fmt.Errorf("%w: %s", capability.ErrTokenCollision, snapshot.Token)
		}
		if err := capability.ValidateSnapshot(existing); err != nil {
			return nil, err
		}
		return &existing, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: put capability snapshot %q: %w", snapshot.Token, err)
	}
	value, err := decodeSnapshot(stored)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: decode just-inserted snapshot %q: %w", snapshot.Token, err)
	}
	return &value, nil
}

func (s *CapabilitySnapshotStore) GetByToken(ctx context.Context, token string) (*capability.Snapshot, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire snapshot get connection: %w", err)
	}
	defer conn.Release()
	return s.getByToken(ctx, conn, token)
}

func (s *CapabilitySnapshotStore) getByToken(ctx context.Context, conn capabilityConn, token string) (*capability.Snapshot, error) {
	value, err := s.getByTokenRaw(ctx, conn, token)
	if err != nil {
		return nil, err
	}
	if err := capability.ValidateSnapshot(value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *CapabilitySnapshotStore) getByTokenRaw(ctx context.Context, conn capabilityConn, token string) (capability.Snapshot, error) {
	var raw []byte
	err := conn.QueryRow(ctx, s.getToken, token).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return capability.Snapshot{}, capability.ErrNotFound
	}
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("postgres explorer store: get capability snapshot %q: %w", token, err)
	}
	var value capability.Snapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		return capability.Snapshot{}, fmt.Errorf("postgres explorer store: decode capability snapshot %q: %w", token, err)
	}
	if value.Token != token {
		return capability.Snapshot{}, fmt.Errorf("%w: snapshot payload token %q is filed under %q", ErrStoreInconsistency, value.Token, token)
	}
	return value, nil
}

func (s *CapabilitySnapshotStore) GetByIdentity(ctx context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire snapshot identity connection: %w", err)
	}
	defer conn.Release()

	var raw []byte
	err = conn.QueryRow(ctx, s.getIdent,
		identity.Project, identity.Generation, identity.AuthorizationScopeDigest, identity.CacheVersion,
		identity.SchemaDigest, identity.ResourceInventoryDigest, identity.RelationshipDigest, identity.FieldDigest,
		identity.ProtocolVersion, identity.CompilerVersion, identity.TraversalPolicyVersion,
		identity.ProjectionPolicyVersion, identity.ShapeDigest).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, capability.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: get capability snapshot by identity: %w", err)
	}
	value, err := decodeSnapshot(raw)
	if err != nil {
		return nil, err
	}
	if value.Identity != identity {
		return nil, fmt.Errorf("%w: snapshot %q carries an identity that disagrees with its mirror columns",
			ErrStoreInconsistency, value.Token)
	}
	return &value, nil
}

var receiptColumns = []string{
	"id", "kind", "parent_catalog_id", "project", "explorer_id", "output_id", "snapshot_token",
	"authorization_scope", "source_generation", "draft_version", "draft_digest",
	"base_document_digest", "base_compilation_receipt_id", "output_fingerprint",
	"compiler_schema_digest", "content_digest", "created_at", "payload",
}

type TableShapeCapabilityStore struct {
	client     *pgstore.Client
	insert     string
	catalog    string
	scanRow    string
	resolution string
	lookup     string
}

var _ tableshapecap.Repository = (*TableShapeCapabilityStore)(nil)

func NewTableShapeCapabilityStore(client *pgstore.Client, schema string) (*TableShapeCapabilityStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: table-shape capability client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	table := pgx.Identifier{schema, TableShapeCapabilitiesTable}.Sanitize()
	cols := strings.Join(receiptColumns, ", ")
	placeholders := make([]string, len(receiptColumns))
	for i := range receiptColumns {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	bindingFilter := "WHERE id = $1 AND kind = $2" + bindingColumnFilterFrom(3)
	lookupFilter := "WHERE id = $1 AND kind = 'CATALOG'" + lookupColumnFilterFrom(2)
	return &TableShapeCapabilityStore{
		client:     client,
		insert:     "INSERT INTO " + table + " (" + cols + ") VALUES (" + strings.Join(placeholders, ", ") + ") ON CONFLICT (id) DO NOTHING RETURNING payload",
		catalog:    "SELECT " + cols + " FROM " + table + " " + bindingFilter,
		scanRow:    "SELECT " + cols + " FROM " + table + " WHERE id = $1 AND kind = $2 AND parent_catalog_id = $3" + bindingColumnFilterFrom(4),
		resolution: "SELECT " + cols + " FROM " + table + " WHERE id = $1 AND kind = $2 AND parent_catalog_id = $3" + bindingColumnFilterFrom(4),
		lookup:     "SELECT " + cols + " FROM " + table + " " + lookupFilter,
	}, nil
}

func bindingColumnFilterFrom(start int) string {
	columns := []string{
		"project", "explorer_id", "output_id", "snapshot_token", "authorization_scope",
		"source_generation", "draft_version", "draft_digest", "base_document_digest",
		"base_compilation_receipt_id", "output_fingerprint", "compiler_schema_digest",
	}
	parts := make([]string, 0, len(columns))
	for i, column := range columns {
		parts = append(parts, fmt.Sprintf("AND %s = $%d", column, start+i))
	}
	return " " + strings.Join(parts, " ")
}

func lookupColumnFilterFrom(start int) string {
	columns := []string{
		"project", "explorer_id", "output_id", "snapshot_token", "authorization_scope",
		"source_generation", "draft_version", "draft_digest", "base_document_digest",
	}
	parts := make([]string, 0, len(columns))
	for i, column := range columns {
		parts = append(parts, fmt.Sprintf("AND %s = $%d", column, start+i))
	}
	return " " + strings.Join(parts, " ")
}

func bindingArgs(binding tableshapecap.Binding) []any {
	return []any{
		binding.Project, binding.ExplorerID, binding.OutputID, binding.SnapshotToken,
		binding.AuthorizationScope, binding.SourceGeneration, int64(binding.DraftVersion),
		binding.DraftDigest, binding.BaseDocumentDigest, binding.BaseCompilationReceiptID,
		binding.OutputFingerprint, binding.CompilerSchemaDigest,
	}
}

func receiptArgs(id, kind, parentCatalogID string, binding tableshapecap.Binding, contentDigest, createdAt string, payload []byte) []any {
	args := make([]any, 0, len(receiptColumns))
	args = append(args, id, kind, parentCatalogID)
	args = append(args, bindingArgs(binding)...)
	args = append(args, contentDigest, createdAt, string(payload))
	return args
}

type receiptRow struct {
	id, kind, parentCatalogID string
	binding                   tableshapecap.Binding
	contentDigest, createdAt  string
	payload                   []byte
}

func scanReceiptRow(row rowScanner) (receiptRow, error) {
	var (
		stored   receiptRow
		draftVer int64
	)
	targets := []any{
		&stored.id, &stored.kind, &stored.parentCatalogID,
		&stored.binding.Project, &stored.binding.ExplorerID, &stored.binding.OutputID,
		&stored.binding.SnapshotToken, &stored.binding.AuthorizationScope, &stored.binding.SourceGeneration,
		&draftVer, &stored.binding.DraftDigest, &stored.binding.BaseDocumentDigest,
		&stored.binding.BaseCompilationReceiptID, &stored.binding.OutputFingerprint,
		&stored.binding.CompilerSchemaDigest,
		&stored.contentDigest, &stored.createdAt, &stored.payload,
	}
	if err := row.Scan(targets...); err != nil {
		return receiptRow{}, err
	}
	stored.binding.DraftVersion = uint64(draftVer)
	return stored, nil
}

func (s *TableShapeCapabilityStore) readCatalogRow(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, id string) (receiptRow, tableshapecap.CatalogReceipt, error) {
	args := append([]any{id, kindCatalog}, bindingArgs(binding)...)
	stored, err := scanReceiptRow(conn.QueryRow(ctx, s.catalog, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return receiptRow{}, tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	if err != nil {
		return receiptRow{}, tableshapecap.CatalogReceipt{}, fmt.Errorf("postgres explorer store: read catalog receipt %q: %w", id, err)
	}
	receipt, err := tableshapecap.DecodeStrict[tableshapecap.CatalogReceipt](stored.payload)
	if err != nil {
		return stored, tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: catalog %s payload failed validation: %v", ErrStoreInconsistency, id, err)
	}
	if stored.id != id || stored.kind != kindCatalog || stored.parentCatalogID != "" ||
		stored.binding != binding || receipt.ID != id || receipt.Binding != binding ||
		stored.contentDigest != receipt.ContentDigest || stored.createdAt != receipt.CreatedAt {
		return stored, tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: catalog %s mirror columns disagree with its payload", ErrStoreInconsistency, id)
	}
	return stored, receipt, nil
}

func (s *TableShapeCapabilityStore) GetCatalog(ctx context.Context, binding tableshapecap.Binding, id string) (tableshapecap.CatalogReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("postgres explorer store: acquire catalog get connection: %w", err)
	}
	defer conn.Release()
	_, receipt, err := s.readCatalogRow(ctx, conn, binding, id)
	return receipt, err
}

func (s *TableShapeCapabilityStore) GetCatalogForLookup(ctx context.Context, lookup tableshapecap.CatalogLookup, id string) (tableshapecap.CatalogReceipt, error) {
	if err := lookup.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("postgres explorer store: acquire catalog lookup connection: %w", err)
	}
	defer conn.Release()

	args := append([]any{id}, lookupArgs(lookup)...)
	stored, err := scanReceiptRow(conn.QueryRow(ctx, s.lookup, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	if err != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("postgres explorer store: lookup catalog receipt %q: %w", id, err)
	}
	receipt, err := tableshapecap.DecodeStrict[tableshapecap.CatalogReceipt](stored.payload)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: catalog %s payload failed validation: %v", ErrStoreInconsistency, id, err)
	}
	if stored.id != id || stored.kind != kindCatalog || stored.parentCatalogID != "" ||
		!lookup.Matches(stored.binding) || stored.binding != receipt.Binding ||
		receipt.ID != id || !lookup.Matches(receipt.Binding) ||
		stored.contentDigest != receipt.ContentDigest || stored.createdAt != receipt.CreatedAt {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: catalog %s mirror columns disagree with its payload", ErrStoreInconsistency, id)
	}
	return receipt, nil
}

func lookupArgs(lookup tableshapecap.CatalogLookup) []any {
	return []any{
		lookup.Project, lookup.ExplorerID, lookup.OutputID, lookup.SnapshotToken,
		lookup.AuthorizationScope, lookup.SourceGeneration, int64(lookup.DraftVersion),
		lookup.DraftDigest, lookup.BaseDocumentDigest,
	}
}

func (s *TableShapeCapabilityStore) PutCatalog(ctx context.Context, receipt tableshapecap.CatalogReceipt) (tableshapecap.CatalogReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, fmt.Errorf("postgres explorer store: acquire catalog put connection: %w", err)
	}
	defer conn.Release()

	storedPayload, err := s.insertReceipt(ctx, conn, receipt.ID, kindCatalog, "", receipt.Binding,
		receipt.ContentDigest, receipt.CreatedAt, receipt)
	if err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	if len(storedPayload) > 0 {
		return tableshapecap.DecodeStrict[tableshapecap.CatalogReceipt](storedPayload)
	}
	_, existing, getErr := s.readCatalogRow(ctx, conn, receipt.Binding, receipt.ID)
	if getErr != nil {
		if errors.Is(getErr, tableshapecap.ErrNotFound) || errors.Is(getErr, ErrStoreInconsistency) {
			return tableshapecap.CatalogReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
		}
		return tableshapecap.CatalogReceipt{}, getErr
	}
	if err := sameCatalog(existing, receipt); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	return existing, nil
}

func (s *TableShapeCapabilityStore) insertReceipt(ctx context.Context, conn capabilityConn, id, kind, parentCatalogID string, binding tableshapecap.Binding, contentDigest, createdAt string, receipt any) ([]byte, error) {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: marshal %s receipt %q: %w", kind, id, err)
	}
	var stored []byte
	err = conn.QueryRow(ctx, s.insert, receiptArgs(id, kind, parentCatalogID, binding, contentDigest, createdAt, payload)...).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: insert %s receipt %q: %w", kind, id, err)
	}
	return stored, nil
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

func (s *TableShapeCapabilityStore) readScanRow(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID, id string) (receiptRow, tableshapecap.CategoryScanReceipt, error) {
	args := append([]any{id, kindCategoryScan, parentCatalogID}, bindingArgs(binding)...)
	stored, err := scanReceiptRow(conn.QueryRow(ctx, s.scanRow, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return receiptRow{}, tableshapecap.CategoryScanReceipt{}, tableshapecap.ErrNotFound
	}
	if err != nil {
		return receiptRow{}, tableshapecap.CategoryScanReceipt{}, fmt.Errorf("postgres explorer store: read category-scan receipt %q: %w", id, err)
	}
	receipt, err := tableshapecap.DecodeStrict[tableshapecap.CategoryScanReceipt](stored.payload)
	if err != nil {
		return stored, tableshapecap.CategoryScanReceipt{}, fmt.Errorf("%w: category scan %s payload failed validation: %v", ErrStoreInconsistency, id, err)
	}
	if stored.id != id || stored.kind != kindCategoryScan || stored.parentCatalogID != parentCatalogID ||
		stored.binding != binding || receipt.ID != id || receipt.Binding != binding ||
		receipt.ParentCatalogID != parentCatalogID ||
		stored.contentDigest != receipt.ContentDigest || stored.createdAt != receipt.CreatedAt {
		return stored, tableshapecap.CategoryScanReceipt{}, fmt.Errorf("%w: category scan %s mirror columns disagree with its payload", ErrStoreInconsistency, id)
	}
	return stored, receipt, nil
}

func (s *TableShapeCapabilityStore) GetCategoryScan(ctx context.Context, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.CategoryScanReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, fmt.Errorf("postgres explorer store: acquire scan get connection: %w", err)
	}
	defer conn.Release()
	found, err := s.getCategoryScan(ctx, conn, binding, parentCatalogID, id)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	_, parent, err := s.readCatalogRow(ctx, conn, binding, parentCatalogID)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if err := tableshapecap.ValidateCategoryScanAgainstCatalog(parent, found); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	return found, nil
}

func (s *TableShapeCapabilityStore) getCategoryScan(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.CategoryScanReceipt, error) {
	_, receipt, err := s.readScanRow(ctx, conn, binding, parentCatalogID, id)
	return receipt, err
}

func (s *TableShapeCapabilityStore) PutCategoryScan(ctx context.Context, receipt tableshapecap.CategoryScanReceipt) (tableshapecap.CategoryScanReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, fmt.Errorf("postgres explorer store: acquire scan put connection: %w", err)
	}
	defer conn.Release()

	_, parent, err := s.readCatalogRow(ctx, conn, receipt.Binding, receipt.ParentCatalogID)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if err := tableshapecap.ValidateCategoryScanAgainstCatalog(parent, receipt); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	storedPayload, err := s.insertReceipt(ctx, conn, receipt.ID, kindCategoryScan, receipt.ParentCatalogID,
		receipt.Binding, receipt.ContentDigest, receipt.CreatedAt, receipt)
	if err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	if len(storedPayload) > 0 {
		return tableshapecap.DecodeStrict[tableshapecap.CategoryScanReceipt](storedPayload)
	}
	existing, getErr := s.getCategoryScan(ctx, conn, receipt.Binding, receipt.ParentCatalogID, receipt.ID)
	if getErr != nil {
		if errors.Is(getErr, tableshapecap.ErrNotFound) || errors.Is(getErr, ErrStoreInconsistency) {
			return tableshapecap.CategoryScanReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
		}
		return tableshapecap.CategoryScanReceipt{}, getErr
	}
	if err := sameCategoryScan(existing, receipt); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	return existing, nil
}

func (s *TableShapeCapabilityStore) readResolutionRow(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID, id string) (receiptRow, tableshapecap.ResolutionReceipt, error) {
	args := append([]any{id, kindResolution, parentCatalogID}, bindingArgs(binding)...)
	stored, err := scanReceiptRow(conn.QueryRow(ctx, s.resolution, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return receiptRow{}, tableshapecap.ResolutionReceipt{}, tableshapecap.ErrNotFound
	}
	if err != nil {
		return receiptRow{}, tableshapecap.ResolutionReceipt{}, fmt.Errorf("postgres explorer store: read resolution receipt %q: %w", id, err)
	}
	receipt, err := tableshapecap.DecodeStrict[tableshapecap.ResolutionReceipt](stored.payload)
	if err != nil {
		return stored, tableshapecap.ResolutionReceipt{}, fmt.Errorf("%w: resolution %s payload failed validation: %v", ErrStoreInconsistency, id, err)
	}
	if stored.id != id || stored.kind != kindResolution || stored.parentCatalogID != parentCatalogID ||
		stored.binding != binding || receipt.ID != id || receipt.Binding != binding ||
		receipt.ParentCatalogID != parentCatalogID ||
		stored.contentDigest != receipt.ContentDigest || stored.createdAt != receipt.CreatedAt {
		return stored, tableshapecap.ResolutionReceipt{}, fmt.Errorf("%w: resolution %s mirror columns disagree with its payload", ErrStoreInconsistency, id)
	}
	return stored, receipt, nil
}

func (s *TableShapeCapabilityStore) GetResolution(ctx context.Context, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	if err := binding.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, fmt.Errorf("postgres explorer store: acquire resolution get connection: %w", err)
	}
	defer conn.Release()
	found, err := s.getResolution(ctx, conn, binding, parentCatalogID, id)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if err := s.validateResolutionGraph(ctx, conn, binding, parentCatalogID, found); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	return found, nil
}

func (s *TableShapeCapabilityStore) getResolution(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	_, receipt, err := s.readResolutionRow(ctx, conn, binding, parentCatalogID, id)
	return receipt, err
}

func (s *TableShapeCapabilityStore) validateResolutionGraph(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID string, resolution tableshapecap.ResolutionReceipt) error {
	_, parent, err := s.readCatalogRow(ctx, conn, binding, parentCatalogID)
	if err != nil {
		return err
	}
	prior, err := s.resolutionDependencies(ctx, conn, binding, parentCatalogID, resolution)
	if err != nil {
		return err
	}
	if err := tableshapecap.ValidateResolutionAgainstCatalog(parent, resolution, prior...); err != nil {
		return err
	}
	if resolution.Pivot != nil {
		scan, err := s.getCategoryScan(ctx, conn, binding, parentCatalogID, resolution.Pivot.CategoryDiscoveryID)
		if err != nil {
			return err
		}
		if err := tableshapecap.ValidateResolutionAgainstCategoryScan(parent, scan, resolution); err != nil {
			return err
		}
	}
	return nil
}

func (s *TableShapeCapabilityStore) resolutionDependencies(ctx context.Context, conn capabilityConn, binding tableshapecap.Binding, parentCatalogID string, current tableshapecap.ResolutionReceipt) ([]tableshapecap.ResolutionReceipt, error) {
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
		previous, err := s.getResolution(ctx, conn, binding, parentCatalogID, id)
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

func (s *TableShapeCapabilityStore) PutResolution(ctx context.Context, receipt tableshapecap.ResolutionReceipt) (tableshapecap.ResolutionReceipt, error) {
	if err := receipt.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, fmt.Errorf("postgres explorer store: acquire resolution put connection: %w", err)
	}
	defer conn.Release()

	if err := s.validateResolutionGraph(ctx, conn, receipt.Binding, receipt.ParentCatalogID, receipt); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	storedPayload, err := s.insertReceipt(ctx, conn, receipt.ID, kindResolution, receipt.ParentCatalogID,
		receipt.Binding, receipt.ContentDigest, receipt.CreatedAt, receipt)
	if err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	if len(storedPayload) > 0 {
		return tableshapecap.DecodeStrict[tableshapecap.ResolutionReceipt](storedPayload)
	}
	existing, getErr := s.getResolution(ctx, conn, receipt.Binding, receipt.ParentCatalogID, receipt.ID)
	if getErr != nil {
		if errors.Is(getErr, tableshapecap.ErrNotFound) || errors.Is(getErr, ErrStoreInconsistency) {
			return tableshapecap.ResolutionReceipt{}, fmt.Errorf("%w: %s", tableshapecap.ErrIdentityClash, receipt.ID)
		}
		return tableshapecap.ResolutionReceipt{}, getErr
	}
	if err := sameResolution(existing, receipt); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	return existing, nil
}
