// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type {
  Construction,
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
  source: { kind: 'field', field: { path: name, projectionMode: 'FIRST' } },
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
    rows: { kind: 'RECORDS', records: {} },
    columns: [firstColumn, secondColumn],
  },
};
const preview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
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
const specimenSourceColumns = [
  { ...column('specimen_id', 'Specimen ID', 0), columnId: 'specimen-id' },
  { ...column('subject_reference', 'Subject reference', 1), columnId: 'subject-reference' },
  { ...column('specimen_status', 'Specimen status', 2), columnId: 'specimen-status' },
  { ...column('collection_date', 'Collection date', 3), columnId: 'collection-date' },
];
const relatedPatientConstruction: Construction = {
  version: 1,
  steps: [{
    id: 'related_patient_id',
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'RELATED_SOURCE',
      relatedSource: {
        anchorColumnId: 'subject-reference',
        choiceId: 'Specimen.subject_Patient',
        sourceOccurrenceId: 'base',
        source: {
          kind: 'FIELD',
          candidateId: 'patient-id',
          nodeId: 'patient-node',
          resourceType: 'Patient',
          path: 'id',
          cardinality: 'required_one',
          logicalType: 'string',
        },
        route: [{
          edgeId: 'Specimen.subject_Patient',
          fromNodeId: 'specimen-node',
          toNodeId: 'patient-node',
          fromResourceType: 'Specimen',
          toResourceType: 'Patient',
          relationship: 'subject',
          storageDirection: 'OUTBOUND',
          matchMode: 'OPTIONAL',
        }],
        contributorRule: { policy: 'ALL_MATCHES' },
        form: 'ALL',
        outputColumnId: 'patient-id',
      },
    },
    outputs: [...specimenSourceColumns.map((sourceColumn) => ({
      id: sourceColumn.columnId, name: sourceColumn.column, label: sourceColumn.label, type: 'string',
    })), {
      id: 'patient-id',
      name: 'patient_id',
      label: 'Patient ID',
      type: 'string',
      nullable: true,
    }],
  }],
};
const relatedPatientPreview: ExplorerBuilderPreviewResult = {
  ...preview,
  columns: [
    ...specimenSourceColumns.map((sourceColumn) => ({
      column: sourceColumn.column,
      label: sourceColumn.label,
      logicalType: 'string',
      filterable: true,
      chartable: false,
    })),
    {
      column: 'patient_id',
      label: 'Patient ID',
      logicalType: 'string',
      filterable: true,
      chartable: false,
    },
  ],
  rows: [{
    specimen_id: 'specimen-1',
    subject_reference: 'Patient/patient-42',
    specimen_status: 'available',
    collection_date: '2024-01-01',
    patient_id: 'patient-42',
  }],
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

  it('shares scalar, FHIR, and array display policy with Viewer', () => {
    expect(formatPreviewCell('  Tissue  ')).toBe('Tissue');
    expect(formatPreviewCell({ text: 'Fixation' })).toBe('Fixation');
    expect(formatPreviewCell({ coding: [{ code: 'fix' }] })).toBe('fix');
    expect(formatPreviewCell([null, 'active', { display: 'Ready' }])).toBe('active; Ready');
    expect(formatPreviewCell({ nested: { value: 'sample' } })).toBe('nested: sample');
  });
});

