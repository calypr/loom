// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type { ConfiguredColumnContextResponse } from '../../../interpretation';
import type { ExplorerBuilderColumn, ExplorerBuilderCommand } from '../../../types';
import { InterpretationPanel } from './InterpretationPanel';

const column = {
  column: 'code',
  label: 'Code',
  logicalType: 'string',
  occurrenceId: 'base',
  source: { kind: 'field' as const, field: { path: 'root.code' } },
} satisfies ExplorerBuilderColumn;

const revisionSummary = {
  id: 'revision-1',
  libraryId: 'codes',
  contentDigest: 'sha256:abc',
  author: 'researcher',
  explanation: 'A reusable patient code meaning',
  createdAt: '2026-01-01T00:00:00Z',
};

const context = {
  snapshotToken: 'snapshot-1',
  draftVersion: 2,
  draftDigest: 'digest-2',
  libraries: [
    { id: 'codes', headRevisionId: 'revision-1', headDigest: 'sha256:abc', head: revisionSummary, updatedAt: revisionSummary.createdAt },
    { id: 'other', headRevisionId: 'revision-other', headDigest: 'sha256:def', head: { ...revisionSummary, id: 'revision-other', libraryId: 'other', explanation: 'Wrong profile' }, updatedAt: revisionSummary.createdAt },
  ],
  pinnedRevisions: [{ ...revisionSummary, id: 'revision-pinned', libraryId: 'legacy', explanation: 'Pinned legacy meaning' }],
  columns: [{
    outputId: 'patients',
    column: 'code',
    occurrenceId: 'base',
    resolution: { state: 'READY', capabilityCandidateIds: ['opaque-candidate-code'], applicableRevisionIds: ['revision-1'] },
  }],
} satisfies ConfiguredColumnContextResponse;

const createdRevision = {
  id: 'revision-2',
  project: 'project',
  libraryId: 'new-codes',
  contentDigest: 'sha256:new',
  applicability: {},
  rules: [],
  author: 'researcher',
  explanation: 'A new meaning',
  createdAt: '2026-01-02T00:00:00Z',
};

const responseFor = (url: string): Response => {
  if (url.endsWith('/interpretation-preview')) {
    return new Response(JSON.stringify({
      baseReceiptId: 'base-receipt',
      candidateReceiptId: 'candidate-receipt',
      outputId: 'patients',
      column: 'code',
      revisionId: 'revision-1',
      completeness: 'INCOMPLETE',
      counts: { compared: 1, changed: 1, resolved: 1, unresolved: 0 },
      samples: [{ rowId: 'row-1', before: { code: 'old' }, after: { code: 'new', 'code[1]': 'indexed' }, state: 'RESOLVED' }],
    }), { status: 200 });
  }
  return new Response(JSON.stringify(createdRevision), { status: 201 });
};

const renderPanel = (
  onApply: (command: ExplorerBuilderCommand) => Promise<boolean>,
  options: { readonly value?: ConfiguredColumnContextResponse; readonly targetColumn?: ExplorerBuilderColumn } = {},
) => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input) => responseFor(String(input)));
  const onContextRefresh = vi.fn();
  const rendered = render(
    <LoomProvider client={createLoomClient({ fetch })}>
      <InterpretationPanel
        project="project"
        explorerId="explorer"
        authResourcePath="/programs/org/projects/project"
        outputId="patients"
        column={options.targetColumn ?? column}
        contextState={{ status: 'ready', response: options.value ?? context }}
        snapshotToken="snapshot-1"
        expectedDraftVersion={2}
        expectedDraftDigest="digest-2"
        disabled={false}
        onApply={onApply}
        onContextRefresh={onContextRefresh}
      />
    </LoomProvider>,
  );
  return { ...rendered, fetch, onContextRefresh };
};

