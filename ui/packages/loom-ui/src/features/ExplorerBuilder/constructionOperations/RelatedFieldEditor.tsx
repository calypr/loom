import { fieldPickerList, fieldPickerMetadata, fieldPickerRow, fieldPickerSelected } from '../components/fieldPickerStyles';
import React, { useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import { useRelatedFieldChoices } from '../session/useRelatedFieldChoices';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStep,
  RelatedFieldChoiceSearchResponse,
} from '../../../contracts/types';
import { constructionWithStep } from '../model/editorShared';
import { constructionInputForStage } from '../model/constructionStages';

type RelatedFieldOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_FIELD' }>;
export type RelatedFieldStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedFieldOperation };
type FieldChoice = RelatedFieldChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;

const fieldName = (source: FieldChoice['source']): string => {
  const suffix = source.path.replace(/^.*\./, '').replace(/[^A-Za-z0-9_]/g, '_');
  return `related_${source.resourceType.toLowerCase()}_${suffix}`;
};

const candidateFor = (
  construction: Construction,
  capabilities: ConstructionCapabilitiesResponse,
  step: RelatedFieldStep | undefined,
  stepId: string,
  outputColumnId: string,
  choice: FieldChoice | undefined,
  outputName: string,
  outputLabel: string,
): CandidateIntent | undefined => {
  const name = outputName.trim();
  const label = outputLabel.trim();
  const stage = capabilities.selectedStage;
  if (!choice || !stage.activeRelatedRecord || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || !label) return undefined;
  if (choice.source.nodeId !== stage.activeRelatedRecord.targetNodeId ||
      choice.source.resourceType !== stage.activeRelatedRecord.targetResourceType ||
      stage.columns.some((column) => column.name.toLowerCase() === name.toLowerCase())) return undefined;
  const output = { id: outputColumnId, name, label, type: choice.source.logicalType, nullable: true };
  const outputs = step
    ? step.outputs.map((column) => column.id === outputColumnId ? output : column)
    : [
      ...stage.columns.map(({ id, name: columnName, label: columnLabel, type }) => ({
        id, name: columnName, label: columnLabel, ...(type ? { type } : {}),
      })),
      output,
    ];
  if (step && !step.outputs.some((column) => column.id === outputColumnId)) return undefined;
  const nextStep: RelatedFieldStep = {
    id: stepId,
    inputs: [constructionInputForStage(stage)],
    operation: {
      kind: 'RELATED_FIELD',
      relatedField: { choiceId: choice.choiceId, source: choice.source, outputColumnId },
    },
    outputs,
  };
  const candidateConstruction = constructionWithStep({ construction, editingStep: step, step: nextStep });
  return candidateConstruction ? { candidateConstruction, changedStepId: stepId } : undefined;
};

