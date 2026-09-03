import { describe, expect, it } from 'vitest';
import type { ExplorerRuntimeV1 } from '../../types';
import { createViewerReducerState, viewerReducer } from './reducer';
import { outputRequestFor } from './serialization';

const runtime: ExplorerRuntimeV1 = {
  generation: 'generation-1',
  outputs: [
    {
      outputId: 'patients',
      name: 'patients',
      title: 'Patients',
      rowLabel: 'patient',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
      columns: [
        { column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: false, sortable: true, chartable: false },
        { column: 'status', label: 'Status', logicalType: 'string', visible: true, order: 1, filterable: true, chartable: true },
        { column: '__loom_private', label: 'Private', logicalType: 'string', visible: false, order: 2, filterable: false, chartable: false },
      ],
      table: {
        columns: [
          { column: 'patient_id', label: 'Patient ID', visible: true, pinned: true },
          { column: 'status', label: 'Status', visible: true },
          { column: '__loom_private', label: 'Private', visible: false },
        ],
      },
      filters: [{ column: 'status', label: 'Status', type: 'enum' }],
      charts: [{ column: 'status', title: 'Status overview', type: 'bar' }],
      fixedFilters: { project: ['NCPI'] },
    },
  ],
  sharedFilters: { cohort: [{ outputId: 'patients', column: 'status' }] },
  diagnostics: [],
};

describe('Explorer Viewer request serialization', () => {
  it('combines fixed, local, and shared filters without exposing hidden columns', () => {
    let state = createViewerReducerState(runtime);
    state = viewerReducer(state, { type: 'setFilter', outputId: 'patients', column: 'status', values: ['active'] });
    state = viewerReducer(state, { type: 'setSharedFilter', name: 'cohort', values: ['reviewed'] });
    state = viewerReducer(state, { type: 'setSort', outputId: 'patients', sort: { column: 'patient_id', desc: true } });

    const request = outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]);

    expect(request.columns).toEqual(['patient_id', 'status']);
    expect(request.filters).toEqual([
      { column: 'project', op: 'EQ', value: 'NCPI' },
      { column: 'status', op: 'EQ', value: 'reviewed' },
    ]);
    expect(request.sort).toEqual({ column: 'patient_id', desc: true });
    expect(request.after).toBeUndefined();
    expect(request.facets).toEqual([
      { name: 'loom:patients:status', kind: 'TERMS', column: 'status', size: 50, excludeSelfFilter: true },
      { name: 'loom:patients:chart:status', kind: 'TERMS', column: 'status', size: 12 },
    ]);
  });

  it('drops chart aggregation work while charts are hidden', () => {
    const initial = createViewerReducerState(runtime);
    const state = viewerReducer(initial, { type: 'toggleCharts', outputId: 'patients' });

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]).facets).toEqual([
      { name: 'loom:patients:status', kind: 'TERMS', column: 'status', size: 50, excludeSelfFilter: true },
    ]);
  });
});
