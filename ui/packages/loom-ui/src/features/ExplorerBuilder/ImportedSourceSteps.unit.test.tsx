// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../types';
import { ImportedSourceSteps } from './ImportedSourceSteps';

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
  return { onRefresh, onApply };
};

describe('ImportedSourceSteps', () => {
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
