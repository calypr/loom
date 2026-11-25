import React from 'react';
import { createPortal } from 'react-dom';
import { GuidedGraphWorkspace } from './GuidedGraphWorkspace';
import { replaceableIncomingEdges } from '../authoring/routeActions';
import { authoredTraversal } from './authoredTraversal';

type GraphProps = React.ComponentProps<typeof GuidedGraphWorkspace>;
type AuthoredTraversalEditorProps = GraphProps & {
  readonly projectionKind: 'saved' | 'proposed';
  readonly network?: import('../../../contracts/types').AuthoringNetworkResponse['network'];
  readonly rowIdentity?: import('../../../contracts/types').AuthoringNetworkResponse['rowIdentity'];
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
  const incomingEdges = selected && !table?.document.importedRecipe ? replaceableIncomingEdges(catalog, table, selected.id) : [];
  const canWriteRelationship = !table?.document.importedRecipe || Boolean(props.importedRelationshipActions);
  const showStartingRecords = Boolean(selected && !selected.parentId && !props.onDefineRow && props.onEditStartingRecords);
  const selectedInspector = selected && props.showInspector !== false && (selected.parentId || showStartingRecords || blockedReason) ? (
    <section aria-label="Selected recipe fact" data-testid="authored-occurrence-inspector" data-occurrence-id={selected.id} className="min-w-0 rounded border border-blue-200 px-2 py-1 text-xs">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        {selected.parentId && incomingEdges.length > 1 ? <select aria-label={`Relationship for ${selected.path}`} className="rounded border border-slate-300 bg-white px-1 py-0.5 text-xs" value={selected.edgeId ?? ''} disabled={blocked}
          onChange={event => { if (!blocked && incomingEdges.some(edge => edge.edgeId === event.currentTarget.value)) props.onChangeEdge(selected.id, event.currentTarget.value); }}>
          {incomingEdges.map(edge => <option key={edge.edgeId} value={edge.edgeId}>{edge.label}</option>)}
        </select> : null}
        {selected.parentId ? <div className="flex w-full min-w-0 flex-wrap items-start gap-x-1.5 gap-y-1">
          <label className="flex min-w-0 flex-1 items-start gap-1.5">
            <input type="checkbox" className="mt-0.5 shrink-0" aria-label={`Match policy for ${selected.path}`} checked={selected.matchMode === 'OPTIONAL'} disabled={blocked || !canWriteRelationship}
              onChange={(event) => changeMatch(event.currentTarget.checked ? 'OPTIONAL' : 'REQUIRED')} />
            <span className="min-w-0 whitespace-normal [overflow-wrap:anywhere]">Keep rows with no match on {selected.relationship ?? selected.resourceType}</span>
          </label>
          <span tabIndex={0} aria-label="Match policy details" className="group relative inline-flex shrink-0 cursor-help items-center text-slate-500 outline-none">
            <span aria-hidden="true">ⓘ</span>
            <span role="tooltip" className="pointer-events-none absolute bottom-full left-1/2 z-30 mb-1 w-64 max-w-[min(16rem,calc(100vw-2rem))] -translate-x-1/2 rounded border border-slate-300 bg-white p-2 text-left text-[11px] font-normal leading-snug text-slate-700 opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100 group-focus:opacity-100">
              OPTIONAL keeps rows when this relationship has no match; REQUIRED keeps only matched rows. It is a semi-join from the nearest required ancestor, so a missing optional parent branch leaves the row eligible. It never expands rows.
            </span>
          </span>
        </div> : null}
        {showStartingRecords ? <button type="button" className="rounded border px-2 py-0.5 text-xs" disabled={blocked} onClick={props.onEditStartingRecords ?? props.onDefineRow}>Change starting records</button> : null}
      </div>
      {blockedReason ? <p role="note" className="mt-1 text-xs text-slate-600">{blockedReason}</p> : null}
    </section>
  ) : null;
  const inspector = selectedInspector
    ? props.inspectorHost === undefined ? selectedInspector : props.inspectorHost ? createPortal(selectedInspector, props.inspectorHost) : null
    : null;
  return <div data-testid="authored-traversal-editor" data-projection-kind={projectionKind} className="flex h-full min-h-0 min-w-0 flex-col gap-3">
    <GuidedGraphWorkspace {...props} presentation="traversal" showSelectionDetails={false}
      headerStatus={projectionKind === 'proposed' ? 'Proposed' : 'Saved'}
      headerPath={selected?.path}
      disabled={blocked} />
    {inspector}
  </div>;
}
