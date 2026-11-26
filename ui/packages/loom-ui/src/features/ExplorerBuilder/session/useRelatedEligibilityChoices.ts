import { useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import { RequestOwner } from '../../../data/requests';
import { sameChoiceRequestScope } from '../../../contracts/identity';
import type { ConstructionCapabilitiesResponse, RelatedExpandChoiceSearchResponse } from '../../../contracts/types';

type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];

export function useRelatedEligibilityChoices({
  project, explorerId, authResourcePath, snapshotToken, outputId, capabilities, anchorColumnId, targetResourceType, supported,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly capabilities: ConstructionCapabilitiesResponse;
} & {
  readonly anchorColumnId: string;
  readonly targetResourceType: string;
  readonly supported?: boolean;
}) {
  const client = useLoomClient();
  const stage = capabilities.selectedStage;
  const [requests] = useState(() => new RequestOwner());
  const queryKey = JSON.stringify([
    snapshotToken, outputId, capabilities.draftVersion, capabilities.draftDigest,
    stage.id, anchorColumnId, targetResourceType,
  ]);
  const choicesQuery = useQuery(
    async (signal) => {
      const response = await client.searchRelatedExpandChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, anchorColumnId, targetResourceType, limit: 10,
      }, signal);
      if (!sameChoiceRequestScope(response, {
        snapshotToken, draftVersion: capabilities.draftVersion, draftDigest: capabilities.draftDigest, outputId, stageId: stage.id,
      }) || response.choices.some((item) =>
            item.anchorColumnId !== anchorColumnId || item.targetResourceType !== targetResourceType)) {
        throw new Error('The available paths changed. Reload this table to choose related records.');
      }
      return response;
    },
    [client, project, explorerId, authResourcePath, snapshotToken, outputId,
      capabilities.draftVersion, capabilities.draftDigest, stage.id, anchorColumnId, targetResourceType, supported],
    Boolean(supported && targetResourceType && anchorColumnId),
  );
  const [moreChoices, setMoreChoices] = useState<{
    readonly key: string;
    readonly choices: ReadonlyArray<RouteChoice>;
    readonly cursor?: string;
    readonly loading: boolean;
    readonly error: string;
  }>({ key: '', choices: [], loading: false, error: '' });
  const choices = [
    ...(choicesQuery.data?.choices ?? []),
    ...(moreChoices.key === queryKey ? moreChoices.choices : []),
  ];
  const cursor = moreChoices.key === queryKey ? moreChoices.cursor : choicesQuery.data?.nextCursor;
  const loading = choicesQuery.isLoading || (moreChoices.key === queryKey && moreChoices.loading);
  const error = choicesQuery.error
    ? choicesQuery.error instanceof Error ? choicesQuery.error.message : 'Could not load related paths.'
    : moreChoices.key === queryKey ? moreChoices.error : '';

  const loadMore = async () => {
    if (!cursor || !targetResourceType || !anchorColumnId || loading) return;
    requests.setIdentity(queryKey);
    const request = requests.start('continuation', queryKey);
    if (!request) return;
    const currentChoices = moreChoices.key === queryKey ? moreChoices.choices : [];
    setMoreChoices({ key: queryKey, choices: currentChoices, cursor, loading: true, error: '' });
    try {
      const response = await client.searchRelatedExpandChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, anchorColumnId, targetResourceType, limit: 10, cursor,
      }, request.signal);
      if (request.signal.aborted) return;
      setMoreChoices((current) => current.key === queryKey
        ? { key: queryKey, choices: [...current.choices, ...response.choices], cursor: response.nextCursor, loading: false, error: '' }
        : current);
    } catch (cause) {
      if (!request.signal.aborted) {
        setMoreChoices((current) => current.key === queryKey
          ? { ...current, loading: false, error: cause instanceof Error ? cause.message : 'Could not load more paths.' }
          : current);
      }
    } finally {
      request.finish();
    }
  };

  return { choices, cursor, loading, error, loadMore };
}
