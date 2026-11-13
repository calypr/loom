import React, { useState } from 'react';
import { createPortal } from 'react-dom';
import { Background, Controls, MarkerType, ReactFlow } from '@xyflow/react';
import type { Edge, Node } from '@xyflow/react';
import type { AuthoringNetworkResponse } from '../../../types';
import { GuidedGraphWorkspace } from './GuidedGraphWorkspace';
import { authoredTraversal } from './authoredTraversal';

type GraphProps = React.ComponentProps<typeof GuidedGraphWorkspace>;
export type AuthoredTraversalEditorProps = GraphProps & {
  readonly projectionKind: 'saved' | 'proposed';
  readonly network?: AuthoringNetworkResponse['network'];
  readonly rowIdentity?: AuthoringNetworkResponse['rowIdentity'];
  readonly onEditSourceStep?: (sourceStepId: string) => void;
  readonly onInspectValues?: (occurrenceId: string) => void;
  readonly onInspectRelatedRows?: (occurrenceId: string) => void;
  readonly onInspectOperation?: (operationId: string) => void;
  readonly onDefineRow?: () => void;
  readonly onEditStartingRecords?: () => void;
  readonly showOccurrenceNavigation?: boolean;
  readonly mutationBlockedReason?: string;
  readonly showInspector?: boolean;
  readonly inspectorHost?: HTMLElement | null;
  readonly catalogOpen?: boolean;
  readonly onCatalogOpenChange?: (open: boolean) => void;
};

