import React, { useImperativeHandle, useMemo, useRef, useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
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
import { routePath } from '../constructionWorkspace/routeDisplay';
import { TraversalPath } from '../constructionWorkspace/TraversalPath';

type RelatedExpandOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_EXPAND' }>;
type RelatedExpandStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedExpandOperation };
type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;
type EmptyPolicy = NonNullable<RelatedExpandOperation['relatedExpand']['emptyPolicy']>;

export interface RelatedExpandQueryOwner {
  readonly draftVersion: number;
  readonly draftDigest: string;
  pauseAndDrain: () => Promise<void>;
  resume: () => void;
}

interface RouteQueryCheckpoint {
  readonly queryKey: string;
  readonly choices: ReadonlyArray<RouteChoice>;
  readonly cursor?: string;
  readonly seenCursors: ReadonlyArray<string>;
  readonly complete: boolean;
  readonly terminalTruncated: boolean;
}

interface ActiveRoutePage {
  readonly queryKey: string;
  readonly task: Promise<void>;
}

const newId = (prefix: string): string => `${prefix}_${globalThis.crypto.randomUUID()}`;

const contributorQuantifier = (choice: ContributorChoice) =>
  choice.source.cardinality === 'many' ? { quantifier: 'ANY' as const } : {};

const routeLabel = (choice: RouteChoice): string => routePath(choice.route);

const availableColumnName = (base: string, columns: ReadonlyArray<{ readonly name: string }>): string => {
  const used = new Set(columns.map((column) => column.name.toLowerCase()));
  let name = base;
  for (let suffix = 2; used.has(name.toLowerCase()); suffix += 1) name = `${base}_${suffix}`;
  return name;
};

