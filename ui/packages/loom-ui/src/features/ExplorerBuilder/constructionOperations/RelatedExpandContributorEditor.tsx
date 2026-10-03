import React, { useEffect, useRef, useState } from 'react';
import { ZodError } from 'zod';
import { useLoomClient } from '../../../react';
import type { RelatedExpandContributorSearchResponse } from '../../../types';

export type ContributorChoice = RelatedExpandContributorSearchResponse['choices'][number];
export type ContributorCondition =
  | { readonly kind: 'ALL' }
  | { readonly kind: 'CHOOSE' }
  | { readonly kind: 'EXISTS'; readonly choice: ContributorChoice }
  | { readonly kind: 'EQUALS'; readonly choice: ContributorChoice; readonly value: string };

type ContributorSearchError = {
  readonly message: string;
  readonly diagnostic?: string;
};

const contributorSearchErrorFrom = (cause: unknown, fallback: string): ContributorSearchError =>
  cause instanceof ZodError
    ? {
      message: 'Could not load the related-record field list because the response did not match the expected format.',
      diagnostic: cause.message,
    }
    : { message: cause instanceof Error ? cause.message : fallback };

const choicesMatchRequest = (
  response: RelatedExpandContributorSearchResponse,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
  outputId: string,
  stageId: string,
  routeChoiceId: string,
  targetNodeId: string,
  targetResourceType: string,
): boolean => response.snapshotToken === snapshotToken
  && response.draftVersion === draftVersion && response.draftDigest === draftDigest
  && response.outputId === outputId && response.stageId === stageId
  && response.routeChoiceId === routeChoiceId
  && response.choices.every((choice) => {
    const scalar = choice.source.cardinality === 'optional_one' || choice.source.cardinality === 'required_one';
    const boundaries = choice.source.repeatedBoundaries ?? [];
    return choice.source.nodeId === targetNodeId
      && choice.source.resourceType === targetResourceType
      && (scalar ? boundaries.length === 0 : choice.source.cardinality === 'many' && boundaries.length > 0);
  });

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
  const client = useLoomClient();
  const [query, setQuery] = useState('');
  const [choices, setChoices] = useState<ReadonlyArray<ContributorChoice>>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<ContributorSearchError | undefined>();
  const requestVersion = useRef(0);
  const moreController = useRef<AbortController | undefined>(undefined);
  const selected = (condition.kind === 'EXISTS' || condition.kind === 'EQUALS')
    && (allowRepeatedFields || condition.choice.source.cardinality !== 'many')
    ? condition.choice : undefined;

  useEffect(() => () => moreController.current?.abort(), [routeChoiceId]);
  useEffect(() => {
    if (condition.kind === 'ALL') return;
    const controller = new AbortController();
    const version = ++requestVersion.current;
    setLoading(true);
    setError(undefined);
    const timeout = globalThis.setTimeout(() => {
      void client.searchRelatedExpandContributors({
        project, explorerId, authResourcePath, snapshotToken,
        expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
        outputId, stageId, routeChoiceId, query: query.trim(), limit: 20,
      }, controller.signal).then((response) => {
        if (controller.signal.aborted || version !== requestVersion.current) return;
        if (!choicesMatchRequest(response, snapshotToken, draftVersion, draftDigest, outputId,
          stageId, routeChoiceId, targetNodeId, targetResourceType)) {
          throw new Error('Available conditions changed. Reload this table before expanding records.');
        }
        setChoices(response.choices.filter((choice) => allowRepeatedFields || choice.source.cardinality !== 'many'));
        setCursor(response.nextCursor);
      }).catch((cause: unknown) => {
        if (!controller.signal.aborted && version === requestVersion.current) {
          setError(contributorSearchErrorFrom(cause, 'Could not load related-record conditions.'));
        }
      }).finally(() => {
        if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
      });
    }, query ? 180 : 0);
    return () => { globalThis.clearTimeout(timeout); controller.abort(); };
  }, [client, project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
    outputId, stageId, routeChoiceId, targetNodeId, targetResourceType, allowRepeatedFields, query, condition.kind === 'ALL']);

  const loadMore = async () => {
    if (!cursor || loading) return;
    moreController.current?.abort();
    const controller = new AbortController();
    moreController.current = controller;
    const version = requestVersion.current;
    setLoading(true);
    setError(undefined);
    try {
      const response = await client.searchRelatedExpandContributors({
        project, explorerId, authResourcePath, snapshotToken,
        expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
        outputId, stageId, routeChoiceId, query: query.trim(), limit: 20, cursor,
      }, controller.signal);
      if (controller.signal.aborted || version !== requestVersion.current) return;
      if (!choicesMatchRequest(response, snapshotToken, draftVersion, draftDigest, outputId,
        stageId, routeChoiceId, targetNodeId, targetResourceType)) {
        throw new Error('Available conditions changed. Reload this table before expanding records.');
      }
      setChoices((current) => [...current,
        ...response.choices.filter((choice) => allowRepeatedFields || choice.source.cardinality !== 'many')]);
      setCursor(response.nextCursor);
    } catch (cause) {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(contributorSearchErrorFrom(cause, 'Could not load more conditions.'));
      }
    } finally {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    }
  };

  return (
    <fieldset className="grid gap-3 rounded border border-slate-200 bg-white p-3 text-sm" data-testid="construction-related-expand-contributors">
      <legend className="px-1 font-medium text-slate-800">Which related records count?</legend>
      <label className="flex gap-2 text-slate-800">
        <input type="radio" name={`contributors-${routeChoiceId}`} checked={condition.kind === 'ALL'}
          disabled={disabled} onChange={() => onChange({ kind: 'ALL' })} />
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
            <input value={query} disabled={disabled} onChange={(event) => {
              requestVersion.current += 1;
              moreController.current?.abort();
              setQuery(event.target.value);
              setChoices([]);
              setCursor(undefined);
            }} placeholder="Search field name or path" className="rounded border border-slate-300 px-3 py-2 font-normal" />
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
          <div role="group" aria-label="Fields for related-record condition" className="max-h-56 space-y-2 overflow-auto">
            {[...choices, ...(selected && !choices.some((choice) => choice.choiceId === selected.choiceId) ? [selected] : [])].map((choice) => (
              <button key={choice.choiceId} type="button" disabled={disabled}
                aria-pressed={selected?.choiceId === choice.choiceId}
                onClick={() => onChange({ kind: 'EXISTS', choice })}
                className={`block w-full rounded border p-2 text-left ${selected?.choiceId === choice.choiceId ? 'border-blue-600 bg-blue-50' : 'border-slate-200 hover:border-blue-300'}`}>
                <span className="block font-medium">{choice.label}</span>
                <span className="block text-xs text-slate-600">{choice.source.path} · {choice.source.logicalType}</span>
              </button>
            ))}
            {!loading && choices.length === 0 && !selected && !error ? <p className="text-slate-600">No supported fields match this search.</p> : null}
          </div>
          {cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start text-blue-800">Load more fields</button> : null}
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
