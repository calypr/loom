import React, { useEffect, useRef, useState } from 'react';
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
import { RelatedExpandContributorEditor, type ContributorChoice, type ContributorCondition } from './RelatedExpandContributorEditor';
import { relationshipLabel } from '../constructionWorkspace/routeDisplay';

type RelatedExpandOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_EXPAND' }>;
type RelatedExpandStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedExpandOperation };
type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;
type EmptyPolicy = NonNullable<RelatedExpandOperation['relatedExpand']['emptyPolicy']>;

const newId = (prefix: string): string => `${prefix}_${globalThis.crypto.randomUUID()}`;

const routeLabel = (choice: RouteChoice): string => choice.route.map((hop) => {
  return `${hop.fromResourceType} to ${hop.toResourceType} through ${relationshipLabel(hop).toLowerCase() || 'relationship'}`;
}).join(' then ');

const availableColumnName = (base: string, columns: ReadonlyArray<{ readonly name: string }>): string => {
  const used = new Set(columns.map((column) => column.name.toLowerCase()));
  let name = base;
  for (let suffix = 2; used.has(name.toLowerCase()); suffix += 1) name = `${base}_${suffix}`;
  return name;
};

const emptyMatchEffect: Record<EmptyPolicy, string> = {
  PRESERVE_PARENT: 'A parent with no match stays as one row with no related record ID.',
  EXCLUDE: 'A parent with no match is left out.',
  ERROR: 'The preview stops if a parent has no match.',
};

const choicesMatchRequest = (
  result: RelatedExpandChoiceSearchResponse,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
  outputId: string,
  stageId: string,
  anchorColumnId: string,
  targetResourceType: string,
): boolean => result.snapshotToken === snapshotToken
  && result.draftVersion === draftVersion && result.draftDigest === draftDigest
  && result.outputId === outputId
  && result.stageId === stageId
  && result.choices.every((item) => item.anchorColumnId === anchorColumnId && item.targetResourceType === targetResourceType);

