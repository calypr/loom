import React, { useEffect, useRef, useState, type ReactNode } from 'react';
import type { LoomClient } from '../../../api';
import type {
  ExplicitGroupRevisionSummary,
  RowDefinitionChoicesResponse,
  RowDefinitionProposal,
  RowDefinitionSelection,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { SelectionRevision } from '../../../selection';
import { ExplicitGroupAuthoring } from './ExplicitGroupAuthoring';

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
  | { readonly kind: 'applying'; readonly selection: RowDefinitionSelection };

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
  disabled,
  onApply,
  relatedRows,
  reshapeRows,
  onChooseRelatedRows,
  onChooseReshape,
  onChangeRootOccurrence,
}: {
  readonly client: Pick<LoomClient, 'listRowDefinitionChoices' | 'proposeRowDefinition' | 'getSelection' | 'createExplicitGroupRevision'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly currentRowMeaning: string;
  readonly startingCollectionSummary: string;
  readonly renderRootSettings: (onRootChange: (nodeId: string, occurrenceId: string) => void) => ReactNode;
  readonly startingCollectionSettings: ReactNode;
  readonly selection?: SelectionRevision;
  readonly disabled: boolean;
  readonly onApply: (proposalId: string) => Promise<boolean>;
  readonly relatedRows: { readonly supported: boolean; readonly reason?: string };
  readonly reshapeRows: Record<'group' | 'pivot', { readonly supported: boolean; readonly reason?: string }> & {
    readonly groupEntry: 'group' | 'source-group' | 'coded-group';
    readonly groupAlternatives: ReadonlyArray<{
      readonly kind: 'group' | 'source-group' | 'coded-group';
      readonly label: string;
    }>;
    readonly pivotEntry?: 'pivot' | 'coded-pivot';
    readonly pivotAlternative?: 'pivot';
  };
  readonly onChooseRelatedRows: () => void;
  readonly onChooseReshape: (kind: 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories') => void;
  readonly onChangeRootOccurrence: (nodeId: string, occurrenceId: string) => void;
}) => {
  const [settings, setSettings] = useState<SettingsState>({ kind: 'closed' });
  const [proposalState, setProposalState] = useState<ProposalState>({ kind: 'none' });
  const [groupAuthoringOpen, setGroupAuthoringOpen] = useState(false);
  const settingsRequestEpoch = useRef(0);
  const previewTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const proposalController = useRef<AbortController | undefined>(undefined);
  const proposalRequestEpoch = useRef(0);

  const settingsOpen = settings.kind !== 'closed';

  const cancelPendingPreview = () => {
    if (previewTimer.current) clearTimeout(previewTimer.current);
    previewTimer.current = undefined;
    proposalController.current?.abort();
    proposalController.current = undefined;
    proposalRequestEpoch.current += 1;
  };

  useEffect(() => () => {
    if (previewTimer.current) clearTimeout(previewTimer.current);
    proposalController.current?.abort();
    proposalRequestEpoch.current += 1;
  }, [table.outputId, snapshotToken, draftVersion, draftDigest]);

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
      setSettings({ kind: 'editing', choices, options, selectionId: table.document.rows.kind === 'RECORDS' ? 'records' : '' });
    } catch (error) {
      if (requestEpoch !== settingsRequestEpoch.current) return;
      setSettings({
        kind: 'error',
        message: requestFailureMessage(error, 'Loom could not load row-definition choices. Reload the Builder and try again.'),
      });
    }
  };

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
      setProposalState({ kind: 'fresh', selection: selected, proposal });
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== proposalRequestEpoch.current) return;
      setProposalState({ kind: 'none' });
      setSettings({
        kind: 'error',
        message: requestFailureMessage(error, 'Loom could not preview this row definition. The saved draft was not changed.'),
      });
    } finally {
      if (proposalController.current === controller) proposalController.current = undefined;
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
    } else {
      setProposalState({ kind: 'stale', selection: proposalState.selection });
    }
  };

  const cancel = () => {
    cancelPendingPreview();
    settingsRequestEpoch.current += 1;
    setSettings({ kind: 'closed' });
    setProposalState({ kind: 'none' });
  };

  const chooseRelatedRows = () => {
    cancel();
    onChooseRelatedRows();
  };
  const chooseReshape = (kind: 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories') => {
    cancel();
    onChooseReshape(kind);
  };

  const currentProposal = proposalState.kind === 'fresh' ? proposalState.proposal : undefined;
  const comparison = currentProposal?.comparison;
  const selectedOption = settings.kind === 'editing'
    ? settings.options.find((option) => option.value === settings.selectionId)
    : undefined;
  const selectedPolicies = settings.kind === 'editing' && selectedOption
    ? settings.options.filter((option) => option.shapeValue === selectedOption.shapeValue && option.policyLabel)
    : [];
  const explicitGroupRootMatches = selection?.resourceType === table.document.rootResourceType;
  const startingCollectionDescription = startingCollectionSummary.replace(/^Starting collection:\s*/, '');
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
        aria-label="Configure rows"
        aria-haspopup="dialog"
        aria-expanded={settings.kind !== 'closed'}
        className="inline-flex max-w-full items-center gap-2 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1.5 text-left text-xs text-slate-700 hover:border-blue-300 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-60"
        disabled={disabled || settings.kind === 'loading'}
        onClick={() => void openSettings()}
      >
        <span className="shrink-0 font-semibold text-slate-900">Rows</span>
        <span className="truncate text-slate-600">{currentRowMeaning}</span>
        <span className="shrink-0 font-medium text-blue-800">Change <span aria-hidden="true">→</span></span>
      </button>
      {settings.kind !== 'closed' ? (
        <div className="fixed inset-0 z-50 overflow-y-auto bg-white">
          <div role="dialog" aria-modal="true" aria-label="Row definition settings" className="mx-auto min-h-dvh w-full max-w-5xl px-4 py-5 sm:px-8 sm:py-8">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h3 className="text-xl font-semibold text-slate-900">Define rows</h3>
                <p className="mt-1 text-xs text-slate-600">Currently: {currentRowMeaning}</p>
              </div>
              <button type="button" className="rounded-md border border-slate-300 px-3 py-1.5 text-sm" onClick={cancel}>Back to table</button>
            </div>
            <div className="mt-3 space-y-3">
              <section aria-label="Choose a row change" className="grid gap-2 md:grid-cols-3">
                <div className="rounded-lg border border-slate-200">
                  <button type="button" data-testid="construction-action-group-rows" disabled={disabled || !reshapeRows.group.supported}
                    onClick={() => chooseReshape(reshapeRows.groupEntry)}
                    className="w-full p-3 text-left hover:bg-blue-50 disabled:opacity-50">
                    <span className="block text-sm font-semibold text-slate-900">Group records</span>
                    <span className="mt-1 block text-xs text-slate-600">{reshapeRows.group.supported
                      ? reshapeRows.groupEntry === 'coded-group'
                        ? 'Group by a recorded code without adding a column first.'
                        : reshapeRows.groupEntry === 'source-group'
                          ? 'Group by a source field without adding a column first.'
                        : 'Group by a column already in this table.'
                      : reshapeRows.group.reason}</span>
                  </button>
                  {reshapeRows.groupAlternatives.length > 0 ? (
                    <div className="flex flex-wrap gap-1 border-t border-slate-200 px-2 py-1.5">
                      {reshapeRows.groupAlternatives.map((alternative) => (
                        <button key={alternative.kind} type="button" data-testid={`construction-action-${alternative.kind}-rows`}
                          disabled={disabled} onClick={() => chooseReshape(alternative.kind)}
                          className="rounded px-2 py-1 text-xs font-medium text-blue-800 hover:bg-blue-50">
                          {alternative.label}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
                <div className="rounded-lg border border-slate-200">
                  <button type="button" data-testid="construction-action-pivot-rows" disabled={disabled}
                    onClick={() => chooseReshape('categories')}
                    className="w-full p-3 text-left hover:bg-blue-50 disabled:opacity-50">
                    <span className="block text-sm font-semibold text-slate-900">Categories to columns</span>
                    <span className="mt-1 block text-xs text-slate-600">Choose coded values, or choose fields for the rows, categories, and values.</span>
                  </button>
                  {reshapeRows.pivotAlternative ? <div className="border-t border-slate-100 px-2 pb-2">
                    <button type="button" data-testid="construction-action-table-pivot-rows"
                      disabled={disabled} onClick={() => chooseReshape('pivot')}
                      className="rounded px-2 py-1 text-xs font-medium text-blue-800 hover:bg-blue-50">
                      Choose category and value fields
                    </button>
                  </div> : null}
                </div>
                <button type="button" disabled={disabled || !relatedRows.supported}
                  onClick={chooseRelatedRows}
                  className="rounded-lg border border-slate-200 p-3 text-left hover:border-blue-400 hover:bg-blue-50 disabled:opacity-50">
                  <span className="block text-sm font-semibold text-slate-900">Related records</span>
                  <span className="mt-1 block text-xs text-slate-600">{relatedRows.supported
                    ? 'One row per matching record on a related path.'
                    : relatedRows.reason ?? 'Checking available related row paths…'}</span>
                </button>
              </section>
              <div className="min-w-0">
                <section aria-label="Row shape settings" className="text-sm text-slate-800">
                  {settings.kind === 'loading' ? <p className="mt-4" role="status">Loading row choices…</p> : null}
                  {settings.kind === 'error' ? <p className="mt-4 text-red-800" role="alert">{settings.message}</p> : null}
                  {settings.kind === 'editing' ? (
                    <>
                <label className="block text-sm font-medium text-slate-800">
                  <span>Start with records or expand repeated values</span>
                  <select
                    aria-label="What should each row represent?"
                    className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2"
                    value={selectedOption?.shapeValue ?? ''}
                    disabled={disabled || proposalState.kind === 'proposing' || proposalState.kind === 'applying'}
                    onChange={(event) => {
                      const next = settings.options.find((option) => option.shapeValue === event.currentTarget.value);
                      if (!next) return;
                      setSettings({ ...settings, selectionId: next.value });
                      schedulePreview(next.selection);
                    }}
                  >
                    <option value="" disabled>Choose a row change</option>
                    {shapeOptions(settings.options).map((option) => <option key={option.shapeValue} value={option.shapeValue}>{option.shapeLabel}</option>)}
                  </select>
                </label>
                {selectedPolicies.length > 1 ? (
                  <section aria-label="Unmatched record handling" className="mt-3 grid gap-1">
                    <label className="block text-xs font-medium text-slate-700">
                      <span>Change how unmatched records are handled</span>
                      <select
                        aria-label="Unmatched record policy"
                        className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm"
                        value={settings.selectionId}
                        disabled={disabled || proposalState.kind === 'proposing' || proposalState.kind === 'applying'}
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
                  </section>
                ) : null}
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
                {!groupAuthoringOpen ? (
                  <section aria-label="Named cohorts" className="mt-4 border-t border-slate-200 pt-3">
                    <h4 className="text-sm font-semibold text-slate-900">Named cohorts</h4>
                    {selection && explicitGroupRootMatches ? (
                      <>
                        <p className="mt-1 text-xs text-slate-600">Start with the {selection.memberCount} selected {selection.resourceType} records, then name each cohort.</p>
                        <button type="button" className="mt-2 rounded-md border border-blue-700 px-3 py-2 text-xs font-semibold text-blue-800 hover:bg-blue-50" disabled={disabled} onClick={() => setGroupAuthoringOpen(true)}>
                          Create groups from this selection
                        </button>
                      </>
                    ) : selection ? (
                      <p className="mt-1 text-xs text-amber-900">The current selection uses {selection.resourceType}, while this table uses {table.document.rootResourceType}. Choose a matching existing selection before creating groups.</p>
                    ) : (
                      <p className="mt-1 text-xs text-slate-600">To name cohorts, choose a saved collection below.</p>
                    )}
                  </section>
                ) : null}
                    </>
                  ) : null}
                  {proposalState.kind === 'proposing' ? <p className="mt-4" role="status">Compiling and comparing row membership…</p> : null}
                  {proposalState.kind === 'stale' ? <p className="mt-4 text-amber-800" role="alert">This row change is out of date. Close Rows and choose it again.</p> : null}
                  {proposalState.kind === 'applying' ? <p className="mt-4" role="status">Applying row definition…</p> : null}
                  {comparison ? (
              <section aria-label="Row definition preview" className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
                <h4 className="font-semibold text-slate-900">Effect on rows</h4>
                <p className="mt-1 text-sm text-slate-700">
                  {comparison.base?.rowCount ?? 'Unavailable'} rows → {comparison.candidate?.rowCount ?? 'Unavailable'} rows
                  {comparison.base?.sampled || comparison.candidate?.sampled ? ' · sampled' : ''}
                </p>
                {comparison.status === 'UNAVAILABLE' ? <p role="status" className="mt-2 text-amber-900">{comparison.reason}</p> : null}
                {comparison.affectedColumns.length > 0 ? <p className="mt-2 text-xs text-slate-600">Affected columns: {comparison.affectedColumns.join(', ')}</p> : null}
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
                  {proposalState.kind === 'fresh' && !currentProposal?.proposalId ? (
              <p role="status" className="mt-3 text-amber-900">{comparison?.status === 'UNAVAILABLE' ? comparison.reason : 'The server did not issue an applicable proposal.'}</p>
                  ) : null}
                  {proposalState.kind === 'fresh' && currentProposal?.proposalId ? (
              <div className="mt-4 flex justify-end gap-2">
                <button type="button" className="rounded-md border border-slate-300 px-3 py-2" onClick={cancel}>Cancel</button>
                <button type="button" className="rounded-md bg-blue-700 px-3 py-2 font-semibold text-white disabled:opacity-50" disabled={disabled || !isCurrentProposal(currentProposal, table, snapshotToken, draftVersion, draftDigest)} onClick={() => void apply()}>
                  Apply row definition
                </button>
              </div>
                  ) : null}
                </section>
              </div>
              <section aria-label="Starting record type" className="min-w-0 border-t border-slate-200 pt-3">
                <h4 className="text-sm font-semibold text-slate-900">Starting record type</h4>
                <div className="mt-2">{renderRootSettings((nodeId, occurrenceId) => {
                  cancel();
                  onChangeRootOccurrence(nodeId, occurrenceId);
                })}</div>
              </section>
              <section aria-label="Starting collection settings" className="min-w-0 border-t border-slate-200 pt-3">
                <h4 className="text-sm font-semibold text-slate-900">Starting collection</h4>
                <p className="mt-1 text-xs text-slate-600">{startingCollectionDescription}</p>
                <div className="mt-2">{startingCollectionSettings}</div>
              </section>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
};
