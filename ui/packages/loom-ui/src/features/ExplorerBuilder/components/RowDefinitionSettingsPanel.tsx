import React, { useEffect, useRef, useState, type ReactNode } from 'react';
import type { LoomClient } from '../../../api';
import type {
  ConstructionStep,
  ExplorerBuilderPreviewResult,
  ExplicitGroupRevisionSummary,
  RowDefinitionChoicesResponse,
  RowDefinitionProposal,
  RowDefinitionSelection,
  RowChangeAssessment,
} from '../../../types';
import { routeNode, type DraftTable } from '../authoring/model';
import type {
  RootChangePickerState,
  RootChangeSelection,
} from '../authoring/rootChangeSelection';
import type { SelectionRevision } from '../../../selection';
import type { ConstructionHistoryStep } from '../constructionWorkspace/ConstructionWorkspace';
import { ExplicitGroupAuthoring } from './ExplicitGroupAuthoring';
import { TraversalPath } from '../constructionWorkspace/TraversalPath';

const rowChangingOperationKinds = new Set<ConstructionStep['operation']['kind']>([
  'GROUP', 'CODED_GROUP', 'PIVOT', 'CODED_PIVOT', 'EXPAND', 'UNPIVOT', 'RELATED_EXPAND',
]);

type RowChangeIntent = 'records' | 'group' | 'reshape';

type SelectionOption = {
  readonly value: string;
  readonly shapeValue: string;
  readonly shapeLabel: string;
  readonly policyLabel?: string;
  readonly selection: RowDefinitionSelection;
};

const emptyCollectionLabel = (policy: string): string => {
  switch (policy) {
    case 'EXCLUDE': return 'Leave out records with no values';
    case 'PRESERVE_PARENT': return 'Keep records with no values as one empty row';
    case 'ERROR': return 'Require at least one value for every record';
    default: return policy;
  }
};

const unassignedMemberLabel = (policy: string): string => {
  switch (policy) {
    case 'EXCLUDE': return 'Leave out records without a group';
    case 'GROUP_AS_UNASSIGNED': return 'Put records without a group in their own group';
    case 'ERROR': return 'Require every record to belong to a group';
    default: return policy;
  }
};

export const rowScopeLabel = (path: string): string => path
  .split('.')
  .map((segment) => segment.replace(/\[\]/g, ''))
  .filter(Boolean)
  .map((segment) => segment.replace(/([a-z0-9])([A-Z])/g, '$1 $2'))
  .map((segment) => segment.charAt(0).toUpperCase() + segment.slice(1))
  .join(' → ');

const expandedRowLabel = (path: string): string => {
  const label = rowScopeLabel(path);
  return label.endsWith(' → Coding')
    ? `One row per code in ${label.slice(0, -' → Coding'.length)}`
    : `One row per value in ${label}`;
};

type SettingsState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'loading' }
  | {
      readonly kind: 'editing';
      readonly choices: RowDefinitionChoicesResponse;
      readonly options: ReadonlyArray<SelectionOption>;
      readonly selectionId: string;
    }
  | { readonly kind: 'error'; readonly message: string };

type ProposalState =
  | { readonly kind: 'none' }
  | {
      readonly kind: 'proposing';
      readonly selection: RowDefinitionSelection;
      readonly outputId: string;
      readonly snapshotToken: string;
      readonly draftVersion: number;
      readonly draftDigest: string;
    }
  | {
      readonly kind: 'fresh';
      readonly selection: RowDefinitionSelection;
      readonly proposal: RowDefinitionProposal;
    }
  | { readonly kind: 'stale'; readonly selection: RowDefinitionSelection }
  | { readonly kind: 'failed'; readonly selection: RowDefinitionSelection; readonly message: string }
  | { readonly kind: 'applying'; readonly selection: RowDefinitionSelection };

export type ReadyReplacementAssessment = Extract<RowChangeAssessment, { readonly status: 'READY' }> & {
  readonly mode: 'REPLACE';
  readonly proposal: Extract<RowChangeAssessment, { readonly status: 'READY' }>['proposal'] & { readonly mode: 'REPLACE' };
};
type ReplacementState =
  | { readonly kind: 'idle' }
  | { readonly kind: 'assessing'; readonly rootNodeId: string }
  | { readonly kind: 'loading-preview'; readonly assessment: ReadyReplacementAssessment }
  | {
      readonly kind: 'ready';
      readonly assessment: ReadyReplacementAssessment;
      readonly preview: ExplorerBuilderPreviewResult;
      readonly idColumn: ExplorerBuilderPreviewResult['columns'][number];
    }
  | { readonly kind: 'blocked'; readonly assessment: Extract<RowChangeAssessment, { readonly status: 'BLOCKED' }> }
  | { readonly kind: 'stale'; readonly rootNodeId: string }
  | { readonly kind: 'failed'; readonly message: string }
  | { readonly kind: 'applying'; readonly assessment: ReadyReplacementAssessment };

const replacementEffectKeys = ['removedFeatureKeys', 'workspaceOutputConsumers', 'sharedFilterBindings'] as const;

const isReadyReplacementAssessment = (
  assessment: RowChangeAssessment,
  table: DraftTable,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
): assessment is ReadyReplacementAssessment => {
  if (assessment.status !== 'READY' || assessment.mode !== 'REPLACE' ||
      assessment.proposal.mode !== 'REPLACE' || !assessment.candidateReceiptId ||
      assessment.proposal.outputId !== table.outputId ||
      assessment.snapshotToken !== snapshotToken || assessment.draftVersion !== draftVersion ||
      assessment.draftDigest !== draftDigest || assessment.proposal.rootOccurrenceId !== 'base') {
    return false;
  }
  return replacementEffectKeys.every((key) => {
    const responseEffects = assessment[key];
    const proposalEffects = assessment.proposal[key];
    return Array.isArray(responseEffects) && Array.isArray(proposalEffects) &&
      JSON.stringify(responseEffects) === JSON.stringify(proposalEffects);
  });
};

const isCurrentReplacement = (
  assessment: ReadyReplacementAssessment,
  table: DraftTable,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
): boolean => assessment.mode === 'REPLACE' && assessment.proposal.mode === 'REPLACE' &&
  assessment.proposal.outputId === table.outputId && assessment.snapshotToken === snapshotToken &&
  assessment.draftVersion === draftVersion && assessment.draftDigest === draftDigest;

const columnLabel = (table: DraftTable | undefined, columnId: string): string =>
  table?.document.columns.find((column) => column.columnId === columnId)?.label ??
  table?.document.columns.find((column) => column.column === columnId)?.label ?? columnId;

type ReplacementConsumerPresentation = {
  readonly key: string;
  readonly title: string;
  readonly detail: string;
};

