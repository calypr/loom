import React from 'react';
import { createPortal } from 'react-dom';
import { GuidedGraphWorkspace } from './GuidedGraphWorkspace';
import { replaceableIncomingEdges } from '../authoring/routeActions';
import { authoredTraversal } from './authoredTraversal';

type GraphProps = React.ComponentProps<typeof GuidedGraphWorkspace>;
export type AuthoredTraversalEditorProps = GraphProps & {
  readonly projectionKind: 'saved' | 'proposed';
  readonly network?: import('../../../types').AuthoringNetworkResponse['network'];
  readonly rowIdentity?: import('../../../types').AuthoringNetworkResponse['rowIdentity'];
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
};

/** The graph selects occurrences; the contextual inspector owns their edits. */
export function AuthoredTraversalEditor(props: AuthoredTraversalEditorProps) {
  const { table, catalog, projectionKind, selectedOccurrenceId } = props;
  const occurrences = table ? authoredTraversal(table, catalog) : [];
  const selected = occurrences.find((item) => item.id === selectedOccurrenceId);
  const blocked = props.disabled || projectionKind === 'proposed';
  const blockedReason = props.mutationBlockedReason ?? (projectionKind === 'proposed'
    ? 'Apply or Cancel the proposed change before starting another edit.'
    : undefined);
  const changeMatch = (matchMode: 'OPTIONAL' | 'REQUIRED') => {
    if (!selected || blocked) return;
    if (table?.document.importedRecipe) props.importedRelationshipActions?.onChangeMatchMode(selected.id, matchMode);
    else props.onChangeMatchMode(selected.id, matchMode);
  };
  const remove = () => {
    if (!selected || blocked) return;
    if (table?.document.importedRecipe) props.importedRelationshipActions?.onRemove(selected.id);
    else props.onTruncate(selected.id);
  };
  const incomingEdges = selected && !table?.document.importedRecipe ? replaceableIncomingEdges(catalog, table, selected.id) : [];
  const rowRootNode = selected?.depth === 1 && selected.catalogNodeId && !table?.document.importedRecipe
    ? catalog.nodes.find(node => node.nodeId === selected.catalogNodeId && node.rowRootEligible) : undefined;
  const canWriteRelationship = !table?.document.importedRecipe || Boolean(props.importedRelationshipActions);
  const selectedInspector = selected && props.showInspector !== false ? (
    <section aria-label="Selected recipe fact" data-testid="authored-occurrence-inspector" data-occurrence-id={selected.id} className="rounded border border-blue-200 p-3 text-sm">
      <h3 className="mb-3 break-words font-semibold">{selected.path}</h3>
      {selected.parentId ? <div className="flex flex-col items-stretch gap-3">
        {incomingEdges.length > 1 ? <label className="flex flex-col gap-1">Relationship
          <select aria-label={`Relationship for ${selected.path}`} className="w-full rounded border border-slate-300 bg-white px-2 py-2" value={selected.edgeId ?? ''} disabled={blocked}
            onChange={event => { if (!blocked && incomingEdges.some(edge => edge.edgeId === event.currentTarget.value)) props.onChangeEdge(selected.id, event.currentTarget.value); }}>
            {incomingEdges.map(edge => <option key={edge.edgeId} value={edge.edgeId}>{edge.label}</option>)}
          </select>
        </label> : null}
        <label className="flex flex-col gap-1">When no record matches
          <select aria-label={`Match policy for ${selected.path}: ${selected.matchMode}`} className="w-full rounded border border-slate-300 bg-white px-2 py-2" value={selected.matchMode} disabled={blocked || !canWriteRelationship} onChange={(event) => changeMatch(event.currentTarget.value as 'OPTIONAL' | 'REQUIRED')}>
            <option value="OPTIONAL">Keep input rows</option><option value="REQUIRED">Require a match</option>
          </select>
        </label>
        <button type="button" className="self-start text-left text-blue-800 underline" disabled={blocked || !canWriteRelationship} onClick={remove}>Remove {selected.relationship ?? selected.resourceType} relationship</button>
      </div> : null}
      {rowRootNode ? <button type="button" className="mt-3 rounded border px-3 py-2" disabled={blocked} onClick={() => { if (!blocked) props.onChangeBase(rowRootNode.nodeId); }}>Make each {selected.resourceType} one row</button> : null}
      {props.onDefineRow ? <button type="button" className="mt-3 rounded border px-3 py-2" disabled={blocked} onClick={() => { if (!blocked) props.onDefineRow?.(); }}>Change row definition</button> : null}
      {!props.onDefineRow && !selected.parentId && props.onEditStartingRecords ? <button type="button" className="mt-2 rounded border px-3 py-2" disabled={blocked} onClick={props.onEditStartingRecords ?? props.onDefineRow}>Change starting records</button> : null}
      {blockedReason ? <p role="note" className="mt-2 text-xs text-slate-600">{blockedReason}</p> : null}
      <details className="mt-3"><summary className="cursor-pointer">Relationship identity</summary><p className="mt-2 break-all text-xs">Occurrence: {selected.id} · catalog edge: {selected.edgeId ?? 'unavailable; no edge inferred'}</p>{table?.document.importedRecipe && selected.parentId ? <p className="mt-2 text-xs">In-place edge replacement is unavailable for pinned relationships. Add or remove a relationship through validated dependency review.</p> : null}</details>
    </section>
  ) : null;
  const inspector = selectedInspector
    ? props.inspectorHost === undefined ? selectedInspector : props.inspectorHost ? createPortal(selectedInspector, props.inspectorHost) : null
    : null;
  return <div data-testid="authored-traversal-editor" data-projection-kind={projectionKind} className="flex min-w-0 flex-col gap-3">
    <GuidedGraphWorkspace {...props} presentation="traversal" showSelectionDetails={false}
      headerStatus={projectionKind === 'proposed' ? 'Proposed' : 'Saved'}
      disabled={blocked} />
    {inspector}
  </div>;
}
