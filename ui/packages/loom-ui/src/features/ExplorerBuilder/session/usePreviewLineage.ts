import { useMemo, useState, useSyncExternalStore } from 'react';
import type { ExplorerBuilderPreviewResult, ExplorerBuilderRowLineageResponse } from '../../../contracts/types';
import { createStore } from '../../../data/createStore';
import { RequestOwner } from '../../../data/requests';

type RowLineageState = {
  readonly rowId: string;
  readonly contributors: NonNullable<ExplorerBuilderRowLineageResponse['contributors']>;
  readonly nextOffset?: number;
  readonly loading: boolean;
  readonly status?: ExplorerBuilderRowLineageResponse['status'];
  readonly reasonCode?: string;
  readonly error?: string;
};

function createLineageState() {
  const store = createStore<RowLineageState | undefined>(undefined);
  const requests = new RequestOwner();
  return {
    requests,
    getSnapshot: store.getSnapshot,
    setSnapshot: store.setSnapshot,
    subscribeInScope: (identity: string) => (listener: () => void) => {
      const unsubscribe = store.subscribe(listener);
      requests.setIdentity(identity);
      return () => {
        unsubscribe();
        queueMicrotask(() => {
          if (!store.hasSubscribers()) requests.cancel('lineage');
        });
      };
    },
  };
}

export function usePreviewLineage(
  preview: Pick<ExplorerBuilderPreviewResult, 'receiptId' | 'outputId'> | undefined,
  onRowLineage?: (rowId: string, offset: number, signal: AbortSignal) => Promise<ExplorerBuilderRowLineageResponse>,
) {
  const [lineage] = useState(createLineageState);
  const { requests, setSnapshot } = lineage;
  const identity = JSON.stringify([preview?.receiptId, preview?.outputId]);
  const subscribe = useMemo(() => lineage.subscribeInScope(identity), [lineage, identity]);
  const rowLineage = useSyncExternalStore(subscribe, lineage.getSnapshot, lineage.getSnapshot);
  const setRowLineage = (update: (previous: RowLineageState | undefined) => RowLineageState) => {
    setSnapshot(update(lineage.getSnapshot()));
  };
  const loadRowLineage = (rowId: string, offset: number) => {
    if (!onRowLineage) return;
    const request = requests.start('lineage', identity);
    if (!request) return;
    setRowLineage((previous) => ({
      rowId,
      contributors: offset > 0 && previous?.rowId === rowId ? previous.contributors : [],
      loading: true,
    }));
    void onRowLineage(rowId, offset, request.signal).then((result) => {
      if (request.signal.aborted) return;
      setRowLineage((previous) => ({
        rowId,
        contributors: [
          ...(offset > 0 && previous?.rowId === rowId ? previous.contributors : []),
          ...(result.contributors ?? []),
        ],
        nextOffset: result.hasMore ? result.nextOffset : undefined,
        loading: false,
        status: result.status,
        reasonCode: result.reasonCode,
      }));
    }).catch((error: unknown) => {
      if (request.signal.aborted) return;
      setRowLineage((previous) => ({
        rowId,
        contributors: offset > 0 && previous?.rowId === rowId ? previous.contributors : [],
        loading: false,
        error: error instanceof Error ? error.message : 'Unable to load source records.',
      }));
    });
  };
  const clearRowLineage = () => {
    requests.cancel('lineage');
    setSnapshot(undefined);
  };
  return { rowLineage, loadRowLineage, clearRowLineage };
}