const candidateFor = (
  construction: Construction,
  stage: ConstructionCapabilitiesResponse['selectedStage'],
  step: RelatedExpandStep | undefined,
  stepId: string,
  outputColumnId: string,
  choice: RouteChoice | undefined,
  anchorColumnId: string,
  condition: ContributorCondition,
  emptyPolicy: EmptyPolicy,
  outputName: string,
  outputLabel: string,
): CandidateIntent | undefined => {
  const name = outputName.trim();
  const label = outputLabel.trim();
  if (!anchorColumnId || !choice || choice.anchorColumnId !== anchorColumnId ||
      condition.kind === 'CHOOSE' || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || !label) return undefined;
  if (condition.kind === 'EQUALS' && !condition.value.trim()) return undefined;
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
  const contributorRule = condition.kind === 'ALL'
    ? { policy: 'ALL_MATCHES' as const }
    : { policy: 'ALL_MATCHES' as const, predicate: condition.kind === 'EXISTS'
      ? { candidateId: condition.choice.source.candidateId, operator: 'EXISTS' as const }
      : { candidateId: condition.choice.source.candidateId, operator: 'EQUALS' as const,
        value: condition.choice.source.logicalType === 'code'
          ? { kind: 'CODE' as const, code: { code: condition.value.trim() } }
          : { kind: 'STRING' as const, string: condition.value.trim() } } };
  const nextStep: RelatedExpandStep = {
    id: stepId,
    inputs: [stage.id === 'source_projection'
      ? { kind: 'SOURCE_PROJECTION' }
      : { kind: 'STEP_OUTPUT', stepId: stage.id }],
    operation: {
      kind: 'RELATED_EXPAND',
      relatedExpand: {
        anchorColumnId,
        choiceId: choice.choiceId,
        targetNodeId: choice.targetNodeId,
        targetResourceType: choice.targetResourceType,
        route: choice.route,
        contributorRule,
        ...(condition.kind === 'ALL' ? {} : {
          contributorSource: condition.choice.source,
          contributorChoiceId: condition.choice.choiceId,
        }),
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
  const anchors = stage.relatedExpandAnchors ?? (stage.activeRelatedRecord ? [] : [{
    anchorColumnId: '_key', kind: 'root' as const, resourceType: '', label: 'Original table record',
  }]);
  const savedAnchor = anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId);
  const [anchorColumnId, setAnchorColumnId] = useState(() => anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId)?.anchorColumnId
    ?? anchors.find((anchor) => anchor.kind === 'activeRelatedRecord')?.anchorColumnId
    ?? anchors[0]?.anchorColumnId ?? '');
  const [stepId] = useState(() => step?.id ?? newId('related_expand'));
  const [outputColumnId] = useState(() => saved?.relatedRecordColumnId ?? newId('related_record'));
  const [targetResourceType, setTargetResourceType] = useState(saved?.targetResourceType ?? '');
  const [choice, setChoice] = useState<RouteChoice | undefined>(() => saved && savedAnchor ? {
    choiceId: saved.choiceId,
    anchorColumnId: savedAnchor.anchorColumnId,
    kind: savedAnchor.kind,
    nodeId: savedAnchor.nodeId ?? saved.route[0].fromNodeId,
    resourceType: savedAnchor.resourceType || saved.route[0].fromResourceType,
    label: savedAnchor.label,
    targetNodeId: saved.targetNodeId,
    targetResourceType: saved.targetResourceType,
    route: saved.route,
  } : undefined);
  const [condition, setCondition] = useState<ContributorCondition>(() => {
    const predicate = saved?.contributorRule.predicate;
    const source = saved?.contributorSource;
    if (!predicate || !source || !saved?.contributorChoiceId) return { kind: 'ALL' };
    const savedChoice: ContributorChoice = {
      choiceId: saved.contributorChoiceId, source, label: source.path,
      operators: source.logicalType === 'string' || source.logicalType === 'code' ? ['EXISTS', 'EQUALS'] : ['EXISTS'],
      suggestedValues: [], suggestionsComplete: false, suggestionsTruncated: false, suggestionsSource: 'catalog',
    };
    if (predicate.operator === 'EXISTS') return { kind: 'EXISTS', choice: savedChoice };
    if (predicate.operator === 'EQUALS' && predicate.value) {
      return { kind: 'EQUALS', choice: savedChoice,
        value: predicate.value.kind === 'CODE' ? predicate.value.code.code : predicate.value.kind === 'STRING' ? predicate.value.string : '' };
    }
    return { kind: 'ALL' };
  });
  const [choices, setChoices] = useState<ReadonlyArray<RouteChoice>>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const moreController = useRef<AbortController | undefined>(undefined);
  const requestVersion = useRef(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [emptyPolicy, setEmptyPolicy] = useState<EmptyPolicy>(saved?.emptyPolicy ?? 'PRESERVE_PARENT');
  const savedOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? '');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? '');
  const targetTypes = [...new Set(catalog.nodes.map((node) => node.resourceType))].sort();
  const selectedAnchor = anchors.find((anchor) => anchor.anchorColumnId === anchorColumnId);
  const matchingRowEffect = condition.kind === 'ALL'
    ? `Each matching ${targetResourceType} record gets its own row.`
    : `Each ${targetResourceType} record that meets your condition gets its own row.`;
  const expansionEffect = !targetResourceType
    ? 'Each matching related record gets its own row. Multiple matches produce multiple rows.'
    : condition.kind === 'CHOOSE'
      ? 'Choose a condition in Advanced options to preview the expansion.'
      : `${matchingRowEffect} Multiple matches produce multiple rows. Existing columns stay on each row. ${emptyMatchEffect[emptyPolicy]}`;

  useEffect(() => () => moreController.current?.abort(), [targetResourceType, stage.id, anchorColumnId]);

  useEffect(() => {
    if (!targetResourceType || !anchorColumnId) return;
    const controller = new AbortController();
    const version = ++requestVersion.current;
    setLoading(true);
    setError('');
    void client.searchRelatedExpandChoices({
      project, explorerId, authResourcePath, snapshotToken, outputId,
      expectedDraftVersion: capabilities.draftVersion,
      expectedDraftDigest: capabilities.draftDigest,
      stageId: stage.id, anchorColumnId, targetResourceType, limit: 10,
    }, controller.signal).then((result) => {
      if (controller.signal.aborted || version !== requestVersion.current) return;
      if (!choicesMatchRequest(result, snapshotToken, capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType)) {
        throw new Error('The available paths changed. Reload this table before expanding records.');
      }
      setChoices(result.choices);
      setCursor(result.nextCursor);
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Could not load related paths.');
      }
    }).finally(() => {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    });
    return () => controller.abort();
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, capabilities.draftVersion, capabilities.draftDigest, stage.id, anchorColumnId, targetResourceType]);

  const emit = (
    nextChoice = choice,
    nextEmptyPolicy = emptyPolicy,
    nextName = outputName,
    nextLabel = outputLabel,
    nextCondition = condition,
  ) => onCandidateChange(candidateFor(
    construction, stage, step, stepId, outputColumnId,
    nextChoice, anchorColumnId, nextCondition, nextEmptyPolicy, nextName, nextLabel,
  ));

  const loadMore = async () => {
    if (!cursor || !targetResourceType || !anchorColumnId || loading) return;
    moreController.current?.abort();
    const controller = new AbortController();
    moreController.current = controller;
    const version = requestVersion.current;
    setLoading(true);
    setError('');
    try {
      const result = await client.searchRelatedExpandChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, anchorColumnId, targetResourceType, limit: 10, cursor,
      }, controller.signal);
      if (controller.signal.aborted || version !== requestVersion.current) return;
      if (!choicesMatchRequest(result, snapshotToken, capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType)) {
        throw new Error('The available paths changed. Reload this table before expanding records.');
      }
      setChoices((current) => [...current, ...result.choices]);
      setCursor(result.nextCursor);
    } catch (cause) {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Could not load more paths.');
      }
    } finally {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    }
  };

  return (
    <div className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4" data-testid="construction-related-expand-editor">
      <div>
        <h4 className="font-semibold text-slate-900">One row per related record</h4>
        <p className="mt-1 text-sm text-slate-600">Choose a related record type and path. Every matching record becomes a row.</p>
      </div>
      {anchors.length === 0 ? <p role="status" className="text-sm text-slate-600">
        Loom has not confirmed a starting record for this stage. Reload the table to check available paths.
      </p> : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        Related record type
        <select value={targetResourceType} disabled={disabled || anchors.length === 0} onChange={(event) => {
          const target = event.target.value;
          requestVersion.current += 1;
          moreController.current?.abort();
          setTargetResourceType(target);
          setChoice(undefined);
          setCondition({ kind: 'ALL' });
          setChoices([]);
          setCursor(undefined);
          const suggested = availableColumnName(`related_${target.toLowerCase()}_id`, stage.columns);
          setOutputName(suggested);
          setOutputLabel(`${target} FHIR resource ID`);
          emit(undefined, emptyPolicy, suggested, `${target} FHIR resource ID`);
        }} className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="">Choose a record type</option>
          {targetTypes.map((target) => <option key={target} value={target}>{target}</option>)}
        </select>
      </label>
      {loading ? <p role="status" className="text-sm text-slate-600">Finding supported paths…</p> : null}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {targetResourceType && !loading && choices.length === 0 && !choice && !cursor && !error
        ? <p role="status" className="text-sm text-slate-600">No supported path reaches this record type from these rows.</p>
        : null}
      {choices.length > 0 || choice || cursor ? (
        <fieldset className="grid gap-2 text-sm">
          <legend className="font-medium text-slate-800">Relationship path</legend>
          {[...choices, ...(choice && !choices.some((item) => item.choiceId === choice.choiceId) ? [choice] : [])].map((item) => (
            <label key={item.choiceId} className="flex gap-2 rounded border border-slate-200 bg-white p-2">
              <input type="radio" name={`related-expand-route-${stepId}`} checked={choice?.choiceId === item.choiceId}
                disabled={disabled} onChange={() => { setChoice(item); setCondition({ kind: 'ALL' }); emit(item, emptyPolicy, outputName, outputLabel, { kind: 'ALL' }); }} />
              <span>{routeLabel(item)}</span>
            </label>
          ))}
          {cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start text-blue-800">Load more paths</button> : null}
        </fieldset>
      ) : null}
      <p role="status" data-testid="construction-related-expand-effect" className="text-sm text-slate-600">
        {selectedAnchor ? `Starting from ${selectedAnchor.label}. ` : ''}{expansionEffect}
      </p>
      <details data-testid="construction-related-expand-advanced" className="rounded-lg border border-slate-200">
        <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-700">Advanced options</summary>
        <div className="grid gap-3 p-3 pt-0">
          {anchors.length > 1 ? <label className="grid gap-1 text-sm font-medium text-slate-800">
            Start from
            <select value={anchorColumnId} disabled={disabled} onChange={(event) => {
              requestVersion.current += 1;
              moreController.current?.abort();
              setAnchorColumnId(event.target.value);
              setChoice(undefined);
              setCondition({ kind: 'ALL' });
              setChoices([]);
              setCursor(undefined);
              onCandidateChange(undefined);
            }} className="rounded border border-slate-300 bg-white px-3 py-2">
              {anchors.map((anchor) => <option key={anchor.anchorColumnId} value={anchor.anchorColumnId}>{anchor.label}</option>)}
            </select>
          </label> : null}
          {choice ? <RelatedExpandContributorEditor
            key={choice.choiceId}
            project={project} explorerId={explorerId} authResourcePath={authResourcePath}
            snapshotToken={snapshotToken} draftVersion={capabilities.draftVersion} draftDigest={capabilities.draftDigest}
            outputId={outputId} stageId={stage.id} routeChoiceId={choice.choiceId}
            targetNodeId={choice.targetNodeId} targetResourceType={choice.targetResourceType}
            condition={condition} disabled={disabled}
            onChange={(nextCondition) => { setCondition(nextCondition); emit(choice, emptyPolicy, outputName, outputLabel, nextCondition); }}
          /> : null}
          <label className="grid gap-1 text-sm font-medium text-slate-800">
            When a parent has no matching record
            <select value={emptyPolicy} disabled={disabled} onChange={(event) => {
              const next = event.target.value as EmptyPolicy;
              setEmptyPolicy(next);
              emit(choice, next);
            }} className="rounded border border-slate-300 bg-white px-3 py-2">
              <option value="EXCLUDE">Omit that parent</option>
              <option value="PRESERVE_PARENT">Keep one row with no related record</option>
              <option value="ERROR">Stop if any parent has no match</option>
            </select>
          </label>
          <label className="grid gap-1 text-sm font-medium text-slate-800">Related FHIR resource ID column
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
        </div>
      </details>
      <p className="text-xs text-slate-600">The proposal preview shows the new rows before Apply.</p>
    </div>
  );
};
