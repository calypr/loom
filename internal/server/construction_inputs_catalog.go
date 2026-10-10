package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/api/columncapabilities"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

const (
	constructionInputsDefaultLimit = 25
	constructionInputsMaxLimit     = 100
	constructionInputsMaxQuery     = 200
)

type constructionInputsRequest struct {
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
	Query                string `json:"query,omitempty"`
	Cursor               string `json:"cursor,omitempty"`
	Limit                int    `json:"limit,omitempty"`
}

type constructionInputsResponse struct {
	SnapshotToken     string                   `json:"snapshotToken"`
	DraftVersion      int64                    `json:"draftVersion"`
	DraftDigest       string                   `json:"draftDigest"`
	DatasetGeneration string                   `json:"datasetGeneration"`
	Entries           []constructionInputEntry `json:"entries"`
	NextCursor        string                   `json:"nextCursor,omitempty"`
}

type constructionInputEntry struct {
	Kind        string                    `json:"kind"`
	TableID     string                    `json:"tableId"`
	RevisionID  string                    `json:"revisionId"`
	OutputID    string                    `json:"outputId"`
	TableTitle  string                    `json:"tableTitle"`
	OutputTitle string                    `json:"outputTitle"`
	RowMeaning  string                    `json:"rowMeaning"`
	IsCurrent   bool                      `json:"isCurrent"`
	CreatedAt   time.Time                 `json:"createdAt"`
	Columns     []constructionInputColumn `json:"columns"`
}

type constructionInputColumn struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Label          string `json:"label"`
	Type           string `json:"type"`
	ClickHouseType string `json:"clickhouseType"`
	Nullable       bool   `json:"nullable"`
	Repeated       bool   `json:"repeated"`
	SemanticPath   string `json:"semanticPath,omitempty"`
}

type constructionInputsExactReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (published.Materialization, error)
	WithExecutionReadPins(context.Context, []string, func(context.Context) error) error
}

type constructionInputsCatalogReader interface {
	ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error)
	GetPointer(context.Context, string) (publication.BundlePointer, error)
}

type constructionInputsExplorerReader interface {
	Get(context.Context, string, string) (*explorer.Explorer, error)
}

type constructionInputsRevisionReader interface {
	GetRevision(context.Context, string) (*explorer.Revision, error)
}

type constructionInputsCatalog struct {
	reader       constructionInputsExactReader
	catalog      constructionInputsCatalogReader
	capabilities lifecycle.CapabilityResolver
	scopes       *authscope.ScopeResolver
	explorers    constructionInputsExplorerReader
	revisions    constructionInputsRevisionReader
}

type constructionInputReference struct {
	execution publication.BundleExecution
	output    publication.BundleOutputRecord
	tableID   string
}

type constructionInputCandidate struct {
	reference    constructionInputReference
	entry        constructionInputEntry
	presentation constructionInputPresentation
}

type constructionInputPresentation struct {
	tableTitle   string
	outputTitle  string
	rowMeaning   string
	columnLabels map[string]string
}

type constructionInputPublicationMetadata struct {
	tableTitle string
	outputs    map[string]constructionInputOutputPresentation
	contracts  explorer.PublicOutputContracts
}

type constructionInputOutputPresentation struct {
	title            string
	rootResourceType string
}

type constructionInputsCursor struct {
	Version       int    `json:"v"`
	Query         string `json:"q"`
	SnapshotToken string `json:"s"`
	DraftVersion  int64  `json:"d"`
	DraftDigest   string `json:"h"`
	TableID       string `json:"t"`
	OutputID      string `json:"o"`
	RevisionID    string `json:"r"`
}

