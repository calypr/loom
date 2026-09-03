import { describe, expect, it } from 'vitest';
import type { ExplorerRuntimeV1 } from '../../types';
import { createViewerReducerState, viewerReducer } from './reducer';

const runtime: ExplorerRuntimeV1 = {
  generation: 'generation-1',
  publication: { state: 'READY', revisionId: 'revision-1' },
  outputs: [
    {
      outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
      columns: [{ column: 'status', label: 'Status', logicalType: 'string', visible: true, order: 0, filterable: true, chartable: true }],
      table: { columns: [{ column: 'status', visible: true }] },
      filters: [{ column: 'status', type: 'enum', label: 'Status' }], charts: [], fixedFilters: {},
    },
    {
      outputId: 'encounters', name: 'encounters', title: 'Encounters', rowLabel: 'encounter',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'encounters' },
      columns: [{ column: 'kind', label: 'Kind', logicalType: 'string', visible: true, order: 0, filterable: true, chartable: true }],
      table: { columns: [{ column: 'kind', visible: true }] },
      filters: [{ column: 'kind', type: 'terms', label: 'Kind' }], charts: [], fixedFilters: {},
    },
  ],
  sharedFilters: { cohort: [{ column: 'status', outputId: 'patients', label: 'Cohort' }] },
  diagnostics: [],
};

describe('Explorer Viewer reducer', () => {
  it('resets cursor history when filter, sort, or page size changes', () => {
    let state = createViewerReducerState(runtime);
    state = viewerReducer(state, { type: 'nextPage', outputId: 'patients', cursor: 'cursor-1' });
    expect(state.outputs.patients.cursorHistory).toEqual([undefined, 'cursor-1']);
    state = viewerReducer(state, { type: 'setFilter', outputId: 'patients', column: 'status', values: ['active'] });
    expect(state.outputs.patients.cursorHistory).toEqual([undefined]);
    state = viewerReducer(state, { type: 'setSort', outputId: 'patients', sort: { column: 'status', desc: true } });
    expect(state.outputs.patients.cursorHistory).toEqual([undefined]);
    state = viewerReducer(state, { type: 'setPageSize', outputId: 'patients', pageSize: 40 });
    expect(state.outputs.patients.pageSize).toBe(40);
  });

  it('requires confirmation before shared filters mutate all output sessions', () => {
    let state = createViewerReducerState(runtime);
    state = viewerReducer(state, { type: 'proposeSharedFilter', proposal: { name: 'cohort', values: ['active'] } });
    expect(state.sharedFilters).toEqual({});
    expect(state.overlay.kind).toBe('sharedFilterConfirmation');
    state = viewerReducer(state, { type: 'setSharedFilter', name: 'cohort', values: ['active'] });
    expect(state.sharedFilters).toEqual({ cohort: ['active'] });
    expect(state.overlay).toEqual({ kind: 'none' });
    expect(state.outputs.encounters.cursorHistory).toEqual([undefined]);
  });

  it('keeps unknown output and duplicate next cursors inert', () => {
    const state = createViewerReducerState(runtime);
    expect(viewerReducer(state, { type: 'selectOutput', outputId: 'missing' })).toEqual(state);
    const next = viewerReducer(state, { type: 'nextPage', outputId: 'patients', cursor: 'cursor-1' });
    expect(viewerReducer(next, { type: 'nextPage', outputId: 'patients', cursor: 'cursor-1' }).outputs.patients.cursorHistory).toEqual([undefined, 'cursor-1']);
  });
});
