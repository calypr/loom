import { useImperativeHandle, useMemo, useRef, useState, type RefObject } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse, RelatedExpandChoiceSearchResponse } from '../../../contracts/types';
import type { RelatedExpandQueryOwner } from './commandQueue';

type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];

interface RouteQueryCheckpoint {
  readonly queryKey: string;
  readonly choices: ReadonlyArray<RouteChoice>;
  readonly truncated: boolean;
  readonly nextScope?: 'saved' | 'catalog';
}

interface ActiveRoutePage {
  readonly queryKey: string;
  readonly task: Promise<void>;
}


const routeDisplayKey = (choice: RouteChoice): string => JSON.stringify([choice.nodeId, choice.route.map(({ edgeId, matchMode }) => [edgeId, matchMode])]);
export const uniqueRoutes = (items: ReadonlyArray<RouteChoice>): RouteChoice[] => {
  const seen = new Set<string>();
  return items.filter((item) => {
    const key = routeDisplayKey(item);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
};

const choicesMatchRequest = (
  result: RelatedExpandChoiceSearchResponse,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
  outputId: string,
  stageId: string,
  anchorColumnId: string,
  targetResourceType: string,
): boolean => sameChoiceRequestScope(result, { snapshotToken, draftVersion, draftDigest, outputId, stageId })
  && result.choices.every((item) => item.anchorColumnId === anchorColumnId && item.targetResourceType === targetResourceType);

export function useRelatedExpandRoutes({
  project, explorerId, authResourcePath, snapshotToken, outputId,
  capabilities, anchorColumnId, targetResourceType, startsFromSavedGraph, queryOwnerRef,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly capabilities: Pick<ConstructionCapabilitiesResponse, 'selectedStage' | 'draftVersion' | 'draftDigest'>;
  readonly anchorColumnId: string;
  readonly targetResourceType: string;
  readonly startsFromSavedGraph: boolean;
  readonly queryOwnerRef?: RefObject<RelatedExpandQueryOwner | null>;
}) {
  const client = useLoomClient();
  const stage = capabilities.selectedStage;
  const routeQueryKey = JSON.stringify([
    project, explorerId, authResourcePath ?? '', snapshotToken,
    capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType, startsFromSavedGraph,
  ]);
  const [loadedChoices, setLoadedChoices] = useState<RouteQueryCheckpoint>();
  const routeCheckpointRef = useRef<RouteQueryCheckpoint | undefined>(undefined);
  const routeQueryPausedRef = useRef(false);
  const activeRoutePageRef = useRef<ActiveRoutePage | undefined>(undefined);
  const choices = loadedChoices?.queryKey === routeQueryKey ? loadedChoices.choices : [];
  const routeSearchTruncated = loadedChoices?.queryKey === routeQueryKey && loadedChoices.truncated;
  const routeQuery = useQuery(async (signal) => {
    let checkpoint = routeCheckpointRef.current;
    if (checkpoint?.queryKey !== routeQueryKey) {
      checkpoint = { queryKey: routeQueryKey, choices: [], truncated: false, nextScope: startsFromSavedGraph ? 'saved' : 'catalog' };
      routeCheckpointRef.current = checkpoint;
      routeQueryPausedRef.current = false;
    }
    while (checkpoint.nextScope && !signal.aborted && !routeQueryPausedRef.current) {
      const scope = checkpoint.nextScope;
      const previous = checkpoint;
      const task = (async () => {
        const result = await client.searchRelatedExpandChoices({
          project, explorerId, authResourcePath, snapshotToken, outputId,
          expectedDraftVersion: capabilities.draftVersion,
          expectedDraftDigest: capabilities.draftDigest,
          stageId: stage.id, anchorColumnId, targetResourceType, limit: 12,
          ...(scope === 'saved' ? { savedGraphOnly: true } : {}),
          requestId: `related-expand-choices-${window.crypto.randomUUID()}`,
        }, signal);
        if (signal.aborted) return;
        if (!choicesMatchRequest(result, snapshotToken, capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType)) {
          throw new Error('The available paths changed. Reload this table before expanding records.');
        }
        if (!result.complete && !result.nextCursor && !result.truncated) {
          throw new Error('Could not finish loading relationship paths. Reopen this editor to retry.');
        }
        const next: RouteQueryCheckpoint = {
          queryKey: routeQueryKey,
          choices: uniqueRoutes([...previous.choices, ...result.choices]),
          truncated: previous.truncated || result.truncated || Boolean(result.nextCursor),
          nextScope: scope === 'saved' ? 'catalog' : undefined,
        };
        checkpoint = next;
        routeCheckpointRef.current = next;
        setLoadedChoices(next);
      })();
      const activePage: ActiveRoutePage = { queryKey: routeQueryKey, task };
      activeRoutePageRef.current = activePage;
      try {
        await task;
      } finally {
        if (activeRoutePageRef.current === activePage) activeRoutePageRef.current = undefined;
      }
    }
    return checkpoint.choices;
  }, [client, authResourcePath, explorerId, project, snapshotToken, capabilities.draftVersion,
    capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType, startsFromSavedGraph],
  Boolean(targetResourceType && anchorColumnId));
  const queryOwner = useMemo<RelatedExpandQueryOwner>(() => ({
    draftVersion: capabilities.draftVersion,
    draftDigest: capabilities.draftDigest,
    pauseAndDrain: async () => {
      if (queryOwnerRef && queryOwnerRef.current !== queryOwner) return;
      routeQueryPausedRef.current = true;
      const activePage = activeRoutePageRef.current;
      if (activePage?.queryKey === routeQueryKey) {
        await activePage.task.then(() => undefined, () => undefined);
      }
    },
    resume: () => {
      if (queryOwnerRef && queryOwnerRef.current !== queryOwner) return;
      routeQueryPausedRef.current = false;
      const checkpoint = routeCheckpointRef.current;
      if (!activeRoutePageRef.current && checkpoint?.queryKey === routeQueryKey && checkpoint.nextScope) void routeQuery.refetch();
    },
  }), [capabilities.draftDigest, capabilities.draftVersion, queryOwnerRef, routeQuery.refetch, routeQueryKey]);
  useImperativeHandle(queryOwnerRef ?? null, () => queryOwner, [queryOwner]);
  const loading = routeQuery.isFetching;
  const error = routeQuery.error instanceof Error
    ? routeQuery.error.message
    : routeQuery.error ? 'Could not load related paths.' : '';
  return {
    choices, routeSearchTruncated, loading, error,
    clearRoutes: () => setLoadedChoices({ queryKey: routeQueryKey, choices: [], truncated: false }),
    retry: routeQuery.refetch,
  };
}
