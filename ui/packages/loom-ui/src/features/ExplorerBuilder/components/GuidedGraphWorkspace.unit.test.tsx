// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { legalOutgoingEdges } from '../authoring/routeActions';
import { graphEdgeOverviewLabels, GuidedGraphWorkspace, layoutDiscoveryRail, layoutTraversalTree } from './GuidedGraphWorkspace';

const mockFitView = vi.fn();
const mockFitBounds = vi.fn();
const mockFlow = { fitView: mockFitView, fitBounds: mockFitBounds, viewportInitialized: true };
const mockLayoutDatasetGraph = vi.fn();

beforeEach(() => {
  mockFitView.mockReset();
  mockFitBounds.mockReset();
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

vi.mock('@xyflow/react', () => {
  const ReactFlow = ({
    nodes,
    edges,
    children,
    onNodeClick,
    onEdgeClick,
    onPaneClick,
    onKeyDown,
  }: {
    readonly nodes: ReadonlyArray<{
      readonly id: string;
      readonly data: { readonly label: React.ReactNode };
      readonly style?: { readonly border?: string };
      readonly position: { readonly x: number; readonly y: number };
    }>;
    readonly edges: ReadonlyArray<{
      readonly id: string;
      readonly source: string;
      readonly target: string;
      readonly markerEnd?: { readonly type?: string };
      readonly data?: { readonly authoredOccurrenceId?: string };
    }>;
    readonly children?: React.ReactNode;
    readonly onNodeClick?: (
      event: unknown,
      node: { readonly id: string },
    ) => void;
    readonly onEdgeClick?: (
      event: React.MouseEvent<HTMLDivElement>,
      edge: { readonly id: string; readonly data?: { readonly authoredOccurrenceId?: string } },
    ) => void;
    readonly onKeyDown?: (event: React.KeyboardEvent<HTMLDivElement>) => void;
    readonly onPaneClick?: (event: React.MouseEvent<HTMLDivElement>) => void;
  }) => (
    <div
      data-testid="mock-react-flow"
      data-node-count={nodes.length}
      data-edge-count={edges.length}
      onClick={(event) => onPaneClick?.(event)}
      onKeyDown={(event) => onKeyDown?.(event)}
    >
      {nodes.map((node) => (
        <div
          key={node.id}
          className="react-flow__node"
          aria-label={`Graph node ${node.id}`}
          data-id={node.id}
          data-position-x={node.position.x}
          data-position-y={node.position.y}
          data-testid={`graph-node-${node.id}`}
          data-border={node.style?.border}
          onClick={(event) => { event.stopPropagation(); onNodeClick?.(event, node); }}
        >
          {node.data.label}
        </div>
      ))}
      {edges.map((edge) => (
        <div
          key={edge.id}
          data-testid={`graph-edge-${edge.id}`}
          data-source={edge.source}
          data-target={edge.target}
          data-marker-type={edge.markerEnd?.type}
          data-authored-occurrence-id={typeof edge.data?.authoredOccurrenceId === 'string' ? edge.data.authoredOccurrenceId : undefined}
          onClick={(event) => onEdgeClick?.(event, edge)}
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
    Position: { Left: 'left', Right: 'right' },
    ReactFlow,
    useNodesInitialized: () => true,
    useReactFlow: () => mockFlow,
    ViewportPortal: ({ children }: { readonly children?: React.ReactNode }) => <div data-testid="mock-viewport-portal">{children}</div>,
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
    rows: { kind: 'RECORDS', records: {} },
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
      onChangeMatchMode={vi.fn()}
      onTruncate={vi.fn()}
    />,
  );

describe('GuidedGraphWorkspace', () => {
  it('shows a left-to-right occurrence tree with siblings parallel and no duplicate Current query bar', () => {
    const branched = table('node-patient');
    branched.document.route.occurrenceId = 'patient-root';
    branched.document.route.children = [
      { occurrenceId: 'document-branch', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has document', matchMode: 'OPTIONAL' },
      { occurrenceId: 'document-branch-2', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has document', matchMode: 'REQUIRED' },
    ];
    const onSelectOccurrence = vi.fn();
    const mutations = [vi.fn(), vi.fn(), vi.fn(), vi.fn(), vi.fn()];
    render(<GuidedGraphWorkspace catalog={catalog} table={branched} selectedOccurrenceId="patient-root" disabled={false}
      onSelectOccurrence={onSelectOccurrence} onSetBase={mutations[0]!} onChangeBase={mutations[1]!} onAppendEdge={mutations[2]!} onChangeEdge={mutations[3]!} onChangeMatchMode={mutations[4]!} onTruncate={vi.fn()} presentation="traversal" />);
    expect(screen.queryByRole('heading', { name: /Related records|Dataset graph/ })).toBeNull();
    expect(screen.queryByRole('navigation', { name: 'Current traversal' })).toBeNull();
    expect(screen.queryByText('Current query')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Expand graph' })).toBeNull();
    expect(screen.getByTestId('graph-viewport')).toBeInTheDocument();
    const repeated = [
      screen.getByTestId('graph-node-occurrence:document-branch'),
      screen.getByTestId('graph-node-occurrence:document-branch-2'),
    ];
    expect(repeated[0]).toHaveTextContent('DocumentReference');
    expect(repeated[1]).toHaveTextContent('DocumentReference');
    expect(repeated[1]).toHaveAttribute('data-id', 'occurrence:document-branch-2');
    expect(Number(repeated[0]!.getAttribute('data-position-x'))).toBeGreaterThan(Number(screen.getByTestId('graph-node-occurrence:patient-root').getAttribute('data-position-x')));
    expect(Number(repeated[0]!.getAttribute('data-position-y'))).not.toBe(Number(repeated[1]!.getAttribute('data-position-y')));
    fireEvent.click(within(repeated[1]!).getByRole('button'));
    expect(onSelectOccurrence).toHaveBeenCalledWith('document-branch-2');
    expect(mutations.every((callback) => callback.mock.calls.length === 0)).toBe(true);
  });

  it('keeps uneven sibling subtrees in disjoint bands and connects each saved occurrence to its exact parent', () => {
    const items: Array<{ id: string; parentId?: string; depth: number }> = [
      { id: 'root', depth: 0 },
      { id: 'wide', parentId: 'root', depth: 1 },
      { id: 'wide-a', parentId: 'wide', depth: 2 },
      { id: 'wide-b', parentId: 'wide', depth: 2 },
      { id: 'short', parentId: 'root', depth: 1 },
      { id: 'short-a', parentId: 'short', depth: 2 },
    ];
    const positions = layoutTraversalTree(items, 440, 168, 112);
    const boxes = items.map((item) => ({ ...item, ...positions.get(item.id)!, right: positions.get(item.id)!.x + 250, bottom: positions.get(item.id)!.y + 112 }));
    for (let left = 0; left < boxes.length; left += 1) for (let right = left + 1; right < boxes.length; right += 1) {
      const a = boxes[left]!; const b = boxes[right]!;
      const overlaps = a.x < b.right && b.x < a.right && a.y < b.bottom && b.y < a.bottom;
      expect(overlaps, `${a.id} overlaps ${b.id}`).toBe(false);
    }
    for (const item of items.filter((candidate) => candidate.parentId)) {
      const parent = boxes.find((candidate) => candidate.id === item.parentId)!;
      const child = boxes.find((candidate) => candidate.id === item.id)!;
      expect(parent.right).toBeLessThan(child.x);
    }

    const nestedCatalog: ExplorerBuilderCatalog = {
      ...catalog, routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
      nodes: [
        { nodeId: 'specimen', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'document', resourceType: 'DocumentReference', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'condition', resourceType: 'Condition', rowRootEligible: true, populated: true, documentCount: 2 },
        { nodeId: 'file', resourceType: 'File', rowRootEligible: false, populated: true, documentCount: 2 },
      ],
      edges: [
        { edgeId: 'specimen-patient', fromNodeId: 'specimen', toNodeId: 'patient', label: 'subject_Patient' },
        { edgeId: 'specimen-observation', fromNodeId: 'specimen', toNodeId: 'observation', label: 'specimen_Observation' },
        { edgeId: 'patient-document', fromNodeId: 'patient', toNodeId: 'document', label: 'has_document' },
        { edgeId: 'patient-condition', fromNodeId: 'patient', toNodeId: 'condition', label: 'has_condition' },
        { edgeId: 'observation-file', fromNodeId: 'observation', toNodeId: 'file', label: 'derived_file' },
      ],
    };
    const nested: DraftTable = { outputId: 'nested', tabId: 'nested', title: 'Nested', document: {
      kind: 'ExplorerBuilderDocument', output: { id: 'nested', title: 'Nested' }, rootResourceType: 'Specimen',
      route: { occurrenceId: 'root', resourceType: 'Specimen', children: [
        { occurrenceId: 'patient-occ', resourceType: 'Patient', catalogEdgeId: 'specimen-patient', relationship: 'subject_Patient', children: [
          { occurrenceId: 'document-occ', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has_document' },
          { occurrenceId: 'condition-occ', resourceType: 'Condition', catalogEdgeId: 'patient-condition', relationship: 'has_condition' },
        ] },
        { occurrenceId: 'observation-occ', resourceType: 'Observation', catalogEdgeId: 'specimen-observation', relationship: 'specimen_Observation', children: [
          { occurrenceId: 'file-occ', resourceType: 'File', catalogEdgeId: 'observation-file', relationship: 'derived_file' },
        ] },
      ] }, rows: { kind: 'RECORDS', records: {} }, columns: [],
    } };
    render(<GuidedGraphWorkspace catalog={nestedCatalog} table={nested} selectedOccurrenceId="root" disabled={false}
      onSelectOccurrence={vi.fn()} onSetBase={vi.fn()} onChangeBase={vi.fn()} onAppendEdge={vi.fn()} onChangeEdge={vi.fn()} onChangeMatchMode={vi.fn()} onTruncate={vi.fn()} presentation="traversal" />);
    for (const [child, parent] of [['patient-occ', 'root'], ['observation-occ', 'root'], ['document-occ', 'patient-occ'], ['condition-occ', 'patient-occ'], ['file-occ', 'observation-occ']]) {
      const edge = screen.getByTestId(`graph-edge-occurrence-edge:${child}`);
      expect(edge).toHaveAttribute('data-source', `occurrence:${parent}`);
      expect(edge).toHaveAttribute('data-target', `occurrence:${child}`);
    }
  });

  it('detours the discovery trunk around lower peer-subtree edges without crossing node boxes', () => {
    const items: Array<{ occurrenceId: string; parentId?: string; depth: number }> = [
      { occurrenceId: 'root', depth: 0 },
      { occurrenceId: 'patient-one', parentId: 'root', depth: 1 },
      { occurrenceId: 'observation', parentId: 'patient-one', depth: 2 },
      { occurrenceId: 'condition', parentId: 'patient-one', depth: 2 },
      { occurrenceId: 'patient-two', parentId: 'root', depth: 1 },
      { occurrenceId: 'unknown-record', parentId: 'root', depth: 1 },
      { occurrenceId: 'unknown-child', parentId: 'unknown-record', depth: 2 },
    ];
    const treePositions = layoutTraversalTree(items.map((item) => ({ id: item.occurrenceId, parentId: item.parentId, depth: item.depth })), 440, 168, 112);
    const positions = new Map([...treePositions].map(([id, position]) => [id.replace(/^occurrence:/, ''), position]));
    const busY = 700;
    const path = layoutDiscoveryRail(items, positions, 'patient-one', busY, 250, 112, 16, 12);
    expect(path[0]).toEqual({ x: positions.get('patient-one')!.x + 250, y: positions.get('patient-one')!.y + 84 });
    expect(path.at(-1)).toEqual({ x: positions.get('patient-one')!.x + 266, y: busY });
    expect(path).toHaveLength(7);

    const boxes = items.map((item) => ({ id: item.occurrenceId, ...positions.get(item.occurrenceId)!, right: positions.get(item.occurrenceId)!.x + 250, bottom: positions.get(item.occurrenceId)!.y + 112 }));
    for (const [index, point] of path.entries()) {
      if (index === 0) continue;
      const previous = path[index - 1]!;
      for (const box of boxes) {
        if (box.id === 'patient-one') continue;
        const crossesHorizontal = previous.y === point.y && previous.y > box.y && previous.y < box.bottom && Math.max(previous.x, point.x) > box.x && Math.min(previous.x, point.x) < box.right;
        const crossesVertical = previous.x === point.x && previous.x > box.x && previous.x < box.right && Math.max(previous.y, point.y) > box.y && Math.min(previous.y, point.y) < box.bottom;
        expect(crossesHorizontal || crossesVertical, `discovery segment crosses ${box.id}`).toBe(false);
      }
    }
    const unknownRecord = boxes.find((box) => box.id === 'unknown-record')!;
    const unknownChild = boxes.find((box) => box.id === 'unknown-child')!;
    const savedEdge = { x1: unknownRecord.right, x2: unknownChild.x, y: unknownRecord.y + 56 };
    for (let index = 1; index < path.length; index += 1) {
      const previous = path[index - 1]!; const point = path[index]!;
      const crossesUnknownSavedEdge = previous.y === point.y && previous.y === savedEdge.y && Math.max(previous.x, point.x) > savedEdge.x1 && Math.min(previous.x, point.x) < savedEdge.x2;
      expect(crossesUnknownSavedEdge, 'discovery route crosses the lower unknown-record branch').toBe(false);
    }
  });

  it('keeps imported mutation callbacks isolated from ordinary Route commands', async () => {
    const importedTable = table('node-document');
    importedTable.document.importedRecipe = { name: 'default', translationVersion: '1', digest: 'pin', output: 'documents' };
    importedTable.document.route.children = [{ occurrenceId: 'file', resourceType: 'File', catalogEdgeId: 'document-file', relationship: 'references file', matchMode: 'OPTIONAL' }];
    const ordinary = { onAppendEdge: vi.fn(), onChangeMatchMode: vi.fn(), onTruncate: vi.fn() };
    const native = { onAdd: vi.fn(), onChangeMatchMode: vi.fn(), onRemove: vi.fn() };
    const props = { catalog, table: importedTable, selectedOccurrenceId: 'file', disabled: false,
      onSelectOccurrence: vi.fn(), onSetBase: vi.fn(), onChangeBase: vi.fn(), onChangeEdge: vi.fn(), ...ordinary };
    const view = render(<GuidedGraphWorkspace {...props} presentation="compact" rowMeaning="One row per document." />);
    const requireMatch = screen.getByRole('button', { name: 'Require File match' });
    expect(requireMatch).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove File branch' })).toBeDisabled();
    view.rerender(<GuidedGraphWorkspace {...props} presentation="compact" rowMeaning="One row per document." importedRelationshipActions={native} />);
    fireEvent.click(screen.getByRole('button', { name: 'Require File match' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove File branch' }));
    expect(native.onChangeMatchMode).toHaveBeenCalledWith('file', 'REQUIRED');
    expect(native.onRemove).toHaveBeenCalledWith('file');
    expect(ordinary.onChangeMatchMode).not.toHaveBeenCalled();
    expect(ordinary.onTruncate).not.toHaveBeenCalled();
    expect(ordinary.onAppendEdge).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Selected relationship inspector')).toHaveTextContent('Multiplicity: unknown');
    expect(screen.getByText('One row per document.')).toBeInTheDocument();
    view.rerender(<GuidedGraphWorkspace {...props} presentation="compact" showSelectionDetails={false} rowMeaning="One row per document." rowControl={<button>Duplicate row settings</button>} importedRelationshipActions={native} />);
    expect(screen.queryByLabelText('Selected relationship inspector')).toBeNull();
    expect(screen.queryByText('One row per document.')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Duplicate row settings' })).toBeNull();
    expect(screen.getByRole('navigation', { name: 'Current traversal' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Require File match' }));
    expect(native.onChangeMatchMode).toHaveBeenLastCalledWith('file', 'REQUIRED');

  });

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
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show orphans' }));
    expect(screen.getByTestId('mock-react-flow')).toHaveAttribute(
      'data-node-count',
      '3',
    );
    expect(
      screen.queryByTestId('graph-node-node-orphan'),
    ).not.toBeInTheDocument();

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
        onChangeMatchMode={vi.fn()}
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
      onChangeMatchMode: vi.fn(),
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
        rows: { kind: 'RECORDS', records: {} },
        columns: [],
      },
    };
    const onSelectOccurrence = vi.fn();
    const onChangeEdge = vi.fn();
    const onChangeMatchMode = vi.fn();
    const onChangeBase = vi.fn();

    render(
      <GuidedGraphWorkspace
        catalog={siblingCatalog}
        table={siblingTable}
        selectedOccurrenceId="base"
        disabled={false}
        onSelectOccurrence={onSelectOccurrence}
        onSetBase={vi.fn()}
        onChangeBase={onChangeBase}
        onAppendEdge={vi.fn()}
        onChangeEdge={onChangeEdge}
        onChangeMatchMode={onChangeMatchMode}
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
    expect(screen.getByRole('button', { name: 'Patient' })).toHaveAttribute(
      'data-occurrence-id',
      'patient',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Patient' }));
    expect(onSelectOccurrence).toHaveBeenCalledWith('patient');
    fireEvent.click(
      screen.getByRole('button', { name: 'Make each Patient one row' }),
    );
    expect(onChangeBase).toHaveBeenCalledWith('node-patient');
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
    fireEvent.click(
      screen.getByRole('button', { name: 'Require Patient match' }),
    );
    expect(onChangeMatchMode).toHaveBeenCalledWith('patient', 'REQUIRED');
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
        rows: { kind: 'RECORDS', records: {} },
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
        onChangeMatchMode={vi.fn()}
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
        onChangeMatchMode={vi.fn()}
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
  it.each([false, true])('keeps imported traversal immutable while allowing inspection (imported=%s)', async (imported) => {
    const current = table('node-patient');
    const branched: DraftTable = {
      ...current,
      document: {
        ...current.document,
        ...(imported ? { importedRecipe: {
          name: 'default', translationVersion: 'v1', digest: 'a'.repeat(64), output: 'documents',
        } } : {}),
        route: {
          ...current.document.route,
          children: [{ occurrenceId: 'documents', resourceType: 'DocumentReference',
            relationship: 'has document', catalogEdgeId: 'patient-document', matchMode: 'OPTIONAL' }],
        },
      },
    };
    const onSelectOccurrence = vi.fn();
    const callbacks = {
      onSetBase: vi.fn(), onChangeBase: vi.fn(), onAppendEdge: vi.fn(),
      onChangeEdge: vi.fn(), onChangeMatchMode: vi.fn(), onTruncate: vi.fn(),
    };
    render(<GuidedGraphWorkspace catalog={{ ...catalog, edges: [...catalog.edges, {
      edgeId: 'patient-document-other', fromNodeId: 'node-patient', toNodeId: 'node-document', label: 'other document',
    }] }} table={branched} selectedOccurrenceId="base" disabled={false}
      onSelectOccurrence={onSelectOccurrence} {...callbacks} />);
    await waitFor(() => expect(mockFitView).toHaveBeenCalled());
    const edge = screen.getByRole('combobox', { name: 'Relationship for DocumentReference occurrence' });
    const match = screen.getByRole('button', { name: 'Require DocumentReference match' });
    const root = screen.getByRole('button', { name: 'Make each DocumentReference one row' });
    const remove = screen.getByRole('button', { name: 'Remove DocumentReference branch' });
    for (const control of [edge, match, root, remove]) {
      if (imported) expect(control).toBeDisabled();
      else expect(control).toBeEnabled();
    }
    fireEvent.click(match);
    fireEvent.click(root);
    fireEvent.click(remove);
    for (const callback of [callbacks.onChangeMatchMode, callbacks.onChangeBase, callbacks.onTruncate]) {
      expect(callback).toHaveBeenCalledTimes(imported ? 0 : 1);
    }
    fireEvent.click(screen.getByTitle('DocumentReference'));
    expect(onSelectOccurrence).toHaveBeenCalledWith('documents');
  });

  it('does not append a source route when inspecting a neighboring imported node', async () => {
    const current = table('node-patient');
    const onAppendEdge = vi.fn();
    render(<GuidedGraphWorkspace catalog={catalog} table={{ ...current, document: {
      ...current.document, importedRecipe: { name: 'default', translationVersion: 'v1', digest: 'a'.repeat(64), output: 'documents' },
    } }} selectedOccurrenceId="base" disabled={false}
      onSelectOccurrence={vi.fn()} onSetBase={vi.fn()} onChangeBase={vi.fn()}
      onAppendEdge={onAppendEdge} onChangeEdge={vi.fn()} onChangeMatchMode={vi.fn()} onTruncate={vi.fn()} />);
    await waitFor(() => expect(mockFitView).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('graph-node-node-document'));
    expect(onAppendEdge).not.toHaveBeenCalled();
  });

});

it('renders unresolved occurrences and descendants as selectable authored graph nodes without inferring catalog edges', () => {
  mockLayoutDatasetGraph.mockResolvedValue({ positions: new Map() });
  const current = table('node-patient');
  current.document.route.children = [{ occurrenceId: 'saved-unresolved', resourceType: 'DocumentReference', catalogEdgeId: 'removed-edge', relationship: 'old document relationship', matchMode: 'OPTIONAL', children: [
    { occurrenceId: 'unresolved-file', resourceType: 'File', catalogEdgeId: 'document-file', relationship: 'references file', matchMode: 'REQUIRED' },
  ] }];
  const onSelectOccurrence = vi.fn();
  render(<GuidedGraphWorkspace catalog={catalog} table={current} selectedOccurrenceId="base" disabled={false}
    onSelectOccurrence={onSelectOccurrence} onSetBase={vi.fn()} onChangeBase={vi.fn()} onAppendEdge={vi.fn()} onChangeEdge={vi.fn()} onChangeMatchMode={vi.fn()} onTruncate={vi.fn()} presentation="traversal" />);
  expect(screen.queryByRole('navigation', { name: 'Current traversal' })).toBeNull();
  const unresolved = screen.getByRole('button', { name: /old document relationship.*saved-unresolved/ });
  const descendant = screen.getByRole('button', { name: /references file.*unresolved-file/ });
  expect(unresolved).toHaveAttribute('data-occurrence-id', 'saved-unresolved');
  expect(descendant).toHaveAttribute('data-occurrence-id', 'unresolved-file');
  expect(screen.getByTestId('graph-node-occurrence:saved-unresolved')).toHaveTextContent('Catalog relationship unavailable');
  expect(screen.getByTestId('graph-node-occurrence:unresolved-file')).toHaveTextContent('Catalog relationship unavailable');
  expect(screen.getByTestId('graph-edge-occurrence-edge:saved-unresolved')).toHaveAttribute('data-source', 'occurrence:base');
  expect(screen.getByTestId('graph-edge-occurrence-edge:unresolved-file')).toHaveAttribute('data-source', 'occurrence:saved-unresolved');
  expect(screen.getByTestId('graph-edge-occurrence-edge:saved-unresolved')).toHaveAttribute('data-authored-occurrence-id', 'saved-unresolved');
  fireEvent.click(unresolved);
  expect(onSelectOccurrence).toHaveBeenCalledExactlyOnceWith('saved-unresolved');
  fireEvent.click(descendant);
  expect(onSelectOccurrence).toHaveBeenLastCalledWith('unresolved-file');
});

it('selects exact repeated occurrences through in-node controls without mutation', () => {
  mockLayoutDatasetGraph.mockResolvedValue({positions:new Map()});
  const value = table('node-patient');
  value.document.route.children = [{ occurrenceId: 'doc-one', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has document', matchMode: 'OPTIONAL' }, { occurrenceId: 'doc-two', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has document', matchMode: 'REQUIRED' }];
  const selected = vi.fn(); const mutate = vi.fn();
  render(<GuidedGraphWorkspace catalog={catalog} table={value} selectedOccurrenceId="base" disabled={false} onSelectOccurrence={selected} onSetBase={mutate} onChangeBase={mutate} onAppendEdge={mutate} onChangeEdge={mutate} onChangeMatchMode={mutate} onTruncate={mutate} presentation="traversal" />);
  const target = screen.getByRole('button', { name: /occurrence doc-two/ });
  expect(target).toHaveAttribute('data-occurrence-id', 'doc-two');
  fireEvent.click(target);
  expect(selected.mock.calls).toEqual([['doc-two']]);
  expect(mutate).not.toHaveBeenCalled();
});

it('matches a clicked relationship edge to its exact authored occurrence when Patient repeats', () => {
  const repeatedPatientCatalog: ExplorerBuilderCatalog = {
    ...catalog,
    nodes: [
      { nodeId: 'specimen-root', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
    ],
    edges: [
      { edgeId: 'subject-edge', fromNodeId: 'specimen-root', toNodeId: 'patient-node', label: 'subject_Patient' },
      { edgeId: 'investigator-edge', fromNodeId: 'specimen-root', toNodeId: 'patient-node', label: 'investigator_Patient' },
    ],
  };
  const repeatedPatientTable: DraftTable = {
    outputId: 'specimens', tabId: 'specimens', title: 'Specimens', document: {
      kind: 'ExplorerBuilderDocument', output: { id: 'specimens', title: 'Specimens' }, rootResourceType: 'Specimen',
      route: { occurrenceId: 'specimen-root-occ', resourceType: 'Specimen', children: [
        { occurrenceId: 'subject-occ', resourceType: 'Patient', catalogEdgeId: 'subject-edge', relationship: 'subject_Patient', matchMode: 'OPTIONAL' },
        { occurrenceId: 'investigator-occ', resourceType: 'Patient', catalogEdgeId: 'investigator-edge', relationship: 'investigator_Patient', matchMode: 'OPTIONAL' },
      ] }, rows: { kind: 'RECORDS', records: {} }, columns: [],
    },
  };
  const selected = vi.fn(); const mutate = vi.fn();
  render(<GuidedGraphWorkspace catalog={repeatedPatientCatalog} table={repeatedPatientTable} selectedOccurrenceId="specimen-root-occ" disabled={false}
    onSelectOccurrence={selected} onSetBase={mutate} onChangeBase={mutate} onAppendEdge={mutate} onChangeEdge={mutate} onChangeMatchMode={mutate} onTruncate={mutate} presentation="traversal" />);
  const subjectNode = screen.getByTestId('graph-node-occurrence:subject-occ');
  const investigatorNode = screen.getByTestId('graph-node-occurrence:investigator-occ');
  expect(within(subjectNode).getByRole('button', { name: /subject_Patient.*subject-occ/ })).toHaveAttribute('data-occurrence-id', 'subject-occ');
  expect(within(investigatorNode).getByRole('button', { name: /investigator_Patient.*investigator-occ/ })).toHaveAttribute('data-occurrence-id', 'investigator-occ');
  fireEvent.click(screen.getByTestId('graph-edge-occurrence-edge:investigator-occ'));
  expect(selected).toHaveBeenCalledExactlyOnceWith('investigator-occ');
  expect(mutate).not.toHaveBeenCalled();
});

it('allows exact proposed/read-only occurrence inspection while blocking graph mutations', () => {
  const value = table('node-patient');
  value.document.route.children = [{ occurrenceId: 'saved-occurrence', resourceType: 'DocumentReference', catalogEdgeId: 'patient-document', relationship: 'has document', matchMode: 'OPTIONAL' }];
  const selected = vi.fn();
  const mutate = vi.fn();
  render(<GuidedGraphWorkspace catalog={catalog} table={value} selectedOccurrenceId="base" disabled onSelectOccurrence={selected}
    onSetBase={mutate} onChangeBase={mutate} onAppendEdge={mutate} onChangeEdge={mutate} onChangeMatchMode={mutate} onTruncate={mutate} presentation="traversal" />);
  const occurrence = screen.getByRole('button', { name: /occurrence saved-occurrence/ });
  fireEvent.click(occurrence);
  expect(selected).toHaveBeenCalledExactlyOnceWith('saved-occurrence');
  fireEvent.click(screen.getByTestId('graph-node-occurrence:base'));
  expect(mutate).not.toHaveBeenCalled();
});

it('keeps the saved occurrence graph fixed when selection changes between scopes with different legal additions', async () => {
  mockLayoutDatasetGraph.mockResolvedValue({ positions: new Map() });
  const selectionCatalog: ExplorerBuilderCatalog = {
    ...catalog,
    routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
    nodes: [
      { nodeId: 'specimen', resourceType: 'Specimen', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'document', resourceType: 'DocumentReference', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'condition', resourceType: 'Condition', rowRootEligible: true, populated: true, documentCount: 2 },
      { nodeId: 'medication', resourceType: 'MedicationAdministration', rowRootEligible: true, populated: true, documentCount: 2 },
    ],
    edges: [
      { edgeId: 'specimen-patient', fromNodeId: 'specimen', toNodeId: 'patient', label: 'subject_Patient' },
      { edgeId: 'specimen-observation', fromNodeId: 'specimen', toNodeId: 'observation', label: 'specimen_Observation' },
      { edgeId: 'patient-document', fromNodeId: 'patient', toNodeId: 'document', label: 'has_document' },
      { edgeId: 'patient-condition', fromNodeId: 'patient', toNodeId: 'condition', label: 'has_condition' },
      { edgeId: 'patient-medication', fromNodeId: 'patient', toNodeId: 'medication', label: 'takes_medication' },
    ],
  };
  const current: DraftTable = { outputId: 'scoped', tabId: 'scoped', title: 'Scoped', document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'scoped', title: 'Scoped' }, rootResourceType: 'Specimen',
    route: { occurrenceId: 'base', resourceType: 'Specimen', children: [
      { occurrenceId: 'patient-occurrence', resourceType: 'Patient', catalogEdgeId: 'specimen-patient', relationship: 'subject_Patient' },
    ] }, rows: { kind: 'RECORDS', records: {} }, columns: [],
  } };
  expect(legalOutgoingEdges(selectionCatalog, current, 'base').map((edge) => edge.edgeId)).toEqual(['specimen-observation']);
  expect(legalOutgoingEdges(selectionCatalog, current, 'patient-occurrence').map((edge) => edge.edgeId)).toEqual(['patient-document', 'patient-condition', 'patient-medication']);
  const props = { catalog: selectionCatalog, table: current, disabled: false, onSelectOccurrence: vi.fn(), onSetBase: vi.fn(), onChangeBase: vi.fn(), onAppendEdge: vi.fn(), onChangeEdge: vi.fn(), onChangeMatchMode: vi.fn(), onTruncate: vi.fn(), presentation: 'traversal' as const };
  const view = render(<GuidedGraphWorkspace {...props} selectedOccurrenceId="base" />);
  await waitFor(() => expect(mockFitBounds).toHaveBeenCalled());
  const initialFitCount = mockFitBounds.mock.calls.length;
  const savedGraphSnapshot = () => [...screen.getByTestId('mock-react-flow').querySelectorAll('.react-flow__node')].map((node) => [
    node.getAttribute('data-id'), node.getAttribute('data-position-x'), node.getAttribute('data-position-y'),
  ]);
  const before = savedGraphSnapshot();
  expect(before).toHaveLength(2);
  const discovery = screen.getByTestId('traversal-discovery-layer');
  expect(screen.queryByRole('heading', { name: /Available from|Add a connection from/ })).toBeNull();
  expect(screen.queryByText('No additional relationships available from this occurrence.')).toBeNull();
  expect(within(discovery).getByRole('button', { name: 'Add Observation via specimen_Observation' })).toHaveAttribute('data-candidate-edge-id', 'specimen-observation');
  expect(within(screen.getByTestId('mock-react-flow')).getAllByTestId(/^graph-node-/)).toHaveLength(2);
  const observationAdd = within(discovery).getByRole('button', { name: 'Add Observation via specimen_Observation' });
  expect(observationAdd).toHaveClass('nokey');
  expect((observationAdd as HTMLElement).style.pointerEvents).toBe('auto');
  expect(within(discovery).getByTestId('available-connection-trunk').getAttribute('data-source-occurrence-id')).toBe('base');
  expect(within(discovery).getByTestId('available-connection-edge-specimen-observation').getAttribute('data-source-occurrence-id')).toBe('base');
  expect(within(discovery).getByTestId('available-connection-edge-specimen-observation').getAttribute('data-candidate-edge-id')).toBe('specimen-observation');
  view.rerender(<GuidedGraphWorkspace {...props} selectedOccurrenceId="patient-occurrence" />);
  expect(savedGraphSnapshot()).toEqual(before);
  expect(mockFitBounds).toHaveBeenCalledTimes(initialFitCount);
  const selectedDiscovery = screen.getByTestId('traversal-discovery-layer');
  expect(within(selectedDiscovery).getByRole('button', { name: 'Add DocumentReference via has_document' })).toHaveAttribute('data-candidate-edge-id', 'patient-document');
  expect(within(selectedDiscovery).getByRole('button', { name: 'Add Condition via has_condition' })).toHaveAttribute('data-candidate-edge-id', 'patient-condition');
  expect(within(selectedDiscovery).getByTestId('available-connection-trunk').getAttribute('data-source-occurrence-id')).toBe('patient-occurrence');
  act(() => { fireEvent.click(within(selectedDiscovery).getByRole('button', { name: 'Next available connections' })); });
  const nextPage = screen.getByTestId('traversal-discovery-layer');
  expect(within(nextPage).getByRole('button', { name: 'Add MedicationAdministration via takes_medication' })).toHaveAttribute('data-candidate-edge-id', 'patient-medication');
  expect(savedGraphSnapshot()).toEqual(before);
  expect(mockFitBounds).toHaveBeenCalledTimes(initialFitCount);
  view.rerender(<GuidedGraphWorkspace {...props} selectedOccurrenceId="base" />);
  expect(within(screen.getByTestId('traversal-discovery-layer')).getByRole('button', { name: 'Add Observation via specimen_Observation' })).toBeInTheDocument();
  view.rerender(<GuidedGraphWorkspace {...props} selectedOccurrenceId="patient-occurrence" />);
  expect(within(screen.getByTestId('traversal-discovery-layer')).getByRole('button', { name: 'Add MedicationAdministration via takes_medication' })).toBeInTheDocument();
  act(() => { fireEvent.click(within(nextPage).getByRole('button', { name: 'Previous available connections' })); });
  expect(within(screen.getByTestId('mock-react-flow')).getAllByTestId(/^graph-node-/)).toHaveLength(2);
  fireEvent.click(within(selectedDiscovery).getByRole('button', { name: 'Add Condition via has_condition' }));
  expect(props.onAppendEdge).toHaveBeenCalledExactlyOnceWith('patient-occurrence', 'patient-condition', 'condition');
  expect(savedGraphSnapshot()).toEqual(before);
});

it('routes imported ghost-node activation through native proposal actions and omits the intercepting recipe notice', () => {
  const current = table('node-patient');
  const importedTable: DraftTable = { ...current, document: { ...current.document, importedRecipe: { name: 'default', translationVersion: '1', digest: 'pin', output: 'documents' } } };
  const nativeAdd = vi.fn();
  const ordinaryAdd = vi.fn();
  const importedActions = { onAdd: nativeAdd, onChangeMatchMode: vi.fn(), onRemove: vi.fn() };
  render(<GuidedGraphWorkspace catalog={catalog} table={importedTable} selectedOccurrenceId="base" disabled={false}
    onSelectOccurrence={vi.fn()} onSetBase={vi.fn()} onChangeBase={vi.fn()} onAppendEdge={ordinaryAdd} onChangeEdge={vi.fn()} onChangeMatchMode={vi.fn()} onTruncate={vi.fn()}
    importedRelationshipActions={importedActions} presentation="traversal" />);
  expect(screen.queryByText(/Based on a shared default recipe|Relationship changes are proposed/)).toBeNull();
  const connect = screen.getByRole('button', { name: 'Add DocumentReference via has document' });
  expect(connect).toBeEnabled();
  expect((connect as HTMLElement).style.pointerEvents).toBe('auto');
  act(() => { fireEvent.click(connect); });
  expect(nativeAdd).toHaveBeenCalledExactlyOnceWith('base', 'patient-document', 'node-document');
  expect(ordinaryAdd).not.toHaveBeenCalled();
});

it('routes a discovery rail around lower cousin subtrees as well as siblings', () => {
  const items = [
    { id: 'root', depth: 0 }, { id: 'upper', parentId: 'root', depth: 1 },
    { id: 'selected', parentId: 'upper', depth: 2 }, { id: 'lower', parentId: 'root', depth: 1 },
    { id: 'cousin', parentId: 'lower', depth: 2 }, { id: 'cousin-child', parentId: 'cousin', depth: 3 },
  ];
  const positions = layoutTraversalTree(items, 440, 168, 112);
  const rail = layoutDiscoveryRail(items.map(item => ({ occurrenceId: item.id, parentId: item.parentId, depth: item.depth })), positions, 'selected', 324);
  const cousin = positions.get('cousin')!; const child = positions.get('cousin-child')!;
  const edgeY = cousin.y + 56;
  expect(rail.length).toBeGreaterThan(3);
  for (let i = 1; i < rail.length; i += 1) {
    const from = rail[i - 1]; const to = rail[i];
    const intersectsSavedEdge = from.x === to.x && from.x > cousin.x + 250 && from.x < child.x && Math.min(from.y, to.y) < edgeY && Math.max(from.y, to.y) > edgeY;
    expect(intersectsSavedEdge).toBe(false);
  }
});