func (c constructionInputsCatalog) list(ctx context.Context, project, explorerID string, request constructionInputsRequest) (constructionInputsResponse, error) {
	response := constructionInputsResponse{Entries: []constructionInputEntry{}}
	query, limit, cursor, err := validateConstructionInputsRequest(request)
	if err != nil {
		return response, malformedRouteError("construction-inputs", err)
	}
	if c.reader == nil || c.catalog == nil || c.capabilities.ForCompilation == nil || c.explorers == nil {
		return response, explorerUnavailable("construction-inputs", "AUTHORING_UNAVAILABLE", "published construction inputs are not configured")
	}
	project = projectid.Canonical(project)
	if project == "" || strings.TrimSpace(explorerID) == "" || strings.TrimSpace(explorerID) != explorerID {
		return response, malformedRouteError("construction-inputs", errors.New("project and explorerId are required"))
	}

	owner, snapshot, scope, err := c.authorizedSnapshot(ctx, project, explorerID, request)
	if err != nil {
		return response, err
	}
	response.SnapshotToken = request.SnapshotToken
	response.DraftVersion = owner.DraftVersion
	response.DraftDigest = owner.DraftDigest
	response.DatasetGeneration = snapshot.Identity.Generation
	if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
		return response, nil
	}

	executions, err := c.catalog.ListExecutions(ctx, publication.BundlePublished, time.Now().UTC().Add(time.Second))
	if err != nil {
		return constructionInputsResponse{}, constructionInputsUnavailable(err)
	}
	references := make([]constructionInputReference, 0)
	for _, execution := range executions {
		execution = execution.CanonicalizeLegacy()
		if !execution.State.Successful() || projectid.Canonical(execution.Project) != project || execution.DatasetGeneration != snapshot.Identity.Generation || strings.TrimSpace(execution.ID) == "" {
			continue
		}
		for _, output := range execution.Outputs {
			if !output.Queryable() || strings.TrimSpace(output.Name) == "" {
				continue
			}
			selector := output.Selector
			if !selector.Valid() {
				selector = execution.Selector(output.Name)
			}
			if !selector.Valid() {
				continue
			}
			references = append(references, constructionInputReference{execution: execution, output: output, tableID: selector.Key()})
		}
	}
	if len(references) == 0 {
		return response, nil
	}

	candidates := make([]constructionInputCandidate, 0, len(references))
	publicationMetadata := make(map[string]*constructionInputPublicationMetadata)
	publicationMetadataLoaded := make(map[string]bool)
	for _, reference := range references {
		materialization := constructionInputMaterialization(reference)
		if err := validateConstructionInputMaterialization(materialization, reference, project, snapshot.Identity.Generation); err != nil {
			continue
		}
		persistedScope, err := persistedMaterializationScope(materialization)
		if err != nil {
			continue
		}
		persistedBindings := recipe.RuntimeBindings{
			Project: materialization.Project, DatasetGeneration: materialization.DatasetGeneration,
			AuthScopeMode: persistedScope.Mode, AuthResourcePaths: append([]string(nil), persistedScope.AuthResourcePaths...),
		}
		if materialization.ScopeDigest != recipeScopeDigest(persistedBindings) {
			continue
		}
		effectiveScope, err := c.effectiveConstructionInputScope(ctx, persistedScope, materialization)
		if err != nil {
			if errors.Is(err, authscope.ErrForbidden) || errors.Is(err, authscope.ErrUnauthenticated) {
				continue
			}
			return constructionInputsResponse{}, constructionInputsUnavailable(err)
		}
		if effectiveScope.Mode == authscope.ReadScopeRestricted && len(effectiveScope.AuthResourcePaths) == 0 || !sameClickHouseReadScope(effectiveScope, scope) {
			continue
		}
		if _, err := resolvedClickHouseColumns(materialization.Columns); err != nil {
			continue
		}
		metadataKey := reference.execution.ID + "\x00" + reference.execution.ReceiptID
		if !publicationMetadataLoaded[metadataKey] {
			metadata, err := c.constructionInputPublicationMetadata(ctx, reference.execution, project)
			if err != nil {
				return constructionInputsResponse{}, constructionInputsUnavailable(err)
			}
			publicationMetadata[metadataKey] = metadata
			publicationMetadataLoaded[metadataKey] = true
		}
		presentation, err := constructionInputPresentationFor(materialization, publicationMetadata[metadataKey])
		if err != nil {
			return constructionInputsResponse{}, constructionInputsUnavailable(err)
		}
		columns := constructionInputColumns(materialization.Columns, presentation.columnLabels)
		if len(columns) == 0 {
			continue
		}
		entry := constructionInputEntry{
			Kind: "TABLE_REVISION", TableID: materialization.Selector.Key(), RevisionID: materialization.Revision,
			OutputID: materialization.Selector.Output, TableTitle: presentation.tableTitle,
			OutputTitle: presentation.outputTitle,
			RowMeaning:  presentation.rowMeaning, CreatedAt: materialization.CreatedAt, Columns: columns,
		}
		if constructionInputMatchesQuery(entry, query) {
			candidates = append(candidates, constructionInputCandidate{reference: reference, entry: entry, presentation: presentation})
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftEntry, rightEntry := candidates[left].entry, candidates[right].entry
		if leftEntry.TableID != rightEntry.TableID {
			return leftEntry.TableID < rightEntry.TableID
		}
		if leftEntry.OutputID != rightEntry.OutputID {
			return leftEntry.OutputID < rightEntry.OutputID
		}
		return leftEntry.RevisionID < rightEntry.RevisionID
	})
	pageCandidates := make([]constructionInputCandidate, 0, limit)
	for _, candidate := range candidates {
		if cursor != nil && !constructionInputAfterCursor(candidate.entry, *cursor) {
			continue
		}
		pageCandidates = append(pageCandidates, candidate)
		if len(pageCandidates) > limit {
			break
		}
	}
	hasMore := len(pageCandidates) > limit
	if hasMore {
		pageCandidates = pageCandidates[:limit]
	}
	if len(pageCandidates) == 0 {
		return response, nil
	}
	pagePins := make([]string, 0, len(pageCandidates))
	seenPagePins := make(map[string]bool, len(pageCandidates))
	for _, candidate := range pageCandidates {
		id := candidate.reference.execution.ID
		if !seenPagePins[id] {
			pagePins = append(pagePins, id)
			seenPagePins[id] = true
		}
	}
	page := make([]constructionInputEntry, 0, len(pageCandidates))
	err = c.reader.WithExecutionReadPins(ctx, pagePins, func(scanCtx context.Context) error {
		for _, candidate := range pageCandidates {
			reference := candidate.reference
			materialization, err := c.reader.ExactExecutionMaterialization(scanCtx, reference.execution.ID, reference.output.Name)
			if err != nil {
				return fmt.Errorf("resolve exact published construction input %q/%q: %w", reference.execution.ID, reference.output.Name, err)
			}
			if err := validateConstructionInputMaterialization(materialization, reference, project, snapshot.Identity.Generation); err != nil {
				return fmt.Errorf("exact published construction input identity changed: %w", err)
			}
			persistedScope, err := persistedMaterializationScope(materialization)
			if err != nil {
				return fmt.Errorf("exact published construction input authorization metadata is invalid: %w", err)
			}
			persistedBindings := recipe.RuntimeBindings{
				Project: materialization.Project, DatasetGeneration: materialization.DatasetGeneration,
				AuthScopeMode: persistedScope.Mode, AuthResourcePaths: append([]string(nil), persistedScope.AuthResourcePaths...),
			}
			if materialization.ScopeDigest != recipeScopeDigest(persistedBindings) {
				return errors.New("exact published construction input authorization digest changed")
			}
			effectiveScope, err := c.effectiveConstructionInputScope(scanCtx, persistedScope, materialization)
			if err != nil {
				return fmt.Errorf("reauthorize exact published construction input: %w", err)
			}
			if effectiveScope.Mode == authscope.ReadScopeRestricted && len(effectiveScope.AuthResourcePaths) == 0 || !sameClickHouseReadScope(effectiveScope, scope) {
				return explorerConflict("construction-inputs", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil)
			}
			if _, err := resolvedClickHouseColumns(materialization.Columns); err != nil {
				return fmt.Errorf("exact published construction input schema is invalid: %w", err)
			}
			columns := constructionInputColumns(materialization.Columns, candidate.presentation.columnLabels)
			if len(columns) == 0 {
				return errors.New("exact published construction input has no stable public columns")
			}
			pointer, err := c.catalog.GetPointer(scanCtx, reference.execution.PointerName())
			isCurrent := err == nil && pointer.ExecutionID == reference.execution.ID
			if err != nil && !errors.Is(err, publication.ErrBundleNotFound) {
				return fmt.Errorf("resolve current marker for exact published input %q: %w", reference.execution.ID, err)
			}
			entry := constructionInputEntry{
				Kind: "TABLE_REVISION", TableID: materialization.Selector.Key(), RevisionID: materialization.Revision,
				OutputID: materialization.Selector.Output, TableTitle: candidate.presentation.tableTitle,
				OutputTitle: candidate.presentation.outputTitle, RowMeaning: candidate.presentation.rowMeaning,
				IsCurrent: isCurrent, CreatedAt: materialization.CreatedAt, Columns: columns,
			}
			page = append(page, entry)
		}
		return nil
	})
	if err != nil {
		var authoringErr *explorer.AuthoringError
		if errors.As(err, &authoringErr) {
			return constructionInputsResponse{}, err
		}
		return constructionInputsResponse{}, constructionInputsUnavailable(err)
	}
	if hasMore {
		last := page[len(page)-1]
		response.NextCursor, err = encodeConstructionInputsCursor(query, request.SnapshotToken, request.ExpectedDraftVersion, request.ExpectedDraftDigest, last)
		if err != nil {
			return constructionInputsResponse{}, fmt.Errorf("encode construction input catalog cursor: %w", err)
		}
	}
	response.Entries = page
	return response, nil
}

