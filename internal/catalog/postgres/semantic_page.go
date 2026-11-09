// Bounded semantic-inventory page reads and selection-resolution reads for
// the PostgreSQL source store (pg-catalog-06).
//
// SemanticReadRepository serves the two public read surfaces that
// internal/catalog/arango/semantic_inventory.go PageSemanticInventory and
// ResolveSemanticInventorySelections serve from Arango, against the one
// contribution table pg-catalog-05 persists
// (fhir_semantic_inventory_contribution, migration
// 000009_semantic_inventory_contribution) and the one build row pg-catalog-04
// persists (fhir_semantic_inventory_build, migration
// 000006_semantic_inventory_build). It writes nothing. This is the read side
// that completes the PG catalog slice after build/contribution storage; the
// server wiring that swaps Arango for PostgreSQL is a runtime ticket, and the
// consumer contract is pinned structurally here to the exact method shapes
// internal/server/server.go binds (SemanticInventory /
// ResolveSemanticInventorySelections).
//
// Guarantees, all matched to the Arango behavioral reference:
//
//   - Same ordered page and next-cursor semantics for exact and
//     SYSTEM_VERSION_CATEGORY (family) modes. Identities page in
//     (binding_id, selection_id) byte order — concept_id in exact mode;
//     "family:"+compact-JSON [source.type, source.path, key.system,
//     NOT_NULL(key.version,"")] for CODED_CONCEPT_V1 category rows, concept_id
//     for the rest — because that is the key the reference COLLECT/SORT
//     orders and the value the shared opaque cursor carries. Every keyset
//     predicate and every sort is pinned to COLLATE "C" so PostgreSQL
//     compares exact bytes like Go and like ArangoDB's memcmp string order;
//     the server's default collation is never trusted (the test service is
//     en_US.utf8 and its locale lower() folds non-ASCII, so the query
//     filter's lower() is likewise pinned to COLLATE "C" to keep the
//     reference's ASCII folding). Pages fetch limit+1 identities and hand
//     back catalog.EncodeSemanticInventoryCursor over the last included
//     identity only when a further identity exists, exactly the reference's
//     count>limit rule.
//   - The cursor is the existing shared opaque cursor
//     (catalog.DecodeSemanticInventoryCursor): replaying it under a changed
//     project/generation/filter/auth-scope/limit/mode request returns the
//     existing typed catalog.ErrSemanticInventoryCursorMismatch, and it is
//     decoded only after the build is known complete, matching the reference
//     guard order (a stale cursor against an incomplete build returns the
//     state page, not the mismatch).
//   - Explicit restricted-empty results. A restricted page (effective auth
//     scope not unrestricted) whose paths match nothing returns an empty,
//     non-nil Entries slice with the completed build present and its
//     internals (scanned count, checkpoints, lease, diagnostics, auth path)
//     scrubbed; an unknown/missing build or a missing contribution table
//     reads as the explicit unknown-state empty page, never an error and
//     never a silent fallback. The legacy convention that a nil
//     AuthResourcePathsUnrestricted with an empty path list means
//     unrestricted is preserved for the page
//     (catalog.EffectiveAuthResourcePathsUnrestricted), while resolve
//     requires an explicit authorization decision, exactly as the Arango
//     adapter does.
//   - Resolution returns only requested identities under the caller's
//     current authorization scope, in request order, dropping unmatched
//     references. Exact references resolve to the one authorized row with
//     the least contribution_key (the content-addressed identity, which is
//     the Arango _key the reference sorts by and limits to one), returning
//     its stored observation verbatim; versioned/unversioned system-version
//     families aggregate only rows of their exact (binding, resource type,
//     source type, source path, system, version-with-empty-means-versionless)
//     tuple, so a versioned family can never absorb versionless codes, a
//     versionless family can never absorb versioned ones, and no record
//     outside the caller's project/generation/build/auth scope can appear.
//   - Every query is bounded: the page fetches at most limit+1 identities
//     (limit clamped to [1, catalog.SemanticInventoryPageLimit]) and one
//     aggregate row per identity; resolve names at most
//     maxSemanticInventoryResolveSelections references in one statement.
//     They run on the target-contract indexes migration 000009 already
//     carries — the scope index
//     (project, dataset_generation, build_id, source_kind, binding_id,
//     concept_id, auth_resource_path), whose migration comment earmarks it
//     "for later bounded catalog page reads", and the build primary key /
//     build_key unique index. No new schema is added by this slice; EXPLAIN
//     in the tests is recorded as plan-shape evidence only, never a speed
//     claim.
//
// Deliberate, named divergences (never silent):
//
//   - Entry materialization. PostgreSQL persists no
//     fhir_semantic_inventory_entries analogue: pg-catalog-05 refuses
//     EnsureSemanticInventoryEntries by name. This adapter therefore reads
//     what this store actually persists — contributions — and aggregates
//     each identity inside the page, the way the Arango adapter aggregates
//     when it pages the contribution collection (entry index version below
//     catalog.SemanticInventoryEntryIndexVersion). Because the Arango
//     stale-index gate ErrSemanticInventoryCategoryIndexUnavailable guards a
//     materialized index this store never builds, family mode is served on
//     PostgreSQL at every version this store can produce: the page always
//     reflects the current contribution rows, which is the stronger
//     guarantee that gate asks for. A build row claiming a *materialized*
//     index (entry_index_version >= catalog.SemanticInventoryEntryIndex-
//     Version — a state only a foreign writer could plant here) is refused
//     by name with the shared ErrSemanticInventoryEntriesUnsupported error
//     for page and resolve alike, instead of silently pretending an
//     unverifiable entries table does not exist.
//   - Observation leaf reading. The only writer of contribution rows is
//     catalog's Go struct encoder, so a leaf inside the observation JSONB is
//     either absent (omitempty) or a present typed value; explicit JSON null
//     is not producible there. Consistently across search text, MIN/MAX
//     inputs, equality predicates, and outputs, this reader treats an absent
//     leaf and a JSON-null leaf identically as "not present": it never
//     matches an equality predicate, never participates in MIN/MAX/SUM, and
//     reads as the typed empty value. "missing versus JSON-null versus
//     SQL-NULL" preservation remains a payload-storage guarantee
//     (pg-catalog-05 keeps SQL NULL distinct from the bytes "null" in
//     fhir_resource.payload); the page/resolve outputs decode into the typed
//     catalog structs, where both spellings collapse to the zero value on
//     both backends. The tests pin that reading with seeded JSON-null rows.
//   - The MIN/MAX string aggregations are pinned to COLLATE "C" so they
//     match Go/ArangoDB byte order rather than the database collation,
//     mirroring the retained-keyset rule pg-catalog-05 established.
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// maxSemanticInventoryResolveSelections bounds one resolution request's
	// reference count (exact + family together), matching the Arango
	// adapter's 1..100 window.
	maxSemanticInventoryResolveSelections = 100
	// semanticCategoryRuleHint is the observation rule hint whose rows group
	// into system-version families, matching
	// catalog.SemanticRuleHintCodedConceptV1 as the reference AQL compares
	// it.
	semanticCategoryRuleHint = "CODED_CONCEPT_V1"
)

