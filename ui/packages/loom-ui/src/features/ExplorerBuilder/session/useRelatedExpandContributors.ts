import { useMemo, useState, useSyncExternalStore } from 'react';
import { ZodError } from 'zod';
import { useLoomClient } from '../../../react';
import { createStore } from '../../../data/createStore';
import { RequestOwner } from '../../../data/requests';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { RelatedExpandContributorSearchResponse } from '../../../contracts/types';

export type ContributorChoice = RelatedExpandContributorSearchResponse['choices'][number];

type ContributorSearchError = {
  readonly message: string;
  readonly diagnostic?: string;
};

const contributorPageSize = 20;
const automaticBrowsePageLimit = 3;
export const repeatedContributorCursorMessage = 'The related-field catalog repeated a page cursor. Search for a field name or path to continue.';
const filterContributorChoices = (
  choices: ReadonlyArray<ContributorChoice>,
  allowRepeatedFields: boolean,
) => choices.filter((choice) => allowRepeatedFields || choice.source.cardinality !== 'many');

const contributorSearchErrorFrom = (cause: unknown, fallback: string): ContributorSearchError =>
  cause instanceof ZodError
    ? {
      message: 'Could not load the related-record field list because the response did not match the expected format.',
      diagnostic: cause.message,
    }
    : { message: cause instanceof Error ? cause.message : fallback };

const choicesMatchRequest = (
  response: RelatedExpandContributorSearchResponse,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
  outputId: string,
  stageId: string,
  routeChoiceId: string,
  targetNodeId: string,
  targetResourceType: string,
): boolean => sameChoiceRequestScope(response, { snapshotToken, draftVersion, draftDigest, outputId, stageId })
  && response.routeChoiceId === routeChoiceId
  && response.choices.every((choice) => {
    const scalar = choice.source.cardinality === 'optional_one' || choice.source.cardinality === 'required_one';
    const boundaries = choice.source.repeatedBoundaries ?? [];
    return choice.source.nodeId === targetNodeId
      && choice.source.resourceType === targetResourceType
      && (scalar ? boundaries.length === 0 : choice.source.cardinality === 'many' && boundaries.length > 0);
  });

interface SearchScope {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
  readonly routeChoiceId: string;
  readonly targetNodeId: string;
  readonly targetResourceType: string;
  readonly allowRepeatedFields: boolean;
}

interface SearchSnapshot {
  readonly choices: ReadonlyArray<ContributorChoice>;
  readonly cursor?: string;
  readonly loading: boolean;
  readonly error?: ContributorSearchError;
}

