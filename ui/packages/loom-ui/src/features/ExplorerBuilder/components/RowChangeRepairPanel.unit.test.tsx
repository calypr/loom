// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog, RowChangeUnresolvedReference } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowChangeRepairPanel } from './RowChangeRepairPanel';

afterEach(cleanup);

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

    render(<RowChangeRepairPanel unresolved={[reference]} catalog={catalog} table={table} disabled={false} onChoose={onChoose} onCancel={vi.fn()} />);

    expect(screen.queryByText(/inverse relationships/)).not.toBeInTheDocument();
    const subject = screen.getByRole('button', { name: 'Match through Subject between Encounter and MedicationRequest' });
    expect(screen.getByRole('button', { name: 'Match through Encounter between Encounter and MedicationRequest' })).toBeEnabled();
    fireEvent.click(subject);
    expect(onChoose).toHaveBeenCalledWith(reference, 'subject-edge');
  });
});
