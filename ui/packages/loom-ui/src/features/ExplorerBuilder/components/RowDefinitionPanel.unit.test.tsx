// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import type {
  RootChangePickerState,
  RootChangeRequestIdentity,
  RootChangeSelection,
} from '../authoring/rootChangeSelection';
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

const rootOnlyTable: DraftTable = {
  ...table,
  document: {
    ...table.document,
    route: { occurrenceId: 'base', resourceType: 'Specimen' },
  },
};

const specimenWithObservationTable: DraftTable = {
  ...rootOnlyTable,
  document: {
    ...rootOnlyTable.document,
    route: {
      occurrenceId: 'base',
      resourceType: 'Specimen',
      children: [{ occurrenceId: 'labs', resourceType: 'Observation', relationship: 'observations' }],
    },
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

const routeToken = (edgeIds: ReadonlyArray<string>, anchorOccurrenceId = 'base'): string => {
  const payload = btoa(JSON.stringify({ edgeIds, anchorOccurrenceId }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `row-change-route:v1:${payload}`;
};

const compilerRouteCatalog: ExplorerBuilderCatalog = {
  ...catalog,
  nodes: [
    { nodeId: 'specimen-node', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 4 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 4 },
    { nodeId: 'encounter-node', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 3 },
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 4 },
  ],
  edges: [
    { edgeId: 'specimen-observation', fromNodeId: 'specimen-node', toNodeId: 'observation-node', label: 'observations' },
    { edgeId: 'observation-specimen', fromNodeId: 'observation-node', toNodeId: 'specimen-node', label: 'observations' },
    { edgeId: 'observation-encounter', fromNodeId: 'observation-node', toNodeId: 'encounter-node', label: 'encounter' },
    { edgeId: 'encounter-observation', fromNodeId: 'encounter-node', toNodeId: 'observation-node', label: 'encounter' },
    { edgeId: 'observation-patient', fromNodeId: 'observation-node', toNodeId: 'patient-node', label: 'subject' },
    { edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'subject' },
    { edgeId: 'observation-patient-alt', fromNodeId: 'observation-node', toNodeId: 'patient-node', label: 'performer' },
    { edgeId: 'patient-observation-alt', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'performer' },
    { edgeId: 'specimen-encounter', fromNodeId: 'specimen-node', toNodeId: 'encounter-node', label: 'encounters' },
    { edgeId: 'encounter-specimen', fromNodeId: 'encounter-node', toNodeId: 'specimen-node', label: 'encounters' },
    { edgeId: 'encounter-patient', fromNodeId: 'encounter-node', toNodeId: 'patient-node', label: 'subject' },
    { edgeId: 'patient-encounter', fromNodeId: 'patient-node', toNodeId: 'encounter-node', label: 'subject' },
  ],
};

const specimenObservationPatientRoute = routeToken(['specimen-observation', 'observation-patient']);
const specimenEncounterPatientRoute = routeToken(['specimen-encounter', 'encounter-patient']);

const rootIdentity = (
  overrides: Partial<RootChangeRequestIdentity> = {},
): RootChangeRequestIdentity => ({
  epoch: 3,
  project: 'BForePC',
  explorerId: 'test-explorer',
  outputId: 'specimens',
  snapshotToken: 'snapshot',
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  ...overrides,
});

const idleRootPickerProps = () => ({
  rootChangeState: { kind: 'idle' } as const satisfies RootChangePickerState,
  currentIdentity: rootIdentity(),
  onResolve: vi.fn(),
  onRefresh: vi.fn(),
  onKeepCurrent: vi.fn(),
});

const blockedRootRouteState: RootChangePickerState = {
  kind: 'blocked',
  selection: { kind: 'catalog-node', nodeId: 'patient-node' },
  resolution: {},
  identity: rootIdentity(),
  assessment: {
    status: 'BLOCKED',
    snapshotToken: 'snapshot',
    draftVersion: 1,
    draftDigest: 'sha256:draft-1',
    currentRootResourceType: 'Specimen',
    candidateRootResourceType: 'Patient',
    preservedFeatureKeys: [],
    unresolved: [{
      kind: 'route',
      id: 'root-occurrence',
      code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
      message: 'Choose the compiler-proven route for Patient.',
      alternatives: [specimenObservationPatientRoute, specimenEncounterPatientRoute],
    }],
    diagnostics: [],
  },
};

afterEach(cleanup);

describe('RowDefinitionPanel', () => {
  it('shows one Record type picker and makes the current type a no-op', () => {
    const onChange = vi.fn();
    const onReplace = vi.fn();
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={catalog} table={table} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'Starting record type' });
    expect(within(section).getAllByRole('combobox')).toHaveLength(1);
    expect(within(section).getByRole('combobox', { name: 'Record type' })).toBeTruthy();
    expect(within(section).getByText('Starting record type')).toBeTruthy();
    expect(within(section).queryByText(/Choose a record type/)).toBeNull();
    expect(within(section).getByRole('option', { name: 'Specimen (current)' })).toHaveProperty('disabled', true);
    expect(within(section).queryByRole('button', { name: 'Review changes' })).toBeNull();
    expect(within(section).queryByRole('combobox', { name: 'Choose replacement starting type' })).toBeNull();
    expect(within(section).queryByRole('button', { name: 'Replace current setup' })).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
    expect(onReplace).not.toHaveBeenCalled();
  });

  it('submits a connected route from the Record type picker without a second review action', () => {
    const onChange = vi.fn();
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={catalog} table={table} disabled={false} onChange={onChange} />);

    const section = screen.getByRole('region', { name: 'Starting record type' });
    const picker = within(section).getByRole('combobox', { name: 'Record type' });
    fireEvent.change(picker, { target: { value: 'occurrence:labs' } });
    expect(onChange).toHaveBeenCalledExactlyOnceWith({
      kind: 'authored-occurrence',
      nodeId: 'observation',
      occurrenceId: 'labs',
    } satisfies RootChangeSelection);
    expect(within(section).queryByRole('button', { name: 'Review changes' })).toBeNull();
  });

  it('starts the assessed replacement flow from a replacement-only picker choice', () => {
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
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={disconnectedCatalog} table={patientTable} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'Starting record type' });
    const picker = within(section).getByRole('combobox', { name: 'Record type' });
    expect(within(picker).getByRole('option', { name: 'ResearchStudy' })).toBeTruthy();
    expect(within(picker).getByRole('option', { name: 'Medication' })).toBeTruthy();
    fireEvent.change(picker, { target: { value: 'replacement:research-study-node' } });
    expect(onReplace).toHaveBeenCalledExactlyOnceWith('research-study-node');
    expect(onChange).not.toHaveBeenCalled();
    expect(within(section).queryByRole('button', { name: 'Review changes' })).toBeNull();
  });

  it('keeps connected Start fresh as a separate contextual replacement action', () => {
    const onChange = vi.fn();
    const onReplace = vi.fn();
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={patientCatalog} table={patientTable} disabled={false} onChange={onChange} onReplace={onReplace} />);

    const section = screen.getByRole('region', { name: 'Starting record type' });
    fireEvent.change(within(section).getByRole('combobox', { name: 'Record type' }), {
      target: { value: 'node:observation' },
    });
    expect(onChange).toHaveBeenCalledExactlyOnceWith({ kind: 'catalog-node', nodeId: 'observation' });
    expect(onReplace).not.toHaveBeenCalled();
    expect(within(section).queryByRole('button', { name: 'Review changes' })).toBeNull();
    fireEvent.click(within(section).getByRole('button', { name: 'Start fresh with Observation' }));

    expect(onReplace).toHaveBeenCalledExactlyOnceWith('observation');
    expect(onChange).toHaveBeenCalledExactlyOnceWith({ kind: 'catalog-node', nodeId: 'observation' });
  });

  it('keeps compiler-proven route, occurrence, and edge choices in the single Record type picker', () => {
    const onChange = vi.fn();
    const onResolve = vi.fn();
    const onRefresh = vi.fn();
    const onKeepCurrent = vi.fn();
    const onReplace = vi.fn();
    const view = render(
      <RowDefinitionPanel
        catalog={compilerRouteCatalog}
        table={rootOnlyTable}
        disabled={false}
        currentIdentity={blockedRootRouteState.identity}
        rootChangeState={blockedRootRouteState}
        onChange={onChange}
        onResolve={onResolve}
        onRefresh={onRefresh}
        onKeepCurrent={onKeepCurrent}
        onReplace={onReplace}
      />,
    );

    const section = screen.getByRole('region', { name: 'Starting record type' });
    const picker = within(section).getByRole('combobox', { name: 'Record type' });
    const routeOption = within(picker).getByRole('option', {
      name: 'Use Specimen → Observations → Observation → Subject → Patient from Specimen rows',
    });
    expect(within(section).getAllByRole('combobox')).toHaveLength(1);
    expect(within(picker).getAllByRole('option', { name: /^Use Specimen/ })).toHaveLength(2);
    expect(within(section).queryByRole('button', { name: 'Review changes' })).toBeNull();
    expect(within(section).queryByRole('heading', { name: 'Choose how to preserve this table' })).toBeNull();
    fireEvent.change(picker, { target: { value: routeOption.getAttribute('value') } });
    expect(onChange).toHaveBeenLastCalledWith({
      kind: 'compiler-route', nodeId: 'patient-node', referenceId: 'root-occurrence',
      routeToken: specimenObservationPatientRoute,
    } satisfies RootChangeSelection);

    const occurrenceTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          occurrenceId: 'base',
          resourceType: 'Specimen',
          children: [
            { occurrenceId: 'labs', resourceType: 'Observation', relationship: 'observations',
              children: [{ occurrenceId: 'visit-via-labs', resourceType: 'Encounter', relationship: 'encounter' }] },
            { occurrenceId: 'visit-direct', resourceType: 'Encounter', relationship: 'encounters' },
          ],
        },
      },
    };
    const occurrenceState: RootChangePickerState = {
      kind: 'blocked', selection: { kind: 'catalog-node', nodeId: 'encounter-node' }, resolution: {},
      identity: rootIdentity(),
      assessment: {
        ...blockedRootRouteState.assessment,
        candidateRootResourceType: 'Encounter',
        unresolved: [{ kind: 'route', id: 'root-occurrence', code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
          message: 'Choose the Encounter occurrence to use.', alternatives: ['visit-via-labs', 'visit-direct'] }],
      },
    };
    view.rerender(
      <RowDefinitionPanel catalog={compilerRouteCatalog} table={occurrenceTable} disabled={false}
        currentIdentity={occurrenceState.identity} rootChangeState={occurrenceState} onChange={onChange}
        onResolve={onResolve} onRefresh={onRefresh} onKeepCurrent={onKeepCurrent} onReplace={onReplace} />,
    );
    const occurrencePicker = screen.getByRole('combobox', { name: 'Record type' });
    const directEncounter = within(occurrencePicker).getByRole('option', { name: 'Use Specimen → Encounters → Encounter' });
    expect(within(occurrencePicker).getByRole('option', {
      name: 'Use Specimen → Observations → Observation → Encounter → Encounter',
    })).toBeTruthy();
    fireEvent.change(occurrencePicker, { target: { value: directEncounter.getAttribute('value') } });
    expect(onChange).toHaveBeenLastCalledWith({
      kind: 'compiler-occurrence', nodeId: 'encounter-node', referenceId: 'root-occurrence',
      occurrenceId: 'visit-direct',
    } satisfies RootChangeSelection);

    const edgeState: RootChangePickerState = {
      ...blockedRootRouteState,
      assessment: {
        ...blockedRootRouteState.assessment,
        unresolved: [{ kind: 'route', id: 'labs', code: 'AMBIGUOUS_ROUTE_REBASE_EDGE',
          message: 'Choose how this route stays connected.',
          alternatives: ['patient-observation', 'patient-observation-alt'] }],
      },
    };
    view.rerender(
      <RowDefinitionPanel catalog={compilerRouteCatalog} table={specimenWithObservationTable} disabled={false}
        currentIdentity={edgeState.identity} rootChangeState={edgeState} onChange={onChange}
        onResolve={onResolve} onRefresh={onRefresh} onKeepCurrent={onKeepCurrent} onReplace={onReplace} />,
    );
    const edgePicker = screen.getByRole('combobox', { name: 'Record type' });
    const edgeOption = within(edgePicker).getByRole('option', {
      name: 'Use Subject between Patient and Observation for Choose how this route stays connected.',
    });
    expect(screen.getAllByRole('combobox', { name: 'Record type' })).toHaveLength(1);
    expect(screen.queryByRole('heading', { name: 'Choose how to preserve this table' })).toBeNull();
    fireEvent.change(edgePicker, { target: { value: edgeOption.getAttribute('value') } });
    expect(onResolve).toHaveBeenCalledExactlyOnceWith('labs', 'patient-observation');

    const staleIdentities: ReadonlyArray<RootChangeRequestIdentity> = [
      rootIdentity({ epoch: 4 }), rootIdentity({ project: 'NextProject' }),
      rootIdentity({ authResourcePath: 'Patient/other' }), rootIdentity({ explorerId: 'next-explorer' }),
      rootIdentity({ outputId: 'other-table' }), rootIdentity({ snapshotToken: 'next-snapshot' }),
      rootIdentity({ draftVersion: 2 }), rootIdentity({ draftDigest: 'sha256:draft-2' }),
    ];
    for (const currentIdentity of staleIdentities) {
      view.rerender(
        <RowDefinitionPanel catalog={compilerRouteCatalog} table={specimenWithObservationTable} disabled={false}
          currentIdentity={currentIdentity} rootChangeState={blockedRootRouteState} onChange={onChange}
          onResolve={onResolve} onRefresh={onRefresh} onKeepCurrent={onKeepCurrent} onReplace={onReplace} />,
      );
      expect(within(screen.getByRole('combobox', { name: 'Record type' }))
        .queryByRole('option', { name: /^Use Specimen/ })).toBeNull();
      expect(screen.getByRole('button', { name: 'Refresh choices' })).toBeEnabled();
      expect(screen.getByRole('button', { name: 'Keep current rows' })).toBeEnabled();
    }
    fireEvent.click(screen.getByRole('button', { name: 'Refresh choices' }));
    fireEvent.click(screen.getByRole('button', { name: 'Keep current rows' }));
    expect(onRefresh).toHaveBeenCalledOnce();
    expect(onKeepCurrent).toHaveBeenCalledOnce();
    expect(onReplace).not.toHaveBeenCalled();
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
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={routeCatalog} table={routeTable} disabled={false} onChange={vi.fn()} />);

    const picker = screen.getByRole('combobox', { name: 'Record type' });
    const encounterOptions = within(picker).getAllByRole('option', { name: /^Encounter via / });
    expect(encounterOptions).toHaveLength(2);
    expect(encounterOptions[0]).toHaveTextContent('Encounter via Specimen → Observations → Observation → Encounter');
    expect(encounterOptions[1]).toHaveTextContent('Encounter via Specimen → Encounters → Encounter');
    expect(encounterOptions[0].getAttribute('value')).not.toBe(encounterOptions[1].getAttribute('value'));
  });

  it('resets the selected type when the snapshot, table, or current root changes', () => {
    const { rerender } = render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={catalog} table={table} disabled={false} onChange={vi.fn()} />);
    const selectRoute = () => fireEvent.change(screen.getByRole('combobox', { name: 'Record type' }), {
      target: { value: 'occurrence:labs' },
    });

    selectRoute();
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'occurrence:labs');
    rerender(<RowDefinitionPanel {...idleRootPickerProps()} currentIdentity={rootIdentity({ outputId: 'other-table' })} catalog={catalog} table={{ ...table, outputId: 'other-table' }} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'current');

    selectRoute();
    const nextSnapshot = { ...catalog, snapshotToken: 'next-snapshot' };
    rerender(<RowDefinitionPanel {...idleRootPickerProps()} currentIdentity={rootIdentity({ outputId: 'other-table', snapshotToken: 'next-snapshot' })} catalog={nextSnapshot} table={{ ...table, outputId: 'other-table' }} disabled={false} onChange={vi.fn()} />);
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
    rerender(<RowDefinitionPanel {...idleRootPickerProps()} currentIdentity={rootIdentity({ outputId: 'other-table', snapshotToken: 'next-snapshot' })} catalog={nextSnapshot} table={observationTable} disabled={false} onChange={vi.fn()} />);
    expect(screen.getByRole('option', { selected: true })).toHaveProperty('value', 'current');
  });

  it('omits the box when no other connected type is available and replacement review is unavailable', () => {
    const rootOnly: DraftTable = {
      ...table,
      document: { ...table.document, route: { occurrenceId: 'base', resourceType: 'Specimen' } },
    };
    const unconnectedCatalog = { ...catalog, edges: [] };
    render(<RowDefinitionPanel {...idleRootPickerProps()} catalog={unconnectedCatalog} table={rootOnly} disabled={false} onChange={vi.fn()} />);
    expect(screen.queryByRole('combobox', { name: 'Record type' })).toBeNull();
    expect(screen.queryByRole('region', { name: 'Starting record type' })).toBeNull();
  });
});
