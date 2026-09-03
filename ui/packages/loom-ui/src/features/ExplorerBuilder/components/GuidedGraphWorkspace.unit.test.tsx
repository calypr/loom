// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { graphEdgeOverviewLabels, GuidedGraphWorkspace } from './GuidedGraphWorkspace';

const mockFitView = vi.fn();
const mockLayoutDatasetGraph = vi.fn();

vi.mock('@xyflow/react', () => {
  const ReactFlow = ({
    nodes,
    edges,
    children,
    onNodeClick,
    onPaneClick,
  }: {
    readonly nodes: ReadonlyArray<{
      readonly id: string;
      readonly data: { readonly label: React.ReactNode };
      readonly style?: { readonly border?: string };
    }>;
    readonly edges: ReadonlyArray<{
      readonly id: string;
      readonly source: string;
      readonly target: string;
      readonly markerEnd?: { readonly type?: string };
    }>;
    readonly children?: React.ReactNode;
    readonly onNodeClick?: (
      event: unknown,
      node: { readonly id: string },
    ) => void;
    readonly onPaneClick?: (event: React.MouseEvent<HTMLDivElement>) => void;
  }) => (
    <div
      data-testid="mock-react-flow"
      data-node-count={nodes.length}
      data-edge-count={edges.length}
      onClick={(event) => onPaneClick?.(event)}
    >
      {nodes.map((node) => (
        <button
          type="button"
          key={node.id}
          aria-label={`Graph node ${node.id}`}
          data-testid={`graph-node-${node.id}`}
          data-border={node.style?.border}
          onClick={(event) => onNodeClick?.(event, node)}
        >
          {node.data.label}
        </button>
      ))}
      {edges.map((edge) => (
        <div
          key={edge.id}
          data-testid={`graph-edge-${edge.id}`}
          data-source={edge.source}
          data-target={edge.target}
          data-marker-type={edge.markerEnd?.type}
        />
      ))}
      <button type="button">Graph background</button>
      {children}
    </div>
  );
  return {
    Background: () => null,
    Controls: () => null,
    MarkerType: { ArrowClosed: 'arrowclosed' },
    ReactFlow,
    useNodesInitialized: () => true,
    useReactFlow: () => ({
      fitView: mockFitView,
      viewportInitialized: true,
    }),
  };
});

vi.mock('../graphLayout', () => ({
  layoutDatasetGraph: (...args: unknown[]) => mockLayoutDatasetGraph(...args),
}));

vi.mock('./RouteExtensionPanel', () => ({
  RouteExtensionPanel: () => null,
}));

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: { allowRepeatedEdges: true, allowSelfLoops: true },
  nodes: [
    {
      nodeId: 'node-patient',
      resourceType: 'Patient',
      rowRootEligible: true,
      populated: true,
      documentCount: 3,
    },
    {
      nodeId: 'node-document',
      resourceType: 'DocumentReference',
      rowRootEligible: true,
      populated: true,
      documentCount: 2,
    },
    {
      nodeId: 'node-file',
      resourceType: 'File',
      rowRootEligible: false,
      populated: true,
      documentCount: 1,
    },
    {
      nodeId: 'node-orphan',
      resourceType: 'Condition',
      rowRootEligible: false,
      populated: false,
      documentCount: 0,
    },
  ],
  edges: [
    {
      edgeId: 'patient-document',
      fromNodeId: 'node-patient',
      toNodeId: 'node-document',
      label: 'has document',
    },
    {
      edgeId: 'document-file',
      fromNodeId: 'node-document',
      toNodeId: 'node-file',
      label: 'references file',
    },
  ],
  candidates: [],
};

const table = (rootNodeId: string): DraftTable => ({
  outputId: 'documents',
  tabId: 'documents',
  title: 'Documents',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'documents', title: 'Documents' },
    rootResourceType:
      catalog.nodes.find((node) => node.nodeId === rootNodeId)?.resourceType ??
      rootNodeId,
    route: {
      occurrenceId: 'base',
      resourceType:
        catalog.nodes.find((node) => node.nodeId === rootNodeId)
          ?.resourceType ?? rootNodeId,
    },
    columns: [],
  },
});