describe('PreviewTable construction output order', () => {
  it('keeps Pivot lineage columns from inheriting the source column presentation order', () => {
    const source = { ...column('specimen_id', 'Specimen ID', 0), columnId: 'specimen-id' };
    const outputs = [
      { id: 'specimen-id', name: 'specimen_id', label: 'Specimen ID', type: 'string' },
      { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' },
      { id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string' },
      { id: 'text-id', name: 'text', label: 'Text', type: 'string' },
      { id: 'null-id', name: 'null_value', label: 'Null', type: 'decimal' },
      { id: 'd-id', name: 'd', label: 'd', type: 'decimal' },
    ];
    const pivotConstruction: Construction = {
      version: 1,
      steps: [{
        id: 'pivot',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: {
          kind: 'PIVOT',
          pivot: {
            constructionId: 'pivot',
            groupKeyIds: ['specimen-id', 'patient-id', 'observation-id', 'text-id'],
            categoryColumnId: 'unit',
            valueColumnId: 'value',
            categories: [
              { key: { kind: 'NULL' }, outputColumnId: 'null-id' },
              { key: { kind: 'STRING', string: 'd' }, outputColumnId: 'd-id' },
            ],
            duplicatePolicy: 'ERROR',
            missingCellPolicy: 'NULL',
            unlistedCategoryPolicy: 'ERROR',
          },
        },
        outputs,
      }],
    };
    const pivotTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [source],
        construction: pivotConstruction,
      },
    };
    const pivotPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: outputs.map((output) => ({
        column: output.name,
        label: output.label,
        logicalType: output.type,
        filterable: true,
        chartable: true,
        ...(output.name === 'null_value' || output.name === 'd'
          ? { authoredColumns: ['specimen_id'] }
          : output.name === 'specimen_id' ? { authoredColumns: ['specimen_id'] } : {}),
      })),
      rows: [{ specimen_id: 's1', patient_id: 'p1', observation_id: 'o1', text: 't', null_value: 1, d: 2 }],
    };

    const onColumnsChange = vi.fn();
    render(React.createElement(PreviewTable, {
      preview: pivotPreview,
      table: pivotTable,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange: vi.fn(),
      onColumnsChange,
    }));

    expect(screen.getAllByRole('columnheader').map((header) => header.textContent)).toEqual([
      'Specimen ID', 'Patient ID', 'Observation ID', 'Text', 'Null', 'd',
    ]);

    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    const dataTransfer = {
      effectAllowed: 'move',
      setData: vi.fn(),
      getData: vi.fn(() => 'null_value'),
    };
    fireEvent.dragStart(screen.getByLabelText('Drag Null'), { dataTransfer });
    const firstRow = screen.getByLabelText('Drag Specimen ID').parentElement;
    expect(firstRow).not.toBeNull();
    fireEvent.dragOver(firstRow!, { clientY: 0, dataTransfer });
    fireEvent.drop(firstRow!, { clientY: 0, dataTransfer });

    expect(onColumnsChange).toHaveBeenCalledWith(expect.arrayContaining([
      expect.objectContaining({
        kind: 'AUTHORED_COLUMN',
        column: expect.objectContaining({
          column: 'specimen_id',
          table: expect.objectContaining({ order: 1 }),
        }),
      }),
      expect.objectContaining({
        kind: 'CONSTRUCTION_OUTPUT',
        stepId: 'pivot',
        column: expect.objectContaining({
          id: 'specimen-id',
          table: expect.objectContaining({ order: 1 }),
        }),
      }),
    ]));
  });
});

