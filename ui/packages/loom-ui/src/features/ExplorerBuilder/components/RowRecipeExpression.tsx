import { useId, useState } from 'react';
import type { AuthoringNetworkResponse, ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { constructionRowMeaning } from '../constructionWorkspace/constructionHistory';
import { legalOutgoingEdges } from '../authoring/routeActions';
import { columnPolicy, expressionColumnLabels, operationExpression, rowConnectionTerms, sourceRowExpression } from './rowRecipeProjection';

export interface RowRecipeExpressionProps {
  /** The complete saved document or validated candidate document, selected by the shared owner. */
  readonly table: DraftTable;
  readonly catalog: ExplorerBuilderCatalog;
  readonly network?: AuthoringNetworkResponse['network'];
  readonly projectionKind: 'saved' | 'proposed';
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly onEditSourceRows: () => void;
  readonly onAddConnection: (parentOccurrenceId: string, edgeId: string, nodeId: string) => void;
  readonly onSetMatchMode: (occurrenceId: string, mode: 'OPTIONAL' | 'REQUIRED') => void;
  readonly onEditColumn: (columnId: string) => void;
  readonly onEditImportedSourceStep: (stepId: string) => void;
  readonly onEditOperation: (stepId: string) => void;
  readonly onAddRelatedValues: () => void;
  readonly onAddRelatedRows: () => void;
  readonly onAddGroup: () => void;
}

const editClass = 'rounded border border-slate-300 bg-white px-2 py-1 text-left text-blue-900 disabled:opacity-50';

/** An editable projection, never an authored expression AST or another proposal owner. */
export function RowRecipeExpression(props: RowRecipeExpressionProps) {
  const { table, catalog, network, projectionKind, disabled = false } = props;
  const terms = rowConnectionTerms(table.document.route);
  const labels = expressionColumnLabels(table.document);
  const [pickerParent, setPickerParent] = useState('');
  const [pickerEdge, setPickerEdge] = useState('');
  const [columnSearch, setColumnSearch] = useState('');
  const pickerId = useId();
  const parent = terms.find((term) => term.occurrenceId === pickerParent) ?? terms[0];
  const edges = parent ? legalOutgoingEdges(catalog, table, parent.occurrenceId) : [];
  // A presentation/table switch never reuses an edge selected for a different parent.
  const edge = edges.find((candidate) => candidate.edgeId === pickerEdge);
  const columns = table.document.columns.filter((column) => `${column.label} ${column.column}`.toLowerCase().includes(columnSearch.toLowerCase()));
  const steps = table.document.construction?.steps ?? [];
  const currentRowMeaning = constructionRowMeaning(sourceRowExpression(table.document), table.document.construction,
    table.document.columns.map((column) => ({ id: column.columnId ?? column.column, name: column.column, label: column.label, type: column.logicalType })));
  return <section aria-label="Define a row" data-testid="row-recipe-expression" data-recipe-state={projectionKind}
    className="min-w-0 space-y-2 rounded border border-slate-200 bg-slate-50 p-2 text-xs">
    <div className="flex items-center justify-between gap-2"><h3 className="font-semibold">Define a row</h3>
      <span>{projectionKind === 'proposed' ? 'Proposed recipe' : 'Saved recipe'}</span></div>
    <p aria-label="Current row meaning" className="font-medium">{currentRowMeaning}</p>
    {props.disabledReason ? <p role="status">{props.disabledReason}</p> : null}
    <div data-term-kind="source-rows" data-stage-id="source_projection" className="flex flex-wrap items-center gap-2">
      <strong>Source rows:</strong>
      <button type="button" className={editClass} disabled={disabled} onClick={props.onEditSourceRows}
        aria-label={`Edit source rows: ${sourceRowExpression(table.document)}`}>{sourceRowExpression(table.document)}</button>
    </div>
    <div><strong>Connected records:</strong>
      <ul aria-label="Row recipe connections" className="max-h-28 space-y-1 overflow-auto">
        {terms.map((term) => <li key={term.occurrenceId} data-term-kind="connection" data-occurrence-id={term.occurrenceId}
          data-stage-id="source_projection" className="flex flex-wrap items-center gap-2">
          <span>{term.path}{!term.parentOccurrenceId ? ' (starting records)' : ''}</span>
          {term.parentOccurrenceId ? <label className="flex items-center gap-1">
            <span>When missing:</span>
            <select aria-label={`Match policy for ${term.path}: ${term.matchMode}`} value={term.matchMode} disabled={disabled}
              onChange={(event) => props.onSetMatchMode(term.occurrenceId, event.target.value as 'OPTIONAL' | 'REQUIRED')}>
              <option value="OPTIONAL">Keep input rows</option><option value="REQUIRED">Require a match; exclude unmatched rows</option>
            </select>
          </label> : null}
          <details><summary className="cursor-pointer text-slate-500">Identity</summary>
            <p>Occurrence: {term.occurrenceId}{term.catalogEdgeId ? ` · edge: ${term.catalogEdgeId}` : ''}</p></details>
        </li>)}
      </ul>
      <details className="mt-1" data-testid="row-connection-picker"><summary className="cursor-pointer font-semibold">Connect records</summary>
        <div className="space-y-1 rounded border bg-white p-2">
          <label htmlFor={`${pickerId}-parent`}>Connect from</label>
          <select id={`${pickerId}-parent`} aria-label="Connect from occurrence" value={parent?.occurrenceId ?? ''} disabled={disabled}
            onChange={(event) => { setPickerParent(event.target.value); setPickerEdge(''); }}>
            {terms.map((term) => <option key={term.occurrenceId} value={term.occurrenceId}>{term.path}</option>)}
          </select>
          <label htmlFor={`${pickerId}-edge`}>Relationship</label>
          <select id={`${pickerId}-edge`} aria-label="Connection relationship" value={edge?.edgeId ?? ''} disabled={disabled || !edges.length}
            onChange={(event) => setPickerEdge(event.target.value)}>
            <option value="">Choose a supported connection</option>
            {edges.map((candidate) => <option key={candidate.edgeId} value={candidate.edgeId}>{candidate.label} → {catalog.nodes.find((node) => node.nodeId === candidate.toNodeId)?.resourceType ?? 'Unknown record type'}</option>)}
          </select>
          {!edges.length ? <p>No additional supported connection leaves this occurrence.</p> : null}
          <button type="button" className={editClass} disabled={disabled || !parent || !edge}
            onClick={() => { if (parent && edge) props.onAddConnection(parent.occurrenceId, edge.edgeId, edge.toNodeId); }}>Preview connection</button>
          <p>Optional connections preserve row identity. Requiring a match may remove rows; expansion is a separate choice.</p>
        </div>
      </details>
    </div>
    <div><strong>Include column values:</strong>
      <label className="ml-2">Find column <input aria-label="Find row recipe column" className="w-36 rounded border" value={columnSearch} onChange={(event) => setColumnSearch(event.target.value)} /></label>
      <ul aria-label="Row recipe column policies" className="max-h-28 space-y-1 overflow-auto">
        {columns.map((column) => {
          const id = column.columnId ?? column.column;
          const policy = columnPolicy(column, network);
          const binding = network?.bindings.find((candidate) => candidate.columnId === id && candidate.status === 'EXACT');
          const occurrenceId = column.source.kind === 'imported' ? binding?.occurrenceId : column.occurrenceId;
          const path = terms.find((term) => term.occurrenceId === occurrenceId)?.path;
          const legacyIdentityUnavailable = column.source.kind !== 'imported' && !column.columnId;
          const canEdit = !legacyIdentityUnavailable && (column.source.kind !== 'imported' || !!policy.sourceStepId);
          return <li key={id} data-term-kind="column" data-column-id={id} data-occurrence-id={occurrenceId} data-stage-id="source_projection">
            <button type="button" data-testid={policy.sourceStepId ? `shared-source-policy-row-${id}` : undefined} className={editClass} disabled={disabled || !canEdit}
              aria-label={`${policy.sourceStepId ? 'Edit shared source policy for' : 'Edit column'} ${column.label}: ${policy.label}`}
              onClick={() => policy.sourceStepId ? props.onEditImportedSourceStep(policy.sourceStepId) : props.onEditColumn(id)}>
              {column.label}: {policy.label}
            </button> {legacyIdentityUnavailable ? <span className="text-slate-600">Legacy column: no stable column identity. Its saved source policy is shown; use the existing source settings for this column.</span> : null} {policy.sourceStepId ? <span className="text-slate-600">Shared source-step policy; editing affects all columns from this step.</span> : null} {path ? <span className="text-slate-600">from {path}</span> : null}
          </li>;
        })}
        {!columns.length ? <li>{columnSearch ? 'No matching columns.' : 'No source columns selected.'}</li> : null}
      </ul>
      <button type="button" className={`${editClass} mt-1`} disabled={disabled} onClick={props.onAddRelatedValues}>Include related values</button>
    </div>
    <div><strong>Ordered row and value operations:</strong>
      <ol aria-label="Row recipe operations" className="max-h-40 space-y-1 overflow-auto">
        {steps.map((step, index) => {
          const expression = operationExpression(step, labels);
          const inputStage = step.inputs.map((input) => input.kind === 'STEP_OUTPUT' ? input.stepId : input.kind === 'SOURCE_PROJECTION' ? 'Source' : input.outputId).join(', ');
          return <li key={step.id} data-term-kind="operation" data-operation-id={step.id} data-stage-id={step.id}
            className="rounded border border-slate-200 bg-white p-1">
            <button type="button" className={editClass} disabled={disabled} onClick={() => props.onEditOperation(step.id)}
              aria-label={`Edit operation ${index + 1}: ${expression.title}`}>{index + 1}. {expression.title}</button>
            <p className="text-slate-600">Input stage: {inputStage || 'Source'}</p>
            {expression.clauses.map((clause) => <p key={clause}>{clause}</p>)}
            <details><summary className="cursor-pointer text-slate-500">Operation identity</summary><p>{step.id}</p></details>
          </li>;
        })}
        {!steps.length ? <li>Source row identity; no construction operations.</li> : null}
      </ol>
      <div className="mt-1 flex flex-wrap gap-2">
        <button type="button" className={editClass} disabled={disabled} onClick={props.onAddRelatedRows}>Define related-record rows</button>
        <button type="button" className={editClass} disabled={disabled} onClick={props.onAddGroup}>Group by key tuple</button>
      </div>
    </div>
  </section>;
}
