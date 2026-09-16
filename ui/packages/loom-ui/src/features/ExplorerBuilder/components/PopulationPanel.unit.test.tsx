// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { SelectionRevision } from '../../../selection';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { PopulationPanel } from './PopulationPanel';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot', generation: 'generation', routePolicy: {},
  nodes: [
    { nodeId: 'specimen', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 2 },
    { nodeId: 'files', resourceType: 'DocumentReference', rowRootEligible: true, populated: true, documentCount: 4 },
  ],
  edges: [{ edgeId: 'subject-edge', fromNodeId: 'specimen', toNodeId: 'files', label: 'subject_Specimen', populated: true }],
  candidates: [],
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

it('attaches a selected file collection through the unique Specimen route', () => {
  const onAttach = vi.fn();
  render(<PopulationPanel catalog={catalog} table={table} selection={selection} loading={false} disabled={false} onAttach={onAttach} onClear={vi.fn()} />);
  expect(screen.getByText(/2 selected DocumentReference resources/)).toBeTruthy();
  expect(screen.getByText(/Specimen → DocumentReference via subject_Specimen/)).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Use selected resources' }));
  expect(onAttach).toHaveBeenCalledWith(['subject-edge']);
});

it('keeps an unselected table as an all-authorized-resource workflow', () => {
  render(<PopulationPanel catalog={catalog} table={table} loading={false} disabled={false} onAttach={vi.fn()} onClear={vi.fn()} />);
  expect(screen.getByText(/uses every authorized Specimen resource/)).toBeTruthy();
});
