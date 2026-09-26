import React, { useEffect, useRef, useState } from 'react';
import { useLoomClient } from '../../../react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStep,
  RelatedFieldChoiceSearchResponse,
} from '../../../types';
import { constructionSchema } from '../../../types';

type RelatedFieldOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_FIELD' }>;
export type RelatedFieldStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedFieldOperation };
type FieldChoice = RelatedFieldChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;

const fieldName = (source: FieldChoice['source']): string => {
  const suffix = source.path.replace(/^.*\./, '').replace(/[^A-Za-z0-9_]/g, '_');
  return `related_${source.resourceType.toLowerCase()}_${suffix}`;
};

const matchesRequest = (
  response: RelatedFieldChoiceSearchResponse,
  snapshotToken: string,
  capabilities: ConstructionCapabilitiesResponse,
  outputId: string,
): boolean => response.snapshotToken === snapshotToken
  && response.draftVersion === capabilities.draftVersion
  && response.draftDigest === capabilities.draftDigest
  && response.outputId === outputId
  && response.stageId === capabilities.selectedStage.id;

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
    inputs: [stage.id === 'source_projection'
      ? { kind: 'SOURCE_PROJECTION' }
      : { kind: 'STEP_OUTPUT', stepId: stage.id }],
    operation: {
      kind: 'RELATED_FIELD',
      relatedField: { choiceId: choice.choiceId, source: choice.source, outputColumnId },
    },
    outputs,
  };
  const parsed = constructionSchema.safeParse({
    version: construction.version,
    steps: step
      ? construction.steps.map((current) => current.id === step.id ? nextStep : current)
      : [...construction.steps, nextStep],
  });
  return parsed.success
    ? { candidateConstruction: parsed.data, changedStepId: stepId }
    : undefined;
};

export const RelatedFieldEditor = ({
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
}: {
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
}) => {
  const client = useLoomClient();
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
  const [query, setQuery] = useState('');
  const [choices, setChoices] = useState<ReadonlyArray<FieldChoice>>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestVersion = useRef(0);
  const moreController = useRef<AbortController | undefined>(undefined);

  useEffect(() => () => moreController.current?.abort(), [stage.id]);
  useEffect(() => {
    if (!stage.activeRelatedRecord) return;
    const controller = new AbortController();
    const version = ++requestVersion.current;
    setLoading(true);
    setError('');
    const timeout = globalThis.setTimeout(() => {
      void client.searchRelatedFieldChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, query: query.trim(), limit: 20,
      }, controller.signal).then((response) => {
        if (controller.signal.aborted || version !== requestVersion.current) return;
        if (!matchesRequest(response, snapshotToken, capabilities, outputId)) {
          throw new Error('Available fields changed. Reload this table before adding a column.');
        }
        setChoices(response.choices.filter((item) =>
          item.source.nodeId === stage.activeRelatedRecord?.targetNodeId &&
          item.source.resourceType === stage.activeRelatedRecord?.targetResourceType &&
          (item.source.cardinality === 'optional_one' || item.source.cardinality === 'required_one'),
        ));
        setCursor(response.nextCursor);
      }).catch((cause: unknown) => {
        if (!controller.signal.aborted && version === requestVersion.current) {
          setError(cause instanceof Error ? cause.message : 'Could not load fields from this record.');
        }
      }).finally(() => {
        if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
      });
    }, query ? 180 : 0);
    return () => { globalThis.clearTimeout(timeout); controller.abort(); };
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, capabilities, stage.id, stage.activeRelatedRecord, query]);

  const emit = (nextChoice = choice, nextName = outputName, nextLabel = outputLabel) =>
    onCandidateChange(candidateFor(
      construction, capabilities, step, stepId, outputColumnId,
      nextChoice, nextName, nextLabel,
    ));

  const loadMore = async () => {
    if (!cursor || loading) return;
    moreController.current?.abort();
    const controller = new AbortController();
    moreController.current = controller;
    const version = requestVersion.current;
    setLoading(true);
    setError('');
    try {
      const response = await client.searchRelatedFieldChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, query: query.trim(), limit: 20, cursor,
      }, controller.signal);
      if (controller.signal.aborted || version !== requestVersion.current) return;
      if (!matchesRequest(response, snapshotToken, capabilities, outputId)) {
        throw new Error('Available fields changed. Reload this table before adding a column.');
      }
      setChoices((current) => [...current, ...response.choices.filter((item) =>
        item.source.nodeId === stage.activeRelatedRecord?.targetNodeId &&
        item.source.resourceType === stage.activeRelatedRecord?.targetResourceType &&
        (item.source.cardinality === 'optional_one' || item.source.cardinality === 'required_one'),
      )]);
      setCursor(response.nextCursor);
    } catch (cause) {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Could not load more fields.');
      }
    } finally {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    }
  };

  if (!stage.activeRelatedRecord) {
    return <p role="status" className="text-sm text-slate-700">This table stage has no exact related record to read from.</p>;
  }

  return (
    <div className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4" data-testid="construction-related-field-editor">
      <div>
        <h4 className="font-semibold text-slate-900">From this row’s {stage.activeRelatedRecord.targetResourceType}</h4>
        <p className="mt-1 text-sm text-slate-600">Fields come from the exact related record that defines this row. Missing records produce an empty value. Preview shows the resulting values.</p>
      </div>
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        Find a field
        <input value={query} disabled={disabled} onChange={(event) => {
          requestVersion.current += 1;
          moreController.current?.abort();
          setQuery(event.target.value);
          setChoices([]);
          setCursor(undefined);
        }} placeholder="Search field name or path" className="rounded border border-slate-300 bg-white px-3 py-2 font-normal" />
      </label>
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {loading ? <p role="status" className="text-sm text-slate-600">Loading available fields…</p> : null}
      <div role="group" aria-label="Fields from this related record" className="max-h-64 space-y-2 overflow-auto">
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
            className={`block w-full rounded border p-3 text-left text-sm ${choice?.choiceId === candidate.choiceId ? 'border-blue-600 bg-blue-50' : 'border-slate-200 bg-white hover:border-blue-300'}`}>
            <span className="block font-medium text-slate-900">{candidate.label}</span>
            <span className="block text-xs text-slate-600">{candidate.source.path} · {candidate.source.logicalType}</span>
          </button>
        ))}
        {!loading && choices.length === 0 && !error ? <p className="text-sm text-slate-600">No executable fields match this search.</p> : null}
      </div>
      {cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start rounded border border-slate-300 bg-white px-3 py-2 text-sm">Load more fields</button> : null}
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
