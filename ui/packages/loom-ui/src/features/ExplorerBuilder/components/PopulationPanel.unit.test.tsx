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
    rootResourceType: 'Specimen', route: { occurrenceId: 'base', resourceType: 'Specimen' },
    rows: { kind: 'RECORDS', records: {} }, columns: [],
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
  expect(screen.getByRole('region', { name: 'Starting collection' })).toHaveAttribute(
    'data-selection-revision-id',
    'selection-1',
  );
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

it('prioritizes a direct same-resource route and distinguishes meaningful alternatives', async () => {
  const directChoice: PopulationRouteChoice = {
    ...populationChoice,
    routeChoiceId: 'direct-population-route',
    route: [],
    presentation: { summary: 'Use selected Specimen records', facts: [] },
  };
  const duplicateDirectChoice = { ...directChoice, routeChoiceId: 'duplicate-direct-route' };
  const requiredOutboundChoice: PopulationRouteChoice = {
    ...populationChoice,
    routeChoiceId: 'required-outbound-route',
    route: [{
      ...populationChoice.route[0]!,
      fromResourceType: 'Specimen',
      toResourceType: 'DocumentReference',
      relationship: 'documentation',
      storageDirection: 'OUTBOUND',
      matchMode: 'REQUIRED',
    }],
    presentation: { summary: 'Use selected Specimen records', facts: [] },
  };
  const optionalInboundChoice: PopulationRouteChoice = {
    ...requiredOutboundChoice,
    routeChoiceId: 'optional-inbound-route',
    route: [{
      ...requiredOutboundChoice.route[0]!,
      storageDirection: 'INBOUND',
      matchMode: 'OPTIONAL',
    }],
  };
  searchPopulationRoutes.mockImplementationOnce(async (request: { selectionRevisionId: string }) => ({
    snapshotToken: 'snapshot',
    outputId: table.outputId,
    selectionRevisionId: request.selectionRevisionId,
    complete: false,
    truncated: true,
    choices: [requiredOutboundChoice, optionalInboundChoice, directChoice, duplicateDirectChoice],
  }));
  const onAttach = vi.fn();
  render(<PopulationPanel table={table} selection={{ ...selection, resourceType: 'Specimen' }} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={onAttach} onClear={vi.fn()} />);

  await screen.findByRole('button', { name: 'Use selected resources' });
  expect(screen.getByText('Use selected Specimen records')).toBeTruthy();
  expect(screen.queryByRole('combobox', { name: 'Population connection' })).toBeNull();
  expect(screen.getByRole('button', { name: 'Other connections' })).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText(/automatic route-search limit/)).toBeNull();

  fireEvent.click(screen.getByRole('button', { name: 'Use selected resources' }));
  expect(onAttach).toHaveBeenNthCalledWith(1, 'direct-population-route');

  fireEvent.click(screen.getByRole('button', { name: 'Other connections' }));
  const options = await screen.findAllByRole('option');
  expect(options).toHaveLength(3);
  expect(options[0]).toHaveTextContent('Use selected Specimen records');
  expect(options[1]?.textContent).not.toBe(options[2]?.textContent);
  expect(options[1]).toHaveTextContent('documentation');
  expect(options[2]).toHaveTextContent('documentation');
  expect(options[1]?.textContent).toMatch(/outgoing|required/i);
  expect(options[2]?.textContent).toMatch(/incoming|available/i);
  fireEvent.change(screen.getByRole('combobox', { name: 'Population connection' }), { target: { value: '1' } });
  expect(await screen.findByText(/automatic route-search limit/)).toBeTruthy();

  fireEvent.click(screen.getByRole('button', { name: 'Use selected resources' }));
  expect(onAttach).toHaveBeenNthCalledWith(2, 'required-outbound-route');
});

it('keeps the route-search warning when no direct same-resource route is available', async () => {
  const secondConnection: PopulationRouteChoice = {
    ...populationChoice,
    routeChoiceId: 'alternate-population-route',
    route: [{ ...populationChoice.route[0]!, relationship: 'encounter' }],
    presentation: { summary: 'Specimen through encounter', facts: [] },
  };
  searchPopulationRoutes.mockImplementationOnce(async (request: { selectionRevisionId: string }) => ({
    snapshotToken: 'snapshot',
    outputId: table.outputId,
    selectionRevisionId: request.selectionRevisionId,
    complete: false,
    truncated: true,
    choices: [populationChoice, secondConnection],
  }));
  render(<PopulationPanel table={table} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={vi.fn()} onClear={vi.fn()} />);

  expect(await screen.findByRole('combobox', { name: 'Population connection' })).toBeTruthy();
  expect(await screen.findByText(/automatic route-search limit/)).toBeTruthy();
});

it('preserves an attached non-direct route when a direct route is also available', async () => {
  const directChoice: PopulationRouteChoice = {
    ...populationChoice,
    routeChoiceId: 'direct-population-route',
    route: [],
    presentation: { summary: 'Use selected Specimen records', facts: [] },
  };
  const savedRouteChoice: PopulationRouteChoice = {
    ...populationChoice,
    routeChoiceId: 'saved-non-direct-route',
    route: [{
      ...populationChoice.route[0]!,
      fromResourceType: 'Specimen',
      toResourceType: 'Specimen',
      relationship: 'relatedStructure',
    }],
  };
  const savedTable: DraftTable = {
    ...table,
    document: {
      ...table.document,
      population: {
        selectionRevisionId: 'selection-1',
        route: [{ resourceType: 'Specimen', relationship: 'relatedStructure' }],
      },
    },
  };
  searchPopulationRoutes.mockImplementationOnce(async (request: { selectionRevisionId: string }) => ({
    snapshotToken: 'snapshot',
    outputId: table.outputId,
    selectionRevisionId: request.selectionRevisionId,
    complete: false,
    truncated: true,
    choices: [directChoice, savedRouteChoice],
  }));
  render(<PopulationPanel table={savedTable} selection={{ ...selection, resourceType: 'Specimen' }} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={vi.fn()} onClear={vi.fn()} />);

  const panel = screen.getByRole('region', { name: 'Starting collection' });
  expect(panel).toHaveAttribute('data-attached-selection-revision-id', 'selection-1');
  expect(screen.getByRole('button', { name: 'Use all authorized rows' })).toBeTruthy();
  expect(screen.queryByRole('combobox', { name: 'Population connection' })).toBeNull();
  expect(await screen.findByText(/automatic route-search limit/)).toBeTruthy();
});

it('keeps an unselected table as an all-authorized-resource workflow', () => {
  render(<PopulationPanel table={table} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={vi.fn()} onClear={vi.fn()} />);
  expect(screen.getByText(/uses every authorized Specimen resource/)).toBeTruthy();
});

it('exposes active and attached revision identities at the collection boundary', () => {
  render(<PopulationPanel table={attachedTable()} selection={selection} loading={false} disabled={false} project="project" explorerId="patients" snapshotToken="snapshot" onAttach={vi.fn()} onClear={vi.fn()} />);
  const panel = screen.getByRole('region', { name: 'Starting collection' });
  expect(panel).toHaveAttribute('data-selection-revision-id', 'selection-1');
  expect(panel).toHaveAttribute('data-attached-selection-revision-id', 'selection-1');
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
