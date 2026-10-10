// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { RootChangeSelection } from '../authoring/rootChangeSelection';
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
  it('offers every eligible authored occurrence as an explicit row choice', () => {
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={onChange} />);

    const section = screen.getByRole('region', { name: 'Starting record type' });
    expect(within(section).getByRole('heading', { name: 'Starting record type' })).toBeTruthy();
    const select = within(section).getByRole('combobox', { name: 'Choose starting record type' });
    expect(within(section).getByRole('option', { name: 'Specimen (current)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Observation via observations' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Encounter via encounter' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Patient/ })).toBeNull();

    fireEvent.change(select, { target: { value: 'occurrence:labs' } });
    expect(onChange).toHaveBeenCalledWith({
      kind: 'authored-occurrence',
      nodeId: 'observation',
      occurrenceId: 'labs',
    } satisfies RootChangeSelection);
  });

  it('offers connected populated catalog types from a table with no authored descendant', () => {
    const patientCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 18 },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 18 },
        { nodeId: 'condition', resourceType: 'Condition', rowRootEligible: true, populated: true, documentCount: 8 },
        { nodeId: 'medication', resourceType: 'Medication', rowRootEligible: true, populated: false, documentCount: 0 },
        { nodeId: 'encounter', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 3 },
      ],
      edges: [
        { edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', label: 'subject_Patient' },
        { edgeId: 'observation-patient', fromNodeId: 'observation', toNodeId: 'patient', label: 'subject_Patient' },
        { edgeId: 'observation-condition', fromNodeId: 'observation', toNodeId: 'condition', label: 'focus_Observation' },
        { edgeId: 'condition-observation', fromNodeId: 'condition', toNodeId: 'observation', label: 'focus_Observation' },
        { edgeId: 'condition-patient', fromNodeId: 'condition', toNodeId: 'patient', label: 'subject_Patient' },
        { edgeId: 'patient-medication', fromNodeId: 'patient', toNodeId: 'medication', label: 'medication' },
      ],
    };
    const plainPatient: DraftTable = {
      ...table,
      document: {
        ...table.document,
        rootResourceType: 'Patient',
        route: { occurrenceId: 'base', resourceType: 'Patient' },
      },
    };
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={patientCatalog} table={plainPatient} disabled={false} onChange={onChange} />);

    const select = screen.getByRole('combobox', { name: 'Choose starting record type' });
    expect(screen.getByRole('option', { name: 'Patient (current)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Observation via Patient → Subject → Observation' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Condition via Patient → Subject → Observation → Focus → Condition' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Medication/ })).toBeNull();
    expect(screen.queryByRole('option', { name: /Encounter/ })).toBeNull();

    fireEvent.change(select, { target: { value: 'node:observation' } });
    expect(onChange).toHaveBeenCalledWith({ kind: 'catalog-node', nodeId: 'observation' });
  });

  it('omits the selector when no other populated eligible root is connected', () => {
    const rootOnly = {
      ...table,
      document: { ...table.document, route: { occurrenceId: 'base', resourceType: 'Specimen' } },
    } as DraftTable;
    const unconnectedCatalog = { ...catalog, edges: [] };
    render(<RowDefinitionPanel catalog={unconnectedCatalog} table={rootOnly} disabled={false} onChange={vi.fn()} />);
    expect(screen.queryByRole('combobox', { name: 'Choose starting record type' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Starting record type' })).toBeNull();
  });
});
