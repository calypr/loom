// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog, RowChangeUnresolvedReference } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowChangeRepairPanel } from './RowChangeRepairPanel';

afterEach(cleanup);

const routeToken = (edgeIds: ReadonlyArray<string>, anchorOccurrenceId = 'base'): string => {
  const payload = btoa(JSON.stringify({ edgeIds, anchorOccurrenceId }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `row-change-route:v1:${payload}`;
};

describe('RowChangeRepairPanel', () => {
  it('offers metadata-driven relationship choices without exposing FHIR edge ids', () => {
    const catalog: ExplorerBuilderCatalog = {
      snapshotToken: 'snapshot',
      generation: 'generation',
      routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
      nodes: [
        { nodeId: 'encounter', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'request', resourceType: 'MedicationRequest', rowRootEligible: true, populated: true, documentCount: 4 },
      ],
      edges: [
        { edgeId: 'subject-edge', fromNodeId: 'encounter', toNodeId: 'request', label: 'subject_Encounter' },
        { edgeId: 'visit-edge', fromNodeId: 'encounter', toNodeId: 'request', label: 'encounter_Encounter' },
      ],
    };
    const table: DraftTable = {
      outputId: 'table',
      tabId: 'table',
      title: 'Requests',
      document: {
        kind: 'ExplorerBuilderDocument',
        output: { id: 'table', title: 'Requests' },
        rootResourceType: 'Encounter',
        route: { occurrenceId: 'base', resourceType: 'Encounter' },
        rows: { kind: 'RECORDS', records: {} },
        columns: [],
      },
    };
    const reference: RowChangeUnresolvedReference = {
      kind: 'column',
      id: 'existing-column',
      code: 'AMBIGUOUS_ROUTE_REBASE_EDGE',
      message: 'several inverse relationships can preserve the previous row root',
      alternatives: ['subject-edge', 'visit-edge'],
    };
    const onChoose = vi.fn();

    render(<RowChangeRepairPanel unresolved={[reference]} catalog={catalog} table={table} candidateRootNodeId="request" snapshotIsCurrent disabled={false} onChoose={onChoose} onCancel={vi.fn()} />);

    expect(screen.queryByText(/inverse relationships/)).not.toBeInTheDocument();
    const subject = screen.getByRole('button', { name: 'Subject between Encounter and MedicationRequest' });
    expect(screen.getByRole('button', { name: 'Encounter between Encounter and MedicationRequest' })).toBeEnabled();
    fireEvent.click(subject);
    expect(onChoose).toHaveBeenCalledWith(reference, 'subject-edge');
  });

  it('labels catalog route alternatives from current relationship metadata and passes the opaque token unchanged', () => {
    const catalog: ExplorerBuilderCatalog = {
      snapshotToken: 'snapshot',
      generation: 'generation',
      routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 4 },
      ],
      edges: [
        { edgeId: 'subject-edge', fromNodeId: 'patient', toNodeId: 'observation', label: 'subject_Patient' },
        { edgeId: 'focus-edge', fromNodeId: 'patient', toNodeId: 'observation', label: 'focus_Patient' },
      ],
    };
    const table: DraftTable = {
      outputId: 'table',
      tabId: 'table',
      title: 'Patients',
      document: {
        kind: 'ExplorerBuilderDocument',
        output: { id: 'table', title: 'Patients' },
        rootResourceType: 'Patient',
        route: { occurrenceId: 'base', resourceType: 'Patient' },
        rows: { kind: 'RECORDS', records: {} },
        columns: [],
      },
    };
    const subjectToken = routeToken(['subject-edge']);
    const focusToken = routeToken(['focus-edge']);
    const reference: RowChangeUnresolvedReference = {
      kind: 'route',
      id: 'root-occurrence',
      code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
      message: 'Choose a connected route for Observation.',
      alternatives: [subjectToken, focusToken],
    };
    const onChoose = vi.fn();

    render(<RowChangeRepairPanel unresolved={[reference]} catalog={catalog} table={table} candidateRootNodeId="observation" snapshotIsCurrent disabled={false} onChoose={onChoose} onCancel={vi.fn()} />);

    const subject = screen.getByRole('button', { name: 'Use Patient → Subject → Observation from Patient rows' });
    expect(screen.getByRole('button', { name: 'Use Patient → Focus → Observation from Patient rows' })).toBeEnabled();
    expect(screen.queryByText(/row-change-route:v1/)).toBeNull();
    fireEvent.click(subject);
    expect(onChoose).toHaveBeenCalledWith(reference, subjectToken);
  });

  it('keeps existing authored-occurrence alternatives available for the same ambiguity code', () => {
    const catalog: ExplorerBuilderCatalog = {
      snapshotToken: 'snapshot',
      generation: 'generation',
      routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
      nodes: [
        { nodeId: 'encounter', resourceType: 'Encounter', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 4 },
      ],
      edges: [
        { edgeId: 'subject-edge', fromNodeId: 'encounter', toNodeId: 'observation', label: 'subject_Encounter' },
        { edgeId: 'visit-edge', fromNodeId: 'encounter', toNodeId: 'observation', label: 'encounter_Encounter' },
      ],
    };
    const table: DraftTable = {
      outputId: 'table',
      tabId: 'table',
      title: 'Encounters',
      document: {
        kind: 'ExplorerBuilderDocument',
        output: { id: 'table', title: 'Encounters' },
        rootResourceType: 'Encounter',
        route: {
          occurrenceId: 'base',
          resourceType: 'Encounter',
          children: [
            { occurrenceId: 'subject-observation', resourceType: 'Observation', relationship: 'subject_Encounter', catalogEdgeId: 'subject-edge' },
            { occurrenceId: 'visit-observation', resourceType: 'Observation', relationship: 'encounter_Encounter', catalogEdgeId: 'visit-edge' },
          ],
        },
        rows: { kind: 'RECORDS', records: {} },
        columns: [],
      },
    };
    const reference: RowChangeUnresolvedReference = {
      kind: 'route',
      id: 'root-occurrence',
      code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
      message: 'Choose an existing Observation occurrence.',
      alternatives: ['subject-observation', 'visit-observation'],
    };
    const onChoose = vi.fn();

    render(<RowChangeRepairPanel unresolved={[reference]} catalog={catalog} table={table} candidateRootNodeId="observation" snapshotIsCurrent disabled={false} onChoose={onChoose} onCancel={vi.fn()} />);

    const subject = screen.getByRole('button', { name: 'Observation through subject_Encounter' });
    expect(screen.getByRole('button', { name: 'Observation through encounter_Encounter' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Unavailable relationship option' })).toBeNull();
    fireEvent.click(subject);
    expect(onChoose).toHaveBeenCalledWith(reference, 'subject-observation');
  });

  it('disables malformed or stale catalog route tokens without exposing them as labels', () => {
    const catalog: ExplorerBuilderCatalog = {
      snapshotToken: 'snapshot',
      generation: 'generation',
      routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 4 },
      ],
      edges: [
        { edgeId: 'subject-edge', fromNodeId: 'patient', toNodeId: 'observation', label: 'subject_Patient' },
      ],
    };
    const table: DraftTable = {
      outputId: 'table',
      tabId: 'table',
      title: 'Patients',
      document: {
        kind: 'ExplorerBuilderDocument',
        output: { id: 'table', title: 'Patients' },
        rootResourceType: 'Patient',
        route: { occurrenceId: 'base', resourceType: 'Patient' },
        rows: { kind: 'RECORDS', records: {} },
        columns: [],
      },
    };
    const malformedToken = 'row-change-route:v1:not-base64!';
    const staleEdgeToken = routeToken(['removed-edge']);
    const reference: RowChangeUnresolvedReference = {
      kind: 'route',
      id: 'root-occurrence',
      code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
      message: 'Choose a connected route for Observation.',
      alternatives: [malformedToken, staleEdgeToken],
    };

    render(<RowChangeRepairPanel unresolved={[reference]} catalog={catalog} table={table} candidateRootNodeId="observation" snapshotIsCurrent={false} disabled={false} onChoose={vi.fn()} onCancel={vi.fn()} />);

    const unavailable = screen.getAllByRole('button', { name: 'Unavailable relationship option' });
    expect(unavailable).toHaveLength(2);
    expect(unavailable.every((button) => (button as HTMLButtonElement).disabled)).toBe(true);
    expect(screen.getByRole('alert')).toHaveTextContent('no longer available');
    expect(screen.queryByText(/row-change-route:v1/)).toBeNull();
  });
});
