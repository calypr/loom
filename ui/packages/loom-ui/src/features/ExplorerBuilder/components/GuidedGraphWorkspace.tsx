import React, { useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { Background, Controls, MarkerType, Position, ReactFlow, ViewportPortal } from '@xyflow/react';
import type { Edge, Node } from '@xyflow/react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  legalOutgoingEdges,
  replaceableIncomingEdges,
} from '../authoring/routeActions';
import { authoredTraversal } from './authoredTraversal';
import { RouteExtensionPanel } from './RouteExtensionPanel';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import { useGraphLayout } from '../hooks/useGraphLayout';
import { useGraphViewportFitter, type GraphViewportBounds } from '../hooks/useGraphViewportFitter';
import { useTraversalOverflow } from '../hooks/useTraversalOverflow';

const OCCURRENCE_NODE_WIDTH = 250;
const OCCURRENCE_NODE_HEIGHT = 112;
const DISCOVERY_FOOTER_HEIGHT = 152;
const DISCOVERY_FOOTER_GAP = 32;
const DISCOVERY_MIN_WIDTH = 760;
const DISCOVERY_CARD_WIDTH = 250;
const DISCOVERY_CARD_GAP = 16;
const DISCOVERY_CARD_LEFT = 12;
const DISCOVERY_CARD_TOP = 20;

interface TraversalTreeLayoutItem {
  readonly id: string;
  readonly parentId?: string;
  readonly depth: number;
}

/** Lays out a saved occurrence tree in left-to-right columns with disjoint sibling bands. */
export function layoutTraversalTree(
  items: ReadonlyArray<TraversalTreeLayoutItem>,
  columnGap = 320,
  rowGap = 168,
  nodeHeight = 112,
): ReadonlyMap<string, { readonly x: number; readonly y: number }> {
  const childrenByParent = new Map<string, TraversalTreeLayoutItem[]>();
  for (const item of items) {
    if (!item.parentId) continue;
    const children = childrenByParent.get(item.parentId) ?? [];
    children.push(item);
    childrenByParent.set(item.parentId, children);
  }
  const positions = new Map<string, { readonly x: number; readonly y: number }>();
  const visited = new Set<string>();
  let nextLeaf = 0;
  const place = (item: TraversalTreeLayoutItem): number => {
    if (visited.has(item.id)) return (nextLeaf++ * rowGap) + nodeHeight / 2;
    visited.add(item.id);
    const children = childrenByParent.get(item.id) ?? [];
    const childCenters = children.map(place);
    const center = childCenters.length
      ? (childCenters[0]! + childCenters[childCenters.length - 1]!) / 2
      : (nextLeaf++ * rowGap) + nodeHeight / 2;
    positions.set(item.id, { x: item.depth * columnGap, y: center - nodeHeight / 2 });
    return center;
  };
  for (const item of items) {
    if (!item.parentId || !items.some((candidate) => candidate.id === item.parentId)) place(item);
  }
  for (const item of items) if (!visited.has(item.id)) place(item);
  return positions;
}

interface DiscoveryRailOccurrence {
  readonly occurrenceId: string;
  readonly parentId?: string;
  readonly depth: number;
}

interface DiscoveryRailPoint {
  readonly x: number;
  readonly y: number;
}

/** Routes the discovery rail outside lower same-depth subtrees and their edges. */
export function layoutDiscoveryRail(
  occurrences: ReadonlyArray<DiscoveryRailOccurrence>,
  positions: ReadonlyMap<string, DiscoveryRailPoint>,
  selectedOccurrenceId: string,
  busY: number,
  nodeWidth = OCCURRENCE_NODE_WIDTH,
  nodeHeight = OCCURRENCE_NODE_HEIGHT,
  gutter = 16,
  padding = 12,
): ReadonlyArray<DiscoveryRailPoint> {
  const selected = occurrences.find((occurrence) => occurrence.occurrenceId === selectedOccurrenceId);
  const selectedPosition = positions.get(selectedOccurrenceId);
  if (!selected || !selectedPosition) return [];
  const childrenByParent = new Map<string, DiscoveryRailOccurrence[]>();
  for (const occurrence of occurrences) {
    if (!occurrence.parentId) continue;
    const children = childrenByParent.get(occurrence.parentId) ?? [];
    children.push(occurrence);
    childrenByParent.set(occurrence.parentId, children);
  }
  const peerDetours = occurrences.flatMap((peer) => {
    const peerPosition = positions.get(peer.occurrenceId);
    if (!peerPosition || peer.occurrenceId === selectedOccurrenceId || peer.depth !== selected.depth || peerPosition.y <= selectedPosition.y || !childrenByParent.has(peer.occurrenceId)) return [];
    const stack = [peer];
    const seen = new Set<string>();
    let minY = Number.POSITIVE_INFINITY;
    let maxBottom = Number.NEGATIVE_INFINITY;
    let maxRight = Number.NEGATIVE_INFINITY;
    while (stack.length) {
      const item = stack.pop()!;
      if (seen.has(item.occurrenceId)) continue;
      seen.add(item.occurrenceId);
      const position = positions.get(item.occurrenceId);
      if (!position) continue;
      minY = Math.min(minY, position.y);
      maxBottom = Math.max(maxBottom, position.y + nodeHeight);
      maxRight = Math.max(maxRight, position.x + nodeWidth);
      stack.push(...(childrenByParent.get(item.occurrenceId) ?? []));
    }
    return Number.isFinite(minY) ? [{ startY: minY - padding, endY: maxBottom + padding, rightX: maxRight + gutter }] : [];
  }).sort((left, right) => left.startY - right.startY);
  const detours: Array<{ startY: number; endY: number; rightX: number }> = [];
  for (const detour of peerDetours) {
    const previous = detours.at(-1);
    if (previous && detour.startY <= previous.endY) {
      previous.endY = Math.max(previous.endY, detour.endY);
      previous.rightX = Math.max(previous.rightX, detour.rightX);
    } else detours.push({ ...detour });
  }
  const source = { x: selectedPosition.x + nodeWidth, y: selectedPosition.y + nodeHeight * 0.75 };
  const railX = source.x + gutter;
  const points: DiscoveryRailPoint[] = [source, { x: railX, y: source.y }];
  let currentY = source.y;
  for (const detour of detours) {
    if (detour.startY <= currentY || detour.endY >= busY) continue;
    points.push(
      { x: railX, y: detour.startY },
      { x: detour.rightX, y: detour.startY },
      { x: detour.rightX, y: detour.endY },
      { x: railX, y: detour.endY },
    );
    currentY = detour.endY;
  }
  points.push({ x: railX, y: busY });
  return points;
}

const GraphViewportFitter = ({
  expanded,
  graphIdentity,
  hostRef,
  nodeCount,
  fitBounds,
}: {
  readonly expanded: boolean;
  readonly graphIdentity: string;
  readonly hostRef: React.RefObject<HTMLDivElement | null>;
  readonly nodeCount: number;
  readonly fitBounds?: GraphViewportBounds;
}) => {
  useGraphViewportFitter({ expanded, graphIdentity, hostRef, nodeCount, fitBounds });
  return null;
};

