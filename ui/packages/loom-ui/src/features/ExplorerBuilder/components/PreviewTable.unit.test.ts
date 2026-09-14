// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type {
  ExplorerBuilderPreviewResult,
  ExplorerBuilderColumn,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import {
  formatPreviewCell,
  PreviewTable,
  previewCellTitle,
} from './PreviewTable';

const column = (
  name: string,
  label: string,
  order: number,
): ExplorerBuilderColumn => ({
  column: name,
  label,
  occurrenceId: 'base',
  source: { kind: 'field', fieldPath: name, projectionMode: 'FIRST' },
  table: { visible: true, order },
});

const firstColumn = column('first_column', 'First column', 0);
const secondColumn = column('second_column', 'Second column', 1);
const table: DraftTable = {
  outputId: 'Specimen',
  tabId: 'specimen',
  title: 'Specimen',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'Specimen', title: 'Specimen' },
    rootResourceType: 'Specimen',
    route: { occurrenceId: 'base', resourceType: 'Specimen' },
    columns: [firstColumn, secondColumn],
  },
};
const preview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  receiptId: 'receipt_preview',
  outputId: 'Specimen',
  columns: [firstColumn, secondColumn].map((value) => ({
    column: value.column,
    label: value.label,
    logicalType: 'string',
    filterable: true,
    chartable: true,
  })),
  rows: [{ first_column: 'one', second_column: 'two' }],
  rowCount: 1,
  diagnostics: [],
};

describe('formatPreviewCell', () => {
  it('preserves scalar values', () => {
    expect(formatPreviewCell('Tissue')).toBe('Tissue');
    expect(formatPreviewCell(25)).toBe('25');
    expect(formatPreviewCell(false)).toBe('false');
    expect(formatPreviewCell(null)).toBe('—');
  });

  it('renders common structured FHIR values as compact human text', () => {
    expect(formatPreviewCell({ reference: 'Patient/example' })).toBe(
      'Patient/example',
    );
    expect(formatPreviewCell([{ use: 'official', value: 'ABC' }])).toBe('ABC');
    expect(
      formatPreviewCell({
        coding: [{ code: 'fix', display: 'Fixation', system: 'example' }],
        text: 'Fixation',
      }),
    ).toBe('Fixation');
    expect(
      formatPreviewCell({
        bodySite: { reference: { reference: 'BodyStructure/example' } },
        collector: { reference: 'Practitioner/example' },
      }),
    ).toBe('bodySite: BodyStructure/example · collector: Practitioner/example');
  });

  it('keeps the lossless JSON value available for the cell tooltip', () => {
    expect(previewCellTitle({ reference: 'Patient/example' })).toBe(
      '{"reference":"Patient/example"}',
    );
  });
});

