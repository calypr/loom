import React, { useState } from 'react';
import { createPortal } from 'react-dom';
import { Background, Controls, MarkerType, ReactFlow } from '@xyflow/react';
import type { Edge, Node } from '@xyflow/react';
import type { AuthoringNetworkResponse } from '../../../types';
import { GuidedGraphWorkspace } from './GuidedGraphWorkspace';
import { authoredRowMeaning, authoredStages, authoredTraversal } from './authoredTraversal';

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
  readonly mutationBlockedReason?: string;
  readonly showInspector?: boolean;
  readonly inspectorHost?: HTMLElement | null;
  readonly catalogOpen?: boolean;
  readonly onCatalogOpenChange?: (open: boolean) => void;
};

/** Both views render the supplied saved/candidate document. Selection is only inspection. */
export function AuthoredTraversalEditor(props: AuthoredTraversalEditorProps) {
  const { table, catalog, projectionKind, selectedOccurrenceId, onSelectOccurrence, network, rowIdentity } = props;
  const [localCatalogOpen, setLocalCatalogOpen] = useState(false);
  const catalogOpen = props.catalogOpen ?? localCatalogOpen;
  if (!table) return <GuidedGraphWorkspace {...props} />;
  const occurrences = authoredTraversal(table, catalog);
  const selected = occurrences.find((item) => item.id === selectedOccurrenceId);
  const selectedBinding = network?.bindings.filter((binding) => binding.occurrenceId === selected?.id) ?? [];
  const stages = authoredStages(table, rowIdentity);
  const blocked = props.disabled || projectionKind === 'proposed';
  const blockedReason = props.mutationBlockedReason ?? (projectionKind === 'proposed' ? 'Apply or Cancel the proposed change before starting another edit.' : undefined);
  const nodes: Node[] = occurrences.map((occurrence, index) => ({
    id: occurrence.id, position: { x: occurrence.depth * 240, y: index * 94 },
    ariaLabel: `Authored ${occurrence.path}`, selected: occurrence.id === selectedOccurrenceId,
    data: { label: <div data-occurrence-id={occurrence.id} data-term-kind="occurrence" className="text-left">
      <strong>{occurrence.resourceType}</strong>
      <p className="text-[10px]">{occurrence.parentId ? occurrence.relationship : 'Starting records'}</p>
      <p className="text-[10px]">{occurrence.parentId ? occurrence.matchMode === 'REQUIRED' ? 'Require a match · rows may be excluded' : 'Optional match · row identity preserved' : 'Source traversal'}</p>
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
        <p>Source stage · {selected.parentId ? selected.matchMode === 'REQUIRED' ? 'A required match may exclude input rows; it does not expand them.' : 'Optional related values preserve the input row identity.' : 'Starting record occurrence.'}</p>
        {selected.parentId ? <div className="flex flex-wrap items-center gap-2">
          <label>Match policy for {selected.path}<select aria-label={`Match policy for ${selected.path}: ${selected.matchMode}`} value={selected.matchMode} disabled={blocked || !canWriteRelationship} onChange={(event) => changeMatch(event.currentTarget.value as 'OPTIONAL' | 'REQUIRED')}>
            <option value="OPTIONAL">Optional · keep unmatched rows</option><option value="REQUIRED">Required · exclude unmatched rows</option>
          </select></label>
          <button type="button" className="underline" disabled={blocked || !canWriteRelationship} onClick={remove}>Remove {selected.relationship ?? selected.resourceType} relationship</button>
        </div> : null}
        {selectedBinding.length ? <p>Bound values: {selectedBinding.map((binding) => `${table.document.columns.find((column) => (column.columnId ?? column.column) === binding.columnId)?.label ?? binding.columnId}: ${binding.valueMode ?? 'configured source policy'}`).join('; ')}.</p> : null}
        <div aria-label="Shared source policies" className="max-h-24 overflow-auto">
        {selectedBinding.filter((binding) => binding.sourceStepId).map((binding) => {
          const column = table.document.columns.find((item) => (item.columnId ?? item.column) === binding.columnId);
          return <button key={binding.columnId} data-testid={`shared-source-policy-${binding.columnId}`} type="button" className="mr-2 underline" disabled={blocked || !props.onEditSourceStep}
            onClick={() => { if (binding.sourceStepId) props.onEditSourceStep?.(binding.sourceStepId); }}>
            Edit shared source policy for {column?.label ?? binding.columnId}
          </button>;
        })}
        </div>
        {selectedBinding.some((binding) => binding.sourceStepId) ? <p>The policy belongs to the shared source step and affects all columns produced by that step. Review every affected value before Apply.</p> : null}
        <div className="mt-1 flex flex-wrap gap-2">
          {props.onInspectValues ? <button type="button" disabled={blocked} className="underline" onClick={() => props.onInspectValues?.(selected.id)}>Add related values from {selected.resourceType}</button> : null}
          {props.onInspectRelatedRows ? <button type="button" disabled={blocked} className="underline" onClick={() => props.onInspectRelatedRows?.(selected.id)}>Define related-record rows from {selected.resourceType}</button> : null}
          {props.onDefineRow ? <button type="button" className="underline" onClick={props.onDefineRow}>Open Define a row</button> : null}
        </div>
        <p className="mt-1 text-slate-600">Value and row actions choose an explicit construction input and anchor in their inspector. Selecting this path does not change that anchor.</p>
        {blockedReason ? <p role="note">{blockedReason}</p> : null}
        <details><summary>Relationship identity</summary><p>Occurrence: {selected.id} · catalog edge: {selected.edgeId ?? 'unavailable; no edge inferred'}</p>{table.document.importedRecipe && selected.parentId ? <p>In-place edge replacement is unavailable for pinned relationships. Add or remove a relationship through validated dependency review.</p> : null}</details>
      </section>
  ) : undefined;
  return <section aria-label="Record network" data-testid="authored-traversal-editor" data-projection-kind={projectionKind} className="min-w-0 space-y-2">
    <div className="flex items-center justify-between gap-2">
      <h2 className="font-semibold">Record network · {projectionKind === 'proposed' ? 'Proposed recipe' : 'Saved recipe'}</h2>
      <button type="button" className="rounded border px-2 py-1 text-xs" aria-pressed={catalogOpen} onClick={() => { const open = !catalogOpen; setLocalCatalogOpen(open); props.onCatalogOpenChange?.(open); }}>{catalogOpen ? 'Show authored traversal' : 'Explore available connections'}</button>
    </div>
    <p aria-live="polite" className="text-xs text-violet-950">{props.rowMeaning ?? authoredRowMeaning(table)}</p>
    {catalogOpen ? <GuidedGraphWorkspace {...props} disabled={blocked} /> : <>
      <div className="h-52 rounded border bg-slate-50" data-testid="authored-graph">
        <ReactFlow key={JSON.stringify(occurrences.map((occurrence) => [occurrence.id, occurrence.parentId, occurrence.edgeId]))} nodes={nodes} edges={edges} fitView nodesDraggable={false} nodesConnectable={false} nodesFocusable edgesFocusable
          minZoom={0.1} maxZoom={2} proOptions={{ hideAttribution: true }}
          onNodeClick={(_, node) => onSelectOccurrence(node.id)}
          onEdgeClick={(_, edge) => { const occurrence = edges.find((item) => item.id === edge.id)?.data?.occurrenceId; if (typeof occurrence === 'string') onSelectOccurrence(occurrence); }}
          onKeyDown={(event) => {
            if (event.key !== 'Enter' && event.key !== ' ') return;
            const id = event.target instanceof Element ? event.target.closest('.react-flow__node')?.getAttribute('data-id') : undefined;
            if (id && occurrences.some((item) => item.id === id)) { event.preventDefault(); onSelectOccurrence(id); }
          }}><Background /><Controls showInteractive={false} /></ReactFlow>
      </div>
      <nav aria-label="Authored occurrences" className="flex max-h-20 flex-wrap gap-1 overflow-auto">
        {occurrences.map((occurrence) => <button key={occurrence.id} type="button" aria-pressed={occurrence.id === selectedOccurrenceId}
          data-occurrence-id={occurrence.id} data-edge-id={occurrence.edgeId} data-term-kind="connection"
          className="rounded border px-2 py-1 text-left text-xs" onClick={() => onSelectOccurrence(occurrence.id)}>{occurrence.path}</button>)}
      </nav>
      {selectedInspector ? (props.inspectorHost === undefined ? selectedInspector : props.inspectorHost ? createPortal(selectedInspector, props.inspectorHost) : null) : !selected ? <p className="text-xs">Select an authored path to inspect its policies.</p> : null}
    </>}
    {stages.length ? <details className="rounded border p-2 text-xs" open={stages.some((stage) => stage.rowChanging)}>
      <summary>Row and value stages · {stages.length} ordered operations</summary>
      <ol aria-label="Traversal row stages" className="max-h-36 overflow-auto">
        {stages.map((stage) => <li key={stage.id} className="mt-2" data-term-kind="operation" data-operation-id={stage.id} data-stage-id={stage.id}>
          <button type="button" disabled={!props.onInspectOperation || blocked} className="font-semibold underline" onClick={() => props.onInspectOperation?.(stage.id)}>{stage.index + 1}. {stage.title}</button>
          <p>Input: {stage.input} {stage.anchorColumnId ? `· anchor: ${stage.anchorColumnId}` : ''}</p>
          {stage.traversal ? <p data-term-kind="stage-traversal" data-operation-id={stage.id}>Authored stage traversal: {stage.traversal}</p> : null}
          <p>{stage.summary}</p>
          {stage.meaning ? <p>{stage.meaning}</p> : null}
          {stage.missingPolicy ? <p>Missing or empty policy: {stage.missingPolicy}</p> : null}
          {stage.listPreservation ? <p>Each output row is an input/related-record pair. Existing list columns retain their identities and list policies and repeat unchanged on each pair. A per-match scalar requires a separately selected column.</p> : null}
          {stage.edgeIds?.length ? <details><summary>Stage relationship identities</summary>{stage.edgeIds.join(' → ')}</details> : null}
          {stage.unsupportedReason ? <p role="alert">{stage.unsupportedReason}</p> : null}
        </li>)}
      </ol>
    </details> : null}
  </section>;
}
