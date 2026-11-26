import { useMemo, useSyncExternalStore } from 'react';
import type { ExplorerAuthoringStateArgs } from '../../../client/api';
import type { ExplorerBuilderState } from '../../../contracts/types';
import { useLoomClient } from '../../../data/provider';
import type { QueryResult } from '../../../data/queries';
import { createBuilderSession } from './createBuilderSession';

export function useGetExplorerBuilderStateV2Query(scope: ExplorerAuthoringStateArgs): QueryResult<ExplorerBuilderState> {
  const client = useLoomClient();
  const { project, explorerId, authResourcePath } = scope;
  const session = useMemo(
    () => createBuilderSession({ client, scope }),
    [client, project, explorerId, authResourcePath],
  );
  const snapshot = useSyncExternalStore(session.subscribe, session.getSnapshot, session.getSnapshot);
  const loading = Boolean(explorerId) && snapshot.loading;
  return { ...snapshot, isLoading: loading, isFetching: loading, refetch: session.refresh };
}
