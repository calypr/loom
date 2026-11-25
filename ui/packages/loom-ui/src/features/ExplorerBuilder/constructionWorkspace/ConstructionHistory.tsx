import type { ConstructionHistorySelection } from '../model/constructionHistory';
import React from 'react';
import type { ConstructionHistoryStep } from '../model/constructionHistory';

export interface ConstructionHistoryProps {
  readonly resourceType: string;
  readonly sourceControl?: React.ReactNode;
  readonly sourceSteps?: React.ReactNode;
  readonly steps: ReadonlyArray<ConstructionHistoryStep>;
  readonly selected: ConstructionHistorySelection;
  readonly disabled?: boolean;
  readonly onSelect: (selection: ConstructionHistorySelection) => void;
  readonly onEditStep?: (stepId: string) => void;
  readonly onRemoveStep?: (stepId: string) => void;
}

export const ConstructionHistory = ({
  resourceType,
  sourceControl,
  sourceSteps,
  steps,
  selected,
  disabled = false,
  onSelect,
  onEditStep,
  onRemoveStep,
}: ConstructionHistoryProps) => {
  if (steps.length === 0 && !sourceControl && !resourceType) return null;

  type HistoryGroup =
    | { readonly kind: 'step'; readonly step: ConstructionHistoryStep }
    | {
        readonly kind: 'column-adjustments';
        readonly firstStepId: string;
        readonly steps: ReadonlyArray<ConstructionHistoryStep>;
      };

  const historyGroups: HistoryGroup[] = [];
  for (let index = 0; index < steps.length;) {
    const step = steps[index];
    if (!step) break;
    if (step.hierarchy !== 'COLUMN_ADJUSTMENT') {
      historyGroups.push({ kind: 'step', step });
      index += 1;
      continue;
    }

    const groupSteps: ConstructionHistoryStep[] = [step];
    index += 1;
    while (steps[index]?.hierarchy === 'COLUMN_ADJUSTMENT' && steps[index]?.scopeKey === step.scopeKey) {
      const adjustment = steps[index];
      if (!adjustment) break;
      groupSteps.push(adjustment);
      index += 1;
    }
    historyGroups.push({
      kind: 'column-adjustments',
      firstStepId: step.id,
      steps: groupSteps,
    });
  }

  const renderStep = (step: ConstructionHistoryStep, number: number) => {
    const isSelected = selected.kind === 'step' && selected.stepId === step.id;
    return (
      <li key={step.id} className="min-w-0">
        <div className={`construction-query-step flex min-w-0 flex-wrap items-center gap-1 rounded px-2 py-1 ${isSelected ? 'border-blue-400 bg-blue-50' : 'border-slate-200 bg-white'}`}>
          <span aria-hidden="true" className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${isSelected ? 'bg-blue-700 text-white' : 'bg-slate-100 text-slate-600'}`}>
            {number}
          </span>
          <button
            type="button"
            aria-pressed={isSelected}
            data-testid={`construction-history-step-${step.id}`}
            disabled={disabled}
            title={step.summary}
            onClick={() => {
              if (onEditStep && step.editable) {
                onEditStep(step.id);
              } else {
                onSelect({ kind: 'step', stepId: step.id });
              }
            }}
            className={`min-w-0 flex-1 rounded px-1 py-0.5 text-left ${isSelected ? 'text-blue-950' : 'text-slate-800 hover:text-blue-950'}`}
          >
            <span className="block break-words text-sm font-medium">{step.title}</span>
            <span className={isSelected ? "mt-0.5 block break-words text-xs text-slate-600" : "sr-only"}>{step.summary}</span>
          </button>
          {isSelected && ((onEditStep && step.editable) || onRemoveStep) ? (
            <div className="flex shrink-0 flex-wrap gap-1 border-l border-slate-200 pl-2">
              {onEditStep && step.editable ? (
                <button type="button" disabled={disabled} data-testid={`construction-edit-step-${step.id}`} onClick={() => onEditStep(step.id)} className="rounded px-2 py-1 text-xs font-medium text-emerald-800 hover:bg-emerald-50 disabled:opacity-45">Edit</button>
              ) : null}
              {onRemoveStep ? (
                <button type="button" disabled={disabled} data-testid={`construction-remove-step-${step.id}`} aria-label={step.removeActionLabel} title={step.removeActionLabel} onClick={() => onRemoveStep(step.id)} className="rounded px-2 py-1 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-45">{step.removeActionLabel ? 'Remove connection' : 'Remove'}</button>
              ) : null}
            </div>
          ) : null}
        </div>
      </li>
    );
  };
  let nextStepNumber = 1;
  const renderedHistoryGroups = historyGroups.map((group) => {
    if (group.kind === 'step') {
      const number = nextStepNumber;
      nextStepNumber += 1;
      return renderStep(group.step, number);
    }
    const firstStepNumber = nextStepNumber;
    nextStepNumber += group.steps.length;
    return (
      <li
        key={`column-adjustments-${group.firstStepId}`}
        data-testid={`construction-history-adjustment-group-${group.firstStepId}`}
        className="min-w-0"
      >
        <details open={selected.kind === 'step' && group.steps.some((step) => step.id === selected.stepId)}>
          <summary className="cursor-pointer rounded bg-slate-100 px-2 py-1 text-sm text-slate-700 hover:bg-blue-50">
            <h4 className="m-0 inline text-sm font-medium">
              Column adjustments <span className="font-normal">({group.steps.length})</span>
            </h4>
            <span className="ml-1 text-xs text-slate-500">{firstStepNumber}–{firstStepNumber + group.steps.length - 1}</span>
          </summary>
          <ol start={firstStepNumber} className="m-0 mt-1 flex min-w-0 list-none flex-wrap gap-1 border-l-2 border-blue-200 pl-2">
            {group.steps.map((step, index) => renderStep(step, firstStepNumber + index))}
          </ol>
        </details>
      </li>
    );
  });

  return (
    <section aria-label="Dataframe steps" data-testid="construction-history" className="construction-history min-w-0 bg-white px-2 py-2">
      <header className="mb-1 flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h2 className="m-0 text-sm font-semibold text-slate-950">Dataframe steps</h2>
        <p className="m-0 text-xs text-slate-500">Source → recipe → transformations</p>
      </header>
      <div className="mb-1 flex min-w-0 flex-wrap items-center gap-2">
        <span className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-slate-500">Source</span>
        {sourceControl ? (
          <div className="min-w-0 max-w-full" onClickCapture={(event) => {
            if (event.target instanceof Element && event.target.closest('[data-construction-source-selection-trigger]')) {
              onSelect({ kind: 'source' });
            }
          }}>
            {sourceControl}
          </div>
        ) : <button
            type="button"
            aria-pressed={selected.kind === 'source'}
            data-testid="construction-history-source"
            disabled={disabled}
            onClick={() => onSelect({ kind: 'source' })}
            className={`min-w-0 max-w-full break-words rounded px-2 py-1 text-left text-sm ${
              selected.kind === 'source' ? 'bg-blue-100 font-semibold text-blue-950' : 'text-slate-700 hover:bg-blue-50'
            }`}
          >
            {resourceType || 'Source records'}
          </button>}
      </div>
      {sourceSteps}
      {steps.length === 0 ? (
        <p data-testid="construction-history-empty" className="m-0 mt-1 text-xs text-slate-500">
          {sourceSteps ? 'Add another step to transform this dataframe.' : 'Add a step to shape this dataframe.'}
        </p>
      ) : (
        <>
          <h3 className="mb-1 mt-2 text-xs font-medium text-slate-500">Transformations</h3>
          <ol className="m-0 flex min-w-0 list-none flex-wrap gap-1 p-0">
            {renderedHistoryGroups}
          </ol>
        </>
      )}
    </section>
  );
};