// Sentinel errors for the public read surfaces. Request shapes are refused
// before any statement runs, so a refusal never half-reads.
var (
	// ErrInvalidSemanticInventoryPage reports a page request this adapter
	// cannot honor: blank project, blank dataset generation after
	// normalization, or an unknown selection mode.
	ErrInvalidSemanticInventoryPage = errors.New("postgres: invalid semantic inventory page request")
	// ErrInvalidSemanticInventorySelection reports a resolution request this
	// adapter cannot honor: blank scope, a missing explicit authorization
	// decision, an empty or oversized reference set, a blank reference
	// identity, a duplicate reference, or a family reference whose FamilyID
	// does not re-derive from its selector fields.
	ErrInvalidSemanticInventorySelection = errors.New("postgres: invalid semantic inventory selection request")
)

// SemanticReadRepository serves bounded semantic-inventory pages and
// selection-resolution reads from one schema of the PostgreSQL source store.
// Create one with NewSemanticReadRepository. The tables (created by
// MigrateSchema) must exist before use; a missing build or contribution
// table reads as the explicit unknown-state empty result the Arango adapter
// returns for a missing collection, and this adapter never creates tables.
// The caller owns the connection source and the schema lifecycle.
type SemanticReadRepository struct {
	src             storepostgres.ConnSource
	schema          string
	contributionFQN string
	buildFQN        string
}

// NewSemanticReadRepository validates the connection source and schema name
// and binds the repository to the contribution and build tables inside
// <schema>. The connection source must be live only when a method runs, not
// here.
func NewSemanticReadRepository(src storepostgres.ConnSource, schema string) (*SemanticReadRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil semantic read connection source")
	}
	if !semanticBuildSchemaPattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", storepostgres.ErrInvalidSchemaName, schema)
	}
	return &SemanticReadRepository{
		src:             src,
		schema:          schema,
		contributionFQN: pgx.Identifier{schema, semanticInventoryContributionTableName}.Sanitize(),
		buildFQN:        pgx.Identifier{schema, semanticInventoryBuildTableName}.Sanitize(),
	}, nil
}

// Structural parity pins: internal/server/server.go binds exactly these two
// method shapes out of the catalog store (catalogStore.PageSemanticInventory
// into the SemanticInventory field, ResolveSemanticInventorySelections into
// the lifecycle config of the same name), so a signature drift fails
// compilation here, not at runtime wiring.
var (
	_ func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error)             = (*SemanticReadRepository)(nil).PageSemanticInventory
	_ func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) = (*SemanticReadRepository)(nil).ResolveSemanticInventorySelections
)

// PageSemanticInventory returns one bounded page of the semantic inventory
// for the caller's exact project/generation/authorization scope, with the
// same identity ordering, keyset cursor, and state distinctions the Arango
// adapter's PageSemanticInventory produces. See the file comment for the
// full guarantee list.
func (r *SemanticReadRepository) PageSemanticInventory(ctx context.Context, opts catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
	if strings.TrimSpace(opts.Project) == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" ||
		(opts.SelectionMode != "" && opts.SelectionMode != catalog.SemanticInventoryExactConcept && opts.SelectionMode != catalog.SemanticInventorySystemVersionCategory) {
		return catalog.SemanticInventoryPage{}, ErrInvalidSemanticInventoryPage
	}
	paths := append([]string(nil), opts.AuthResourcePaths...)
	sort.Strings(paths)
	paths = compactSemanticAuthPaths(paths)
	unrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(paths, opts.AuthResourcePathsUnrestricted)
	limit := semanticInventoryPageLimit(opts.Limit)

	buildsExist, err := r.tableExists(ctx, r.buildFQN)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	if !buildsExist {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	// One bounded statement selects the current build identity for the
	// scope. The Arango reference lists the scope's builds and takes the
	// first whose build_id equals the derived identity; the build primary
	// key makes that selection provably the single row this query returns.
	buildID := catalog.SemanticInventoryBuildID(opts.Project, opts.DatasetGeneration)
	build, found, err := r.readBuildByID(ctx, opts.Project, catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), buildID)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	if !found {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	normalizeReadBuild(&build, opts.Project, catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), buildID)

	page := catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, Build: build, State: build.State}
	if !unrestricted {
		// A restricted caller never sees backfill internals.
		page.Build.ScannedResources = 0
		page.Build.Checkpoint = ""
		page.Build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{}
		page.Build.LeaseToken = ""
		page.Build.LeaseExpiresAt = 0
		page.Build.Diagnostics = nil
		page.Build.AuthResourcePath = ""
	}
	if build.State != catalog.SemanticInventoryComplete || build.BuildID == "" {
		return page, nil
	}
	if build.EntryIndexVersion >= catalog.SemanticInventoryEntryIndexVersion {
		// Named refusal, never a silent fallback: the stored build claims a
		// materialized entry index this PostgreSQL slice does not persist
		// and therefore cannot serve or verify.
		return catalog.SemanticInventoryPage{}, &SemanticInventoryEntriesUnsupportedError{
			Project:           build.Project,
			DatasetGeneration: build.DatasetGeneration,
			BuildID:           build.BuildID,
			EntryIndexVersion: build.EntryIndexVersion,
		}
	}
	contributionsExist, err := r.tableExists(ctx, r.contributionFQN)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	if !contributionsExist {
		return catalog.SemanticInventoryPage{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}, nil
	}
	afterBindingID, afterSelectionID, err := catalog.DecodeSemanticInventoryCursor(opts.Cursor, opts, build.BuildID)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}
	sourceKind := build.SourceKind
	if sourceKind == "" {
		sourceKind = catalog.SemanticInventorySourceFile
	}

	sql, args := semanticPageQuery(r.contributionFQN, semanticPageParams{
		project:        opts.Project,
		generation:     catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		buildID:        build.BuildID,
		sourceKind:     sourceKind,
		unrestricted:   unrestricted,
		paths:          paths,
		resourceType:   opts.ResourceType,
		bindingID:      opts.BindingID,
		query:          opts.Query,
		afterBindingID: afterBindingID,
		afterSelection: afterSelectionID,
		limit:          limit,
		familyMode:     opts.SelectionMode == catalog.SemanticInventorySystemVersionCategory,
	})
	rows, err := r.queryPageRows(ctx, sql, args...)
	if err != nil {
		return catalog.SemanticInventoryPage{}, err
	}

	// The statement returns up to limit+1 identities; only the first
	// limit are emitted, and the cursor names the last emitted identity —
	// the reference's count>limit rule, including the rule that the extra
	// identity never advances the cursor.
	count := 0
	var lastBinding, lastSelection string
	for i := range rows {
		count++
		if count > limit {
			break
		}
		row := &rows[i]
		lastBinding, lastSelection = row.bindingID, row.selectionID
		if row.isCategory {
			family := row.toFamily(sourceKind, build.ObservationSchema)
			family.FamilyID = catalog.SemanticInventoryFamilyID(family.SemanticInventoryFamilyReference)
			page.Families = append(page.Families, family)
			continue
		}
		page.Entries = append(page.Entries, row.toEntry(build.ObservationSchema))
	}
	if count > limit {
		page.NextCursor = catalog.EncodeSemanticInventoryCursor(opts, build.BuildID, lastBinding, lastSelection)
	}
	return page, nil
}

// semanticPageParams carries every value one page statement binds. Page
// statements number their parameters deterministically: scope is $1..$6,
// optional filters $7..$9, the cursor pair $10..$11, and the row budget
// $12. Every value is bound; only code-owned structure is interpolated.
type semanticPageParams struct {
	project        string
	generation     string
	buildID        string
	sourceKind     string
	unrestricted   bool
	paths          []string
	resourceType   string
	bindingID      string
	query          string
	afterBindingID string
	afterSelection string
	limit          int
	familyMode     bool
}

