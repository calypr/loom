// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RouteExtensionPanel } from './RouteExtensionPanel';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: { allowRepeatedEdges: true, allowSelfLoops: false },
  nodes: [
    {
      nodeId: 'specimen',
      resourceType: 'Specimen',
      rowRootEligible: true,
      populated: true,
      documentCount: 2,
    },
    {
      nodeId: 'observation',
      resourceType: 'Observation',
      rowRootEligible: true,
      populated: true,
      documentCount: 2,
    },
    {
      nodeId: 'patient',
      resourceType: 'Patient',
      rowRootEligible: true,
      populated: true,
      documentCount: 2,
    },
  ],
  edges: [
    {
      edgeId: 'specimen-observation',
      fromNodeId: 'specimen',
      toNodeId: 'observation',
      label: 'focus_Specimen',
    },
    {
      edgeId: 'specimen-patient',
      fromNodeId: 'specimen',
      toNodeId: 'patient',
      label: 'subject_Patient',
    },
    {
      edgeId: 'observation-patient',
      fromNodeId: 'observation',
      toNodeId: 'patient',
      label: 'subject_Patient',
    },
  ],
  candidates: [],
};

const table: DraftTable = {
  outputId: 'Specimen',
  tabId: 'specimen',
  title: 'Specimen',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'Specimen', title: 'Specimen' },
    rootResourceType: 'Specimen',
    route: {
      occurrenceId: 'base',
      resourceType: 'Specimen',
      children: [
        {
          occurrenceId: 'observation',
          resourceType: 'Observation',
          relationship: 'focus_Specimen',
        },
      ],
    },
    columns: [],
  },
};

describe('RouteExtensionPanel', () => {
  it('does not render relationship controls for one unambiguous edge', () => {
    const { container } = render(
      <RouteExtensionPanel
        catalog={catalog}
        table={table}
        selectedOccurrenceId="base"
        inspectedNodeId="patient"
        disabled={false}
        onSelectEdge={vi.fn()}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={vi.fn()}
        onAddEdge={vi.fn()}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });

  it('only asks for a relationship when multiple edges reach the resource', () => {
    const onSelectEdge = vi.fn();
    const onAddEdge = vi.fn();
    const ambiguousCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      edges: [
        ...catalog.edges,
        {
          edgeId: 'specimen-patient-secondary',
          fromNodeId: 'specimen',
          toNodeId: 'patient',
          label: 'participant_Patient',
        },
      ],
    };
    const { rerender } = render(
      <RouteExtensionPanel
        catalog={ambiguousCatalog}
        table={table}
        selectedOccurrenceId="base"
        inspectedNodeId="patient"
        disabled={false}
        onSelectEdge={onSelectEdge}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={vi.fn()}
        onAddEdge={onAddEdge}
      />,
    );

    fireEvent.change(
      screen.getByRole('combobox', { name: 'Relationship to add' }),
      {
        target: { value: 'specimen-patient' },
      },
    );
    expect(onSelectEdge).toHaveBeenCalledWith('specimen-patient');

    rerender(
      <RouteExtensionPanel
        catalog={ambiguousCatalog}
        table={table}
        selectedOccurrenceId="base"
        inspectedNodeId="patient"
        selectedEdgeId="specimen-patient"
        disabled={false}
        onSelectEdge={onSelectEdge}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={vi.fn()}
        onAddEdge={onAddEdge}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add branch' }));
    expect(onAddEdge).toHaveBeenCalledWith('specimen-patient', 'patient');
  });

  it('adds another occurrence when the inspected resource is already in the route', () => {
    const onSelectEdge = vi.fn();
    const onAddEdge = vi.fn();
    const multiRelationshipCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      edges: [
        ...catalog.edges,
        {
          edgeId: 'specimen-patient-secondary',
          fromNodeId: 'specimen',
          toNodeId: 'patient',
          label: 'participant_Patient',
        },
      ],
    };
    const tableWithPatient: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          ...table.document.route,
          children: [
            ...(table.document.route.children ?? []),
            {
              occurrenceId: 'patient-subject',
              resourceType: 'Patient',
              relationship: 'subject_Patient',
            },
          ],
        },
      },
    };
    render(
      <RouteExtensionPanel
        catalog={multiRelationshipCatalog}
        table={tableWithPatient}
        selectedOccurrenceId="base"
        inspectedNodeId="patient"
        allowExistingExtension
        disabled={false}
        onSelectEdge={onSelectEdge}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={vi.fn()}
        onAddEdge={onAddEdge}
      />,
    );

    expect(
      screen.getByText('Add another Patient under Specimen'),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('combobox', { name: 'Relationship to add' }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add traversal' }));

    expect(onAddEdge).toHaveBeenCalledWith(
      'specimen-patient-secondary',
      'patient',
    );
  });

  it('does not reopen extension controls after an ordinary route addition', () => {
    const tableWithPatient: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          ...table.document.route,
          children: [
            ...(table.document.route.children ?? []),
            {
              occurrenceId: 'patient-subject',
              resourceType: 'Patient',
              relationship: 'subject_Patient',
            },
          ],
        },
      },
    };
    const { container } = render(
      <RouteExtensionPanel
        catalog={catalog}
        table={tableWithPatient}
        selectedOccurrenceId="base"
        inspectedNodeId="patient"
        disabled={false}
        onSelectEdge={vi.fn()}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={vi.fn()}
        onAddEdge={vi.fn()}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });

  it('offers a clear destructive restart for an eligible disconnected node', () => {
    const disconnectedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        {
          nodeId: 'substance',
          resourceType: 'Substance',
          rowRootEligible: true,
          populated: true,
          documentCount: 1,
        },
      ],
    };
    const onChangeRowStart = vi.fn();
    render(
      <RouteExtensionPanel
        catalog={disconnectedCatalog}
        table={table}
        selectedOccurrenceId="base"
        inspectedNodeId="substance"
        disabled={false}
        onSelectEdge={vi.fn()}
        onUseAsRowStart={vi.fn()}
        onChangeRowStart={onChangeRowStart}
        onAddEdge={vi.fn()}
      />,
    );

    expect(screen.queryByText(/not reachable/i)).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole('button', {
        name: 'Start new query from Substance',
      }),
    );
    expect(onChangeRowStart).toHaveBeenCalledWith('substance');
  });
});
