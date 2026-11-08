import { describe, expect, it, vi } from 'vitest';
import { createLoomClient, type LoomOutputFilter } from '../../api';
import type { ExplorerRuntimeV1 } from '../../types';
import { createViewerReducerState, viewerReducer } from './reducer';
import { outputFiltersFor, outputRequestFor } from './serialization';
import { missingSelection, valuesSelection, type ViewerFieldSelection } from './model';

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

// Each filterable column exercises every source state. The contract under test is
// that publication writes an absent property and an explicit null into the same
// ClickHouse Nullable column, so both become SQL NULL and only populated values
// compare as data.
type ContractRow = Record<string, unknown> & { readonly __rowId: string };
const contractRows: ReadonlyArray<ContractRow> = [
  { __rowId: 'status-absent', project: 'NCPI', note: 'queued' },
  { __rowId: 'status-null', project: 'NCPI', status: null, note: 'queued' },
  { __rowId: 'status-active', project: 'NCPI', status: 'active', note: 'queued' },
  { __rowId: 'status-pending', project: 'NCPI', status: 'pending', note: 'queued' },
  { __rowId: 'note-absent', project: 'NCPI', status: 'observed' },
  { __rowId: 'note-null', project: 'NCPI', status: 'recorded', note: null },
  { __rowId: 'note-observed', project: 'NCPI', status: 'entered', note: 'observed' },
];

// Independent of the serializer: absent property and explicit null collapse to
// NULL; the reader joins every clause with AND.
const matchesFilter = (row: ContractRow, filter: LoomOutputFilter): boolean => {
  const value = Object.prototype.hasOwnProperty.call(row, filter.column) ? row[filter.column] : null;
  switch (filter.op) {
    case 'EQ':
      return value !== null && value === filter.value;
    case 'IN':
      return value !== null && Array.isArray(filter.value) && filter.value.includes(value);
    case 'IS_NULL':
      return value === null;
    default:
      throw new Error(`row-membership oracle does not implement ${filter.op}`);
  }
};

const rowIdsFor = (filters: ReadonlyArray<LoomOutputFilter>): ReadonlyArray<string> =>
  contractRows.filter((row) => filters.every((filter) => matchesFilter(row, filter))).map((row) => row.__rowId);

const contractOutput: ExplorerRuntimeV1['outputs'][number] = {
  outputId: 'patients',
  name: 'patients',
  title: 'Patients',
  rowLabel: 'patient',
  selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
  columns: [
    { column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: false, sortable: true, chartable: false },
    { column: 'status', label: 'Status', logicalType: 'string', visible: true, order: 1, filterable: true, chartable: true },
    { column: 'note', label: 'Note', logicalType: 'string', visible: true, order: 2, filterable: true, chartable: false },
  ],
  table: { columns: [{ column: 'patient_id', label: 'Patient ID', visible: true }] },
  filters: [
    { column: 'status', label: 'Status', type: 'enum' },
    { column: 'note', label: 'Note', type: 'terms' },
  ],
  charts: [],
  fixedFilters: { project: ['NCPI'] },
};

const contractRuntime: ExplorerRuntimeV1 = {
  generation: 'generation-1',
  outputs: [contractOutput],
  sharedFilters: {},
  diagnostics: [],
};

const selectedFilters = (column: string, selection: ViewerFieldSelection): ReadonlyArray<LoomOutputFilter> => {
  const state = viewerReducer(createViewerReducerState(contractRuntime), {
    type: 'setFilter', outputId: 'patients', column, selection,
  });
  return outputFiltersFor(contractRuntime, state, contractOutput).filter((filter) => filter.column === column);
};

describe('Explorer Viewer request serialization', () => {
  it('combines fixed, local, and shared filters without exposing hidden columns', () => {
    let state = createViewerReducerState(runtime);
    state = viewerReducer(state, { type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection(['active']) });
    state = viewerReducer(state, { type: 'setSharedFilter', name: 'cohort', selection: valuesSelection(['reviewed']) });
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
    ]);
  });

  it('starts a fresh Viewer state without selected local or shared values while retaining configured fixed filters', () => {
    let selected = createViewerReducerState(runtime);
    selected = viewerReducer(selected, { type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection(['active']) });
    selected = viewerReducer(selected, { type: 'setSharedFilter', name: 'cohort', selection: valuesSelection(['reviewed']) });

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, selected, runtime.outputs[0]).filters).toEqual([
      { column: 'project', op: 'EQ', value: 'NCPI' },
      { column: 'status', op: 'EQ', value: 'reviewed' },
    ]);

    const reloaded = createViewerReducerState(runtime);

    expect(reloaded.outputs.patients.filterValues).toEqual({});
    expect(reloaded.sharedFilters).toEqual({});
    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, reloaded, runtime.outputs[0]).filters).toEqual([
      { column: 'project', op: 'EQ', value: 'NCPI' },
    ]);
  });

  it('requests filter and chart facets only when their controls are opened', () => {
    const initial = createViewerReducerState(runtime);
    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, initial, runtime.outputs[0]).facets).toEqual([]);
    const withFilter = viewerReducer(initial, {
      type: 'toggleFacet',
      facet: 'patients:filter:status',
    });
    const state = viewerReducer(withFilter, { type: 'toggleCharts', outputId: 'patients' });

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]).facets).toEqual([
      { name: 'loom:patients:status', kind: 'TERMS', column: 'status', size: 50, excludeSelfFilter: true },
      { name: 'loom:patients:chart:status', kind: 'TERMS', column: 'status', size: 12 },
    ]);
  });
});