// semanticPageQuery builds the one bounded page statement for either mode.
//
// candidates applies the scope, optional filters, and the query filter;
// identities dedups to the paged key (binding_id, concept_id) in exact mode
// and (binding_id, selection_id, is_category) in family mode, applies the
// after-key filter (a predicate on the group keys, so applying it before the
// GROUP BY is equivalent to the reference's post-COLLECT FILTER), and
// budgets limit+1 rows in byte order; the LATERAL aggregate then folds every
// contributing row of each identity. The query filter selects which
// identities appear but never narrows an identity's aggregate, matching the
// reference's two-stage queries. Category identities re-apply the category
// predicate and the exact scope tuple (via the selection string); plain
// identities re-filter by concept_id alone, including any category-flagged
// rows that share the concept — the reference quirk, preserved.
func semanticPageQuery(contributionFQN string, p semanticPageParams) (string, []any) {
	args := []any{
		p.project, p.generation, p.buildID, p.sourceKind, p.unrestricted, p.paths,
		p.resourceType, p.bindingID, p.query,
		p.afterBindingID, p.afterSelection,
		p.limit + 1,
	}

	var sb strings.Builder
	sb.WriteString("WITH candidates AS (SELECT c.binding_id")
	lateralMatch := " AND c.concept_id = i.concept_id"
	if p.familyMode {
		sb.WriteString(", c.concept_id" +
			", " + semanticCategoryPredicate("c") + " AS is_category" +
			", " + semanticFamilySelectionExpr("c") + " AS selection_id")
		// Category identities re-apply the category predicate and the exact
		// scope tuple (via the selection string); plain identities re-filter
		// by concept_id alone, including any category-flagged rows that
		// share the concept — the reference quirk, preserved.
		lateralMatch = " AND ((" + semanticCategoryPredicate("c") + " AND " +
			semanticFamilySelectionExpr("c") + " = i.selection_id)" +
			" OR (NOT i.is_category AND c.concept_id = i.concept_id))"
	} else {
		sb.WriteString(", c.concept_id, false AS is_category, c.concept_id AS selection_id")
	}
	sb.WriteString(" FROM " + contributionFQN + " c WHERE " + semanticScopeClause("c") +
		" AND ($7 = '' OR c.resource_type = $7)" +
		" AND ($8 = '' OR c.binding_id = $8)" +
		" AND ($9 = '' OR " + semanticSearchContains("c", "$9") + "))," +
		" identities AS (SELECT binding_id, max(concept_id) AS concept_id, selection_id, is_category FROM candidates WHERE " +
		semanticKeysetPredicate("candidates.binding_id", "candidates.selection_id") +
		" GROUP BY binding_id, selection_id, is_category" +
		" ORDER BY binding_id COLLATE \"C\", selection_id COLLATE \"C\", is_category" +
		" LIMIT $12)" +
		" SELECT i.binding_id, i.concept_id, i.selection_id, i.is_category, a.* FROM identities i" +
		" CROSS JOIN LATERAL (SELECT " + semanticEntryAggregateSelects("c") +
		" FROM " + contributionFQN + " c WHERE " + semanticScopeClause("c") +
		" AND c.binding_id = i.binding_id" + lateralMatch +
		") a ORDER BY i.binding_id COLLATE \"C\", i.selection_id COLLATE \"C\", i.is_category")
	return sb.String(), args
}

// semanticResolveReferencesQuery builds the one bounded exact-reference
// resolution statement: a VALUES table of the requested identities (with
// their request ordinals) CROSS JOIN LATERAL to the single least
// contribution_key row per request. $1..$6 carry the authorization scope.
func semanticResolveReferencesQuery(contributionFQN string, scope resolveScope, references []catalog.SemanticInventoryReference) (string, []any) {
	args := scope.args()
	var values strings.Builder
	values.WriteString("(VALUES ")
	for i, reference := range references {
		if i > 0 {
			values.WriteString(", ")
		}
		bindingParam := "$" + strconv.Itoa(len(args)+1)
		args = append(args, reference.BindingID)
		conceptParam := "$" + strconv.Itoa(len(args)+1)
		args = append(args, reference.ConceptID)
		values.WriteString("(" + bindingParam + "::text, " + conceptParam + "::text, " + strconv.Itoa(i) + ")")
	}
	values.WriteString(") AS requested(binding_id, concept_id, ord)")

	sql := "SELECT requested.ord, c.binding_id, c.concept_id, c.observation FROM " + values.String() +
		" CROSS JOIN LATERAL (SELECT c.observation, c.binding_id, c.concept_id FROM " + contributionFQN + " c WHERE " +
		semanticScopeClause("c") +
		" AND c.binding_id = requested.binding_id AND c.concept_id = requested.concept_id" +
		" ORDER BY c.contribution_key COLLATE \"C\" LIMIT 1) c ORDER BY requested.ord"
	return sql, args
}

// semanticResolveFamiliesQuery builds the one bounded family-resolution
// statement: a VALUES table of the requested family selectors, LEFT JOINed
// to the rows of each selector's exact tuple, grouped per request and
// dropped when no row matched. $1..$6 carry the authorization scope; the
// reference's own source_kind must equal the build's.
func semanticResolveFamiliesQuery(contributionFQN string, scope resolveScope, references []catalog.SemanticInventoryFamilyReference) (string, []any) {
	args := scope.args()
	var values strings.Builder
	values.WriteString("(VALUES ")
	for i, reference := range references {
		if i > 0 {
			values.WriteString(", ")
		}
		values.WriteString("(")
		for field, value := range []string{reference.BindingID, reference.ResourceType, reference.SourcePath, reference.System, reference.Version, reference.SourceKind} {
			if field > 0 {
				values.WriteString(", ")
			}
			param := "$" + strconv.Itoa(len(args)+1)
			args = append(args, value)
			values.WriteString(param + "::text")
		}
		values.WriteString(", " + strconv.Itoa(i) + ")")
	}
	values.WriteString(") AS requested(binding_id, resource_type, source_path, system, version, source_kind, ord)")

	join := " LEFT JOIN " + contributionFQN + " c ON " + semanticScopeClause("c") +
		" AND c.binding_id = requested.binding_id" +
		" AND c.resource_type = requested.resource_type" +
		" AND c.observation #>> '{source,type}' = requested.resource_type" +
		" AND c.observation #>> '{source,path}' = requested.source_path" +
		" AND c.observation #>> '{key,system}' = requested.system" +
		" AND " + semanticFamilyVersionMatch("c") +
		" AND c.observation #>> '{rule_hint}' = " + sqlStringLiteral(semanticCategoryRuleHint) +
		" AND " + semanticExtensionEmptyPredicate("c")

	sql := "SELECT requested.ord, " + semanticFamilyAggregateSelects("c") + " FROM " + values.String() + join +
		" WHERE requested.source_kind = $4" +
		" GROUP BY requested.ord HAVING COUNT(c.contribution_key) > 0" +
		" ORDER BY requested.ord"
	return sql, args
}

