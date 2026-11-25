import { useMemo, useRef, useState } from 'react';
import { useQuery } from '../../../react';
import { semanticConceptLabel } from '../model/catalogItems';
import { codedCategoryKey, codedFamilyKey, type GroupCodedCategory, type GroupForm } from '../model/reshapeForms';
import type { FrameSourceOption, SemanticInventoryBrowseResponse, SemanticInventoryItem } from '../../../contracts/types';
import type { CodedPivotContext } from './codedPivotContext';

const INITIAL_VISIBLE_CODED_CHOICES = 10;
const CODED_INVENTORY_READINESS_POLL_INTERVAL_MS = 1_000;

const waitForCodedInventoryReadinessPoll = (signal: AbortSignal): Promise<void> => new Promise((resolve) => {
  if (signal.aborted) {
    resolve();
    return;
  }
  let timer: number | undefined;
  const finish = () => {
    if (timer !== undefined) window.clearTimeout(timer);
    signal.removeEventListener('abort', finish);
    resolve();
  };
  timer = window.setTimeout(finish, CODED_INVENTORY_READINESS_POLL_INTERVAL_MS);
  signal.addEventListener('abort', finish, { once: true });
});

const availableGroupCodedEntry = (
  item: SemanticInventoryItem,
  sources: ReadonlyArray<FrameSourceOption>,
) => {
  const source = sources.find((candidate) => candidate.resourceType === item.resourceType
    && candidate.bindingId === item.bindingId && candidate.sourcePath === item.sourcePath);
  const semanticSource = item.constructionChoice?.source;
  if (!source || !item.code || item.codingVersion || !item.constructionChoice || item.constructionChoice.route.length !== 0
      || semanticSource?.kind !== 'SEMANTIC' || semanticSource.resourceType !== source.resourceType
      || semanticSource.bindingId !== source.bindingId || semanticSource.sourcePath !== source.sourcePath
      || semanticSource.code !== item.code || (semanticSource.system ?? '') !== item.system
      || item.readiness.status !== 'READY' && item.readiness.status !== 'READY_WITH_WARNING') return undefined;
  return { item, source, familyKey: codedFamilyKey(source.bindingId, source.sourcePath) };
};

type GroupCodedSourceDiscovery = {
  readonly scopeKey: string;
  readonly sources: ReadonlyArray<FrameSourceOption>;
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly loading: boolean;
};

type GroupCodedInventoryReadiness = {
  readonly scopeKey: string;
  readonly contextToken: string;
  readonly buildId: string;
  readonly state: SemanticInventoryBrowseResponse['state'];
};

type GroupCodedInventoryFamily = {
  readonly source: FrameSourceOption;
  readonly entries: SemanticInventoryItem[];
  readonly seenCursors: Set<string>;
  started: boolean;
  cursor?: string;
  exhausted: boolean;
};

type GroupCodedInventoryCheckpoint = {
  readonly scopeKey: string;
  readonly families: Map<string, GroupCodedInventoryFamily>;
  contextToken?: string;
  buildId?: string;
  initialized: boolean;
  loadedRound: number;
};

type GroupCodedInventoryView = {
  readonly scopeKey: string;
  readonly sourceKey: string;
  readonly entries: ReadonlyArray<SemanticInventoryItem>;
  readonly hasMore: boolean;
  readonly complete: boolean;
};

