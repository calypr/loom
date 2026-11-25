import React from 'react';
import { useEscapeToClose } from '../../dom/useEscapeToClose';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../../contracts/types';
import { ConstructionOperationEditor } from './ConstructionOperationEditor';
import { RelatedEligibilityEditor, type EligibilityStep } from './RelatedEligibilityEditor';

type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId' | 'removeStepIds'>;
export const FilterRowsEditor = ({
  project, explorerId, authResourcePath, snapshotToken, outputId, catalog,
  construction, capabilities, editingStep, selectedColumns, disabled,
  onCandidateChange, onEditStep, onCancel,
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
  readonly onCancel?: () => void;
}) => {
  useEscapeToClose(Boolean(onCancel), onCancel ?? (() => {}));
  return (
    <div className="grid gap-3">
      {editingStep?.operation.kind === 'RELATED_ELIGIBILITY' ? (
        <RelatedEligibilityEditor
          project={project} explorerId={explorerId} authResourcePath={authResourcePath}
          snapshotToken={snapshotToken} outputId={outputId} catalog={catalog}
          construction={construction} capabilities={capabilities}
          step={editingStep as EligibilityStep}
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