// ResolveSemanticInventorySelections resolves only the requested catalog
// identities under the caller's current authorization scope. It never pages
// or returns the wider inventory to the authoring path, and it returns
// results in request order with unmatched references dropped, matching the
// Arango adapter.
func (r *SemanticReadRepository) ResolveSemanticInventorySelections(ctx context.Context, opts catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
	selectionCount := len(opts.References) + len(opts.FamilyReferences)
	if strings.TrimSpace(opts.Project) == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" ||
		opts.AuthResourcePathsUnrestricted == nil || selectionCount == 0 || selectionCount > maxSemanticInventoryResolveSelections {
		return catalog.SemanticInventoryResolveResult{}, ErrInvalidSemanticInventorySelection
	}
	paths := append([]string(nil), opts.AuthResourcePaths...)
	sort.Strings(paths)
	paths = compactSemanticAuthPaths(paths)
	unrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(paths, opts.AuthResourcePathsUnrestricted)
	references := make([]catalog.SemanticInventoryReference, 0, len(opts.References))
	seen := make(map[string]struct{}, selectionCount)
	for _, reference := range opts.References {
		if strings.TrimSpace(reference.ConceptID) == "" || strings.TrimSpace(reference.BindingID) == "" {
			return catalog.SemanticInventoryResolveResult{}, ErrInvalidSemanticInventorySelection
		}
		identity := reference.BindingID + "\x00" + reference.ConceptID
		if _, ok := seen[identity]; ok {
			return catalog.SemanticInventoryResolveResult{}, ErrInvalidSemanticInventorySelection
		}
		seen[identity] = struct{}{}
		references = append(references, reference)
	}
	familyReferences := make([]catalog.SemanticInventoryFamilyReference, 0, len(opts.FamilyReferences))
	for _, reference := range opts.FamilyReferences {
		if reference.FamilyID != catalog.SemanticInventoryFamilyID(reference) || strings.TrimSpace(reference.BindingID) == "" ||
			strings.TrimSpace(reference.System) == "" || strings.TrimSpace(reference.ResourceType) == "" || strings.TrimSpace(reference.SourcePath) == "" ||
			(reference.SourceKind != catalog.SemanticInventorySourceFile && reference.SourceKind != catalog.SemanticInventorySourceRetained) {
			return catalog.SemanticInventoryResolveResult{}, ErrInvalidSemanticInventorySelection
		}
		if _, ok := seen[reference.FamilyID]; ok {
			return catalog.SemanticInventoryResolveResult{}, ErrInvalidSemanticInventorySelection
		}
		seen[reference.FamilyID] = struct{}{}
		familyReferences = append(familyReferences, reference)
	}

	result := catalog.SemanticInventoryResolveResult{Entries: []catalog.SemanticInventoryEntry{}, State: catalog.SemanticInventoryUnknown}
	buildsExist, err := r.tableExists(ctx, r.buildFQN)
	if err != nil {
		return catalog.SemanticInventoryResolveResult{}, err
	}
	if !buildsExist {
		return result, nil
	}
	// The Arango reference reads by build_key then compares build_id; the
	// unique build_key index serves the same single-row read, and the
	// stored build_id must still equal the derived identity before this
	// slice serves anything.
	build, storedBuildID, found, err := r.readBuildByKey(ctx, catalog.SemanticInventoryBuildKey(opts.Project, opts.DatasetGeneration))
	if err != nil {
		return catalog.SemanticInventoryResolveResult{}, err
	}
	buildID := catalog.SemanticInventoryBuildID(opts.Project, opts.DatasetGeneration)
	if !found || storedBuildID != buildID {
		return result, nil
	}
	normalizeReadBuild(&build, opts.Project, catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), buildID)
	result.Build, result.State = build, build.State
	if !unrestricted {
		// The same authorization boundary as the page: a restricted caller
		// never sees backfill internals through the resolve response.
		result.Build.ScannedResources = 0
		result.Build.Checkpoint = ""
		result.Build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{}
		result.Build.LeaseToken = ""
		result.Build.LeaseExpiresAt = 0
		result.Build.Diagnostics = nil
		result.Build.AuthResourcePath = ""
	}
	if build.State != catalog.SemanticInventoryComplete {
		return result, nil
	}
	if build.EntryIndexVersion >= catalog.SemanticInventoryEntryIndexVersion {
		// The same named boundary as the page: the reference would serve a
		// materialized-index shape this store neither persists nor can
		// verify, so both resolution paths are refused by name.
		return catalog.SemanticInventoryResolveResult{}, &SemanticInventoryEntriesUnsupportedError{
			Project:           build.Project,
			DatasetGeneration: build.DatasetGeneration,
			BuildID:           build.BuildID,
			EntryIndexVersion: build.EntryIndexVersion,
		}
	}
	sourceKind := build.SourceKind
	if sourceKind == "" {
		sourceKind = catalog.SemanticInventorySourceFile
	}
	contributionsExist, err := r.tableExists(ctx, r.contributionFQN)
	if err != nil {
		return catalog.SemanticInventoryResolveResult{}, err
	}
	if !contributionsExist {
		// The Arango adapter treats a missing inventory collection behind
		// a complete build as storage corruption, not an empty result.
		return catalog.SemanticInventoryResolveResult{}, &SemanticInventoryStorageMissingError{
			Schema: r.schema, Table: semanticInventoryContributionTableName,
		}
	}

	scope := resolveScope{
		project:           opts.Project,
		generation:        catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		buildID:           build.BuildID,
		sourceKind:        sourceKind,
		unrestricted:      unrestricted,
		paths:             paths,
		observationSchema: build.ObservationSchema,
	}
	if len(references) > 0 {
		entries, err := r.resolveReferences(ctx, scope, references)
		if err != nil {
			return catalog.SemanticInventoryResolveResult{}, err
		}
		result.Entries = entries
	}
	if len(familyReferences) > 0 {
		families, err := r.resolveFamilies(ctx, scope, familyReferences)
		if err != nil {
			return catalog.SemanticInventoryResolveResult{}, err
		}
		result.Families = families
	}
	return result, nil
}

// resolveScope is the six-value authorization scope every resolve statement
// binds at $1..$6, plus the completed build's observation schema, which
// labels resolved family output exactly as @observation_schema labels it in
// the reference queries.
type resolveScope struct {
	project           string
	generation        string
	buildID           string
	sourceKind        string
	unrestricted      bool
	paths             []string
	observationSchema int
}

func (s resolveScope) args() []any {
	return []any{s.project, s.generation, s.buildID, s.sourceKind, s.unrestricted, s.paths}
}

