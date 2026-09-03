// @vitest-environment jsdom

import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from './api';
import { LoomExplorerViewer } from './Viewer';

const state = {
  apiVersion: 'loom.calypr.org/explorer-state/v1', kind: 'ExplorerState', project: 'NCPI_ACCEPTANCE', explorerId: 'default', title: 'Clinical Explorer', management: 'interactive',
  draft: { version: 1, digest: 'digest' }, active: {}, generated: {}, activeUrl: '/viewer',
  runtime: {
    generation: 'generation-1', outputs: [{ outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient', selector: { recipe: 'r', translationVersion: 'v1', output: 'patients' }, columns: [{ column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: true, chartable: false }], table: { columns: [{ column: 'patient_id', label: 'Patient ID', visible: true }] }, filters: [], charts: [], fixedFilters: {}, actions: [] }], sharedFilters: {}, diagnostics: [],
  },
};

describe('Loom Explorer Viewer', () => {
  it('renders server rows and opens the public row details hook', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => {
      if (String(input).includes('/explorers/default')) return Promise.resolve(new Response(JSON.stringify(state), { status: 200 }));
      const body = JSON.parse(String(init?.body)) as { query?: string };
      if (body.query?.includes('dataframeRows')) {
        const payload = { data: { dataframeRows: { columns: ['patient_id'], rows: [['patient-1']], totalCount: 1, pageInfo: { hasNextPage: false } } } };
        return Promise.resolve(new Response(JSON.stringify(payload), { status: 200 }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    });
    const client = createLoomClient({ fetch });
    render(<LoomExplorerViewer client={client} project="NCPI_ACCEPTANCE" renderRowDetails={(row) => <span>Details for {String(row.patient_id)}</span>} />);
    await waitFor(() => expect(screen.getByText('patient-1')).toBeTruthy());
    expect(screen.getAllByText('Patients').length).toBeGreaterThan(0);
    const tableControls = screen.getByRole('navigation', { name: 'Table controls' });
    const table = screen.getByRole('table', { name: 'Patients results' });
    expect(tableControls.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'patient-1' }));
    expect(await screen.findByRole('dialog')).toHaveTextContent('Details for patient-1');
    fireEvent.click(screen.getByRole('button', { name: 'Close row details' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('exposes output tabs and updates the selected output', async () => {
    const multiOutputState = {
      ...state,
      runtime: {
        ...state.runtime,
        outputs: [
          state.runtime.outputs[0],
          {
            ...state.runtime.outputs[0],
            outputId: 'biospecimens',
            name: 'biospecimens',
            title: 'Biospecimens',
            rowLabel: 'biospecimen',
          },
        ],
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>((input) => {
      if (String(input).includes('/explorers/default')) return Promise.resolve(new Response(JSON.stringify(multiOutputState), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ data: { dataframeRows: { columns: ['patient_id'], rows: [['patient-1']], totalCount: 1, pageInfo: { hasNextPage: false } } } }), { status: 200 }));
    });

    render(<LoomExplorerViewer client={createLoomClient({ fetch })} project="NCPI_ACCEPTANCE" />);

    const tabList = await screen.findByRole('tablist', { name: 'Output tables' });
    expect(tabList).toBeTruthy();
    const patientsTab = screen.getByRole('tab', { name: /Patients/ });
    const biospecimensTab = screen.getByRole('tab', { name: /Biospecimens/ });
    expect(patientsTab).toHaveAttribute('aria-selected', 'true');
    expect(patientsTab).toHaveAttribute('data-active');
    expect(biospecimensTab).toHaveAttribute('aria-selected', 'false');
    expect(biospecimensTab).not.toHaveAttribute('data-active');
    for (const tab of [patientsTab, biospecimensTab]) {
      const panelId = tab.getAttribute('aria-controls');
      expect(panelId).toBeTruthy();
      expect(document.getElementById(panelId ?? '')).toHaveAttribute('role', 'tabpanel');
    }

    patientsTab.focus();
    fireEvent.keyDown(patientsTab, { key: 'ArrowRight' });

    await waitFor(() => {
      expect(patientsTab).toHaveAttribute('aria-selected', 'false');
      expect(patientsTab).not.toHaveAttribute('data-active');
      expect(biospecimensTab).toHaveAttribute('aria-selected', 'true');
      expect(biospecimensTab).toHaveAttribute('data-active');
    });
  });

  it('selects visible facet values without opening a generic dropdown', async () => {
    const filterState = {
      ...state,
      runtime: {
        ...state.runtime,
        outputs: state.runtime.outputs.map((output) => ({
          ...output,
          filters: [{ column: 'patient_id', label: 'Patient ID', type: 'terms' }],
        })),
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => {
      if (String(input).includes('/explorers/default')) return Promise.resolve(new Response(JSON.stringify(filterState), { status: 200 }));
      const payload = {
        data: {
          dataframeRows: { columns: ['patient_id'], rows: [['patient-1']], totalCount: 1, pageInfo: { hasNextPage: false } },
          dataframeAggregations: { aggregations: [{ name: 'loom:patients:patient_id', kind: 'TERMS', columns: ['key', 'doc_count'], rows: [['patient-1', 1]] }] },
        },
      };
      return Promise.resolve(new Response(JSON.stringify(payload), { status: 200 }));
    });

    render(<LoomExplorerViewer client={createLoomClient({ fetch })} project="NCPI_ACCEPTANCE" />);

    const value = await screen.findByRole('checkbox', { name: /patient-1/ });
    fireEvent.click(value);
    await waitFor(() => expect((value as HTMLInputElement).checked).toBe(true));
    await waitFor(() => {
      const graphQLCalls = fetch.mock.calls.filter(([input]) => String(input).includes('/graphql/graph'));
      expect(graphQLCalls.length).toBeGreaterThan(1);
      const request = JSON.parse(String(graphQLCalls.at(-1)?.[1]?.body)) as { variables: { input: { filters?: unknown } } };
      expect(request.variables.input.filters).toEqual([{ column: 'patient_id', op: 'EQ', value: 'patient-1' }]);
    });
  });

  it('routes named runtime actions through the host adapter', async () => {
    const actionState = {
      ...state,
      runtime: {
        ...state.runtime,
        outputs: state.runtime.outputs.map((output) => ({
          ...output,
          actions: [{ type: 'open-study', title: 'Open study' }],
        })),
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>((input) => {
      if (String(input).includes('/explorers/default')) return Promise.resolve(new Response(JSON.stringify(actionState), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ data: { dataframeRows: { columns: ['patient_id'], rows: [['patient-1']], totalCount: 1, pageInfo: { hasNextPage: false } } } }), { status: 200 }));
    });
    const openStudy = vi.fn();

    render(<LoomExplorerViewer client={createLoomClient({ fetch })} project="NCPI_ACCEPTANCE" customActions={{ 'open-study': openStudy }} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Open study' }));
    await waitFor(() => expect(openStudy).toHaveBeenCalledTimes(1));
    expect(openStudy.mock.calls[0]?.[0]).toMatchObject({
      project: 'NCPI_ACCEPTANCE',
      output: { outputId: 'patients' },
      action: { type: 'open-study' },
    });
  });

  it('exports the configured output, columns, and headers for built-in actions', async () => {
    const actionState = {
      ...state,
      runtime: {
        ...state.runtime,
        outputs: [
          {
            ...state.runtime.outputs[0],
            actions: [{
              type: 'download-related',
              title: 'Download related',
              output: 'biospecimens',
              columns: ['sample_id'],
              exportHeaders: { sample_id: 'Sample ID' },
            }],
          },
          {
            ...state.runtime.outputs[0],
            outputId: 'biospecimens',
            name: 'biospecimens',
            title: 'Biospecimens',
            rowLabel: 'biospecimen',
            selector: { recipe: 'r', translationVersion: 'v1', output: 'biospecimens' },
            columns: [{ column: 'sample_id', label: 'Sample ID', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false }],
            table: { columns: [{ column: 'sample_id', label: 'Sample ID', visible: true }] },
            actions: [],
          },
        ],
      },
    };
    const fetch = vi.fn<typeof globalThis.fetch>((input) => {
      if (String(input).includes('/explorers/default')) return Promise.resolve(new Response(JSON.stringify(actionState), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ data: { dataframeRows: { columns: ['patient_id'], rows: [['patient-1']], totalCount: 1, pageInfo: { hasNextPage: false } } } }), { status: 200 }));
    });
    const client = createLoomClient({ fetch });
    const exportOutput = vi.spyOn(client, 'exportOutput').mockResolvedValue(new Blob(['Sample ID\nsample-1']));
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:test') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);

    render(<LoomExplorerViewer client={client} project="NCPI_ACCEPTANCE" />);

    fireEvent.click(await screen.findByRole('button', { name: 'Download related' }));
    await waitFor(() => expect(exportOutput).toHaveBeenCalledTimes(1));
    expect(exportOutput.mock.calls[0]?.[0]).toMatchObject({
      selector: { output: 'biospecimens' },
      columns: ['sample_id'],
      exportHeaders: { sample_id: 'Sample ID' },
    });
  });
});