func (c constructionInputsCatalog) authorizedSnapshot(ctx context.Context, project, explorerID string, request constructionInputsRequest) (*explorer.Explorer, capability.Snapshot, authscope.ReadScope, error) {
	authorized, err := c.capabilities.ForCompilation(ctx, project, request.SnapshotToken)
	if err != nil {
		if errors.Is(err, capability.ErrStaleSnapshot) {
			return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil)
		}
		if errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) || errors.Is(err, capability.ErrSnapshotUnavailable) {
			return nil, capability.Snapshot{}, authscope.ReadScope{}, constructionInputsUnavailable(err)
		}
		if errors.Is(err, authscope.ErrForbidden) || errors.Is(err, authscope.ErrUnauthenticated) {
			return nil, capability.Snapshot{}, authscope.ReadScope{}, &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err}
		}
		return nil, capability.Snapshot{}, authscope.ReadScope{}, constructionInputsUnavailable(err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != project {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil)
	}
	owner, err := c.explorers.Get(ctx, project, explorerID)
	if err != nil {
		if !errors.Is(err, explorer.ErrNotFound) {
			return nil, capability.Snapshot{}, authscope.ReadScope{}, constructionInputsUnavailable(err)
		}
		return nil, capability.Snapshot{}, authscope.ReadScope{}, err
	}
	if owner == nil || owner.ExplorerID != explorerID || projectid.Canonical(owner.Project) != project {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorer.ErrNotFound
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting construction inputs", nil)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be verified", nil)
	}
	draftDigest, err := workspace.Digest()
	if err != nil || draftDigest != owner.DraftDigest {
		return nil, capability.Snapshot{}, authscope.ReadScope{}, explorerConflict("construction-inputs", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil)
	}
	return owner, snapshot, authorized.Scope.Clone(), nil
}

