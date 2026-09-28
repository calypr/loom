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
import { relationshipLabel, routeMeaning } from '../constructionWorkspace/routeDisplay';

type EligibilityOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_ELIGIBILITY' }>;
export type EligibilityStep = Omit<ConstructionStep, 'operation'> & { readonly operation: EligibilityOperation };
type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];
type Match = EligibilityOperation['relatedEligibility']['match'];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;

const routeLabel = (choice: RouteChoice): string => choice.route.map((hop) =>
  `${hop.fromResourceType} to ${hop.toResourceType} through ${relationshipLabel(hop).toLowerCase() || 'relationship'}`,
).join(' then ');

const savedCondition = (step: EligibilityStep | undefined): ContributorCondition => {
  const eligibility = step?.operation.relatedEligibility;
  const predicate = eligibility?.contributorRule.predicate;
  const source = eligibility?.contributorSource;
  if (!predicate || !source || !eligibility?.contributorChoiceId) return { kind: 'ALL' };
  const choice: ContributorChoice = {
    choiceId: eligibility.contributorChoiceId,
    source,
    label: source.path,
    operators: source.logicalType === 'string' || source.logicalType === 'code' ? ['EXISTS', 'EQUALS'] : ['EXISTS'],
    suggestedValues: [],
    suggestionsComplete: false,
    suggestionsTruncated: false,
    suggestionsSource: 'catalog',
  };
  if (predicate.operator === 'EXISTS') return { kind: 'EXISTS', choice };
  if (predicate.operator === 'EQUALS' && predicate.value) return {
    kind: 'EQUALS',
    choice,
    value: predicate.value.kind === 'CODE'
      ? predicate.value.code.code
      : predicate.value.kind === 'STRING' ? predicate.value.string : '',
  };
  return { kind: 'ALL' };
};

const candidateFor = (
  construction: Construction,
  stage: ConstructionCapabilitiesResponse['selectedStage'],
  step: EligibilityStep | undefined,
  stepId: string,
  anchorColumnId: string,
  choice: RouteChoice | undefined,
  match: Match,
  condition: ContributorCondition,
): CandidateIntent | undefined => {
  if (!choice || !anchorColumnId || choice.anchorColumnId !== anchorColumnId || condition.kind === 'CHOOSE') return undefined;
  if (condition.kind === 'EQUALS' && !condition.value.trim()) return undefined;
  const contributorRule = condition.kind === 'ALL'
    ? { policy: 'ALL_MATCHES' as const }
    : {
        policy: 'ALL_MATCHES' as const,
        predicate: condition.kind === 'EXISTS'
          ? { candidateId: condition.choice.source.candidateId, operator: 'EXISTS' as const }
          : {
              candidateId: condition.choice.source.candidateId,
              operator: 'EQUALS' as const,
              value: condition.choice.source.logicalType === 'code'
                ? { kind: 'CODE' as const, code: { code: condition.value.trim() } }
                : { kind: 'STRING' as const, string: condition.value.trim() },
            },
      };
  const nextStep: EligibilityStep = {
    id: stepId,
    inputs: [stage.id === 'source_projection'
      ? { kind: 'SOURCE_PROJECTION' }
      : { kind: 'STEP_OUTPUT', stepId: stage.id }],
    operation: {
      kind: 'RELATED_ELIGIBILITY',
      relatedEligibility: {
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
        match,
      },
    },
    outputs: stage.columns.map(({ id, name, label, type }) => ({
      id, name, label, ...(type ? { type } : {}),
    })),
  };
  const parsed = constructionSchema.safeParse({
    version: construction.version,
    steps: step
      ? construction.steps.map((current) => current.id === step.id ? nextStep : current)
      : [...construction.steps, nextStep],
  });
  return parsed.success ? { candidateConstruction: parsed.data, changedStepId: stepId } : undefined;
};