export const replacementConsumerPresentations = (
  workspaceTables: ReadonlyArray<DraftTable>,
  replacementOutputId: string,
  references: NonNullable<RowChangeAssessment['workspaceOutputConsumers']>,
): ReadonlyArray<ReplacementConsumerPresentation> => references.map((reference) => {
  const consumer = workspaceTables.find((candidate) => candidate.outputId === reference.outputId);
  const step = consumer?.document.construction?.steps.find((candidate) => candidate.id === reference.stepId);
  const input = step?.inputs[reference.inputIndex];
  const targetInput = input?.kind === 'WORKSPACE_OUTPUT' && input.outputId === replacementOutputId;
  const title = consumer?.title ?? 'Another table';
  const fallbackDetail = step?.operation.kind === 'COMBINE'
    ? step.operation.combine.kind === 'KEY_JOIN' ? 'Uses this table in a join'
      : step.operation.combine.kind === 'APPEND' ? 'Uses this table in an append'
        : 'Uses this table in a membership check'
    : 'Uses this table in a construction step';
  if (!targetInput || !step || step.operation.kind !== 'COMBINE') {
    return { key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`, title, detail: fallbackDetail };
  }

  const columnIds = new Set<string>();
  for (const projection of step.operation.combine.projections) {
    if (projection.inputIndex === reference.inputIndex) columnIds.add(projection.inputColumnId);
  }
  for (const key of step.operation.combine.keys ?? []) {
    const keyColumnId = reference.inputIndex === 0
      ? key.leftColumnId
      : reference.inputIndex === 1 ? key.rightColumnId : undefined;
    if (keyColumnId) columnIds.add(keyColumnId);
  }
  if (columnIds.size === 0) return { key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`, title, detail: fallbackDetail };
  const labels = [...columnIds].map((columnId) => columnLabel(
    workspaceTables.find((candidate) => candidate.outputId === replacementOutputId), columnId,
  ));
  const operationLabel = step.operation.combine.kind === 'KEY_JOIN'
    ? 'Join fields'
    : step.operation.combine.kind === 'APPEND' ? 'Append fields' : 'Membership fields';
  return {
    key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`,
    title,
    detail: `${operationLabel}: ${labels.join(', ')}`,
  };
});

const sharedFilterPresentation = (
  workspaceTables: ReadonlyArray<DraftTable>,
  reference: NonNullable<RowChangeAssessment['sharedFilterBindings']>[number],
): string => {
  const table = workspaceTables.find((candidate) => candidate.outputId === reference.outputId);
  const label = table?.document.columns.find((column) => column.column === reference.column)?.label ?? 'field';
  return `${reference.name} · ${table?.title ?? 'another table'} · ${label}`;
};

const removedSetupPresentations = (
  table: DraftTable,
  selection: SelectionRevision | undefined,
  constructionHistory: ReadonlyArray<ConstructionHistoryStep>,
  featureKeys: ReadonlyArray<string>,
): ReadonlyArray<{ readonly key: string; readonly label: string }> => {
  const historyByStepId = new Map(constructionHistory.map((step) => [step.id, step] as const));
  return featureKeys.map((featureKey) => {
    let label: string;
    if (featureKey.startsWith('root:')) {
      label = `Starting record type — ${featureKey.slice('root:'.length)}`;
    } else if (featureKey.startsWith('rowLabel:')) {
      label = `Row label — ${featureKey.slice('rowLabel:'.length)}`;
    } else if (featureKey.startsWith('route:')) {
      const route = routeNode(table.document.route, featureKey.slice('route:'.length));
      label = route
        ? `Related record path — ${route.relationship ?? route.resourceType}`
        : 'Related record path';
    } else if (featureKey.startsWith('rows:')) {
      const rowKind = featureKey.slice('rows:'.length);
      label = rowKind === 'GROUPS'
        ? 'Row definition — grouped rows'
        : rowKind === 'EXPANDED' ? 'Row definition — one row per list value' : 'Row definition';
    } else if (featureKey.startsWith('column:')) {
      const columnName = featureKey.slice('column:'.length);
      const column = table.document.columns.find((candidate) => candidate.column === columnName);
      label = column ? `Field — ${column.label}` : 'Field';
    } else if (featureKey.startsWith('population:')) {
      const revisionId = featureKey.slice('population:'.length);
      label = selection?.id === revisionId
        ? `Starting collection — ${selection.memberCount} ${selection.resourceType} records`
        : 'Starting collection';
    } else if (featureKey.startsWith('populationRoute:')) {
      const routeStep = table.document.population?.route
        .map((step, index) => ({ step, index }))
        .find(({ step, index }) => featureKey === `populationRoute:${index}:${step.resourceType}:${step.relationship}`);
      label = routeStep
        ? `Starting collection path ${routeStep.index + 1} — ${routeStep.step.resourceType} via ${routeStep.step.relationship}`
        : 'Starting collection path';
    } else if (featureKey.startsWith('frame:')) {
      const frameId = featureKey.slice('frame:'.length);
      const frame = table.document.frames?.find((candidate) => candidate.id === frameId);
      label = frame ? `Record detail frame — ${frame.title}` : 'Record detail frame';
    } else if (featureKey.startsWith('tableShape:reshape:')) {
      const kind = featureKey.slice('tableShape:reshape:'.length);
      label = kind === 'PIVOT' ? 'Table shape — pivoted rows'
        : kind === 'UNPIVOT' ? 'Table shape — unpivoted rows' : 'Table shape — reshaped rows';
    } else if (featureKey.startsWith('tableShape:derived:')) {
      label = 'Table shape — derived columns';
    } else if (featureKey === 'tableShape') {
      label = 'Table shape';
    } else if (featureKey.startsWith('construction:')) {
      const stepId = featureKey.slice('construction:'.length);
      label = historyByStepId.has(stepId)
        ? `Construction step — ${historyByStepId.get(stepId)!.title}`
        : 'Construction step';
    } else if (featureKey.startsWith('fixedFilter:')) {
      const columnName = featureKey.slice('fixedFilter:'.length);
      const column = table.document.columns.find((candidate) => candidate.column === columnName);
      label = column ? `Fixed filter — ${column.label}` : 'Fixed filter';
    } else if (featureKey.startsWith('action:')) {
      const action = table.document.actions?.find((candidate) =>
        featureKey === `action:${candidate.type}:${candidate.title}`,
      );
      label = action ? `Saved action — ${action.title}` : 'Saved action';
    } else if (featureKey === 'construction') {
      label = 'Construction setup';
    } else {
      label = 'Other saved setup';
    }
    return { key: featureKey, label };
  });
};

const displayPreviewCell = (value: unknown): string => {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return String(value);
  return JSON.stringify(value);
};

const selectionOptions = (choices: RowDefinitionChoicesResponse): ReadonlyArray<SelectionOption> => {
  const options: SelectionOption[] = [{
    value: 'records',
    shapeValue: 'records',
    shapeLabel: 'One row per source record',
    selection: { kind: 'RECORDS' },
  }];
  const pathCounts = new Map<string, number>();
  const labelPaths = new Map<string, Set<string>>();
  for (const choice of choices.choices) {
    if (choice.kind !== 'EXPANDED') continue;
    pathCounts.set(choice.fieldPath, (pathCounts.get(choice.fieldPath) ?? 0) + 1);
    const label = expandedRowLabel(choice.fieldPath);
    const paths = labelPaths.get(label) ?? new Set<string>();
    paths.add(choice.fieldPath);
    labelPaths.set(label, paths);
  }
  for (const choice of choices.choices) {
    if (choice.kind !== 'EXPANDED') continue;
    const occurrence = (pathCounts.get(choice.fieldPath) ?? 0) > 1 ? ` · ${choice.occurrenceSummary}` : '';
    const shapeValue = `expanded:${choice.choiceId}`;
    const label = expandedRowLabel(choice.fieldPath);
    const disambiguation = (labelPaths.get(label)?.size ?? 0) > 1 ? ` (${choice.fieldPath})` : '';
    const shapeLabel = `${label}${disambiguation}${occurrence}`;
    for (const policy of choice.policies) {
      if (policy.name !== 'emptyCollectionPolicy') continue;
      for (const emptyCollectionPolicy of [...policy.options].sort((left, right) =>
        Number(right === 'PRESERVE_PARENT') - Number(left === 'PRESERVE_PARENT'))) {
        options.push({
          value: `expanded:${choice.choiceId}:${emptyCollectionPolicy}`,
          shapeValue,
          shapeLabel,
          policyLabel: emptyCollectionLabel(emptyCollectionPolicy),
          selection: { kind: 'EXPANDED', expanded: { rowChoiceId: choice.choiceId, emptyCollectionPolicy } },
        });
      }
    }
  }
  for (const group of choices.explicitGroups) {
    for (const unassignedMemberPolicy of [...group.unassignedMemberPolicies].sort((left, right) =>
      Number(right === 'GROUP_AS_UNASSIGNED') - Number(left === 'GROUP_AS_UNASSIGNED'))) {
      const shapeLabel = `One row per saved group (${group.revisionId.slice(0, 12)}) · ${group.groupCount} groups, ${group.memberCount} members`;
      options.push({
        value: `explicit:${group.revisionId}:${unassignedMemberPolicy}`,
        shapeValue: `explicit:${group.revisionId}`,
        shapeLabel,
        policyLabel: unassignedMemberLabel(unassignedMemberPolicy),
        selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: group.revisionId, unassignedMemberPolicy } },
      });
    }
  }
  return options;
};

const shapeOptions = (options: ReadonlyArray<SelectionOption>): ReadonlyArray<SelectionOption> => {
  const seen = new Set<string>();
  return options.filter((option) => {
    if (seen.has(option.shapeValue)) return false;
    seen.add(option.shapeValue);
    return true;
  });
};

const isCurrentProposal = (
  proposal: RowDefinitionProposal,
  table: DraftTable,
  snapshotToken: string,
  draftVersion: number,
  draftDigest: string,
) => proposal.outputId === table.outputId && proposal.snapshotToken === snapshotToken &&
  proposal.draftVersion === draftVersion && proposal.draftDigest === draftDigest;

const requestFailureMessage = (error: unknown, fallback: string): string => {
  if (typeof error === 'object' && error !== null && 'status' in error && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return fallback;
};

export const RowDefinitionSettingsPanel = ({
  triggerPresentation,
  triggerLabel,
  client,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  draftVersion,
  draftDigest,
  table,
  currentRowMeaning,
  startingCollectionSummary,
  renderRootSettings,
  startingCollectionSettings,
  selection,
  constructionHistory = [],
  workspaceTables,
  rowOperationEditor,
  disabled,
  onApply,
  relatedRows,
  reshapeRows,
  onChooseRelatedRows,
  onChooseReshape,
  onSwitchRowIntent,
  onCloseRowIntentSession,
  sourceCollectionAction,
  onChangeRootSelection,
  onResolveRootSelection,
  rootChangeState,
  rootChangePreviewOpen,
  onCancelRootChange,
  onApplyRootReplacement,
  onEditConstructionStep,
  onRemoveConstructionStep,
}: {
  readonly triggerPresentation?: 'source-chip';
  readonly triggerLabel?: string;
  readonly client: Pick<LoomClient, 'listRowDefinitionChoices' | 'proposeRowDefinition' | 'getSelection' | 'createExplicitGroupRevision'> &
    Partial<Pick<LoomClient, 'assessRowChange' | 'preview'>>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly workspaceTables: ReadonlyArray<DraftTable>;
  readonly currentRowMeaning: string;
  readonly startingCollectionSummary: string;
  readonly renderRootSettings: (
    onRootChange: (selection: RootChangeSelection) => void,
    onResolveRootChange: (referenceId: string, alternative: string) => void,
    onReplaceRoot: (rootNodeId: string) => void,
  ) => ReactNode;
  readonly startingCollectionSettings: ReactNode;
  readonly selection?: SelectionRevision;
  readonly constructionHistory?: ReadonlyArray<ConstructionHistoryStep>;
  readonly disabled: boolean;
  readonly rowOperationEditor?: ReactNode;
  readonly onApply: (proposalId: string) => Promise<boolean>;
  readonly relatedRows: { readonly supported: boolean; readonly reason?: string };
  readonly reshapeRows: Record<'group' | 'pivot', { readonly supported: boolean; readonly reason?: string }> & {
    readonly pivotPending?: boolean;
    readonly groupEntry: 'group' | 'source-group' | 'coded-group';
    readonly groupAlternatives: ReadonlyArray<{
      readonly kind: 'group' | 'source-group' | 'coded-group';
      readonly label: string;
    }>;
    readonly pivotEntry?: 'pivot' | 'coded-pivot';
    readonly pivotAlternative?: 'pivot';
  };
  readonly onChooseRelatedRows: () => void;
  readonly onChooseReshape: (kind: 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories' | 'unpivot') => void;
  readonly onSwitchRowIntent?: (intent: 'records' | 'reshape') => void;
  readonly onCloseRowIntentSession?: () => void;
  readonly sourceCollectionAction?: ReactNode;
  readonly onChangeRootSelection: (selection: RootChangeSelection) => void;
  readonly onResolveRootSelection: (referenceId: string, alternative: string) => void;
  readonly rootChangeState: RootChangePickerState;
  readonly rootChangePreviewOpen: boolean;
  readonly onCancelRootChange: () => void;
  readonly onApplyRootReplacement: (assessment: ReadyReplacementAssessment) => Promise<boolean>;
  readonly onEditConstructionStep?: (stepId: string) => void;
  readonly onRemoveConstructionStep?: (stepId: string) => void;
}) => {
  const [settings, setSettings] = useState<SettingsState>({ kind: 'closed' });
  const [rowChangeIntent, setRowChangeIntent] = useState<RowChangeIntent>('records');
  const [proposalState, setProposalState] = useState<ProposalState>({ kind: 'none' });
  const [replacementState, setReplacementState] = useState<ReplacementState>({ kind: 'idle' });
  const [groupAuthoringOpen, setGroupAuthoringOpen] = useState(false);
  const settingsRequestEpoch = useRef(0);
  const previewTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const proposalController = useRef<AbortController | undefined>(undefined);
  const proposalRequestEpoch = useRef(0);
  const replacementController = useRef<AbortController | undefined>(undefined);
  const replacementRequestEpoch = useRef(0);

  const settingsOpen = settings.kind !== 'closed';

  const cancelPendingPreview = () => {
    if (previewTimer.current) clearTimeout(previewTimer.current);
    previewTimer.current = undefined;
    proposalController.current?.abort();
    proposalController.current = undefined;
    proposalRequestEpoch.current += 1;
  };

  const cancelReplacementRequest = () => {
    replacementController.current?.abort();
    replacementController.current = undefined;
    replacementRequestEpoch.current += 1;
  };

  useEffect(() => () => {
    if (previewTimer.current) clearTimeout(previewTimer.current);
    proposalController.current?.abort();
    proposalRequestEpoch.current += 1;
    replacementController.current?.abort();
    replacementRequestEpoch.current += 1;
  }, [table.outputId, snapshotToken, draftVersion, draftDigest]);

  useEffect(() => {
    setReplacementState((current) => {
      if (current.kind === 'assessing') return { kind: 'stale', rootNodeId: current.rootNodeId };
      if ((current.kind === 'loading-preview' || current.kind === 'ready' || current.kind === 'applying') &&
          !isCurrentReplacement(current.assessment, table, snapshotToken, draftVersion, draftDigest)) {
        return { kind: 'stale', rootNodeId: current.assessment.proposal.rootNodeId };
      }
      return current;
    });
  }, [table, snapshotToken, draftVersion, draftDigest]);

  useEffect(() => {
    setProposalState((current) => {
      if (current.kind === 'fresh' && !isCurrentProposal(
        current.proposal, table, snapshotToken, draftVersion, draftDigest,
      )) return { kind: 'stale', selection: current.selection };
      if (current.kind === 'proposing' && (
        current.outputId !== table.outputId || current.snapshotToken !== snapshotToken ||
        current.draftVersion !== draftVersion || current.draftDigest !== draftDigest
      )) return { kind: 'stale', selection: current.selection };
      return current;
    });
  }, [table, snapshotToken, draftVersion, draftDigest]);

  const openSettings = async () => {
    cancelPendingPreview();
    const requestEpoch = ++settingsRequestEpoch.current;
    setRowChangeIntent('records');
    setSettings({ kind: 'loading' });
    setProposalState({ kind: 'none' });
    try {
      const choices = await client.listRowDefinitionChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId: table.outputId,
      });
      if (requestEpoch !== settingsRequestEpoch.current) return;
      if (choices.outputId !== table.outputId || choices.snapshotToken !== snapshotToken) {
        setSettings({ kind: 'error', message: 'The server returned row choices for a different table or catalog snapshot.' });
        return;
      }
      const options = selectionOptions(choices);
      const rows = table.document.rows;
      const selected = options.find((option) => {
        const selection = option.selection;
        if (rows.kind === 'RECORDS') return selection.kind === 'RECORDS';
        if (rows.kind === 'EXPANDED' && selection.kind === 'EXPANDED') {
          const choice = choices.choices.find((candidate) => candidate.choiceId === selection.expanded.rowChoiceId);
          return choice?.occurrenceId === rows.expanded.occurrenceId &&
            choice.fieldPath === rows.expanded.scopePath &&
            selection.expanded.emptyCollectionPolicy === rows.expanded.emptyCollectionPolicy;
        }
        if (rows.kind === 'GROUPS' && rows.groups.source.kind === 'EXPLICIT' && selection.kind === 'EXPLICIT_GROUP') {
          return selection.explicitGroup.revisionId === rows.groups.source.explicit.revisionId &&
            selection.explicitGroup.unassignedMemberPolicy === rows.groups.source.explicit.unassignedMemberPolicy;
        }
        return false;
      });
      setSettings({ kind: 'editing', choices, options, selectionId: selected?.value ?? '' });
    } catch (error) {
      if (requestEpoch !== settingsRequestEpoch.current) return;
      setSettings({
        kind: 'error',
        message: requestFailureMessage(error, 'Loom could not load row-definition choices. Reload the Builder and try again.'),
      });
    }
  };

  useEffect(() => {
    if (rootChangeState.kind === 'idle') return;
    if (settings.kind === 'closed') void openSettings();
  }, [rootChangeState.kind, rootChangeState.kind === 'idle' ? -1 : rootChangeState.identity.epoch,
    settings.kind]);

  useEffect(() => {
    if (rootChangePreviewOpen && settings.kind !== 'closed') {
      settingsRequestEpoch.current += 1;
      cancelPendingPreview();
      cancelReplacementRequest();
      setProposalState({ kind: 'none' });
      setReplacementState({ kind: 'idle' });
      setGroupAuthoringOpen(false);
      setSettings({ kind: 'closed' });
      onCloseRowIntentSession?.();
    }
  }, [rootChangePreviewOpen, settings.kind]);

  const propose = async (selected: RowDefinitionSelection) => {
    cancelPendingPreview();
    const requestEpoch = proposalRequestEpoch.current;
    const controller = new AbortController();
    proposalController.current = controller;
    setProposalState({
      kind: 'proposing', selection: selected, outputId: table.outputId,
      snapshotToken, draftVersion, draftDigest,
    });
    try {
      const proposal = await client.proposeRowDefinition({
        project, explorerId, authResourcePath, snapshotToken, expectedDraftVersion: draftVersion,
        expectedDraftDigest: draftDigest, outputId: table.outputId, selection: selected,
      }, controller.signal);
      if (controller.signal.aborted || requestEpoch !== proposalRequestEpoch.current) return;
      if (!isCurrentProposal(proposal, table, snapshotToken, draftVersion, draftDigest)) {
        setProposalState({ kind: 'stale', selection: selected });
        return;
      }
      if (proposal.comparison.status !== 'AVAILABLE' || !proposal.proposalId) {
        setProposalState({ kind: 'failed', selection: selected, message: proposal.comparison.status === 'UNAVAILABLE' ? proposal.comparison.reason : 'Loom returned an incomplete row preview. The saved draft was not changed.' });
        return;
      }
      setProposalState({ kind: 'fresh', selection: selected, proposal });
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== proposalRequestEpoch.current) return;
      setProposalState({
        kind: 'failed', selection: selected,
        message: requestFailureMessage(error, 'Loom could not preview this row definition. The saved draft was not changed.'),
      });
    } finally {
      if (proposalController.current === controller) proposalController.current = undefined;
    }
  };

  const assessReplacement = async (rootNodeId: string) => {
    cancelPendingPreview();
    cancelReplacementRequest();
    const assessRowChange = client.assessRowChange;
    const loadPreview = client.preview;
    if (!assessRowChange || !loadPreview) {
      setReplacementState({ kind: 'failed', message: 'Replacement review is unavailable in this Builder session. The saved draft was not changed.' });
      return;
    }
    const requestEpoch = replacementRequestEpoch.current;
    const controller = new AbortController();
    replacementController.current = controller;
    setReplacementState({ kind: 'assessing', rootNodeId });
    try {
      const assessment = await assessRowChange({
        project, explorerId, authResourcePath, mode: 'REPLACE', snapshotToken,
        draftVersion, draftDigest, outputId: table.outputId, rootNodeId,
      }, controller.signal);
      if (controller.signal.aborted || requestEpoch !== replacementRequestEpoch.current) return;
      if (assessment.snapshotToken !== snapshotToken || assessment.draftVersion !== draftVersion ||
          assessment.draftDigest !== draftDigest) {
        setReplacementState({ kind: 'stale', rootNodeId });
        return;
      }
      if (assessment.status === 'BLOCKED') {
        setReplacementState({ kind: 'blocked', assessment });
        return;
      }
      if (!isReadyReplacementAssessment(assessment, table, snapshotToken, draftVersion, draftDigest) ||
          assessment.proposal.rootNodeId !== rootNodeId) {
        setReplacementState({ kind: 'failed', message: 'Loom returned an incomplete or mismatched replacement review. The saved draft was not changed.' });
        return;
      }
      const candidateReceiptId = assessment.candidateReceiptId;
      if (!candidateReceiptId) {
        setReplacementState({ kind: 'failed', message: 'Loom did not provide a replacement preview receipt. The saved draft was not changed.' });
        return;
      }
      setReplacementState({ kind: 'loading-preview', assessment });
      const preview = await loadPreview({
        project, explorerId, authResourcePath, receiptId: candidateReceiptId,
        outputId: assessment.proposal.outputId,
      }, controller.signal);
      if (controller.signal.aborted || requestEpoch !== replacementRequestEpoch.current) return;
      if (preview.receiptId !== candidateReceiptId || preview.outputId !== table.outputId) {
        setReplacementState({ kind: 'failed', message: 'Loom returned a preview for a different replacement receipt or table. The saved draft was not changed.' });
        return;
      }
      const idColumns = preview.columns.filter((column) => column.column === 'id');
      if (idColumns.length !== 1) {
        setReplacementState({ kind: 'failed', message: 'The replacement preview does not contain exactly one default id column. The saved draft was not changed.' });
        return;
      }
      const idColumn = idColumns[0];
      if (idColumn.sourcePath !== 'id' || idColumn.sourceResourceType !== assessment.candidateRootResourceType) {
        setReplacementState({ kind: 'failed', message: 'The replacement preview does not identify the base record id field. The saved draft was not changed.' });
        return;
      }
      setReplacementState({ kind: 'ready', assessment, preview, idColumn });
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== replacementRequestEpoch.current) return;
      setReplacementState({
        kind: 'failed',
        message: requestFailureMessage(error, 'Loom could not review this replacement. The saved draft was not changed.'),
      });
    } finally {
      if (replacementController.current === controller) replacementController.current = undefined;
    }
  };

  const cancelReplacementReview = () => {
    cancelReplacementRequest();
    setReplacementState({ kind: 'idle' });
  };

  const applyReplacement = async () => {
    if (replacementState.kind !== 'ready') return;
    if (!isCurrentReplacement(replacementState.assessment, table, snapshotToken, draftVersion, draftDigest)) {
      setReplacementState({ kind: 'stale', rootNodeId: replacementState.assessment.proposal.rootNodeId });
      return;
    }
    const assessment = replacementState.assessment;
    setReplacementState({ kind: 'applying', assessment });
    if (await onApplyRootReplacement(assessment)) {
      setReplacementState({ kind: 'idle' });
      setSettings({ kind: 'closed' });
      setGroupAuthoringOpen(false);
      onCloseRowIntentSession?.();
    } else {
      setReplacementState({ kind: 'stale', rootNodeId: assessment.proposal.rootNodeId });
    }
  };

  const schedulePreview = (selection: RowDefinitionSelection) => {
    cancelPendingPreview();
    setProposalState({ kind: 'none' });
    previewTimer.current = setTimeout(() => {
      previewTimer.current = undefined;
      void propose(selection);
    }, 300);
  };

  const apply = async () => {
    if (proposalState.kind !== 'fresh') return;
    if (!isCurrentProposal(proposalState.proposal, table, snapshotToken, draftVersion, draftDigest)) {
      setProposalState({ kind: 'stale', selection: proposalState.selection });
      return;
    }
    const proposalId = proposalState.proposal.proposalId;
    if (!proposalId) return;
    setProposalState({ kind: 'applying', selection: proposalState.selection });
    if (await onApply(proposalId)) {
      setSettings({ kind: 'closed' });
      setProposalState({ kind: 'none' });
      setGroupAuthoringOpen(false);
      onCloseRowIntentSession?.();
    } else {
      setProposalState({ kind: 'stale', selection: proposalState.selection });
    }
  };

  const cancel = () => {
    cancelPendingPreview();
    cancelReplacementRequest();
    if (rootChangeState.kind !== 'idle' || rootChangePreviewOpen) onCancelRootChange();
    setReplacementState({ kind: 'idle' });
    settingsRequestEpoch.current += 1;
    setSettings({ kind: 'closed' });
    setProposalState({ kind: 'none' });
    setGroupAuthoringOpen(false);
    onCloseRowIntentSession?.();
  };

  const cancelLocalRowProposals = () => {
    cancelPendingPreview();
    cancelReplacementRequest();
    setProposalState({ kind: 'none' });
    setReplacementState({ kind: 'idle' });
    setGroupAuthoringOpen(false);
  };

  const switchRowIntent = (intent: 'records' | 'reshape') => {
    cancelLocalRowProposals();
    setRowChangeIntent(intent);
    onSwitchRowIntent?.(intent);
  };

  const beginRootChange = (next: RootChangeSelection) => {
    cancelReplacementRequest();
    setReplacementState({ kind: 'idle' });
    onChangeRootSelection(next);
  };
  const beginRootResolution = (referenceId: string, alternative: string) => {
    cancelReplacementRequest();
    setReplacementState({ kind: 'idle' });
    onResolveRootSelection(referenceId, alternative);
  };

  const chooseRelatedRows = () => {
    cancelLocalRowProposals();
    setRowChangeIntent('records');
    onChooseRelatedRows();
  };
  const chooseReshape = (kind: 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories' | 'unpivot') => {
    cancelLocalRowProposals();
    setRowChangeIntent(kind === 'group' || kind === 'source-group' || kind === 'coded-group' ? 'group' : 'reshape');
    onChooseReshape(kind);
  };
  const editConstructionStep = (stepId: string) => {
    cancel();
    onEditConstructionStep?.(stepId);
  };
  const removeConstructionStep = (stepId: string) => {
    cancel();
    onRemoveConstructionStep?.(stepId);
  };

  const currentProposal = proposalState.kind === 'fresh' ? proposalState.proposal : undefined;
  const comparison = currentProposal?.comparison;
  const replacementAssessment = replacementState.kind === 'loading-preview' ||
    replacementState.kind === 'ready' || replacementState.kind === 'applying'
    ? replacementState.assessment
    : undefined;
  const replacementConsumers = replacementAssessment
    ? replacementConsumerPresentations(workspaceTables, table.outputId, replacementAssessment.workspaceOutputConsumers ?? [])
    : [];
  const replacementRemovedSetup = replacementAssessment
    ? removedSetupPresentations(table, selection, constructionHistory, replacementAssessment.removedFeatureKeys ?? [])
    : [];
  const replacementIsCurrent = replacementAssessment
    ? isCurrentReplacement(replacementAssessment, table, snapshotToken, draftVersion, draftDigest)
    : false;
  const selectedOption = settings.kind === 'editing'
    ? settings.options.find((option) => option.value === settings.selectionId)
    : undefined;
  const selectedPolicies = settings.kind === 'editing' && selectedOption
    ? settings.options.filter((option) => option.shapeValue === selectedOption.shapeValue && option.policyLabel)
    : [];
  const explicitGroupRootMatches = selection?.resourceType === table.document.rootResourceType;
  const startingCollectionDescription = startingCollectionSummary.replace(/^Starting collection:\s*/, '');
  const historyByStepId = new Map(constructionHistory.map((step) => [step.id, step] as const));
  const appliedRowSteps = (table.document.construction?.steps ?? [])
    .filter((step) => rowChangingOperationKinds.has(step.operation.kind));
  const onExplicitGroupsCreated = async (revision: ExplicitGroupRevisionSummary) => {
    const choices = await client.listRowDefinitionChoices({
      project, explorerId, authResourcePath, snapshotToken, outputId: table.outputId,
    });
    if (choices.outputId !== table.outputId || choices.snapshotToken !== snapshotToken ||
        !choices.explicitGroups.some((group) => group.revisionId === revision.revisionId)) {
      throw new Error('The new group revision is not available for this table. Check that its selected resource type matches the table row type.');
    }
    const options = selectionOptions(choices);
    const preferred = options.find((option) => option.value === `explicit:${revision.revisionId}:ERROR`) ??
      options.find((option) => option.value.startsWith(`explicit:${revision.revisionId}:`));
    setSettings({ kind: 'editing', choices, options, selectionId: preferred?.value ?? options[0]!.value });
    schedulePreview((preferred ?? options[0]!).selection);
    setGroupAuthoringOpen(false);
  };

  return (
    <>
      <button
        type="button"
        data-testid="construction-rows-settings-trigger"
        data-construction-source-selection-trigger
        aria-label={triggerPresentation === 'source-chip'
          ? `Change starting records for ${triggerLabel || table.document.rootResourceType || 'this table'}`
          : 'Change current rows'}
        aria-haspopup="dialog"
        aria-expanded={settings.kind !== 'closed'}
        className={triggerPresentation === 'source-chip'
          ? 'construction-source-chip inline-flex h-7 max-w-full items-center rounded-md bg-white px-2.5 text-left text-xs font-medium text-blue-950 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-60'
          : 'inline-flex h-9 max-w-full items-center gap-2 rounded-md border border-slate-300 bg-white px-3 text-left text-xs text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60'}
        disabled={disabled || settings.kind === 'loading'}
        onClick={() => void openSettings()}
      >
        <span className="shrink-0">{triggerLabel ?? 'Change rows'}</span>
        {triggerPresentation === 'source-chip' ? null : <span className="shrink-0 font-medium text-blue-800" aria-hidden="true">→</span>}
      </button>
      {settings.kind !== 'closed' ? (
        <div className="fixed inset-0 z-50 overflow-y-auto bg-white">
          <div role="dialog" aria-modal="true" aria-label="Row definition settings" className="min-h-dvh w-full px-4 py-4 sm:px-6 lg:px-8">
            <header className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <h3 className="text-xl font-semibold text-slate-900">Change current rows</h3>
                <p className="mt-1 text-sm text-slate-700">{currentRowMeaning}</p>
              </div>
              <button type="button" className="shrink-0 whitespace-nowrap rounded-md border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50" onClick={cancel}>Back to table</button>
            </header>
            <section aria-label="Choose a row intent" className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
              <button type="button" data-testid="construction-row-intent-records" aria-pressed={rowChangeIntent === 'records'}
                onClick={() => switchRowIntent('records')}
                className={`flex min-h-[4.5rem] min-w-0 flex-col justify-center rounded-lg border px-3 py-2 text-left transition-colors hover:border-blue-400 hover:bg-blue-50 ${rowChangeIntent === 'records' ? 'border-blue-500 bg-blue-50' : 'border-slate-200 bg-white'}`}>
                <span className="block text-sm font-semibold text-slate-900">Use different records</span>
                <span className="block text-xs text-slate-600">Choose a starting record type, row shape, or related records.</span>
              </button>
              <div className={`min-w-0 rounded-lg border ${rowChangeIntent === 'group' ? 'border-blue-500 bg-blue-50' : 'border-slate-200 bg-white'}`}>
                <button type="button" data-testid="construction-action-group-rows" aria-pressed={rowChangeIntent === 'group'} disabled={disabled || !reshapeRows.group.supported}
                  onClick={() => chooseReshape(reshapeRows.groupEntry)}
                  className="flex min-h-[4.5rem] w-full min-w-0 flex-col justify-center rounded-lg px-3 py-2 text-left hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
                  <span className="block text-sm font-semibold text-slate-900">Group and summarize</span>
                  <span className={`block text-xs text-slate-600 ${reshapeRows.group.supported ? 'truncate' : 'whitespace-normal'}`} title={reshapeRows.group.supported
                    ? reshapeRows.groupEntry === 'coded-group'
                      ? 'Make one row for each distinct recorded code. Rows with the same code become one group row.'
                      : reshapeRows.groupEntry === 'source-group'
                        ? 'Make one row for each combination of field values you choose, without adding columns first.'
                        : 'Make one row for each combination of matching values in the fields you choose.'
                    : reshapeRows.group.reason}>{reshapeRows.group.supported
                    ? reshapeRows.groupEntry === 'coded-group'
                      ? 'Group rows by one recorded code.'
                      : reshapeRows.groupEntry === 'source-group'
                        ? 'Group on source fields without adding columns.'
                        : 'Group by matching values in selected fields.'
                    : reshapeRows.group.reason}</span>
                </button>
                {reshapeRows.groupAlternatives.length > 0 ? (
                  <div className="flex flex-wrap gap-1 border-t border-slate-200 px-2 py-1">
                    {reshapeRows.groupAlternatives.map((alternative) => (
                      <button key={alternative.kind} type="button" data-testid={`construction-action-${alternative.kind}-rows`}
                        disabled={disabled} onClick={() => chooseReshape(alternative.kind)}
                        className="rounded px-2 py-1 text-xs font-medium text-blue-800 hover:bg-blue-50 disabled:opacity-50">
                        {alternative.label}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
              <button type="button" data-testid="construction-row-intent-reshape" aria-pressed={rowChangeIntent === 'reshape'}
                onClick={() => switchRowIntent('reshape')}
                className={`flex min-h-[4.5rem] min-w-0 flex-col justify-center rounded-lg border px-3 py-2 text-left transition-colors hover:border-blue-400 hover:bg-blue-50 ${rowChangeIntent === 'reshape' ? 'border-blue-500 bg-blue-50' : 'border-slate-200 bg-white'}`}>
                <span className="block text-sm font-semibold text-slate-900">Reshape table</span>
                <span className="block text-xs text-slate-600">Pivot categories into columns or stack columns into rows.</span>
              </button>
            </section>
            {rowChangeIntent === 'records' ? (
              <>
                <div className="mt-3 grid min-w-0 gap-2 md:grid-cols-2">
                  {renderRootSettings(beginRootChange, beginRootResolution, (rootNodeId) => void assessReplacement(rootNodeId))}
                  <div className="min-w-0 rounded-lg border border-slate-200 bg-white px-3 py-2">
                    {settings.kind === 'loading' ? <p className="text-sm text-slate-600" role="status">Loading row choices…</p> : null}
                    {settings.kind === 'error' ? <p className="text-sm text-red-800" role="alert">{settings.message}</p> : null}
                    {settings.kind === 'editing' ? (
                      <>
                        <label className="flex min-w-0 flex-col gap-1 text-xs font-medium text-slate-800 sm:flex-row sm:items-center sm:gap-3">
                          <span className="shrink-0">Rows per starting record</span>
                          <select
                            aria-label="Rows per starting record"
                            className="min-w-0 flex-1 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm"
                            value={selectedOption?.shapeValue ?? ''}
                            disabled={disabled || proposalState.kind === 'applying'}
                            onChange={(event) => {
                              const next = settings.options.find((option) => option.shapeValue === event.currentTarget.value);
                              if (!next) return;
                              setSettings({ ...settings, selectionId: next.value });
                              schedulePreview(next.selection);
                            }}
                          >
                            <option value="" disabled>Choose a row shape</option>
                            {shapeOptions(settings.options).map((option) => <option key={option.shapeValue} value={option.shapeValue}>{option.shapeLabel}</option>)}
                          </select>
                        </label>
                        {selectedPolicies.length > 1 ? (
                          <label className="mt-2 flex min-w-0 flex-col gap-1 border-t border-slate-100 pt-2 text-xs font-medium text-slate-700 sm:flex-row sm:items-center sm:gap-3">
                            <span className="shrink-0">When a record has no values</span>
                            <select
                              aria-label="Unmatched record policy"
                              className="min-w-0 flex-1 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm"
                              value={settings.selectionId}
                              disabled={disabled || proposalState.kind === 'applying'}
                              onChange={(event) => {
                                const next = selectedPolicies.find((option) => option.value === event.currentTarget.value);
                                if (!next) return;
                                setSettings({ ...settings, selectionId: next.value });
                                schedulePreview(next.selection);
                              }}
                            >
                              {selectedPolicies.map((option) => <option key={option.value} value={option.value}>{option.policyLabel}</option>)}
                            </select>
                          </label>
                        ) : null}
                      </>
                    ) : null}
                  </div>
                </div>
                <section aria-label="Related record rows" className="mt-3">
                  <button type="button" data-testid="construction-action-related-rows" disabled={disabled || !relatedRows.supported}
                    onClick={chooseRelatedRows}
                    title={relatedRows.supported
                      ? 'Follow a relationship to another record type. Existing values repeat for each match; unmatched rows follow the selected policy.'
                      : relatedRows.reason}
                    className="flex min-h-[4.5rem] min-w-0 w-full flex-col justify-center rounded-lg border border-slate-200 bg-white px-3 py-2 text-left transition-colors hover:border-blue-400 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
                    <span className="block text-sm font-semibold text-slate-900">Make a row for each related record</span>
                    <span className={`block text-xs text-slate-600 ${relatedRows.supported ? 'truncate' : 'whitespace-normal'}`}>{relatedRows.supported
                      ? 'Follow a relationship; make one row for each match.'
                      : relatedRows.reason ?? 'Checking available related row paths…'}</span>
                  </button>
                </section>
                {sourceCollectionAction ? (
                  <details data-testid="construction-advanced-row-operations" className="mt-3 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
                    <summary className="cursor-pointer text-sm font-medium text-slate-700">Advanced row operations</summary>
                    <section aria-label="Starting-record rows" className="mt-2 min-w-0 rounded-lg border border-slate-200 bg-white p-2">
                      <p className="mb-1 text-xs font-medium text-slate-600">Repeat a source list as rows</p>
                      {sourceCollectionAction}
                    </section>
                  </details>
                ) : null}
              </>
            ) : rowChangeIntent === 'reshape' ? (
              <section aria-label="Reshape table options" className="mt-3 grid gap-2 sm:grid-cols-2">
                <div className="min-w-0 rounded-lg border border-slate-200 bg-white">
                  <button type="button" data-testid="construction-action-pivot-rows" disabled={disabled || reshapeRows.pivotPending === true}
                    onClick={() => chooseReshape('categories')}
                    className="flex min-h-[4.5rem] w-full min-w-0 flex-col justify-center rounded-lg px-3 py-2 text-left hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
                    <span className="block text-sm font-semibold text-slate-900">Pivot categories</span>
                    <span className="block text-xs text-slate-600">Make a column for each category. Choose which values fill those columns.</span>
                  </button>
                  <p className="border-t border-slate-100 px-3 py-2 text-xs text-slate-600">
                    Illustrative shape: <code>Patient | Type | Value</code> → <code>Patient | Tumor | Normal</code>
                    <span className="block"><code>P1 | Tumor | 8; P1 | Normal | 3</code> → <code>P1 | 8 | 3</code></span>
                  </p>
                  {reshapeRows.pivotAlternative ? (
                    <div className="flex flex-wrap gap-1 border-t border-slate-100 px-2 py-1">
                      <button type="button" data-testid="construction-action-table-pivot-rows"
                        disabled={disabled} onClick={() => chooseReshape('pivot')}
                        className="rounded px-2 py-1 text-xs font-medium text-blue-800 hover:bg-blue-50 disabled:opacity-50">
                        Choose category and value fields
                      </button>
                    </div>
                  ) : null}
                </div>
                <div className="min-w-0 rounded-lg border border-slate-200 bg-white">
                  <button type="button" data-testid="construction-action-unpivot-rows" disabled={disabled}
                    onClick={() => chooseReshape('unpivot')}
                    title="Move selected columns into a field-name column and a value column. Other columns repeat on each new row."
                    className="flex min-h-[4.5rem] w-full min-w-0 flex-col justify-center rounded-lg px-3 py-2 text-left transition-colors hover:border-blue-400 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
                    <span className="block text-sm font-semibold text-slate-900">Stack columns</span>
                    <span className="block text-xs text-slate-600">Move selected columns into field and value rows.</span>
                  </button>
                  <p className="border-t border-slate-100 px-3 py-2 text-xs text-slate-600">
                    Illustrative shape: <code>Patient | Jan | Feb</code> → <code>Patient | Month | Value</code>
                    <span className="block"><code>P1 | 8 | 3</code> → <code>P1 | Jan | 8; P1 | Feb | 3</code></span>
                  </p>
                </div>
              </section>
            ) : null}
              {rowOperationEditor !== undefined ? (
                <div data-testid="construction-row-operation-editor" className="mt-2 min-w-0 space-y-2">
                  {rowOperationEditor}
                </div>
              ) : null}
              {appliedRowSteps.length > 0 ? (
                <section aria-label="Applied row changes" data-testid="construction-row-operation-history" className="mt-3 grid gap-2 border-t border-slate-200 pt-2">
                  <h4 className="text-sm font-semibold text-slate-900">Applied row changes</h4>
                  <ol className="grid gap-2">
                    {appliedRowSteps.map((step, index) => {
                      const history = historyByStepId.get(step.id);
                      return (
                        <li key={step.id} className="flex items-start justify-between gap-3 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
                          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                            <span className="text-xs text-slate-400">{index + 1}</span>
                            {step.operation.kind === 'RELATED_EXPAND' ? (
                              <>
                                <TraversalPath route={step.operation.relatedExpand.route} />
                                <span className="rounded bg-white px-1.5 py-0.5 text-[10px] text-slate-600">Rows: {step.operation.relatedExpand.targetResourceType}</span>
                                <span className="rounded bg-white px-1.5 py-0.5 text-[10px] text-slate-600">
                                  {step.operation.relatedExpand.emptyPolicy === 'EXCLUDE' ? 'Drop unmatched' : step.operation.relatedExpand.emptyPolicy === 'ERROR' ? 'Stop on missing' : 'Keep unmatched'}
                                </span>
                              </>
                            ) : (
                              <span className="min-w-0">
                                <span className="font-medium text-slate-800">{history?.title ?? 'Row change'}</span>
                                {history?.summary ? <span className="ml-2 text-xs text-slate-600">{history.summary}</span> : null}
                              </span>
                            )}
                          </div>
                          <div className="flex shrink-0 items-center gap-1">
                            {history?.editable && onEditConstructionStep ? (
                              <button type="button" disabled={disabled} data-testid={`construction-row-edit-${step.id}`} onClick={() => editConstructionStep(step.id)} className="rounded px-1.5 py-1 text-xs font-medium text-blue-800 hover:bg-blue-100 disabled:opacity-45">Edit</button>
                            ) : null}
                            {onRemoveConstructionStep ? (
                              <button type="button" disabled={disabled} data-testid={`construction-row-remove-${step.id}`} onClick={() => removeConstructionStep(step.id)} className="rounded px-1.5 py-1 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-45">Remove</button>
                            ) : null}
                          </div>
                        </li>
                      );
                    })}
                  </ol>
                </section>
              ) : null}
              {rowOperationEditor === undefined ? (
              <div className="mt-2 min-w-0 space-y-2">
              {settings.kind === 'editing' ? (
                <>
                {groupAuthoringOpen && selection ? (
                  <ExplicitGroupAuthoring
                    client={client}
                    project={project}
                    explorerId={explorerId}
                    authResourcePath={authResourcePath}
                    snapshotToken={snapshotToken}
                    selection={selection}
                    onCancel={() => setGroupAuthoringOpen(false)}
                    onCreated={onExplicitGroupsCreated}
                  />
                ) : null}
                {!groupAuthoringOpen && selection ? (
                  <section aria-label="Named cohorts" className="mt-2 rounded-lg border border-slate-200 bg-white px-3 py-2">
                    {explicitGroupRootMatches ? (
                      <>
                        <p className="text-xs text-slate-600">{selection.memberCount} selected {selection.resourceType} records</p>
                        <button type="button" className="mt-1 rounded-md border border-blue-700 px-2.5 py-1 text-xs font-semibold text-blue-800 hover:bg-blue-50" disabled={disabled} onClick={() => setGroupAuthoringOpen(true)}>
                          Create groups from this selection
                        </button>
                      </>
                    ) : selection ? (
                      <p className="mt-1 text-xs text-amber-900">The current selection uses {selection.resourceType}, while this table uses {table.document.rootResourceType}. Choose a matching existing selection before creating groups.</p>
                    ) : (
                      <p className="mt-1 text-xs text-slate-600">No saved record selection is attached. Open the Builder with a saved selection to create named cohorts.</p>
                    )}
                  </section>
                ) : null}
                </>
              ) : null}
                  {proposalState.kind === 'proposing' ? <p className="mt-4" role="status">Compiling and comparing row membership…</p> : null}
                  {proposalState.kind === 'stale' ? <p className="mt-4 text-amber-800" role="alert">This row change is out of date. Close Rows and choose it again.</p> : null}
                  {proposalState.kind === 'failed' ? <p className="mt-4 text-red-800" role="alert">{proposalState.message}</p> : null}
                  {proposalState.kind === 'applying' ? <p className="mt-4" role="status">Applying row definition…</p> : null}
                  {comparison ? (
              <section aria-label="Row definition preview" className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
                <h4 className="font-semibold text-slate-900">Effect on rows</h4>
                <p className="mt-1 text-sm text-slate-700">
                  {comparison.base?.rowCount ?? 'Unavailable'} rows → {comparison.candidate?.rowCount ?? 'Unavailable'} rows
                  {comparison.base?.sampled || comparison.candidate?.sampled ? ' · sampled' : ''}
                </p>
                {comparison.affectedColumns.length > 0 ? <p className="mt-2 text-xs text-slate-600">Affected columns: {comparison.affectedColumns.map((columnId) => table.document.columns.find((column) => column.column === columnId)?.label ?? columnId).join(', ')}</p> : null}
                {comparison.examples.length > 0 ? (
                  <ul className="mt-2 space-y-1 text-xs text-slate-600" aria-label="Membership changes">
                    {comparison.examples.map((example) => (
                      <li key={example.rowIdentity}>
                        {example.basePresent === example.candidatePresent ? 'Unchanged' : example.candidatePresent ? 'Added' : 'Removed'} · {example.rowIdentity}
                      </li>
                    ))}
                  </ul>
                ) : null}
                {comparison.notices.map((notice) => <p key={notice} className="mt-2 text-xs text-slate-600">{notice}</p>)}
              </section>
                  ) : null}
                  {proposalState.kind === 'fresh' && currentProposal?.proposalId && comparison?.status === 'AVAILABLE' ? (
              <div className="mt-4 flex justify-end gap-2">
                <button type="button" className="rounded-md border border-slate-300 px-3 py-2" onClick={cancel}>Cancel</button>
                <button type="button" className="rounded-md bg-blue-700 px-3 py-2 font-semibold text-white disabled:opacity-50" disabled={disabled || !isCurrentProposal(currentProposal, table, snapshotToken, draftVersion, draftDigest)} onClick={() => void apply()}>
                  Apply row definition
                </button>
              </div>
                  ) : null}
              </div>
              ) : null}
              {startingCollectionSettings ? (
                <details data-testid="construction-starting-collection-footer" className="mt-2 rounded-lg border border-slate-200 bg-white px-3 py-2">
                  <summary className="flex cursor-pointer flex-wrap items-center gap-x-2 text-sm font-medium text-slate-800 hover:text-blue-800">
                    <span>Starting collection</span>
                    <span className="text-xs font-normal text-slate-600">{startingCollectionDescription}</span>
                  </summary>
                  <div className="pt-2">{startingCollectionSettings}</div>
                </details>
              ) : null}
            </div>
        </div>
      ) : null}
      {replacementState.kind !== 'idle' ? (
        <div className="fixed inset-0 z-[60] overflow-y-auto bg-slate-950/40 p-4 sm:p-8">
          <section role="dialog" aria-modal="true" aria-labelledby="root-replacement-title" className="mx-auto max-w-3xl rounded-xl bg-white p-5 shadow-xl sm:p-7">
            <h2 id="root-replacement-title" className="text-xl font-semibold text-slate-900">Replace this table’s current setup?</h2>
            <p className="mt-2 text-sm text-slate-700">This replaces the current setup for {table.title}. The table, output, and tab keep their identity. Review the candidate and its effects before applying.</p>
            {replacementState.kind === 'assessing' ? <p role="status" className="mt-4 text-sm text-slate-700">Checking the replacement and preparing its preview…</p> : null}
            {replacementState.kind === 'loading-preview' ? <p role="status" className="mt-4 text-sm text-slate-700">Loading the receipt-bound replacement preview…</p> : null}
            {replacementState.kind === 'blocked' ? (
              <div className="mt-4 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950">
                <h3 className="font-semibold">This record type cannot replace the setup yet</h3>
                {replacementState.assessment.diagnostics.map((diagnostic, index) => (
                  <p key={`${diagnostic.code}:${index}`} className="mt-1">{diagnostic.message}</p>
                ))}
              </div>
            ) : null}
            {replacementState.kind === 'failed' ? <p role="alert" className="mt-4 text-sm text-red-800">{replacementState.message}</p> : null}
            {replacementState.kind === 'stale' || (replacementAssessment && !replacementIsCurrent) ? (
              <p role="alert" className="mt-4 text-sm text-amber-900">This replacement review is out of date. Choose the starting type again.</p>
            ) : null}
            {replacementAssessment ? (
              <section aria-label="Replacement consequences" className="mt-4 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-slate-800">
                <h3 className="font-semibold text-slate-900">What will change</h3>
                <dl className="mt-2 grid gap-2 sm:grid-cols-[10rem_1fr]">
                  <dt className="font-medium">Table</dt>
                  <dd>{table.title} · same output and tab</dd>
                  <dt className="font-medium">Starting record</dt>
                  <dd>{replacementAssessment.candidateRootResourceType}</dd>
                  <dt className="font-medium">Rows</dt>
                  <dd>One row per source record · {replacementState.kind === 'ready' ? `${replacementState.preview.rowCount} preview rows` : 'preview pending'}</dd>
                  {replacementState.kind === 'ready' ? (
                    <>
                      <dt className="font-medium">Default ID column</dt>
                      <dd>{replacementState.idColumn.label} · id (base value)</dd>
                    </>
                  ) : null}
                </dl>
                <div className="mt-3">
                  <h4 className="font-medium">Removed setup</h4>
                  {replacementRemovedSetup.length > 0 ? (
                    <ul className="mt-1 list-disc pl-5" aria-label="Removed setup features">
                      {replacementRemovedSetup.map((feature) => <li key={feature.key}>{feature.label}</li>)}
                    </ul>
                  ) : <p className="mt-1">No saved feature keys will be removed.</p>}
                </div>
                <div className="mt-3">
                  <h4 className="font-medium">Current-draft tables to repair</h4>
                  {replacementConsumers.length > 0 ? (
                    <ul className="mt-1 list-disc pl-5" aria-label="Affected current-draft consumers">
                      {replacementConsumers.map((consumer) => <li key={consumer.key}><span className="font-medium">{consumer.title}</span> · {consumer.detail}</li>)}
                    </ul>
                  ) : <p className="mt-1">No current-draft table inputs use this output.</p>}
                </div>
                <div className="mt-3">
                  <h4 className="font-medium">Shared filters to update</h4>
                  {(replacementAssessment.sharedFilterBindings ?? []).length > 0 ? (
                    <ul className="mt-1 list-disc pl-5" aria-label="Pruned shared-filter bindings">
                      {(replacementAssessment.sharedFilterBindings ?? []).map((binding) => (
                        <li key={`${binding.name}:${binding.outputId}:${binding.column}:${binding.bindingIndex}`}>
                          {sharedFilterPresentation(workspaceTables, binding)}
                        </li>
                      ))}
                    </ul>
                  ) : <p className="mt-1">No shared-filter bindings will be removed.</p>}
                </div>
              </section>
            ) : null}
            {replacementState.kind === 'ready' ? (
              <section className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
                <h3 className="font-semibold text-slate-900">Candidate preview</h3>
                <div className="mt-2 max-h-72 overflow-auto">
                  <table data-testid="construction-proposal-preview" className="min-w-full border-collapse text-left text-xs">
                    <thead><tr>{replacementState.preview.columns.map((column) => <th key={column.column} className="border border-slate-300 bg-slate-100 px-2 py-1 font-semibold">{column.label}</th>)}</tr></thead>
                    <tbody>
                      {replacementState.preview.rows?.map((row, rowIndex) => (
                        <tr key={rowIndex}>{replacementState.preview.columns.map((column) => <td key={column.column} className="border border-slate-300 px-2 py-1">{displayPreviewCell(row[column.column])}</td>)}</tr>
                      ))}
                      {replacementState.preview.rows?.length === 0 ? (
                        <tr><td className="border border-slate-300 px-2 py-2" colSpan={replacementState.preview.columns.length}>No sample rows are available.</td></tr>
                      ) : null}
                    </tbody>
                  </table>
                </div>
              </section>
            ) : null}
            <div className="mt-5 flex justify-end gap-2">
              <button type="button" className="rounded-md border border-slate-300 px-3 py-2" onClick={cancelReplacementReview}>Cancel</button>
              {replacementState.kind === 'ready' ? (
                <button
                  type="button"
                  className="rounded-md bg-amber-800 px-3 py-2 font-semibold text-white disabled:opacity-50"
                  disabled={disabled || !replacementIsCurrent}
                  onClick={() => void applyReplacement()}
                >
                  Change starting record type
                </button>
              ) : null}
              {replacementState.kind === 'applying' ? <span role="status" className="self-center text-sm text-slate-700">Applying replacement…</span> : null}
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
};
