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

const patientTable: DraftTable = {
  ...table,
  document: {
    ...table.document,
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
  },
};

const patientCatalog: ExplorerBuilderCatalog = {
  ...catalog,
  nodes: [
    { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 18 },
    { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 18 },
    { nodeId: 'condition', resourceType: 'Condition', rowRootEligible: true, populated: true, documentCount: 8 },
    { nodeId: 'medication', resourceType: 'Medication', rowRootEligible: true, populated: false, documentCount: 0 },
  ],
  edges: [
    { edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', label: 'subject_Patient' },
    { edgeId: 'observation-patient', fromNodeId: 'observation', toNodeId: 'patient', label: 'subject_Patient' },
    { edgeId: 'observation-condition', fromNodeId: 'observation', toNodeId: 'condition', label: 'focus_Observation' },
    { edgeId: 'condition-observation', fromNodeId: 'condition', toNodeId: 'observation', label: 'focus_Observation' },
    { edgeId: 'condition-patient', fromNodeId: 'condition', toNodeId: 'patient', label: 'subject_Patient' },
  ],
};

afterEach(cleanup);

describe('RowDefinitionPanel', () => {
  it('shows one Record type picker and makes the current type a no-op', () => {
    const onChange = vi.fn();
    const onReplace = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'What should each row represent?' });
    expect(within(section).getAllByRole('combobox')).toHaveLength(1);
    expect(within(section).getByRole('combobox', { name: 'Record type' })).toBeTruthy();
    expect(within(section).getByText('Choose a record type. Loom will show how this changes your rows and whether existing columns or filters need updating.')).toBeTruthy();
    expect(within(section).getByRole('option', { name: 'Specimen (current)' })).toHaveProperty('disabled', true);
    expect(within(section).getByRole('button', { name: 'Review changes' })).toHaveProperty('disabled', true);
    expect(within(section).queryByRole('combobox', { name: 'Choose replacement starting type' })).toBeNull();
    expect(within(section).queryByRole('button', { name: 'Replace current setup' })).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
    expect(onReplace).not.toHaveBeenCalled();
  });

  it('reviews a connected route through the existing preserve callback', () => {
    const onChange = vi.fn();
    render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={onChange} />);

    const section = screen.getByRole('region', { name: 'What should each row represent?' });
    const picker = within(section).getByRole('combobox', { name: 'Record type' });
    fireEvent.change(picker, { target: { value: 'occurrence:labs' } });
    expect(within(section).getByText('Loom will check which existing columns and filters can carry over.')).toBeTruthy();
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(within(section).getByRole('button', { name: 'Review changes' }));

    expect(onChange).toHaveBeenCalledExactlyOnceWith({
      kind: 'authored-occurrence',
      nodeId: 'observation',
      occurrenceId: 'labs',
    } satisfies RootChangeSelection);
  });

  it('reviews a replacement-only loaded eligible type through the assessed replacement flow', () => {
    const disconnectedCatalog: ExplorerBuilderCatalog = {
      ...patientCatalog,
      nodes: [
        { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 4 },
        { nodeId: 'research-study-node', resourceType: 'ResearchStudy', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'unpopulated-node', resourceType: 'Medication', rowRootEligible: true, populated: false, documentCount: 0 },
      ],
      edges: [],
    };
    const onChange = vi.fn();
    const onReplace = vi.fn();
    render(<RowDefinitionPanel catalog={disconnectedCatalog} table={patientTable} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'What should each row represent?' });
    const picker = within(section).getByRole('combobox', { name: 'Record type' });
    expect(within(picker).getByRole('option', { name: 'ResearchStudy' })).toBeTruthy();
    expect(within(picker).getByRole('option', { name: 'Medication' })).toBeTruthy();
    fireEvent.change(picker, { target: { value: 'replacement:research-study-node' } });
    expect(within(section).getByText('A new setup is required. Review will list the current setup items that would be removed.')).toBeTruthy();
    expect(onReplace).not.toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(within(section).getByRole('button', { name: 'Review changes' }));

    expect(onReplace).toHaveBeenCalledExactlyOnceWith('research-study-node');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('offers Start fresh for a selected connected type without opening preserve review', () => {
    const onChange = vi.fn();
    const onReplace = vi.fn();
    render(<RowDefinitionPanel catalog={patientCatalog} table={patientTable} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'What should each row represent?' });
    fireEvent.change(within(section).getByRole('combobox', { name: 'Record type' }), {
      target: { value: 'node:observation' },
    });
    expect(onChange).not.toHaveBeenCalled();
    expect(onReplace).not.toHaveBeenCalled();
    fireEvent.click(within(section).getByRole('button', { name: 'Start fresh with Observation' }));

    expect(onReplace).toHaveBeenCalledExactlyOnceWith('observation');
    expect(onChange).not.toHaveBeenCalled();
  });

  it('keeps connected routes with the same resource type distinct and readable', () => {
    const routeCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      edges: [
        ...catalog.edges,
        { edgeId: 'specimen-encounter', fromNodeId: 'specimen', toNodeId: 'encounter', label: 'encounters' },
      ],
    };
    const routeTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          occurrenceId: 'base',
          resourceType: 'Specimen',
          children: [
            {
              occurrenceId: 'labs',
              resourceType: 'Observation',
              relationship: 'observations',
              children: [{ occurrenceId: 'visit-via-labs', resourceType: 'Encounter', relationship: 'encounter' }],
            },
            { occurrenceId: 'visit-direct', resourceType: 'Encounter', relationship: 'encounters' },
          ],
        },
      },
    };
    render(<RowDefinitionPanel catalog={routeCatalog} table={routeTable} disabled={false} onChange={vi.fn()} />);

    const picker = screen.getByRole('combobox', { name: 'Record type' });
    const encounterOptions = within(picker).getAllByRole('option', { name: /^Encounter via / });
    expect(encounterOptions).toHaveLength(2);
    expect(encounterOptions[0]).toHaveTextContent('Encounter via Specimen → Observations → Observation → Encounter');
    expect(encounterOptions[1]).toHaveTextContent('Encounter via Specimen → Encounters → Encounter');
    expect(encounterOptions[0].value).not.toBe(encounterOptions[1].value);
  });

  it('resets the selected type when the snapshot, table, or current root changes', () => {
    const { rerender } = render(<RowDefinitionPanel catalog={catalog} table={table} disabled={false} onChange={vi.fn()} />);
    const selectRoute = () => fireEvent.change(screen.getByRole('combobox', { name: 'Record type' }), {
      target: { value: 'occurrence:labs' },
    });

    selectRoute();
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'occurrence:labs');
    rerender(<RowDefinitionPanel catalog={catalog} table={{ ...table, outputId: 'other-table' }} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'current');

    selectRoute();
    const nextSnapshot = { ...catalog, snapshotToken: 'next-snapshot' };
    rerender(<RowDefinitionPanel catalog={nextSnapshot} table={{ ...table, outputId: 'other-table' }} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'current');

    selectRoute();
    const observationTable: DraftTable = {
      ...table,
      outputId: 'other-table',
      document: {
        ...table.document,
        rootResourceType: 'Observation',
        route: {
          occurrenceId: 'base-observation',
          resourceType: 'Observation',
          children: [{ occurrenceId: 'encounter', resourceType: 'Encounter', relationship: 'encounter' }],
        },
      },
    };
    rerender(<RowDefinitionPanel catalog={nextSnapshot} table={observationTable} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'current');
  });

  it('omits the box when no other connected type is available and replacement review is unavailable', () => {
    const rootOnly: DraftTable = {
      ...table,
      document: { ...table.document, route: { occurrenceId: 'base', resourceType: 'Specimen' } },
    };
    const unconnectedCatalog = { ...catalog, edges: [] };
    render(<RowDefinitionPanel catalog={unconnectedCatalog} table={rootOnly} disabled={false} onChange={vi.fn()} />);
    expect(screen.queryByRole('combobox', { name: 'Record type' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'What should each row represent?' })).toBeNull();
  });
});
