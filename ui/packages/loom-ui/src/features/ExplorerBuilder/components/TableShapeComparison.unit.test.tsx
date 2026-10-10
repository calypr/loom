// @vitest-environment jsdom
import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { TableShapeComparison as Comparison } from '../../../types';
import { TableShapeComparison } from './TableShapeComparison';

const evidence: Comparison = {
  status: 'AVAILABLE',
  base: { rowCount: 2, sampled: true },
  candidate: { rowCount: 1, sampled: false },
  changedColumns: ['vitals', 'active'],
  changedRowCount: 1,
  changedRowsSampled: false,
  changedRows: [{
    rowIdentity: 'patient-1',
    basePresent: true,
    candidatePresent: true,
    changedColumns: ['vitals', 'active'],
    changedCells: [
      {
        column: 'active',
        before: { present: false, value: null },
        after: { present: true, value: null },
        trace: { state: 'AVAILABLE', contributors: [], complete: true, sampled: false },
      },
      {
        column: 'vitals',
        before: { present: true, value: 0 },
        after: { present: true, value: false },
        trace: {
          state: 'AVAILABLE', cellStatus: 'VALUE', complete: true, sampled: false,
          contributors: [{ resourceType: 'Observation', resourceId: 'obs-zero', value: '' }],
        },
      },
    ],
  }],
  contributors: [{ resourceType: 'Observation', resourceId: 'obs-zero' }],
  contributorsSampled: true,
  exclusions: {
    status: 'COMPLETE',
    records: [
      {
        sourceIdentity: { resourceType: 'Patient', resourceId: 'patient-excluded-1' },
        category: { present: false, value: null },
        categoryType: 'string',
        outputRowId: 'row-missing-category',
        reason: 'The category value was missing.',
        omissionCode: 'MISSING_CATEGORY',
      },
      {
        category: { present: true, value: null }, categoryType: 'null', outputRowId: 'row-null-category',
        reason: 'The recorded null category was excluded.',
      },
      {
        category: { present: true, value: false }, categoryType: 'boolean', outputRowId: 'row-false-category',
        reason: 'The false category was excluded.',
      },
      {
        category: { present: true, value: 0 }, categoryType: 'number', outputRowId: 'row-zero-category',
        reason: 'The zero category was excluded.',
      },
      {
        category: { present: true, value: '' }, categoryType: 'string', outputRowId: 'row-empty-category',
        reason: 'The empty category was excluded.',
      },
    ],
    complete: true,
    sampled: false,
  },
  declaredInformationLoss: {
    status: 'COMPLETE',
    items: [{
      code: 'DROPPED_COLUMNS', label: 'Dropped columns', detail: 'The reshape removed unused columns.',
      affectedColumns: ['legacy_measure', 'old_status'],
    }],
  },
  evidenceLimitations: [{ code: 'CHANGED_CELLS_ONLY', message: 'Cell evidence covers only returned changed cells.' }],
  notices: ['Missing is distinct from recorded null.'],
};

