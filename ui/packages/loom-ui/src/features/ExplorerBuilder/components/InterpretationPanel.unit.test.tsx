// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type { ExplorerBuilderCatalog, ExplorerBuilderColumn, ExplorerBuilderCommand } from '../../../types';
import { InterpretationPanel } from './InterpretationPanel';
import { resolveInterpretationCandidate } from '../authoring/interpretationCandidate';
import { resolveInterpretationBinding } from '../authoring/interpretationCandidate';

const catalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [{ nodeId: 'node-1', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 }],
  edges: [],
  candidates: [{
    candidateId: 'candidate-code', nodeId: 'node-1', fieldPath: 'root.code', label: 'Code', logicalType: 'string', cardinality: 'required_one',
    filterable: true, chartable: false, projectionModes: ['VALUE'], defaultProjectionMode: 'VALUE',
    conceptCandidates: [{ sourceResourceType: 'Patient', completeness: 'COMPLETE', status: 'READY', population: 1 }],
  }],
} satisfies ExplorerBuilderCatalog;

const column = {
  column: 'code', label: 'Code', logicalType: 'string', occurrenceId: 'base',
  source: { kind: 'field', field: { path: 'root.code' } },
} satisfies ExplorerBuilderColumn;

const revision = {
  id: 'revision-1', project: 'project', libraryId: 'codes', contentDigest: 'sha256:abc',
  applicability: { resourceTypes: ['Patient'], logicalTypes: ['string'], cardinalities: ['required_one'] },
  rules: [{ id: 'rule-code', match: { resourceType: 'Patient', logicalType: 'string', cardinality: 'required_one' }, definition: { source: column.source } }],
  author: 'researcher', explanation: 'A reusable patient code meaning', createdAt: '2026-01-01T00:00:00Z',
};

const responseFor = (url: string, init: RequestInit | undefined): Response => {
  if (url.endsWith('/interpretation-libraries') && !init?.method) {
    const inapplicable = { ...revision, id: 'revision-other', explanation: 'Wrong profile', applicability: { sourceProfiles: ['profile-other'] } };
    return new Response(JSON.stringify({ project: 'project', libraries: [
      { library: { id: 'codes', project: 'project', headRevisionId: 'revision-1', headDigest: 'sha256:abc', createdAt: revision.createdAt, updatedAt: revision.createdAt }, head: revision },
      { library: { id: 'other', project: 'project', headRevisionId: 'revision-other', headDigest: 'sha256:def', createdAt: revision.createdAt, updatedAt: revision.createdAt }, head: inapplicable },
    ] }), { status: 200 });
  }
  if (url.endsWith('/interpretation-preview')) {
    return new Response(JSON.stringify({ baseReceiptId: 'base-receipt', candidateReceiptId: 'candidate-receipt', outputId: 'patients', column: 'code', revisionId: 'revision-1', completeness: 'INCOMPLETE', counts: { compared: 1, changed: 1, resolved: 1, unresolved: 0 }, samples: [{ rowId: 'row-1', before: { code: 'old' }, after: { code: 'new', 'code[1]': 'indexed' }, state: 'RESOLVED' }] }), { status: 200 });
  }
  return new Response(JSON.stringify(revision), { status: 201 });
};

const renderPanel = (onApply: (command: ExplorerBuilderCommand) => Promise<boolean>) => {
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => responseFor(String(input), init));
  return render(
    <LoomProvider client={createLoomClient({ fetch })}>
      <InterpretationPanel project="project" explorerId="explorer" outputId="patients" column={column} binding={resolveInterpretationBinding(column, catalog, 'node-1')} catalog={catalog} snapshotToken="snapshot-1" expectedDraftVersion={2} expectedDraftDigest="digest-2" disabled={false} onApply={onApply} />
    </LoomProvider>,
  );
};

