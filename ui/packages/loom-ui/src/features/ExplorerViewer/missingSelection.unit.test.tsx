// @vitest-environment jsdom
// Literal/missing collision contracts for the published Viewer: a value spelled exactly
// like the missing bucket label stays data (EQ/IN), and only the tagged bucket emits
// IS_NULL. Table and ordinary CSV export consume the same request.
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createLoomClient, type LoomOutputFilter, type LoomOutputRequest } from '../../api';
import { LoomExplorerViewer } from '../../Viewer';
import type { ExplorerRuntimeV1 } from '../../types';
import { clearedSelection, missingSelection, valuesSelection } from './model';
import { createViewerReducerState, viewerReducer } from './reducer';
import { MISSING_VALUE, MISSING_VALUE_DESCRIPTION, outputRequestFor } from './serialization';

type CapturedFilter = Pick<LoomOutputFilter, 'column' | 'op' | 'value'>;

const runtime: ExplorerRuntimeV1 = {
  generation: 'generation-1',
  publication: { state: 'READY', revisionId: 'revision-1' },
  outputs: [
    {
      outputId: 'patients',
      name: 'patients',
      title: 'Patients',
      rowLabel: 'patient',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
      columns: [
        { column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false },
        { column: 'status', label: 'Status', logicalType: 'string', visible: true, order: 1, filterable: true, chartable: false },
      ],
      table: { columns: [{ column: 'patient_id', label: 'Patient ID', visible: true }, { column: 'status', label: 'Status', visible: true }] },
      filters: [{ column: 'status', label: 'Status', type: 'terms' }],
      charts: [],
      fixedFilters: {},
      actions: [{ type: 'loom:export-csv', title: 'Export CSV', fileName: 'patients.csv' }],
    },
  ],
  sharedFilters: { cohort: [{ outputId: 'patients', column: 'status', label: 'Cohort' }] },
  diagnostics: [],
};

const statusFacet = (literalTerm: boolean): Record<string, unknown> => ({
  name: 'loom:patients:status',
  kind: 'TERMS',
  columns: ['key', 'doc_count'],
  rows: [
    { key: 'active', doc_count: 3 },
    { key: 'pending', doc_count: 2 },
    ...(literalTerm ? [{ key: MISSING_VALUE, doc_count: 1 }] : []),
  ],
  missingCount: 5,
});

