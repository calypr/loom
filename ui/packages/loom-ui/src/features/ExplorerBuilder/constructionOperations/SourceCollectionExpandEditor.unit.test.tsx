// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { RowDefinitionChoicesResponse, RowDefinitionProposal } from '../../../types';
import {
  SourceCollectionExpandEditor,
  type SourceCollectionExpandContext,
} from './SourceCollectionExpandEditor';

const choices: RowDefinitionChoicesResponse = {
  snapshotToken: 'snapshot-1',
  outputId: 'observations',
  choices: [{
    choiceId: 'component-scope-choice',
    occurrenceId: 'observation-root',
    fieldPath: 'component[]',
    label: 'Observation component',
    description: '',
    occurrenceSummary: 'Starting Observation',
    routeSummary: 'Observation',
    kind: 'EXPANDED',
    valueType: 'ARRAY',
    policies: [{ name: 'emptyCollectionPolicy', options: ['ERROR', 'EXCLUDE', 'PRESERVE_PARENT'] }],
  }],
  explicitGroups: [],
};

const proposalFor = (policy: string): RowDefinitionProposal => ({
  proposalId: `proposal-${policy.toLowerCase()}`,
  baseReceiptId: 'base-receipt',
  outputId: 'observations',
  snapshotToken: 'snapshot-1',
  draftVersion: 4,
  draftDigest: 'draft-digest-4',
  baseDocumentDigest: 'document-digest-4',
  candidateWorkspaceDigest: `candidate-${policy}`,
  mode: 'EXPANDED',
  comparison: {
    status: 'AVAILABLE',
    base: { rowCount: 3, sampled: false },
    candidate: { rowCount: policy === 'EXCLUDE' ? 2 : 4, sampled: false },
    affectedColumns: ['component-code'],
    notices: [],
    examples: [{ rowIdentity: 'observation-1', basePresent: true, candidatePresent: true }],
  },
});

const contextFor = (overrides: Partial<SourceCollectionExpandContext> = {}): SourceCollectionExpandContext => ({
  client: {
    listRowDefinitionChoices: vi.fn().mockResolvedValue(choices),
    proposeRowDefinition: vi.fn().mockImplementation(({ selection }) => Promise.resolve(
      proposalFor(selection.expanded.emptyCollectionPolicy),
    )),
  } as unknown as SourceCollectionExpandContext['client'],
  project: 'org/project',
  explorerId: 'explorer-1',
  authResourcePath: '/programs/org/projects/project',
  snapshotToken: 'snapshot-1',
  draftVersion: 4,
  draftDigest: 'draft-digest-4',
  outputId: 'observations',
  rowsKind: 'RECORDS',
  onApply: vi.fn().mockResolvedValue(true),
  ...overrides,
});

afterEach(cleanup);

describe('SourceCollectionExpandEditor', () => {
  it('previews the signed object collection and re-previews empty policy changes before cancel', async () => {
    const context = contextFor();
    render(<SourceCollectionExpandEditor context={context} disabled={false} />);

    fireEvent.click(screen.getByTestId('construction-reshape-expand-source'));
    await screen.findByRole('dialog', { name: 'Expand a repeated source field' });
    expect(context.client.listRowDefinitionChoices).toHaveBeenCalledWith({
      project: 'org/project',
      explorerId: 'explorer-1',
      authResourcePath: '/programs/org/projects/project',
      snapshotToken: 'snapshot-1',
      outputId: 'observations',
    }, expect.any(AbortSignal));
    expect(screen.getByText('Create one row per item in a repeated source field, then run the existing row operations.')).toBeInTheDocument();
    expect(screen.getByText("This changes the table's source rows. Review the preview before applying.")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Repeated source field'), { target: { value: 'component-scope-choice' } });
    await waitFor(() => expect(screen.getByTestId('construction-source-expand-apply')).toBeEnabled());
    expect(context.client.proposeRowDefinition).toHaveBeenLastCalledWith(expect.objectContaining({
      project: 'org/project',
      explorerId: 'explorer-1',
      authResourcePath: '/programs/org/projects/project',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 4,
      expectedDraftDigest: 'draft-digest-4',
      outputId: 'observations',
      selection: {
        kind: 'EXPANDED',
        expanded: { rowChoiceId: 'component-scope-choice', emptyCollectionPolicy: 'PRESERVE_PARENT' },
      },
    }), expect.any(AbortSignal));
    expect(screen.getByText('3 rows → 4 rows')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('When a record has no values'), { target: { value: 'EXCLUDE' } });
    await waitFor(() => expect(context.client.proposeRowDefinition).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByText('3 rows → 2 rows')).toBeInTheDocument());
    expect(context.client.proposeRowDefinition).toHaveBeenLastCalledWith(expect.objectContaining({
      selection: {
        kind: 'EXPANDED',
        expanded: { rowChoiceId: 'component-scope-choice', emptyCollectionPolicy: 'EXCLUDE' },
      },
    }), expect.any(AbortSignal));

    fireEvent.click(screen.getAllByRole('button', { name: 'Cancel' }).at(-1)!);
    expect(screen.queryByRole('dialog', { name: 'Expand a repeated source field' })).not.toBeInTheDocument();
    expect(context.onApply).not.toHaveBeenCalled();
  });

  it('applies only the fresh row-definition proposal for the current table identity', async () => {
    const context = contextFor();
    render(<SourceCollectionExpandEditor context={context} disabled={false} />);
    fireEvent.click(screen.getByTestId('construction-reshape-expand-source'));
    await screen.findByRole('dialog', { name: 'Expand a repeated source field' });
    fireEvent.change(screen.getByLabelText('Repeated source field'), { target: { value: 'component-scope-choice' } });
    await waitFor(() => expect(screen.getByTestId('construction-source-expand-apply')).toBeEnabled());

    fireEvent.click(screen.getByTestId('construction-source-expand-apply'));
    await waitFor(() => expect(context.onApply).toHaveBeenCalledWith('proposal-preserve_parent'));
    expect(screen.queryByRole('dialog', { name: 'Expand a repeated source field' })).not.toBeInTheDocument();
  });

  it('blocks Apply if the draft identity changes after the source preview', async () => {
    const context = contextFor();
    const view = render(<SourceCollectionExpandEditor context={context} disabled={false} />);
    fireEvent.click(screen.getByTestId('construction-reshape-expand-source'));
    await screen.findByRole('dialog', { name: 'Expand a repeated source field' });
    fireEvent.change(screen.getByLabelText('Repeated source field'), { target: { value: 'component-scope-choice' } });
    await waitFor(() => expect(screen.getByTestId('construction-source-expand-apply')).toBeEnabled());

    view.rerender(<SourceCollectionExpandEditor context={contextFor({ draftDigest: 'draft-digest-5' })} disabled={false} />);
    expect(screen.getByRole('alert')).toHaveTextContent('The table changed while this panel was open');
    expect(screen.getByTestId('construction-source-expand-apply')).toBeDisabled();
    expect(context.onApply).not.toHaveBeenCalled();
  });

  it('does not open a source-expansion proposal while the saved row definition is grouped', () => {
    const context = contextFor({ rowsKind: 'GROUPS' });
    render(<SourceCollectionExpandEditor context={context} disabled={false} />);
    expect(screen.getByTestId('construction-reshape-expand-source')).toBeDisabled();
    expect(screen.getByText(/cannot replace grouped rows/)).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-reshape-expand-source'));
    expect(screen.queryByRole('dialog', { name: 'Expand a repeated source field' })).not.toBeInTheDocument();
    expect(context.client.listRowDefinitionChoices).not.toHaveBeenCalled();
  });
});
