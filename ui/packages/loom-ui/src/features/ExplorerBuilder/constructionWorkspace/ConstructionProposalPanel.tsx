import React from 'react';
import type {
  ConstructionProposalResponse,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import { PreviewValueCoverage } from './PreviewValueCoverage';

export type ConstructionProposalViewState =
  | { readonly status: 'idle' }
  | { readonly status: 'previewing' }
  | {
      readonly status: 'ready';
      readonly response: ConstructionProposalResponse;
      readonly preview: ExplorerBuilderPreviewResult;
    }
  | {
      readonly status: 'needs-repair';
      readonly response: ConstructionProposalResponse;
    }
  | { readonly status: 'error'; readonly message: string }
  | {
      readonly status: 'applying';
      readonly response: ConstructionProposalResponse;
      readonly preview: ExplorerBuilderPreviewResult;
    };

export const constructionProposalIsApplicable = (
  state: ConstructionProposalViewState,
  identity: {
    readonly outputId: string;
    readonly snapshotToken: string;
    readonly draftVersion: number;
    readonly draftDigest: string;
  },
): state is Extract<ConstructionProposalViewState, { readonly status: 'ready' }> =>
  state.status === 'ready' &&
  Boolean(state.response.proposalId) &&
  state.response.previewStatus === 'READY' &&
  state.response.outputId === identity.outputId &&
  state.response.snapshotToken === identity.snapshotToken &&
  state.response.draftVersion === identity.draftVersion &&
  state.response.draftDigest === identity.draftDigest &&
  state.preview.receiptId === state.response.proposalId &&
  state.preview.outputId === identity.outputId &&
  state.preview.rows !== null;

export const ConstructionProposalPanel = ({
  state,
  canApply,
  onApply,
  onCancel,
  onRetry,
}: {
  readonly state: ConstructionProposalViewState;
  readonly canApply: boolean;
  readonly onApply: () => void;
  readonly onCancel: () => void;
  readonly onRetry: () => void;
}) => {
  if (state.status === 'idle') return null;

  const response = 'response' in state ? state.response : undefined;
  const isReady = state.status === 'ready';
  const missingInputCount = response?.dependencyImpact.missingInputs?.length ?? 0;
  const affectedStepCount = response?.dependencyImpact.affectedStepIds.filter(
    (stepId) => stepId !== response.changedStepId,
  ).length ?? 0;
  const changedStep = response?.candidateConstruction.steps.find(
    (step) => step.id === response.changedStepId,
  );
  const relatedOutputId = changedStep?.operation.kind === 'RELATED_SOURCE'
    ? changedStep.operation.relatedSource.outputColumnId
    : undefined;
  const relatedColumnId = relatedOutputId
    ? changedStep?.outputs.find((output) => output.id === relatedOutputId)?.name
    : undefined;
  const relatedForm = changedStep?.operation.kind === 'RELATED_SOURCE'
    ? changedStep.operation.relatedSource.form
    : undefined;

  return (
    <section
      aria-label="Proposed change"
      data-testid="construction-proposal-panel"
      data-proposal-status={state.status}
      data-proposal-id={response?.proposalId ?? ''}
      className="mt-4 rounded-xl border border-emerald-200 bg-white p-4 shadow-sm"
    >
      {state.status === 'previewing' ? (
        <p role="status" data-testid="construction-proposal-loading" className="text-sm text-slate-700">
          Checking this change and preparing its table preview…
        </p>
      ) : null}

      {state.status === 'needs-repair' ? (
        <div role="status" data-testid="construction-proposal-needs-repair">
          <h3 className="font-semibold text-amber-950">Later steps need attention</h3>
          <p className="mt-1 text-sm text-amber-900">
            {missingInputCount > 0
              ? `${missingInputCount} saved input${missingInputCount === 1 ? '' : 's'} are no longer available.`
              : 'This change affects later steps that must be reviewed.'}
            {affectedStepCount > 0
              ? ` ${affectedStepCount} later step${affectedStepCount === 1 ? '' : 's'} are affected.`
              : ''}
            {' '}Edit or remove the affected steps before applying this change.
          </p>
        </div>
      ) : null}

      {state.status === 'ready' ? (
        <div data-testid="construction-proposal-ready">
          <h3 className="font-semibold text-emerald-950">Proposal preview</h3>
          <p className="mt-1 text-sm text-slate-700">
            {state.preview.rowCount.toLocaleString()} rows and {state.preview.columns.length} columns
            {' '}· checked in {state.response.previewDurationMs} ms.
          </p>
          {relatedColumnId ? (
            <PreviewValueCoverage preview={state.preview} columnIds={[relatedColumnId]} resultForm={relatedForm} />
          ) : null}
          {affectedStepCount > 0 ? (
            <p className="mt-1 text-xs text-slate-600">
              {affectedStepCount} later step{affectedStepCount === 1 ? '' : 's'} will be recalculated.
            </p>
          ) : null}
        </div>
      ) : null}

      {state.status === 'applying' ? (
        <p role="status" data-testid="construction-proposal-applying" className="text-sm text-slate-700">
          Saving this table change…
        </p>
      ) : null}

      {state.status === 'error' ? (
        <div role="alert" data-testid="construction-proposal-error">
          <p className="text-sm text-red-800">{state.message}</p>
          <button
            type="button"
            data-testid="construction-retry-proposal"
            className="mt-2 rounded border border-slate-300 px-2.5 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
            onClick={onRetry}
          >
            Retry preview
          </button>
        </div>
      ) : null}

      <div className="mt-3 flex flex-wrap justify-end gap-2">
        {isReady ? (
          <button
            type="button"
            data-testid="construction-apply-proposal"
            disabled={!canApply}
            className="rounded-md bg-emerald-800 px-3 py-2 text-sm font-semibold text-white hover:bg-emerald-900 disabled:cursor-not-allowed disabled:opacity-45"
            onClick={onApply}
          >
            Apply change
          </button>
        ) : null}
        {state.status !== 'applying' ? (
          <button
            type="button"
            data-testid="construction-cancel-proposal"
            className="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
            onClick={onCancel}
          >
            Cancel
          </button>
        ) : null}
      </div>
    </section>
  );
};
