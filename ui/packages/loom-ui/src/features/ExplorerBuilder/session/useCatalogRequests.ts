import { sameCatalogSnapshot } from '../../../contracts/identity';
import { useRef, useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import { RequestOwner } from '../../../data/requests';
import type {
  ConstructionChoice, ConstructionChoiceSearchSource, ExplorerBuilderCatalog, FieldPresentation,
  FieldPresentationRequest, SemanticInventoryBrowseResponse, SemanticInventorySelectionMode,
} from '../../../contracts/types';
import { catalogItemConstructionChoice, fieldPresentationMatchesRequest, type CatalogChoiceGroup, type CatalogItem } from '../model/catalogItems';

export const CATALOG_PAGE_SIZE = 50;
const ROUTE_PAGE_SIZE = 10;

type CatalogLoadState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready' }
  | { readonly status: 'error'; readonly message: string };

export type FieldPresentationCacheEntry = {
  readonly request: FieldPresentationRequest;
  readonly presentation: FieldPresentation;
  readonly inventoryBuildId: string;
  readonly readinessKey: string;
  readonly searchGeneration: number;
};

export const semanticBrowseReadinessKey = (response: SemanticInventoryBrowseResponse): string =>
  JSON.stringify([
    response.contextToken,
    response.buildId,
    response.state,
    response.sourceAvailability ?? '',
  ]);

interface CatalogPageNavigation {
  readonly scopeKey: string;
  readonly cursors: ReadonlyArray<string | undefined>;
  readonly index: number;
}

type CatalogRequest = NonNullable<ReturnType<RequestOwner['start']>>;

export function useCatalogRequests({
  project, explorerId, authResourcePath, snapshotToken, outputId, tableContextKey,
  rowRoot, catalog, presentationContext, presentationRequest, searchGeneration, viewRevision,
  browseResourceType, semanticSelectionMode, query, pageNavigation, view, browseRouteContext,
  onCatalogChanged,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly tableContextKey: string;
  readonly rowRoot: string;
  readonly catalog: ExplorerBuilderCatalog;
  readonly presentationContext?: Omit<FieldPresentationRequest, 'candidateIds'>;
  readonly presentationRequest?: FieldPresentationRequest;
  readonly searchGeneration: number;
  readonly viewRevision?: number;
  readonly browseResourceType?: string;
  readonly semanticSelectionMode?: SemanticInventorySelectionMode;
  readonly query: string;
  readonly pageNavigation?: CatalogPageNavigation;
  readonly view: 'all' | 'coded' | 'fields';
  readonly browseRouteContext?: { readonly occurrenceId: string };
  readonly onCatalogChanged: () => void;
}) {
  const client = useLoomClient();
  const [requests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity('feature-catalog');
    return owner;
  });
  const activeChoiceRequest = useRef<CatalogRequest | undefined>(undefined);
  const activeMoreRouteRequest = useRef<{
    readonly controller: CatalogRequest;
    readonly tableContextKey: string;
  } | undefined>(undefined);
  const selectionContext = useRef<string | undefined>(undefined);
  const semanticContextRef = useRef<{
    readonly scopeKey: string;
    readonly client: typeof client;
    readonly value?: { readonly contextToken: string; readonly buildId: string };
  }>({ scopeKey: '', client });
  const presentationScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    outputId,
    tableContextKey,
    rowRoot,
    catalog.authorizationScopeDigest ?? '',
    catalog.generation,
    presentationContext?.expectedDraftVersion ?? 0,
    presentationContext?.expectedDraftDigest ?? '',
    presentationContext?.sourceNodeId ?? '',
    presentationContext?.occurrenceId ?? '',
    presentationContext?.stageId ?? '',
  ]);
  const presentationRequestKey = presentationRequest
    ? JSON.stringify([presentationScopeKey, presentationRequest.candidateIds])
    : 'none';
  const presentationCache = useRef<{
    readonly client: typeof client;
    readonly scopeKey: string;
    readonly entries: Map<string, FieldPresentationCacheEntry>;
  }>({ client, scopeKey: presentationScopeKey, entries: new Map() });
  if (presentationCache.current.client !== client || presentationCache.current.scopeKey !== presentationScopeKey) {
    presentationCache.current = { client, scopeKey: presentationScopeKey, entries: new Map() };
  }
  const cachedPresentationEntry = presentationRequest
    ? presentationCache.current.entries.get(presentationRequestKey)
    : undefined;
  const cachedPresentation = cachedPresentationEntry && (
    cachedPresentationEntry.presentation.kind !== 'UNAVAILABLE' ||
    cachedPresentationEntry.searchGeneration === searchGeneration
  ) ? cachedPresentationEntry : undefined;
  const presentationRequestToSend = presentationRequest && !cachedPresentation
    ? presentationRequest
    : undefined;
  const semanticContextScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    outputId,
    rowRoot,
    semanticSelectionMode ?? '',
  ]);
  if (semanticContextRef.current.scopeKey !== semanticContextScopeKey || semanticContextRef.current.client !== client) {
    semanticContextRef.current = { scopeKey: semanticContextScopeKey, client };
  }
  const catalogRequestScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    tableContextKey,
    rowRoot,
    outputId,
    browseResourceType ?? '',
    semanticSelectionMode ?? '',
    viewRevision,
    query,
    searchGeneration,
    presentationRequestKey,
  ]);
  const currentPageNavigation = pageNavigation?.scopeKey === catalogRequestScopeKey
    ? pageNavigation
    : { scopeKey: catalogRequestScopeKey, cursors: [undefined], index: 0 };
  const cursor = currentPageNavigation.cursors[currentPageNavigation.index];
  const pageCache = useRef<{
    readonly client: typeof client;
    readonly scopeKey: string;
    readonly pages: Map<string, SemanticInventoryBrowseResponse>;
  }>({ client, scopeKey: catalogRequestScopeKey, pages: new Map() });
  if (pageCache.current.client !== client || pageCache.current.scopeKey !== catalogRequestScopeKey) {
    pageCache.current = { client, scopeKey: catalogRequestScopeKey, pages: new Map() };
  }
  const catalogPageCacheKey = JSON.stringify([catalogRequestScopeKey, cursor ?? null]);
  const currentCatalogRequestScopeKey = useRef(catalogRequestScopeKey);
  currentCatalogRequestScopeKey.current = catalogRequestScopeKey;
  const catalogQuery = useQuery(async (signal) => {
    if (signal.aborted) return undefined;
    const cached = pageCache.current.pages.get(catalogPageCacheKey);
    if (cached) return cached;
    const browseCurrentPage = (fieldPresentation?: FieldPresentationRequest) =>
      client.browseSemanticInventory({
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        rowRoot,
        outputId,
        selectionMode: semanticSelectionMode,
        resourceType: browseResourceType,
        query,
        cursor,
        limit: CATALOG_PAGE_SIZE,
        ...(fieldPresentation ? { fieldPresentation } : {}),
        requestId: `feature-catalog-${window.crypto.randomUUID()}`,
      }, signal);
    let response = await browseCurrentPage(presentationRequestToSend);
    let proofRequestForResponse = presentationRequestToSend;
    if (!signal.aborted && presentationRequest && cachedPresentation &&
      (cachedPresentation.inventoryBuildId !== response.buildId ||
        cachedPresentation.readinessKey !== semanticBrowseReadinessKey(response))) {
      if (presentationCache.current.scopeKey === presentationScopeKey) {
        presentationCache.current.entries.delete(presentationRequestKey);
      }
      response = await browseCurrentPage(presentationRequest);
      proofRequestForResponse = presentationRequest;
    }
    if (!signal.aborted && proofRequestForResponse && response.fieldPresentation &&
      presentationCache.current.scopeKey === presentationScopeKey) {
      if (response.fieldPresentation.kind === 'UNAVAILABLE' || fieldPresentationMatchesRequest({
        presentation: response.fieldPresentation,
        request: proofRequestForResponse,
        project,
        explorerId,
        snapshotToken,
        outputId,
        datasetGeneration: catalog.generation,
        inventoryBuildId: response.buildId,
        authorizationScopeDigest: catalog.authorizationScopeDigest,
      })) {
        presentationCache.current.entries.set(presentationRequestKey, {
          request: proofRequestForResponse,
          presentation: response.fieldPresentation,
          inventoryBuildId: response.buildId,
          readinessKey: semanticBrowseReadinessKey(response),
          searchGeneration,
        });
      }
    }
    if (!signal.aborted && currentCatalogRequestScopeKey.current === catalogRequestScopeKey) {
      if (selectionContext.current && selectionContext.current !== response.contextToken) {
        onCatalogChanged();
      }
      selectionContext.current = response.contextToken;
      pageCache.current.pages.set(catalogPageCacheKey, response);
    }
    return response;
  }, [
    authResourcePath,
    catalogPageCacheKey,
    client,
    cursor,
    explorerId,
    outputId,
    project,
    query,
    browseResourceType,
    rowRoot,
    presentationRequestKey,
    searchGeneration,
    snapshotToken,
  ], Boolean(snapshotToken && rowRoot && (view !== 'fields' || presentationRequestToSend)));
  const loadState: CatalogLoadState = !snapshotToken || !rowRoot
    ? { status: 'idle' }
    : catalogQuery.error
      ? { status: 'error', message: catalogQuery.error instanceof Error
        ? catalogQuery.error.message
        : 'Loom could not load the feature catalog.' }
      : catalogQuery.isLoading
        ? { status: 'loading' }
        : catalogQuery.data
          ? { status: 'ready' }
          : { status: 'idle' };
  const semanticResponse = catalogQuery.data ?? pageCache.current.pages.get(catalogPageCacheKey);
  if (semanticResponse) {
    semanticContextRef.current = {
      scopeKey: semanticContextScopeKey,
      client,
      value: { contextToken: semanticResponse.contextToken, buildId: semanticResponse.buildId },
    };
  }
  const resolveChoiceGroup = async (
    choicesForCurrentTable: (group: CatalogChoiceGroup) => ReadonlyArray<ConstructionChoice>,
    item: CatalogItem,
    signal?: AbortSignal,
    cursor?: string,
    semanticContext?: { readonly contextToken: string; readonly buildId: string },
  ): Promise<CatalogChoiceGroup> => {
    const existingChoice = catalogItemConstructionChoice(item);
    if (
      !cursor &&
      existingChoice &&
      !browseRouteContext &&
      existingChoice.source.resourceType === rowRoot &&
      existingChoice.route.length === 0
    ) {
      return { item, choices: [existingChoice], complete: true, truncated: false };
    }
    const retainedSemanticContext = semanticContextRef.current.scopeKey === semanticContextScopeKey && semanticContextRef.current.client === client
      ? semanticContextRef.current.value
      : undefined;
    if (item.kind === 'SEMANTIC' && !semanticResponse) {
      if (!semanticContext && !retainedSemanticContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
    }
    let source: ConstructionChoiceSearchSource;
    if (item.kind === 'FIELD') {
      source = { kind: 'FIELD', candidateId: item.candidate.candidateId };
    } else {
      const effectiveContext = semanticContext ?? (semanticResponse ? {
        contextToken: semanticResponse.contextToken,
        buildId: semanticResponse.buildId,
      } : retainedSemanticContext);
      if (!effectiveContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
      if (item.item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        if (!existingChoice) throw new Error('Loom did not provide a signed choice for this coded family.');
        source = {
          kind: 'SEMANTIC',
          contextToken: effectiveContext.contextToken,
          buildId: effectiveContext.buildId,
          conceptId: item.item.conceptId,
          bindingId: item.item.bindingId,
          selectionMode: item.item.selectionMode,
          choiceId: existingChoice.choiceId,
        };
      } else {
        source = {
          kind: 'SEMANTIC',
          contextToken: effectiveContext.contextToken,
          buildId: effectiveContext.buildId,
          conceptId: item.item.conceptId,
          bindingId: item.item.bindingId,
        };
      }
    }
    const choicesById = new Map<string, ConstructionChoice>();
    const seenCursors = new Set(cursor ? [cursor] : []);
    let pageCursor = cursor;
    let nextCursor: string | undefined;
    let resolved: Awaited<ReturnType<typeof client.searchConstructionChoices>> | undefined;
    let choices: ReadonlyArray<ConstructionChoice> = [];
    do {
      if (signal?.aborted) throw new Error('Construction choice search was cancelled.');
      resolved = await client.searchConstructionChoices({
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        outputId,
        ...(browseRouteContext ? { occurrenceId: browseRouteContext.occurrenceId } : {}),
        source,
        limit: ROUTE_PAGE_SIZE,
        ...(pageCursor ? { cursor: pageCursor } : {}),
        requestId: `construction-choices-${window.crypto.randomUUID()}`,
      }, signal);
      if (
        !sameCatalogSnapshot(resolved.snapshotToken, snapshotToken) ||
        resolved.outputId !== outputId
      ) {
        throw new Error('Loom returned construction choices for another table or catalog snapshot.');
      }
      for (const choice of resolved.choices) {
        if (!choicesById.has(choice.choiceId)) choicesById.set(choice.choiceId, choice);
      }
      choices = [...choicesById.values()];
      nextCursor = resolved.nextCursor;
      const usableChoices = item.kind === 'FIELD'
        ? choicesForCurrentTable({
            item,
            choices,
            complete: resolved.complete,
            truncated: resolved.truncated,
            ...(resolved.nextCursor ? { nextCursor: resolved.nextCursor } : {}),
          })
        : choices;
      if (usableChoices.length > 0) {
        if (nextCursor && seenCursors.has(nextCursor)) nextCursor = undefined;
        break;
      }
      if (nextCursor && seenCursors.has(nextCursor)) {
        throw new Error('Construction choice pagination repeated a page.');
      }
      if (!nextCursor) break;
      seenCursors.add(nextCursor);
      pageCursor = nextCursor;
    } while (nextCursor);
    if (!resolved) throw new Error('Loom did not return construction choices.');
    const finalGroup: CatalogChoiceGroup = {
      item,
      choices,
      complete: resolved.complete,
      truncated: resolved.truncated,
      ...(nextCursor ? { nextCursor } : {}),
    };
    if (!resolved.complete && !nextCursor && choicesForCurrentTable(finalGroup).length === 0) {
      throw new Error('Loom could not finish checking construction choices.');
    }
    return {
      item,
      choices,
      complete: resolved.complete,
      truncated: resolved.truncated,
      ...(nextCursor ? { nextCursor } : {}),
      ...(item.kind === 'SEMANTIC' ? {
        semanticContext: semanticContext ?? (semanticResponse ? {
          contextToken: semanticResponse.contextToken,
          buildId: semanticResponse.buildId,
        } : retainedSemanticContext),
      } : {}),
    };
  };

  return {
    presentationScopeKey, presentationRequestKey, presentationCache, cachedPresentation,
    catalogRequestScopeKey, pageIndex: currentPageNavigation.index, semanticResponse, loadState,
    resolveChoiceGroup, activeChoiceRequest, activeMoreRouteRequest,
    startChoiceRequest: () => requests.start('choice', 'feature-catalog'),
    startContinuation: () => requests.start('continuation', 'feature-catalog'),
  };
}
