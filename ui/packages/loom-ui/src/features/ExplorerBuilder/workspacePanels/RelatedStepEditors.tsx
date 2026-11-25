import React from 'react';
import type { Construction } from '../../../contracts/types';
import type { BuilderAuthoringState } from '../model/authoring';
import { RelatedSourceStepEditor, type RelatedSourceStep } from '../constructionOperations/RelatedSourceStepEditor';
import { RelatedFieldEditor, type RelatedFieldStep } from '../constructionOperations/RelatedFieldEditor';
import { sanitizeReasonText } from '../model/catalogItems';
import { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';

type ConstructionLifecycle = ReturnType<typeof useConstructionLifecycle>;

export const RelatedStepEditors = ({
  table,
  state,
  ownerKey,
  construction,
  lifecycle,
  sourceStep,
  fieldStep,
  project,
  authResourcePath,
  pendingCommands,
  publishing,
  onCancelSource,
  onCancelField,
}: {
  readonly table: BuilderAuthoringState['tables'][number];
  readonly state: BuilderAuthoringState;
  readonly ownerKey: string;
  readonly construction?: Construction;
  readonly lifecycle: ConstructionLifecycle;
  readonly sourceStep?: RelatedSourceStep;
  readonly fieldStep?: RelatedFieldStep;
  readonly project: string;
  readonly authResourcePath?: string;
  readonly pendingCommands: number;
  readonly publishing: boolean;
  readonly onCancelSource: () => void;
  readonly onCancelField: () => void;
}) => {
  const disabled = pendingCommands > 0 || state.reconciliation === 'pending' || publishing;
  const sourceEditor = sourceStep ? (
    <section
      aria-label="Related source editor"
      data-testid="construction-related-source-editor"
      className="min-w-0 bg-white p-4"
    >
      {lifecycle.capabilities.status === 'loading' ? (
        <p role="status" className="text-sm text-slate-700">Loading the saved step’s input stage…</p>
      ) : lifecycle.capabilities.status === 'error' ? (
        <p role="alert" className="text-sm text-red-800">{sanitizeReasonText(lifecycle.capabilities.message)}</p>
      ) : lifecycle.capabilities.status === 'ready' ? (
        <RelatedSourceStepEditor
          key={`${table.outputId}:${sourceStep.id}`}
          project={project}
          explorerId={state.explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={state.catalog.snapshotToken}
          outputId={table.outputId}
          rowRoot={table.document.rootResourceType ?? ''}
          catalog={state.catalog}
          construction={construction ?? lifecycle.capabilities.response.baseConstruction}
          capabilities={lifecycle.capabilities.response}
          step={sourceStep}
          disabled={disabled}
          onCandidateChange={lifecycle.onCandidateChange}
          onCancel={onCancelSource}
        />
      ) : null}
    </section>
  ) : undefined;
  const fieldEditor = fieldStep ? (
    <section aria-label="Related field editor" className="min-w-0 bg-white p-4">
      {lifecycle.capabilities.status === 'loading' ? (
        <p role="status" className="text-sm text-slate-700">Loading the saved step’s input stage…</p>
      ) : lifecycle.capabilities.status === 'error' ? (
        <p role="alert" className="text-sm text-red-800">{sanitizeReasonText(lifecycle.capabilities.message)}</p>
      ) : lifecycle.capabilities.status === 'ready' ? (
        <RelatedFieldEditor
          key={`${ownerKey}:${fieldStep.id}`}
          presentation="standalone"
          project={project}
          explorerId={state.explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={state.catalog.snapshotToken}
          outputId={table.outputId}
          construction={construction ?? lifecycle.capabilities.response.baseConstruction}
          capabilities={lifecycle.capabilities.response}
          step={fieldStep}
          disabled={disabled}
          onCandidateChange={lifecycle.onCandidateChange}
          onCancel={onCancelField}
        />
      ) : null}
    </section>
  ) : undefined;
  return <>{sourceEditor}{fieldEditor}</>;
};
