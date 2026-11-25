import type { ExplorerBuilderPreviewResult, RowChangeAssessment } from '../../../contracts/types';
import { createStore } from '../../../data/createStore';
import { sameRootChangeRequestIdentity, type RootChangePickerState, type RootChangeRequestIdentity } from '../model/rootChangeSelection';

type ReadyRootChangeAssessment = Extract<RowChangeAssessment, { readonly status: 'READY' }>;

type RowChangePreviewContext = {
  readonly assessment: ReadyRootChangeAssessment;
  readonly identity: RootChangeRequestIdentity;
};

export type PendingRowChangePreview =
  | (RowChangePreviewContext & { readonly status: 'loading' })
  | (RowChangePreviewContext & { readonly status: 'ready'; readonly preview: ExplorerBuilderPreviewResult });

type RootChangeStateSnapshot = {
  readonly rootChangeState: RootChangePickerState;
  readonly pendingRowChangePreview: PendingRowChangePreview | undefined;
};

type RootChangeStateStore = {
  readonly getSnapshot: () => RootChangeStateSnapshot;
  readonly subscribe: (subscriber: () => void) => () => void;
  readonly activate: (identity: RootChangeRequestIdentity | undefined) => void;
  readonly update: (update: (current: RootChangeStateSnapshot) => RootChangeStateSnapshot) => void;
};

export const createRootChangeStateStore = (
  onDetach: () => void,
  setMessage: (message: string) => void,
): RootChangeStateStore => {
  const store = createStore<RootChangeStateSnapshot>({
    rootChangeState: { kind: 'idle' },
    pendingRowChangePreview: undefined,
  });
  let activated = false;
  let activeIdentity: RootChangeRequestIdentity | undefined;
  return {
    getSnapshot: store.getSnapshot,
    subscribe: (subscriber) => {
      const unsubscribe = store.subscribe(subscriber);
      return () => {
        unsubscribe();
        onDetach();
      };
    },
    activate: (identity) => {
      if (!activated) {
        activated = true;
        activeIdentity = identity;
        return;
      }
      if (identity && activeIdentity && sameRootChangeRequestIdentity(activeIdentity, identity)) return;

      const previous = activeIdentity;
      const { rootChangeState: active, pendingRowChangePreview: pending } = store.getSnapshot();
      const activeIsStale = active.kind !== 'idle' && (
        identity === undefined || !sameRootChangeRequestIdentity(active.identity, identity)
      );
      const previewIsStale = pending !== undefined && (
        identity === undefined || !sameRootChangeRequestIdentity(pending.identity, identity)
      );
      const sameExplorerOutput = identity !== undefined && previous !== undefined &&
        previous.project === identity.project &&
        previous.authResourcePath === identity.authResourcePath &&
        previous.explorerId === identity.explorerId &&
        previous.outputId === identity.outputId;

      onDetach();
      activeIdentity = identity;
      if (!activeIsStale && !previewIsStale) return;

      const nextRootChangeState = activeIsStale
        ? sameExplorerOutput
          ? {
              kind: 'failed' as const,
              reason: 'stale' as const,
              selection: active.selection,
              resolution: active.resolution,
              identity: active.identity,
              message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
            }
          : { kind: 'idle' as const }
        : active;
      const next = {
        rootChangeState: nextRootChangeState,
        pendingRowChangePreview: previewIsStale ? undefined : pending,
      };
      store.setSnapshot(next, previewIsStale ? () => {
        setMessage('The table changed while Loom prepared the row preview. Review the change again.');
      } : undefined);
    },
    update: (update) => {
      store.setSnapshot(update(store.getSnapshot()));
    },
  };
};
