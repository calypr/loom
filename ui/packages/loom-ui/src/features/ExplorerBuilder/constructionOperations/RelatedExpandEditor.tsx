import React, { useEffect, useState } from 'react';
import { useLoomClient } from '../../../react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStep,
  ExplorerBuilderCatalog,
  RelatedExpandChoiceSearchResponse,
} from '../../../types';
import { constructionSchema } from '../../../types';

type RelatedExpandOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_EXPAND' }>;
type RelatedExpandStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedExpandOperation };
type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;
type EmptyPolicy = RelatedExpandOperation['relatedExpand']['emptyPolicy'];

const newId = (prefix: string): string => `${prefix}_${globalThis.crypto.randomUUID()}`;

const routeLabel = (choice: RouteChoice): string =>
  choice.route.map((hop) => `${hop.toResourceType} via ${hop.relationship}`).join(' → ');

const candidateFor = (
  construction: Construction,
  stage: ConstructionCapabilitiesResponse['selectedStage'],
  step: RelatedExpandStep | undefined,
  stepId: string,
  outputColumnId: string,
  choice: RouteChoice | undefined,
  emptyPolicy: EmptyPolicy | undefined,
  outputName: string,
  outputLabel: string,
): CandidateIntent | undefined => {
  const name = outputName.trim();
  const label = outputLabel.trim();
  if (!choice || !emptyPolicy || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || !label) return undefined;
  if (stage.columns.some((column) => column.name.toLowerCase() === name.toLowerCase())) return undefined;
  const priorOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const outputs = step
    ? step.outputs.map((column) => column.id === outputColumnId
      ? { ...column, name, label, type: 'string' }
      : column)
    : [
      ...stage.columns.map(({ id, name: columnName, label: columnLabel, type }) => ({
        id, name: columnName, label: columnLabel, ...(type ? { type } : {}),
      })),
      { id: outputColumnId, name, label, type: 'string' },
    ];
  if (step && !priorOutput) return undefined;
  const nextStep: RelatedExpandStep = {
    id: stepId,
    inputs: [stage.id === 'source_projection'
      ? { kind: 'SOURCE_PROJECTION' }
      : { kind: 'STEP_OUTPUT', stepId: stage.id }],
    operation: {
      kind: 'RELATED_EXPAND',
      relatedExpand: {
        anchorColumnId: '_key',
        choiceId: choice.choiceId,
        targetNodeId: choice.targetNodeId,
        targetResourceType: choice.targetResourceType,
        route: choice.route,
        contributorRule: step?.operation.relatedExpand.contributorRule ?? { policy: 'ALL_MATCHES' },
        ...(step?.operation.relatedExpand.contributorSource
          ? { contributorSource: step.operation.relatedExpand.contributorSource }
          : {}),
        ...(step?.operation.relatedExpand.contributorChoiceId
          ? { contributorChoiceId: step.operation.relatedExpand.contributorChoiceId }
          : {}),
        emptyPolicy,
        relatedRecordColumnId: outputColumnId,
      },
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

export const RelatedExpandEditor = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  catalog,
  construction,
  capabilities,
  step,
  disabled,
  onCandidateChange,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly catalog: ExplorerBuilderCatalog;
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly step?: RelatedExpandStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
}) => {
  const client = useLoomClient();
  const stage = capabilities.selectedStage;
  const saved = step?.operation.relatedExpand;
  const [stepId] = useState(() => step?.id ?? newId('related_expand'));
  const [outputColumnId] = useState(() => saved?.relatedRecordColumnId ?? newId('related_record'));
  const [targetResourceType, setTargetResourceType] = useState(saved?.targetResourceType ?? '');
  const [choice, setChoice] = useState<RouteChoice | undefined>(() => saved ? {
    choiceId: saved.choiceId,
    targetNodeId: saved.targetNodeId,
    targetResourceType: saved.targetResourceType,
    route: saved.route,
  } : undefined);
  const [choices, setChoices] = useState<ReadonlyArray<RouteChoice>>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [emptyPolicy, setEmptyPolicy] = useState<EmptyPolicy | undefined>(saved?.emptyPolicy);
  const savedOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? '');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? '');
  const targetTypes = [...new Set(catalog.nodes
    .filter((node) => node.populated)
    .map((node) => node.resourceType))].sort();

  useEffect(() => {
    if (!targetResourceType) return;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    void client.searchRelatedExpandChoices({
      project, explorerId, authResourcePath, snapshotToken, outputId,
      stageId: stage.id, targetResourceType, limit: 10,
    }, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      if (result.snapshotToken !== snapshotToken || result.outputId !== outputId || result.stageId !== stage.id) {
        throw new Error('The available paths changed. Reload this table before expanding records.');
      }
      setChoices(result.choices);
      setCursor(result.nextCursor);
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load related paths.');
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, stage.id, targetResourceType]);

  const emit = (
    nextChoice = choice,
    nextEmptyPolicy = emptyPolicy,
    nextName = outputName,
    nextLabel = outputLabel,
  ) => onCandidateChange(candidateFor(
    construction, stage, step, stepId, outputColumnId,
    nextChoice, nextEmptyPolicy, nextName, nextLabel,
  ));

  const loadMore = async () => {
    if (!cursor || !targetResourceType || loading) return;
    setLoading(true);
    setError('');
    try {
      const result = await client.searchRelatedExpandChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        stageId: stage.id, targetResourceType, limit: 10, cursor,
      });
      if (result.snapshotToken !== snapshotToken || result.outputId !== outputId || result.stageId !== stage.id) {
        throw new Error('The available paths changed. Reload this table before expanding records.');
      }
      setChoices((current) => [...current, ...result.choices]);
      setCursor(result.nextCursor);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load more paths.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4" data-testid="construction-related-expand-editor">
      <div>
        <h4 className="font-semibold text-slate-900">One row per related record</h4>
        <p className="mt-1 text-sm text-slate-600">Choose a related record type and path. Each matching source record becomes one row with its parent retained.</p>
      </div>
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        Related record type
        <select value={targetResourceType} disabled={disabled} onChange={(event) => {
          const target = event.target.value;
          setTargetResourceType(target);
          setChoice(undefined);
          setChoices([]);
          setCursor(undefined);
          const suggested = `related_${target.toLowerCase()}_id`;
          setOutputName(suggested);
          setOutputLabel(`${target} record ID`);
          emit(undefined, emptyPolicy, suggested, `${target} record ID`);
        }} className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="">Choose a record type</option>
          {targetTypes.map((target) => <option key={target} value={target}>{target}</option>)}
        </select>
      </label>
      {loading ? <p role="status" className="text-sm text-slate-600">Finding supported paths…</p> : null}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {targetResourceType && !loading && choices.length === 0 && !choice && !error
        ? <p role="status" className="text-sm text-slate-600">No supported path reaches this record type from these rows.</p>
        : null}
      {choices.length > 0 || choice ? (
        <fieldset className="grid gap-2 text-sm">
          <legend className="font-medium text-slate-800">Relationship path</legend>
          {[...choices, ...(choice && !choices.some((item) => item.choiceId === choice.choiceId) ? [choice] : [])].map((item) => (
            <label key={item.choiceId} className="flex gap-2 rounded border border-slate-200 bg-white p-2">
              <input type="radio" name={`related-expand-route-${stepId}`} checked={choice?.choiceId === item.choiceId}
                disabled={disabled} onChange={() => { setChoice(item); emit(item); }} />
              <span>{routeLabel(item)}</span>
            </label>
          ))}
          {cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start text-blue-800">Load more paths</button> : null}
        </fieldset>
      ) : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        When a parent has no matching record
        <select value={emptyPolicy ?? ''} disabled={disabled} onChange={(event) => {
          const next = event.target.value as EmptyPolicy | '';
          setEmptyPolicy(next || undefined);
          emit(choice, next || undefined);
        }} className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="">Choose what happens</option>
          <option value="EXCLUDE">Omit that parent</option>
          <option value="PRESERVE_PARENT">Keep one row with no related record</option>
          <option value="ERROR">Stop if any parent has no match</option>
        </select>
      </label>
      <label className="grid gap-1 text-sm font-medium text-slate-800">Related record ID column
        <input value={outputName} disabled={disabled} onChange={(event) => {
          setOutputName(event.target.value);
          emit(choice, emptyPolicy, event.target.value);
        }} className="rounded border border-slate-300 bg-white px-3 py-2" />
      </label>
      <label className="grid gap-1 text-sm font-medium text-slate-800">Column label
        <input value={outputLabel} disabled={disabled} onChange={(event) => {
          setOutputLabel(event.target.value);
          emit(choice, emptyPolicy, outputName, event.target.value);
        }} className="rounded border border-slate-300 bg-white px-3 py-2" />
      </label>
      <p className="text-xs text-slate-600">The proposal preview shows the new rows before Apply. Later columns can use the exact related record on each row.</p>
    </div>
  );
};