const harness = (options: { readonly literalTerm?: boolean } = {}) => {
  const inputs: Array<Record<string, unknown>> = [];
  const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const payload = JSON.parse(String(init?.body)) as {
      variables: { input: Record<string, unknown> };
    };
    inputs.push(payload.variables.input);
    return new Response(JSON.stringify({
      data: {
        dataframeRows: {
          materialization: { id: 'mat-1', state: 'READY' },
          columns: ['patient_id', 'status'],
          rows: [{ patient_id: 'p-1', status: 'active' }],
          totalCount: 12,
          pageInfo: { hasNextPage: true, endCursor: 'cursor-1' },
        },
        dataframeAggregations: { aggregations: [statusFacet(options.literalTerm ?? false)] },
      },
    }), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  const client = createLoomClient({ baseUrl: 'http://loom.test', fetch: fetcher as typeof fetch });
  vi.spyOn(client, 'getExplorer').mockResolvedValue(runtime);
  const exportOutput = vi.spyOn(client, 'exportOutput').mockResolvedValue(new Blob(['patient_id,status\np-1,active\n'], { type: 'text/csv' }));
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:loom-export');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined);
  vi.spyOn(HTMLElement.prototype, 'click').mockImplementation(() => undefined);
  render(<LoomExplorerViewer project="NCPI_ACCEPTANCE" explorerId="default" client={client} />);
  const statusFilters = (index: number): ReadonlyArray<CapturedFilter> =>
    ((inputs[index]?.filters ?? []) as ReadonlyArray<CapturedFilter>).filter((filter) => filter.column === 'status');
  // Each distinct request identity loads exactly once, so a new GraphQL input means
  // the new selection landed in the request the table is rendered from. The baseline
  // is the last verified request rather than the count at call time: an undebounced
  // selection can already have fired its request before this helper runs.
  let verified = -1;
  const expectStatusFilters = async (expected: ReadonlyArray<CapturedFilter>): Promise<number> => {
    await waitFor(() => {
      expect(inputs.length).toBeGreaterThan(verified);
      expect(statusFilters(inputs.length - 1)).toEqual(expected);
    });
    verified = inputs.length - 1;
    return verified;
  };
  const openFilterSection = async (name: string): Promise<HTMLElement> => {
    const rail = await screen.findByRole('complementary', { name: 'Explorer filters' });
    const section = within(rail).getByRole('region', { name });
    fireEvent.click(within(section).getByRole('button', { name: 'Load values' }));
    await within(section).findByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(missing or null\\)`) });
    return section;
  };
  return { client, exportOutput, inputs, statusFilters, expectStatusFilters, openFilterSection };
};

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const withFixedFilters = (fixedFilters: Readonly<Record<string, ReadonlyArray<string>>>): ExplorerRuntimeV1['outputs'][number] =>
  ({ ...runtime.outputs[0], fixedFilters });

describe('authored fixed filters stay literal data', () => {
  it('emits EQ for a singleton spelled exactly like the missing label, and IN that retains both with active', () => {
    const state = createViewerReducerState(runtime);

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, withFixedFilters({ status: [MISSING_VALUE] })).filters)
      .toEqual([{ column: 'status', op: 'EQ', value: MISSING_VALUE }]);

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, withFixedFilters({ status: [MISSING_VALUE, 'active'] })).filters)
      .toEqual([{ column: 'status', op: 'IN', value: [MISSING_VALUE, 'active'] }]);
  });

  it('keeps other literals on the existing EQ and IN behavior', () => {
    const state = createViewerReducerState(runtime);

    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, withFixedFilters({ status: ['active'], project: ['NCPI', 'OTHER'] })).filters)
      .toEqual([
        { column: 'status', op: 'EQ', value: 'active' },
        { column: 'project', op: 'IN', value: ['NCPI', 'OTHER'] },
      ]);
  });
});

describe('tagged missing bucket beside a colliding facet term', () => {
  it('keeps the colliding literal visible and separately selectable while the bucket emits IS_NULL with a non-null placeholder', async () => {
    const view = harness({ literalTerm: true });
    const section = await view.openFilterSection('Status');

    const literal = within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(exact value\\)`) });
    const bucket = within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(missing or null\\)`) });
    expect(literal).not.toBe(bucket);
    expect(within(section).getByRole('checkbox', { name: /^active/ })).toBeTruthy();
    expect(within(section).getByText(MISSING_VALUE_DESCRIPTION)).toBeTruthy();

    fireEvent.click(literal);
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: MISSING_VALUE }]);

    fireEvent.click(literal);
    await view.expectStatusFilters([]);

    fireEvent.click(bucket);
    await view.expectStatusFilters([{ column: 'status', op: 'IS_NULL', value: {} }]);
    const request = view.inputs[view.inputs.length - 1];
    const emitted = ((request?.filters ?? []) as ReadonlyArray<CapturedFilter>).find((filter) => filter.column === 'status');
    expect(emitted?.value).not.toBeNull();
    expect(literal).toHaveProperty('checked', false);
  });

  it('keeps a populated selection that contains the label text as IN, never IS_NULL', async () => {
    const view = harness({ literalTerm: true });
    const section = await view.openFilterSection('Status');

    fireEvent.click(within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(exact value\\)`) }));
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: MISSING_VALUE }]);
    fireEvent.click(within(section).getByRole('checkbox', { name: /^active/ }));
    await view.expectStatusFilters([{ column: 'status', op: 'IN', value: [MISSING_VALUE, 'active'] }]);
  });
});