function createSearch({
  project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
  outputId, stageId, routeChoiceId, targetNodeId, targetResourceType, allowRepeatedFields,
}: SearchScope, client: ReturnType<typeof useLoomClient>, query: string, enabled: boolean) {
  const store = createStore<SearchSnapshot>({ choices: [], loading: enabled });
  const requests = new RequestOwner();
  const scope = 'related-expand-contributors';
  requests.setIdentity(scope);
  const cursorHistory = new Set<string>();
  let started = false;
  const update = (patch: Partial<SearchSnapshot>) => store.setSnapshot({ ...store.getSnapshot(), ...patch });
  const setChoices = (choices: SearchSnapshot['choices'] | ((current: SearchSnapshot['choices']) => SearchSnapshot['choices'])) =>
    update({ choices: typeof choices === 'function' ? choices(store.getSnapshot().choices) : choices });
  const setCursor = (cursor: string | undefined) => update({ cursor });
  const setError = (error: ContributorSearchError | undefined) => update({ error });
  const setLoading = (loading: boolean) => update({ loading });
  const start = () => {
    const request = requests.start('initial', scope);
    if (!request) return;
    cursorHistory.clear();
    const timeout = globalThis.setTimeout(() => {
      const available: ContributorChoice[] = [];
      let pageCursor: string | undefined;
      void (async () => {
        let pagesRead = 0;
        while (pagesRead < automaticBrowsePageLimit && available.length === 0) {
          if (!request.isCurrent()) return;
          if (pageCursor) {
            if (cursorHistory.has(pageCursor)) {
              throw new Error(repeatedContributorCursorMessage);
            }
          }
          const response = await client.searchRelatedExpandContributors({
            project, explorerId, authResourcePath, snapshotToken,
            expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
            outputId, stageId, routeChoiceId, query: query.trim(), limit: contributorPageSize,
            ...(pageCursor ? { cursor: pageCursor } : {}),
          }, request.signal);
          if (!request.isCurrent()) return;
          if (!choicesMatchRequest(response, snapshotToken, draftVersion, draftDigest, outputId,
            stageId, routeChoiceId, targetNodeId, targetResourceType)) {
            throw new Error('Available conditions changed. Reload this table before expanding records.');
          }
          if (pageCursor) cursorHistory.add(pageCursor);
          available.push(...filterContributorChoices(response.choices, allowRepeatedFields));
          pagesRead += 1;
          const nextCursor = response.nextCursor;
          if (!nextCursor) {
            pageCursor = undefined;
            break;
          }
          if (cursorHistory.has(nextCursor) || nextCursor === pageCursor) {
            throw new Error(repeatedContributorCursorMessage);
          }
          pageCursor = nextCursor;
        }
        if (!request.isCurrent()) return;
        setChoices(available);
        setCursor(pageCursor);
      })().catch((cause: unknown) => {
        if (request.isCurrent()) {
          setError(contributorSearchErrorFrom(cause, 'Could not load related-record conditions.'));
          if (cause instanceof Error && cause.message === repeatedContributorCursorMessage) {
            setCursor(undefined);
          } else if (cause instanceof Error && cause.message.startsWith('Available conditions changed.')) {
            setChoices([]);
            setCursor(undefined);
          } else {
            setChoices(available);
            setCursor(pageCursor);
          }
        }
      }).finally(() => {
        if (request.isCurrent()) setLoading(false);
        request.finish();
      });
    }, query ? 180 : 0);
    request.signal.addEventListener('abort', () => globalThis.clearTimeout(timeout), { once: true });
  };

  const loadMore = async () => {
    const { cursor, loading } = store.getSnapshot();
    if (!cursor || loading) return;
    const request = requests.start('continuation', scope);
    if (!request) return;
    setLoading(true);
    setError(undefined);
    const available: ContributorChoice[] = [];
    let pageCursor: string | undefined = cursor;
    try {
      let pagesRead = 0;
      while (pageCursor && pagesRead < automaticBrowsePageLimit && available.length < contributorPageSize) {
        if (!request.isCurrent()) return;
        if (cursorHistory.has(pageCursor)) {
          throw new Error(repeatedContributorCursorMessage);
        }
        const response = await client.searchRelatedExpandContributors({
          project, explorerId, authResourcePath, snapshotToken,
          expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
          outputId, stageId, routeChoiceId, query: query.trim(), limit: contributorPageSize, cursor: pageCursor,
        }, request.signal);
        if (!request.isCurrent()) return;
        if (!choicesMatchRequest(response, snapshotToken, draftVersion, draftDigest, outputId,
          stageId, routeChoiceId, targetNodeId, targetResourceType)) {
          throw new Error('Available conditions changed. Reload this table before expanding records.');
        }
        cursorHistory.add(pageCursor);
        available.push(...filterContributorChoices(response.choices, allowRepeatedFields));
        pagesRead += 1;
        const nextCursor = response.nextCursor;
        if (nextCursor && (cursorHistory.has(nextCursor) || nextCursor === pageCursor)) {
          throw new Error(repeatedContributorCursorMessage);
        }
        pageCursor = nextCursor;
      }
      if (!request.isCurrent()) return;
      setChoices((current) => [...current, ...available]);
      setCursor(pageCursor);
    } catch (cause) {
      if (request.isCurrent()) {
        setError(contributorSearchErrorFrom(cause, 'Could not load more conditions.'));
        if (cause instanceof Error && cause.message === repeatedContributorCursorMessage) {
          setCursor(undefined);
        } else if (cause instanceof Error && cause.message.startsWith('Available conditions changed.')) {
          setChoices([]);
          setCursor(undefined);
        } else {
          setChoices((current) => [...current, ...available]);
          setCursor(pageCursor);
        }
      }
    } finally {
      if (request.isCurrent()) setLoading(false);
      request.finish();
    }
  };

  return {
    getSnapshot: store.getSnapshot,
    loadMore,
    cancel: () => requests.cancelAll(),
    subscribe(listener: () => void) {
      const unsubscribe = store.subscribe(listener);
      if (!started && enabled) {
        started = true;
        start();
      }
      return () => {
        unsubscribe();
        queueMicrotask(() => {
          if (store.hasSubscribers()) return;
          requests.cancelAll();
          started = false;
        });
      };
    },
  };
}

export function useRelatedExpandContributors(scope: SearchScope, enabled: boolean) {
  const client = useLoomClient();
  const [query, setQuery] = useState('');
  const [attempt, setAttempt] = useState(0);
  const { project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
    outputId, stageId, routeChoiceId, targetNodeId, targetResourceType, allowRepeatedFields } = scope;
  const search = useMemo(() => createSearch(scope, client, query, enabled), [
    client, project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
    outputId, stageId, routeChoiceId, targetNodeId, targetResourceType, allowRepeatedFields, query, enabled, attempt,
  ]);
  const snapshot = useSyncExternalStore(search.subscribe, search.getSnapshot, search.getSnapshot);
  return {
    ...snapshot,
    query,
    loadMore: search.loadMore,
    cancel: search.cancel,
    retrySearch() {
      search.cancel();
      setAttempt(current => current + 1);
    },
    searchFor(nextQuery: string) {
      if (nextQuery === query) return;
      search.cancel();
      setQuery(nextQuery);
    },
  };
}
