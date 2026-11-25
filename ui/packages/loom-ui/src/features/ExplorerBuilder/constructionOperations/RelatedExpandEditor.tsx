import type { RelatedExpandQueryOwner } from '../session/commandQueue';
import React, { useState } from 'react';
import { uniqueRoutes, useRelatedExpandRoutes } from '../session/useRelatedExpandRoutes';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStep,
  ExplorerBuilderCatalog,
  RelatedExpandChoiceSearchResponse,
} from '../../../contracts/types';
import { constructionWithStep, createOpaqueId } from '../model/editorShared';
import { constructionInputForStage } from '../model/constructionStages';
import { RelatedExpandContributorEditor, type ContributorChoice, type ContributorCondition } from './RelatedExpandContributorEditor';
import { relationshipLabel, routePath } from '../model/routeDisplay';
import { useDismissibleLayer } from '../hooks/useDismissibleLayer';

type RelatedExpandOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_EXPAND' }>;
type RelatedExpandStep = Omit<ConstructionStep, 'operation'> & { readonly operation: RelatedExpandOperation };
type RouteChoice = RelatedExpandChoiceSearchResponse['choices'][number];
type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;
type EmptyPolicy = NonNullable<RelatedExpandOperation['relatedExpand']['emptyPolicy']>;

const contributorQuantifier = (choice: ContributorChoice) =>
  choice.source.cardinality === 'many' ? { quantifier: 'ANY' as const } : {};

