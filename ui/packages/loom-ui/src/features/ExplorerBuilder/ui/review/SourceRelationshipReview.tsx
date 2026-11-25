import { useEscapeToClose } from '../../hooks/useEscapeToClose';
import { useSourceRelationshipReview, type SourceRelationshipReviewProps } from '../../session/useSourceRelationshipReview';

export function SourceRelationshipReview(props: SourceRelationshipReviewProps) {
  const {
    automaticPreview, busy, phase, previewCanceled, escapeEnabled, schedulePreview, cancel,
    preview, apply, visibleResult, visibleError, externallyPreparing,
  } = useSourceRelationshipReview(props);
  useEscapeToClose(escapeEnabled, cancel);
  const sourceEdit = Boolean(props.sourceRule || props.sourceStep || props.sourceSteps);
  const creatingColumnRule = Boolean(props.sourceRule);
  const addedColumnIds = creatingColumnRule && visibleResult
    ? new Set(visibleResult.proposal.affectedColumns) : undefined;
  const addedColumns = addedColumnIds
    ? visibleResult?.proposal.candidateDocument.columns.filter(column => column.columnId !== undefined && addedColumnIds.has(column.columnId)) : undefined;
  const summary = sourceEdit
    ? props.sourceSteps ? `${props.sourceSteps.length} source changes` : 'Source change'
    : `${props.change?.action === 'ADD' ? 'Add' : props.change?.action === 'REMOVE' ? 'Remove' : 'Update'}${props.relationshipDescription ? `: ${props.relationshipDescription}` : ''}`;
  return <section ref={automaticPreview ? schedulePreview : undefined} aria-label={sourceEdit ? 'Proposed source step' : 'Proposed related records'} className="flex flex-wrap items-center justify-end gap-1">
    {addedColumns ? <section aria-label="Generated columns" className="grid w-full gap-1 text-xs text-slate-600">
      <h4 className="font-semibold text-slate-800">Columns this rule will generate</h4>
      {addedColumns.length ? <ul className="max-h-40 divide-y divide-slate-100 overflow-auto border-y border-slate-200">
        {addedColumns.map(column => <li key={column.columnId} className="px-2 py-1.5 text-slate-800">{column.label || column.column}</li>)}
      </ul> : <p>No columns were generated in this preview.</p>}
    </section> : null}
    {visibleError ? <p role="alert" className="mr-auto text-xs text-red-800">{visibleError}</p> : sourceEdit ? null : <p className="mr-auto min-w-0 truncate text-xs text-slate-700" title={summary}>{summary}</p>}
    {automaticPreview ? (
      !visibleResult && !visibleError && !props.disabled ? <p role="status" className="mr-auto text-xs text-slate-600">{previewCanceled ? 'Preview canceled.' : 'Updating preview…'}</p>
        : visibleError ? <button type="button" disabled={busy || props.disabled} onClick={() => void preview()}
            className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">Retry preview</button> : null
    ) : (
      <button type="button" disabled={busy || externallyPreparing || props.disabled} onClick={() => void preview()} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">{externallyPreparing ? 'Preparing…' : phase === 'previewing' ? (props.createRuleReview ? 'Preparing preview…' : 'Working…') : creatingColumnRule ? props.createRuleReview?.label ?? 'Preview column rule' : sourceEdit ? 'Preview source step' : 'Preview relationship'}</button>
    )}
    {!(props.createRuleReview || props.ruleEdit) || visibleResult ? <button type="button" data-testid={sourceEdit ? 'imported-source-step-apply' : undefined} disabled={!visibleResult || busy || externallyPreparing || props.disabled} onClick={() => void apply()} className="h-8 rounded bg-blue-700 px-3 text-sm text-white disabled:opacity-50">{phase === 'applying' && (props.createRuleReview || props.ruleEdit) ? 'Saving…' : creatingColumnRule ? 'Apply column rule' : props.ruleEdit ? 'Apply changes' : sourceEdit ? 'Apply source step' : 'Apply relationship'}</button> : null}
    {!props.createRuleReview || automaticPreview || busy || visibleError || visibleResult ? <button type="button" disabled={phase === 'applying' || (busy && !!visibleResult)} onClick={cancel} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">Cancel</button> : null}
  </section>;
}