// resolveReferences resolves exact (binding_id, concept_id) references in
// one bounded statement. Each reference binds a LATERAL that picks the one
// authorized contribution with the least contribution_key — the
// content-addressed identity, the Arango _key the reference SORTs by and
// LIMITs to one — and returns its stored observation verbatim, as the
// reference's pre-materialization query does. Unmatched references drop
// out; results are assembled in request order.
func (r *SemanticReadRepository) resolveReferences(ctx context.Context, scope resolveScope, references []catalog.SemanticInventoryReference) ([]catalog.SemanticInventoryEntry, error) {
	sql, args := semanticResolveReferencesQuery(r.contributionFQN, scope, references)

	entries := make([]*catalog.SemanticInventoryEntry, len(references))
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("postgres: resolve semantic inventory selections: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ordinal int
			var entry catalog.SemanticInventoryEntry
			var observation []byte
			if err := rows.Scan(&ordinal, &entry.BindingID, &entry.ConceptID, &observation); err != nil {
				return fmt.Errorf("postgres: resolve semantic inventory selections: %w", err)
			}
			if ordinal < 0 || ordinal >= len(entries) {
				return fmt.Errorf("postgres: resolve semantic inventory selections: unexpected ordinal %d", ordinal)
			}
			if err := json.Unmarshal(observation, &entry.Observation); err != nil {
				return fmt.Errorf("postgres: decode resolved semantic inventory observation: %w", err)
			}
			entries[ordinal] = &entry
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	out := make([]catalog.SemanticInventoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry != nil {
			out = append(out, *entry)
		}
	}
	return out, nil
}

// resolveFamilies resolves system-version family references in one bounded
// statement. Each reference contributes its own aggregate row, built only
// from rows matching its exact tuple (resource type, source type, source
// path, system, version where the empty spelling is the versionless
// selection), the category rule hint, and the empty extension path. The
// returned family echoes the requested selector fields (the reference's
// MERGE(requested, {...}) keeps them) and carries the aggregate's observed
// payload values; unmatched references drop out.
func (r *SemanticReadRepository) resolveFamilies(ctx context.Context, scope resolveScope, references []catalog.SemanticInventoryFamilyReference) ([]catalog.SemanticInventoryFamily, error) {
	sql, args := semanticResolveFamiliesQuery(r.contributionFQN, scope, references)

	families := make([]*catalog.SemanticInventoryFamily, len(references))
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("postgres: resolve semantic inventory families: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var row semanticAggregateRow
			if err := rows.Scan(row.scanFamily()...); err != nil {
				return fmt.Errorf("postgres: resolve semantic inventory families: %w", err)
			}
			if row.ord < 0 || row.ord >= len(families) {
				return fmt.Errorf("postgres: resolve semantic inventory families: unexpected ordinal %d", row.ord)
			}
			reference := references[row.ord]
			family := row.toFamily(scope.sourceKind, scope.observationSchema)
			// The requested selector fields are authoritative (MERGE keeps
			// them); the aggregate supplies only observed payload values.
			family.SemanticInventoryFamilyReference = reference
			family.Source = catalog.SemanticObservationSource{
				Canonical: family.Source.Canonical,
				Type:      reference.ResourceType,
				Profile:   family.Source.Profile,
				Path:      reference.SourcePath,
			}
			family.FamilyID = catalog.SemanticInventoryFamilyID(reference)
			families[row.ord] = &family
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	out := make([]catalog.SemanticInventoryFamily, 0, len(families))
	for _, family := range families {
		if family != nil {
			out = append(out, *family)
		}
	}
	return out, nil
}

// readBuildByID reads the one build row for the exact scope identity.
func (r *SemanticReadRepository) readBuildByID(ctx context.Context, project, generation, buildID string) (catalog.SemanticInventoryBuild, bool, error) {
	var build catalog.SemanticInventoryBuild
	var found bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		// Ordered parameters: $1 project, $2 generation, $3 build id.
		err := conn.QueryRow(ctx,
			"SELECT "+semanticInventoryBuildColumns+" FROM "+r.buildFQN+
				" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3",
			project, generation, buildID,
		).Scan(scanSemanticInventoryBuild(&build)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("postgres: read semantic inventory build for page: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, false, err
	}
	return build, found, nil
}

// readBuildByKey reads the one build row named by its derived key and the
// stored build_id beside it. The shared column list carries only the
// fields the caller does not already bind, so the identity the reference
// verifies after its key lookup is read as an extra column here.
func (r *SemanticReadRepository) readBuildByKey(ctx context.Context, buildKey string) (catalog.SemanticInventoryBuild, string, bool, error) {
	var build catalog.SemanticInventoryBuild
	var storedBuildID string
	var found bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		// Ordered parameter: $1 build key.
		err := conn.QueryRow(ctx,
			"SELECT "+semanticInventoryBuildColumns+", build_id FROM "+r.buildFQN+" WHERE build_key = $1",
			buildKey,
		).Scan(append(scanSemanticInventoryBuild(&build), &storedBuildID)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("postgres: read semantic inventory build for resolve: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return catalog.SemanticInventoryBuild{}, "", false, err
	}
	return build, storedBuildID, found, nil
}

// queryPageRows runs one page statement and scans every identity row.
func (r *SemanticReadRepository) queryPageRows(ctx context.Context, sql string, args ...any) ([]semanticAggregateRow, error) {
	var rows []semanticAggregateRow
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		result, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("postgres: page semantic inventory: %w", err)
		}
		defer result.Close()
		for result.Next() {
			var row semanticAggregateRow
			if err := result.Scan(row.scanPageRow()...); err != nil {
				return fmt.Errorf("postgres: page semantic inventory: %w", err)
			}
			rows = append(rows, row)
		}
		return result.Err()
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// tableExists resolves one qualified table name; an unmigrated schema reads
// as an absent collection, the way the Arango adapter's CollectionExists
// feeds its unknown-state empty results.
func (r *SemanticReadRepository) tableExists(ctx context.Context, fqn string) (bool, error) {
	var exists bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", fqn).Scan(&exists); err != nil {
			return fmt.Errorf("postgres: check semantic inventory table %s: %w", fqn, err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (r *SemanticReadRepository) withConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire semantic read connection: %w", err)
	}
	defer conn.Release()
	return fn(conn)
}

// compactSemanticAuthPaths removes adjacent duplicates from a sorted path
// list, matching catalog.compactStrings and the Arango adapter's
// compactAuthPaths (both unexported in their own packages).
func compactSemanticAuthPaths(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	write := 1
	for read := 1; read < len(paths); read++ {
		if paths[read] != paths[write-1] {
			paths[write] = paths[read]
			write++
		}
	}
	return paths[:write]
}

// semanticInventoryPageLimit clamps one public page size, matching the
// reference's fixed 50-row cap.
func semanticInventoryPageLimit(limit int) int {
	if limit <= 0 || limit > catalog.SemanticInventoryPageLimit {
		return catalog.SemanticInventoryPageLimit
	}
	return limit
}

// --- SQL expression helpers ---------------------------------------------
//
// Each helper emits the PostgreSQL reading of one AQL expression from the
// reference queries in internal/catalog/arango/semantic_inventory.go. The
// observation leaf rule (absent and JSON-null both read as not present) is
// documented in the file header; the only writer encodes the typed catalog
// struct, so the JSON-null spelling is unreachable except in adversarially
// seeded fixtures, which the tests pin.

// semanticScopeClause emits the six scope predicates every contribution
// statement carries, bound to $1..$6: project, dataset generation, build id,
// source kind, the unrestricted flag, and the auth path list. An empty path
// list under a restricted scope matches nothing — the explicit
// restricted-empty result.
func semanticScopeClause(alias string) string {
	return alias + ".project = $1" +
		" AND " + alias + ".dataset_generation = $2" +
		" AND " + alias + ".build_id = $3" +
		" AND " + alias + ".source_kind = $4" +
		" AND ($5 OR " + alias + ".auth_resource_path = ANY($6::text[]))"
}

// semanticKeysetPredicate emits the after-key filter in byte order: binding
// strictly greater, or equal binding with the selection strictly greater.
// COLLATE "C" on both sides pins exact-byte comparison regardless of the
// server collation, matching the cursor's Go byte order and ArangoDB's
// memcmp string order.
func semanticKeysetPredicate(bindingColumn, selectionColumn string) string {
	return "($10 = ''" +
		" OR " + bindingColumn + " COLLATE \"C\" > $10 COLLATE \"C\"" +
		" OR (" + bindingColumn + " = $10 COLLATE \"C\"" +
		" AND " + selectionColumn + " COLLATE \"C\" > $11 COLLATE \"C\"))"
}

// semanticLeafText emits the observation leaf as its text form or SQL NULL
// when the leaf is absent or JSON null (MIN/MAX/SUM inputs skip it; callers
// COALESCE for output).
func semanticLeafText(alias string, path ...string) string {
	return "(" + alias + ".observation #>> '" + semanticPath(path) + "')"
}

// semanticLeafToken emits the leaf as the AQL NOT_NULL(leaf, "") token:
// absent or JSON null reads as the empty string (search text, and the
// version selection where the empty spelling is the versionless choice).
func semanticLeafToken(alias string, path ...string) string {
	return "COALESCE(" + semanticLeafText(alias, path...) + ", '')"
}

// semanticLeafJSON emits the leaf as jsonb or SQL NULL when absent; JSON
// null remains a value the caller must exclude explicitly.
func semanticLeafJSON(alias string, path ...string) string {
	return "(" + alias + ".observation #> '" + semanticPath(path) + "')"
}

// sqlStringLiteral renders a code-owned constant as a single-quoted SQL
// string literal (Go's strconv.Quote would emit a double-quoted identifier).
func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func semanticPath(path []string) string {
	return "{" + strings.Join(path, ",") + "}"
}

// semanticMinText is the AQL MIN over string leaves: byte-ordered
// (COLLATE "C"), skipping absent and JSON-null rows, so a leaf present as
// the empty string participates exactly as AQL's "" does.
func semanticMinText(alias string, path ...string) string {
	return "min(" + semanticLeafText(alias, path...) + " COLLATE \"C\")"
}

// semanticMinArray is the AQL MIN over JSON array leaves: only rows whose
// leaf is a JSON array participate (jsonb compares string elements by byte
// order, matching AQL's element-wise memcmp compare).
func semanticArrayLeafExpr(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "CASE WHEN jsonb_typeof(" + leaf + ") = 'array' THEN " + leaf + " END"
}

// PostgreSQL defines no min/max aggregate over jsonb, so the ordered
// array_agg projection gives the same single value from the btree ordering
// the reference's memcmp ordering was verified against. NULLS LAST keeps
// non-array rows (SQL NULL from the CASE) out of the front of both picks.
func semanticMinArray(alias string, path ...string) string {
	expr := semanticArrayLeafExpr(alias, path...)
	return "(array_agg(" + expr + " ORDER BY " + expr + " ASC NULLS LAST))[1]"
}

// semanticMaxArray is the AQL MAX counterpart for leaf-pair comparisons.
func semanticMaxArray(alias string, path ...string) string {
	expr := semanticArrayLeafExpr(alias, path...)
	return "(array_agg(" + expr + " ORDER BY " + expr + " DESC NULLS LAST))[1]"
}

// semanticMinBooleanText is the AQL MIN over boolean leaves: 'false' sorts
// before 'true' in byte order, matching AQL false < true, and non-boolean
// leaves never participate (AQL sorts booleans below strings and numbers).
func semanticMinBooleanText(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "min(CASE WHEN jsonb_typeof(" + leaf + ") = 'boolean' THEN " +
		semanticLeafText(alias, path...) + " COLLATE \"C\" END)"
}

// semanticPopulationSum is the AQL SUM over observation.population: an
// absent or non-integer leaf contributes 0, so one hostile row can neither
// abort the statement nor shift a real count.
func semanticPopulationSum(alias string) string {
	leaf := semanticLeafText(alias, "population")
	return "coalesce(sum(CASE WHEN jsonb_typeof(" + semanticLeafJSON(alias, "population") + ") = 'number'" +
		" AND " + leaf + " ~ '^-?[0-9]+$' THEN (" + leaf + ")::numeric ELSE 0 END), 0)::bigint"
}

// semanticExampleMin is the AQL MIN(d.example): the persisted column stores
// the typed empty string for "no example", which the encoder omits from the
// Arango document entirely, so MIN must skip it exactly as AQL skips the
// missing attribute — otherwise one example-less sibling would erase a real
// example from the identity.
func semanticExampleMin(alias string) string {
	return "min(NULLIF(" + alias + ".example, '') COLLATE \"C\")"
}

// semanticExamplesTruncatedAny is the AQL MAX(leaf ? 1 : 0): absent, JSON
// null, and false read falsy; any other present value reads truthy, exactly
// as AQL's ternary treats everything except null and false.
func semanticExamplesTruncatedAny(alias string) string {
	leaf := semanticLeafJSON(alias, "examples_truncated")
	return "max(CASE WHEN " + leaf + " IS NULL OR " + leaf + " = 'false'::jsonb OR " + leaf + " = 'null'::jsonb" +
		" THEN 0 ELSE 1 END)"
}

// semanticExtensionEmptyPredicate is the AQL
// LENGTH(NOT_NULL(extension_url_path, [])) == 0: an absent leaf is the
// empty array, an array counts, and anything else (including an explicit
// JSON null, whose LENGTH cannot match) fails.
func semanticExtensionEmptyPredicate(alias string) string {
	leaf := semanticLeafJSON(alias, "extension_url_path")
	return "(CASE WHEN " + leaf + " IS NULL THEN true" +
		" WHEN jsonb_typeof(" + leaf + ") = 'array' THEN jsonb_array_length(" + leaf + ") = 0" +
		" ELSE false END)"
}

// semanticCategoryPredicate is the reference category test: rule_hint is
// exactly CODED_CONCEPT_V1, the key system is a non-empty token, and the
// extension path is empty.
func semanticCategoryPredicate(alias string) string {
	return "((" + semanticLeafText(alias, "rule_hint") + ") = " + sqlStringLiteral(semanticCategoryRuleHint) +
		" AND " + semanticLeafToken(alias, "key", "system") + " <> ''" +
		" AND " + semanticExtensionEmptyPredicate(alias) + ")"
}

// semanticFamilyElement renders one family-scope element the way AQL
// JSON_STRINGIFY renders the NOT_NULL-canonicalized value inside the
// selection array: absent or JSON null becomes the JSON empty string, and a
// present value keeps its exact JSON text (a string element is quoted and
// escaped by PostgreSQL's jsonb renderer, which matches JSON.stringify's
// escape set: quote, backslash, and the control characters, with valid
// UTF-8 and forward slashes passed through).
func semanticFamilyElement(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "COALESCE(" + leaf + "::text, 'null')"
}

// semanticFamilyVersionElement is the selection array's version slot:
// NOT_NULL(leaf, "") keeps an explicit JSON null (a present value) and
// replaces only the ABSENT spelling with "".
func semanticFamilyVersionElement(alias string) string {
	leaf := semanticLeafJSON(alias, "key", "version")
	return "CASE WHEN " + leaf + " IS NULL THEN '\"\"' WHEN " + leaf + " = 'null'::jsonb THEN 'null' ELSE " + leaf + "::text END"
}

// semanticFamilyVersionMatch is the resolve-side NOT_NULL(leaf, "") ==
// requested.version filter: absent compares as the empty string, an
// explicit JSON null compares as SQL NULL against anything and so matches
// neither versioned nor versionless requests.
func semanticFamilyVersionMatch(alias string) string {
	leaf := semanticLeafJSON(alias, "key", "version")
	return "(CASE WHEN " + leaf + " IS NULL THEN '' WHEN " + leaf + " = 'null'::jsonb THEN NULL" +
		" ELSE (" + alias + ".observation #>> '{key,version}') END) = requested.version"
}

// semanticFamilySelectionExpr is the reference selection_id: category rows
// collapse to "family:" + the compact JSON array of the four scope values;
// every other row keeps its concept_id. jsonb_build_array is deliberately
// not used: its text form pads commas with spaces, which is not the compact
// byte form the reference orders and the cursor carries.
func semanticFamilySelectionExpr(alias string) string {
	return "CASE WHEN " + semanticCategoryPredicate(alias) + " THEN 'family:['" +
		" || " + semanticFamilyElement(alias, "source", "type") + " || ','" +
		" || " + semanticFamilyElement(alias, "source", "path") + " || ','" +
		" || " + semanticFamilyElement(alias, "key", "system") + " || ','" +
		" || " + semanticFamilyVersionElement(alias) + " || ']'" +
		" ELSE " + alias + ".concept_id END"
}

// semanticSearchContains is the AQL
// CONTAINS(LOWER(CONCAT_SEPARATOR(" ", resource_type, source.path,
// value.selector, owning_scope, key.system, key.code, key.display)),
// LOWER(@query)). Every token participates: each leaf is COALESCEd to the
// empty string so concat_ws keeps its separator slot exactly like
// CONCAT_SEPARATOR. Both sides fold with lower() pinned to COLLATE "C":
// PostgreSQL's locale lower() folds non-ASCII, ArangoDB's LOWER does not.
func semanticSearchContains(alias string, queryParam string) string {
	search := "concat_ws(' ', " + alias + ".resource_type" +
		", " + semanticLeafToken(alias, "source", "path") +
		", " + semanticLeafToken(alias, "value", "selector") +
		", " + semanticLeafToken(alias, "owning_scope") +
		", " + semanticLeafToken(alias, "key", "system") +
		", " + semanticLeafToken(alias, "key", "code") +
		", " + semanticLeafToken(alias, "key", "display") + ")"
	return "strpos(lower(" + search + " COLLATE \"C\"), lower(" + queryParam + " COLLATE \"C\")) > 0"
}

// --- aggregate column sets ------------------------------------------------

// semanticEntryAggregateSelects emits the ordered aggregate columns that
// reconstruct one SemanticInventoryEntry observation, in the exact order
// semanticAggregateRow.scanPageRow binds. Family pages compute this full
// set and ignore the entry-only columns for category identities.
func semanticEntryAggregateSelects(alias string) string {
	return semanticPopulationSum(alias) + " AS agg_population" +
		", " + semanticExampleMin(alias) + " AS agg_example" +
		", " + semanticMinText(alias, "source", "canonical") + " AS agg_canonical" +
		", " + semanticMinText(alias, "source", "type") + " AS agg_source_type" +
		", " + semanticMinText(alias, "source", "profile") + " AS agg_profile" +
		", " + semanticMinText(alias, "source", "path") + " AS agg_path" +
		", " + semanticMinText(alias, "key", "selector") + " AS agg_key_selector" +
		", " + semanticMinText(alias, "key", "system") + " AS agg_key_system" +
		", " + semanticMinText(alias, "key", "version") + " AS agg_key_version" +
		", min(" + semanticLeafToken(alias, "key", "version") + " COLLATE \"C\") AS agg_version_token" +
		", " + semanticMinText(alias, "key", "code") + " AS agg_key_code" +
		", " + semanticMinText(alias, "key", "display") + " AS agg_key_display" +
		", " + semanticMinText(alias, "value", "selector") + " AS agg_value_selector" +
		", " + semanticMinText(alias, "value", "type") + " AS agg_value_type" +
		", " + semanticMinArray(alias, "value", "fallback_selectors") + " AS agg_fallback_selectors" +
		", " + semanticMinBooleanText(alias, "value", "fallback_on_blank_string") + " AS agg_fallback_on_blank" +
		", " + semanticMinText(alias, "owning_scope") + " AS agg_owning_scope" +
		", " + semanticMinArray(alias, "extension_url_path") + " AS agg_extension_url_path" +
		", " + semanticMinText(alias, "choice_arm") + " AS agg_choice_arm" +
		", " + semanticMinText(alias, "logical_type") + " AS agg_logical_type" +
		", " + semanticMinArray(alias, "observed_units") + " AS agg_units_min" +
		", " + semanticMaxArray(alias, "observed_units") + " AS agg_units_max" +
		", " + semanticMinText(alias, "completeness") + " AS agg_completeness" +
		", " + semanticMinText(alias, "status") + " AS agg_status" +
		", " + semanticMinText(alias, "rule_hint") + " AS agg_rule_hint" +
		", " + semanticMinText(alias, "rule_version") + " AS agg_rule_version" +
		", " + semanticExamplesTruncatedAny(alias) + " AS agg_examples_truncated"
}

// semanticFamilyAggregateSelects emits the aggregate columns for a family
// resolution, in the exact order semanticAggregateRow.scanFamily binds.
func semanticFamilyAggregateSelects(alias string) string {
	return semanticPopulationSum(alias) + " AS agg_population" +
		", " + semanticExampleMin(alias) + " AS agg_example" +
		", " + semanticMinText(alias, "source", "canonical") + " AS agg_canonical" +
		", " + semanticMinText(alias, "source", "type") + " AS agg_source_type" +
		", " + semanticMinText(alias, "source", "profile") + " AS agg_profile" +
		", " + semanticMinText(alias, "source", "path") + " AS agg_path" +
		", " + semanticMinText(alias, "key", "selector") + " AS agg_key_selector" +
		", " + semanticMinText(alias, "key", "system") + " AS agg_key_system" +
		", min(" + semanticLeafToken(alias, "key", "version") + " COLLATE \"C\") AS agg_version_token" +
		", " + semanticMinText(alias, "value", "selector") + " AS agg_value_selector" +
		", " + semanticMinText(alias, "value", "type") + " AS agg_value_type" +
		", " + semanticMinArray(alias, "value", "fallback_selectors") + " AS agg_fallback_selectors" +
		", " + semanticMinBooleanText(alias, "value", "fallback_on_blank_string") + " AS agg_fallback_on_blank" +
		", " + semanticMinText(alias, "owning_scope") + " AS agg_owning_scope" +
		", " + semanticMinText(alias, "logical_type") + " AS agg_logical_type" +
		", " + semanticMinText(alias, "completeness") + " AS agg_completeness" +
		", " + semanticMinText(alias, "status") + " AS agg_status" +
		", " + semanticMinText(alias, "rule_version") + " AS agg_rule_version" +
		", " + semanticExamplesTruncatedAny(alias) + " AS agg_examples_truncated"
}

// semanticAggregateRow holds one scanned page or family-resolution row.
// Nullable fields mirror the SQL NULLs the aggregates produce; the
// conversions in toEntry/toFamily apply the documented AQL output rules.
type semanticAggregateRow struct {
	ord               int
	bindingID         string
	conceptID         string
	selectionID       string
	isCategory        bool
	population        int64
	example           *string
	canonical         *string
	sourceType        *string
	profile           *string
	path              *string
	keySelector       *string
	keySystem         *string
	keyVersion        *string
	versionToken      *string
	keyCode           *string
	keyDisplay        *string
	valueSelector     *string
	valueType         *string
	fallbackSelectors []byte
	fallbackOnBlank   *string
	owningScope       *string
	extensionURLPath  []byte
	choiceArm         *string
	logicalType       *string
	unitsMin          []byte
	unitsMax          []byte
	completeness      *string
	status            *string
	ruleHint          *string
	ruleVersion       *string
	examplesTruncated int
}

// scanPageRow binds the page SELECT column order, shared by both modes:
// identity keys first, then the full entry aggregate.
func (row *semanticAggregateRow) scanPageRow() []any {
	return []any{
		&row.bindingID, &row.conceptID, &row.selectionID, &row.isCategory,
		&row.population, &row.example, &row.canonical, &row.sourceType,
		&row.profile, &row.path, &row.keySelector, &row.keySystem,
		&row.keyVersion, &row.versionToken, &row.keyCode, &row.keyDisplay,
		&row.valueSelector, &row.valueType, &row.fallbackSelectors, &row.fallbackOnBlank,
		&row.owningScope, &row.extensionURLPath, &row.choiceArm, &row.logicalType,
		&row.unitsMin, &row.unitsMax, &row.completeness, &row.status,
		&row.ruleHint, &row.ruleVersion, &row.examplesTruncated,
	}
}

// scanFamily binds the family-resolution SELECT column order: the requested
// ordinal first, then the family aggregate columns.
func (row *semanticAggregateRow) scanFamily() []any {
	return []any{
		&row.ord,
		&row.population, &row.example, &row.canonical, &row.sourceType,
		&row.profile, &row.path, &row.keySelector, &row.keySystem,
		&row.versionToken, &row.valueSelector, &row.valueType,
		&row.fallbackSelectors, &row.fallbackOnBlank, &row.owningScope,
		&row.logicalType, &row.completeness, &row.status, &row.ruleVersion,
		&row.examplesTruncated,
	}
}

func semanticStringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func semanticStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// toEntry reconstructs the reference's per-identity entry output: the
// build's observation schema labels the aggregate, examples hold the single
// least example (empty list when none), observed units are the least array
// or the empty array, examples are truncated whenever any contribution said
// so or the identity carries more than one population, and unit truncation
// compares the least and greatest observed-units arrays.
func (row *semanticAggregateRow) toEntry(observationSchema int) catalog.SemanticInventoryEntry {
	entry := catalog.SemanticInventoryEntry{
		ConceptID: row.conceptID,
		BindingID: row.bindingID,
		Observation: catalog.SemanticObservation{
			SchemaVersion: observationSchema,
			Source: catalog.SemanticObservationSource{
				Canonical: semanticStringOrEmpty(row.canonical),
				Type:      semanticStringOrEmpty(row.sourceType),
				Profile:   semanticStringOrEmpty(row.profile),
				Path:      semanticStringOrEmpty(row.path),
			},
			Key: catalog.SemanticObservationKey{
				Selector: semanticStringOrEmpty(row.keySelector),
				System:   semanticStringOrEmpty(row.keySystem),
				Version:  semanticStringOrEmpty(row.keyVersion),
				Code:     semanticStringOrEmpty(row.keyCode),
				Display:  semanticStringOrEmpty(row.keyDisplay),
			},
			Value: catalog.SemanticObservationValue{
				Selector:              semanticStringOrEmpty(row.valueSelector),
				Type:                  semanticStringOrEmpty(row.valueType),
				FallbackSelectors:     semanticStringList(row.fallbackSelectors),
				FallbackOnBlankString: semanticStringOrEmpty(row.fallbackOnBlank) == "true",
			},
			OwningScope:       semanticStringOrEmpty(row.owningScope),
			ExtensionURLPath:  semanticStringList(row.extensionURLPath),
			ChoiceArm:         semanticStringOrEmpty(row.choiceArm),
			LogicalType:       semanticStringOrEmpty(row.logicalType),
			ObservedUnits:     []string{},
			Completeness:      catalog.SemanticObservationCompleteness(semanticStringOrEmpty(row.completeness)),
			Status:            semanticStringOrEmpty(row.status),
			Population:        row.population,
			Examples:          []string{},
			ExamplesTruncated: row.examplesTruncated > 0 || row.population > 1,
			RuleHint:          semanticStringOrEmpty(row.ruleHint),
			RuleVersion:       semanticStringOrEmpty(row.ruleVersion),
		},
	}
	if units := semanticStringList(row.unitsMin); units != nil {
		entry.Observation.ObservedUnits = units
	}
	entry.Observation.ObservedUnitsTruncated = !equalJSONArray(row.unitsMin, row.unitsMax)
	if example := semanticStringOrEmpty(row.example); example != "" {
		entry.Observation.Examples = []string{example}
	}
	return entry
}

// toFamily reconstructs the reference's family output. Resolution callers
// overwrite the selector fields with the requested reference; page callers
// keep the aggregate-derived tuple (the reference's family_scope).
func (row *semanticAggregateRow) toFamily(sourceKind string, observationSchema int) catalog.SemanticInventoryFamily {
	family := catalog.SemanticInventoryFamily{
		SemanticInventoryFamilyReference: catalog.SemanticInventoryFamilyReference{
			BindingID:    row.bindingID,
			SourceKind:   sourceKind,
			ResourceType: semanticStringOrEmpty(row.sourceType),
			SourcePath:   semanticStringOrEmpty(row.path),
			System:       semanticStringOrEmpty(row.keySystem),
			Version:      semanticStringOrEmpty(row.versionToken),
		},
		Source: catalog.SemanticObservationSource{
			Canonical: semanticStringOrEmpty(row.canonical),
			Type:      semanticStringOrEmpty(row.sourceType),
			Profile:   semanticStringOrEmpty(row.profile),
			Path:      semanticStringOrEmpty(row.path),
		},
		KeySelector: semanticStringOrEmpty(row.keySelector),
		Value: catalog.SemanticObservationValue{
			Selector:              semanticStringOrEmpty(row.valueSelector),
			Type:                  semanticStringOrEmpty(row.valueType),
			FallbackSelectors:     semanticStringList(row.fallbackSelectors),
			FallbackOnBlankString: semanticStringOrEmpty(row.fallbackOnBlank) == "true",
		},
		OwningScope:       semanticStringOrEmpty(row.owningScope),
		LogicalType:       semanticStringOrEmpty(row.logicalType),
		SchemaVersion:     observationSchema,
		RuleVersion:       semanticStringOrEmpty(row.ruleVersion),
		Completeness:      catalog.SemanticObservationCompleteness(semanticStringOrEmpty(row.completeness)),
		Status:            semanticStringOrEmpty(row.status),
		Population:        row.population,
		Examples:          []string{},
		ExamplesTruncated: row.examplesTruncated > 0 || row.population > 1,
	}
	if example := semanticStringOrEmpty(row.example); example != "" {
		family.Examples = []string{example}
	}
	return family
}

// equalJSONArray compares two scanned jsonb values; both absent (no row
// carried an array) compare equal, which keeps observed_units_truncated
// false for an identity that never observed units.
func equalJSONArray(left, right []byte) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return string(left) == string(right)
}