export const RelatedEligibilityEditor = ({
  project, explorerId, authResourcePath, snapshotToken, outputId, catalog,
  construction, capabilities, step, disabled, onCandidateChange,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly catalog: ExplorerBuilderCatalog;
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly step?: EligibilityStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
}) => {
  const client = useLoomClient();
  const stage = capabilities.selectedStage;
  const saved = step?.operation.relatedEligibility;
  const anchors = stage.relatedExpandAnchors ?? [];
  const [anchorColumnId, setAnchorColumnId] = useState(() =>
    anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId)?.anchorColumnId
    ?? anchors.find((anchor) => anchor.kind === 'activeRelatedRecord')?.anchorColumnId
    ?? anchors[0]?.anchorColumnId ?? '',
  );
  const [stepId] = useState(() => step?.id ?? `related_eligibility_${globalThis.crypto.randomUUID()}`);
  const [targetResourceType, setTargetResourceType] = useState(saved?.targetResourceType ?? '');
  const savedAnchor = anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId);
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
  const [match, setMatch] = useState<Match>(saved?.match ?? { kind: 'EXISTS' });
  const [condition, setCondition] = useState<ContributorCondition>(() => savedCondition(step));
  const [conditionOpen, setConditionOpen] = useState(savedCondition(step).kind !== 'ALL');
  const [choices, setChoices] = useState<ReadonlyArray<RouteChoice>>([]);
  const [cursor, setCursor] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestVersion = useRef(0);
  const moreController = useRef<AbortController | undefined>(undefined);
  const targetTypes = [...new Set(catalog.nodes.map((node) => node.resourceType))].sort();
  const support = stage.capabilities.find((capability) => capability.kind === 'RELATED_ELIGIBILITY');

  useEffect(() => () => moreController.current?.abort(), [targetResourceType, stage.id, anchorColumnId]);
  useEffect(() => {
    if (!support?.supported || !targetResourceType || !anchorColumnId) return;
    const controller = new AbortController();
    const version = ++requestVersion.current;
    setLoading(true);
    setError('');
    void client.searchRelatedExpandChoices({
      project, explorerId, authResourcePath, snapshotToken, outputId,
      expectedDraftVersion: capabilities.draftVersion,
      expectedDraftDigest: capabilities.draftDigest,
      stageId: stage.id, anchorColumnId, targetResourceType, limit: 10,
    }, controller.signal).then((response) => {
      if (controller.signal.aborted || version !== requestVersion.current) return;
      if (response.snapshotToken !== snapshotToken || response.draftVersion !== capabilities.draftVersion ||
          response.draftDigest !== capabilities.draftDigest || response.outputId !== outputId ||
          response.stageId !== stage.id || response.choices.some((item) =>
            item.anchorColumnId !== anchorColumnId || item.targetResourceType !== targetResourceType)) {
        throw new Error('The available paths changed. Reload this table to choose related records.');
      }
      setChoices(response.choices);
      setCursor(response.nextCursor);
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Could not load related paths.');
      }
    }).finally(() => {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    });
    return () => controller.abort();
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId,
    capabilities.draftVersion, capabilities.draftDigest, stage.id, anchorColumnId, targetResourceType, support?.supported]);

  const emit = (nextChoice = choice, nextMatch = match, nextCondition = condition, nextAnchor = anchorColumnId) =>
    onCandidateChange(candidateFor(construction, stage, step, stepId, nextAnchor, nextChoice, nextMatch, nextCondition));
  const loadMore = async () => {
    if (!cursor || !targetResourceType || !anchorColumnId || loading) return;
    moreController.current?.abort();
    const controller = new AbortController();
    moreController.current = controller;
    const version = requestVersion.current;
    setLoading(true);
    setError('');
    try {
      const response = await client.searchRelatedExpandChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId,
        expectedDraftVersion: capabilities.draftVersion,
        expectedDraftDigest: capabilities.draftDigest,
        stageId: stage.id, anchorColumnId, targetResourceType, limit: 10, cursor,
      }, controller.signal);
      if (controller.signal.aborted || version !== requestVersion.current) return;
      setChoices((current) => [...current, ...response.choices]);
      setCursor(response.nextCursor);
    } catch (cause) {
      if (!controller.signal.aborted && version === requestVersion.current) {
        setError(cause instanceof Error ? cause.message : 'Could not load more paths.');
      }
    } finally {
      if (!controller.signal.aborted && version === requestVersion.current) setLoading(false);
    }
  };

  const listedRoutes = [...choices, ...(choice && !choices.some((item) => item.choiceId === choice.choiceId) ? [choice] : [])];
  const shortestRoute = Math.min(...listedRoutes.map((item) => item.route.length));
  const directRoutes = listedRoutes.filter((item) => item.route.length === shortestRoute || item.choiceId === choice?.choiceId);
  const otherRoutes = listedRoutes.filter((item) => item.route.length > shortestRoute && item.choiceId !== choice?.choiceId);
  const renderRoute = (item: RouteChoice) => (
    <label key={item.choiceId} className="flex gap-2 rounded border border-slate-200 bg-white p-2 text-sm">
      <input type="radio" name={`eligibility-route-${stepId}`} aria-label={routeLabel(item)}
        checked={choice?.choiceId === item.choiceId} disabled={disabled}
        onChange={() => { setChoice(item); setCondition({ kind: 'ALL' }); emit(item, match, { kind: 'ALL' }); }} />
      <span className="grid gap-1"><span>{routeLabel(item)}</span><span className="text-xs text-slate-600">{routeMeaning(item.route)}</span></span>
    </label>
  );

  if (!support?.supported) return (
    <p role="status" data-testid="construction-related-eligibility-unavailable" className="rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
      {support?.reason ?? 'Loom has not confirmed related-record filtering for this stage.'}
    </p>
  );

  return (
    <section aria-label="Filter by related records" data-testid="construction-related-eligibility-editor" className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4">
      <div>
        <h3 className="font-semibold text-slate-900">Filter by related records</h3>
        <p className="mt-1 text-sm text-slate-600">Keep rows according to records linked to each row. This does not add columns or multiply rows.</p>
      </div>
      {anchors.length === 0 ? <p role="status" className="text-sm text-amber-900">Loom has not confirmed a related-record anchor for this stage.</p> : null}
      {anchors.length > 1 ? <label className="grid gap-1 text-sm font-medium text-slate-800">Start from
        <select aria-label="Related eligibility anchor" value={anchorColumnId} disabled={disabled}
          onChange={(event) => { setAnchorColumnId(event.target.value); setChoice(undefined); setCondition({ kind: 'ALL' }); emit(undefined, match, { kind: 'ALL' }, event.target.value); }}
          className="rounded border border-slate-300 bg-white px-3 py-2">
          {anchors.map((anchor) => <option key={anchor.anchorColumnId} value={anchor.anchorColumnId}>{anchor.label}</option>)}
        </select>
      </label> : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">Related record type
        <select aria-label="Related eligibility record type" value={targetResourceType} disabled={disabled || anchors.length === 0}
          onChange={(event) => { setTargetResourceType(event.target.value); setChoice(undefined); setCondition({ kind: 'ALL' }); setChoices([]); setCursor(undefined); emit(undefined, match, { kind: 'ALL' }); }}
          className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="">Choose a record type</option>
          {targetTypes.map((target) => <option key={target} value={target}>{target}</option>)}
        </select>
      </label>
      {loading ? <p role="status" className="text-sm text-slate-600">Finding supported paths…</p> : null}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {targetResourceType && !loading && !error && listedRoutes.length === 0 ? <p role="status" className="text-sm text-slate-600">No supported path reaches this record type from these rows.</p> : null}
      {directRoutes.length > 0 ? <fieldset className="grid gap-2"><legend className="text-sm font-medium text-slate-800">How are these records linked?</legend>{directRoutes.map(renderRoute)}</fieldset> : null}
      {otherRoutes.length > 0 || cursor ? <details className="text-sm"><summary className="cursor-pointer font-medium text-blue-800">Other paths</summary>
        <div className="mt-2 grid gap-2">{otherRoutes.map(renderRoute)}{cursor ? <button type="button" disabled={disabled || loading} onClick={() => void loadMore()} className="justify-self-start text-blue-800">Load more paths</button> : null}</div>
      </details> : null}
      {choice ? <>
        <label className="grid gap-1 text-sm font-medium text-slate-800">Keep rows where
          <select aria-label="Related eligibility rule" value={match.kind} disabled={disabled}
            onChange={(event) => { const next: Match = event.target.value === 'ABSENT'
              ? { kind: 'ABSENT' }
              : event.target.value === 'COUNT_AT_LEAST' ? { kind: 'COUNT_AT_LEAST', threshold: 2 } : { kind: 'EXISTS' };
              setMatch(next); emit(choice, next); }}
            className="rounded border border-slate-300 bg-white px-3 py-2">
            <option value="EXISTS">At least one related record matches</option>
            <option value="ABSENT">No related record matches</option>
            <option value="COUNT_AT_LEAST">At least a chosen number match</option>
          </select>
        </label>
        {match.kind === 'COUNT_AT_LEAST' ? <label className="grid gap-1 text-sm font-medium text-slate-800">Minimum matching records
          <input aria-label="Minimum matching records" type="number" min={1} step={1} value={match.threshold} disabled={disabled}
            onChange={(event) => { const threshold = Number(event.target.value); const next = { kind: 'COUNT_AT_LEAST' as const, threshold }; setMatch(next); emit(choice, next); }}
            className="rounded border border-slate-300 px-3 py-2" />
        </label> : null}
        <details open={conditionOpen} onToggle={(event) => setConditionOpen(event.currentTarget.open)} className="rounded border border-slate-200 bg-white p-3 text-sm">
          <summary className="cursor-pointer font-medium text-slate-800">Which related records count?</summary>
          {conditionOpen ? <div className="mt-3"><RelatedExpandContributorEditor
            project={project} explorerId={explorerId} authResourcePath={authResourcePath}
            snapshotToken={snapshotToken} draftVersion={capabilities.draftVersion} draftDigest={capabilities.draftDigest}
            outputId={outputId} stageId={stage.id} routeChoiceId={choice.choiceId}
            targetNodeId={choice.targetNodeId} targetResourceType={choice.targetResourceType}
            condition={condition} disabled={disabled}
            onChange={(next) => { setCondition(next); emit(choice, match, next); }}
          /></div> : null}
        </details>
        <p className="text-xs text-slate-600">Preview shows which current rows qualify. Rows without a match are kept only with “No related record matches.”</p>
      </> : null}
    </section>
  );
};
