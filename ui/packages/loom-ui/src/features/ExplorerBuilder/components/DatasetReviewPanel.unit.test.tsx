// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ExplorerBuilderCatalog,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderContractColumn,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { DatasetReviewPanel, type DatasetReviewTarget } from './DatasetReviewPanel';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [{ nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 }],
  edges: [],
  candidates: [],
};

const column = (
  name: string,
  label: string,
  path: string,
  projectionMode?: 'FIRST' | 'ALL',
): DraftTable['document']['columns'][number] => ({
  column: name,
  label,
  occurrenceId: 'base',
  source: { kind: 'field', field: { path, projectionMode } },
  table: { visible: true, order: 0 },
});

const table: DraftTable = {
  outputId: 'patients',
  tabId: 'patients',
  title: 'Patients',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'patients', title: 'Patients', rowLabel: 'Patient record' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [
      column('patient_name', 'Name', 'name', 'FIRST'),
      column('telecom_values', 'Telecom values', 'telecom[]', 'ALL'),
      column('contact_record', 'Contact', 'contact', 'ALL'),
    ],
  },
};

const contractColumn = (
  name: string,
  shape: string,
  lossReasons: ReadonlyArray<string> = [],
): ExplorerBuilderContractColumn => ({
  column: name,
  authoredColumns: [name],
  label: name,
  logicalType: 'string',
  filterable: true,
  chartable: true,
  shape,
  lossReasons: [...lossReasons],
});

const receipt: ExplorerBuilderCompileResult = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderReceipt',
  receiptId: 'receipt-1',
  snapshotToken: 'snapshot-1',
  builder: {
    apiVersion: EXPLORER_AUTHORING_API_VERSION,
    kind: 'ExplorerBuilderWorkspace',
    semanticsVersion: 4,
    explorer: { title: 'Test' },
    documents: [],
    tabs: [],
  },
  outputs: [{
    outputId: 'patients',
    rowGrain: 'Patient record',
    columns: [
      contractColumn('patient_name', 'scalar'),
      contractColumn('telecom_values', 'array', ['FIELD_FIRST_REDUCTION']),
      contractColumn('contact_record', 'record'),
    ],
  }],
  diagnostics: [],
};

const preview: ExplorerBuilderPreviewResult = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview',
  receiptId: 'receipt-1',
  outputId: 'patients',
  columns: [],
  rows: [{ id: 'patient-1' }, { id: 'patient-2' }],
  rowCount: 2,
  diagnostics: [],
};

describe('DatasetReviewPanel', () => {
  it('reports categorical reductions and routes namespace diagnostics to the column', () => {
    const onFocus = vi.fn<(target: DatasetReviewTarget) => void>();
    const categoricalTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{
          ...column('diagnosis', 'Diagnosis', 'code.coding[].code'),
          source: {
            kind: 'categoricalBySystem',
            categorical: {
              system: 'urn:diagnosis',
              binding: { keyPath: 'code.coding[]', systemPath: 'system', valuePath: 'code', logicalType: 'string' },
              projectionMode: 'DISTINCT',
            },
          },
        }],
      },
    };
    render(<DatasetReviewPanel
      tables={[categoricalTable]}
      catalog={catalog}
      diagnostics={[{ severity: 'error', code: 'SOURCE_UNAVAILABLE', fieldPath: 'urn:diagnosis', message: 'The selected namespace is unavailable.' }]}
      reconciliation="resolved"
      onFocus={onFocus}
      onClose={vi.fn()}
    />);
    expect(screen.getByText('Keeps distinct values and drops repeats.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Review column Diagnosis' }));
    expect(onFocus).toHaveBeenCalledWith({ kind: 'column', outputId: 'patients', column: 'diagnosis', label: 'Diagnosis', occurrenceId: 'base' });
  });

  it('summarizes saved rows, requested shapes, reductions, and limited preview evidence', () => {
    render(
      <DatasetReviewPanel
        tables={[table]}
        catalog={catalog}
        receipt={receipt}
        preview={preview}
        diagnostics={[]}
        reconciliation="resolved"
        onFocus={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText(/One row per Patient record/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Name \(patient_name\)/ })).toBeInTheDocument();
    expect(screen.getByText(/Result shape: Scalar/)).toBeInTheDocument();
    expect(screen.getByText(/Result shape: List/)).toBeInTheDocument();
    expect(screen.getByText(/Result shape: Record/)).toBeInTheDocument();
    expect(screen.getByText(/Keeps only the first value/)).toBeInTheDocument();
    expect(screen.getByText(/Only the first value in a repeated field is kept/)).toBeInTheDocument();
    expect(screen.getByText(/Preview returned 2 sample rows/)).toBeInTheDocument();
    expect(screen.getByText(/does not validate the complete population/)).toBeInTheDocument();
    expect(screen.getByText(/does not establish clinical correctness or machine-learning readiness/)).toBeInTheDocument();
  });

  it('offers a row-type action for an incomplete saved table', () => {
    const onFocus = vi.fn<(target: DatasetReviewTarget) => void>();
    const incomplete: DraftTable = {
      ...table,
      document: {
        ...table.document,
        rootResourceType: '',
        route: { occurrenceId: 'base', resourceType: '' },
        columns: [],
      },
    };
    render(
      <DatasetReviewPanel
        tables={[incomplete]}
        catalog={catalog}
        diagnostics={[]}
        reconciliation="idle"
        onFocus={onFocus}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText(/needs a starting row type/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Choose row type' }));
    expect(onFocus).toHaveBeenCalledWith({
      kind: 'row',
      outputId: 'patients',
      control: 'row-type',
    });
  });

  it('reports the missing-table publication blocker with a creation action', () => {
    const onFocus = vi.fn<(target: DatasetReviewTarget) => void>();
    render(
      <DatasetReviewPanel
        tables={[]}
        catalog={catalog}
        diagnostics={[]}
        reconciliation="idle"
        onFocus={onFocus}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText(/Create at least one output table before publishing/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Create your first table' }));
    expect(onFocus).toHaveBeenCalledWith({ kind: 'new-table' });
  });

  it('maps a blocking field diagnostic to its requested column action', () => {
    const onFocus = vi.fn<(target: DatasetReviewTarget) => void>();
    render(
      <DatasetReviewPanel
        tables={[table]}
        catalog={catalog}
        diagnostics={[{
          severity: 'error',
          code: 'INVALID_FIELD',
          fieldPath: 'name',
          message: 'The requested field is not available.',
        }]}
        reconciliation="resolved"
        onFocus={onFocus}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText(/Responsible setting: name/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Review column Name' }));
    expect(onFocus).toHaveBeenCalledWith({
      kind: 'column',
      outputId: 'patients',
      column: 'patient_name',
      label: 'Name',
      occurrenceId: 'base',
    });
  });
});
