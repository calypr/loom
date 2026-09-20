// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import type { SelectionRevision } from '../../../selection';
import type { PopulationRouteChoice } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { PopulationPanel } from './PopulationPanel';

const triggerPopulation = vi.hoisted(() => vi.fn());
const loomClient = vi.hoisted(() => ({ searchPopulationRoutes: vi.fn() }));
const searchPopulationRoutes = loomClient.searchPopulationRoutes;
vi.mock('../../../react', () => ({
  useLoomClient: () => loomClient,
  usePopulationMappingMutation: () => [triggerPopulation, { isLoading: false }],
}));

const populationChoice: PopulationRouteChoice = {
  routeChoiceId: 'population-route-1',
  route: [{
    edgeId: 'subject-edge',
    fromNodeId: 'specimen',
    toNodeId: 'files',
    fromResourceType: 'Specimen',
    toResourceType: 'DocumentReference',
    relationship: 'subject_Specimen',
    storageDirection: 'OUTBOUND',
    matchMode: 'OPTIONAL',
  }],
  presentation: {
    summary: 'Specimen → DocumentReference via subject_Specimen',
    facts: [{ label: 'Selected records', value: 'DocumentReference' }],
  },
};
const table: DraftTable = {
  outputId: 'specimens', tabId: 'tab', title: 'Specimens',
  document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'specimens', title: 'Specimens' },
    rootResourceType: 'Specimen', route: { occurrenceId: 'base', resourceType: 'Specimen' }, columns: [],
  },
};
const selection: SelectionRevision = {
  id: 'selection-1', project: 'project', generation: 'generation', resourceType: 'DocumentReference',
  rule: { kind: 'EXPLICIT' }, source: { kind: 'EXPLICIT_REFS' }, scopeDigest: 'scope', ruleDigest: 'rule',
  membershipDigest: 'members', memberCount: 2, memberBytes: 20, complete: true,
  createdAt: '2026-09-16T00:00:00.000Z', completedAt: '2026-09-16T00:00:01.000Z',
};

const attachedTable = (selectionRevisionId = 'selection-1'): DraftTable => ({
  ...table,
  document: {
    ...table.document,
    population: { selectionRevisionId, route: [{ resourceType: 'DocumentReference', relationship: 'subject_Specimen' }] },
  },
});

const reportBinding = (receiptId: string, currentSelection: SelectionRevision = selection) => ({
  receiptId, outputId: table.outputId, project: currentSelection.project, explorerId: 'patients',
  generation: currentSelection.generation, scopeDigest: currentSelection.scopeDigest,
  selectionRevisionId: currentSelection.id, membershipDigest: currentSelection.membershipDigest,
  resourceType: currentSelection.resourceType,
});

beforeEach(() => {
  triggerPopulation.mockReset();
  searchPopulationRoutes.mockReset();
  searchPopulationRoutes.mockImplementation(async (request: { selectionRevisionId: string }) => ({
    snapshotToken: 'snapshot',
    outputId: table.outputId,
    selectionRevisionId: request.selectionRevisionId,
    complete: true,
    truncated: false,
    choices: [populationChoice],
  }));
});

it('attaches a selected file collection through a server-issued route choice', async () => {
  const onAttach = vi.fn();
  render(<PopulationPanel table={table} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={onAttach} onClear={vi.fn()} />);
  expect(screen.getByText(/2 selected DocumentReference resources/)).toBeTruthy();
  expect(await screen.findByText(/Specimen → DocumentReference via subject_Specimen/)).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Use selected resources' }));
  expect(onAttach).toHaveBeenCalledWith('population-route-1');
  expect(searchPopulationRoutes).toHaveBeenCalledWith(expect.objectContaining({
    snapshotToken: 'snapshot',
    outputId: 'specimens',
    selectionRevisionId: 'selection-1',
  }), expect.any(AbortSignal));
});

it('keeps an unselected table as an all-authorized-resource workflow', () => {
  render(<PopulationPanel table={table} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={vi.fn()} onClear={vi.fn()} />);
  expect(screen.getByText(/uses every authorized Specimen resource/)).toBeTruthy();
});

it('ignores a deferred report after the receipt and selection change', async () => {
  let resolveReport: (value: unknown) => void = () => undefined;
  const deferred = new Promise((resolve) => { resolveReport = resolve; });
  triggerPopulation.mockReturnValueOnce({ unwrap: () => deferred, abort: vi.fn() });
  const { rerender } = render(
    <PopulationPanel table={attachedTable()} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" receiptId="receipt-a" onAttach={vi.fn()} onClear={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Check selected-resource coverage' }));

  const changedSelection = { ...selection, id: 'selection-2', membershipDigest: 'members-2' };
  rerender(
    <PopulationPanel table={attachedTable('selection-2')} selection={changedSelection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" receiptId="receipt-b" onAttach={vi.fn()} onClear={vi.fn()} />,
  );
  await act(async () => {
    resolveReport({ binding: reportBinding('receipt-a'), status: 'COMPLETE', counts: { selected: 2, mapped: 2, unmapped: 0, emittedRows: 1 }, unmapped: [], diagnostics: [] });
    await deferred;
  });
  expect(screen.queryByText('2 selected · 2 produce rows · 0 needs attention')).toBeNull();
});

it('renders complete and incomplete report states with the safe binding', async () => {
  const onExclude = vi.fn();
  triggerPopulation.mockReturnValueOnce({ unwrap: () => Promise.resolve({
    binding: reportBinding('receipt-a'), status: 'COMPLETE',
    counts: { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 },
    unmapped: [{ project: 'project', generation: 'generation', resourceType: 'DocumentReference', id: 'dev-file-004' }],
    diagnostics: [],
  }), abort: vi.fn() });
  const { rerender } = render(
    <PopulationPanel table={attachedTable()} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" receiptId="receipt-a" onAttach={vi.fn()} onClear={vi.fn()} onExclude={onExclude} />,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Check selected-resource coverage' }));
  await waitFor(() => expect(screen.getByText('3 selected · 2 produce rows · 1 needs attention')).toBeTruthy());
  expect(screen.getByText('DocumentReference/dev-file-004')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Remove from collection' }));
  expect(onExclude).toHaveBeenCalledWith(
    { project: 'project', generation: 'generation', resourceType: 'DocumentReference', id: 'dev-file-004' },
    populationChoice.route,
  );

  triggerPopulation.mockReturnValueOnce({ unwrap: () => Promise.resolve({
    binding: reportBinding('receipt-b'), status: 'INCOMPLETE', unmapped: [], diagnostics: [{ severity: 'INFO', stage: 'populationMapping', code: 'INCOMPLETE', message: 'incomplete' }],
  }), abort: vi.fn() });
  rerender(
    <PopulationPanel table={attachedTable()} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" receiptId="receipt-b" onAttach={vi.fn()} onClear={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Check selected-resource coverage' }));
  await waitFor(() => expect(screen.getByText('Coverage check incomplete; exact counts are unavailable.')).toBeTruthy());
  expect(screen.queryByText(/selected · .* produce rows/)).toBeNull();
});
