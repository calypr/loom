import { QueryClient, useInfiniteQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse } from '../../../contracts/types';
import { useLoomClient } from '../../../react';

const stoppedAdvancing = 'The field list stopped advancing. Change your search or reopen this editor.';

export function useRelatedFieldChoices({
  project, explorerId, authResourcePath, snapshotToken, outputId, capabilities,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly capabilities: ConstructionCapabilitiesResponse;
}) {
  const client = useLoomClient();
  const { draftVersion, draftDigest, selectedStage: stage } = capabilities;
  const relatedRecord = stage.activeRelatedRecord;
  const [queryClient] = useState(() => new QueryClient());
  const [query, setQuery] = useState('');
  const [clientScope, setClientScope] = useState({ client, revision: 0 });
  if (clientScope.client !== client) {
    setClientScope({ client, revision: clientScope.revision + 1 });
  }
  const queryKey = [
    'related-field-choices', clientScope.revision, project, explorerId, authResourcePath, snapshotToken,
    outputId, draftVersion, draftDigest, stage.id, relatedRecord?.targetNodeId,
    relatedRecord?.targetResourceType, query,
  ];
  const result = useInfiniteQuery({
    queryKey,
    enabled: Boolean(relatedRecord),
    networkMode: 'always',
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
    queryFn: async ({ pageParam, signal }: { pageParam: string | null; signal: AbortSignal }) => {
      if (!relatedRecord) throw new Error('This table stage has no exact related record to read from.');
      const loadPage = async (cursor?: string) => {
        const response = await client.searchRelatedFieldChoices({
          project, explorerId, authResourcePath, snapshotToken, outputId,
          expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
          stageId: stage.id, query: query.trim(), limit: 20,
          ...(cursor ? { cursor } : {}),
        }, signal);
        if (!sameChoiceRequestScope(response, { snapshotToken, draftVersion, draftDigest, outputId, stageId: stage.id })) {
          throw new Error('Available fields changed. Reload this table before adding a column.');
        }
        return {
          choices: response.choices.filter(({ source }) =>
            source.nodeId === relatedRecord.targetNodeId &&
            source.resourceType === relatedRecord.targetResourceType &&
            (source.cardinality === 'optional_one' || source.cardinality === 'required_one'),
          ),
          cursor: response.nextCursor,
        };
      };
      let page = await loadPage(pageParam ?? undefined);
      if (pageParam !== null) return {
        ...page,
        error: page.cursor === pageParam ? stoppedAdvancing : '',
      };
      const seenCursors = new Set<string>();
      while (!page.choices.length && page.cursor && seenCursors.size < 2 && !seenCursors.has(page.cursor)) {
        signal.throwIfAborted();
        seenCursors.add(page.cursor);
        page = await loadPage(page.cursor);
      }
      return page.cursor && seenCursors.has(page.cursor)
        ? { choices: page.choices, cursor: undefined, error: stoppedAdvancing }
        : { ...page, error: '' };
    },
    initialPageParam: null,
    getNextPageParam: (page) => page.cursor,
  }, queryClient);
  const pages = result.data?.pages ?? [];
  const lastPage = pages.at(-1);
  const loading = result.isLoading || result.isFetchingNextPage;
  const error = result.error
    ? result.error instanceof Error
      ? result.error.message
      : result.isFetchNextPageError ? 'Could not load more fields.' : 'Could not load fields from this record.'
    : lastPage?.error ?? '';

  return {
    choices: pages.flatMap((page) => page.choices),
    cursor: lastPage?.cursor,
    loading,
    error,
    async loadMore() {
      if (!result.hasNextPage || loading) return;
      await result.fetchNextPage({ cancelRefetch: false });
    },
    query,
    searchFor: setQuery,
  };
}
