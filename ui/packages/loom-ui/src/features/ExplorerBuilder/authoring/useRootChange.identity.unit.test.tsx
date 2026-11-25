// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../client/api';
import type { ExplorerBuilderCatalog, RowChangeAssessment } from '../../../contracts/types';
import { useRootChange } from './useRootChange';
import type { BuilderAuthoringState, DraftTable } from './model';

describe('useRootChange identity changes', () => {
  it.each(['same-output', 'different-output'] as const)(
    'preserves the identity-change behavior for %s while assessment and preview are pending',
    (change) => {
    const document = {
      kind: 'ExplorerBuilderDocument' as const,
      output: { id: 'table', title: 'Table' },
      rootResourceType: 'Observation',
      rows: { kind: 'RECORDS' as const, records: {} },
      route: { occurrenceId: 'root', resourceType: 'Observation' },
      columns: [],
    };
    const table: DraftTable = {
      outputId: 'table',
      tabId: 'table',
      title: 'Table',
      document,
    };
    const catalog: ExplorerBuilderCatalog = {
      snapshotToken: 'snapshot',
      generation: 'generation',
      routePolicy: {},
      nodes: [],
      edges: [],
    };
    const state: BuilderAuthoringState = {
      project: 'project',
      explorerId: 'explorer',
      catalog,
      workspace: null,
      draftVersion: 1,
      draftDigest: 'digest-1',
      previewRequestVersion: 0,
      tables: [table],
      selectedOutputId: 'table',
      selectedOccurrenceId: 'root',
      diagnostics: [],
      dirty: false,
      reconciliation: 'idle',
    };
    const latestState = { current: state };
    const serverDraft = { current: { version: 1, digest: 'digest-1' } };
    const setMessage = vi.fn();
    const options = {
      projectId: 'project',
      state,
      table,
      latestState,
      serverDraft,
      workspaceAttached: { current: true },
      assessRowChange: vi.fn(),
      loomClient: createLoomClient(),
      previewLimit: 25 as const,
      refetchBuilder: vi.fn(),
      syncBuilderData: vi.fn(),
      applyCommands: vi.fn(),
      rowSettingsOpenAction: { current: undefined },
      rowSettingsCloseAction: { current: undefined },
      setMessage,
    };
    const { result, rerender } = renderHook((props) => useRootChange(props), {
      initialProps: options,
    });
    const identity = result.current.currentRootChangeIdentity;
    if (!identity) throw new Error('Expected the selected table identity');
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

    act(() => {
      result.current.setRootChangeState({ kind: 'assessing', selection, resolution, identity });
      result.current.setPendingRowChangePreview({
        status: 'loading',
        assessment,
        identity,
      });
    });

    const nextTable: DraftTable = change === 'same-output'
      ? table
      : { ...table, outputId: 'other-table', document: { ...document, output: { id: 'other-table', title: 'Other' } } };
    const changedState: BuilderAuthoringState = change === 'same-output'
      ? { ...state, draftVersion: 2, draftDigest: 'digest-2' }
      : {
          ...state,
          tables: [nextTable],
          selectedOutputId: nextTable.outputId,
        };
    latestState.current = changedState;
    if (change === 'same-output') serverDraft.current = { version: 2, digest: 'digest-2' };
    rerender({ ...options, state: changedState, table: nextTable });

    expect(result.current.rootChangeState).toEqual(change === 'same-output'
      ? {
          kind: 'failed',
          reason: 'stale',
          selection,
          resolution,
          identity,
          message: 'The table changed while Loom checked this row choice. Refresh the choices or keep the current rows.',
        }
      : { kind: 'idle' });
    expect(result.current.pendingRowChangePreview).toBeUndefined();
    expect(setMessage).toHaveBeenCalledWith(
      'The table changed while Loom prepared the row preview. Review the change again.',
    );
    },
  );
});
