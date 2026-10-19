import React, { useState } from 'react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';
import { RelatedEligibilityEditor, type EligibilityStep } from './RelatedEligibilityEditor';

type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId' | 'removeStepIds'>;
type Mode = 'COLUMN' | 'RELATED';

export const FilterRowsEditor = ({
  project, explorerId, authResourcePath, snapshotToken, outputId, catalog,
  construction, capabilities, editingStep, selectedColumns, disabled,
  onCandidateChange, onEditStep,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly catalog: ExplorerBuilderCatalog;
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly editingStep?: ConstructionStep;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly disabled: boolean;
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
  readonly onEditStep: (stepId: string) => void;
}) => {
  const [mode, setMode] = useState<Mode>(editingStep?.operation.kind === 'RELATED_ELIGIBILITY' ? 'RELATED' : 'COLUMN');
  const selectedMode = editingStep?.operation.kind === 'RELATED_ELIGIBILITY' ? 'RELATED'
    : editingStep ? 'COLUMN' : mode;
  const relatedSupport = capabilities.selectedStage.capabilities.find((capability) => capability.kind === 'RELATED_ELIGIBILITY');
  const relatedAvailable = Boolean(relatedSupport?.supported && capabilities.selectedStage.relatedExpandAnchors?.length);
  return (
    <div className="grid gap-4">
      {!editingStep ? <fieldset className="grid gap-2 rounded-lg border border-slate-200 bg-white p-3 text-sm">
        <legend className="px-1 font-semibold text-slate-900">Filter rows by</legend>
        <div className="flex flex-wrap gap-2">
          {([['COLUMN', 'A value in this table'], ['RELATED', 'Related records']] as const).map(([value, label]) => (
            <button key={value} type="button" aria-pressed={selectedMode === value} disabled={disabled || (value === 'RELATED' && !relatedAvailable)}
              onClick={() => { setMode(value); onCandidateChange(undefined); }}
              className={`rounded-md border px-3 py-2 font-medium ${selectedMode === value ? 'border-blue-700 bg-blue-50 text-blue-900' : 'border-slate-300 text-slate-700 hover:border-blue-300'}`}>
              {label}
            </button>
          ))}
        </div>
        {!relatedAvailable ? <p role="status" className="text-xs text-slate-600">{relatedSupport?.reason ?? 'Loom has not confirmed a related-record route from these rows.'}</p> : null}
      </fieldset> : null}
      {selectedMode === 'RELATED' ? (
        <RelatedEligibilityEditor
          project={project} explorerId={explorerId} authResourcePath={authResourcePath}
          snapshotToken={snapshotToken} outputId={outputId} catalog={catalog}
          construction={construction} capabilities={capabilities}
          step={editingStep?.operation.kind === 'RELATED_ELIGIBILITY'
            ? editingStep as EligibilityStep : undefined}
          disabled={disabled} onCandidateChange={onCandidateChange}
        />
      ) : (
        <ConstructionOperationEditor
          family="KEEP_ROWS" construction={construction} capabilities={capabilities}
          editingStep={editingStep} selectedColumns={selectedColumns}
          disabled={disabled} onCandidateChange={onCandidateChange} onEditStep={onEditStep}
        />
      )}
    </div>
  );
};