describe('TableShapeComparison', () => {
  it('shows exact cells, excluded category presence and values, identity, dropped columns, and coded limitations', () => {
    render(<TableShapeComparison comparison={evidence} columnLabels={{
      vitals: 'Vital signs', active: 'Active', legacy_measure: 'Legacy measurement',
    }} />);

    expect(screen.getByTestId('ui04-comparison-base-rows').textContent).toContain('2');
    expect(screen.getByTestId('ui04-comparison-base-rows').textContent).toContain('Sampled');
    expect(screen.getByTestId('ui04-comparison-candidate-rows').textContent).toContain('1');
    expect(screen.getByTestId('ui04-comparison-changed-columns').textContent).toContain('Vital signs');
    const values = screen.getByTestId('ui04-comparison-example-1-cells').textContent ?? '';
    expect(values).toContain('Missing');
    expect(values).toContain('null');
    expect(values).toContain('0');
    expect(values).toContain('false');
    expect(values).toContain('Observation/obs-zero: ""');
    expect(screen.getByTestId('ui04-comparison-contributors').textContent).toContain('Observation/obs-zero');
    const exclusions = screen.getByTestId('ui04-comparison-exclusions');
    expect(screen.getByTestId('ui04-comparison-exclusion-summary').textContent)
      .toContain('Records complete: yes. Sampled: no.');
    expect(exclusions.textContent).toContain('Status: COMPLETE.');
    expect(exclusions.textContent).toContain('Source resource type: Patient');
    expect(exclusions.textContent).toContain('Source resource ID: patient-excluded-1');
    expect(exclusions.textContent).toContain('MISSING_CATEGORY');
    expect(exclusions.textContent).toContain('The category value was missing.');
    expect(exclusions.textContent).toContain('missing (string), value null');
    expect(exclusions.textContent).toContain('present (null), value null');
    expect(exclusions.textContent).toContain('present (boolean), value false');
    expect(exclusions.textContent).toContain('present (number), value 0');
    expect(exclusions.textContent).toContain('present (string), value ""');
    const informationLoss = screen.getByTestId('ui04-comparison-information-loss');
    expect(informationLoss.textContent).toContain('Dropped columns (DROPPED_COLUMNS)');
    expect(informationLoss.textContent).toContain('legacy_measure (Legacy measurement)');
    expect(informationLoss.textContent).toContain('old_status');
    expect(screen.getByTestId('ui04-comparison-limitations').textContent)
      .toContain('CHANGED_CELLS_ONLY: Cell evidence covers only returned changed cells.');
    expect(screen.getByTestId('ui04-comparison-notices').textContent).toContain('Missing is distinct from recorded null.');
  });

  it('reports sampled, incomplete exclusion evidence and bounds the visible records', () => {
    const partial: Comparison = {
      ...evidence,
      exclusions: {
        ...evidence.exclusions,
        records: [...evidence.exclusions.records, {
          category: { present: true, value: 'extra' }, categoryType: 'string',
          outputRowId: 'row-extra-category', reason: 'An additional excluded record.',
        }],
        status: 'INCOMPLETE',
        complete: false,
        sampled: true,
      },
    };
    render(<TableShapeComparison comparison={partial} columnLabels={{}} />);

    const summary = screen.getByTestId('ui04-comparison-exclusion-summary').textContent ?? '';
    expect(summary).toContain('Status: INCOMPLETE.');
    expect(summary).toContain('Records complete: no. Sampled: yes.');
    expect(screen.getByText('Showing 5 of 6 excluded records.')).toBeTruthy();
    expect(screen.queryByTestId('ui04-comparison-exclusion-6')).toBeNull();
  });

  it('renders unavailable comparison reasons and explicit evidence limitations', () => {
    const unavailable: Comparison = {
      status: 'UNAVAILABLE', reasonCode: 'TRACE_UNAVAILABLE', reason: 'Loom could not compare this transformation.',
      changedColumns: [], changedRowCount: 0, changedRowsSampled: false, changedRows: [],
      contributors: [], contributorsSampled: false,
      exclusions: {
        status: 'UNAVAILABLE', records: [], complete: false, sampled: false, failureCode: 'EXCLUSIONS_UNAVAILABLE',
      },
      declaredInformationLoss: {
        status: 'UNAVAILABLE', items: [], failureCode: 'INFORMATION_LOSS_UNAVAILABLE',
      },
      evidenceLimitations: [{ code: 'CANDIDATE_UNAVAILABLE', message: 'The candidate could not be executed.' }],
      notices: [],
    };
    render(<TableShapeComparison comparison={unavailable} columnLabels={{}} />);
    expect(screen.getByTestId('ui04-comparison-unavailable-reason').textContent)
      .toContain('TRACE_UNAVAILABLE: Loom could not compare this transformation.');
    expect(screen.getByTestId('ui04-comparison-limitations').textContent)
      .toContain('CANDIDATE_UNAVAILABLE: The candidate could not be executed.');
    const exclusionSummary = screen.getByTestId('ui04-comparison-exclusion-summary').textContent ?? '';
    expect(exclusionSummary).toContain('Status: UNAVAILABLE.');
    expect(exclusionSummary).toContain('Records complete: no. Sampled: no.');
    expect(screen.getByTestId('ui04-comparison-exclusions').textContent).toContain('EXCLUSIONS_UNAVAILABLE');
    expect(screen.getByTestId('ui04-comparison-information-loss-summary').textContent)
      .toContain('Status: UNAVAILABLE.');
    expect(screen.getByTestId('ui04-comparison-information-loss').textContent)
      .toContain('INFORMATION_LOSS_UNAVAILABLE');
  });
});