describe('InterpretationPanel', () => {
  it('resolves only unambiguous field and typed lookup candidates', () => {
    expect(resolveInterpretationCandidate(column, {
      ...catalog,
      candidates: [catalog.candidates?.[0], { ...catalog.candidates?.[0], candidateId: 'candidate-code-2' }].filter((candidate): candidate is NonNullable<typeof candidate> => candidate !== undefined),
    }, 'node-1')).toBeUndefined();
    const codingColumn = {
      ...column,
      source: {
        kind: 'codingBySystem' as const,
        lookup: {
          binding: { ownerPath: 'code', keyPath: 'coding', systemPath: 'system', codePath: 'code', valuePath: 'value', logicalType: 'string' },
          key: { system: 'urn:codes', code: 'active' },
        },
      },
    } satisfies ExplorerBuilderColumn;
    const codingCandidate = { ...catalog.candidates?.[0], candidateId: 'candidate-coding', fieldPath: 'value', conceptCandidates: [{ sourceResourceType: 'Patient', sourcePath: 'value', system: 'urn:other', code: 'active', completeness: 'COMPLETE', status: 'READY', population: 1 }, { sourceResourceType: 'Patient', sourcePath: 'value', system: 'urn:codes', code: 'active', completeness: 'COMPLETE', status: 'READY', population: 1 }] };
    expect(resolveInterpretationCandidate(codingColumn, { ...catalog, candidates: codingCandidate ? [codingCandidate] : [] }, 'node-1')?.candidateId).toBe('candidate-coding');
    const extensionColumn = {
      ...column,
      source: { kind: 'extensionByUrl' as const, lookup: { extension: { ownerPath: 'extension', urlPath: ['urn:example'], valuePath: 'value', logicalType: 'string' } } },
    } satisfies ExplorerBuilderColumn;
    const extensionCandidate = { ...catalog.candidates?.[0], candidateId: 'candidate-extension', fieldPath: 'value', conceptCandidates: [{ sourceResourceType: 'Patient', sourcePath: 'value', extensionUrlPath: ['urn:example'], completeness: 'COMPLETE', status: 'READY', population: 1 }] };
    expect(resolveInterpretationCandidate(extensionColumn, { ...catalog, candidates: extensionCandidate ? [extensionCandidate] : [] }, 'node-1')?.candidateId).toBe('candidate-extension');
  });

  it('browses applicable revisions, reviews, cancels, and applies the exact receipt', async () => {
    const applied: ExplorerBuilderCommand[] = [];
    renderPanel(async (command) => {
      applied.push(command);
      return true;
    });
    fireEvent.click(await screen.findByText('Reusable mappings'));
    expect(await screen.findByText('A reusable patient code meaning')).toBeTruthy();
    expect(screen.queryByText('Wrong profile')).toBeNull();
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

  it('creates a library revision without auto-applying it', async () => {
    const applied: ExplorerBuilderCommand[] = [];
    renderPanel(async (command) => {
      applied.push(command);
      return true;
    });
    fireEvent.click(await screen.findByText('Reusable mappings'));
    fireEvent.change(screen.getByLabelText('Library name'), { target: { value: 'new-codes' } });
    fireEvent.change(screen.getByLabelText('Explanation'), { target: { value: 'A new meaning' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reusable mapping' }));
    expect(await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.')).toBeTruthy();
    expect(applied).toEqual([]);
  });

  it('revises the selected applicable head with its exact parent', async () => {
    const applied: ExplorerBuilderCommand[] = [];
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => responseFor(String(input), init));
    render(<LoomProvider client={createLoomClient({ fetch })}><InterpretationPanel project="project" explorerId="explorer" outputId="patients" column={column} binding={resolveInterpretationBinding(column, catalog, 'node-1')} catalog={catalog} snapshotToken="snapshot-1" expectedDraftVersion={2} expectedDraftDigest="digest-2" disabled={false} onApply={async (command) => { applied.push(command); return true; }} /></LoomProvider>);
    fireEvent.click(await screen.findByText('Reusable mappings'));
    fireEvent.change(screen.getByLabelText('Revision mode'), { target: { value: 'revise' } });
    fireEvent.change(screen.getByLabelText('Explanation'), { target: { value: 'Refined meaning' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save reusable mapping' }));
    await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.');
    const createCall = fetch.mock.calls.find(([url, init]) => String(url).endsWith('/interpretation-libraries') && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({ libraryId: 'codes', parentRevisionId: 'revision-1', explanation: 'Refined meaning' });
    expect(applied).toEqual([]);
  });

  it('makes pinned provenance explicit and keeps creation disabled', async () => {
    const pinnedColumn = { ...column, interpretation: { kind: 'PINNED' as const, pinned: { revisionId: 'revision-pinned' } } };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async (input, init) => responseFor(String(input), init));
    render(<LoomProvider client={createLoomClient({ fetch })}><InterpretationPanel project="project" explorerId="explorer" outputId="patients" column={pinnedColumn} binding={resolveInterpretationBinding(pinnedColumn, catalog, 'node-1')} catalog={catalog} snapshotToken="snapshot-1" expectedDraftVersion={2} expectedDraftDigest="digest-2" disabled={false} onApply={async () => true} /></LoomProvider>);
    expect(await screen.findByText('Pinned revision revision-pinned')).toBeTruthy();
    expect(await screen.findByText('A reusable patient code meaning')).toBeTruthy();
    fireEvent.click(screen.getByText('Reusable mappings'));
    expect(await screen.findByText('This feature is already pinned. Create from its exact revision after loading that revision, or use an inline feature.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save reusable mapping' })).toBeDisabled();
  });
});
