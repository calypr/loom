import { useMemo, useRef, useState, useSyncExternalStore } from 'react';
import type { LoomClient } from '../../../client/api';
import { createQueryResource } from '../../../data/queryResource';
import { useQuery } from '../../../react';
import type { FrameSourceOption, SemanticInventoryBrowseResponse, SemanticInventoryItem } from '../../../contracts/types';

export type CodedPivotTransport = Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>;

interface CodedPivotScope {
  readonly client: CodedPivotTransport;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
}

type CodedValuePage = {
  readonly entries: ReadonlyArray<SemanticInventoryItem>;
  readonly nextCursor?: string;
  readonly state: SemanticInventoryBrowseResponse['state'];
  readonly sourceAvailability: SemanticInventoryBrowseResponse['sourceAvailability'];
  readonly error?: string;
};
export const isAvailableCodedValue = (item: SemanticInventoryItem) =>
  Boolean(item.code) && !item.codingVersion && Boolean(item.constructionChoice) &&
  (item.readiness.status === 'READY' || item.readiness.status === 'READY_WITH_WARNING');

export function useCodedPivotSources({
  client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
  onSources,
}: CodedPivotScope & { readonly onSources: (sources: ReadonlyArray<FrameSourceOption>) => void }) {
  const [sourceQuery, setSourceQuery] = useState('');
  const [sourceCursor, setSourceCursor] = useState<string>();
  const [sourceNextCursor, setSourceNextCursor] = useState<string>();
  const [sources, setSources] = useState<ReadonlyArray<FrameSourceOption>>([]);
  const onSourcesRef = useRef(onSources);
  onSourcesRef.current = onSources;
  const resource = useMemo(() => {
    let observing = false;
    const query = createQueryResource(async (signal) => {
      try {
        const response = await client.browseFrameSourceOptions({
          project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: rowRoot,
          ...(sourceQuery ? { query: sourceQuery } : {}),
          ...(sourceCursor ? { cursor: sourceCursor } : {}), limit: 50,
        }, signal);
        if (!observing || signal.aborted) return;
        const direct = response.sources.filter((option) => option.route.length === 0 && option.resourceType === rowRoot);
        setSources((current) => sourceCursor ? [...current, ...direct] : direct);
        setSourceNextCursor(response.nextCursor);
        onSourcesRef.current(direct);
      } catch (error) {
        throw error instanceof Error ? error : new Error('Coded sources could not be loaded.');
      }
    });
    return {
      getSnapshot: query.getSnapshot,
      subscribe(listener: () => void) {
        observing = true;
        const unsubscribe = query.subscribe(listener);
        return () => {
          observing = false;
          unsubscribe();
        };
      },
    };
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, sourceQuery, sourceCursor]);
  const query = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return {
    sources, sourceQuery, sourceNextCursor, setSourceCursor,
    sourcesLoading: query.loading,
    sourceError: query.error instanceof Error ? query.error.message
      : query.error ? 'Coded sources could not be loaded.' : undefined,
    searchSources(next: string) {
      setSourceQuery(next);
      setSourceCursor(undefined);
      setSourceNextCursor(undefined);
    },
  };
}

export function useCodedPivotCategories({
  client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
  categorySourceChoiceId, categoryQuery, categoryCursor, categorySeed,
}: CodedPivotScope & {
  readonly categorySourceChoiceId?: string;
  readonly categoryQuery: string;
  readonly categoryCursor?: string;
  readonly categorySeed: ReadonlyArray<SemanticInventoryItem>;
}) {
  const categoryPage = useQuery(async (signal) => {
    if (!categorySourceChoiceId) return undefined;
    const page = await client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
      sourceChoiceId: categorySourceChoiceId,
      ...(categoryQuery ? { query: categoryQuery } : {}),
      ...(categoryCursor ? { cursor: categoryCursor } : {}), limit: 50,
    }, signal);
    return {
      ...page,
      entries: [...(categoryCursor ? categorySeed : []), ...page.entries],
    };
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
    categorySourceChoiceId, categoryQuery, categoryCursor, categorySeed], Boolean(categorySourceChoiceId));
  const categoryBasePage = categoryPage.data;
  const categoryNeedsContinuation = Boolean(categoryBasePage?.nextCursor
    && categoryBasePage.entries.filter(isAvailableCodedValue).length < 50);
  const categoryContinuation = useQuery<CodedValuePage>(async (signal) => {
    if (!categoryBasePage || !categorySourceChoiceId) {
      return { entries: [], state: 'unknown', sourceAvailability: 'unknown' };
    }
    const basePage = categoryBasePage;
    const sourceChoiceId = categorySourceChoiceId;
    const entries = [...basePage.entries];
    let cursor = basePage.nextCursor;
    let state = basePage.state;
    let sourceAvailability = basePage.sourceAvailability;
    const seenCursors = new Set<string>();
    while (cursor && entries.filter(isAvailableCodedValue).length < 50) {
      if (signal.aborted) throw new DOMException('The inventory request was aborted.', 'AbortError');
      if (seenCursors.has(cursor)) {
        return {
          entries, state, sourceAvailability,
          error: 'The coded-value catalog repeated a page cursor.',
        } satisfies CodedValuePage;
      }
      seenCursors.add(cursor);
      let page: SemanticInventoryBrowseResponse;
      try {
        page = await client.browseSemanticInventory({
          project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
          sourceChoiceId,
          ...(categoryQuery ? { query: categoryQuery } : {}), cursor, limit: 50,
        }, signal);
      } catch (error) {
        if (signal.aborted) throw error;
        return {
          entries, state, sourceAvailability,
          error: error instanceof Error ? error.message : 'Coded values could not be loaded.',
        } satisfies CodedValuePage;
      }
      if (signal.aborted) throw new DOMException('The inventory request was aborted.', 'AbortError');
      entries.push(...page.entries);
      state = page.state;
      sourceAvailability = page.sourceAvailability;
      if (page.nextCursor && seenCursors.has(page.nextCursor)) {
        return {
          entries, state, sourceAvailability,
          error: 'The coded-value catalog repeated a page cursor.',
        } satisfies CodedValuePage;
      }
      cursor = page.nextCursor;
    }
    return {
      entries, state, sourceAvailability,
      ...(cursor ? { nextCursor: cursor } : {}),
    } satisfies CodedValuePage;
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
    categorySourceChoiceId, categoryQuery, categoryBasePage], categoryNeedsContinuation);
  const categoryInventory = categoryContinuation.data ?? categoryBasePage;
  const inventory = categoryInventory?.entries ?? categorySeed;
  const inventoryState = categoryInventory?.state;
  const inventorySourceAvailability = categoryInventory?.sourceAvailability;
  const categoryNextCursor = categoryContinuation.data
    ? categoryContinuation.data.nextCursor
    : categoryBasePage?.nextCursor;
  const categoriesLoading = categoryPage.isLoading
    || categoryNeedsContinuation && categoryContinuation.isLoading;
  const categoryError = categoryPage.error instanceof Error
    ? categoryPage.error.message
    : categoryPage.error ? 'Coded values could not be loaded.'
      : categoryContinuation.data?.error
        ?? (categoryContinuation.error instanceof Error ? categoryContinuation.error.message
          : categoryContinuation.error ? 'Coded values could not be loaded.' : undefined);
  return { inventory, inventoryState, inventorySourceAvailability, categoryNextCursor, categoriesLoading, categoryError };
}