export function useGroupCodedInventory(props: {
  readonly context: CodedPivotContext;
  readonly form: GroupForm;
  readonly onChange: (form: GroupForm) => void;
}) {
  const { client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot } = props.context;
  const [pageRound, setPageRound] = useState(0);
  const [sourceProgress, setSourceProgress] = useState<GroupCodedSourceDiscovery>();
  const [inventoryProgress, setInventoryProgress] = useState<GroupCodedInventoryView>();
  const [readinessProgress, setReadinessProgress] = useState<GroupCodedInventoryReadiness>();
  const checkpointRef = useRef<GroupCodedInventoryCheckpoint | undefined>(undefined);
  const requestedRoundRef = useRef(0);
  const currentForm = useRef(props.form);
  currentForm.current = props.form;
  const changeForm = useRef(props.onChange);
  changeForm.current = props.onChange;

  const sourceScopeKey = JSON.stringify([project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot]);
  const readinessQuery = useQuery(async (signal) => {
    const browseReadiness = () => client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, rowRoot, limit: 1,
    }, signal);
    let response = await browseReadiness();
    while (response.state === 'running' && !signal.aborted) {
      setReadinessProgress({
        scopeKey: sourceScopeKey,
        contextToken: response.contextToken,
        buildId: response.buildId,
        state: response.state,
      });
      await waitForCodedInventoryReadinessPoll(signal);
      if (!signal.aborted) response = await browseReadiness();
    }
    return {
      scopeKey: sourceScopeKey,
      contextToken: response.contextToken,
      buildId: response.buildId,
      state: response.state,
    } satisfies GroupCodedInventoryReadiness;
  }, [client, sourceScopeKey]);
  const readiness = readinessQuery.data?.scopeKey === sourceScopeKey
    ? readinessQuery.data
    : readinessProgress?.scopeKey === sourceScopeKey ? readinessProgress : undefined;
  const inventoryReady = Boolean(readiness?.buildId && readiness.state === 'complete');
  const inventoryScopeKey = JSON.stringify([sourceScopeKey, readiness?.contextToken, readiness?.buildId]);
  const sourcesQuery = useQuery(async (signal) => {
    const sources = new Map<string, FrameSourceOption>();
    const seenCursors = new Set<string>();
    let cursor: string | undefined;
    let complete = false;
    let truncated = false;
    do {
      const response = await client.browseFrameSourceOptions({
        project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: rowRoot, limit: 50,
        ...(cursor ? { cursor } : {}),
      }, signal);
      if (response.snapshotToken !== snapshotToken || response.outputId !== outputId) {
        throw new Error('Coded source fields were returned for a different table snapshot. Reload the table and retry.');
      }
      for (const source of response.sources) {
        if (source.route.length === 0 && source.resourceType === rowRoot) sources.set(source.choiceId, source);
      }
      complete = response.complete;
      truncated = truncated || response.truncated;
      cursor = response.nextCursor;
      const progress = {
        scopeKey: inventoryScopeKey,
        sources: [...sources.values()],
        complete: complete && !cursor,
        truncated,
        loading: Boolean(cursor),
      } satisfies GroupCodedSourceDiscovery;
      if (!signal.aborted) setSourceProgress(progress);
      if (cursor && seenCursors.has(cursor)) throw new Error('Coded source fields could not be completely listed. Reopen the editor to retry.');
      if (cursor) seenCursors.add(cursor);
    } while (cursor && !signal.aborted);
    return {
      scopeKey: inventoryScopeKey,
      sources: [...sources.values()],
      complete: complete && !cursor,
      truncated,
      loading: false,
    } satisfies GroupCodedSourceDiscovery;
  }, [client, inventoryScopeKey], inventoryReady);

  const sourceDiscovery = sourcesQuery.data?.scopeKey === inventoryScopeKey
    ? sourcesQuery.data
    : sourceProgress?.scopeKey === inventoryScopeKey ? sourceProgress : undefined;
  const sources = sourceDiscovery?.sources ?? [];
  const sourceKey = JSON.stringify(sources.map((source) => [source.choiceId, source.bindingId, source.sourcePath]));
  const currentInventoryScopeKey = useRef(inventoryScopeKey);
  currentInventoryScopeKey.current = inventoryScopeKey;
  const inventoryQuery = useQuery(async (signal) => {
    let checkpoint = checkpointRef.current;
    if (!checkpoint || checkpoint.scopeKey !== inventoryScopeKey
        || checkpoint.contextToken !== readiness?.contextToken || checkpoint.buildId !== readiness?.buildId) {
      checkpoint = {
        scopeKey: inventoryScopeKey,
        families: new Map(),
        contextToken: readiness?.contextToken,
        buildId: readiness?.buildId,
        initialized: false,
        loadedRound: 0,
      };
      checkpointRef.current = checkpoint;
    }
    const currentCheckpoint = checkpoint;
    for (const source of sources) {
      const existing = currentCheckpoint.families.get(source.choiceId);
      if (existing && (existing.source.resourceType !== source.resourceType
          || existing.source.bindingId !== source.bindingId || existing.source.sourcePath !== source.sourcePath)) {
        throw new Error('The direct coded source changed while it was loading. Reload the table and retry.');
      }
      if (!existing) currentCheckpoint.families.set(source.choiceId, {
        source, entries: [], seenCursors: new Set<string>(), started: false, exhausted: false,
      });
    }
    const makeView = (): GroupCodedInventoryView => {
      const families = [...currentCheckpoint.families.values()];
      return {
        scopeKey: inventoryScopeKey,
        sourceKey,
        entries: families.flatMap((family) => family.entries),
        hasMore: families.some((family) => Boolean(family.cursor)),
        complete: sourceDiscovery?.complete === true && !sourceDiscovery.truncated && families.every((family) => family.exhausted),
      };
    };
    const publish = () => setInventoryProgress(makeView());
    const fetchPage = async (family: GroupCodedInventoryFamily) => {
      if (signal.aborted || family.exhausted) return;
      const response = await client.browseSemanticInventory({
        project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
        sourceChoiceId: family.source.choiceId,
        ...(family.cursor ? { cursor: family.cursor } : {}),
        limit: 50,
      }, signal);
      if (signal.aborted) return;
      if (response.state !== 'complete') {
        throw new Error(`Coded values for ${family.source.title} are not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.`);
      }
      if (currentCheckpoint.contextToken !== response.contextToken
          || currentCheckpoint.buildId !== response.buildId) {
        throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
      }
      family.entries.push(...response.entries.filter((item) => item.resourceType === family.source.resourceType
        && item.bindingId === family.source.bindingId && item.sourcePath === family.source.sourcePath));
      family.cursor = response.nextCursor;
      family.exhausted = !response.nextCursor;
      family.started = true;
      if (response.nextCursor) {
        if (family.seenCursors.has(response.nextCursor)) {
          throw new Error(`Coded values for ${family.source.title} could not be completely loaded. Reopen the editor to retry.`);
        }
        family.seenCursors.add(response.nextCursor);
      }
    };
    const unstarted = [...currentCheckpoint.families.values()].filter((family) => !family.started);
    if (unstarted.length > 0) {
      await Promise.all(unstarted.map(fetchPage));
      if (signal.aborted) return makeView();
      publish();
    }
    if (!currentCheckpoint.initialized || unstarted.length > 0) {
      while (!signal.aborted) {
        const visibleKeys = new Set(makeView().entries.flatMap((item) => {
          const entry = availableGroupCodedEntry(item, sources);
          return entry ? [codedCategoryKey(entry.familyKey, item.system, item.code)] : [];
        }));
        if (visibleKeys.size >= INITIAL_VISIBLE_CODED_CHOICES) break;
        const pending = [...currentCheckpoint.families.values()].filter((family) => !family.exhausted);
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        publish();
      }
      if (signal.aborted) return makeView();
      currentCheckpoint.initialized = true;
      currentCheckpoint.loadedRound = pageRound;
    } else if (pageRound > currentCheckpoint.loadedRound) {
      for (let round = currentCheckpoint.loadedRound; round < pageRound && !signal.aborted; round += 1) {
        const pending = [...currentCheckpoint.families.values()].filter((family) => Boolean(family.cursor));
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        currentCheckpoint.loadedRound = round + 1;
        publish();
      }
    }
    return makeView();
  }, [client, inventoryScopeKey, readiness?.contextToken, readiness?.buildId, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.truncated, sourceDiscovery?.loading, pageRound],
  inventoryReady && ((sourceDiscovery !== undefined && sources.length > 0) || sourceDiscovery?.complete === true));

  const inventoryView = inventoryQuery.data?.scopeKey === inventoryScopeKey && inventoryQuery.data.sourceKey === sourceKey
    ? inventoryQuery.data
    : inventoryProgress?.scopeKey === inventoryScopeKey && inventoryProgress.sourceKey === sourceKey ? inventoryProgress : undefined;
  const inventory = inventoryView?.entries ?? [];
  const available = useMemo(() => {
    const entries = new Map<string, NonNullable<ReturnType<typeof availableGroupCodedEntry>>>();
    for (const item of inventory) {
      const entry = availableGroupCodedEntry(item, sources);
      if (entry) entries.set(codedCategoryKey(entry.familyKey, item.system, item.code), entry);
    }
    return [...entries.values()];
  }, [inventory, sources]);

  const savedCategoryKey = props.form.codedCategories.filter((category) => category.choice === 'saved')
    .map((category) => category.key).join('\u0001');
  const savedCategoryQuery = useQuery(async (signal) => {
    const unresolved = currentForm.current.codedCategories.filter((category) => category.choice === 'saved');
    const hydrated = await Promise.all(unresolved.map(async (category) => {
      const source = sources.find((candidate) => candidate.resourceType === rowRoot
        && codedFamilyKey(candidate.bindingId, candidate.sourcePath) === category.familyKey);
      if (!source) return { key: category.key, value: undefined, pending: !sourceDiscovery?.complete };
      try {
        const response = await client.browseSemanticInventory({
          project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
          sourceChoiceId: source.choiceId, query: category.code, limit: 50,
        }, signal);
        if (response.state !== 'complete') {
          throw new Error('The saved coded value is not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.');
        }
        if (response.contextToken !== readiness?.contextToken || response.buildId !== readiness?.buildId) {
          throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
        }
        const item = response.entries.find((candidate) => candidate.system === category.system && candidate.code === category.code
          && availableGroupCodedEntry(candidate, [source]));
        return { key: category.key, value: item?.constructionChoice ? {
          sourceChoiceId: source.choiceId,
          categoryChoiceId: item.constructionChoice.choiceId,
          display: semanticConceptLabel(item),
          valueType: item.valueType,
        } : undefined, pending: false };
      } catch (reason: unknown) {
        return {
          key: category.key,
          value: undefined,
          pending: false,
          error: reason instanceof Error ? reason.message : 'A saved coded value could not be reauthorized.',
        };
      }
    }));
    if (signal.aborted || currentInventoryScopeKey.current !== inventoryScopeKey) return { unavailableKeys: [], error: undefined };
    const byKey = new Map(hydrated.flatMap((item) => item.value ? [[item.key, item.value] as const] : []));
    const form = currentForm.current;
    const savedKeys = new Set(form.codedCategories.filter((category) => category.choice === 'saved').map((category) => category.key));
    let changed = false;
    const codedCategories = form.codedCategories.map((category): GroupCodedCategory => {
      if (category.choice !== 'saved') return category;
      const item = byKey.get(category.key);
      if (!item) return category;
      changed = true;
      return {
        ...category,
        choice: 'ready',
        sourceChoiceId: item.sourceChoiceId,
        categoryChoiceId: item.categoryChoiceId,
        display: item.display,
        valueType: item.valueType,
      };
    });
    if (changed) changeForm.current({ ...form, codedCategories });
    return {
      unavailableKeys: hydrated.filter((item) => savedKeys.has(item.key) && !item.value && !item.pending).map((item) => item.key),
      error: hydrated.find((item) => item.error)?.error,
    };
  }, [client, inventoryScopeKey, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.loading, savedCategoryKey],
  inventoryReady && sourceDiscovery !== undefined && savedCategoryKey.length > 0);
  const unavailableSavedKeys = new Set(savedCategoryQuery.data?.unavailableKeys ?? []);

  const loadNextPage = () => {
    if (!inventoryView?.hasMore || inventoryQuery.isLoading || pageRound !== requestedRoundRef.current) return;
    requestedRoundRef.current = pageRound + 1;
    setPageRound(pageRound + 1);
  };
  const sourceListIncomplete = Boolean(sourceDiscovery && !sourceDiscovery.loading && (!sourceDiscovery.complete || sourceDiscovery.truncated));
  const error = readinessQuery.error ?? sourcesQuery.error ?? inventoryQuery.error ?? savedCategoryQuery.data?.error;
  const errorText = error instanceof Error ? error.message : error ? 'Coded values could not be loaded.' : undefined;
  const choicesLoading = readinessQuery.isLoading || sourcesQuery.isLoading || inventoryQuery.isLoading || savedCategoryQuery.isLoading;
  return {
    available, choicesLoading, errorText, inventoryReady, inventoryView, loadNextPage,
    readiness, readinessQuery, sourceListIncomplete, sources, unavailableSavedKeys,
  };
}
