import type { ExplorerBuilderCatalog } from '../../../contracts/types';

export const OCCURRENCE_NODE_WIDTH = 250;

export interface TraversalTreeLayoutItem {
  readonly id: string;
  readonly parentId?: string;
  readonly depth: number;
}

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

export function layoutDiscoveryRail(
  occurrences: ReadonlyArray<DiscoveryRailOccurrence>,
  positions: ReadonlyMap<string, DiscoveryRailPoint>,
  selectedOccurrenceId: string,
  busY: number,
  nodeWidth = OCCURRENCE_NODE_WIDTH,
  nodeHeight = 112,
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
