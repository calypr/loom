// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
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
    { nodeId: 'encounter', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 3 },
    { nodeId: 'subject', resourceType: 'Patient', rowRootEligible: false, populated: true, documentCount: 2 },
  ],
  edges: [
    { edgeId: 'specimen-observation', fromNodeId: 'specimen', toNodeId: 'observation', label: 'observations' },
    { edgeId: 'observation-encounter', fromNodeId: 'observation', toNodeId: 'encounter', label: 'encounter' },
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
        { occurrenceId: 'labs', resourceType: 'Observation', relationship: 'observations', children: [
          { occurrenceId: 'visit', resourceType: 'Encounter', relationship: 'encounter' },
        ] },
        { occurrenceId: 'patient', resourceType: 'Patient', relationship: 'subject' },
      ],
    },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  },
};

afterEach(cleanup);

describe('RowDefinitionPanel', () => {
  it('offers every eligible resource type without requiring an authored traversal', () => {
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={onChange} />);

    const select = screen.getByRole('combobox', { name: 'One row per' });
    expect(screen.getByRole('option', { name: 'Specimen' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Observation' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Encounter' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Patient/ })).toBeNull();

    fireEvent.change(select, { target: { value: 'observation' } });
    expect(onChange).toHaveBeenCalledWith('observation');
  });

  it('offers a resource that is not on the current table route', () => {
    const rootOnly = {
      ...table,
      document: { ...table.document, route: { occurrenceId: 'base', resourceType: 'Specimen' } },
    } as DraftTable;
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={rootOnly} disabled={false} onChange={onChange} />);
    const select = screen.getByRole('combobox', { name: 'One row per' });
    expect(select).not.toBeDisabled();
    fireEvent.change(select, { target: { value: 'encounter' } });
    expect(onChange).toHaveBeenCalledWith('encounter');
  });
});
