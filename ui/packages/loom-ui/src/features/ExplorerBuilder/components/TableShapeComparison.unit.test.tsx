// @vitest-environment jsdom
import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { TableShapeComparison, type TableShapeComparisonEvidence } from './TableShapeComparison';

const example = (index: number) => ({
  exampleId: `row-${index}`,
  label: `Example row ${index}`,
  cells: Array.from({ length: 8 }, (_, cellIndex) => ({
    columnId: `example-column-${cellIndex + 1}`,
    columnLabel: cellIndex === 0 ? 'Systolic pressure' : `Example cell ${cellIndex + 1}`,
    before: { kind: 'value' as const, displayText: cellIndex === 0 ? '120' : `before-${cellIndex + 1}` },
    after: { kind: 'value' as const, displayText: cellIndex === 0 ? String(100 + index) : `after-${cellIndex + 1}` },
  })),
  contributors: [`Resource ${index}`],
  exclusions: [`Excluded resource ${index}`],
});

const evidence: TableShapeComparisonEvidence = {
  base: {
    rowCount: 18,
    sampling: { kind: 'sampled', label: 'Sampled', explanation: 'Preview sample only.' },
  },
  candidate: {
    rowCount: 9,
    sampling: { kind: 'complete', label: 'Complete' },
  },
  changedColumns: Array.from({ length: 10 }, (_, index) => ({
    columnId: `column-${index + 1}`,
    label: index === 0 ? 'Systolic pressure' : `Changed column ${index + 1}`,
  })),
  examples: [1, 2, 3, 4, 5, 6].map(example),
  contributors: ['Resource A', 'Resource B', 'Resource C', 'Resource D', 'Resource E', 'Resource F'],
  exclusions: ['Outside date window', 'No usable value', 'Duplicate record', 'Unknown category', 'Missing input', 'Sixth exclusion'],
  informationLoss: [1, 2, 3, 4, 5, 6].map((index) => ({
    lossId: `loss-${index}`,
    label: `Loss ${index}`,
    explanation: `Server-declared information loss ${index}.`,
  })),
};

describe('TableShapeComparison', () => {
  it('renders row counts, changed values, evidence, sampling, and declared loss with bounded examples', () => {
    render(<TableShapeComparison evidence={evidence} />);

    expect(screen.getByTestId('ui04-comparison-base-rows').textContent).toContain('18');
    expect(screen.getByTestId('ui04-comparison-candidate-rows').textContent).toContain('9');
    expect(screen.getByTestId('ui04-comparison-base-sampling').textContent).toBe('Sampled');
    expect(screen.getByTestId('ui04-comparison-base-sampling').getAttribute('data-sampled')).toBe('true');
    expect(screen.getByTestId('ui04-comparison-candidate-sampling').getAttribute('data-sampled')).toBe('false');
    expect(screen.getByTestId('ui04-comparison-changed-columns').textContent).toContain('Systolic pressure');
    expect(screen.getByTestId('ui04-comparison-changed-columns').textContent)
      .toContain('Showing the first 8 of 10 returned changed columns.');
    expect(screen.getByTestId('ui04-comparison-changed-columns').textContent).not.toContain('Changed column 9');
    expect(screen.getByTestId('ui04-comparison-example-1').textContent).toContain('120');
    expect(screen.getByTestId('ui04-comparison-example-1').textContent).toContain('101');
    expect(screen.getByTestId('ui04-comparison-example-1-cells').textContent)
      .toContain('Showing the first 6 of 8 returned cells.');
    expect(screen.getByTestId('ui04-comparison-example-1-cells').textContent).toContain('Example cell 6');
    expect(screen.getByTestId('ui04-comparison-example-1-cells').textContent).not.toContain('Example cell 7');
    expect(screen.getByTestId('ui04-comparison-example-1-contributors').textContent).toContain('Resource 1');
    expect(screen.getByTestId('ui04-comparison-example-1-exclusions').textContent).toContain('Excluded resource 1');
    expect(screen.getByTestId('ui04-comparison-contributors').textContent).toContain('Resource E');
    expect(screen.getByTestId('ui04-comparison-exclusions').textContent).toContain('Unknown category');
    expect(screen.getByTestId('ui04-comparison-information-loss').textContent).toContain('Server-declared information loss 5.');
    expect(screen.getByTestId('ui04-comparison-information-loss').textContent).not.toContain('Server-declared information loss 6.');
    expect(screen.getByTestId('ui04-comparison-example-5')).toBeTruthy();
    expect(screen.queryByTestId('ui04-comparison-example-6')).toBeNull();
    expect(screen.getByTestId('ui04-comparison-examples').textContent).toContain('Showing the first 5 of 6 returned examples.');
  });
});
