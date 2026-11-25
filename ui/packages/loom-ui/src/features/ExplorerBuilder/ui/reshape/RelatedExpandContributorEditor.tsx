import { fieldPickerList, fieldPickerMetadata, fieldPickerRow, fieldPickerSelected } from '../shared/fieldPickerStyles';
import React from 'react';
import { repeatedContributorCursorMessage, useRelatedExpandContributors, type ContributorChoice } from '../../session/useRelatedExpandContributors';
export type { ContributorChoice } from '../../session/useRelatedExpandContributors';

export type ContributorCondition =
  | { readonly kind: 'ALL' }
  | { readonly kind: 'CHOOSE' }
  | { readonly kind: 'EXISTS'; readonly choice: ContributorChoice }
  | { readonly kind: 'EQUALS'; readonly choice: ContributorChoice; readonly value: string };

export const RelatedExpandContributorEditor = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  draftVersion,
  draftDigest,
  outputId,
  stageId,
  routeChoiceId,
  targetNodeId,
  targetResourceType,
  allowRepeatedFields = false,
  condition,
  disabled,
  onChange,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
  readonly routeChoiceId: string;
  readonly targetNodeId: string;
  readonly targetResourceType: string;
  readonly allowRepeatedFields?: boolean;
  readonly condition: ContributorCondition;
  readonly disabled: boolean;
  readonly onChange: (condition: ContributorCondition) => void;
}) => {
  const { query, choices, cursor, loading, error, cancel, retrySearch, searchFor, loadMore } =
    useRelatedExpandContributors({
      project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
      outputId, stageId, routeChoiceId, targetNodeId, targetResourceType, allowRepeatedFields,
    }, condition.kind !== 'ALL');
  const selected = (condition.kind === 'EXISTS' || condition.kind === 'EQUALS')
    && (allowRepeatedFields || condition.choice.source.cardinality !== 'many')
    ? condition.choice : undefined;
  const staleSearch = error?.message === 'Available conditions changed. Reload this table before expanding records.';

  const chooseAllMatches = () => {
    cancel();
    onChange({ kind: 'ALL' });
  };

  return (
    <fieldset className="grid gap-3 rounded border border-slate-200 bg-white p-3 text-sm" data-testid="construction-related-expand-contributors">
      <legend className="px-1 font-medium text-slate-800">Which related records count?</legend>
      <label className="flex gap-2 text-slate-800">
        <input type="radio" name={`contributors-${routeChoiceId}`} checked={condition.kind === 'ALL'}
          disabled={disabled} onChange={chooseAllMatches} />
        All matching records
      </label>
      <label className="flex gap-2 text-slate-800">
        <input type="radio" name={`contributors-${routeChoiceId}`} checked={condition.kind !== 'ALL'}
          disabled={disabled} onChange={() => onChange(selected ? condition : { kind: 'CHOOSE' })} />
        Only records meeting a condition
      </label>
      {condition.kind !== 'ALL' ? (
        <div className="grid gap-3 border-l-2 border-blue-100 pl-3">
          <label className="grid gap-1 font-medium text-slate-800">Find a field on {targetResourceType}
            <input value={query} disabled={disabled} onChange={(event) => searchFor(event.target.value)} placeholder="Search field name or path" className="rounded border border-slate-300 px-3 py-2 font-normal" />
          </label>
          {error ? (
            <div role="alert" className="text-red-800">
              <p>{error.message}</p>
              {error.diagnostic ? (
                <details className="mt-1 text-xs">
                  <summary>Technical details</summary>
                  <pre className="mt-1 whitespace-pre-wrap">{error.diagnostic}</pre>
                </details>
              ) : null}
            </div>
          ) : null}
          {loading ? <p role="status" className="text-slate-600">Finding supported fields…</p> : null}
          {error && !loading && !cursor && !staleSearch && error.message !== repeatedContributorCursorMessage ? (
            <button type="button" disabled={disabled} onClick={retrySearch}
              className="justify-self-start text-blue-800 underline">
              Retry field search
            </button>
          ) : null}
          <div role="group" aria-label="Fields for related-record condition" className={fieldPickerList}>
            {[...choices, ...(selected && !choices.some((choice) => choice.choiceId === selected.choiceId) ? [selected] : [])].map((choice) => (
              <button key={choice.choiceId} type="button" disabled={disabled}
                aria-pressed={selected?.choiceId === choice.choiceId}
                onClick={() => onChange({ kind: 'EXISTS', choice })}
                className={`${fieldPickerRow} ${selected?.choiceId === choice.choiceId ? fieldPickerSelected : ''} disabled:opacity-50`}>
                <span className="min-w-0 flex-1 truncate">{choice.label}</span>
                <span className={`${fieldPickerMetadata} max-w-[45%] truncate`} title={choice.source.path}>{choice.source.path} · {choice.source.logicalType}
                  {choice.source.cardinality === 'many' ? ' · Repeated values (any item can match)' : ''}
                </span>
              </button>
            ))}
            {!loading && choices.length === 0 && !selected && !error && !cursor ? <p className="text-slate-600">No supported fields match this search.</p> : null}
            {!loading && choices.length === 0 && !selected && !error && cursor ? <p role="status" className="text-slate-600">More supported fields are available. Load more to continue browsing.</p> : null}
          </div>
          {cursor && !staleSearch ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start text-blue-800">
            {error ? 'Retry loading more fields' : 'Load more fields'}
          </button> : null}
          {selected ? (
            <>
              <label className="grid gap-1 font-medium text-slate-800">Condition
                <select value={condition.kind} disabled={disabled} onChange={(event) => onChange(event.target.value === 'EQUALS'
                  ? { kind: 'EQUALS', choice: selected, value: '' }
                  : { kind: 'EXISTS', choice: selected })} className="rounded border border-slate-300 bg-white px-3 py-2 font-normal">
                  {selected.operators.includes('EXISTS') ? <option value="EXISTS">Has a value</option> : null}
                  {selected.operators.includes('EQUALS') ? <option value="EQUALS">Equals a value</option> : null}
                </select>
              </label>
              {selected.source.cardinality === 'many' ? (
                <p role="note" className="text-xs text-slate-600">
                  {condition.kind === 'EQUALS'
                    ? selected.source.logicalType === 'code'
                      ? 'This condition matches when any value in the repeated field equals the exact code below; matching uses the code alone.'
                      : 'This condition matches when any value in the repeated field equals the exact value below.'
                    : 'This condition matches when any value in the repeated field is present.'}
                </p>
              ) : null}
              {condition.kind === 'EQUALS' ? (
                <div className="grid gap-2">
                  <label className="grid gap-1 font-medium text-slate-800">{selected.source.logicalType === 'code' ? 'Code' : 'Exact value'}
                    <input value={condition.value} disabled={disabled} onChange={(event) => onChange({ ...condition, value: event.target.value })}
                      className="rounded border border-slate-300 px-3 py-2 font-normal" />
                  </label>
                  {selected.suggestedValues.length > 0 ? (
                    <div className="flex flex-wrap gap-2" aria-label="Catalog value suggestions">
                      {selected.suggestedValues.map((value) => <button key={value} type="button" disabled={disabled}
                        onClick={() => onChange({ ...condition, value })} className="rounded border border-slate-300 px-2 py-1 text-blue-800">{value}</button>)}
                    </div>
                  ) : null}
                  <p className="text-xs text-slate-600">Suggestions come from the source catalog. Preview checks the records in this table.</p>
                </div>
              ) : null}
            </>
          ) : null}
        </div>
      ) : null}
    </fieldset>
  );
};
