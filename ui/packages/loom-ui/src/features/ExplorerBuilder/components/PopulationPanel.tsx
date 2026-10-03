import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useLoomClient, usePopulationMappingMutation } from '../../../react';
import type { PopulationMappingResponse } from '../../../api';
import type { SelectionRevision } from '../../../selection';
import type { ResourceRef } from '../../../selection';
import type {
  ConstructionRouteStep,
  PopulationRouteChoice,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { matchesSavedPopulationRoute, populationRouteOptions } from '../populationRoutes';

type RouteLoadState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly truncated: boolean }
  | { readonly status: 'error'; readonly message: string };

export const PopulationPanel = ({
  table,
  selection,
  loading,
  error,
  disabled,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  receiptId,
  onAttach,
  onClear,
  onExclude,
}: {
  readonly table: DraftTable;
  readonly selection?: SelectionRevision;
  readonly loading: boolean;
  readonly error?: string;
  readonly disabled: boolean;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly receiptId?: string;
  readonly onAttach: (routeChoiceId: string) => void;
  readonly onClear: () => void;
  readonly onExclude?: (
    ref: ResourceRef,
    route: ReadonlyArray<ConstructionRouteStep>,
  ) => void;
}) => {
  const client = useLoomClient();
  const [routeChoices, setRouteChoices] = useState<ReadonlyArray<PopulationRouteChoice>>([]);
  const [routeLoadState, setRouteLoadState] = useState<RouteLoadState>({ status: 'idle' });
  const [pathIndex, setPathIndex] = useState(0);
  const [otherConnectionsOpen, setOtherConnectionsOpen] = useState(false);
  const [checkPopulation, checkStatus] = usePopulationMappingMutation();
  const [coverage, setCoverage] = useState<PopulationMappingResponse>();
  const [coverageError, setCoverageError] = useState<string>();
  const reportRequestEpoch = useRef(0);
  const routeRequestEpoch = useRef(0);
  const attached = table.document.population;
  const selectionRevisionId = selection?.id;
  const attachedChoices = useMemo(
    () => attached
      ? routeChoices.filter((choice) => matchesSavedPopulationRoute(choice.route, attached.route))
      : [],
    [attached, routeChoices],
  );
  const routeOptions = useMemo(
    () => populationRouteOptions({
      choices: routeChoices,
      selectionResourceType: selection?.resourceType,
      rootResourceType: table.document.rootResourceType,
    }),
    [routeChoices, selection?.resourceType, table.document.rootResourceType],
  );
  const attachedChoice = attachedChoices.length === 1 ? attachedChoices[0] : undefined;
  const selectedChoice = routeOptions[pathIndex]?.choice;
  const directRouteFirst = routeOptions[0]?.isDirectSameResource === true;
  const directSameResourceRouteSelected = selection?.resourceType === table.document.rootResourceType && (
    attached ? attached.route.length === 0 : routeOptions[pathIndex]?.isDirectSameResource === true
  );
  useEffect(() => {
    routeRequestEpoch.current += 1;
    setRouteChoices([]);
    setPathIndex(0);
    setOtherConnectionsOpen(false);
    if (!selectionRevisionId) {
      setRouteLoadState({ status: 'idle' });
      return;
    }
    const requestEpoch = routeRequestEpoch.current;
    const controller = new AbortController();
    setRouteLoadState({ status: 'loading' });
    void client.searchPopulationRoutes({
      project,
      explorerId,
      authResourcePath,
      snapshotToken,
      outputId: table.outputId,
      selectionRevisionId,
      limit: 50,
      requestId: `population-routes-${window.crypto.randomUUID()}`,
    }, controller.signal).then((response) => {
      if (controller.signal.aborted || requestEpoch !== routeRequestEpoch.current) return;
      if (
        response.snapshotToken !== snapshotToken ||
        response.outputId !== table.outputId ||
        response.selectionRevisionId !== selectionRevisionId
      ) {
        throw new Error('Loom returned population routes for another table or collection.');
      }
      setRouteChoices(response.choices);
      setRouteLoadState({ status: 'ready', truncated: response.truncated });
    }).catch((error: unknown) => {
      if (controller.signal.aborted || requestEpoch !== routeRequestEpoch.current) return;
      setRouteLoadState({
        status: 'error',
        message: error instanceof Error
          ? error.message
          : 'Loom could not find a supported population route.',
      });
    });
    return () => controller.abort();
  }, [authResourcePath, client, explorerId, project, selectionRevisionId, snapshotToken, table.outputId]);
  useEffect(() => {
    reportRequestEpoch.current += 1;
    setCoverage(undefined);
    setCoverageError(undefined);
    return () => {
      reportRequestEpoch.current += 1;
    };
  }, [attached?.selectionRevisionId, table.outputId, receiptId, project, explorerId, selection?.id, selection?.project, selection?.generation, selection?.scopeDigest, selection?.membershipDigest]);
  const checkCoverage = () => {
    if (!receiptId || !project || !explorerId || !attached || !selection) return;
    setCoverageError(undefined);
    const requestEpoch = ++reportRequestEpoch.current;
    const expectedBinding = {
      receiptId,
      outputId: table.outputId,
      project: selection.project,
      explorerId,
      generation: selection.generation,
      scopeDigest: selection.scopeDigest,
      selectionRevisionId: selection.id,
      membershipDigest: selection.membershipDigest,
      resourceType: selection.resourceType,
    };
    void checkPopulation({
      project,
      explorerId,
      authResourcePath,
      receiptId,
      outputId: table.outputId,
      limit: 100,
    }).unwrap().then((report) => {
      if (requestEpoch !== reportRequestEpoch.current) return;
      const bindingMatches = Object.entries(expectedBinding).every(([key, value]) => report.binding[key as keyof typeof report.binding] === value);
      if (!bindingMatches) {
        setCoverageError('Coverage check returned a stale report.');
        return;
      }
      setCoverage(report);
    }).catch((error: unknown) => {
      if (requestEpoch !== reportRequestEpoch.current) return;
      setCoverageError(error instanceof Error ? error.message : 'Coverage check failed.');
    });
  };
  return (
    <section
      aria-label="Starting collection"
      data-selection-revision-id={selectionRevisionId}
      data-attached-selection-revision-id={attached?.selectionRevisionId}
      className="rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-800"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          {loading ? <p className="mt-1 text-slate-600">Loading the saved selection…</p> : null}
          {error ? <p role="alert" className="mt-1 text-red-700">{error}</p> : null}
        </div>
        {attached ? (
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" disabled={disabled} onClick={onClear} className="rounded-md border border-slate-300 bg-white px-3 py-2 font-semibold hover:bg-slate-50 disabled:opacity-40">
              Use all authorized rows
            </button>
            {receiptId && selection ? (
              <button type="button" disabled={disabled || checkStatus.isLoading} onClick={checkCoverage} className="rounded-md border border-indigo-400 bg-white px-3 py-2 font-semibold text-indigo-800 hover:bg-indigo-50 disabled:opacity-40">
                {checkStatus.isLoading ? 'Checking selected-resource coverage…' : 'Check selected-resource coverage'}
              </button>
            ) : null}
          </div>
        ) : selection && routeOptions.length > 0 ? (
          <div className="flex w-full min-w-0 flex-wrap items-center gap-2">
            {routeOptions.length > 1 ? (
              <>
                {directRouteFirst ? (
                  <>
                    <span className="text-xs text-slate-600">{routeOptions[pathIndex]?.label}</span>
                    <button type="button" aria-expanded={otherConnectionsOpen} onClick={() => setOtherConnectionsOpen((open) => !open)} className="rounded-md border border-slate-300 bg-white px-3 py-2 font-semibold hover:bg-slate-50">
                      Other connections
                    </button>
                  </>
                ) : null}
                {!directRouteFirst || otherConnectionsOpen ? (
                  <label className="flex w-full min-w-0 items-center gap-2">
                    <span className="font-medium">Connection</span>
                    <select aria-label="Population connection" value={pathIndex} onChange={(event) => setPathIndex(Number(event.currentTarget.value))} className="min-w-0 flex-1 rounded border border-slate-300 bg-white px-2 py-2">
                      {routeOptions.map((option, index) => <option key={option.choice.routeChoiceId} value={index}>{option.label}</option>)}
                    </select>
                  </label>
                ) : null}
              </>
            ) : <span className="text-xs text-slate-600">{routeOptions[0]?.label}</span>}
            <button type="button" disabled={disabled || !selectedChoice} onClick={() => selectedChoice && onAttach(selectedChoice.routeChoiceId)} className="rounded-md bg-indigo-700 px-3 py-2 font-semibold text-white hover:bg-indigo-800 disabled:opacity-40">
              Use selected resources
            </button>
          </div>
        ) : selection && routeLoadState.status === 'loading' ? (
          <p className="font-medium text-slate-600">Finding compiler-proved connections…</p>
        ) : selection && routeLoadState.status === 'error' ? (
          <p role="alert" className="font-medium text-red-700">{routeLoadState.message}</p>
        ) : selection && !loading && routeLoadState.status === 'ready' ? (
          <p role="alert" className="font-medium text-amber-800">No supported path connects {table.document.rootResourceType} rows to {selection.resourceType}.</p>
        ) : null}
      </div>
      {routeLoadState.status === 'ready' && routeLoadState.truncated && !directSameResourceRouteSelected ? (
        <p role="status" className="mt-2 text-amber-800">
          Loom reached the automatic route-search limit. The listed connections are valid, but additional routes may be available in Advanced graph.
        </p>
      ) : null}
      {!attached && selectedChoice && selectedChoice.presentation.facts.length > 0 ? (
        <dl className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-600">
          {selectedChoice.presentation.facts.map((fact) => (
            <div key={`${fact.label}:${fact.value}`} className="flex gap-1">
              <dt className="font-semibold">{fact.label}</dt>
              <dd>{fact.value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      {coverageError ? <p role="alert" className="mt-2 text-red-700">{coverageError}</p> : null}
      {coverage?.status === 'COMPLETE' && coverage.counts ? (
        <div className="mt-3 border-t border-indigo-200 pt-3" data-testid="population-coverage-report">
          <p className="font-semibold">{coverage.counts.selected.toLocaleString()} selected · {coverage.counts.mapped.toLocaleString()} produce rows · {coverage.counts.unmapped.toLocaleString()} needs attention</p>
          {coverage.unmapped.length > 0 ? (
            <ul className="mt-2 space-y-1 text-slate-700">
              {coverage.unmapped.map((ref) => <li key={`${ref.resourceType}:${ref.id}`} className="flex flex-wrap items-center gap-2">
                <span>{ref.resourceType}/{ref.id}</span>
                {onExclude && attachedChoice ? (
                  <button type="button" disabled={disabled} onClick={() => onExclude(ref, attachedChoice.route)} className="rounded border border-amber-400 bg-white px-2 py-1 text-xs font-semibold text-amber-900 disabled:opacity-40">
                    Remove from collection
                  </button>
                ) : null}
              </li>)}
            </ul>
          ) : <p className="mt-1 text-slate-600">All selected resources produce rows.</p>}
          {coverage.nextCursor ? <p className="mt-1 text-slate-600">Showing first {coverage.unmapped.length.toLocaleString()} unmatched resources; more available.</p> : null}
        </div>
      ) : coverage?.status === 'INCOMPLETE' ? (
        <p role="status" className="mt-2 text-amber-800">Coverage check incomplete; exact counts are unavailable.</p>
      ) : null}
    </section>
  );
};
