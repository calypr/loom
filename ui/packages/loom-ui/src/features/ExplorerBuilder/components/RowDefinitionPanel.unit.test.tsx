// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowDefinitionPanel } from './RowDefinitionPanel';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
  nodes: [
    { nodeId: 'specimen', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 2 },
    { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 4 },
    { nodeId: 'subject', resourceType: 'Patient', rowRootEligible: false, populated: true, documentCount: 2 },
  ],
  edges: [
    { edgeId: 'specimen-observation', fromNodeId: 'specimen', toNodeId: 'observation', label: 'observations' },
    { edgeId: 'specimen-patient', fromNodeId: 'specimen', toNodeId: 'subject', label: 'subject' },
  ],
  candidates: [],
};

const table: DraftTable = {
  outputId: 'specimens', tabId: 'specimens', title: 'Specimens',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'specimens', title: 'Specimens' },
    rootResourceType: 'Specimen',
    route: {
      occurrenceId: 'base', resourceType: 'Specimen',
      children: [
        { occurrenceId: 'labs', resourceType: 'Observation', relationship: 'observations' },
        { occurrenceId: 'patient', resourceType: 'Patient', relationship: 'subject' },
      ],
    },
    columns: [],
  },
};

describe('RowDefinitionPanel', () => {
  it('offers the current root and eligible direct relationships as explicit row choices', () => {
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={onChange} />);

    const select = screen.getByRole('combobox', { name: 'One row per' });
    expect(screen.getByRole('option', { name: 'Specimen' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Observation via observations' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Patient/ })).toBeNull();

    fireEvent.change(select, { target: { value: 'labs' } });
    expect(onChange).toHaveBeenCalledWith('observation', 'labs');
  });

  it('disables the selector when the current root is the only safe row definition', () => {
    const rootOnly = {
      ...table,
      document: { ...table.document, route: { occurrenceId: 'base', resourceType: 'Specimen' } },
    } as DraftTable;
    render(<RowDefinitionPanel catalog={catalog} table={rootOnly} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('combobox', { name: 'One row per' })).toBeDisabled();
    expect(screen.getByText(/Add a directly related resource/)).toBeTruthy();
  });
});
