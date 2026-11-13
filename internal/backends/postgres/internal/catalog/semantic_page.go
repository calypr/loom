package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxSemanticInventoryResolveSelections = 100
	semanticCategoryRuleHint              = "CODED_CONCEPT_V1"
)

var (
	ErrInvalidSemanticInventoryPage      = errors.New("postgres: invalid semantic inventory page request")
	ErrInvalidSemanticInventorySelection = errors.New("postgres: invalid semantic inventory selection request")
)

type SemanticReadRepository struct {
	src             storepostgres.ConnSource
	schema          string
	contributionFQN string
	buildFQN        string
}

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

var (
	_ func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error)             = (*SemanticReadRepository)(nil).PageSemanticInventory
	_ func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) = (*SemanticReadRepository)(nil).ResolveSemanticInventorySelections
)

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
		" AND ($7 = '' OR c.resource_type = $7)" +
		" AND c.binding_id = i.binding_id" + lateralMatch +
		") a ORDER BY i.binding_id COLLATE \"C\", i.selection_id COLLATE \"C\", i.is_category")
	return sb.String(), args
}

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

func (r *SemanticReadRepository) readBuildByID(ctx context.Context, project, generation, buildID string) (catalog.SemanticInventoryBuild, bool, error) {
	var build catalog.SemanticInventoryBuild
	var found bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
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

func (r *SemanticReadRepository) readBuildByKey(ctx context.Context, buildKey string) (catalog.SemanticInventoryBuild, string, bool, error) {
	var build catalog.SemanticInventoryBuild
	var storedBuildID string
	var found bool
	err := r.withConn(ctx, func(conn *pgxpool.Conn) error {
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

func semanticInventoryPageLimit(limit int) int {
	if limit <= 0 || limit > catalog.SemanticInventoryPageLimit {
		return catalog.SemanticInventoryPageLimit
	}
	return limit
}

func semanticScopeClause(alias string) string {
	return alias + ".project = $1" +
		" AND " + alias + ".dataset_generation = $2" +
		" AND " + alias + ".build_id = $3" +
		" AND " + alias + ".source_kind = $4" +
		" AND ($5 OR " + alias + ".auth_resource_path = ANY($6::text[]))"
}

func semanticKeysetPredicate(bindingColumn, selectionColumn string) string {
	return "($10 = ''" +
		" OR " + bindingColumn + " COLLATE \"C\" > $10 COLLATE \"C\"" +
		" OR (" + bindingColumn + " = $10 COLLATE \"C\"" +
		" AND " + selectionColumn + " COLLATE \"C\" > $11 COLLATE \"C\"))"
}

func semanticLeafText(alias string, path ...string) string {
	return "(" + alias + ".observation #>> '" + semanticPath(path) + "')"
}

func semanticLeafToken(alias string, path ...string) string {
	return "COALESCE(" + semanticLeafText(alias, path...) + ", '')"
}

func semanticLeafJSON(alias string, path ...string) string {
	return "(" + alias + ".observation #> '" + semanticPath(path) + "')"
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func semanticPath(path []string) string {
	return "{" + strings.Join(path, ",") + "}"
}

func semanticMinText(alias string, path ...string) string {
	return "min(" + semanticLeafText(alias, path...) + " COLLATE \"C\")"
}

func semanticArrayLeafExpr(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "CASE WHEN jsonb_typeof(" + leaf + ") = 'array' THEN " + leaf + " END"
}

func semanticMinArray(alias string, path ...string) string {
	expr := semanticArrayLeafExpr(alias, path...)
	return "(array_agg(" + expr + " ORDER BY " + expr + " ASC NULLS LAST))[1]"
}

func semanticMaxArray(alias string, path ...string) string {
	expr := semanticArrayLeafExpr(alias, path...)
	return "(array_agg(" + expr + " ORDER BY " + expr + " DESC NULLS LAST))[1]"
}

func semanticMinBooleanText(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "min(CASE WHEN jsonb_typeof(" + leaf + ") = 'boolean' THEN " +
		semanticLeafText(alias, path...) + " COLLATE \"C\" END)"
}

func semanticPopulationSum(alias string) string {
	leaf := semanticLeafText(alias, "population")
	return "coalesce(sum(CASE WHEN jsonb_typeof(" + semanticLeafJSON(alias, "population") + ") = 'number'" +
		" AND " + leaf + " ~ '^-?[0-9]+$' THEN (" + leaf + ")::numeric ELSE 0 END), 0)::bigint"
}

func semanticExampleMin(alias string) string {
	return "min(NULLIF(" + alias + ".example, '') COLLATE \"C\")"
}

func semanticExamplesTruncatedAny(alias string) string {
	leaf := semanticLeafJSON(alias, "examples_truncated")
	return "max(CASE WHEN " + leaf + " IS NULL OR " + leaf + " = 'false'::jsonb OR " + leaf + " = 'null'::jsonb" +
		" THEN 0 ELSE 1 END)"
}

func semanticExtensionEmptyPredicate(alias string) string {
	leaf := semanticLeafJSON(alias, "extension_url_path")
	return "(CASE WHEN " + leaf + " IS NULL THEN true" +
		" WHEN jsonb_typeof(" + leaf + ") = 'array' THEN jsonb_array_length(" + leaf + ") = 0" +
		" ELSE false END)"
}

func semanticCategoryPredicate(alias string) string {
	return "((" + semanticLeafText(alias, "rule_hint") + ") = " + sqlStringLiteral(semanticCategoryRuleHint) +
		" AND " + semanticLeafToken(alias, "key", "system") + " <> ''" +
		" AND " + semanticExtensionEmptyPredicate(alias) + ")"
}

func semanticFamilyElement(alias string, path ...string) string {
	leaf := semanticLeafJSON(alias, path...)
	return "COALESCE(" + leaf + "::text, 'null')"
}

func semanticFamilyVersionElement(alias string) string {
	leaf := semanticLeafJSON(alias, "key", "version")
	return "CASE WHEN " + leaf + " IS NULL THEN '\"\"' WHEN " + leaf + " = 'null'::jsonb THEN 'null' ELSE " + leaf + "::text END"
}

func semanticFamilyVersionMatch(alias string) string {
	leaf := semanticLeafJSON(alias, "key", "version")
	return "(CASE WHEN " + leaf + " IS NULL THEN '' WHEN " + leaf + " = 'null'::jsonb THEN NULL" +
		" ELSE (" + alias + ".observation #>> '{key,version}') END) = requested.version"
}

func semanticFamilySelectionExpr(alias string) string {
	return "CASE WHEN " + semanticCategoryPredicate(alias) + " THEN 'family:['" +
		" || " + semanticFamilyElement(alias, "source", "type") + " || ','" +
		" || " + semanticFamilyElement(alias, "source", "path") + " || ','" +
		" || " + semanticFamilyElement(alias, "key", "system") + " || ','" +
		" || " + semanticFamilyVersionElement(alias) + " || ']'" +
		" ELSE " + alias + ".concept_id END"
}

func semanticSearchContains(alias string, queryParam string) string {
	search := "concat_ws(' ', " + alias + ".resource_type" +
		", " + semanticLeafText(alias, "source", "path") +
		", " + semanticLeafText(alias, "value", "selector") +
		", " + semanticLeafText(alias, "owning_scope") +
		", " + semanticLeafText(alias, "key", "system") +
		", " + semanticLeafText(alias, "key", "code") +
		", " + semanticLeafText(alias, "key", "display") + ")"
	return "strpos(lower(" + search + " COLLATE \"C\"), lower(" + queryParam + " COLLATE \"C\")) > 0"
}

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

func equalJSONArray(left, right []byte) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return string(left) == string(right)
}
