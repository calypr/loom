import type { ReactNode } from 'react';
import type { AuthoringNetworkResponse, ConstructionStageDescriptor } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { rowValuePolicyLabel } from './rowRecipeProjection';
import { traversalScopeModel, type ScopedOperation } from './traversalScopeModel';

/** View metadata for real authored fields; fieldId comes from the typed source-plan descriptor. */
export interface TraversalSourceField {
  readonly fieldId: string;
  readonly name: string;
  readonly label?: string;
  readonly valueMode?: string;
  readonly editable?: boolean;
}
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
  readonly onSelectScope: (occurrenceId: string) => void;
  readonly onEditSourceStep: (stepId: string) => void;
  readonly onEditSourceOutput?: (stepId: string, outputKey: string) => void;
  readonly onEditConstructionStep: (stepId: string) => void;
  readonly onEditColumn: (columnId: string) => void;
  readonly onEditSourceRows: () => void;
  readonly onExpandRelatedRows: (occurrenceId: string) => void;
  readonly onGroupRows: (occurrenceId: string) => void;
}
const buttonClass = 'rounded border border-slate-300 bg-white px-2 py-1 text-left text-xs text-blue-900 disabled:opacity-50';
type NetworkBinding = NonNullable<TraversalScopeNavigatorProps['network']>['bindings'][number];
type EditableOutputBinding = NetworkBinding & { readonly sourceStepId: string; readonly outputKey: string };
function isEditableOutputBinding(binding: NetworkBinding): binding is EditableOutputBinding {
  return binding.status === 'EXACT' && typeof binding.sourceStepId === 'string' &&
    'outputKey' in binding && typeof binding.outputKey === 'string' && binding.outputKey.length > 0;
}