describe('PreviewTable column controls', () => {
  it('renders compiler-generated cohort columns without authored column entries', () => {
    render(React.createElement(PreviewTable, {
      preview: {
        ...preview,
        columns: [
          { column: 'group_label', label: 'Group label', logicalType: 'string', filterable: true, chartable: true },
          { column: 'group_ordinal', label: 'Group ordinal', logicalType: 'integer', filterable: true, chartable: true },
        ],
        rows: [{ group_label: 'Tumor cohort', group_ordinal: 0 }],
      },
      table,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange: vi.fn(),
      onColumnsChange: vi.fn(),
    }));
    expect(screen.getByRole('columnheader', { name: /Group label/ })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: /Group ordinal/ })).toBeInTheDocument();
    expect(screen.getByText('Tumor cohort')).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: /First column/ })).not.toBeInTheDocument();
  });

  it('lets a researcher inspect a saved preview row identity', () => {
    render(React.createElement(PreviewTable, {
      preview: { ...preview, rows: [{ ...preview.rows![0], __loom_row_id: 'stable-specimen-row' }] },
      table,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange: vi.fn(),
      onColumnsChange: vi.fn(),
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(screen.getByRole('dialog', { name: 'Row 1 identity' })).toHaveTextContent('stable-specimen-row');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog', { name: 'Row 1 identity' })).not.toBeInTheDocument();
  });


  it('shows a compound cohort identity without requesting unavailable lineage', () => {
    const onRowLineage = vi.fn();
    render(React.createElement(PreviewTable, {
      preview: {
        ...preview,
        rows: [{ first_column: 'one', __loom_row_id: { group_id: 'cohort-a', group_revision_id: 'revision-1' } }],
        rowSources: [{ kind: 'COMPOSITE' }],
      },
      table,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange: vi.fn(),
      onColumnsChange: vi.fn(),
      onRowLineage,
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(screen.getByRole('dialog', { name: 'Row 1 identity' })).toHaveTextContent('cohort-a');
    expect(screen.getByRole('dialog', { name: 'Row 1 identity' })).toHaveTextContent('revision-1');
    expect(onRowLineage).not.toHaveBeenCalled();
  });

  it('uses preview row-source evidence without requiring a visible ID column', () => {
    const sourcePreview: ExplorerBuilderPreviewResult = {
      ...preview,
      rows: [{ first_column: 'one', second_column: 'two', __loom_row_id: 'stable-specimen-row' }],
      rowSources: [{ kind: 'SINGLE', resourceType: 'Specimen', id: 'specimen-1' }],
    };
    const props = { preview: sourcePreview, table, limit: 25, onLimitChange: vi.fn(), onColumnChange: vi.fn(), onColumnsChange: vi.fn() };
    const view = render(React.createElement(PreviewTable, props));
    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(screen.getByText('Starting FHIR record')).toBeInTheDocument();
    expect(screen.getByText('Specimen/specimen-1')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    view.rerender(React.createElement(PreviewTable, { ...props, preview: {
      ...sourcePreview,
      rowSources: [{ kind: 'COMPOSITE' }],
    } }));
    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(screen.getByText(/Source records cannot be listed for this table shape/)).toBeInTheDocument();
    expect(screen.queryByText('Specimen/specimen-1')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    view.rerender(React.createElement(PreviewTable, { ...props, preview: { ...sourcePreview, rowSources: undefined } }));
    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(screen.getByText('Source record details are unavailable for this row.')).toBeInTheDocument();
  });

  it('lists bounded source records for a grouped row and loads the next page', async () => {
    const onRowLineage = vi.fn()
      .mockResolvedValueOnce({ receiptId: preview.receiptId, outputId: preview.outputId, rowId: 'group-row', status: 'COMPLETE', contributors: [{ resourceType: 'BodyStructure', resourceId: 'body-1', occurrenceKey: 'one' }], hasMore: true, nextOffset: 1 })
      .mockResolvedValueOnce({ receiptId: preview.receiptId, outputId: preview.outputId, rowId: 'group-row', status: 'COMPLETE', contributors: [{ resourceType: 'BodyStructure', resourceId: 'body-2', occurrenceKey: 'two' }], hasMore: false });
    render(React.createElement(PreviewTable, {
      preview: {
        ...preview,
        rows: [{ ...preview.rows![0], __loom_row_id: 'group-row' }],
        rowSources: [{ kind: 'COMPOSITE' }],
        rowLineageCapability: { status: 'AVAILABLE' },
      },
      table,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange: vi.fn(),
      onColumnsChange: vi.fn(),
      onRowLineage,
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
    expect(onRowLineage).toHaveBeenCalledWith('group-row', 0, expect.any(AbortSignal));
    expect(await screen.findByText('BodyStructure/body-1')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show more source records' }));
    await waitFor(() => expect(screen.getByText('BodyStructure/body-2')).toBeInTheDocument());
    expect(onRowLineage).toHaveBeenCalledWith('group-row', 1, expect.any(AbortSignal));
    expect(screen.queryByRole('button', { name: 'Show more source records' })).not.toBeInTheDocument();
  });

  it('preserves repeated source occurrences with the same key across lineage pages', async () => {
    const duplicateKeyWarnings = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const onRowLineage = vi.fn()
      .mockResolvedValueOnce({
        receiptId: preview.receiptId,
        outputId: preview.outputId,
        rowId: 'composed-row',
        status: 'COMPLETE',
        contributors: [
          { resourceType: 'Specimen', resourceId: 'root-specimen', occurrenceKey: 'specimen-root-key' },
          { resourceType: 'Patient', resourceId: 'patient-1', occurrenceKey: 'patient-1-key' },
          { resourceType: 'Specimen', resourceId: 'related-specimen', occurrenceKey: 'specimen-related-key' },
        ],
        hasMore: true,
        nextOffset: 3,
      })
      .mockResolvedValueOnce({
        receiptId: preview.receiptId,
        outputId: preview.outputId,
        rowId: 'composed-row',
        status: 'COMPLETE',
        contributors: [{ resourceType: 'Patient', resourceId: 'patient-1', occurrenceKey: 'patient-1-key' }],
        hasMore: false,
      });

    try {
      render(React.createElement(PreviewTable, {
        preview: {
          ...preview,
          rows: [{ ...preview.rows![0], __loom_row_id: 'composed-row' }],
          rowSources: [{ kind: 'COMPOSITE' }],
          rowLineageCapability: { status: 'AVAILABLE' },
        },
        table,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
        onRowLineage,
      }));

      fireEvent.click(screen.getByRole('button', { name: 'Inspect row 1 identity' }));
      const dialog = screen.getByRole('dialog', { name: 'Row 1 identity' });
      expect(await within(dialog).findByText('Patient/patient-1')).toBeInTheDocument();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Show more source records' }));
      await waitFor(() => expect(within(dialog).getAllByText('Patient/patient-1')).toHaveLength(2));
      expect(Array.from(dialog.querySelectorAll('li'), item => item.textContent)).toEqual([
        'Specimen/root-specimen',
        'Patient/patient-1',
        'Specimen/related-specimen',
        'Patient/patient-1',
      ]);
      expect(onRowLineage).toHaveBeenNthCalledWith(1, 'composed-row', 0, expect.any(AbortSignal));
      expect(onRowLineage).toHaveBeenNthCalledWith(2, 'composed-row', 3, expect.any(AbortSignal));
      expect(duplicateKeyWarnings.mock.calls.flat().join(' ')).not.toContain('Encountered two children with the same key');
    } finally {
      duplicateKeyWarnings.mockRestore();
    }
  });

  it('shows related construction outputs in the preview after applying a saved construction', () => {
    const relatedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: specimenSourceColumns,
        construction: relatedPatientConstruction,
      },
    };
    render(
      React.createElement(PreviewTable, {
        preview: relatedPatientPreview,
        table: relatedTable,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '5');
    expect(screen.getByRole('columnheader', { name: 'Patient ID' })).toBeInTheDocument();
    expect(screen.getByText('patient-42')).toBeInTheDocument();
  });

  it('keeps explicitly hidden authored fields hidden while showing construction outputs', () => {
    const relatedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: specimenSourceColumns.map((sourceColumn, index) => index === 0
          ? { ...sourceColumn, table: { visible: false, order: 0 } }
          : sourceColumn),
        construction: relatedPatientConstruction,
      },
    };
    render(
      React.createElement(PreviewTable, {
        preview: relatedPatientPreview,
        table: relatedTable,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '4');
    expect(screen.queryByRole('columnheader', { name: 'Specimen ID' })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Patient ID' })).toBeInTheDocument();
    expect(screen.getByText('patient-42')).toBeInTheDocument();
  });

  it('renames and removes source columns from the table menu and disables editing during saves', () => {
    const onColumnChange = vi.fn();
    const onRemoveColumn = vi.fn();
    const props = { preview, table, limit: 25 as const, onLimitChange: vi.fn(), onColumnChange, onColumnsChange: vi.fn(), onRemoveColumn };
    const rendered = render(React.createElement(PreviewTable, props));
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    const name = screen.getByRole('textbox', { name: 'Column name for First column' });
    fireEvent.change(name, { target: { value: 'Research label' } });
    fireEvent.blur(name);
    expect(onColumnChange).toHaveBeenCalledWith({ kind: 'AUTHORED_COLUMN', column: { ...firstColumn, label: 'Research label' } });
    fireEvent.click(screen.getByRole('button', { name: 'Remove First column column' }));
    expect(onRemoveColumn).toHaveBeenCalledWith('first_column');
    rendered.rerender(React.createElement(PreviewTable, { ...props, disabled: true }));
    expect(screen.getByRole('textbox', { name: 'Column name for First column' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove First column column' })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: 'First column' })).toBeDisabled();
  });

  it('uses the saved authored label when a preview emission still has its default label', () => {
    const authoredLabel = 'CDA primary disease';
    const configuredTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [
          { ...firstColumn, column: 'primary_disease_type', label: authoredLabel },
        ],
      },
    };
    const stalePreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [{
        column: 'primary_disease_type',
        label: 'primary_disease_type',
        logicalType: 'string',
        filterable: true,
        chartable: true,
      }],
      rows: [{ primary_disease_type: 'Nevi and melanomas' }],
    };

    render(
      React.createElement(PreviewTable, {
        preview: stalePreview,
        table: configuredTable,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    expect(screen.getByRole('columnheader', { name: authoredLabel })).toBeInTheDocument();
    expect(screen.queryByRole('columnheader', { name: 'primary_disease_type' })).not.toBeInTheDocument();
    expect(screen.getByText('Nevi and melanomas')).toBeInTheDocument();
  });

  it('shows copied source columns only once and keeps their authored labels authoritative', () => {
    const copiedSourceOutputs = specimenSourceColumns.map((sourceColumn, index) => ({
      id: sourceColumn.columnId,
      name: sourceColumn.column,
      label: index === 0 ? 'Stale stage label' : sourceColumn.label,
      type: 'string',
    }));
    const mixedConstruction: Construction = {
      ...relatedPatientConstruction,
      steps: relatedPatientConstruction.steps.map((step) => ({
        ...step,
        outputs: [...copiedSourceOutputs, ...step.outputs.filter((output) => output.name === 'patient_id')],
      })),
    };
    const sourceColumns = specimenSourceColumns.map((sourceColumn, index) =>
      index === 0 ? { ...sourceColumn, label: 'Specimen identifier' } : sourceColumn,
    );
    const mixedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: sourceColumns,
        construction: mixedConstruction,
      },
    };
    const mixedPreview: ExplorerBuilderPreviewResult = {
      ...relatedPatientPreview,
      columns: relatedPatientPreview.columns.map((column) =>
        column.column === 'specimen_id'
          ? { ...column, label: 'Specimen identifier' }
          : column,
      ),
    };
    render(
      React.createElement(PreviewTable, {
        preview: mixedPreview,
        table: mixedTable,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));

    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    expect(screen.getAllByRole('checkbox', { name: 'Specimen identifier' })).toHaveLength(1);
    expect(screen.queryByRole('checkbox', { name: 'Stale stage label' })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Specimen identifier' })).toBeInTheDocument();
    expect(screen.getByText('patient-42')).toBeInTheDocument();
  });

  it('renames, hides, and reorders saved construction outputs through presentation controls', () => {
    const outputWithPresentation = (label: string, visible: boolean, order = 4): Construction => ({
      ...relatedPatientConstruction,
      steps: relatedPatientConstruction.steps.map((step) => ({
        ...step,
        outputs: step.outputs.map((output) => ({
          ...output,
          label,
          table: { visible, order },
        })),
      })),
    });
    const makeTable = (construction: Construction): DraftTable => ({
      ...table,
      document: {
        ...table.document,
        columns: specimenSourceColumns,
        construction,
      },
    });
    const onColumnChange = vi.fn();
    const onColumnsChange = vi.fn();
    const rendered = render(
      React.createElement(PreviewTable, {
        preview: relatedPatientPreview,
        table: makeTable(outputWithPresentation('Patient ID', true)),
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange,
        onColumnsChange,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));

    expect((screen.getByRole('checkbox', { name: 'Patient ID' }) as HTMLInputElement).checked).toBe(true);
    const dataTransfer = {
      effectAllowed: 'move',
      setData: vi.fn(),
      getData: vi.fn(() => 'patient_id'),
    };
    fireEvent.dragStart(screen.getByLabelText('Drag Patient ID'), { dataTransfer });
    const firstRow = screen.getByLabelText('Drag Specimen ID').parentElement;
    expect(firstRow).not.toBeNull();
    fireEvent.dragOver(firstRow!, { clientY: 0, dataTransfer });
    fireEvent.drop(firstRow!, { clientY: 0, dataTransfer });
    expect(onColumnsChange).toHaveBeenCalledWith(expect.arrayContaining([
      expect.objectContaining({
        kind: 'CONSTRUCTION_OUTPUT',
        column: expect.objectContaining({
          id: 'patient-id',
          name: 'patient_id',
          table: expect.objectContaining({ order: 0 }),
        }),
      }),
    ]));

    onColumnsChange.mockClear();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Patient ID' }));
    expect(onColumnChange).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'CONSTRUCTION_OUTPUT',
      stepId: 'related_patient_id',
      column: expect.objectContaining({
        id: 'patient-id',
        name: 'patient_id',
        label: 'Patient ID',
        table: { visible: false, order: 4 },
      }),
    }));

    rendered.rerender(React.createElement(PreviewTable, {
      preview: relatedPatientPreview,
      table: makeTable(outputWithPresentation('Related Patient ID', false)),
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange,
      onColumnsChange,
    }));
    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '4');
    expect(screen.queryByRole('columnheader', { name: 'Related Patient ID' })).not.toBeInTheDocument();

    rendered.rerender(React.createElement(PreviewTable, {
      preview: relatedPatientPreview,
      table: makeTable(outputWithPresentation('Related Patient ID', true)),
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange,
      onColumnsChange,
    }));
    expect(screen.getByRole('columnheader', { name: 'Related Patient ID' })).toBeInTheDocument();
    expect(screen.getByText('patient-42')).toBeInTheDocument();
  });

  it('renames a grouped row value through its terminal construction output when its source has the same name', () => {
    const sourceColumn = {
      ...column('specimen_type', 'CDA specimen_type values', 0),
      columnId: 'specimen-type-source',
    };
    const construction: Construction = {
      version: 1,
      steps: [{
        id: 'group-specimen-types',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: {
          kind: 'GROUP',
          group: { constructionId: 'group-specimen-types' },
        },
        rowValues: [{
          inputColumnId: 'specimen-type-source',
          outputColumnId: 'grouped-specimen-types',
          policy: 'ALL',
        }],
        outputs: [{
          id: 'grouped-specimen-types',
          name: 'specimen_type',
          label: 'Specimen type',
          type: 'array',
        }],
      }],
    };
    const groupedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [sourceColumn],
        construction,
      },
    };
    const groupedPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [{
        column: 'specimen_type',
        label: 'Specimen type',
        logicalType: 'array',
        filterable: true,
        chartable: true,
        authoredColumns: ['specimen_type'],
      }],
      rows: [{ specimen_type: ['tumor', 'normal'] }],
    };
    const onColumnChange = vi.fn();

    render(React.createElement(PreviewTable, {
      preview: groupedPreview,
      table: groupedTable,
      limit: 25,
      onLimitChange: vi.fn(),
      onColumnChange,
      onColumnsChange: vi.fn(),
    }));

    expect(screen.getByRole('columnheader', { name: 'Specimen type' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    const name = screen.getByRole('textbox', { name: 'Column name for Specimen type' });
    expect((name as HTMLInputElement).value).toBe('Specimen type');
    fireEvent.change(name, { target: { value: 'Column name for Specimen type' } });
    fireEvent.blur(name);

    expect(onColumnChange).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'CONSTRUCTION_OUTPUT',
      stepId: 'group-specimen-types',
      column: expect.objectContaining({
        id: 'grouped-specimen-types',
        name: 'specimen_type',
        label: 'Column name for Specimen type',
      }),
    }));
    expect(sourceColumn.label).toBe('CDA specimen_type values');
  });

  it('keeps a same-name row-value visibility control aligned with the preview when source visibility is saved', () => {
    const sourceColumn = {
      ...column('specimen_type', 'CDA specimen_type values', 0),
      columnId: 'specimen-type-source',
      table: { visible: false, order: 0 },
    };
    const construction: Construction = {
      version: 1,
      steps: [{
        id: 'group-specimen-types',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: {
          kind: 'GROUP',
          group: { constructionId: 'group-specimen-types' },
        },
        rowValues: [{
          inputColumnId: 'specimen-type-source',
          outputColumnId: 'grouped-specimen-types',
          policy: 'ALL',
        }],
        outputs: [{
          id: 'grouped-specimen-types',
          name: 'specimen_type',
          label: 'Renamed specimen type',
          type: 'array',
        }],
      }],
    };
    const groupedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [sourceColumn],
        construction,
      },
    };
    const groupedPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [{
        column: 'specimen_type',
        label: 'Specimen type',
        logicalType: 'array',
        filterable: true,
        chartable: true,
        authoredColumns: ['specimen_type'],
      }],
      rows: [{ specimen_type: ['tumor', 'normal'] }],
    };
    const onColumnChange = vi.fn();
    const props = {
      preview: groupedPreview,
      table: groupedTable,
      limit: 25 as const,
      onLimitChange: vi.fn(),
      onColumnChange,
      onColumnsChange: vi.fn(),
    };
    const rendered = render(React.createElement(PreviewTable, props));

    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    const visibility = screen.getByRole('checkbox', { name: 'Renamed specimen type' }) as HTMLInputElement;
    expect(visibility.checked).toBe(false);
    expect(screen.queryByRole('columnheader', { name: 'Renamed specimen type' })).not.toBeInTheDocument();
    fireEvent.click(visibility);
    expect(onColumnChange).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'CONSTRUCTION_OUTPUT',
      stepId: 'group-specimen-types',
      column: expect.objectContaining({
        id: 'grouped-specimen-types',
        table: { visible: true, order: 0 },
      }),
    }));

    const visibleConstruction: Construction = {
      ...construction,
      steps: construction.steps.map((step) => ({
        ...step,
        outputs: step.outputs.map((output) => ({
          ...output,
          table: { visible: true, order: 0 },
        })),
      })),
    };
    rendered.rerender(React.createElement(PreviewTable, {
      ...props,
      table: {
        ...groupedTable,
        document: { ...groupedTable.document, construction: visibleConstruction },
      },
    }));
    expect((screen.getByRole('checkbox', { name: 'Renamed specimen type' }) as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole('columnheader', { name: 'Renamed specimen type' })).toBeInTheDocument();

    const visibleSourceTable: DraftTable = {
      ...groupedTable,
      document: {
        ...groupedTable.document,
        columns: [{ ...sourceColumn, table: { visible: true, order: 0 } }],
        construction,
      },
    };
    rendered.rerender(React.createElement(PreviewTable, {
      ...props,
      table: visibleSourceTable,
    }));
    expect((screen.getByRole('checkbox', { name: 'Renamed specimen type' }) as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole('columnheader', { name: 'Renamed specimen type' })).toBeInTheDocument();
  });

  it('shows compiler-provided units in Preview headers while leaving unitless columns unchanged', () => {
    const previewWithUnit: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: preview.columns.map((entry) => entry.column === 'first_column'
        ? {
            ...entry,
            column: 'mean_body_height',
            authoredColumns: ['first_column'],
            label: 'Mean body height',
            logicalType: 'decimal',
            resultUnit: { system: 'http://unitsofmeasure.org', code: 'cm' },
          }
        : entry),
      rows: [{ mean_body_height: 180, second_column: 'two' }],
    };
    render(
      React.createElement(PreviewTable, {
        preview: previewWithUnit,
        table,
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    const heightHeader = screen.getByRole('columnheader', { name: 'Mean body height (cm)' });
    expect(heightHeader).toBeInTheDocument();
    expect(screen.getByText('(cm)')).toHaveAttribute(
      'title', 'Unit cm; system http://unitsofmeasure.org',
    );
    expect(screen.getByRole('columnheader', { name: 'Second column' })).toBeInTheDocument();
  });

  it('opens a repeated FHIR record inspector without flattening cell evidence', () => {
    const ownerColumn: ExplorerBuilderColumn = {
      column: 'height_records',
      label: 'Body height records',
      logicalType: 'object',
      occurrenceId: 'base',
      source: {
        kind: 'ownerRecords',
        ownerRecords: {
          binding: {
            ownerPath: 'component[]', keyPath: 'component[].code.coding[]',
            systemPath: 'system', codePath: 'code', valuePath: 'valueQuantity.value',
            choiceArms: ['valueQuantity'], logicalType: 'decimal', unitPath: 'valueQuantity.unit',
          },
          key: { system: 'http://loinc.org', code: '8302-2' },
        },
      },
      table: { visible: true, order: 0 },
    };
    const ownerPreview: ExplorerBuilderPreviewResult = {
      ...preview,
      columns: [{ column: ownerColumn.column, label: ownerColumn.label, logicalType: 'object', filterable: false, chartable: false }],
      rows: [{
        height_records: [{
          source: { resourceType: 'Observation', resourceId: 'obs-1', ownerPath: 'component[]', ownerOrdinal: 0 },
          codings: [{ system: 'http://loinc.org', code: '8302-2' }],
          choiceArm: 'valueQuantity', logicalType: 'decimal', value: 0, values: [0], unit: 'cm', status: 'VALUE',
          owner: { code: { coding: [{ system: 'http://loinc.org', code: '8302-2' }] }, valueQuantity: { value: 0, unit: 'cm' } },
        }, {
          source: { resourceType: 'Observation', resourceId: 'obs-1', ownerPath: 'component[]', ownerOrdinal: 1 },
          codings: [{ system: 'http://loinc.org', code: '8302-2' }],
          choiceArm: 'valueQuantity', logicalType: 'decimal', values: [], status: 'ABSENT',
          owner: { code: { coding: [{ system: 'http://loinc.org', code: '8302-2' }] }, _valueString: { extension: [{ url: 'urn:j01:missing', valueBoolean: true }] } },
        }],
      }],
    };
    render(
      React.createElement(PreviewTable, {
        preview: ownerPreview,
        table: { ...table, document: { ...table.document, columns: [ownerColumn] } },
        limit: 25,
        onLimitChange: vi.fn(),
        onColumnChange: vi.fn(),
        onColumnsChange: vi.fn(),
      }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Inspect Body height records for row 1' }));
    expect(screen.getByRole('dialog', { name: 'Body height records record evidence' })).toBeInTheDocument();
    expect(screen.getByText('VALUE')).toBeInTheDocument();
    expect(screen.getByText('ABSENT')).toBeInTheDocument();
    expect(screen.getAllByText('valueQuantity')).toHaveLength(2);
    expect(screen.getByText('cm')).toBeInTheDocument();
    expect(screen.getAllByText('http://loinc.org · 8302-2')).toHaveLength(2);
    expect(screen.getAllByText(/resourceId: obs-1/)).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

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
      screen.getByText(/drag to change its order/),
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
        kind: 'AUTHORED_COLUMN',
        column: expect.objectContaining({
          column: 'first_column',
          table: { visible: false, order: 0 },
        }),
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
        kind: 'AUTHORED_COLUMN',
        column: expect.objectContaining({
          column: 'second_column',
          table: expect.objectContaining({ order: 0 }),
        }),
      }),
      expect.objectContaining({
        kind: 'AUTHORED_COLUMN',
        column: expect.objectContaining({
          column: 'first_column',
          table: expect.objectContaining({ order: 1 }),
        }),
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
        field: {
          path: 'name[].given[]',
          projectionMode: 'INDEXED',
        },
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


it('keeps generated Group columns before newly appended source fields', () => {
  const extra = column('extra', 'Extra', 3);
  const groupOutputs = [
    { id: 'key', name: firstColumn.column, label: firstColumn.label, type: 'string' },
    { id: 'count', name: 'count', label: 'Count', type: 'integer' },
    { id: 'distinct', name: 'distinct', label: 'Distinct', type: 'integer' },
    { id: 'extra', name: extra.column, label: extra.label, type: 'array' },
  ];
  const groupedTable: DraftTable = {
    ...table,
    document: {
      ...table.document,
      columns: [firstColumn, extra],
      construction: {
        version: 1,
        steps: [{
          id: 'group', inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: { kind: 'GROUP', group: { constructionId: 'group', keys: [], aggregates: [] } },
          outputs: groupOutputs,
        }],
      },
    },
  };
  render(React.createElement(PreviewTable, {
    table: groupedTable,
    preview: { ...preview, columns: groupOutputs.map(output => ({ column: output.name, label: output.label, logicalType: output.type, filterable: true, chartable: true })), rows: [{ first_column: 'one', count: 31, distinct: 2, extra: ['value'] }] },
    limit: 25, onLimitChange: vi.fn(), onColumnChange: vi.fn(), onColumnsChange: vi.fn(),
  }));
  expect(screen.getAllByRole('columnheader').map(header => header.textContent?.trim())).toEqual([
    'First column', 'Count', 'Distinct', 'Extra',
  ]);
});


it('configures only final Group columns, including hidden outputs, without exposing consumed sources', () => {
  const consumed = column('consumed_source', 'Specimen ID', 0);
  const carried = column('carried_values', 'Specimen ID', 2);
  const outputs = [
    { id: 'patient', name: 'patient_id', label: 'Patient ID', type: 'string' },
    { id: 'count', name: 'count', label: 'Count', type: 'integer' },
    { id: 'carried', name: carried.column, label: carried.label, type: 'array' },
    { id: 'hidden', name: 'hidden_count', label: 'Hidden count', type: 'integer', table: { visible: false, order: 3 } },
  ];
  const onRemoveColumn = vi.fn();
  render(React.createElement(PreviewTable, {
    table: { ...table, document: { ...table.document, columns: [consumed, carried], construction: {
      version: 1, steps: [{ id: 'group', inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'GROUP', group: { constructionId: 'group', keys: [], aggregates: [] } }, outputs }],
    } } },
    preview: { ...preview, columns: outputs.map(output => ({ column: output.name, label: output.label, logicalType: output.type, filterable: true, chartable: true })), rows: [{ patient_id: 'patient-1', count: 2, carried_values: ['s1', 's2'], hidden_count: 2 }] },
    limit: 25, onLimitChange: vi.fn(), onColumnChange: vi.fn(), onColumnsChange: vi.fn(), onRemoveColumn,
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
  expect(screen.getAllByRole('textbox', { name: 'Column name for Specimen ID' })).toHaveLength(1);
  expect(screen.getByRole('checkbox', { name: 'Hidden count', checked: false })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Remove Specimen ID column' }));
  expect(onRemoveColumn).toHaveBeenCalledWith(carried.column);
});


it('keeps fixed cohort columns before selected member fields after reload', () => {
  const member = { ...column('member_type', 'Resource Type', 3), columnId: 'member-type' };
  render(React.createElement(PreviewTable, {
    table: { ...table, document: { ...table.document, columns: [member], rows: {
      kind: 'GROUPS', groups: {
        source: { kind: 'EXPLICIT', explicit: { revisionId: 'cohort-revision', unassignedMemberPolicy: 'EXCLUDE' } },
        rowValues: [{ columnId: 'member-type', policy: 'ALL' }],
      },
    } } },
    preview: { ...preview, columns: [
      { column: 'group_label', label: 'Group label', logicalType: 'string', filterable: true, chartable: true },
      { column: 'group_ordinal', label: 'Group ordinal', logicalType: 'integer', filterable: true, chartable: true },
      { column: 'members', label: 'Members', logicalType: 'object', filterable: false, chartable: false },
      { column: member.column, label: member.label, logicalType: 'string', filterable: true, chartable: true },
    ], rows: [{ group_label: 'Two Specimens', group_ordinal: 0, members: [], member_type: ['Specimen'] }] },
    limit: 25, onLimitChange: vi.fn(), onColumnChange: vi.fn(), onColumnsChange: vi.fn(),
  }));
  expect(screen.getAllByRole('columnheader').map(header => header.textContent?.trim())).toEqual([
    'Group label', 'Group ordinal', 'Members', 'Resource Type',
  ]);
});


it('configures only selected member fields when the cohort is the terminal stage', () => {
  const consumed = { ...column('original_specimen_id', 'Original Specimen ID', 0), columnId: 'original-specimen-id' };
  const member = { ...column('member_type', 'Resource Type', 1), columnId: 'member-type' };
  const outputs = [
    { id: 'original-specimen-id', name: consumed.column, label: consumed.label, type: 'string' },
  ];
  render(React.createElement(PreviewTable, {
    table: { ...table, document: {
      ...table.document,
      columns: [consumed, member],
      rows: { kind: 'GROUPS', groups: {
        source: { kind: 'EXPLICIT', explicit: { revisionId: 'cohort-revision', unassignedMemberPolicy: 'EXCLUDE' } },
        afterStepId: 'filter_source',
        rowValues: [{ columnId: 'member-type', policy: 'ONE' }],
      } },
      construction: { version: 1, steps: [{
        id: 'filter_source', inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: consumed.columnId, operator: 'EXISTS' } },
        outputs,
      }] },
    } },
    preview: { ...preview, columns: [
      { column: 'group_label', label: 'Group label', logicalType: 'string', filterable: true, chartable: true },
      { column: 'group_ordinal', label: 'Group ordinal', logicalType: 'integer', filterable: true, chartable: true },
      { column: 'members', label: 'Members', logicalType: 'object', filterable: false, chartable: false },
      { column: member.column, label: member.label, logicalType: 'string', filterable: true, chartable: true },
    ], rows: [{ group_label: 'Cohort', group_ordinal: 0, members: [], member_type: 'Specimen' }] },
    limit: 25, onLimitChange: vi.fn(), onColumnChange: vi.fn(), onColumnsChange: vi.fn(),
  }));
  fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
  expect(screen.getAllByRole('textbox', { name: 'Column name for Resource Type' })).toHaveLength(1);
  expect(screen.queryByRole('textbox', { name: 'Column name for Original Specimen ID' })).not.toBeInTheDocument();
  expect(screen.getAllByRole('columnheader').map(header => header.textContent?.trim())).toEqual([
    'Group label', 'Group ordinal', 'Members', 'Resource Type',
  ]);
});