func validateConstructionInputsRequest(request constructionInputsRequest) (string, int, *constructionInputsCursor, error) {
	for name, value := range map[string]string{
		"snapshotToken": request.SnapshotToken, "expectedDraftDigest": request.ExpectedDraftDigest,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return "", 0, nil, fmt.Errorf("%s is required and must not contain surrounding whitespace", name)
		}
	}
	if request.ExpectedDraftVersion < 1 {
		return "", 0, nil, errors.New("expectedDraftVersion must be positive")
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	if len(query) > constructionInputsMaxQuery {
		return "", 0, nil, fmt.Errorf("query must be at most %d characters", constructionInputsMaxQuery)
	}
	limit := request.Limit
	if limit == 0 {
		limit = constructionInputsDefaultLimit
	}
	if limit < 1 || limit > constructionInputsMaxLimit {
		return "", 0, nil, fmt.Errorf("limit must be between 1 and %d", constructionInputsMaxLimit)
	}
	if request.Cursor == "" {
		return query, limit, nil, nil
	}
	if len(request.Cursor) > 2048 {
		return "", 0, nil, errors.New("cursor is too long")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
	if err != nil {
		return "", 0, nil, errors.New("cursor is invalid")
	}
	var cursor constructionInputsCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != 1 || cursor.Query != query ||
		cursor.SnapshotToken != request.SnapshotToken || cursor.DraftVersion != request.ExpectedDraftVersion || cursor.DraftDigest != request.ExpectedDraftDigest ||
		cursor.TableID == "" || cursor.OutputID == "" || cursor.RevisionID == "" {
		return "", 0, nil, errors.New("cursor does not match this catalog query")
	}
	return query, limit, &cursor, nil
}

func (c constructionInputsCatalog) effectiveConstructionInputScope(ctx context.Context, persisted authscope.ReadScope, materialization published.Materialization) (authscope.ReadScope, error) {
	if c.scopes == nil {
		return persisted, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	return c.scopes.ResolveReadScopeForGeneration(ctx, principal, materialization.Project, materialization.DatasetGeneration, persisted.AuthResourcePaths)
}

func validateConstructionInputMaterialization(materialization published.Materialization, reference constructionInputReference, project, generation string) error {
	if materialization.State != published.StateReady || materialization.Revision != reference.execution.ID || materialization.Selector.Key() != reference.tableID || materialization.Selector.Output != reference.output.Name {
		return errors.New("exact table revision identity changed")
	}
	if projectid.Canonical(materialization.Project) != project || materialization.DatasetGeneration != generation {
		return errors.New("exact table revision project or generation changed")
	}
	if strings.TrimSpace(materialization.ReceiptID) == "" || strings.TrimSpace(materialization.SchemaDigest) == "" || strings.TrimSpace(materialization.ScopeDigest) == "" || strings.TrimSpace(materialization.PhysicalTable) == "" {
		return errors.New("exact table revision has incomplete immutable metadata")
	}
	return nil
}

func constructionInputMaterialization(reference constructionInputReference) published.Materialization {
	execution, output := reference.execution, reference.output
	selector := output.Selector
	if !selector.Valid() {
		selector = execution.Selector(output.Name)
	}
	name := output.Name
	if output.SourceRow.Valid() {
		name = output.SourceRow.ResourceType
	}
	columns := make([]published.Column, len(output.Columns))
	for index, column := range output.Columns {
		columns[index] = published.Column{
			ID: column.ID, Name: column.Name, SemanticPath: column.SemanticPath,
			ClickHouse: column.ClickHouse, LogicalType: column.LogicalType,
			Nullable: column.Nullable, Repeated: column.Repeated, LoomOwned: column.LoomOwned,
		}
	}
	return published.Materialization{
		ID: execution.ID + ":" + output.Name, Name: name, Revision: execution.ID,
		ReceiptID: execution.ReceiptID, SchemaDigest: execution.SchemaDigest, ScopeDigest: execution.ScopeDigest,
		AuthScopeMode: execution.AuthScopeMode, Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		State: published.StateReady, ScopeUnrestricted: len(execution.AuthResourcePaths) == 0,
		AuthResourcePaths: append([]string(nil), execution.AuthResourcePaths...), Columns: columns,
		PhysicalTable: output.PhysicalTable, RowCount: output.RowCount,
		CreatedAt: execution.CreatedAt, UpdatedAt: execution.UpdatedAt, ReadyAt: execution.ReadyAt,
		Selector: selector, SourceRow: output.SourceRow,
	}
}

func (c constructionInputsCatalog) constructionInputPublicationMetadata(ctx context.Context, execution publication.BundleExecution, project string) (*constructionInputPublicationMetadata, error) {
	if c.revisions == nil || !strings.HasPrefix(execution.ReceiptID, "receipt_") {
		return nil, nil
	}
	revisionID := "authoring_" + strings.TrimPrefix(execution.ReceiptID, "receipt_")
	revision, err := c.revisions.GetRevision(ctx, revisionID)
	if errors.Is(err, explorer.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve immutable source presentation for published revision %q: %w", execution.ID, err)
	}
	if revision == nil || revision.ID != revisionID || projectid.Canonical(revision.Project) != project || revision.CompilationReceiptID != execution.ReceiptID ||
		revision.SourceGeneration != execution.DatasetGeneration || revision.Publication.ExecutionID != execution.ID ||
		(revision.Status != explorer.RevisionReady && revision.Status != explorer.RevisionActive && revision.Status != explorer.RevisionSuperseded) {
		return nil, fmt.Errorf("immutable source presentation does not match published revision %q", execution.ID)
	}
	contracts, err := explorer.DecodePublicOutputContracts(revision.PublicOutputContract)
	if err != nil {
		return nil, fmt.Errorf("decode immutable source presentation for published revision %q: %w", execution.ID, err)
	}
	if err := contracts.ValidateAgainst(revision.Recipe, revision.EmittedColumns); err != nil {
		return nil, fmt.Errorf("validate immutable source presentation for published revision %q: %w", execution.ID, err)
	}
	var workspace struct {
		Explorer struct {
			Title string `json:"title"`
		} `json:"explorer"`
		Documents []struct {
			Output struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"output"`
			RootResourceType string `json:"rootResourceType"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(revision.AuthoringBundle, &workspace); err != nil {
		return nil, fmt.Errorf("decode immutable authoring labels for published revision %q: %w", execution.ID, err)
	}
	outputs := make(map[string]constructionInputOutputPresentation, len(workspace.Documents))
	for _, document := range workspace.Documents {
		outputID := strings.TrimSpace(document.Output.ID)
		if outputID == "" {
			return nil, fmt.Errorf("immutable authoring bundle has an output without an ID for published revision %q", execution.ID)
		}
		if _, duplicate := outputs[outputID]; duplicate {
			return nil, fmt.Errorf("immutable authoring bundle repeats output %q", outputID)
		}
		outputs[outputID] = constructionInputOutputPresentation{title: document.Output.Title, rootResourceType: document.RootResourceType}
	}
	return &constructionInputPublicationMetadata{tableTitle: workspace.Explorer.Title, outputs: outputs, contracts: contracts}, nil
}

func constructionInputPresentationFor(materialization published.Materialization, metadata *constructionInputPublicationMetadata) (constructionInputPresentation, error) {
	presentation := constructionInputPresentation{
		tableTitle: materialization.Selector.Recipe, outputTitle: materialization.Selector.Output,
		rowMeaning: constructionInputRowMeaning(materialization), columnLabels: nil,
	}
	if metadata == nil {
		return presentation, nil
	}
	document, documentFound := metadata.outputs[materialization.Selector.Output]
	contract, ok := metadata.contracts.Output(materialization.Selector.Output)
	if !ok || !documentFound {
		return constructionInputPresentation{}, fmt.Errorf("immutable source presentation is incomplete for output %q", materialization.Selector.Output)
	}
	labels := make(map[string]string, len(contract.Columns))
	for _, column := range contract.Columns {
		if strings.TrimSpace(column.Column) == "" || strings.TrimSpace(column.Label) == "" {
			continue
		}
		labels[column.Column] = column.Label
	}
	if strings.TrimSpace(metadata.tableTitle) != "" {
		presentation.tableTitle = metadata.tableTitle
	}
	if strings.TrimSpace(document.title) != "" {
		presentation.outputTitle = document.title
	}
	if strings.TrimSpace(presentation.rowMeaning) == "" && strings.TrimSpace(document.rootResourceType) != "" {
		presentation.rowMeaning = document.rootResourceType
	}
	presentation.columnLabels = labels
	return presentation, nil
}

func constructionInputColumns(columns []published.Column, labels map[string]string) []constructionInputColumn {
	result := make([]constructionInputColumn, 0, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column.ID) == "" {
			continue
		}
		capabilities := columncapabilities.FromClickHouse(column.ClickHouse)
		logicalType := column.LogicalType
		if logicalType == "" {
			logicalType = capabilities.Logical
		}
		label := column.Name
		if pinnedLabel := labels[column.Name]; strings.TrimSpace(pinnedLabel) != "" {
			label = pinnedLabel
		}
		result = append(result, constructionInputColumn{
			ID: column.ID, Name: column.Name, Label: label, Type: logicalType,
			ClickHouseType: column.ClickHouse, Nullable: column.Nullable || capabilities.Nullable,
			Repeated: column.Repeated || capabilities.Repeated, SemanticPath: column.SemanticPath,
		})
	}
	return result
}

func constructionInputRowMeaning(materialization published.Materialization) string {
	if materialization.SourceRow != nil && materialization.SourceRow.Valid() {
		return materialization.SourceRow.ResourceType
	}
	return materialization.Name
}

func constructionInputMatchesQuery(entry constructionInputEntry, query string) bool {
	if query == "" {
		return true
	}
	values := []string{entry.TableTitle, entry.OutputTitle, entry.RowMeaning, entry.TableID, entry.RevisionID, entry.OutputID}
	for _, column := range entry.Columns {
		values = append(values, column.Name, column.Label, column.SemanticPath)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func constructionInputAfterCursor(entry constructionInputEntry, cursor constructionInputsCursor) bool {
	if entry.TableID != cursor.TableID {
		return entry.TableID > cursor.TableID
	}
	if entry.OutputID != cursor.OutputID {
		return entry.OutputID > cursor.OutputID
	}
	return entry.RevisionID > cursor.RevisionID
}

func encodeConstructionInputsCursor(query, snapshotToken string, draftVersion int64, draftDigest string, entry constructionInputEntry) (string, error) {
	value, err := json.Marshal(constructionInputsCursor{
		Version: 1, Query: query, SnapshotToken: snapshotToken, DraftVersion: draftVersion, DraftDigest: draftDigest,
		TableID: entry.TableID, OutputID: entry.OutputID, RevisionID: entry.RevisionID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func constructionInputsUnavailable(err error) error {
	return &explorer.AuthoringError{
		Status:     503,
		Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "construction-inputs", Code: "AUTHORING_UNAVAILABLE", Message: "published construction inputs are unavailable"},
		Cause:      err,
	}
}

func (h *explorerHTTPHandlers) getConstructionInputsDirect(ctx context.Context, project, explorerID string, body *constructionInputsRequest) (constructionInputsResponse, error) {
	var result constructionInputsResponse
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, malformedRouteError("construction-inputs", errors.New("request body is required"))
	}
	return h.constructionInputs.list(ctx, project, explorerID, *body)
}

var _ constructionInputsExactReader = (*published.Reader)(nil)
var _ constructionInputsCatalogReader = (publication.BundleCatalog)(nil)
var _ constructionInputsExplorerReader = (*explorer.Service)(nil)
