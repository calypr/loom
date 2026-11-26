import { sameCatalogSnapshot } from '../../../contracts/identity';
import { useBuilderActions, useBuilderSession } from './BuilderSessionProvider';
import { useCallback, useRef, useState, type Dispatch } from 'react';
import type { BuilderMutationResult } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import { derivedOccurrences, selectedTable, type BuilderAuthoringState } from '../model/authoring';
import type { BuilderAction } from '../model/reducer';

type UseSuggestionsOptions = {
  readonly getSuggestions: BuilderMutationResult<'suggestions'>[0];
};

export const useSuggestions = ({
  getSuggestions, }: UseSuggestionsOptions) => {
  const { state, draftAccess, builderQueryArgs } = useBuilderSession(draft => draft);
  const { dispatch } = useBuilderActions();
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const [suggestionsFailure, setSuggestionsFailure] = useState<{
    readonly key: string;
    readonly message: string;
  }>();
  const suggestionRequestKey = useRef('');
  const ensureSuggestions = useCallback(() => {
    const current = draftAccess.getState();
    const currentOccurrence = derivedOccurrences(
      selectedTable(current),
      current.catalog,
    ).find((candidate) => candidate.id === current.selectedOccurrenceId);
    if (!currentOccurrence || !current.catalog.snapshotToken) return;
    const key = `${current.explorerId}:${current.catalog.snapshotToken}:${currentOccurrence.id}`;
    if (suggestionRequestKey.current === key) return;
    if (
      (current.catalog.candidates ?? []).some(
        (candidate) => candidate.nodeId === currentOccurrence.nodeId,
      )
    )
      return;
    suggestionRequestKey.current = key;
    setSuggestionsFailure(undefined);
    const request = getSuggestions({
      project: projectId,
      explorerId: current.explorerId,
      authResourcePath,
      snapshotToken: current.catalog.snapshotToken,
      nodeId: currentOccurrence.nodeId,
      requestId: `suggestions-${currentOccurrence.id}`,
    });
    void request
      .unwrap()
      .then((value) => {
        const latest = draftAccess.getState();
        if (
          latest.explorerId === current.explorerId &&
          sameCatalogSnapshot(value.snapshotToken, latest.catalog.snapshotToken) &&
          latest.selectedOccurrenceId === currentOccurrence.id
        ) {
          dispatch({ type: 'candidatesLoaded', candidates: value.candidates });
          setSuggestionsFailure(undefined);
        } else if (suggestionRequestKey.current === key) {
          suggestionRequestKey.current = '';
        }
      })
      .catch((error: ExplorerAuthoringApiError) => {
        if (suggestionRequestKey.current === key) suggestionRequestKey.current = '';
        if (error.code !== 'CLIENT_CANCELLED') {
          const suffix = error.code ? ` (${error.code})` : '';
          const latest = draftAccess.getState();
          if (latest.explorerId === current.explorerId &&
            sameCatalogSnapshot(latest.catalog.snapshotToken, current.catalog.snapshotToken) &&
            latest.selectedOccurrenceId === currentOccurrence.id) {
            setSuggestionsFailure({
              key,
              message: `Available columns could not be loaded: ${error.message}${suffix}`,
            });
          }
        }
      });
  }, [authResourcePath, dispatch, getSuggestions, projectId]);
  const suggestionHostRef = useCallback(
    (host: HTMLSpanElement | null) => {
      if (host) ensureSuggestions();
    },
    [ensureSuggestions],
  );
  const suggestionIdentity = `${state.explorerId}:${state.catalog.snapshotToken}:${state.selectedOccurrenceId}`;
  return {
    suggestionsFailure, setSuggestionsFailure, suggestionRequestKey,
    ensureSuggestions, suggestionHostRef, suggestionIdentity,
  };
};
