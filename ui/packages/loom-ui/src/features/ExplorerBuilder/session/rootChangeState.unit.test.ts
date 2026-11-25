import { describe, expect, it, vi } from 'vitest';
import type { RowChangeAssessment } from '../../../contracts/types';
import type { RootChangeRequestIdentity } from '../model/rootChangeSelection';
import { createRootChangeStateStore } from './rootChangeState';

describe('root change identity changes', () => {
  it.each(['same-output', 'different-output'] as const)(
    'preserves the identity-change behavior for %s while assessment and preview are pending',
    (change) => {
      const identity: RootChangeRequestIdentity = {
        epoch: 0, project: 'project', explorerId: 'explorer', outputId: 'table',
        snapshotToken: 'snapshot', draftVersion: 1, draftDigest: 'digest-1',
      };
      const setMessage = vi.fn();
      const store = createRootChangeStateStore(() => {}, setMessage);
      store.activate(identity);
      const selection = { kind: 'catalog-node' as const, nodeId: 'Observation' };
      const resolution = { routeRebase: [{ occurrenceId: 'child', edgeId: 'edge-1' }] };
      const assessment: Extract<RowChangeAssessment, { readonly status: 'READY' }> = {
        status: 'READY',
        snapshotToken: 'snapshot',
        draftVersion: 1,
        draftDigest: 'digest-1',
        currentRootResourceType: 'Observation',
        candidateRootResourceType: 'Observation',
        preservedFeatureKeys: [],
        diagnostics: [],
        proposal: {
          outputId: 'table',
          rootNodeId: 'Observation',
          rootOccurrenceId: 'root',
          sourceDocumentDigest: 'document-digest',
          routeRebase: [],
          preservedFeatureKeys: [],
        },
        unresolved: [],
      };

      store.update(() => ({
        rootChangeState: { kind: 'assessing', selection, resolution, identity },
        pendingRowChangePreview: { status: 'loading', assessment, identity },
      }));
      store.activate(change === 'same-output'
        ? { ...identity, draftVersion: 2, draftDigest: 'digest-2' }
        : { ...identity, outputId: 'other-table' });
      const snapshot = store.getSnapshot();
      expect(snapshot.rootChangeState).toEqual(change === 'same-output'
        ? {
            kind: 'failed',
            reason: 'stale',
            selection,
            resolution,
            identity,
            message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
          }
        : { kind: 'idle' });
      expect(snapshot.pendingRowChangePreview).toBeUndefined();
      expect(setMessage).toHaveBeenCalledWith(
        'The table changed while Loom prepared the row preview. Review the change again.',
      );
    },
  );
});