/** Controlled navigation over the same saved/candidate recipe; never a second editing owner. */
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
  const operationItem = (operation: ScopedOperation, showScope = false, compact = false) => <li key={`${operation.kind}:${operation.position}:${operation.id}`}
    data-operation-id={operation.id} data-operation-kind={operation.kind} data-occurrence-id={operation.scopeId}
    data-source-plan-position={operation.kind === 'source' ? operation.position : undefined}
    data-construction-position={operation.kind === 'construction' ? operation.position : undefined} className="space-y-1 border-b border-slate-100 py-2">
    <button type="button" className={buttonClass} disabled={(operation.kind === 'source' ? sourceOperationBlocked : blocked) || !operation.editable}
      aria-pressed={selectedOperation?.kind === operation.kind && selectedOperation.stepId === operation.id}
      aria-label={`Edit ${operation.kind === 'source' ? 'source operation' : 'table operation'} ${operation.position}: ${operationTitle(operation)}`}
      onClick={() => editOperation(operation)}>
      <span className="font-medium">{operation.position}. {operationTitle(operation)}</span>{!operation.enabled ? ' · disabled' : ''}
    </button>
    {showScope ? <p className="text-xs text-slate-600">{operation.scopeId ? model.scopes.find(scope => scope.id === operation.scopeId)?.path : operation.scopeReason}</p> : null}
    {operation.scopeReason && !showScope ? <p role="note" className="text-xs text-amber-900">{operation.scopeReason}</p> : null}
    {!operation.editable ? <p className="text-xs text-amber-900">This operation has no supported in-place editor in the current operation metadata.</p> : null}
    {operation.sourceStep && !compact ? <>
      {(operation.sourceStep.kind === 'CATALOG_PROJECTION' || operation.sourceStep.kind === 'PIVOT') && props.onEditSourceOutput ? (() => {
        const outputs = model.columns.flatMap(column => {
          const bindings = props.network?.bindings.filter((binding): binding is EditableOutputBinding =>
            isEditableOutputBinding(binding) && binding.columnId === column.id && binding.sourceStepId === operation.id) ?? [];
          return bindings.length === 1 ? [{ column, outputKey: bindings[0].outputKey }] : [];
        });
        return outputs.length ? <details>
          <summary className="cursor-pointer text-xs">Outputs ({outputs.length})</summary>
          <ul className="max-h-48 space-y-1 overflow-auto" aria-label={`Editable outputs from ${operationTitle(operation)}`}>
            {outputs.map(({ column, outputKey }) => <li key={column.id} data-column-id={column.id} data-output-key={outputKey}>
              <button type="button" className={buttonClass} disabled={sourceOperationBlocked || !operation.editable}
                aria-label={`Edit output ${column.label}`} onClick={() => props.onEditSourceOutput?.(operation.id, outputKey)}>{column.label}</button>
            </li>)}
          </ul>
        </details> : null;
      })() : null}
    </> : null}
  </li>;
  const unresolvedSource = model.sourceOperations.filter(operation => !operation.scopeId);
  const unresolvedFields = model.columns.filter(column => column.unavailableReason);
  const scopeOperations = selected ? model.sourceOperations.filter(operation => operation.scopeId === selected.id) : [];
  const scopeConstruction = selected ? model.constructionOperations.filter(operation => operation.scopeId === selected.id) : [];
  const ordinaryFields = selected && !table.document.importedRecipe ? model.columns.filter(column => column.scopeId === selected.id) : [];
  return <section aria-label="Traversal operations" data-testid="traversal-scope-navigator" data-recipe-state={projectionKind} className="min-h-0 min-w-0 space-y-2">
    {props.showScopeNavigation !== false ? <nav aria-label="Authored traversal scopes" className="max-h-32 overflow-auto">
      <ul className="space-y-1">{model.scopes.map((scope, index) => <li key={`${scope.id}:${index}`} style={{ paddingLeft: `${Math.min(scope.depth, 8) * 8}px` }}>
        <button type="button" className={buttonClass} data-occurrence-id={scope.id} aria-pressed={selectedOccurrenceId === scope.id}
          onClick={() => props.onSelectScope(scope.id)}>{scope.path}</button>
      </li>)}</ul>
    </nav> : null}
    {selected ? <>
      <h3 className="text-sm font-semibold">{selected.resourceType} operations</h3>
      <div className="flex flex-wrap gap-1" aria-label={`Actions for ${selected.resourceType}`}>
        {selected.parentId === undefined ? <button type="button" className={buttonClass} disabled={blocked} onClick={props.onEditSourceRows}>Edit source rows</button> : null}
        <button type="button" className={buttonClass} disabled={blocked} onClick={() => props.onExpandRelatedRows(selected.id)}>Expand related records into rows</button>
        <button type="button" className={buttonClass} disabled={blocked} onClick={() => props.onGroupRows(selected.id)}>Group resulting rows</button>
        {props.resultActions ? <div className="flex flex-wrap gap-1" aria-label={`Result operations for ${selected.resourceType}`}>{props.resultActions}</div> : null}
      </div>
      <div>
        <h4 className="text-xs font-medium">Source operations · source-plan order</h4>
        {scopeOperations.length ? <ol aria-label="Source operations in selected scope">{scopeOperations.map(operation => operationItem(operation))}</ol> : <p className="text-xs">No source operations in this scope.</p>}
        {ordinaryFields.length ? <details><summary className="cursor-pointer text-xs">Source fields ({ordinaryFields.length})</summary><ul aria-label="Source fields in selected scope" className="max-h-48 overflow-auto">{ordinaryFields.map(column => <li key={column.id} data-column-id={column.id}>
          <button type="button" className={buttonClass} disabled={blocked} onClick={() => props.onEditColumn(column.id)}>Value policy for {column.label}</button>
        </li>)}</ul></details> : null}
        {scopeConstruction.length ? <><h4 className="mt-2 text-xs font-medium">Operations using this traversal · table execution position</h4><ol aria-label="Table operations using selected traversal">{scopeConstruction.map(operation => operationItem(operation))}</ol></> : null}
        {model.constructionOperations.length ? <><h4 className="mt-2 text-xs font-medium">Result operations · execution order</h4>
          <ol aria-label="Ordered resulting-table operations" className="max-h-56 overflow-auto">{model.constructionOperations.map(operation => operationItem(operation, true, true))}</ol></> : null}
      </div>
    </> : <p role="status" className="text-xs">{selectedOccurrenceId ? 'The selected occurrence is unavailable or ambiguous. Select an authored scope; no operation was retargeted.' : 'Select an authored scope to inspect its operations.'}</p>}
    {blockedReason ? <p role="note" className="text-xs text-amber-900">{blockedReason}</p> : null}
    {unresolvedSource.length || unresolvedFields.length ? <details>
      <summary className="cursor-pointer text-xs text-amber-900">Unresolved source scope ({unresolvedSource.length} operations, {unresolvedFields.length} fields)</summary>
      <ol aria-label="Unresolved source operations" className="max-h-48 overflow-auto">{unresolvedSource.map(operation => operationItem(operation))}</ol>
      <ul aria-label="Fields awaiting exact source scope" className="max-h-48 overflow-auto">{unresolvedFields.map(field => <li key={field.id} data-column-id={field.id} className="text-xs">{field.label}: {field.unavailableReason}</li>)}</ul>
    </details> : null}
  </section>;
}