describe('PreviewTable column controls', () => {
  it('reports row-limit changes to the preview owner', () => {
    const onLimitChange = vi.fn();
    render(
      React.createElement(PreviewTable, {
        preview,
        table,
        limit: 25,
        onLimitChange,
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    fireEvent.change(screen.getByRole('combobox'), {
      target: { value: '1000' },
    });
    expect(onLimitChange).toHaveBeenCalledWith(1000);
  });

  it('uses scrollable table and selector surfaces', () => {
    render(
      React.createElement(PreviewTable, {
        preview,
        table,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByTestId('preview-table-scroll')).toHaveClass(
      'overflow-auto',
      'max-h-[min(65dvh,40rem)]',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    expect(screen.getByRole('list', { name: 'Table columns' })).toHaveClass(
      'overflow-y-auto',
    );
    expect(
      screen.getByText(/Drag rows to change table order/),
    ).toBeInTheDocument();
    fireEvent.pointerDown(document.body);
    expect(
      screen.queryByRole('list', { name: 'Table columns' }),
    ).not.toBeInTheDocument();
  });

  it('toggles visibility and drag-reorders columns from the selector', () => {
    const onColumnChange = vi.fn();
    const onColumnsChange = vi.fn();
    render(
      React.createElement(PreviewTable, {
        preview,
        table,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange,
        onColumnsChange,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));

    fireEvent.click(screen.getByRole('checkbox', { name: 'First column' }));
    expect(onColumnChange).toHaveBeenCalledWith(
      expect.objectContaining({
        column: 'first_column',
        table: { visible: false, order: 0 },
      }),
    );

    onColumnChange.mockClear();
    const dataTransfer = {
      effectAllowed: 'move',
      setData: vi.fn(),
      getData: vi.fn(() => 'second_column'),
    };
    fireEvent.dragStart(screen.getByLabelText('Drag Second column'), {
      dataTransfer,
    });
    const firstRow = screen.getByLabelText('Drag First column').parentElement;
    expect(firstRow).not.toBeNull();
    fireEvent.dragOver(firstRow!, { clientY: 0, dataTransfer });
    fireEvent.drop(firstRow!, { clientY: 0, dataTransfer });

    expect(onColumnChange).not.toHaveBeenCalled();
    expect(onColumnsChange).toHaveBeenCalledTimes(1);
    expect(onColumnsChange).toHaveBeenCalledWith([
      expect.objectContaining({
        column: 'second_column',
        table: expect.objectContaining({ order: 0 }),
      }),
      expect.objectContaining({
        column: 'first_column',
        table: expect.objectContaining({ order: 1 }),
      }),
    ]);
  });

  it('renders a bounded cell window for a wide preview', () => {
    const wideColumns = Array.from({ length: 200 }, (_, index) =>
      column(`column_${index}`, `Column ${index}`, index),
    );
    const widePreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: wideColumns.map((value) => ({
        column: value.column,
        label: value.label,
        logicalType: 'string',
        filterable: true,
        chartable: true,
      })),
      rows: Array.from({ length: 1000 }, () =>
        Object.fromEntries(wideColumns.map((value) => [value.column, 'value'])),
      ),
    };
    render(
      React.createElement(PreviewTable, {
        preview: widePreview,
        table: { ...table, document: { ...table.document, columns: wideColumns } },
        limit: 1000,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getAllByRole('cell').length).toBeLessThanOrEqual(1500);
  });

  it('renders indexed physical columns under one authored column', () => {
    const given: ExplorerBuilderColumn = {
      ...column('given', 'Given names', 0),
      source: {
        kind: 'field',
        fieldPath: 'name[].given[]',
        projectionMode: 'INDEXED',
      },
    };
    const indexedPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [
        {
          column: 'given__0__0',
          authoredColumns: ['given'],
          label: 'Given names [0] [0]',
          logicalType: 'string',
          filterable: true,
          chartable: true,
        },
        {
          column: 'given__0__1',
          authoredColumns: ['given'],
          label: 'Given names [0] [1]',
          logicalType: 'string',
          filterable: true,
          chartable: true,
        },
        {
          column: 'name__count',
          authoredColumns: ['given'],
          label: 'name__count',
          logicalType: 'integer',
          filterable: false,
          chartable: false,
        },
      ],
      rows: [
        {
          given__0__0: 'Ada',
          given__0__1: 'Augusta',
          name__count: 1,
        },
      ],
    };

    render(
      React.createElement(PreviewTable, {
        preview: indexedPreview,
        table: {
          ...table,
          document: { ...table.document, columns: [given] },
        },
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '3');
    expect(screen.getByRole('columnheader', { name: 'Given names [0] [0]' })).toBeInTheDocument();
    expect(screen.getByText('Ada')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByRole('checkbox', { name: 'Given names' })).toHaveProperty(
      'checked',
      true,
    );
  });

  it('shows a shared count when any authored owner is visible', () => {
    const hiddenGiven: ExplorerBuilderColumn = {
      ...column('given', 'Given names', 0),
      table: { visible: false, order: 0 },
    };
    const visibleFamily = column('family', 'Family names', 1);
    const sharedCountPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [
        {
          column: 'name__count',
          authoredColumns: ['given', 'family'],
          label: 'name__count',
          logicalType: 'integer',
          filterable: false,
          chartable: false,
        },
      ],
      rows: [{ name__count: 2 }],
    };

    render(
      React.createElement(PreviewTable, {
        preview: sharedCountPreview,
        table: {
          ...table,
          document: {
            ...table.document,
            columns: [hiddenGiven, visibleFamily],
          },
        },
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '1');
    expect(screen.getByText('2')).toBeInTheDocument();
  });
});