const renderGraph = (currentTable: DraftTable) =>
  render(
    <GuidedGraphWorkspace
      catalog={catalog}
      table={currentTable}
      selectedOccurrenceId="base"
      disabled={false}
      onSelectOccurrence={vi.fn()}
      onSetBase={vi.fn()}
      onChangeBase={vi.fn()}
      onAppendEdge={vi.fn()}
      onChangeEdge={vi.fn()}
      onTruncate={vi.fn()}
    />,
  );

describe('GuidedGraphWorkspace', () => {
  it('summarizes multiple relationships between the same resource pair', () => {
    const labels = graphEdgeOverviewLabels([
      { edgeId: 'patient-specimen', fromNodeId: 'patient', toNodeId: 'specimen', label: 'subject_Patient' },
      { edgeId: 'specimen-patient', fromNodeId: 'specimen', toNodeId: 'patient', label: 'subject_Patient' },
      { edgeId: 'patient-focus', fromNodeId: 'patient', toNodeId: 'specimen', label: 'focus_Patient' },
      { edgeId: 'specimen-observation', fromNodeId: 'specimen', toNodeId: 'observation', label: 'focus_Specimen' },
    ]);

    expect([...labels]).toEqual([
      ['patient-specimen', '2 relationships'],
      ['specimen-observation', 'focus_Specimen'],
    ]);
  });

  beforeEach(() => {
    mockFitView.mockReset();
    mockLayoutDatasetGraph.mockReset();
    mockLayoutDatasetGraph.mockImplementation(async (nodes) => ({
      positions: new Map(
        nodes.map((node: { id: string }, index: number) => [
          node.id,
          { x: index * 220, y: 0 },
        ]),
      ),
      routes: new Map(),
    }));
  });

  it('shows the full graph and optionally includes orphans', async () => {
    renderGraph(table('node-document'));

    await waitFor(() =>
      expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
        'data-node-count',
        '3',
      ),
    );
    expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
      'data-edge-count',
      '2',
    );
    expect(screen.getByTestId('graph-edge-patient-document')).toHaveAttribute(
      'data-source',
      'node-patient',
    );
    expect(screen.getByTestId('graph-edge-patient-document')).toHaveAttribute(
      'data-target',
      'node-document',
    );
    expect(screen.getByTestId('graph-edge-patient-document')).toHaveAttribute(
      'data-marker-type',
      'arrowclosed',
    );
    expect(screen.getByTestId('graph-node-node-document')).toBeInTheDocument();
    expect(screen.getByTestId('graph-node-node-patient')).toBeInTheDocument();
    expect(
      screen.queryByTestId('graph-node-node-orphan'),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(mockFitView).toHaveBeenCalled());

    fireEvent.click(screen.getByRole('checkbox', { name: 'Show orphans' }));

    expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
      'data-node-count',
      '4',
    );
    expect(screen.getByTestId('graph-node-node-orphan')).toBeInTheDocument();

    expect(
      screen.queryByRole('button', { name: 'Focus route' }),
    ).not.toBeInTheDocument();
  });

  it('keeps the graph populated when a stale route id does not match catalog nodes', async () => {
    renderGraph(table('DocumentReference'));

    await waitFor(() =>
      expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
        'data-node-count',
        '3',
      ),
    );
    expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
      'data-edge-count',
      '2',
    );
  });

  it('starts a blank table directly from any eligible graph resource without expanding', async () => {
    const onSetBase = vi.fn();
    render(
      <GuidedGraphWorkspace
        catalog={catalog}
        table={table('')}
        selectedOccurrenceId="base"
        disabled={false}
        onSelectOccurrence={vi.fn()}
        onSetBase={onSetBase}
        onChangeBase={vi.fn()}
        onAppendEdge={vi.fn()}
        onChangeEdge={vi.fn()}
        onTruncate={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('graph-node-node-patient')).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByTestId('graph-node-node-patient'));

    expect(onSetBase).toHaveBeenCalledWith('node-patient');
    expect(screen.getByTestId('graph-node-node-patient')).toHaveAttribute(
      'data-border',
      '3px solid #7c3aed',
    );
    expect(
      screen.queryByRole('dialog', { name: 'Expanded dataset graph' }),
    ).not.toBeInTheDocument();
  });

  it('does not revive an inspected node after switching tables', async () => {
    const props = {
      catalog,
      selectedOccurrenceId: 'base',
      disabled: false,
      onSelectOccurrence: vi.fn(),
      onSetBase: vi.fn(),
      onChangeBase: vi.fn(),
      onAppendEdge: vi.fn(),
      onChangeEdge: vi.fn(),
      onTruncate: vi.fn(),
    };
    const view = render(
      <GuidedGraphWorkspace {...props} table={table('node-document')} />,
    );
    await waitFor(() => expect(mockFitView).toHaveBeenCalled());

    fireEvent.click(screen.getByRole('checkbox', { name: 'Show orphans' }));
    fireEvent.click(screen.getByTestId('graph-node-node-orphan'));
    expect(screen.getByTestId('graph-node-node-orphan')).toHaveAttribute(
      'data-border',
      '3px solid #f59e0b',
    );

    const otherTable = {
      ...table('node-document'),
      outputId: 'other-table',
    };
    view.rerender(<GuidedGraphWorkspace {...props} table={otherTable} />);
    view.rerender(<GuidedGraphWorkspace {...props} table={otherTable} />);
    await waitFor(() => expect(mockFitView).toHaveBeenCalled());

    expect(screen.getByTestId('graph-node-node-orphan')).toHaveAttribute(
      'data-border',
      '1px solid #94a3b8',
    );
  });

  it('renders and independently selects every sibling in a configured route tree', async () => {
    const siblingCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        {
          nodeId: 'node-specimen',
          resourceType: 'Specimen',
          rowRootEligible: true,
          populated: true,
          documentCount: 2,
        },
        {
          nodeId: 'node-observation',
          resourceType: 'Observation',
          rowRootEligible: true,
          populated: true,
          documentCount: 2,
        },
        {
          nodeId: 'node-patient',
          resourceType: 'Patient',
          rowRootEligible: true,
          populated: true,
          documentCount: 2,
        },
      ],
      edges: [
        {
          edgeId: 'specimen-observation',
          fromNodeId: 'node-specimen',
          toNodeId: 'node-observation',
          label: 'focus_Specimen',
        },
        {
          edgeId: 'specimen-patient',
          fromNodeId: 'node-specimen',
          toNodeId: 'node-patient',
          label: 'subject_Patient',
        },
        {
          edgeId: 'specimen-patient-participant',
          fromNodeId: 'node-specimen',
          toNodeId: 'node-patient',
          label: 'participant_Patient',
        },
      ],
    };
    const siblingTable: DraftTable = {
      outputId: 'Specimen',
      tabId: 'Specimen',
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
            {
              occurrenceId: 'patient',
              resourceType: 'Patient',
              relationship: 'subject_Patient',
            },
          ],
        },
        columns: [],
      },
    };
    const onSelectOccurrence = vi.fn();
    const onChangeEdge = vi.fn();

    render(
      <GuidedGraphWorkspace
        catalog={siblingCatalog}
        table={siblingTable}
        selectedOccurrenceId="base"
        disabled={false}
        onSelectOccurrence={onSelectOccurrence}
        onSetBase={vi.fn()}
        onChangeBase={vi.fn()}
        onAppendEdge={vi.fn()}
        onChangeEdge={onChangeEdge}
        onTruncate={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
        'data-node-count',
        '3',
      ),
    );
    expect(
      screen.getByRole('button', { name: 'Observation' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Patient' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Patient' }));
    expect(onSelectOccurrence).toHaveBeenCalledWith('patient');
    fireEvent.change(
      screen.getByRole('combobox', {
        name: 'Relationship for Patient occurrence',
      }),
      { target: { value: 'specimen-patient-participant' } },
    );
    expect(onChangeEdge).toHaveBeenCalledWith(
      'patient',
      'specimen-patient-participant',
    );
  });

  it('inspects an existing resource when another relationship can add a new occurrence', async () => {
    const multiRelationshipCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        {
          nodeId: 'node-specimen',
          resourceType: 'Specimen',
          rowRootEligible: true,
          populated: true,
          documentCount: 2,
        },
        {
          nodeId: 'node-patient',
          resourceType: 'Patient',
          rowRootEligible: true,
          populated: true,
          documentCount: 2,
        },
      ],
      edges: [
        {
          edgeId: 'specimen-patient-subject',
          fromNodeId: 'node-specimen',
          toNodeId: 'node-patient',
          label: 'subject_Patient',
        },
        {
          edgeId: 'specimen-patient-participant',
          fromNodeId: 'node-specimen',
          toNodeId: 'node-patient',
          label: 'participant_Patient',
        },
      ],
    };
    const tableWithPatient: DraftTable = {
      outputId: 'Specimen',
      tabId: 'Specimen',
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
              occurrenceId: 'patient-subject',
              resourceType: 'Patient',
              relationship: 'subject_Patient',
            },
          ],
        },
        columns: [],
      },
    };
    const onSelectOccurrence = vi.fn();
    const onAppendEdge = vi.fn();

    render(
      <GuidedGraphWorkspace
        catalog={multiRelationshipCatalog}
        table={tableWithPatient}
        selectedOccurrenceId="base"
        disabled={false}
        onSelectOccurrence={onSelectOccurrence}
        onSetBase={vi.fn()}
        onChangeBase={vi.fn()}
        onAppendEdge={onAppendEdge}
        onChangeEdge={vi.fn()}
        onTruncate={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('graph-node-node-patient')).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByTestId('graph-node-node-patient'));

    expect(onSelectOccurrence).not.toHaveBeenCalled();
    expect(onAppendEdge).not.toHaveBeenCalled();
    expect(screen.getByTestId('graph-node-node-patient')).toHaveAttribute(
      'data-border',
      '3px solid #f59e0b',
    );
  });

  it('keeps node interaction inline, expands from the background, and directly adds one legal edge', async () => {
    const onAppendEdge = vi.fn();
    const onTableToolbarHostChange = vi.fn();
    render(
      <GuidedGraphWorkspace
        catalog={catalog}
        table={table('node-document')}
        selectedOccurrenceId="base"
        disabled={false}
        onSelectOccurrence={vi.fn()}
        onSetBase={vi.fn()}
        onChangeBase={vi.fn()}
        onAppendEdge={onAppendEdge}
        onChangeEdge={vi.fn()}
        onTruncate={vi.fn()}
        onTableToolbarHostChange={onTableToolbarHostChange}
      />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('graph-node-node-file')).toBeInTheDocument(),
    );
    expect(
      screen.queryByRole('button', { name: 'Expand graph' }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('graph-node-node-file'));

    expect(onAppendEdge).toHaveBeenCalledWith(
      'base',
      'document-file',
      'node-file',
    );
    expect(
      screen.queryByRole('dialog', { name: 'Expanded dataset graph' }),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('graph-node-node-file')).toHaveAttribute(
      'data-border',
      '3px solid #f59e0b',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Graph background' }));

    const expandedGraph = screen.getByRole('dialog', {
      name: 'Expanded dataset graph',
    });
    expect(expandedGraph).toBeInTheDocument();
    expect(expandedGraph).toHaveClass('fixed', 'inset-3');
    expect(expandedGraph).not.toHaveClass('relative');

    fireEvent.click(screen.getByRole('button', { name: 'Close graph' }));

    expect(
      screen.queryByRole('dialog', { name: 'Expanded dataset graph' }),
    ).not.toBeInTheDocument();
    expect(
      onTableToolbarHostChange.mock.calls.some(([host]) => host === null),
    ).toBe(true);
    const currentHost = onTableToolbarHostChange.mock.calls.at(-1)?.[0];
    expect(currentHost).toBeInstanceOf(HTMLDivElement);
    expect(currentHost?.isConnected).toBe(true);
  });
});