describe('Explorer Viewer missing-value filter contract', () => {
  it('selects the absent property and the explicit null together as one IS_NULL bucket', () => {
    const filters = selectedFilters('status', missingSelection());

    expect(filters).toEqual([{ column: 'status', op: 'IS_NULL', value: {} }]);
    expect(rowIdsFor(filters)).toEqual(['status-absent', 'status-null']);
  });

  it('keeps each populated facet selection on its exact row IDs', () => {
    expect(rowIdsFor(selectedFilters('status', valuesSelection(['active'])))).toEqual(['status-active']);
    expect(rowIdsFor(selectedFilters('status', valuesSelection(['active', 'pending'])))).toEqual(['status-active', 'status-pending']);
    expect(rowIdsFor(selectedFilters('note', valuesSelection(['queued'])))).toEqual([
      'status-absent', 'status-null', 'status-active', 'status-pending',
    ]);
  });

  it('reports the missing bucket for a column whose nulls are explicit rather than absent', () => {
    const filters = selectedFilters('note', missingSelection());

    expect(filters).toEqual([{ column: 'note', op: 'IS_NULL', value: {} }]);
    expect(rowIdsFor(filters)).toEqual(['note-absent', 'note-null']);
  });

  it('does not fold the missing bucket into a populated selection, which the AND join would empty', () => {
    let state = viewerReducer(createViewerReducerState(contractRuntime), {
      type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection(['active']),
    });
    state = viewerReducer(state, {
      type: 'setFilter', outputId: 'patients', column: 'status', selection: missingSelection(),
    });
    const filters = outputFiltersFor(contractRuntime, state, contractOutput).filter((filter) => filter.column === 'status');

    expect(filters).toEqual([{ column: 'status', op: 'IS_NULL', value: {} }]);
    expect(rowIdsFor(filters)).not.toEqual([]);
    expect(rowIdsFor(filters)).toEqual(rowIdsFor(selectedFilters('status', missingSelection())));
  });

  it('carries a defined value so the JSON! filter input accepts the request', () => {
    for (const filter of selectedFilters('status', missingSelection())) {
      expect(filter.value).toBeDefined();
      expect(JSON.stringify({ column: filter.column, op: filter.op, value: filter.value }))
        .not.toContain('null');
    }
  });

  it('sends the missing bucket over GraphQL as a non-null IS_NULL value', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: {
        dataframeRows: {
          materialization: { id: 'mat-1', state: 'READY' },
          columns: ['status'], rows: [], totalCount: 0,
          pageInfo: { hasNextPage: false, endCursor: null },
        },
        dataframeAggregations: { aggregations: [] },
      },
    }), { status: 200 }));
    const state = viewerReducer(createViewerReducerState(contractRuntime), {
      type: 'setFilter', outputId: 'patients', column: 'status', selection: missingSelection(),
    });
    const request = outputRequestFor('NCPI_ACCEPTANCE', contractRuntime, state, contractOutput);

    await createLoomClient({ fetch }).queryOutput(request);

    const payload = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as {
      variables: { input: { filters: ReadonlyArray<LoomOutputFilter> }; facetInput?: { filters: ReadonlyArray<LoomOutputFilter> } };
    };
    for (const filters of [payload.variables.input.filters, payload.variables.facetInput?.filters ?? []]) {
      const status = filters.filter((filter) => filter.column === 'status');
      expect(status).toEqual([{ column: 'status', op: 'IS_NULL', value: {} }]);
      expect(status[0]?.value).not.toBeNull();
    }
  });

  it('emits no missing or value operator for an empty selection', () => {
    const state = viewerReducer(createViewerReducerState(contractRuntime), {
      type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection(['active']),
    });
    const cleared = viewerReducer(state, { type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection([]) });

    const filters = outputFiltersFor(contractRuntime, cleared, contractOutput);
    expect(filters).toEqual([{ column: 'project', op: 'EQ', value: 'NCPI' }]);
    expect(rowIdsFor(filters)).toEqual(contractRows.map((row) => row.__rowId));
  });
});
