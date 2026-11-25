import type { ReactNode } from 'react';
import type { AuthoringNetworkResponse, ConstructionStageDescriptor } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { rowValuePolicyLabel } from './rowRecipeProjection';
import { traversalScopeModel, type ScopedOperation } from './traversalScopeModel';

export interface TraversalScopeNavigatorProps {
  readonly table: DraftTable;
  readonly network?: AuthoringNetworkResponse['network'];
  readonly rowIdentity?: AuthoringNetworkResponse['rowIdentity'];
  readonly constructionStages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly sourceOperationLabels?: ReadonlyMap<string, string>;
  readonly selectedOccurrenceId?: string;
  readonly showScopeNavigation?: boolean;
  readonly selectedOperation?: { readonly kind: 'source' | 'construction'; readonly stepId: string };
  readonly projectionKind: 'saved' | 'proposed';
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly sourceStepEditOnly?: boolean;
  readonly sourceOperationNavigationDisabled?: boolean;
  readonly resultActions?: ReactNode;
  readonly combineActions?: ReactNode;
  readonly showActions?: boolean;
  readonly onSelectScope: (occurrenceId: string) => void;
  readonly onEditSourceStep: (stepId: string) => void;
  readonly onEditSourceField?: (stepId: string, fieldId: string) => void;
  readonly onEditConstructionStep: (stepId: string) => void;
  readonly onRemoveSourceStep?: (stepId: string) => void;
  readonly onRemoveConstructionStep?: (stepId: string) => void;
  readonly onEditColumn: (columnId: string) => void;
  readonly onEditSourceRows: () => void;
  readonly onExpandRelatedRows: (occurrenceId: string) => void;
  readonly onGroupRows: (occurrenceId: string) => void;
}
const rowButtonClass = 'flex w-full min-w-0 items-start gap-1.5 rounded-md px-1 py-0.5 text-left text-xs text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50';
const scopeButtonClass = 'flex w-full min-w-0 items-center rounded-md px-2 py-1 text-left text-xs transition-colors disabled:opacity-50';
const secondaryActionClass = 'inline-flex items-center rounded-md bg-white px-2.5 py-1.5 text-xs font-medium text-slate-700 ring-1 ring-inset ring-slate-200 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50';
const summaryClass = 'flex cursor-pointer items-center gap-1 text-xs font-medium text-slate-500 transition-colors hover:text-slate-900';
export function TraversalScopeNavigator(props: TraversalScopeNavigatorProps) {
  const { table, selectedOccurrenceId, selectedOperation, projectionKind } = props;
  const model = traversalScopeModel(table.document, props.network, props.rowIdentity, props.constructionStages);
  const selectedScopes = model.scopes.filter(scope => scope.id === selectedOccurrenceId);
  const selected = selectedScopes.length === 1 ? selectedScopes[0] : undefined;
  const blocked = props.disabled || projectionKind === 'proposed';
  const sourceOperationBlocked = props.sourceOperationNavigationDisabled || (blocked && !props.sourceStepEditOnly);
  const blockedReason = props.disabledReason ?? (projectionKind === 'proposed' ? 'Apply or Cancel the proposed change before editing another operation.' : undefined);
  const editOperation = (operation: ScopedOperation) => operation.kind === 'source'
    ? props.onEditSourceStep(operation.id) : props.onEditConstructionStep(operation.id);
  const operationTitle = (operation: ScopedOperation) => operation.kind === 'source' ? props.sourceOperationLabels?.get(operation.id) ?? operation.title : operation.title;
  const operationItem = (operation: ScopedOperation, showScope = false, fieldGroup = false) => <li key={`${operation.kind}:${operation.position}:${operation.id}`}
    data-operation-id={operation.id} data-operation-kind={operation.kind} data-occurrence-id={operation.scopeId}
    data-source-plan-position={operation.kind === 'source' ? operation.position : undefined}
    data-construction-position={operation.kind === 'construction' ? operation.position : undefined} className="border-b border-slate-100/70 py-0.5 last:border-0">
    <div className="flex min-w-0 items-start gap-1">
      <button type="button" className={`${rowButtonClass} ${selectedOperation?.kind === operation.kind && selectedOperation.stepId === operation.id ? 'bg-blue-50 text-blue-950' : ''}`}
        disabled={(operation.kind === 'source' ? sourceOperationBlocked : blocked) || !operation.editable}
        aria-pressed={selectedOperation?.kind === operation.kind && selectedOperation.stepId === operation.id}
        aria-label={`Edit ${operation.kind === 'source' ? 'source operation' : 'table operation'} ${operation.position}: ${fieldGroup ? `Fields from ${selected?.resourceType}` : operationTitle(operation)}`}
        onClick={() => editOperation(operation)}>
        <span className="min-w-0 flex-1 font-medium leading-5">{fieldGroup ? `Fields from ${selected?.resourceType}` : operationTitle(operation)}</span>
      </button>
      {!fieldGroup && (operation.kind === 'source'
        ? operation.sourceStep?.editable && !['OUTPUT', 'FIELD_GROUP', 'EXPANSION', 'IDENTITY'].includes(operation.sourceStep.kind) && props.onRemoveSourceStep
        : props.onRemoveConstructionStep) ? (
        <button type="button" disabled={blocked || (operation.kind === 'source' && sourceOperationBlocked)}
          aria-label={`Remove ${operation.kind === 'source' ? 'source operation' : 'table operation'} ${operation.position}: ${operationTitle(operation)}`}
          onClick={() => (operation.kind === 'source' ? props.onRemoveSourceStep : props.onRemoveConstructionStep)?.(operation.id)}
          className="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-45">Remove</button>
      ) : null}
    </div>
    {showScope && (operation.scopeId || operation.scopeReason) ? <p className="text-xs leading-4 text-slate-600">{operation.scopeId ? model.scopes.find(scope => scope.id === operation.scopeId)?.path : operation.scopeReason}</p> : null}
    {operation.scopeReason && !showScope ? <p role="note" className="text-xs text-amber-900">{operation.scopeReason}</p> : null}
    {!operation.editable ? <p className="text-xs text-amber-900">This operation has no supported in-place editor in the current operation metadata.</p> : null}
  </li>;
  const activeSourceOperations = model.sourceOperations;
  const unresolvedSource = activeSourceOperations.filter(operation => !operation.scopeId && Boolean(operation.scopeReason));
  const unresolvedFields = model.columns.filter(column => column.unavailableReason);
  const scopedSourceOperations = selected ? activeSourceOperations.filter(operation => operation.scopeId === selected.id) : [];
  const scopeOperations = scopedSourceOperations;
  const ordinaryFields = selected && !table.document.importedRecipe ? model.columns.filter(column => column.scopeId === selected.id) : [];
  return <section aria-label="Traversal operations" data-testid="traversal-scope-navigator" data-recipe-state={projectionKind} className="min-h-0 min-w-0 space-y-2">
    {props.showScopeNavigation !== false ? <nav aria-label="Authored traversal scopes" className="max-h-32 overflow-auto rounded-lg bg-slate-50/80 p-1">
      <ul className="space-y-0.5">{model.scopes.map((scope, index) => <li key={`${scope.id}:${index}`} style={{ paddingLeft: `${Math.min(scope.depth, 8) * 8}px` }}>
        <button type="button" className={`${scopeButtonClass} ${selectedOccurrenceId === scope.id ? 'bg-white font-semibold text-blue-900 shadow-sm ring-1 ring-slate-200' : 'text-slate-600 hover:bg-white/80 hover:text-slate-900'}`} data-occurrence-id={scope.id} aria-pressed={selectedOccurrenceId === scope.id}
          onClick={() => props.onSelectScope(scope.id)}><span className="truncate">{scope.path}</span></button>
      </li>)}</ul>
    </nav> : null}
    {selected ? <>
      <h3 className="text-[13px] font-semibold tracking-tight text-slate-900">{selected.resourceType} operations</h3>
      {props.showActions !== false ? <div className="flex flex-wrap items-center gap-1.5" aria-label={`Actions for ${selected.resourceType}`}>
        {props.resultActions ? <div className="flex flex-wrap gap-1" aria-label={`Result operations for ${selected.resourceType}`}>{props.resultActions}</div> : null}
        <button type="button" className={secondaryActionClass} disabled={blocked} onClick={() => props.onExpandRelatedRows(selected.id)}>Expand related records into rows</button>
        <button type="button" className={secondaryActionClass} disabled={blocked} onClick={() => props.onGroupRows(selected.id)}>Group resulting rows</button>
        {selected.parentId === undefined && !table.document.importedRecipe ? <button type="button" className="rounded-md px-2 py-1.5 text-xs font-medium text-slate-500 transition-colors hover:bg-slate-50 hover:text-slate-900 disabled:opacity-50" disabled={blocked} onClick={props.onEditSourceRows}>Edit row definition</button> : null}
        {props.combineActions ? <div className="flex flex-wrap gap-1" aria-label={`Combine operations for ${selected.resourceType}`}>{props.combineActions}</div> : null}
      </div> : null}
      <div data-testid="traversal-recipe-steps">
        <h4 className="text-[11px] font-semibold uppercase tracking-wide text-slate-400">Recipe steps</h4>
        {scopeOperations.length ? <ol aria-label="Source operations in selected scope" className="list-decimal space-y-0 pl-5 marker:text-[10px] marker:font-semibold marker:text-slate-400">{scopeOperations.map(operation => operationItem(operation, false, operation.sourceStep?.kind === 'FIELD_GROUP'))}</ol> : null}
        {model.constructionOperations.length ? <ol start={scopeOperations.length + 1} aria-label="Ordered resulting-table operations" className="max-h-56 list-decimal space-y-0 overflow-auto pl-5 marker:text-[10px] marker:font-semibold marker:text-slate-400">{model.constructionOperations.map(operation => operationItem(operation, true))}</ol> : null}
        {!scopeOperations.length && !model.constructionOperations.length ? <p className="text-xs">No operations in this scope.</p> : null}
        {ordinaryFields.length ? <details className="mt-1"><summary className={summaryClass}>Source fields ({ordinaryFields.length})</summary><ul aria-label="Source fields in selected scope" className="max-h-48 overflow-auto">{ordinaryFields.map(column => <li key={column.id} data-column-id={column.id}>
          <button type="button" className={rowButtonClass} disabled={blocked} onClick={() => props.onEditColumn(column.id)}>Value policy for {column.label}</button>
        </li>)}</ul></details> : null}
      </div>
    </> : <p role="status" className="text-xs">{selectedOccurrenceId ? 'The selected occurrence is unavailable or ambiguous. Select an authored scope; no operation was retargeted.' : 'Select an authored scope to inspect its operations.'}</p>}
    {blockedReason ? <p role="note" className="text-xs text-amber-900">{blockedReason}</p> : null}
    {unresolvedSource.length || unresolvedFields.length ? <details>
      <summary className="flex cursor-pointer items-center gap-1 text-xs font-medium text-amber-800 hover:text-amber-950">Unresolved source scope ({unresolvedSource.length} operations, {unresolvedFields.length} fields)</summary>
      <ul aria-label="Unresolved source operations" className="max-h-48 overflow-auto">{unresolvedSource.map(operation => operationItem(operation))}</ul>
      {unresolvedFields.length ? <ul aria-label="Fields awaiting exact source scope" className="max-h-48 overflow-auto">{unresolvedFields.map(field => <li key={field.id} data-column-id={field.id} className="text-xs">{field.label}: {field.unavailableReason}</li>)}</ul> : null}
    </details> : null}
  </section>;
}
