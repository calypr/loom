import { hasDraftVersion } from '../../../../contracts/identity';
import React from 'react';
import { usePopulationWorkflow } from '../../session/usePopulationWorkflow';
import type { SelectionRevision } from '../../../../contracts/selection';
import type { ResourceRef } from '../../../../contracts/selection';
import type {
  ConstructionRouteStep,
} from '../../../../contracts/types';
import type { DraftTable } from '../../model/authoring';
import { PopulationMemberRemoval } from './PopulationMemberRemoval';

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
  draftVersion,
  draftDigest,
  receiptId,
  onAttach,
  onClear,
  onExclude,
  onApplyMemberRemoval,
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
  readonly draftVersion?: number;
  readonly draftDigest?: string;
  readonly receiptId?: string;
  readonly onAttach: (routeChoiceId: string) => void;
  readonly onClear: () => void;
  readonly onExclude?: (
    ref: ResourceRef,
    route: ReadonlyArray<ConstructionRouteStep>,
  ) => void;
  readonly onApplyMemberRemoval?: (proposalId: string) => Promise<boolean>;
}) => {
  const {
    memberTransport, attached, selectionRevisionId, routeLoadState, currentRouteSelection,
    setRouteSelection, currentCoverage, coverage, coverageError, pathIndex, otherConnectionsOpen,
    routeOptions, attachedChoice, selectedChoice, directRouteFirst, directSameResourceRouteSelected, checkCoverage,
  } = usePopulationWorkflow({ table, selection, project, explorerId, authResourcePath, snapshotToken, receiptId });
  const routeOptionsControl = routeOptions.length > 1 ? (
    <>
      {directRouteFirst ? (
        <>
          <span className="text-xs text-slate-600">{routeOptions[pathIndex]?.label}</span>
          <button type="button" aria-expanded={otherConnectionsOpen} onClick={() => setRouteSelection({
            ...currentRouteSelection,
            otherConnectionsOpen: !currentRouteSelection.otherConnectionsOpen,
          })} className="rounded-md border border-slate-300 bg-white px-3 py-2 font-semibold hover:bg-slate-50">
            Other connections
          </button>
        </>
      ) : null}
      {!directRouteFirst || otherConnectionsOpen ? (
        <label className="flex w-full min-w-0 items-center gap-2">
          <span className="font-medium">Connection</span>
          <select aria-label="Population connection" value={pathIndex} onChange={(event) => setRouteSelection({
            ...currentRouteSelection,
            pathIndex: Number(event.currentTarget.value),
          })} className="min-w-0 flex-1 rounded border border-slate-300 bg-white px-2 py-2">
            {routeOptions.map((option, index) => <option key={option.choice.routeChoiceId} value={index}>{option.label}</option>)}
          </select>
        </label>
      ) : null}
    </>
  ) : (
    <span className="text-xs text-slate-600">{routeOptions[0]?.label}</span>
  );
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
            {selection && attached.selectionRevisionId !== selection.id ? (
              <p role="status" className="text-xs text-slate-600">
                Current collection stays attached until you replace it. New selection: {selection.memberCount.toLocaleString()} {selection.resourceType} resources.
              </p>
            ) : null}
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" disabled={disabled} onClick={onClear} className="rounded-md border border-slate-300 bg-white px-3 py-2 font-semibold hover:bg-slate-50 disabled:opacity-40">
                Use all authorized rows
              </button>
              {receiptId && selection && attached.selectionRevisionId === selection.id ? (
                <button type="button" disabled={disabled || currentCoverage?.loading === true} onClick={checkCoverage} className="rounded-md border border-indigo-400 bg-white px-3 py-2 font-semibold text-indigo-800 hover:bg-indigo-50 disabled:opacity-40">
                  {currentCoverage?.loading ? 'Checking selected-resource coverage…' : 'Check selected-resource coverage'}
                </button>
              ) : null}
              {selection && attached.selectionRevisionId !== selection.id &&
               routeLoadState.status === 'ready' && selectedChoice ? (
                <>
                  {routeOptionsControl}
                  <button type="button" disabled={disabled} onClick={() => onAttach(selectedChoice.routeChoiceId)} className="rounded-md bg-indigo-700 px-3 py-2 font-semibold text-white hover:bg-indigo-800 disabled:opacity-40">
                    Replace current collection
                  </button>
                </>
              ) : null}
            </div>
            {selection && attached.selectionRevisionId !== selection.id && routeLoadState.status === 'loading' ? (
              <p className="text-xs text-slate-600">Finding connections for the new selection…</p>
            ) : null}
            {selection && attached.selectionRevisionId !== selection.id && routeLoadState.status === 'error' ? (
              <p role="alert" className="text-xs text-red-700">{routeLoadState.message}</p>
            ) : null}
            {selection && attached.selectionRevisionId !== selection.id &&
             routeLoadState.status === 'ready' && routeOptions.length === 0 && !routeLoadState.truncated ? (
              <p role="alert" className="text-xs text-amber-800">
                No supported path connects {table.document.rootResourceType} rows to {selection.resourceType}.
              </p>
            ) : null}
          </div>
        ) : selection && routeOptions.length > 0 ? (
          <div className="flex w-full min-w-0 flex-wrap items-center gap-2">
            {routeOptionsControl}
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
      {attached && selection && attached.selectionRevisionId === selection.id &&
       hasDraftVersion(draftVersion) && draftDigest && onApplyMemberRemoval ? (
        <PopulationMemberRemoval
          client={memberTransport}
          project={project}
          explorerId={explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={snapshotToken}
          draftVersion={draftVersion}
          draftDigest={draftDigest}
          outputId={table.outputId}
          selection={selection}
          disabled={disabled}
          onApply={onApplyMemberRemoval}
        />
      ) : null}
    </section>
  );
};
