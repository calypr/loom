// @vitest-environment jsdom
import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderColumn } from '../../../types';
import { FeaturePolicyEditor } from './FeaturePolicyEditor';

const column = {
  column: 'weight',
  label: 'Weight',
  logicalType: 'decimal',
  occurrenceId: 'root',
  source: { kind: 'field' as const, field: { path: 'root.weight' } },
} satisfies ExplorerBuilderColumn;

describe('FeaturePolicyEditor', () => {
  it('groups value settings separately from server-resolved time and unit settings', () => {
    render(
      <FeaturePolicyEditor
        column={column}
        candidate={undefined}
        candidates={[]}
        related={false}
        resourceLabel="Patient"
        disabled={false}
        onSourceChange={vi.fn()}
        onTransformationChange={vi.fn()}
        onContributorChange={vi.fn()}
      />,
    );

    expect(screen.getByRole('group', { name: 'Values' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Time and units' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Values' }).textContent)
      .toContain('Category recoding is unavailable until the server resolves this column capability.');
    expect(screen.getByRole('group', { name: 'Time and units' }).textContent)
      .toContain('Choose a date-aware value selection to configure its time window here.');
  });
});