export const RelatedFieldEditor = ({
  presentation,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  construction,
  capabilities,
  step,
  disabled,
  onCandidateChange,
  onCancel,
  sourceControls,
}: {
  readonly presentation: 'standalone' | 'source-group';
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly step?: RelatedFieldStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
  readonly onCancel?: () => void;
  readonly sourceControls?: React.ReactNode;
}) => {
  const stage = capabilities.selectedStage;
  const [stepId] = useState(() => step?.id ?? `related_field_${globalThis.crypto.randomUUID()}`);
  const [outputColumnId] = useState(() => step?.operation.relatedField.outputColumnId ?? `related_field_column_${globalThis.crypto.randomUUID()}`);
  const savedOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const [choice, setChoice] = useState<FieldChoice | undefined>(() => step ? {
    choiceId: step.operation.relatedField.choiceId,
    source: step.operation.relatedField.source,
    label: savedOutput?.label ?? step.operation.relatedField.source.path,
  } : undefined);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? '');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? '');

  const { query, searchFor, choices, cursor, loading, error, loadMore } = useRelatedFieldChoices({
    project, explorerId, authResourcePath, snapshotToken, outputId, capabilities,
  });

  const emit = (nextChoice = choice, nextName = outputName, nextLabel = outputLabel) =>
    onCandidateChange(candidateFor(
      construction, capabilities, step, stepId, outputColumnId,
      nextChoice, nextName, nextLabel,
    ));

  if (!stage.activeRelatedRecord) {
    return <p role="status" className="text-sm text-slate-700">This table stage has no exact related record to read from.</p>;
  }

  useEscapeToClose(Boolean(onCancel), onCancel ?? (() => {}));
  return (
    <div
      className={presentation === 'standalone' ? 'grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4' : 'grid gap-4'}
      data-testid="construction-related-field-editor"
    >
      {presentation === 'standalone' ? (
        <div>
          <h4 className="font-semibold text-slate-900">From this row’s {stage.activeRelatedRecord.targetResourceType}</h4>
          <p className="mt-1 text-sm text-slate-600">Fields come from the exact related record that defines this row. Missing records produce an empty value. Preview shows the resulting values.</p>
        </div>
      ) : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        {presentation === 'standalone' ? 'Find a field' : 'Filter these fields (optional)'}
        <input value={query} disabled={disabled} onChange={(event) => searchFor(event.target.value)} placeholder="Search field name or path" className="rounded border border-slate-300 bg-white px-3 py-2 font-normal" />
      </label>
      {sourceControls}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {loading ? <p role="status" className="text-sm text-slate-600">Loading available fields…</p> : null}
      <div role="group" aria-label="Fields from this related record" className={fieldPickerList}>
        {choices.map((candidate) => (
          <button key={candidate.choiceId} type="button" disabled={disabled}
            aria-pressed={choice?.choiceId === candidate.choiceId}
            onClick={() => {
              const base = fieldName(candidate.source);
              const used = new Set(stage.columns.map((column) => column.name.toLowerCase()));
              let name = base;
              for (let suffix = 2; used.has(name.toLowerCase()); suffix += 1) name = `${base}_${suffix}`;
              setChoice(candidate);
              setOutputName(name);
              setOutputLabel(candidate.label);
              emit(candidate, name, candidate.label);
            }}
            className={`${fieldPickerRow} ${choice?.choiceId === candidate.choiceId ? fieldPickerSelected : ''} disabled:opacity-50`}>
            <span className="min-w-0 flex-1 truncate">{candidate.label}</span>
            <span className={`${fieldPickerMetadata} max-w-[45%] truncate`} title={candidate.source.path}>{candidate.source.path} · {candidate.source.logicalType}</span>
          </button>
        ))}
        {!loading && choices.length === 0 && !error ? <p className="text-sm text-slate-600">
          {cursor
            ? query.trim()
              ? 'No matching fields on this page. Browse more fields or refine your search.'
              : 'No supported fields on this page. Browse more fields to see the remaining options.'
            : query.trim()
              ? 'No executable fields match this search.'
              : 'No supported fields are available from this related record.'}
        </p> : null}
      </div>
      {cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start rounded border border-slate-300 bg-white px-3 py-2 text-sm">
        {choices.length === 0 ? 'Browse next page of fields' : 'Load more fields'}
      </button> : null}
      {choice ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-1 text-sm font-medium">Column name
            <input value={outputName} disabled={disabled} onChange={(event) => { setOutputName(event.target.value); emit(choice, event.target.value, outputLabel); }} className="rounded border border-slate-300 bg-white px-3 py-2 font-normal" />
          </label>
          <label className="grid gap-1 text-sm font-medium">Column label
            <input value={outputLabel} disabled={disabled} onChange={(event) => { setOutputLabel(event.target.value); emit(choice, outputName, event.target.value); }} className="rounded border border-slate-300 bg-white px-3 py-2 font-normal" />
          </label>
        </div>
      ) : null}
      {onCancel ? <button type="button" onClick={onCancel} className="justify-self-start text-sm text-slate-600 underline">Close editor</button> : null}
    </div>
  );
};
