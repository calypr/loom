// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../types';
import { groupSourceSteps, ImportedSourceSteps } from './ImportedSourceSteps';

afterEach(cleanup);

const sourcePlan = {
  steps: [
    {
      id: 'fields',
      kind: 'FIELD_GROUP',
      title: 'Patient fields',
      summary: 'Configure fields from each Patient record',
      enabled: true,
      editable: true,
    },
    {
      id: 'traversals[0]',
      kind: 'TRAVERSAL',
      title: 'Observation',
      summary: 'Traverse from Patient to Observation records',
      enabled: false,
      editable: true,
    },
    {
      id: 'traversals[0].pivots[0]',
      parentId: 'traversals[0]',
      kind: 'PIVOT',
      title: 'Observation values',
      summary: 'Discover columns for Observation values',
      enabled: true,
      editable: true,
      maxColumns: 128,
      valueMode: 'FIRST',
    },
  ],
} satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;

const renderSourceSteps = (
  props: Partial<Parameters<typeof ImportedSourceSteps>[0]> = {},
) => {
  const onRefresh = vi.fn<() => Promise<boolean>>().mockResolvedValue(true);
  const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(true);
  render(
    <ImportedSourceSteps
      outputId="patients"
      importedSource={sourcePlan}
      onRefresh={onRefresh}
      onApply={onApply}
      {...props}
    />,
  );
  for (const group of screen.queryAllByTestId(/^source-step-group-/)) {
    if (group instanceof HTMLDetailsElement) group.open = true;
  }
  return { onRefresh, onApply };
};

describe('ImportedSourceSteps', () => {
  it('uses related-record wording and keeps the exact technical route available in details', () => {
    renderSourceSteps({ importedSource: { steps: [{
      id: 'patient-route', kind: 'TRAVERSAL', title: 'Traversal to Patient via subject_Patient',
      summary: 'Optional relationship with nested source steps.', enabled: true, editable: true,
    }] } });
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Related Patient records' }));
    expect(screen.getByText('Use fields from these related records. A match is optional.')).toBeTruthy();
    const technical = screen.getByText('Traversal to Patient via subject_Patient');
    expect(technical.closest('details')).toHaveProperty('open', false);
    expect(screen.getByText('Relationship details')).toBeTruthy();
  });

  it('groups only adjacent columns in one scope and never moves a step across a traversal', () => {
    const steps = [
      sourcePlan.steps[0],
      { ...sourcePlan.steps[0], id: 'catalog', kind: 'CATALOG_PROJECTION' },
      sourcePlan.steps[1],
      sourcePlan.steps[2],
      { ...sourcePlan.steps[0], id: 'later-columns' },
    ];
    const groups = groupSourceSteps(steps);
    expect(groups.map((group) => group.entries.map(({ step }) => step.id))).toEqual([
      ['fields', 'catalog'], ['traversals[0]'], ['traversals[0].pivots[0]'], ['later-columns'],
    ]);
    expect(groups.flatMap((group) => group.entries.map(({ step }) => step.id))).toEqual(steps.map((step) => step.id));
    expect(groups.flatMap((group) => group.entries.map(({ number }) => number))).toEqual([1, 2, 3, 4, 5]);
    expect(groupSourceSteps(Array.from({ length: 100 }, (_, i) => ({ ...sourcePlan.steps[0], id: `column-${i}` })))).toHaveLength(1);
  });

  it('keeps descriptions in one selected inspector while every recipe step stays selectable', () => {
    renderSourceSteps();
    expect(screen.getAllByRole('button', { name: /^Source step / })).toHaveLength(3);
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Patient fields' }));
    expect(screen.getByText(sourcePlan.steps[0].summary)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Source step 3: Observation values' }));
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    expect(screen.getByText(sourcePlan.steps[2].summary)).toBeTruthy();
    expect(screen.getByText('Under Observation')).toBeTruthy();
    expect(screen.getAllByTestId('imported-source-step-editor')).toHaveLength(1);
  });

  it('shows numbered recipe summaries and inactive descendants without dropping their settings', async () => {
    const { onApply } = renderSourceSteps();

    expect(screen.getByRole('button', { name: 'Source step 1: Patient fields' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Source step 2: Observation' }).textContent).toContain('Disabled');
    expect(screen.getByRole('button', { name: 'Source step 3: Observation values' }).textContent).toContain('Inactive · parent off');

    fireEvent.click(screen.getByTestId('imported-source-step-traversals[0].pivots[0]'));
    expect(screen.getByRole('status').textContent).toContain('Inactive because Observation is disabled');
    expect(screen.getByTestId('imported-source-value-mode')).toHaveProperty('value', 'FIRST');
    expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '128');

    fireEvent.change(screen.getByTestId('imported-source-max-columns'), { target: { value: '64' } });
    fireEvent.change(screen.getByTestId('imported-source-value-mode'), { target: { value: 'ALL' } });
    fireEvent.click(screen.getByTestId('imported-source-step-apply'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith({
      type: 'UPDATE_IMPORTED_SOURCE_STEP',
      outputId: 'patients',
      sourceStep: {
        stepId: 'traversals[0].pivots[0]',
        enabled: true,
        maxColumns: 64,
        valueMode: 'ALL',
      },
    }));
    await waitFor(() => expect(screen.queryByTestId('imported-source-step-editor')).toBeNull());
  });

  it('keeps the editor open when the command wrapper reports failure', async () => {
    const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(false);
    renderSourceSteps({ onApply });

    fireEvent.click(screen.getByTestId('imported-source-step-fields'));
    fireEvent.click(screen.getByLabelText('Enable this source step'));
    fireEvent.click(screen.getByTestId('imported-source-step-apply'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith({
      type: 'UPDATE_IMPORTED_SOURCE_STEP',
      outputId: 'patients',
      sourceStep: { stepId: 'fields', enabled: false },
    }));
    expect(screen.getByTestId('imported-source-step-editor')).toBeTruthy();
  });

  it('offers a refresh action only when an older imported document has no source plan', async () => {
    const { onRefresh } = renderSourceSteps({ importedSource: undefined });

    fireEvent.click(screen.getByTestId('imported-source-refresh'));

    await waitFor(() => expect(onRefresh).toHaveBeenCalledOnce());
    expect(screen.getByTestId('imported-source-refresh')).toBeTruthy();
  });

  it('shows only supported controls advertised for each source step', () => {
    renderSourceSteps();
    fireEvent.click(screen.getByTestId('imported-source-step-fields'));

    expect(screen.queryByTestId('imported-source-max-columns')).toBeNull();
    expect(screen.queryByTestId('imported-source-value-mode')).toBeNull();
  });
});

 it.each(['Optional relationship selection follows an external policy.', 'Required relationship selection follows an external policy.'])(
  'preserves an unfamiliar relationship explanation: %s', (summary) => {
    renderSourceSteps({ importedSource: { steps: [{
      id: 'unknown', kind: 'TRAVERSAL', title: 'Unfamiliar relationship description',
      summary, enabled: true, editable: true,
    }] } });
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Unfamiliar relationship description' }));
    const editor = screen.getByTestId('imported-source-step-editor');
    const primarySummary = [...editor.children].find((child) => child.tagName === 'P');
    expect(primarySummary?.textContent).toBe(summary);
  },
);
