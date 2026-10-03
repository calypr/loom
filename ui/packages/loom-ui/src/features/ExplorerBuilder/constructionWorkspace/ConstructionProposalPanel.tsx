import React from 'react';
import type {
  Construction,
  ConstructionProposalResponse,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import { PreviewValueCoverage } from './PreviewValueCoverage';
import { constructionHistorySteps } from './constructionHistory';

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
  | { readonly status: 'error'; readonly message: string; readonly retryable?: boolean }
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
  baseConstruction,
}: {
  readonly state: ConstructionProposalViewState;
  readonly canApply: boolean;
  readonly onApply: () => void;
  readonly onCancel: () => void;
  readonly onRetry: () => void;
  readonly baseConstruction?: Construction;
}) => {
  if (state.status === 'idle') return null;

  const response = 'response' in state ? state.response : undefined;
  const isReady = state.status === 'ready';
  const missingInputCount = response?.dependencyImpact.missingInputs?.length ?? 0;
  const removedIds = new Set(response?.dependencyImpact.removedStepIds ?? []);
  const removedSteps = constructionHistorySteps(baseConstruction, []).filter((step) => removedIds.has(step.id));
  const removedExpansions = baseConstruction?.steps.filter((step) => !step.ownerStepId && removedIds.has(step.id));
  const expansionResources = removedExpansions?.flatMap((step) => step.operation.kind === 'RELATED_EXPAND'
    ? [step.operation.relatedExpand.targetResourceType]
    : []) ?? [];
  const isEdit = Boolean(response?.changedStepId);
  const expansionNames = new Intl.ListFormat('en', { style: 'long', type: 'conjunction' });
  let removalSummary: string | undefined;
  if (removedSteps.length > 0) {
    const onlyExpansions = expansionResources.length === removedSteps.length;
    if (isEdit) {
      removalSummary = onlyExpansions
        ? `This edit also removes the dependent ${expansionNames.format(expansionResources)} expansion${expansionResources.length > 1 ? 's' : ''}.`
        : `This edit also removes ${removedSteps.length} dependent step${removedSteps.length > 1 ? 's' : ''}.`;
    } else if (onlyExpansions) {
      const dependents = expansionResources.length > 1
        ? ` and its dependent ${expansionNames.format(expansionResources.slice(1))} expansion${expansionResources.length > 2 ? 's' : ''}`
        : '';
      removalSummary = `Remove ${expansionResources[0]} expansion${dependents}.`;
    } else {
      const dependents = removedSteps.length > 1
        ? ` and ${removedSteps.length - 1} dependent step${removedSteps.length > 2 ? 's' : ''}`
        : '';
      removalSummary = `Remove ${removedSteps[0].title}${dependents}.`;
    }
  }
  const affectedStepCount = response?.dependencyImpact.affectedStepIds.filter(
    (stepId) => stepId !== response.changedStepId && !removedIds.has(stepId),
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
          {removalSummary ? (
            <div data-testid="construction-removal-summary" className="mb-3 rounded-md border border-amber-200 bg-amber-50 p-3 text-amber-950">
              <p className="text-sm font-semibold">{removalSummary}</p>
              <ul className="mt-2 list-disc space-y-1 pl-5 text-xs">
                {removedSteps.map((step) => (
                  <li key={step.id} data-testid={`construction-removal-step-${step.id}`}>
                    {step.title}: {step.summary}
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-xs">The preview shows the resulting table. Nothing is saved until you apply this {isEdit ? 'change' : 'removal'}.</p>
            </div>
          ) : null}
          <h3 className="font-semibold text-emerald-950">{removalSummary && !isEdit ? 'Removal preview' : 'Proposal preview'}</h3>
          <p className="mt-1 text-sm text-slate-700">
            {state.preview.rowCount.toLocaleString()} {state.preview.rowCount === 1 ? 'row' : 'rows'} and {state.preview.columns.length} {state.preview.columns.length === 1 ? 'column' : 'columns'}
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
          {state.retryable === true ? (
            <button
              type="button"
              data-testid="construction-retry-proposal"
              className="mt-2 rounded border border-slate-300 px-2.5 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50"
              onClick={onRetry}
            >
              Retry preview
            </button>
          ) : null}
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
            {removalSummary && !isEdit ? 'Apply removal' : 'Apply change'}
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