const routeLabel = (choice: RouteChoice): string => routePath(choice.route);
const availableColumnName = (base: string, columns: ReadonlyArray<{ readonly name: string }>): string => {
  const used = new Set(columns.map((column) => column.name.toLowerCase()));
  let name = base;
  for (let suffix = 2; used.has(name.toLowerCase()); suffix += 1) name = `${base}_${suffix}`;
  return name;
};

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
      ? { ...column, name, label, type: 'string', nullable: emptyPolicy === 'PRESERVE_PARENT' }
      : column)
    : [
      ...stage.columns.map(({ id, name: columnName, label: columnLabel, type }) => ({
        id, name: columnName, label: columnLabel, ...(type ? { type } : {}),
      })),
      { id: outputColumnId, name, label, type: 'string', nullable: emptyPolicy === 'PRESERVE_PARENT' },
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
    inputs: [constructionInputForStage(stage)],
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
  const candidateConstruction = constructionWithStep({ construction, editingStep: step, step: nextStep });
  return candidateConstruction ? { candidateConstruction, changedStepId: stepId } : undefined;
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
  const stage = capabilities.selectedStage;
  const saved = step?.operation.relatedExpand;
  const anchors = stage.relatedExpandAnchors ?? (stage.activeRelatedRecord ? [] : [{
    anchorColumnId: '_key', kind: 'root' as const, resourceType: '', label: 'Original table record',
  }]);
  const savedAnchor = anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId);
  const [anchorColumnId, setAnchorColumnId] = useState(() => anchors.find((anchor) => anchor.anchorColumnId === saved?.anchorColumnId)?.anchorColumnId
    ?? anchors.find((anchor) => anchor.kind === 'activeRelatedRecord')?.anchorColumnId
    ?? anchors[0]?.anchorColumnId ?? '');
  const [stepId] = useState(() => step?.id ?? createOpaqueId('related_expand', globalThis.crypto.randomUUID()));
  const [outputColumnId] = useState(() => saved?.relatedRecordColumnId ?? createOpaqueId('related_record', globalThis.crypto.randomUUID()));
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
  const [aboutOpen, setAboutOpen] = useState(false);
  const aboutRef = useDismissibleLayer<HTMLDivElement>(aboutOpen, setAboutOpen);
  const selectedAnchor = anchors.find((anchor) => anchor.anchorColumnId === anchorColumnId);
  const startsFromSavedGraph = selectedAnchor?.kind === 'root' || selectedAnchor?.kind === 'rootContributors';
  const savedOutput = step?.outputs.find((column) => column.id === outputColumnId);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? '');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? '');
  const targetTypes = [...new Set(catalog.nodes.map((node) => node.resourceType))].sort();
  const { choices, routeSearchTruncated, loading, error, clearRoutes, retry } = useRelatedExpandRoutes({
    project, explorerId, authResourcePath, snapshotToken, outputId,
    capabilities, anchorColumnId, targetResourceType, startsFromSavedGraph, queryOwnerRef,
  });
  const staleRouteSearch = error === 'The available paths changed. Reload this table before expanding records.';


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

  const listedRoutes = uniqueRoutes([
    ...(choice ? [choice] : []),
    ...choices.filter((item) => item.choiceId !== choice?.choiceId),
  ]);
  const shortestRoute = Math.min(...listedRoutes.map((item) => item.route.length));
  const visibleRoutes = choice ? listedRoutes.filter((item) => item.choiceId === choice.choiceId) : listedRoutes.filter((item) => item.route.length === shortestRoute).slice(0, 3);
  const otherRoutes = listedRoutes.filter((item) => !visibleRoutes.some((visible) => visible.choiceId === item.choiceId));
  const renderRoute = (item: RouteChoice) => (
    <label key={item.choiceId}
      className="flex min-w-0 cursor-pointer items-start gap-3 rounded-md border border-slate-200 px-3 py-2.5 hover:border-blue-400 has-[:checked]:border-blue-500 has-[:checked]:bg-blue-50 has-[:disabled]:cursor-not-allowed">
      <input type="radio" name={`related-expand-route-${stepId}`} aria-label={routeLabel(item)} checked={choice?.choiceId === item.choiceId}
        className="mt-1 shrink-0" disabled={disabled}
        onChange={() => { setChoice(item); setCondition({ kind: 'ALL' }); emit(item, emptyPolicy, outputName, outputLabel, { kind: 'ALL' }); }} />
      <span className="grid min-w-0 gap-1">
        <span className="font-medium text-slate-900">{item.targetResourceType}</span>
        <span className="text-xs leading-relaxed text-slate-700">
          {item.route[0]?.fromResourceType}
          {item.route.map((hop, index) => (
            <span key={index}> › {relationshipLabel(hop)} → {hop.toResourceType}</span>
          ))}
        </span>
      </span>
    </label>
  );

  return (
    <div className="scroll-mt-20 grid gap-4" data-testid="construction-related-expand-editor" data-related-stage-id={stage.id} data-related-output-id={outputId}>
      <div className="relative" ref={aboutRef}>
        <div className="flex items-center gap-2">
          <h4 className="font-semibold" style={{ color: '#000' }}>Expand related records into rows</h4>
          <button type="button" aria-expanded={aboutOpen} aria-label="About expanding related records"
            className="h-5 w-5 shrink-0 rounded-full border border-slate-300 text-[11px] font-semibold leading-none text-slate-600 hover:bg-slate-100"
            onClick={() => setAboutOpen((open) => !open)}>i</button>
        </div>
        <p className="mt-1 text-sm text-slate-600">
          Choose a relationship to follow from each current row.
        </p>
        {aboutOpen ? (
          <div role="dialog" aria-label="How expanding related records works"
            className="absolute left-0 top-full z-30 mt-2 grid w-[min(24rem,calc(100vw-2rem))] gap-2 rounded-lg border border-slate-200 bg-white p-3 text-xs text-slate-700 shadow-lg">
            <p className="font-semibold text-slate-900">What this does</p>
            <p>For every current row, follow the selected relationship from the starting record and match records of the chosen type. Each match becomes its own output row; existing column values are copied onto every new row.</p>
            <p>Loom adds a related record ID column for the matched record. Contributor rules limit which matches count. The no-match policy keeps the parent row, removes it, or fails the preview.</p>
            <p>Row identity becomes the parent row identity plus the matched record identity.</p>
          </div>
        ) : null}
      </div>
      <div className="grid gap-3">
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
                clearRoutes();
              onCandidateChange(undefined);
            }} className="rounded border border-slate-300 bg-white px-3 py-2">
              {anchors.map((anchor) => <option key={anchor.anchorColumnId} value={anchor.anchorColumnId}>{anchor.label}</option>)}
            </select>
          </label>
        ) : null}
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          Related record type
          <select aria-label="Related record type" value={targetResourceType} disabled={disabled || anchors.length === 0} onChange={(event) => {
            const target = event.target.value;
            setTargetResourceType(target);
            setChoice(undefined);
            setCondition({ kind: 'ALL' });
            setOtherRoutesOpen(false);
            clearRoutes();
            const suggested = availableColumnName(`related_${target.toLowerCase()}_id`, stage.columns);
            setOutputName(suggested);
            setOutputLabel(`${target} FHIR resource ID`);
            onCandidateChange(undefined);
          }} className="rounded border border-slate-300 bg-white px-3 py-2">
            <option value="">Choose a record type</option>
            {targetTypes.map((target) => <option key={target} value={target}>{target}</option>)}
          </select>
        </label>
      </div>
      {loading ? <p role="status" className="text-sm text-slate-600">Finding relationship paths…</p> : null}
      {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
      {error && !loading && !staleRouteSearch ? <button type="button" disabled={disabled}
        onClick={() => void retry()} className="justify-self-start text-sm text-blue-800 underline">
        Retry finding paths
      </button> : null}
      {routeSearchTruncated && !loading ? <p role="status" data-testid="construction-related-route-truncation" className="text-sm text-amber-900">
        Showing an initial set of supported paths.
      </p> : null}
      {targetResourceType && !loading && choices.length === 0 && !choice && !error && !routeSearchTruncated ? (
        <p role="status" className="text-sm text-slate-600">No supported path reaches this record type from these rows.</p>
      ) : null}
      {choices.length > 0 || choice ? (
        <fieldset className="grid min-w-0 gap-2 text-sm">
          <legend className="font-medium text-slate-800">Relationship paths</legend>
          <p className="text-xs text-slate-600">Choose a path to preview how it changes your rows.</p>
          {visibleRoutes.map(renderRoute)}
          {otherRoutes.length > 0 ? (
            <details data-testid="construction-related-expand-other-routes" open={otherRoutesOpen}
              onToggle={(event) => setOtherRoutesOpen(event.currentTarget.open)}
              className="p-1">
              <summary className="cursor-pointer font-medium text-blue-800">Other relationship paths ({otherRoutes.length})</summary>
              {otherRoutesOpen ? <div className="mt-2 grid gap-2">{otherRoutes.map(renderRoute)}</div> : null}
            </details>
          ) : null}

        </fieldset>
      ) : null}
      <section aria-label="What happens to your rows" className="grid gap-2 rounded-lg border border-slate-200 bg-slate-50 p-3">
        <h5 className="text-sm font-semibold text-slate-900">What happens to your rows</h5>
        {choice ? <p className="text-sm text-slate-700">Each matching {choice.targetResourceType} record gets a new row. Values from the current row repeat in each match.</p> : null}
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          If a current row has no matches
          <select aria-label="If a current row has no matches" value={emptyPolicy} disabled={disabled} onChange={(event) => {
            const next = event.target.value as EmptyPolicy;
            setEmptyPolicy(next);
            emit(choice, next);
          }} className="rounded border border-slate-300 bg-white px-3 py-2">
            <option value="EXCLUDE">Leave that current row out</option>
            <option value="PRESERVE_PARENT">Keep the row with an empty related record ID</option>
            <option value="ERROR">Stop with an error if any current row has no match</option>
          </select>
        </label>
      </section>
      {choice ? (
        <details
          key={choice.choiceId}
          data-testid="construction-related-expand-contributor-options"
          open={contributorOptionsOpen}
          onToggle={(event) => setContributorOptionsOpen(event.currentTarget.open)}
        >
          <summary className="cursor-pointer py-1 text-sm font-medium text-slate-800">
            Which related records count? · {condition.kind === 'ALL' ? 'All matching records' : 'Only records meeting a condition'}
          </summary>
          <div className="pt-1">
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
    </div>
  );
};
