// @vitest-environment jsdom

import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { ConstructionProposalPreview } from './ConstructionProposalPreview';

const preview = (sampled: boolean, partialValidation = false): ExplorerBuilderPreviewResult => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
  receiptId: 'receipt-1',
  outputId: 'patients',
  columns: [{ column: 'id', label: 'ID', logicalType: 'string', filterable: true, chartable: false }],
  rows: [{ id: 'patient-1' }],
  rowCount: 1,
  sampled,
  partialValidation,
  diagnostics: [],
});

const pivotPreview = (): ExplorerBuilderPreviewResult => ({
  ...preview(false),
  columns: [
    { column: 'specimen_id', label: 'Specimen ID', logicalType: 'string', filterable: true, chartable: false },
    { column: 'patient_id', label: 'Patient FHIR ID', logicalType: 'string', filterable: true, chartable: false },
    { column: 'observation_id', label: 'Observation FHIR ID', logicalType: 'string', filterable: true, chartable: false },
    { column: 'related_value', label: 'Observation value', logicalType: 'string', filterable: true, chartable: false },
    { column: 'null_value', label: 'Null', logicalType: 'string', filterable: true, chartable: false },
    { column: 'd', label: 'd', logicalType: 'integer', filterable: true, chartable: false },
  ],
  rows: [{
    specimen_id: 'specimen-1',
    patient_id: 'patient-1',
    observation_id: 'observation-1',
    related_value: 'value-1',
    null_value: null,
    d: 7,
  }],
});

const pivotPresentationTable = (): DraftTable => ({
  outputId: 'patients',
  tabId: 'patients',
  title: 'Patients',
  document: {
    columns: [],
    construction: {
      version: 1,
      steps: [{
        id: 'pivot',
        inputs: [],
        operation: { kind: 'PIVOT' },
        outputs: [
          { id: 'd-output', name: 'd', label: 'Observed quantity d', type: 'integer', table: { order: 0 } },
          { id: 'specimen-output', name: 'specimen_id', label: 'Specimen ID', type: 'string', table: { order: 1 } },
          { id: 'patient-output', name: 'patient_id', label: 'Patient FHIR ID', type: 'string', table: { order: 2 } },
          { id: 'observation-output', name: 'observation_id', label: 'Observation FHIR ID', type: 'string', table: { order: 3 } },
          { id: 'value-output', name: 'related_value', label: 'Observation value', type: 'string', table: { order: 4 } },
          { id: 'null-output', name: 'null_value', label: 'Null', type: 'string', table: { order: 5 } },
        ],
      }],
    },
  } as unknown as DraftTable['document'],
});

describe('ConstructionProposalPreview', () => {
  it('does not describe a bounded preview as the full table', () => {
    render(<ConstructionProposalPreview preview={preview(true)} />);
    expect(screen.getByText(/Full-output coverage is unavailable before publication/)).toBeInTheDocument();
  });

  it('identifies a preview that exhausted the output', () => {
    render(<ConstructionProposalPreview preview={preview(false)} />);
    expect(screen.getByText('Showing all 1 row in this proposal.')).toBeInTheDocument();
  });

  it('explains when only displayed construction groups were validated', () => {
    render(<ConstructionProposalPreview preview={preview(false, true)} />);
    expect(screen.getByText('Only displayed groups were checked in this preview. Publishing runs Pivot rules over the full source.')).toBeInTheDocument();
  });

  it('renders a Pivot proposal using the candidate terminal outputs presentation order', () => {
    render(<ConstructionProposalPreview preview={pivotPreview()} presentationTable={pivotPresentationTable()} />);

    expect(screen.getAllByRole('columnheader').map((header) => header.firstElementChild?.textContent)).toEqual([
      'Observed quantity d',
      'Specimen ID',
      'Patient FHIR ID',
      'Observation FHIR ID',
      'Observation value',
      'Null',
    ]);
    expect(screen.getAllByRole('cell').map((cell) => cell.textContent)).toEqual([
      '7',
      'specimen-1',
      'patient-1',
      'observation-1',
      'value-1',
      '—',
    ]);
  });
});