describe('InterpretationPanel', () => {
  it('renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt', async () => {
    const applied: ExplorerBuilderCommand[] = [];
    renderPanel(async (command) => {
      applied.push(command);
      return true;
    });
    fireEvent.click(await screen.findByText('Reusable mappings'));
    expect(await screen.findByText('A reusable patient code meaning')).toBeTruthy();
    expect(screen.queryByText('Wrong profile')).toBeNull();
    expect(screen.getByText('revision-1')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Review' }));
    expect(await screen.findByText('Sample only: the bounded review did not exhaust the output.')).toBeTruthy();
    expect(screen.getByText('code[1]')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Sample only: the bounded review did not exhaust the output.')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Review' }));
    await screen.findByText('Compared 1 · Changed 1 · Resolved 1 · Unresolved 0');
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await waitFor(() => expect(applied).toEqual([{
      type: 'APPLY_INTERPRETATION_CANDIDATE', outputId: 'patients', column: 'code',
      interpretationCandidate: { candidateReceiptId: 'candidate-receipt', revisionId: 'revision-1' },
    }]));
  });

  it('creates from the saved column without sending rules or auto-applying the result', async () => {
    const applied: ExplorerBuilderCommand[] = [];
    const { fetch, onContextRefresh } = renderPanel(async (command) => {
      applied.push(command);
      return true;
    });
    fireEvent.click(await screen.findByText('Reusable mappings'));
    fireEvent.change(screen.getByLabelText('Library name'), { target: { value: 'new-codes' } });
    fireEvent.change(screen.getByLabelText('Explanation'), { target: { value: 'A new meaning' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reusable mapping' }));
    expect(await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.')).toBeTruthy();
    const createCall = fetch.mock.calls.find(([url, init]) => String(url).includes('/interpretation-revisions?') && init?.method === 'POST');
    expect(createCall?.[0]).toBe('/api/v1/projects/project/explorers/explorer/authoring/v2/interpretation-revisions?auth_resource_path=%2Fprograms%2Forg%2Fprojects%2Fproject');
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 2,
      expectedDraftDigest: 'digest-2',
      outputId: 'patients',
      column: 'code',
      libraryId: 'new-codes',
      explanation: 'A new meaning',
    });
    expect(onContextRefresh).toHaveBeenCalledTimes(1);
    expect(applied).toEqual([]);
  });

  it('revises the selected applicable head with its exact parent revision', async () => {
    const { fetch } = renderPanel(async () => false);
    fireEvent.click(await screen.findByText('Reusable mappings'));
    fireEvent.change(screen.getByLabelText('Revision mode'), { target: { value: 'revise' } });
    fireEvent.change(screen.getByLabelText('Explanation'), { target: { value: 'Refined meaning' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reusable mapping' }));
    await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.');
    const createCall = fetch.mock.calls.find(([url, init]) => String(url).includes('/interpretation-revisions?') && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({ libraryId: 'codes', parentRevisionId: 'revision-1', explanation: 'Refined meaning' });
  });

  it('shows pinned summaries from context and keeps creation disabled', async () => {
    const pinnedColumn = { ...column, interpretation: { kind: 'PINNED' as const, pinned: { revisionId: 'revision-pinned' } } };
    renderPanel(async () => true, { targetColumn: pinnedColumn });
    expect(screen.getByText('Pinned revision revision-pinned')).toBeTruthy();
    expect(screen.getByText('Pinned legacy meaning · authored by researcher')).toBeTruthy();
    fireEvent.click(screen.getByText('Reusable mappings'));
    expect(screen.getByText('This feature is already pinned. Choose an inline feature to create a mapping from a saved column.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save reusable mapping' })).toBeDisabled();
  });

  it('shows unavailable context states without offering a client-selected mapping', () => {
    const unavailable = {
      ...context,
      columns: [{ outputId: 'patients', column: 'code', occurrenceId: 'base', resolution: { state: 'AMBIGUOUS', reason: 'The saved source has multiple matches.' } }],
    } satisfies ConfiguredColumnContextResponse;
    renderPanel(async () => true, { value: unavailable });
    expect(screen.getByText('The saved source has multiple matches.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save reusable mapping' })).toBeDisabled();
  });

  it('shows loading and context errors explicitly', () => {
    const onApply = vi.fn(async () => true);
    const { rerender } = render(
      <LoomProvider client={createLoomClient({ fetch: vi.fn<typeof globalThis.fetch>() })}>
        <InterpretationPanel project="project" explorerId="explorer" outputId="patients" column={column}
          contextState={{ status: 'loading' }} snapshotToken="snapshot-1" expectedDraftVersion={2}
          expectedDraftDigest="digest-2" disabled={false} onApply={onApply} onContextRefresh={vi.fn()} />
      </LoomProvider>,
    );
    expect(screen.getByText('Loading saved interpretation context…')).toBeTruthy();
    rerender(
      <LoomProvider client={createLoomClient({ fetch: vi.fn<typeof globalThis.fetch>() })}>
        <InterpretationPanel project="project" explorerId="explorer" outputId="patients" column={column}
          contextState={{ status: 'error', message: 'draft changed' }} snapshotToken="snapshot-1"
          expectedDraftVersion={2} expectedDraftDigest="digest-2" disabled={false} onApply={onApply}
          onContextRefresh={vi.fn()} />
      </LoomProvider>,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('draft changed');
  });
});