describe('missing and populated choices stay mutually exclusive', () => {
  it('replaces populated values with the bucket and back, and preserves page reset and clear', async () => {
    const view = harness();
    const section = await view.openFilterSection('Status');
    const bucket = within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(missing or null\\)`) });
    const active = within(section).getByRole('checkbox', { name: /^active/ });
    const pending = within(section).getByRole('checkbox', { name: /^pending/ });

    fireEvent.click(active);
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: 'active' }]);
    fireEvent.click(pending);
    await view.expectStatusFilters([{ column: 'status', op: 'IN', value: ['active', 'pending'] }]);

    // The reader ANDs clauses with no OR group, so the bucket replaces populated values.
    fireEvent.click(bucket);
    await view.expectStatusFilters([{ column: 'status', op: 'IS_NULL', value: {} }]);
    expect(active).toHaveProperty('checked', false);
    expect(pending).toHaveProperty('checked', false);
    expect(bucket).toHaveProperty('checked', true);

    fireEvent.click(active);
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: 'active' }]);
    expect(bucket).toHaveProperty('checked', false);

    // Advance to page two, then any selection change returns to the first page.
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => {
      const latest = view.inputs[view.inputs.length - 1];
      expect(latest).toHaveProperty('after', 'cursor-1');
    });

    fireEvent.click(bucket);
    const pageReset = await view.expectStatusFilters([{ column: 'status', op: 'IS_NULL', value: {} }]);
    expect(view.inputs[pageReset]).not.toHaveProperty('after');

    fireEvent.click(within(section).getByRole('button', { name: 'Clear' }));
    await view.expectStatusFilters([]);
    expect(bucket).toHaveProperty('checked', false);
  });

  it('swaps the shared bucket and shared populated values only through confirmation', async () => {
    const view = harness();
    const section = await view.openFilterSection('cohort');
    const bucket = within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(missing or null\\)`) });
    const active = within(section).getByRole('checkbox', { name: /^active/ });

    fireEvent.click(bucket);
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Apply cohort to every linked output');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Apply filter' }));
    await view.expectStatusFilters([{ column: 'status', op: 'IS_NULL', value: {} }]);
    expect(bucket).toHaveProperty('checked', true);

    fireEvent.click(active);
    fireEvent.click(await screen.findByRole('button', { name: 'Apply filter' }));
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: 'active' }]);
    expect(bucket).toHaveProperty('checked', false);

    fireEvent.click(within(section).getByRole('button', { name: 'Clear' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Apply filter' }));
    await view.expectStatusFilters([]);
    expect(bucket).toHaveProperty('checked', false);
  });

  it('keeps local and shared state literal-safe at the reducer and request level', () => {
    let state = viewerReducer(createViewerReducerState(runtime), {
      type: 'setFilter', outputId: 'patients', column: 'status', selection: valuesSelection([MISSING_VALUE, 'active']),
    });
    expect(state.outputs.patients.filterValues.status).toEqual(valuesSelection([MISSING_VALUE, 'active']));
    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]).filters)
      .toEqual([{ column: 'status', op: 'IN', value: [MISSING_VALUE, 'active'] }]);

    state = viewerReducer(state, { type: 'setSharedFilter', name: 'cohort', selection: missingSelection() });
    expect(state.sharedFilters.cohort).toEqual({ kind: 'missing' });
    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]).filters)
      .toEqual([{ column: 'status', op: 'IS_NULL', value: {} }]);

    state = viewerReducer(state, { type: 'setSharedFilter', name: 'cohort', selection: clearedSelection() });
    expect(state.sharedFilters).toEqual({});
    expect(outputRequestFor('NCPI_ACCEPTANCE', runtime, state, runtime.outputs[0]).filters)
      .toEqual([{ column: 'status', op: 'IN', value: [MISSING_VALUE, 'active'] }]);
  });
});

describe('table and ordinary CSV export share one request', () => {
  const exportRequestOf = (): LoomOutputRequest => {
    const call = view_exportCall();
    if (!call) throw new Error('Expected the Viewer to export through the client.');
    return call[0];
  };
  let view_exportCall: () => readonly [LoomOutputRequest, AbortSignal?] | undefined;

  it('exports the same IS_NULL semantics the table is showing, without a page cursor', async () => {
    const view = harness();
    view_exportCall = () => view.exportOutput.mock.calls[0];
    const section = await view.openFilterSection('Status');
    fireEvent.click(within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(missing or null\\)`) }));
    await view.expectStatusFilters([{ column: 'status', op: 'IS_NULL', value: {} }]);

    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));
    await waitFor(() => { expect(view.exportOutput).toHaveBeenCalledTimes(1); });
    const request = exportRequestOf();
    expect(request.filters?.filter((filter) => filter.column === 'status')).toEqual([{ column: 'status', op: 'IS_NULL', value: {} }]);
    // No cursor: the export follows the full first-page table request, and JSON
    // serialization drops the undefined key before it reaches the wire.
    expect(request.after).toBeUndefined();
    expect(request.columns).toEqual(['patient_id', 'status']);
  });

  it('exports a literal equal to the label as EQ, in lockstep with the table request', async () => {
    const view = harness({ literalTerm: true });
    view_exportCall = () => view.exportOutput.mock.calls[0];
    const section = await view.openFilterSection('Status');
    fireEvent.click(within(section).getByRole('checkbox', { name: new RegExp(`^${MISSING_VALUE} \\(exact value\\)`) }));
    await view.expectStatusFilters([{ column: 'status', op: 'EQ', value: MISSING_VALUE }]);

    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));
    await waitFor(() => { expect(view.exportOutput).toHaveBeenCalledTimes(1); });
    const request = exportRequestOf();
    expect(request.filters?.filter((filter) => filter.column === 'status')).toEqual([{ column: 'status', op: 'EQ', value: MISSING_VALUE }]);
  });
});
