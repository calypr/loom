import { useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import { RequestOwner } from '../../../data/requests';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse, RelatedFieldChoiceSearchResponse } from '../../../contracts/types';

type FieldChoice = RelatedFieldChoiceSearchResponse['choices'][number];

const matchesRequest = (
  response: RelatedFieldChoiceSearchResponse,
  snapshotToken: string,
  capabilities: ConstructionCapabilitiesResponse,
  outputId: string,
): boolean => sameChoiceRequestScope(response, {
  snapshotToken, draftVersion: capabilities.draftVersion, draftDigest: capabilities.draftDigest,
  outputId, stageId: capabilities.selectedStage.id,
});

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
  const stage = capabilities.selectedStage;
  const [requests] = useState(() => new RequestOwner());
  const [query, setQuery] = useState('');
  const queryKey = JSON.stringify([
    snapshotToken, outputId, capabilities.draftVersion, capabilities.draftDigest,
    stage.id, stage.activeRelatedRecord?.targetNodeId, stage.activeRelatedRecord?.targetResourceType, query,
  ]);
  const choicesQuery = useQuery(
    async (signal) => {
      if (!stage.activeRelatedRecord) throw new Error('This table stage has no exact related record to read from.');
      const eligibleChoices = (items: ReadonlyArray<FieldChoice>) => items.filter((item) =>
        item.source.nodeId === stage.activeRelatedRecord?.targetNodeId &&
        item.source.resourceType === stage.activeRelatedRecord?.targetResourceType &&
        (item.source.cardinality === 'optional_one' || item.source.cardinality === 'required_one'),
      );
      let response = await client.searchRelatedFieldChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, query: query.trim(), limit: 20,
      }, signal);
      if (!matchesRequest(response, snapshotToken, capabilities, outputId)) {
        throw new Error('Available fields changed. Reload this table before adding a column.');
      }
      let available = eligibleChoices(response.choices);
      let nextCursor = response.nextCursor;
      const seenCursors = new Set<string>();
      for (let page = 1; available.length === 0 && nextCursor && page < 3; page += 1) {
        if (seenCursors.has(nextCursor)) {
          return { choices: available, cursor: undefined, error: 'The field list stopped advancing. Change your search or reopen this editor.' };
        }
        seenCursors.add(nextCursor);
        const next = await client.searchRelatedFieldChoices({
          project, explorerId, authResourcePath, snapshotToken, outputId,
          expectedDraftVersion: capabilities.draftVersion,
          expectedDraftDigest: capabilities.draftDigest,
          stageId: stage.id, query: query.trim(), limit: 20, cursor: nextCursor,
        }, signal);
        if (!matchesRequest(next, snapshotToken, capabilities, outputId)) {
          throw new Error('Available fields changed. Reload this table before adding a column.');
        }
        available = eligibleChoices(next.choices);
        nextCursor = next.nextCursor;
      }
      if (nextCursor && seenCursors.has(nextCursor)) {
        return { choices: available, cursor: undefined, error: 'The field list stopped advancing. Change your search or reopen this editor.' };
      }
      return { choices: available, cursor: nextCursor, error: '' };
    },
    [client, project, explorerId, authResourcePath, snapshotToken, outputId, capabilities, stage.id, stage.activeRelatedRecord, query],
    Boolean(stage.activeRelatedRecord),
  );
  const [moreChoices, setMoreChoices] = useState<{
    readonly key: string;
    readonly choices: ReadonlyArray<FieldChoice>;
    readonly cursor?: string;
    readonly loading: boolean;
    readonly error: string;
  }>({ key: '', choices: [], loading: false, error: '' });
  const choices = [
    ...(choicesQuery.data?.choices ?? []),
    ...(moreChoices.key === queryKey ? moreChoices.choices : []),
  ];
  const cursor = moreChoices.key === queryKey ? moreChoices.cursor : choicesQuery.data?.cursor;
  const loading = choicesQuery.isLoading || (moreChoices.key === queryKey && moreChoices.loading);
  const error = choicesQuery.error
    ? choicesQuery.error instanceof Error ? choicesQuery.error.message : 'Could not load fields from this record.'
    : choicesQuery.data?.error
      ? choicesQuery.data.error
      : moreChoices.key === queryKey ? moreChoices.error : '';

  const loadMore = async () => {
    if (!cursor || loading) return;
    requests.setIdentity(queryKey);
    const request = requests.start('continuation', queryKey);
    if (!request) return;
    const currentChoices = moreChoices.key === queryKey ? moreChoices.choices : [];
    setMoreChoices({ key: queryKey, choices: currentChoices, cursor, loading: true, error: '' });
    try {
      const response = await client.searchRelatedFieldChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, query: query.trim(), limit: 20, cursor,
      }, request.signal);
      if (request.signal.aborted) return;
      if (!matchesRequest(response, snapshotToken, capabilities, outputId)) {
        throw new Error('Available fields changed. Reload this table before adding a column.');
      }
      const pageChoices = response.choices.filter((item) =>
        item.source.nodeId === stage.activeRelatedRecord?.targetNodeId &&
        item.source.resourceType === stage.activeRelatedRecord?.targetResourceType &&
        (item.source.cardinality === 'optional_one' || item.source.cardinality === 'required_one'),
      );
      setMoreChoices((current) => current.key === queryKey
        ? {
            key: queryKey, choices: [...current.choices, ...pageChoices],
            cursor: response.nextCursor, loading: false,
            error: response.nextCursor === cursor ? 'The field list stopped advancing. Change your search or reopen this editor.' : '',
          }
        : current);
    } catch (cause) {
      if (!request.signal.aborted) {
        setMoreChoices((current) => current.key === queryKey
          ? { ...current, loading: false, error: cause instanceof Error ? cause.message : 'Could not load more fields.' }
          : current);
      }
    } finally {
      request.finish();
    }
  };

  return { choices, cursor, loading, error, loadMore, query, searchFor(nextQuery: string) {
    requests.cancel('continuation');
    setQuery(nextQuery);
  } };
}