export const graphEdgeOverviewLabels = (
  edges: ExplorerBuilderCatalog['edges'],
): ReadonlyMap<string, string> => {
  const groups = new Map<string, { readonly owner: string; readonly labels: Set<string> }>();
  for (const edge of edges) {
    const endpoints = [edge.fromNodeId, edge.toNodeId].sort();
    const key = `${endpoints[0]}\u0000${endpoints[1]}`;
    const group = groups.get(key) ?? { owner: edge.edgeId, labels: new Set<string>() };
    group.labels.add(edge.label.trim());
    groups.set(key, group);
  }
  return new Map([...groups.values()].map((group) => [
    group.owner,
    group.labels.size === 1 ? [...group.labels][0] : `${group.labels.size} relationships`,
  ]));
};

export const GuidedGraphWorkspace = ({
  catalog,
  table,
  selectedOccurrenceId,
  disabled,
  onSelectOccurrence,
  onSetBase,
  onChangeBase,
  onAppendEdge,
  onChangeEdge,
  onChangeMatchMode,
  onTruncate,
  onTableToolbarHostChange,
  presentation = 'catalog',
  showSelectionDetails = true,
  headerStatus,
  rowControl,
  rowMeaning,
  importedRelationshipActions,
}: {
  readonly presentation?: 'catalog' | 'compact' | 'traversal';
  readonly headerStatus?: React.ReactNode;
  /** The traversal-first owner supplies the single contextual inspector. */
  readonly showSelectionDetails?: boolean;
  readonly rowControl?: React.ReactNode;
  readonly rowMeaning?: string;
  // Imported changes have a separate native proposal owner. Never reuse the
  // ordinary Route callbacks when that owner has not been supplied.
  readonly importedRelationshipActions?: {
    readonly onAdd: (parentOccurrenceId: string, edgeId: string, nodeId: string) => void;
    readonly onChangeMatchMode: (occurrenceId: string, matchMode: 'OPTIONAL' | 'REQUIRED') => void;
    readonly onRemove: (occurrenceId: string) => void;
  };
  readonly catalog: ExplorerBuilderCatalog;
  readonly table?: DraftTable;
  readonly selectedOccurrenceId: string;
  readonly disabled: boolean;
  readonly onSelectOccurrence: (id: string) => void;
  readonly onSetBase: (nodeId: string) => void;
  readonly onChangeBase: (nodeId: string) => void;
  readonly onAppendEdge: (
    parentOccurrenceId: string,
    edgeId: string,
    nodeId: string,
  ) => void;
  readonly onChangeEdge: (occurrenceId: string, edgeId: string) => void;
  readonly onChangeMatchMode: (
    occurrenceId: string,
    matchMode: 'OPTIONAL' | 'REQUIRED',
  ) => void;
  readonly onTruncate: (occurrenceId: string) => void;
  readonly onTableToolbarHostChange?: (host: HTMLDivElement | null) => void;
}) => {
  const imported = Boolean(table?.document.importedRecipe);
  const canEditRelationships = !imported || Boolean(importedRelationshipActions);
  const compact = presentation === 'compact';
  const traversalPresentation = presentation === 'traversal';
  const graphHostRef = React.useRef<HTMLDivElement>(null);
  const traversalRef = React.useRef<HTMLElement>(null);
  const [isExpanded, setIsExpanded] = useState(false);
  const [discoveryPage, setDiscoveryPage] = useState<{ scope: string; page: number }>();
  const [showOrphans, setShowOrphans] = useState(false);
  const [showFullNetwork, setShowFullNetwork] = useState(false);
  const catalogIdentity = useMemo(
    () =>
      JSON.stringify({
        nodes: catalog.nodes.map((node) => node.nodeId),
        edges: catalog.edges.map((edge) => [
          edge.edgeId,
          edge.fromNodeId,
          edge.toNodeId,
        ]),
      }),
    [catalog.edges, catalog.nodes],
  );
  const layoutNodes = useMemo(
    () =>
      catalog.nodes.map((node) => ({
        id: node.nodeId,
        width: 170,
        height: 54,
      })),
    [catalog.nodes],
  );
  const layoutEdges = useMemo(
    () =>
      catalog.edges.map((edge) => ({
        id: edge.edgeId,
        source: edge.fromNodeId,
        target: edge.toNodeId,
      })),
    [catalog.edges],
  );
  const layout = useGraphLayout(catalogIdentity, layoutNodes, layoutEdges);
  useEscapeToClose(isExpanded, setIsExpanded);
  const tableIdentity = `${table?.outputId ?? ''}:${table?.document.rootResourceType ?? ''}`;
  const [inspection, setInspection] = useState<{
    readonly tableIdentity: string;
    readonly occurrenceId: string;
    readonly nodeId?: string;
    readonly edgeId?: string;
    readonly allowExistingExtension?: boolean;
  }>({ tableIdentity, occurrenceId: selectedOccurrenceId });
  const activeInspectedNodeId =
    inspection.tableIdentity === tableIdentity ? inspection.nodeId : undefined;
  const activeSelectedEdgeId =
    inspection.tableIdentity === tableIdentity &&
    inspection.occurrenceId === selectedOccurrenceId
      ? inspection.edgeId
      : undefined;
  const activeAllowExistingExtension =
    inspection.tableIdentity === tableIdentity &&
    inspection.occurrenceId === selectedOccurrenceId &&
    inspection.allowExistingExtension === true;
  const updateInspection = (
    nodeId?: string,
    edgeId?: string,
    allowExistingExtension = false,
  ) =>
    setInspection({
      tableIdentity,
      occurrenceId: selectedOccurrenceId,
      nodeId,
      edgeId,
      allowExistingExtension,
    });
  const selectEdge = (edgeId?: string) =>
    updateInspection(
      activeInspectedNodeId,
      edgeId,
      activeAllowExistingExtension,
    );
  const catalogNodeById = useMemo(
    () => new Map(catalog.nodes.map((node) => [node.nodeId, node] as const)),
    [catalog.nodes],
  );
  const occurrences = useMemo(
    () =>
      derivedOccurrences(table, catalog).map((occurrence) => ({
        occurrenceId: occurrence.id,
        index: occurrence.index,
        nodeId: occurrence.nodeId,
        resourceType: catalogNodeById.get(occurrence.nodeId)?.resourceType ?? occurrence.nodeId,
        incomingEdgeId: occurrence.incomingEdgeId,
        relationship: occurrence.relationship,
        matchMode: occurrence.matchMode,
        parentId: occurrence.parentId,
        depth: occurrence.depth,
      })),
    [catalog, catalogNodeById, table],
  );
  const traversalIdentity = `${isExpanded}:${occurrences.map((occurrence) => occurrence.occurrenceId).join(',')}`;
  const isTraversalOverflowing = useTraversalOverflow(
    graphHostRef,
    traversalRef,
    traversalIdentity,
  );
  const occurrencesByNode = useMemo(() => {
    const result = new Map<string, Array<(typeof occurrences)[number]>>();
    for (const occurrence of occurrences) {
      const existing = result.get(occurrence.nodeId);
      if (existing) existing.push(occurrence);
      else result.set(occurrence.nodeId, [occurrence]);
    }
    return result;
  }, [occurrences]);
  const resourceTypeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const node of catalog.nodes) {
      counts.set(node.resourceType, (counts.get(node.resourceType) ?? 0) + 1);
    }
    return counts;
  }, [catalog.nodes]);
  const edgeById = useMemo(
    () => new Map(catalog.edges.map((edge) => [edge.edgeId, edge] as const)),
    [catalog.edges],
  );
  const routeEdgeIds = useMemo(
    () => new Set(occurrences.flatMap((occurrence) => occurrence.incomingEdgeId ? [occurrence.incomingEdgeId] : [])),
    [occurrences],
  );
  const editableEdgesByOccurrence = useMemo(
    () =>
      new Map(
        occurrences.map((occurrence) => [
          occurrence.occurrenceId,
          replaceableIncomingEdges(
            catalog,
            table,
            occurrence.occurrenceId,
          ),
        ]),
      ),
    [catalog, occurrences, table],
  );
  const legalNextEdges = useMemo(
    () => legalOutgoingEdges(catalog, table, selectedOccurrenceId),
    [catalog, selectedOccurrenceId, table],
  );
  const legalNextEdgeIds = useMemo(
    () => new Set(legalNextEdges.map((edge) => edge.edgeId)),
    [legalNextEdges],
  );
  const legalNextEdgesByTarget = useMemo(() => {
    const result = new Map<string, typeof legalNextEdges>();
    for (const edge of legalNextEdges) {
      const existing = result.get(edge.toNodeId);
      if (existing) existing.push(edge);
      else result.set(edge.toNodeId, [edge]);
    }
    return result;
  }, [legalNextEdges]);
  const legalNextNodeIds = useMemo(
    () => new Set(legalNextEdgesByTarget.keys()),
    [legalNextEdgesByTarget],
  );
  const traversalOccurrences = table ? authoredTraversal(table, catalog).map((item) => ({
    occurrenceId: item.id, parentId: item.parentId, resourceType: item.resourceType,
    incomingEdgeId: item.edgeId, relationship: item.relationship, matchMode: item.matchMode,
    depth: item.depth, path: item.path, catalogNodeId: item.catalogNodeId,
  })) : [];
  const authoredOccurrencesByNode = new Map<string, typeof traversalOccurrences>();
  for (const occurrence of traversalOccurrences) {
    if (!occurrence.catalogNodeId) continue;
    const existing = authoredOccurrencesByNode.get(occurrence.catalogNodeId);
    if (existing) existing.push(occurrence);
    else authoredOccurrencesByNode.set(occurrence.catalogNodeId, [occurrence]);
  }
  const traversalOccurrenceLabel = (occurrence: (typeof traversalOccurrences)[number]) => {
    const label = occurrence.parentId ? occurrence.relationship ?? 'Relationship unavailable' : 'Starting records';
    const siblings = traversalOccurrences.filter((candidate) => candidate.parentId === occurrence.parentId && candidate.resourceType === occurrence.resourceType && candidate.relationship === occurrence.relationship);
    return siblings.length > 1 ? `${label} (${siblings.findIndex((candidate) => candidate.occurrenceId === occurrence.occurrenceId) + 1})` : label;
  };
  const nodes: Node[] = catalog.nodes.map((node, index) => {
    const nodeOccurrences = occurrencesByNode.get(node.nodeId) ?? [];
    const authoredOccurrences = traversalPresentation
      ? authoredOccurrencesByNode.get(node.nodeId) ?? []
      : [];
    const inRoute = nodeOccurrences.length > 0 || authoredOccurrences.length > 0;
    const isPendingRowStart =
      occurrences.length === 0 &&
      node.rowRootEligible &&
      activeInspectedNodeId === node.nodeId;
    const isSelected =
      isPendingRowStart ||
      authoredOccurrences.some((occurrence) => occurrence.occurrenceId === selectedOccurrenceId) ||
      nodeOccurrences.some(
        (occurrence) => occurrence.occurrenceId === selectedOccurrenceId,
      );
    const isInspected =
      !isPendingRowStart && activeInspectedNodeId === node.nodeId;
    const isReachable = legalNextNodeIds.has(node.nodeId);
    const canStart = occurrences.length === 0 && node.rowRootEligible;
    const duplicate = (resourceTypeCounts.get(node.resourceType) ?? 0) > 1;
    return {
      id: node.nodeId,
      position: layout.positions.get(node.nodeId) ?? {
        x: (index % 4) * 210,
        y: Math.floor(index / 4) * 100,
      },
      data: {
        label: (
          <div className="min-w-0 px-2 py-1 text-left">
            <div className="truncate text-sm font-semibold text-slate-900">
              {node.resourceType}
            </div>
            <div className="mt-0.5 text-[10px] font-medium uppercase tracking-wide text-slate-500">
              {isSelected
                ? 'Editing this query node'
                : isInspected
                  ? 'Selected resource'
                  : inRoute
                    ? 'In traversal'
                    : canStart
                      ? 'Available row start'
                      : occurrences.length === 0
                        ? 'Not eligible as a row start'
                        : isReachable
                          ? 'Click to add to query'
                          : node.rowRootEligible
                            ? 'Click to start a new query'
                            : 'Available resource'}
            </div>
            {duplicate && (
              <div className="mt-0.5 truncate font-mono text-[10px] text-slate-400">
                {node.nodeId.slice(-8)}
              </div>
            )}
            {nodeOccurrences.length > 1 && (
              <div className="mt-0.5 text-[10px] font-semibold text-violet-700">
                {nodeOccurrences.length} query occurrences
              </div>
            )}
            {authoredOccurrences.length ? <div className="mt-1 grid max-h-24 gap-1 overflow-y-auto" aria-label={`Authored ${node.resourceType} occurrences`}>
              {authoredOccurrences.map((occurrence) => <button key={occurrence.occurrenceId} type="button"
                data-occurrence-selector="true" data-occurrence-id={occurrence.occurrenceId} data-occurrence-path={occurrence.path}
                data-term-kind="occurrence" aria-pressed={occurrence.occurrenceId === selectedOccurrenceId}
                aria-label={`${occurrence.path} (occurrence ${occurrence.occurrenceId})`}
                title={`${occurrence.path} · occurrence ${occurrence.occurrenceId}`}
                className={`rounded border px-1.5 py-1 text-left text-[10px] leading-tight ${occurrence.occurrenceId === selectedOccurrenceId ? 'border-blue-600 bg-blue-600 text-white' : 'border-blue-200 bg-white text-blue-900'}`}
                onClick={(event) => { event.stopPropagation(); onSelectOccurrence(occurrence.occurrenceId); }}>
                {traversalOccurrenceLabel(occurrence)}
              </button>)}
            </div> : null}
            <div className="mt-1 text-[10px] text-slate-500">
              {node.rowGrain ?? 'row grain unavailable'} ·{' '}
              {node.populated ? node.documentCount.toLocaleString() : '0'}{' '}
              documents
            </div>
          </div>
        ),
      },
      style: {
        border: isSelected
          ? '3px solid #7c3aed'
          : isInspected
            ? '3px solid #f59e0b'
            : inRoute
              ? '3px solid #2f5aac'
              : isReachable
                ? '3px solid #16a34a'
                : '1px solid #94a3b8',
        borderRadius: 12,
        background: isSelected
          ? '#f3e8ff'
          : isInspected
            ? '#fffbeb'
            : inRoute
              ? '#dbeafe'
              : isReachable
                ? '#f0fdf4'
                : '#ffffff',
        boxShadow:
          isSelected || isInspected || inRoute || isReachable
            ? '0 8px 24px rgba(30,64,175,.18)'
            : '0 3px 10px rgba(15,23,42,.08)',
        opacity: isSelected || isInspected || inRoute || isReachable ? 1 : 0.42,
        padding: 8,
        width: 190,
        cursor: disabled && !traversalPresentation ? 'default' : 'pointer',
        pointerEvents: disabled && !traversalPresentation ? 'none' : 'auto',
      },
    };
  });
  const edgeOverviewLabels = useMemo(
    () => graphEdgeOverviewLabels(catalog.edges),
    [catalog.edges],
  );
  const edges: Edge[] = catalog.edges.map((edge) => {
    const isRouteEdge = routeEdgeIds.has(edge.edgeId);
    const isLegalNextEdge = legalNextEdgeIds.has(edge.edgeId);
    const edgeColor = isRouteEdge
      ? '#2563eb'
      : isLegalNextEdge
        ? '#16a34a'
        : '#64748b';
    const edgeWidth = isRouteEdge ? 3 : isLegalNextEdge ? 2.5 : 1.75;
    return {
      id: edge.edgeId,
      // Loom's catalog edges are directed. Keep the catalog orientation in
      // React Flow; do not synthesize a reverse edge for FHIR relationships.
      source: edge.fromNodeId,
      target: edge.toNodeId,
      label: edgeOverviewLabels.get(edge.edgeId),
      type: 'smoothstep',
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: edgeColor,
        width: 16,
        height: 16,
        strokeWidth: 1.5,
      },
      animated: isRouteEdge,
      style: {
        stroke: edgeColor,
        strokeWidth: edgeWidth,
        opacity: isRouteEdge
          ? 1
          : occurrences.length === 0 || isLegalNextEdge
            ? 0.7
            : 0.28,
      },
      labelStyle: {
        fill: '#475569',
        fontSize: 9,
        fontWeight: 600,
      },
      labelBgStyle: {
        fill: '#ffffff',
        fillOpacity: 0.9,
      },
      labelBgPadding: [4, 2],
      labelBgBorderRadius: 4,
    };
  });
  const nodeIds = new Set(nodes.map((node) => node.id));
  const connectedNodeIds = new Set(
    edges.flatMap((edge) =>
      nodeIds.has(edge.source) && nodeIds.has(edge.target)
        ? [edge.source, edge.target]
        : [],
    ),
  );
  const focusedNodeIds = new Set(legalNextNodeIds);
  let focusedOccurrence = occurrences.find((item) => item.occurrenceId === selectedOccurrenceId);
  while (focusedOccurrence) {
    focusedNodeIds.add(focusedOccurrence.nodeId);
    focusedOccurrence = occurrences.find((item) => item.occurrenceId === focusedOccurrence?.parentId);
  }
  for (const occurrence of occurrences) {
    if (occurrence.parentId === selectedOccurrenceId || !occurrence.parentId) focusedNodeIds.add(occurrence.nodeId);
  }
  const catalogNodes = nodes.filter((node) =>
    (showOrphans || connectedNodeIds.has(node.id)) &&
    (!compact || showFullNetwork || occurrences.length === 0 || focusedNodeIds.has(node.id)),
  );
  const catalogNodeIds = new Set(catalogNodes.map((node) => node.id));
  const catalogEdges = edges.filter(
    (edge) =>
      catalogNodeIds.has(edge.source) && catalogNodeIds.has(edge.target),
  );
  const authoredGraphActive = traversalPresentation && traversalOccurrences.length > 0;
  const routeNodeId = (occurrenceId: string) => `occurrence:${occurrenceId}`;
  const layoutItems: TraversalTreeLayoutItem[] = traversalOccurrences.map((occurrence) => ({
    id: routeNodeId(occurrence.occurrenceId),
    parentId: occurrence.parentId ? routeNodeId(occurrence.parentId) : undefined,
    depth: occurrence.depth,
  }));
  const occurrencePositions = layoutTraversalTree(layoutItems, 440, 168, OCCURRENCE_NODE_HEIGHT);
  const occurrenceNodes: Node[] = authoredGraphActive ? traversalOccurrences.map((occurrence) => {
    const unresolved = !occurrence.catalogNodeId;
    const selected = occurrence.occurrenceId === selectedOccurrenceId;
    return {
      id: routeNodeId(occurrence.occurrenceId),
      position: occurrencePositions.get(routeNodeId(occurrence.occurrenceId)) ?? { x: occurrence.depth * 440, y: 0 },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      ariaLabel: `${occurrence.path} (occurrence ${occurrence.occurrenceId})`,
      data: { label: <button type="button" data-occurrence-selector="true" data-occurrence-id={occurrence.occurrenceId}
        data-occurrence-path={occurrence.path} data-term-kind="occurrence" aria-pressed={selected}
        aria-label={`${occurrence.path} (occurrence ${occurrence.occurrenceId})`}
        title={`${occurrence.path} · occurrence ${occurrence.occurrenceId}`}
        className={`nokey flex h-full w-full flex-col justify-center gap-1 rounded-lg border px-3 py-2 text-left ${selected ? 'border-blue-700 bg-blue-700 text-white shadow-md' : unresolved ? 'border-amber-400 bg-amber-50 text-amber-950' : 'border-blue-300 bg-white text-slate-900'}`}
        onClick={(event) => { event.stopPropagation(); onSelectOccurrence(occurrence.occurrenceId); }}>
        <span className="truncate text-sm font-semibold">{occurrence.resourceType}</span>
        <span className={`truncate text-xs ${selected ? 'text-blue-100' : unresolved ? 'text-amber-900' : 'text-blue-800'}`}>{occurrence.parentId ? occurrence.relationship ?? 'Relationship unavailable' : 'Starting records'}</span>
        <span className={`text-[10px] ${selected ? 'text-blue-100' : 'text-slate-500'}`}>{unresolved ? 'Catalog relationship unavailable' : occurrence.matchMode === 'REQUIRED' && occurrence.parentId ? 'Required match' : occurrence.parentId ? 'Optional feature' : 'Row start'}</span>
      </button> },
      style: { width: OCCURRENCE_NODE_WIDTH, height: OCCURRENCE_NODE_HEIGHT, border: selected ? '2px solid #1d4ed8' : unresolved ? '1px solid #d97706' : '1px solid #93c5fd', borderRadius: 10, background: '#fff', padding: 0, cursor: 'pointer', pointerEvents: 'auto' },
    };
  }) : [];
  const routeOccurrenceEdges: Edge[] = authoredGraphActive ? traversalOccurrences.flatMap((occurrence) => {
    if (!occurrence.parentId) return [];
    const parent = traversalOccurrences.find((candidate) => candidate.occurrenceId === occurrence.parentId);
    if (!parent) return [];
    const unresolved = !parent.catalogNodeId || !occurrence.catalogNodeId;
    return [{
      id: `occurrence-edge:${occurrence.occurrenceId}`,
      source: routeNodeId(parent.occurrenceId),
      target: routeNodeId(occurrence.occurrenceId),
      type: 'default',
      markerEnd: { type: MarkerType.ArrowClosed, color: unresolved ? '#d97706' : '#2563eb' },
      label: occurrence.relationship && occurrence.relationship.length > 22 ? `${occurrence.relationship.slice(0, 21)}…` : occurrence.relationship ?? 'Saved relationship',
      ariaLabel: `Saved path from ${parent.path} to ${occurrence.path}`,
      data: { authoredOccurrenceId: occurrence.occurrenceId },
      style: { stroke: unresolved ? '#d97706' : '#2563eb', strokeWidth: 2, ...(unresolved ? { strokeDasharray: '6 4' } : {}) },
      labelStyle: { fill: unresolved ? '#92400e' : '#334155', fontSize: 9, fontWeight: 600 },
      labelBgStyle: { fill: '#ffffff', fillOpacity: 0.96 },
      labelBgPadding: [4, 2],
      labelBgBorderRadius: 4,
    }];
  }) : [];
  const nodesForViewport = authoredGraphActive
    ? occurrenceNodes
    : catalogNodes;
  const edgesForViewport = authoredGraphActive
    ? routeOccurrenceEdges
    : catalogEdges;
  const selectedAuthoredOccurrence = traversalOccurrences.find((occurrence) => occurrence.occurrenceId === selectedOccurrenceId);
  const availableConnections = authoredGraphActive && selectedAuthoredOccurrence ? legalNextEdges : [];
  const authoredViewportGeometry = authoredGraphActive ? (() => {
    const positions = occurrenceNodes.map((node) => node.position);
    const minX = Math.min(...positions.map((position) => position.x));
    const minY = Math.min(...positions.map((position) => position.y));
    const maxX = Math.max(...positions.map((position) => position.x + OCCURRENCE_NODE_WIDTH));
    const maxY = Math.max(...positions.map((position) => position.y + OCCURRENCE_NODE_HEIGHT));
    const discoveryY = maxY + DISCOVERY_FOOTER_GAP;
    const discoveryWidth = Math.max(DISCOVERY_MIN_WIDTH, maxX - minX + 32);
    return {
      viewportBounds: {
        x: minX,
        y: minY,
        width: discoveryWidth,
        height: discoveryY + DISCOVERY_FOOTER_HEIGHT - minY,
      },
      discoveryBounds: { x: minX, y: discoveryY, width: discoveryWidth, height: DISCOVERY_FOOTER_HEIGHT },
    };
  })() : undefined;
  const authoredViewportBounds = authoredViewportGeometry?.viewportBounds;
  const discoveryBounds = authoredViewportGeometry?.discoveryBounds;
  const discoveryPageSize = Math.max(1, Math.floor(((discoveryBounds?.width ?? DISCOVERY_MIN_WIDTH) - 2 * DISCOVERY_CARD_LEFT - 88 + DISCOVERY_CARD_GAP) / (DISCOVERY_CARD_WIDTH + DISCOVERY_CARD_GAP)));
  const discoveryScope = JSON.stringify([table?.outputId, selectedOccurrenceId, availableConnections.map(edge => edge.edgeId)]);
  const availableConnectionPage = discoveryPage?.scope === discoveryScope ? discoveryPage.page : 0;
  const availableConnectionPageCount = Math.max(1, Math.ceil(availableConnections.length / discoveryPageSize));
  const visibleConnectionPage = Math.min(availableConnectionPage, availableConnectionPageCount - 1);
  const visibleConnections = availableConnections.slice(visibleConnectionPage * discoveryPageSize, (visibleConnectionPage + 1) * discoveryPageSize);
  const setAvailableConnectionPage = (page: number) => setDiscoveryPage({ scope: discoveryScope, page });
  const discoveryPageCards = visibleConnections.map((edge, index) => ({
    edge,
    x: DISCOVERY_CARD_LEFT + index * (DISCOVERY_CARD_WIDTH + DISCOVERY_CARD_GAP),
  }));
  const discoveryFooterTop = discoveryBounds && authoredViewportBounds ? discoveryBounds.y - authoredViewportBounds.y : 0;
  const discoveryBusY = discoveryFooterTop + 12;
  const discoveryCardTop = discoveryFooterTop + DISCOVERY_CARD_TOP;
  const occurrencePositionById = new Map(traversalOccurrences.map((occurrence) => [
    occurrence.occurrenceId,
    occurrencePositions.get(routeNodeId(occurrence.occurrenceId))!,
  ]));
  const discoveryTrunkPoints = authoredGraphActive && discoveryBounds
    ? layoutDiscoveryRail(traversalOccurrences, occurrencePositionById, selectedOccurrenceId, discoveryBounds.y + 12)
    : [];
  const localDiscoveryTrunkPoints = authoredViewportBounds
    ? discoveryTrunkPoints.map((point) => ({ x: point.x - authoredViewportBounds.x, y: point.y - authoredViewportBounds.y }))
    : [];
  const discoveryRailX = localDiscoveryTrunkPoints[1]?.x ?? 0;
  const discoveryCenters = discoveryPageCards.map(({ x }) => x + DISCOVERY_CARD_WIDTH / 2);
  const discoveryBusStart = Math.min(discoveryRailX, ...discoveryCenters);
  const discoveryBusEnd = Math.max(discoveryRailX, ...discoveryCenters);
  const graphIdentity = JSON.stringify([
    authoredGraphActive ? 'authored-tree' : layout.identity ?? 'layout-pending',
    nodesForViewport.map(node => [node.id, node.position.x, node.position.y]),
    edgesForViewport.map(edge => [edge.id, edge.source, edge.target]),
  ]);
  const selectedPathRequiresMatches = (() => {
    let occurrence = occurrences.find((candidate) => candidate.occurrenceId === selectedOccurrenceId);
    while (occurrence?.parentId) {
      if (occurrence.matchMode !== 'REQUIRED') return false;
      occurrence = occurrences.find((candidate) => candidate.occurrenceId === occurrence?.parentId);
    }
    return true;
  })();
  const inspectNode = (nodeId: string) => {
    const authoredOccurrence = traversalOccurrences.find((occurrence) => routeNodeId(occurrence.occurrenceId) === nodeId);
    if (authoredOccurrence) {
      updateInspection();
      onSelectOccurrence(authoredOccurrence.occurrenceId);
      return;
    }
    const authoredNodeOccurrences = authoredOccurrencesByNode.get(nodeId) ?? [];
    if (disabled) {
      if (!traversalPresentation) return;
      updateInspection(nodeId);
      const selected = authoredNodeOccurrences.find((occurrence) => occurrence.occurrenceId === selectedOccurrenceId) ?? authoredNodeOccurrences[0];
      if (selected) onSelectOccurrence(selected.occurrenceId);
      return;
    }
    if (imported && !importedRelationshipActions) {
      const occurrence = authoredNodeOccurrences.find((item) => item.occurrenceId === selectedOccurrenceId) ?? authoredNodeOccurrences.at(-1);
      updateInspection(nodeId, undefined, Boolean(occurrence && importedRelationshipActions));
      if (occurrence) onSelectOccurrence(occurrence.occurrenceId);
      return;
    }
    if (occurrences.length === 0) {
      updateInspection(nodeId);
      const node = catalogNodeById.get(nodeId);
      if (node?.rowRootEligible) onSetBase(nodeId);
      return;
    }
    const routeOccurrences = occurrencesByNode.get(nodeId) ?? [];
    const matchingEdges = legalNextEdgesByTarget.get(nodeId) ?? [];
    if (routeOccurrences.length > 0 && matchingEdges.length > 0) {
      updateInspection(nodeId, undefined, true);
      return;
    }
    if (routeOccurrences.length === 1) {
      updateInspection();
      onSelectOccurrence(routeOccurrences[0].occurrenceId);
      return;
    } else if (routeOccurrences.length > 1) {
      const currentIndex = routeOccurrences.findIndex(
        (occurrence) => occurrence.occurrenceId === selectedOccurrenceId,
      );
      const next =
        routeOccurrences[(currentIndex + 1) % routeOccurrences.length];
      updateInspection();
      onSelectOccurrence(next.occurrenceId);
      return;
    }
    if (matchingEdges.length === 1) {
      updateInspection(nodeId);
      if (!imported) onAppendEdge(selectedOccurrenceId, matchingEdges[0].edgeId, nodeId);
      return;
    }
    updateInspection(nodeId);
  };
  const inspectEdge = (edgeId: string) => {
    if (authoredGraphActive) return;
    if (disabled && !traversalPresentation) return;
    const edge = edgeById.get(edgeId);
    if (!edge) return;
    updateInspection(edge.toNodeId, edgeId);
    if (traversalPresentation) {
      const authoredCandidates = traversalOccurrences.filter((occurrence) => occurrence.incomingEdgeId === edgeId);
      const authoredOccurrence = authoredCandidates.find((occurrence) => occurrence.occurrenceId === selectedOccurrenceId) ?? authoredCandidates[0];
      if (authoredOccurrence) onSelectOccurrence(authoredOccurrence.occurrenceId);
      return;
    }
    const routeOccurrence = occurrencesByNode.get(edge.toNodeId)?.at(-1);
    if (routeOccurrence) onSelectOccurrence(routeOccurrence.occurrenceId);
  };
  const useAsRowStart = (nodeId: string) => {
    if (disabled || table?.document.importedRecipe) return;
    onSetBase(nodeId);
    selectEdge(undefined);
  };
  const addRelationship = (edgeId: string, nodeId: string) => {
    if (disabled || !canEditRelationships || !legalNextEdgeIds.has(edgeId)) return;
    if (imported) importedRelationshipActions?.onAdd(selectedOccurrenceId, edgeId, nodeId);
    else onAppendEdge(selectedOccurrenceId, edgeId, nodeId);
    updateInspection();
  };
  const selectedOccurrence = occurrences.find((item) => item.occurrenceId === selectedOccurrenceId);
  const content = (
    <>
      {isExpanded && <div className="fixed inset-0 z-40 bg-slate-950/30" />}
      <section
        role={isExpanded ? 'dialog' : undefined}
        aria-modal={isExpanded ? true : undefined}
        aria-label={isExpanded ? 'Expanded dataset graph' : traversalPresentation ? 'Record network' : undefined}
        className={`flex min-w-0 flex-col p-3 ${isExpanded ? 'fixed inset-3 z-50 h-[calc(100dvh-1.5rem)] min-h-0 rounded-xl border border-slate-200 bg-white shadow-xl' : compact ? 'relative h-[26rem] min-h-0 rounded-lg border border-slate-200 bg-white' : traversalPresentation ? 'relative h-[28rem] min-h-0 rounded-lg border border-slate-200 bg-white' : 'relative h-[min(70dvh,52rem)] min-h-[43rem] bg-white/60'}`}
      >
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
          <div className="min-w-0">
            {!traversalPresentation ? <h2 className="text-base font-semibold text-slate-900">
              {compact ? 'Related records' : 'Dataset graph'}{' '}
              <span className="ml-1 text-xs font-normal text-slate-400">
                {catalog.nodes.length} resources · {catalog.edges.length}{' '}
                relationships
              </span>
            </h2> : null}
          </div>
          <div
            data-testid="graph-header-controls"
            className="flex flex-wrap items-center gap-1.5"
          >
            {traversalPresentation && headerStatus ? <span className="px-2 text-xs text-slate-500">{headerStatus}</span> : null}
            {compact ? <label className="flex items-center gap-1.5 text-xs text-slate-700"><input type="checkbox" checked={showFullNetwork} onChange={(event) => setShowFullNetwork(event.currentTarget.checked)} />Show full network</label> : null}
            {!authoredGraphActive ? <label className="flex shrink-0 cursor-pointer items-center gap-1.5 rounded border border-slate-300 bg-white/95 px-2 py-1 text-[11px] font-semibold text-slate-700 shadow-sm">
              <input
                type="checkbox"
                className="h-3.5 w-3.5 rounded border-slate-400 text-blue-600 focus:ring-blue-500"
                checked={showOrphans}
                onChange={(event) => setShowOrphans(event.target.checked)}
                aria-label="Show orphans"
              />
              Show orphans
            </label> : null}
            <div
              ref={onTableToolbarHostChange}
              id="explorer-builder-table-toolbar-host"
              className="min-w-0"
            />
            {isExpanded && (
              <button
                type="button"
                className="rounded border border-blue-300 bg-blue-50 px-2 py-1 text-[11px] font-semibold text-blue-800 hover:bg-blue-100"
                onClick={() => setIsExpanded(false)}
              >
                Close graph
              </button>
            )}
          </div>
        </div>
        {showSelectionDetails && (rowControl || rowMeaning) ? (
          <div className="mt-2 flex shrink-0 flex-wrap items-center gap-2 rounded border border-violet-200 bg-violet-50 px-2 py-1.5" aria-label="Row definition">
            {rowControl}
            {rowMeaning ? <p className="min-w-0 text-xs text-violet-950">{rowMeaning}</p> : null}
          </div>
        ) : null}
        <div
          ref={graphHostRef}
          data-testid="graph-viewport"
          className={`relative mt-2 min-h-0 flex-1 overflow-hidden rounded-lg bg-slate-100/70 ${isExpanded || compact || traversalPresentation ? '' : 'min-h-[32rem]'}`}
        >
          <ReactFlow
            key={graphIdentity}
            nodes={nodesForViewport}
            edges={edgesForViewport}
            nodesDraggable={false}
            nodesConnectable={false}
            nodesFocusable
            autoPanOnNodeFocus={!authoredGraphActive}
            onKeyDown={(event) => {
              const target = event.target instanceof Element ? event.target : undefined;
              if (target?.closest('[data-occurrence-selector="true"], [data-candidate-edge-id]')) return;
              const nodeId = target?.closest('.react-flow__node')?.getAttribute('data-id');
              if (nodeId && (event.key === 'Enter' || event.key === ' ')) {
                event.preventDefault();
                inspectNode(nodeId);
              }
            }}
            minZoom={0.05}
            maxZoom={2}
            proOptions={{ hideAttribution: true }}
            onInit={(instance) => {
              if (authoredGraphActive && authoredViewportBounds) {
                void instance.fitBounds(authoredViewportBounds, { padding: 0.08, duration: 200 });
              } else {
                void instance.fitView({
                  padding: 0.08,
                  minZoom: 0.05,
                  maxZoom: 1.8,
                  duration: 200,
                });
              }
            }}
            onNodeClick={(event, node) => {
              event.stopPropagation();
              inspectNode(node.id);
            }}
            onEdgeClick={(event, edge) => {
              event.stopPropagation();
              const occurrenceId = edge.data?.authoredOccurrenceId;
              if (typeof occurrenceId === 'string') {
                onSelectOccurrence(occurrenceId);
                return;
              }
              inspectEdge(edge.id);
            }}
            onPaneClick={(event) => {
              const target = event.target;
              if (
                target instanceof Element &&
                target.closest('.react-flow__node, .react-flow__edge')
              )
                return;
              if (!isExpanded && !authoredGraphActive) setIsExpanded(true);
            }}
          >
            <GraphViewportFitter
              expanded={isExpanded}
              graphIdentity={graphIdentity}
              hostRef={graphHostRef}
              nodeCount={nodesForViewport.length}
              fitBounds={authoredViewportBounds}
            />
            <Background color="#cbd5e1" gap={28} size={1} />
            <Controls showInteractive={false} />
            {authoredGraphActive && discoveryBounds && authoredViewportBounds ? <ViewportPortal>
              <div data-testid="traversal-discovery-layer"
                role="group" aria-label="Available connection nodes"
                className="nopan nodrag nowheel pointer-events-none"
                style={{ position: 'absolute', transform: `translate(${authoredViewportBounds.x}px, ${authoredViewportBounds.y}px)`, width: `${authoredViewportBounds.width}px`, height: `${authoredViewportBounds.height}px`, zIndex: 8 }}>
                {visibleConnections.length && localDiscoveryTrunkPoints.length > 1 ? <svg aria-hidden="true" data-testid="traversal-discovery-connectors"
                  width={authoredViewportBounds.width} height={authoredViewportBounds.height} className="pointer-events-none absolute inset-0 overflow-visible">
                  <defs><marker id="available-connection-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto" markerUnits="userSpaceOnUse"><path d="M0,0 L8,4 L0,8 z" fill="#059669" /></marker></defs>
                  <path data-testid="available-connection-trunk" data-source-occurrence-id={selectedOccurrenceId}
                    data-peer-detours={Math.max(0, (localDiscoveryTrunkPoints.length - 3) / 4)}
                    d={`M ${localDiscoveryTrunkPoints[0]!.x} ${localDiscoveryTrunkPoints[0]!.y} ${localDiscoveryTrunkPoints.slice(1).map((point) => `L ${point.x} ${point.y}`).join(' ')}`}
                    fill="none" stroke="#059669" strokeWidth="2" strokeDasharray="6 5" />
                  {discoveryCenters.length > 0 ? <path data-testid="available-connection-bus" data-source-occurrence-id={selectedOccurrenceId}
                    d={`M ${discoveryBusStart} ${discoveryBusY} H ${discoveryBusEnd}`}
                    fill="none" stroke="#059669" strokeWidth="2" strokeDasharray="6 5" /> : null}
                  {discoveryPageCards.map(({ edge, x }) => <path key={edge.edgeId}
                    data-testid={`available-connection-edge-${edge.edgeId}`}
                    data-source-occurrence-id={selectedOccurrenceId}
                    data-candidate-edge-id={edge.edgeId}
                    data-target-x={x + DISCOVERY_CARD_WIDTH / 2}
                    data-target-y={discoveryCardTop}
                    d={`M ${x + DISCOVERY_CARD_WIDTH / 2} ${discoveryBusY} V ${discoveryCardTop}`}
                    fill="none" stroke="#059669" strokeWidth="2" strokeDasharray="6 5" markerEnd="url(#available-connection-arrow)" />)}
                </svg> : null}
                {discoveryPageCards.map(({ edge, x }) => {
                  const resource = catalogNodeById.get(edge.toNodeId)?.resourceType ?? 'resource';
                  return <button key={edge.edgeId} type="button" data-candidate-edge-id={edge.edgeId}
                    aria-label={`Add ${resource} via ${edge.label}`} title={`Connect ${selectedAuthoredOccurrence?.resourceType ?? 'selected resource'} to ${resource} via ${edge.label}`}
                    className="nopan nodrag nowheel nokey pointer-events-auto absolute flex h-[112px] w-[250px] cursor-pointer flex-col justify-center gap-2 rounded-lg border border-dashed border-emerald-600 bg-emerald-50/95 px-3 py-2 text-left text-emerald-950 shadow-md hover:bg-emerald-100 disabled:cursor-not-allowed disabled:opacity-50"
                    style={{ left: x, top: discoveryFooterTop + DISCOVERY_CARD_TOP, pointerEvents: 'auto' }}
                    disabled={disabled || !canEditRelationships}
                    onPointerDown={(event) => event.stopPropagation()}
                    onWheel={(event) => event.stopPropagation()}
                    onClick={(event) => { event.stopPropagation(); addRelationship(edge.edgeId, edge.toNodeId); }}>
                    <span className="text-sm font-semibold">Connect to {resource}</span>
                    <span className="truncate text-xs text-emerald-800">via {edge.label}</span>
                  </button>;
                })}
                {availableConnectionPageCount > 1 ? <div className="nopan nodrag nowheel pointer-events-auto absolute flex items-center gap-1 text-[10px] text-slate-700" style={{ left: DISCOVERY_CARD_LEFT + discoveryPageSize * DISCOVERY_CARD_WIDTH + (discoveryPageSize - 1) * DISCOVERY_CARD_GAP + 16, top: discoveryFooterTop + 64, pointerEvents: 'auto' }}>
                  <button type="button" className="nokey rounded px-1 py-1 hover:bg-emerald-50 disabled:opacity-40" aria-label="Previous available connections"
                    disabled={visibleConnectionPage === 0} onClick={(event) => { event.stopPropagation(); setAvailableConnectionPage(visibleConnectionPage - 1); }}>‹</button>
                  <span>{visibleConnectionPage + 1}/{availableConnectionPageCount}</span>
                  <button type="button" className="nokey rounded px-1 py-1 hover:bg-emerald-50 disabled:opacity-40" aria-label="Next available connections"
                    disabled={visibleConnectionPage >= availableConnectionPageCount - 1} onClick={(event) => { event.stopPropagation(); setAvailableConnectionPage(visibleConnectionPage + 1); }}>›</button>
                </div> : null}
              </div>
            </ViewportPortal> : null}
          </ReactFlow>
          {!authoredGraphActive ? <RouteExtensionPanel
            catalog={catalog}
            table={table}
            selectedOccurrenceId={selectedOccurrenceId}
            inspectedNodeId={activeInspectedNodeId}
            selectedEdgeId={activeSelectedEdgeId}
            allowExistingExtension={activeAllowExistingExtension}
            disabled={disabled || !canEditRelationships}
            requireConfirmation={imported}
            onSelectEdge={selectEdge}
            onUseAsRowStart={useAsRowStart}
            onChangeRowStart={table?.document.importedRecipe ? () => undefined : onChangeBase}
            onAddEdge={addRelationship}
          /> : null}
          {table?.document.importedRecipe && !authoredGraphActive ? (
            <p role="note" className="absolute bottom-3 left-3 z-10 max-w-sm rounded-md border border-amber-200 bg-amber-50/95 px-3 py-2 text-xs text-amber-900">
              {importedRelationshipActions ? 'Relationship changes are proposed for this project; the shared recipe stays pinned. Adding values keeps the row identity. Required matches can exclude rows.' : 'Based on a shared default recipe. Relationship editing is unavailable until native source proposals are ready.'}
            </p>
          ) : null}
          {!traversalPresentation ? <nav
            ref={traversalRef}
            aria-label="Current traversal"
            className={`absolute left-3 top-3 z-10 flex w-fit max-w-[calc(100%-9rem)] items-center gap-2 overflow-x-auto rounded-lg border border-blue-200 bg-blue-50/95 px-2 py-1.5 text-xs shadow-sm ${isTraversalOverflowing ? 'group transition-[max-width] hover:max-w-[calc(100%-1.5rem)]' : ''}`}
          >
            <div className="shrink-0 border-r border-blue-200 pr-2">
              <p className="font-semibold text-blue-950">Current query</p>
              <p
                data-traversal-label
                className={`${isTraversalOverflowing ? 'max-w-28 truncate group-hover:max-w-48' : ''} text-[10px] text-blue-600`}
              >
                Selected parent:{' '}
                {occurrences.find(
                  (occurrence) =>
                    occurrence.occurrenceId === selectedOccurrenceId,
                )?.resourceType ?? 'none'}
              </p>
            </div>
            {occurrences.length === 0 ? (
              <span className="shrink-0 text-blue-950">
                Click a resource to start.
              </span>
            ) : (
              <div className="flex min-w-max items-center gap-1">
                {occurrences.map((occurrence) => (
                  <div
                    key={occurrence.occurrenceId}
                    className="flex items-center gap-1.5"
                  >
                    {occurrence.depth > 0 && (
                      <span className="font-bold text-blue-400">↳</span>
                    )}
                    <div
                      className={`rounded-md border ${occurrence.occurrenceId === selectedOccurrenceId ? 'border-blue-600 bg-blue-600 text-white shadow-sm' : 'border-blue-300 bg-white text-blue-900'}`}
                    >
                      <button
                        type="button"
                        title={occurrence.resourceType}
                        data-traversal-label
                        data-occurrence-id={occurrence.occurrenceId}
                        className={`${isTraversalOverflowing ? 'max-w-24 truncate transition-[max-width] group-hover:max-w-40' : ''} block w-full px-2 py-1 text-left font-semibold`}
                        onClick={() =>
                          onSelectOccurrence(occurrence.occurrenceId)
                        }
                      >
                        {occurrence.resourceType}
                      </button>
                      {!occurrence.parentId ? (
                        <span className="block border-t border-current/20 px-2 pb-1 pt-0.5 text-[9px] font-medium opacity-75">
                          Row start
                        </span>
                      ) : (
                        <>
                          <select
                            aria-label={`Relationship for ${occurrence.resourceType} occurrence`}
                            title="Relationship used to reach this query occurrence"
                            className={`block max-w-40 border-x-0 border-b-0 border-t border-current/20 bg-transparent px-1.5 pb-1 pt-0.5 font-mono text-[9px] outline-none ${occurrence.occurrenceId === selectedOccurrenceId ? 'text-white' : 'text-blue-700'}`}
                            value={occurrence.incomingEdgeId ?? ''}
                            disabled={
                              disabled || Boolean(table?.document.importedRecipe) ||
                              (editableEdgesByOccurrence.get(
                                occurrence.occurrenceId,
                              )?.length ?? 0) < 2
                            }
                            onChange={(event) =>
                              onChangeEdge(
                                occurrence.occurrenceId,
                                event.currentTarget.value,
                              )
                            }
                          >
                            {(
                              editableEdgesByOccurrence.get(
                                occurrence.occurrenceId,
                              ) ?? []
                            ).map((edge) => (
                              <option key={edge.edgeId} value={edge.edgeId}>
                                {edge.label}
                              </option>
                            ))}
                          </select>
                          <button
                            type="button"
                            aria-label={`${occurrence.matchMode === 'REQUIRED' ? 'Keep' : 'Require'} ${occurrence.resourceType} match`}
                            title={occurrence.matchMode === 'REQUIRED' ? 'Keep rows even when this relationship has no match' : 'Keep only rows with a match for this relationship'}
                            className="block w-full border-t border-current/20 px-2 py-1 text-left text-[9px] font-semibold"
                            disabled={disabled || !canEditRelationships}
                            onClick={() =>
                              (imported ? importedRelationshipActions?.onChangeMatchMode : onChangeMatchMode)?.(
                                occurrence.occurrenceId,
                                occurrence.matchMode === 'REQUIRED'
                                  ? 'OPTIONAL'
                                  : 'REQUIRED',
                              )
                            }
                          >
                            {occurrence.matchMode === 'REQUIRED'
                              ? 'Required match'
                              : 'Optional feature'}
                          </button>
                          {occurrence.depth === 1 &&
                            catalogNodeById.get(occurrence.nodeId)
                              ?.rowRootEligible ? (
                              <button
                                type="button"
                                aria-label={`Make each ${occurrence.resourceType} one row`}
                                title={`Use ${occurrence.resourceType} as the row start`}
                                className="block w-full border-t border-current/20 px-2 py-1 text-left text-[9px] font-semibold"
                                disabled={disabled || Boolean(table?.document.importedRecipe)}
                                onClick={() => onChangeBase(occurrence.nodeId)}
                              >
                                Make rows
                              </button>
                            ) : null}
                        </>
                      )}
                    </div>
                    {occurrence.parentId && (
                      <button
                        type="button"
                        aria-label={`Remove ${occurrence.resourceType} branch`}
                        title={`Remove ${occurrence.resourceType} branch`}
                        className="rounded-full px-1.5 py-0.5 text-base leading-none text-blue-500 hover:bg-blue-100 hover:text-red-600"
                        disabled={disabled || !canEditRelationships}
                        onClick={() => imported ? importedRelationshipActions?.onRemove(occurrence.occurrenceId) : onTruncate(occurrence.occurrenceId)}
                      >
                        ×
                      </button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </nav> : null}
        </div>
        {showSelectionDetails && compact && selectedOccurrence ? (
          <div className="mt-2 shrink-0 rounded border border-slate-200 bg-white px-2 py-1.5 text-xs" aria-label="Selected relationship inspector">
            <p className="font-semibold text-slate-900">{selectedOccurrence.resourceType} · {!selectedOccurrence.parentId ? 'Starting records' : selectedOccurrence.relationship || 'Relationship label unavailable'}</p>
            <p className="mt-0.5 text-slate-600">{!selectedOccurrence.parentId ? 'Source row identity is shown above; later row operations can change the current grain.' : selectedOccurrence.matchMode === 'REQUIRED' ? selectedPathRequiresMatches ? 'Require a match: unmatched source rows are excluded. Matching records supply values without expanding rows.' : 'Require a match within this optional parent path. Related values can be excluded while source rows stay. This does not expand rows.' : 'Optional values: keep source rows without a match. Multiple matches do not request row expansion.'}</p>
            <details className="mt-1"><summary className="cursor-pointer text-slate-700">Relationship details</summary><p className="break-all font-mono">Occurrence: {selectedOccurrence.occurrenceId}{selectedOccurrence.incomingEdgeId ? ` · Catalog edge: ${selectedOccurrence.incomingEdgeId}` : ''}</p><p>Multiplicity: unknown. Preview observations do not prove one-to-one cardinality.</p></details>
          </div>
        ) : null}
      </section>
    </>
  );
  return isExpanded && typeof document !== 'undefined' ? createPortal(content, document.body) : content;
};
