// @vitest-environment jsdom

import React from 'react';
import { MantineProvider } from '@mantine/core';
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { LoomOutputResult } from '../../api';
import type { ExplorerRuntimeV1 } from '../../types';
import { createViewerReducerState } from './reducer';
import { OutputTable } from './components';

const tableDataInputs = vi.hoisted((): unknown[][] => []);

vi.mock('@tanstack/react-table', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-table')>();
  return {
    ...actual,
    useReactTable: (options: Parameters<typeof actual.useReactTable>[0]) => {
      tableDataInputs.push(options.data);
      return actual.useReactTable(options);
    },
  };
});

const runtime: ExplorerRuntimeV1 = {
  outputs: [{
    outputId: 'patients',
    name: 'patients',
    title: 'Patients',
    rowLabel: 'patient',
    selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
    columns: [{ column: 'id', label: 'ID', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false }],
    table: { columns: [{ column: 'id', label: 'ID', visible: true }] },
    filters: [],
    charts: [],
    fixedFilters: {},
  }],
  sharedFilters: {},
  diagnostics: [],
};

const result: LoomOutputResult = {
  columns: ['id'],
  rows: [{ id: 'patient-1' }],
  totalCount: 1,
  pageInfo: { hasNextPage: false },
  facets: [],
};

describe('Explorer Viewer output table', () => {
  it('keeps table data stable when result rows are unchanged', () => {
    const state = createViewerReducerState(runtime);
    const dispatch = vi.fn();
    const view = () => (
      <MantineProvider>
        <OutputTable
          runtime={runtime}
          output={runtime.outputs[0]}
          result={result}
          state={state}
          dispatch={dispatch}
        />
      </MantineProvider>
    );
    const { rerender } = render(view());
    const firstData = tableDataInputs.at(-1);

    rerender(view());

    expect(tableDataInputs.at(-1)).toBe(firstData);
  });
});