/** Both views render the supplied saved/candidate document. Selection is only inspection. */
export function AuthoredTraversalEditor(props: AuthoredTraversalEditorProps) {
  const { table, catalog, projectionKind, selectedOccurrenceId, onSelectOccurrence } = props;
  const [localCatalogOpen, setLocalCatalogOpen] = useState(false);
  const catalogOpen = props.catalogOpen ?? localCatalogOpen;
  if (!table) return <GuidedGraphWorkspace {...props} />;
  const occurrences = authoredTraversal(table, catalog);
  const selected = occurrences.find((item) => item.id === selectedOccurrenceId);
  const positions = new Map<string, { x: number; y: number }>();
  let nextLeaf = 0;
  const position = (id: string, visiting = new Set<string>()): number => {
    const found = positions.get(id);
    if (found) return found.y;
    if (visiting.has(id)) return nextLeaf++ * 120;
    const occurrence = occurrences.find((item) => item.id === id);
    if (!occurrence) return nextLeaf++ * 120;
    const seen = new Set(visiting); seen.add(id);
    const children = occurrences.filter((item) => item.parentId === id);
    const rows = children.map((child) => position(child.id, seen));
    const y = rows.length ? (Math.min(...rows) + Math.max(...rows)) / 2 : nextLeaf++ * 120;
    positions.set(id, { x: occurrence.depth * 260, y });
    return y;
  };
  occurrences.filter((item) => !item.parentId).forEach((item) => position(item.id));
  const blocked = props.disabled || projectionKind === 'proposed';
  const blockedReason = props.mutationBlockedReason ?? (projectionKind === 'proposed' ? 'Apply or Cancel the proposed change before starting another edit.' : undefined);
  const nodes: Node[] = occurrences.map((occurrence, index) => ({
    id: occurrence.id, position: positions.get(occurrence.id) ?? { x: occurrence.depth * 260, y: index * 120 },
    ariaLabel: `Authored ${occurrence.path}`, selected: occurrence.id === selectedOccurrenceId,
    data: { label: <div data-occurrence-id={occurrence.id} data-term-kind="occurrence" className="text-left text-sm">
      <strong>{occurrence.resourceType}</strong>
      <p className="text-xs">{occurrence.parentId ? occurrence.relationship : 'Starting records'}</p>
      <p className="text-xs">{occurrence.parentId ? occurrence.matchMode === 'REQUIRED' ? 'Match required' : 'Keep unmatched rows' : 'Starting scope'}</p>
    </div> },
    style: { width: 210, border: occurrence.id === selectedOccurrenceId ? '2px solid #7c3aed' : '1px solid #2563eb', background: projectionKind === 'proposed' ? '#fff7ed' : '#eff6ff', borderRadius: 8 },
  }));
  const edges: Edge[] = occurrences.flatMap((occurrence) => occurrence.parentId ? [{
    id: `authored:${occurrence.id}`, source: occurrence.parentId, target: occurrence.id,
    label: occurrence.relationship ?? 'Relationship unavailable', markerEnd: { type: MarkerType.ArrowClosed },
    data: { occurrenceId: occurrence.id, catalogEdgeId: occurrence.edgeId },
    ariaLabel: `Authored relationship ${occurrence.path}`,
    style: { stroke: '#2563eb', strokeWidth: 2 },
  }] : []);
  const changeMatch = (matchMode: 'OPTIONAL' | 'REQUIRED') => {
    if (!selected || blocked) return;
    if (table.document.importedRecipe) props.importedRelationshipActions?.onChangeMatchMode(selected.id, matchMode);
    else props.onChangeMatchMode(selected.id, matchMode);
  };
  const remove = () => {
    if (!selected || blocked) return;
    if (table.document.importedRecipe) props.importedRelationshipActions?.onRemove(selected.id);
    else props.onTruncate(selected.id);
  };
  const canWriteRelationship = !table.document.importedRecipe || Boolean(props.importedRelationshipActions);
  const selectedInspector = selected && props.showInspector !== false && !catalogOpen ? (
<section aria-label="Selected recipe fact" data-testid="authored-occurrence-inspector" data-occurrence-id={selected.id} className={`rounded border border-blue-200 p-2 text-xs${props.inspectorHost !== undefined ? ' h-full max-h-full min-h-0 overflow-auto' : ''}`}>
        <h3 className="font-semibold">{selected.path}</h3>
        {selected.parentId ? <div className="flex flex-wrap items-center gap-2">
          <label>When no record matches<select aria-label={`Match policy for ${selected.path}: ${selected.matchMode}`} value={selected.matchMode} disabled={blocked || !canWriteRelationship} onChange={(event) => changeMatch(event.currentTarget.value as 'OPTIONAL' | 'REQUIRED')}>
            <option value="OPTIONAL">Keep input rows</option><option value="REQUIRED">Require a match</option>
          </select></label>
          <button type="button" className="underline" disabled={blocked || !canWriteRelationship} onClick={remove}>Remove {selected.relationship ?? selected.resourceType} relationship</button>
        </div> : null}
        {!selected.parentId && (props.onEditStartingRecords || props.onDefineRow) ? <button type="button" className="mt-2 rounded border px-2 py-1" disabled={blocked} onClick={props.onEditStartingRecords ?? props.onDefineRow}>Change starting records</button> : null}
        {blockedReason ? <p role="note">{blockedReason}</p> : null}
        <details><summary>Relationship identity</summary><p>Occurrence: {selected.id} · catalog edge: {selected.edgeId ?? 'unavailable; no edge inferred'}</p>{table.document.importedRecipe && selected.parentId ? <p>In-place edge replacement is unavailable for pinned relationships. Add or remove a relationship through validated dependency review.</p> : null}</details>
      </section>
  ) : undefined;
  return <section aria-label="Record network" data-testid="authored-traversal-editor" data-projection-kind={projectionKind} className="authored-traversal flex min-h-0 min-w-0 flex-1 flex-col gap-2">
    <div className="flex items-center justify-between gap-2">
      <h2 className="text-sm font-semibold">Authored traversal</h2><span className="text-xs text-slate-500">{projectionKind === 'proposed' ? 'Proposed' : 'Saved'}</span>
      <button type="button" className="rounded border px-2 py-1 text-xs" aria-pressed={catalogOpen} onClick={() => { const open = !catalogOpen; setLocalCatalogOpen(open); props.onCatalogOpenChange?.(open); }}>{catalogOpen ? 'Return to traversal' : 'Add a connection'}</button>
    </div>
    {catalogOpen ? <GuidedGraphWorkspace {...props} disabled={blocked} showSelectionDetails={false} /> : <>
      <div className="min-h-40 min-w-0 flex-1 rounded border bg-slate-50" data-testid="authored-graph">
        <ReactFlow key={JSON.stringify(occurrences.map((occurrence) => [occurrence.id, occurrence.parentId, occurrence.edgeId]))} nodes={nodes} edges={edges} fitView fitViewOptions={{ padding: 0.15, maxZoom: 1 }} nodesDraggable={false} nodesConnectable={false} nodesFocusable edgesFocusable
          minZoom={0.1} maxZoom={2} proOptions={{ hideAttribution: true }}
          onNodeClick={(_, node) => onSelectOccurrence(node.id)}
          onEdgeClick={(_, edge) => { const occurrence = edges.find((item) => item.id === edge.id)?.data?.occurrenceId; if (typeof occurrence === 'string') onSelectOccurrence(occurrence); }}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' && event.key !== ' ') return;
            const target = event.target instanceof Element ? event.target : undefined;
            const nodeId = target?.closest('.react-flow__node')?.getAttribute('data-id');
            const edgeId = target?.closest('.react-flow__edge')?.getAttribute('data-id');
            const occurrenceId = nodeId ?? edges.find((edge) => edge.id === edgeId)?.data?.occurrenceId;
            if (typeof occurrenceId === 'string' && occurrences.some((item) => item.id === occurrenceId)) {
              event.preventDefault(); onSelectOccurrence(occurrenceId);
            }
          }}><Background /><Controls showInteractive={false} /></ReactFlow>
      </div>
      {props.showOccurrenceNavigation !== false ? <nav aria-label="Authored occurrences" className="flex max-h-20 flex-wrap gap-1 overflow-auto">
        {occurrences.map((occurrence) => <button key={occurrence.id} type="button" aria-pressed={occurrence.id === selectedOccurrenceId}
          data-occurrence-id={occurrence.id} data-edge-id={occurrence.edgeId} data-term-kind="connection"
          className="rounded border px-2 py-1 text-left text-xs" onClick={() => onSelectOccurrence(occurrence.id)}>{occurrence.path}</button>)}
      </nav> : null}
      {selectedInspector ? (props.inspectorHost === undefined ? selectedInspector : props.inspectorHost ? createPortal(selectedInspector, props.inspectorHost) : null) : !selected ? <p className="text-xs">Select an authored path to inspect its policies.</p> : null}
    </>}
  </section>;
}