const emptyMatchEffect: Record<EmptyPolicy, string> = {
  PRESERVE_PARENT: 'Keep that current row once, with no related record ID.',
  EXCLUDE: 'Leave that current row out.',
  ERROR: 'Stop with an error if any current row has no match.',
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
      ? { candidateId: condition.choice.source.candidateId, ...contributorQuantifier(condition.choice), operator: 'EXISTS' as const }
      : { candidateId: condition.choice.source.candidateId, ...contributorQuantifier(condition.choice), operator: 'EQUALS' as const,
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
  queryOwnerRef,
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
  readonly queryOwnerRef?: React.RefObject<RelatedExpandQueryOwner | null>;
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
  const [emptyPolicy, setEmptyPolicy] = useState<EmptyPolicy>(saved?.emptyPolicy ?? 'PRESERVE_PARENT');
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
  const [contributorOptionsOpen, setContributorOptionsOpen] = useState(condition.kind !== 'ALL');
  const [otherRoutesOpen, setOtherRoutesOpen] = useState(false);
  const routeQueryKey = JSON.stringify([
    project, explorerId, authResourcePath ?? '', snapshotToken,
    capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType,
  ]);
  const [loadedChoices, setLoadedChoices] = useState<{
    readonly queryKey: string;
    readonly choices: ReadonlyArray<RouteChoice>;
    readonly truncated: boolean;
  }>();
  const routeCheckpointRef = useRef<RouteQueryCheckpoint | undefined>(undefined);
  const routeQueryPausedRef = useRef(false);
  const pausedAtPageBoundaryRef = useRef(false);
  const activeRoutePageRef = useRef<ActiveRoutePage | undefined>(undefined);
  const choices = loadedChoices?.queryKey === routeQueryKey ? loadedChoices.choices : [];
  const routeSearchTruncated = loadedChoices?.queryKey === routeQueryKey && loadedChoices.truncated;
  const savedOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? '');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? '');
  const targetTypes = [...new Set(catalog.nodes.map((node) => node.resourceType))].sort();
  const selectedAnchor = anchors.find((anchor) => anchor.anchorColumnId === anchorColumnId);
  const startingAnchorLabel = selectedAnchor?.label ?? anchors[0]?.label;
  const matchingRowEffect = condition.kind === 'ALL'
    ? `For each current row, make one row for each matching ${targetResourceType} record.`
    : `For each current row, make one row for each ${targetResourceType} record that meets the selected condition.`;
  const noMatchDescription = targetResourceType
    ? `If a current row has no matching ${targetResourceType} records: `
    : 'If a current row has no matching records: ';
  const expansionEffect = !targetResourceType
    ? `Example: one current row with three matching records becomes three rows. Existing values on the current row repeat on each new row. ${noMatchDescription}${emptyMatchEffect[emptyPolicy]}`
    : condition.kind === 'CHOOSE'
      ? `Choose which ${targetResourceType} records count in the condition below. ${noMatchDescription}${emptyMatchEffect[emptyPolicy]}`
      : `${matchingRowEffect} Existing values on the current row repeat on each new row. After Apply, choose fields from the matched records in Add columns. ${noMatchDescription}${emptyMatchEffect[emptyPolicy]}`;

  const routeQuery = useQuery(async (signal) => {
    const currentCheckpoint = routeCheckpointRef.current?.queryKey === routeQueryKey
      ? routeCheckpointRef.current
      : { queryKey: routeQueryKey, choices: [], seenCursors: [], complete: false, terminalTruncated: false };
    let checkpoint: RouteQueryCheckpoint = currentCheckpoint;
    if (routeCheckpointRef.current !== currentCheckpoint) {
      routeCheckpointRef.current = checkpoint;
      routeQueryPausedRef.current = false;
      pausedAtPageBoundaryRef.current = false;
    }
    let cursor = checkpoint.cursor;
    let paths = [...checkpoint.choices];
    let complete = checkpoint.complete;
    let terminalTruncated = checkpoint.terminalTruncated;
    const seenCursors = new Set(checkpoint.seenCursors);
    while (!complete && !terminalTruncated) {
      if (signal.aborted) return paths;
      if (routeQueryPausedRef.current) {
        pausedAtPageBoundaryRef.current = true;
        return paths;
      }
      const task = (async () => {
        const result = await client.searchRelatedExpandChoices({
          project, explorerId, authResourcePath, snapshotToken, outputId,
          expectedDraftVersion: capabilities.draftVersion,
          expectedDraftDigest: capabilities.draftDigest,
          stageId: stage.id, anchorColumnId, targetResourceType, limit: 50,
          requestId: `related-expand-choices-${window.crypto.randomUUID()}`,
          ...(cursor ? { cursor } : {}),
        }, signal);
        if (signal.aborted) return;
        if (!choicesMatchRequest(result, snapshotToken, capabilities.draftVersion, capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType)) {
          throw new Error('The available paths changed. Reload this table before expanding records.');
        }
        paths = [...paths, ...result.choices];
        complete = result.complete;
        cursor = result.nextCursor;
        terminalTruncated = result.truncated && !cursor;
        setLoadedChoices({ queryKey: routeQueryKey, choices: paths, truncated: terminalTruncated });
        if (!complete && !cursor && !terminalTruncated) {
          throw new Error('Could not finish loading relationship paths. Reopen this editor to retry.');
        }
        if (cursor && seenCursors.has(cursor)) {
          throw new Error('Could not finish loading relationship paths. Reopen this editor to retry.');
        }
        if (cursor) seenCursors.add(cursor);
        checkpoint = {
          queryKey: routeQueryKey,
          choices: paths,
          ...(cursor ? { cursor } : {}),
          seenCursors: [...seenCursors],
          complete,
          terminalTruncated,
        };
        routeCheckpointRef.current = checkpoint;
      })();
      const activePage: ActiveRoutePage = { queryKey: routeQueryKey, task };
      activeRoutePageRef.current = activePage;
      try {
        await task;
      } finally {
        if (activeRoutePageRef.current === activePage) activeRoutePageRef.current = undefined;
      }
      if (signal.aborted) return paths;
      if (routeQueryPausedRef.current) {
        pausedAtPageBoundaryRef.current = !complete && !terminalTruncated;
        return paths;
      }
    }
    if (!complete && !terminalTruncated) throw new Error('Could not finish loading relationship paths. Reopen this editor to retry.');
    return paths;
  }, [client, authResourcePath, explorerId, project, snapshotToken, capabilities.draftVersion,
    capabilities.draftDigest, outputId, stage.id, anchorColumnId, targetResourceType],
  Boolean(targetResourceType && anchorColumnId));
  const queryOwner = useMemo<RelatedExpandQueryOwner>(() => ({
    draftVersion: capabilities.draftVersion,
    draftDigest: capabilities.draftDigest,
    pauseAndDrain: async () => {
      if (queryOwnerRef && queryOwnerRef.current !== queryOwner) return;
      routeQueryPausedRef.current = true;
      const activePage = activeRoutePageRef.current;
      if (activePage?.queryKey === routeQueryKey) {
        await activePage.task.then(() => undefined, () => undefined);
      }
    },
    resume: () => {
      if (queryOwnerRef && queryOwnerRef.current !== queryOwner) return;
      routeQueryPausedRef.current = false;
      if (!pausedAtPageBoundaryRef.current) return;
      pausedAtPageBoundaryRef.current = false;
      const checkpoint = routeCheckpointRef.current;
      if (checkpoint?.queryKey === routeQueryKey && !checkpoint.complete && !checkpoint.terminalTruncated) {
        void routeQuery.refetch();
      }
    },
  }), [capabilities.draftDigest, capabilities.draftVersion, queryOwnerRef, routeQuery.refetch, routeQueryKey]);
  useImperativeHandle(queryOwnerRef ?? null, () => queryOwner, [queryOwner]);
  const loading = routeQuery.isFetching;
  const error = routeQuery.error instanceof Error
    ? routeQuery.error.message
    : routeQuery.error ? 'Could not load related paths.' : '';


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

  const listedRoutes = [...choices, ...(choice && !choices.some((item) => item.choiceId === choice.choiceId) ? [choice] : [])];
  const shortestRoute = Math.min(...listedRoutes.map((item) => item.route.length));
  const visibleRoutes = choice ? listedRoutes.filter((item) => item.choiceId === choice.choiceId) : listedRoutes.filter((item) => item.route.length === shortestRoute);
  const otherRoutes = listedRoutes.filter((item) => !visibleRoutes.some((visible) => visible.choiceId === item.choiceId));
  const renderRoute = (item: RouteChoice) => (
    <label key={item.choiceId} className="flex gap-2 rounded border border-slate-200 bg-white p-2">
      <input type="radio" name={`related-expand-route-${stepId}`} aria-label={routeLabel(item)} checked={choice?.choiceId === item.choiceId}
        disabled={disabled} onChange={() => { setChoice(item); setCondition({ kind: 'ALL' }); emit(item, emptyPolicy, outputName, outputLabel, { kind: 'ALL' }); }} />
      <span className="grid gap-1">
        <TraversalPath route={item.route} referenceRoute={choice?.route ?? listedRoutes[0]?.route} />
      </span>
    </label>
  );

  return (
    <div className="scroll-mt-20 grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4" data-testid="construction-related-expand-editor" data-related-stage-id={stage.id} data-related-output-id={outputId}>
      <div>
        <h4 className="font-semibold text-slate-900">Make a row for each related record</h4>
        <p className="mt-1 text-sm text-slate-600">Choose the records you want each row to describe. Follow a relationship from a record already in the row, then make a separate row for each matching record.</p>
      </div>
      <section aria-label="How related records change the table" className="grid gap-3 rounded-lg border border-slate-200 bg-white p-3 sm:grid-cols-2">
        <div>
          <h5 className="text-sm font-semibold text-slate-900">Rows before and after</h5>
          <p className="mt-1 text-sm text-slate-700">Example: a Patient row links to two Observations.</p>
          <div className="mt-2 grid gap-1 text-sm text-slate-700">
            <p><span className="font-semibold">Before:</span> Patient A</p>
            <p><span className="font-semibold">After:</span> Patient A + Observation 1</p>
            <p className="pl-11">Patient A + Observation 2</p>
          </div>
          <p className="mt-2 text-xs text-slate-600">Existing column values repeat in both rows. Choose below what happens when there are no matches.</p>
        </div>
        <div>
          <h5 className="text-sm font-semibold text-slate-900">Fields you can add</h5>
          <p className="mt-1 text-sm text-slate-700">{targetResourceType
            ? `After applying, Add columns offers fields from the matching ${targetResourceType} record in each row.`
            : 'After applying, Add columns offers fields from the matching related record in each row.'}</p>
          <p className="mt-2 text-xs text-slate-600">For example, each Observation row can have its own measurement value. Choosing a different record type or relationship path changes which records those fields come from.</p>
        </div>
      </section>
      {anchors.length === 0 ? <p role="status" className="text-sm text-slate-600">
        Loom has not confirmed a starting record for this stage. Reload the table to check available paths.
      </p> : null}
      {anchors.length > 1 ? (
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          Start from
          <select aria-label="Start from" value={anchorColumnId} disabled={disabled} onChange={(event) => {
            setAnchorColumnId(event.target.value);
            setChoice(undefined);
            setCondition({ kind: 'ALL' });
            setOtherRoutesOpen(false);
            setLoadedChoices({ queryKey: routeQueryKey, choices: [], truncated: false });
            onCandidateChange(undefined);
          }} className="rounded border border-slate-300 bg-white px-3 py-2">
            {anchors.map((anchor) => <option key={anchor.anchorColumnId} value={anchor.anchorColumnId}>{anchor.label}</option>)}
          </select>
          <span className="text-xs font-normal text-slate-600">Relationship paths below start from this record.</span>
        </label>
      ) : startingAnchorLabel ? (
        <div className="grid gap-1 text-sm text-slate-800">
          <span className="font-medium">Start from</span>
          <span>{startingAnchorLabel}</span>
          <span className="text-xs text-slate-600">Relationship paths below start from this record.</span>
        </div>
      ) : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        Related record type
        <select aria-label="Related record type" value={targetResourceType} disabled={disabled || anchors.length === 0} onChange={(event) => {
          const target = event.target.value;
          setTargetResourceType(target);
          setChoice(undefined);
          setCondition({ kind: 'ALL' });
          setOtherRoutesOpen(false);
          setLoadedChoices({ queryKey: routeQueryKey, choices: [], truncated: false });
          const suggested = availableColumnName(`related_${target.toLowerCase()}_id`, stage.columns);
          setOutputName(suggested);
          setOutputLabel(`${target} FHIR resource ID`);
          onCandidateChange(undefined);
        }} className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="">Choose a record type</option>
          {targetTypes.map((target) => <option key={target} value={target}>{target}</option>)}
        </select>
      </label>
      {loading ? <p role="status" className="text-sm text-slate-600">Finding supported paths…</p> : null}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {routeSearchTruncated && !loading ? <p role="status" data-testid="construction-related-route-truncation" className="text-sm text-amber-900">
        Showing {choices.length.toLocaleString()} paths found before the search limit; additional paths may exist.
      </p> : null}
      {targetResourceType && !loading && choices.length === 0 && !choice && !error && !routeSearchTruncated
        ? <p role="status" className="text-sm text-slate-600">No supported path reaches this record type from these rows.</p>
        : null}
      {choices.length > 0 || choice ? (
        <fieldset className="grid gap-2 text-sm">
          <legend className="font-medium text-slate-800">{choice ? 'Selected relationship path' : 'Choose a relationship path'}</legend>
          <p className="text-xs text-slate-600">Each path begins at {selectedAnchor?.label ?? 'the selected starting record'} and leads to {targetResourceType} records.</p>
          {visibleRoutes.map(renderRoute)}
          {otherRoutes.length > 0 ? (
            <details data-testid="construction-related-expand-other-routes" open={otherRoutesOpen}
              onToggle={(event) => setOtherRoutesOpen(event.currentTarget.open)}
              className="rounded border border-slate-200 bg-white p-2">
              <summary className="cursor-pointer font-medium text-blue-800">Other relationship paths ({otherRoutes.length})</summary>
              <p className="mt-2 text-xs text-slate-600"><span className="rounded bg-amber-100 px-1.5 py-0.5 text-amber-950">Highlighted segments</span> differ from {choice ? 'the selected path' : 'the shortest path'}. Select a path to use it.</p>
              {otherRoutesOpen ? <div className="mt-2 grid gap-2">{otherRoutes.map(renderRoute)}</div> : null}
            </details>
          ) : null}

        </fieldset>
      ) : null}
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        If a current row has no matches
        <select aria-label="If a current row has no matches" value={emptyPolicy} disabled={disabled} onChange={(event) => {
          const next = event.target.value as EmptyPolicy;
          setEmptyPolicy(next);
          emit(choice, next);
        }} className="rounded border border-slate-300 bg-white px-3 py-2">
          <option value="EXCLUDE">Leave that current row out</option>
          <option value="PRESERVE_PARENT">Keep that current row once, with no related record ID</option>
          <option value="ERROR">Stop with an error if any current row has no match</option>
        </select>
      </label>
      <p role="status" data-testid="construction-related-expand-effect" className="rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-slate-700">
        <span className="mb-1 block font-semibold text-slate-900">What will change</span>
        {selectedAnchor ? `Starting from ${selectedAnchor.label}. ` : ''}{expansionEffect}
      </p>
      {choice ? (
        <details
          key={choice.choiceId}
          data-testid="construction-related-expand-contributor-options"
          open={contributorOptionsOpen}
          onToggle={(event) => setContributorOptionsOpen(event.currentTarget.open)}
          className="rounded-lg border border-slate-200 bg-white"
        >
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-800">
            Which related records count? · {condition.kind === 'ALL' ? 'All matching records' : 'Only records meeting a condition'}
          </summary>
          <div className="p-3 pt-0">
            <RelatedExpandContributorEditor
              project={project} explorerId={explorerId} authResourcePath={authResourcePath}
              snapshotToken={snapshotToken} draftVersion={capabilities.draftVersion} draftDigest={capabilities.draftDigest}
              outputId={outputId} stageId={stage.id} routeChoiceId={choice.choiceId}
              targetNodeId={choice.targetNodeId} targetResourceType={choice.targetResourceType}
              allowRepeatedFields
              condition={condition} disabled={disabled}
              onChange={(nextCondition) => { setCondition(nextCondition); emit(choice, emptyPolicy, outputName, outputLabel, nextCondition); }}
            />
          </div>
        </details>
      ) : null}
      <details data-testid="construction-related-expand-advanced" className="rounded-lg border border-slate-200">
        <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-700">Advanced options</summary>
        <div className="grid gap-3 p-3 pt-0">
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
        </div>
      </details>
      <p className="text-xs text-slate-600">The proposal preview shows the new rows before Apply.</p>
    </div>
  );
};
