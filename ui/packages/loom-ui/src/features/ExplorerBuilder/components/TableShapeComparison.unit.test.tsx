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
  evidenceLimitations: ['Cell evidence covers only returned changed cells.'],
  notices: ['Missing is distinct from recorded null.'],
};

describe('TableShapeComparison', () => {
  it('shows exact cell presence and values, per-cell source values, global contributors, and limitations', () => {
    render(<TableShapeComparison comparison={evidence} columnLabels={{ vitals: 'Vital signs', active: 'Active' }} />);

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
    expect(screen.getByTestId('ui04-comparison-limitations').textContent).toContain('Cell evidence covers only returned changed cells.');
    expect(screen.getByTestId('ui04-comparison-notices').textContent).toContain('Missing is distinct from recorded null.');
  });

  it('renders unavailable comparison reasons and explicit evidence limitations', () => {
    const unavailable: Comparison = {
      status: 'UNAVAILABLE', reasonCode: 'TRACE_UNAVAILABLE', reason: 'Loom could not compare this transformation.',
      changedColumns: [], changedRowCount: 0, changedRowsSampled: false, changedRows: [],
      contributors: [], contributorsSampled: false, evidenceLimitations: ['The candidate could not be executed.'], notices: [],
    };
    render(<TableShapeComparison comparison={unavailable} columnLabels={{}} />);
    expect(screen.getByTestId('ui04-comparison-unavailable-reason').textContent)
      .toContain('TRACE_UNAVAILABLE: Loom could not compare this transformation.');
    expect(screen.getByTestId('ui04-comparison-limitations').textContent)
      .toContain('The candidate could not be executed.');
  });
});
