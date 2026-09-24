import React, { useEffect, useState } from 'react';
import type { LoomClient } from '../../../api';
import type {
  ExplicitGroupRevisionSummary,
  ExplicitGroupRevisionChoice,
  ExplorerRowDefinition,
  RowDefinitionChoice,
  RowDefinitionChoicesResponse,
  RowDefinitionProposal,
  RowDefinitionSelection,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { SelectionRevision } from '../../../selection';
import { ExplicitGroupAuthoring } from './ExplicitGroupAuthoring';

type ExpandedChoice = Extract<RowDefinitionChoice, { kind: 'EXPANDED' }>;
type ExpandedPolicy = Extract<RowDefinitionSelection, { kind: 'EXPANDED' }>['expanded']['emptyCollectionPolicy'];
type GroupPolicy = Extract<RowDefinitionSelection, { kind: 'EXPLICIT_GROUP' }>['explicitGroup']['unassignedMemberPolicy'];
type SelectionOption =
  | { readonly kind: 'RECORDS'; readonly value: 'records'; readonly label: string }
  | { readonly kind: 'EXPANDED'; readonly value: string; readonly label: string; readonly choice: ExpandedChoice; readonly policies: ReadonlyArray<ExpandedPolicy> }
  | { readonly kind: 'EXPLICIT_GROUP'; readonly value: string; readonly label: string; readonly group: ExplicitGroupRevisionChoice; readonly policies: ReadonlyArray<GroupPolicy> };

type SettingsState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'loading' }
  | {
      readonly kind: 'editing';
      readonly choices: RowDefinitionChoicesResponse;
      readonly options: ReadonlyArray<SelectionOption>;
      readonly selectionId: string;
      readonly policyId: string;
    }
  | { readonly kind: 'error'; readonly message: string };

type ProposalState =
  | { readonly kind: 'none' }
  | { readonly kind: 'proposing'; readonly selection: RowDefinitionSelection }
  | {
      readonly kind: 'fresh';
      readonly selection: RowDefinitionSelection;
      readonly proposal: RowDefinitionProposal;
    }
  | { readonly kind: 'stale'; readonly selection: RowDefinitionSelection }
  | { readonly kind: 'applying'; readonly selection: RowDefinitionSelection };

const readableField = (label: string): string => {
  const path = label.includes('[]') ? label.replace(/^[^.]+\./, '') : label;
  return path.replace(/\[\]/g, '').replace(/\./g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase();
};

const selectionOptions = (choices: RowDefinitionChoicesResponse, resourceType: string): ReadonlyArray<SelectionOption> => {
  const options: SelectionOption[] = [{
    kind: 'RECORDS',
    value: 'records',
    label: `One row per ${resourceType} record`,
  }];
  for (const choice of choices.choices) {
    if (choice.kind !== 'EXPANDED') continue;
    for (const policy of choice.policies) {
      if (policy.name !== 'emptyCollectionPolicy') continue;
      options.push({
        kind: 'EXPANDED', value: `expanded:${choice.choiceId}`,
        label: `One row per ${readableField(choice.label)}`, choice, policies: policy.options,
      });
    }
  }
  for (const group of choices.explicitGroups) {
    options.push({
      kind: 'EXPLICIT_GROUP', value: `explicit:${group.revisionId}`,
      label: `One row per saved group (${group.groupCount} groups, ${group.memberCount} records)`,
      group, policies: group.unassignedMemberPolicies,
    });
  }
  return options;
};

const selectionFor = (option: SelectionOption, policyId: string): RowDefinitionSelection | undefined => {
  switch (option.kind) {
    case 'RECORDS': return { kind: 'RECORDS' };
    case 'EXPANDED': {
      const emptyCollectionPolicy = option.policies.find((policy) => policy === policyId);
      return emptyCollectionPolicy ? { kind: 'EXPANDED', expanded: { rowChoiceId: option.choice.choiceId, emptyCollectionPolicy } } : undefined;
    }
    case 'EXPLICIT_GROUP': {
      const unassignedMemberPolicy = option.policies.find((policy) => policy === policyId);
      return unassignedMemberPolicy ? { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: option.group.revisionId, unassignedMemberPolicy } } : undefined;
    }
    default: {
      const _exhaustive: never = option;
      return _exhaustive;
    }
  }
};

const defaultPolicyFor = (option: SelectionOption): string => {
  switch (option.kind) {
    case 'RECORDS': return '';
    case 'EXPANDED': return option.policies.find((policy) => policy === 'PRESERVE_PARENT') ?? option.policies[0];
    case 'EXPLICIT_GROUP': return option.policies.find((policy) => policy === 'ERROR') ?? option.policies[0];
    default: {
      const _exhaustive: never = option;
      return _exhaustive;
    }
  }
};

const policyLabel = (policy: ExpandedPolicy | GroupPolicy): string => {
  switch (policy) {
    case 'ERROR': return 'Stop if any record has no match';
    case 'EXCLUDE': return 'Leave records with no match out';
    case 'PRESERVE_PARENT': return 'Keep a row with an empty value';
    case 'GROUP_AS_UNASSIGNED': return 'Put unmatched records in an Unassigned group';
    default: {
      const _exhaustive: never = policy;
      return _exhaustive;
    }
  }
};

const describeCurrentRows = (rows: ExplorerRowDefinition, resourceType: string): string => {
  switch (rows.kind) {
    case 'RECORDS':
      return `One row per ${resourceType} record`;
    case 'GROUPS':
      switch (rows.groups.source.kind) {
        case 'FIELD':
          return `One row per ${readableField(rows.groups.source.field.fieldPath)} group`;
        case 'EXPLICIT':
          return 'One row per saved group';
        default: {
          const _exhaustive: never = rows.groups.source;
          return _exhaustive;
        }
      }
    case 'EXPANDED':
      return `One row per ${readableField(rows.expanded.scopePath)}`;
    default: {
      const _exhaustive: never = rows;
      return _exhaustive;
    }
  }
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
  selection,
  disabled,
  onApply,
}: {
  readonly client: Pick<LoomClient, 'listRowDefinitionChoices' | 'proposeRowDefinition' | 'getSelection' | 'createExplicitGroupRevision'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly selection?: SelectionRevision;
  readonly disabled: boolean;
  readonly onApply: (proposalId: string) => Promise<boolean>;
}) => {
  const [settings, setSettings] = useState<SettingsState>({ kind: 'closed' });
  const [proposalState, setProposalState] = useState<ProposalState>({ kind: 'none' });
  const [groupAuthoringOpen, setGroupAuthoringOpen] = useState(false);

  useEffect(() => {
    setProposalState((current) => current.kind === 'fresh' && !isCurrentProposal(
      current.proposal, table, snapshotToken, draftVersion, draftDigest,
    ) ? { kind: 'stale', selection: current.selection } : current);
  }, [table, snapshotToken, draftVersion, draftDigest]);

  const openSettings = async () => {
    setSettings({ kind: 'loading' });
    setProposalState({ kind: 'none' });
    setGroupAuthoringOpen(false);
    try {
      const choices = await client.listRowDefinitionChoices({
        project, explorerId, authResourcePath, snapshotToken, outputId: table.outputId,
      });
      if (choices.outputId !== table.outputId || choices.snapshotToken !== snapshotToken) {
        setSettings({ kind: 'error', message: 'The server returned row choices for a different table or catalog snapshot.' });
        return;
      }
      const options = selectionOptions(choices, table.document.rootResourceType);
      setSettings({ kind: 'editing', choices, options, selectionId: options[0].value, policyId: '' });
    } catch (error) {
      setSettings({
        kind: 'error',
        message: requestFailureMessage(error, 'Loom could not load row-definition choices. Reload the Builder and try again.'),
      });
    }
  };

  const propose = async () => {
    if (settings.kind !== 'editing') return;
    const selected = settings.options.find((option) => option.value === settings.selectionId);
    const rowSelection = selected && selectionFor(selected, settings.policyId);
    if (!rowSelection) return;
    setProposalState({ kind: 'proposing', selection: rowSelection });
    try {
      const proposal = await client.proposeRowDefinition({
        project, explorerId, authResourcePath, snapshotToken, expectedDraftVersion: draftVersion,
        expectedDraftDigest: draftDigest, outputId: table.outputId, selection: rowSelection,
      });
      if (!isCurrentProposal(proposal, table, snapshotToken, draftVersion, draftDigest)) {
        setProposalState({ kind: 'stale', selection: rowSelection });
        return;
      }
      setProposalState({ kind: 'fresh', selection: rowSelection, proposal });
    } catch (error) {
      setProposalState({ kind: 'none' });
      setSettings({
        kind: 'error',
        message: requestFailureMessage(error, 'Loom could not preview this row definition. The saved draft was not changed.'),
      });
    }
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
    setSettings({ kind: 'closed' });
    setProposalState({ kind: 'none' });
    setGroupAuthoringOpen(false);
  };

  const currentProposal = proposalState.kind === 'fresh' ? proposalState.proposal : undefined;
  const comparison = currentProposal?.comparison;
  const explicitGroupRootMatches = selection?.resourceType === table.document.rootResourceType;
  const onExplicitGroupsCreated = async (revision: ExplicitGroupRevisionSummary) => {
    const choices = await client.listRowDefinitionChoices({
      project, explorerId, authResourcePath, snapshotToken, outputId: table.outputId,
    });
    if (choices.outputId !== table.outputId || choices.snapshotToken !== snapshotToken ||
        !choices.explicitGroups.some((group) => group.revisionId === revision.revisionId)) {
      throw new Error('The new group revision is not available for this table. Check that its selected resource type matches the table row type.');
    }
    const options = selectionOptions(choices, table.document.rootResourceType);
    const preferred = options.find((option) => option.value === `explicit:${revision.revisionId}`);
    setSettings({
      kind: 'editing', choices, options, selectionId: preferred?.value ?? options[0].value,
      policyId: preferred ? defaultPolicyFor(preferred) : '',
    });
    setProposalState({ kind: 'none' });
    setGroupAuthoringOpen(false);
  };
  const selectedOption = settings.kind === 'editing'
    ? settings.options.find((option) => option.value === settings.selectionId)
    : undefined;

  return (
    <section aria-label="Row definition settings" className="rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-slate-800 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold text-slate-900">Row definition settings</h2>
          <p className="mt-1 text-xs text-slate-600">Current rows: {describeCurrentRows(table.document.rows, table.document.rootResourceType)}</p>
        </div>
        <button
          type="button"
          className="rounded-md border border-slate-300 bg-white px-3 py-2 font-medium text-slate-800 hover:bg-slate-50 disabled:opacity-50"
          disabled={disabled || settings.kind === 'loading'}
          onClick={() => void openSettings()}
        >
          Configure rows
        </button>
      </div>
      {settings.kind !== 'closed' ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/30 p-4">
          <div role="dialog" aria-modal="true" aria-labelledby="row-definition-dialog-title" className="max-h-[90vh] w-full max-w-xl overflow-auto rounded-xl bg-white p-5 shadow-xl">
            <h3 id="row-definition-dialog-title" className="text-lg font-semibold text-slate-900">Choose what each row represents</h3>
            <p className="mt-1 text-sm text-slate-600">Current rows: {describeCurrentRows(table.document.rows, table.document.rootResourceType)}</p>
            {settings.kind === 'loading' ? <p className="mt-4" role="status">Loading row choices…</p> : null}
            {settings.kind === 'error' ? <p className="mt-4 text-red-800" role="alert">{settings.message}</p> : null}
            {settings.kind === 'error' ? (
              <div className="mt-4 flex justify-end">
                <button type="button" className="rounded-md border border-slate-300 px-3 py-2" onClick={cancel}>Close</button>
              </div>
            ) : null}
            {settings.kind === 'editing' ? (
              <>
                <label className="mt-4 block text-sm font-medium text-slate-800">
                  <span>Make each row</span>
                  <select
                    aria-label="Make each row"
                    className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2"
                    value={settings.selectionId}
                    disabled={disabled || proposalState.kind === 'proposing' || proposalState.kind === 'applying'}
                    onChange={(event) => {
                      const option = settings.options.find((candidate) => candidate.value === event.currentTarget.value);
                      if (!option) return;
                      setSettings({ ...settings, selectionId: option.value, policyId: defaultPolicyFor(option) });
                      setProposalState({ kind: 'none' });
                    }}
                  >
                    {settings.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </select>
                </label>
                {selectedOption?.kind === 'RECORDS' ? (
                  <p className="mt-2 text-xs text-slate-600">Each selected {table.document.rootResourceType} record makes one row.</p>
                ) : null}
                {selectedOption?.kind === 'EXPANDED' ? (
                  <p className="mt-2 text-xs text-slate-600">A record can make several rows, one for each {readableField(selectedOption.choice.label)} item.</p>
                ) : null}
                {selectedOption?.kind === 'EXPLICIT_GROUP' ? (
                  <p className="mt-2 text-xs text-slate-600">Each saved group makes one row. A record may belong to more than one group.</p>
                ) : null}
                {selectedOption && selectedOption.kind !== 'RECORDS' ? (
                  <fieldset className="mt-4 rounded-lg border border-slate-200 p-3">
                    <legend className="px-1 text-sm font-medium text-slate-800">What if a record has no match?</legend>
                    <div className="mt-1 space-y-2">
                      {selectedOption.policies.map((policy) => (
                        <label key={policy} className="flex min-h-8 items-center gap-2 text-sm text-slate-700">
                          <input type="radio" name="row-missing-policy" value={policy} checked={settings.policyId === policy}
                            disabled={disabled || proposalState.kind === 'proposing' || proposalState.kind === 'applying'}
                            onChange={() => { setSettings({ ...settings, policyId: policy }); setProposalState({ kind: 'none' }); }} />
                          {policyLabel(policy)}
                        </label>
                      ))}
                    </div>
                  </fieldset>
                ) : null}
                {selectedOption && selectedOption.kind !== 'RECORDS' ? (
                  <details className="mt-3 text-xs text-slate-600">
                    <summary className="cursor-pointer font-medium text-blue-800">Technical source details</summary>
                    {selectedOption.kind === 'EXPANDED' ? (
                      <p className="mt-1">{selectedOption.choice.occurrenceSummary} · {selectedOption.choice.routeSummary} · {selectedOption.choice.label}</p>
                    ) : (
                      <p className="mt-1">Saved group revision {selectedOption.group.revisionId}</p>
                    )}
                  </details>
                ) : null}
                <details className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
                  <summary className="cursor-pointer text-sm font-medium text-blue-800">Create custom groups</summary>
                  {settings.choices.explicitGroups.length === 0 ? (
                    <p className="mt-2 text-xs text-slate-600">No saved groups are available for this table yet.</p>
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
                  ) : selection && explicitGroupRootMatches ? (
                    <>
                      <p className="mt-2 text-xs text-slate-600">Use the current selection of {selection.memberCount} {selection.resourceType} records as the starting set.</p>
                      <button type="button" className="mt-3 rounded-md border border-blue-700 px-3 py-2 text-xs font-semibold text-blue-800 hover:bg-blue-50" disabled={disabled} onClick={() => setGroupAuthoringOpen(true)}>
                        Create groups from this selection
                      </button>
                    </>
                  ) : selection ? (
                    <p className="mt-2 text-xs text-amber-900">The current selection contains {selection.resourceType} records. Choose a {table.document.rootResourceType} selection to make groups for this table.</p>
                  ) : (
                    <p className="mt-2 text-xs text-slate-600">Choose a starting collection before making custom groups.</p>
                  )}
                </details>
                <div className="mt-4 flex flex-wrap justify-end gap-2">
                  <button type="button" className="rounded-md border border-slate-300 px-3 py-2" onClick={cancel}>Cancel</button>
                  <button type="button" className="rounded-md bg-blue-700 px-3 py-2 font-semibold text-white disabled:opacity-50" disabled={disabled || proposalState.kind === 'proposing' || proposalState.kind === 'applying'} onClick={() => void propose()}>
                    Preview row change
                  </button>
                </div>
              </>
            ) : null}
            {proposalState.kind === 'proposing' ? <p className="mt-4" role="status">Compiling and comparing row membership…</p> : null}
            {proposalState.kind === 'stale' ? <p className="mt-4 text-amber-800" role="alert">This proposal is stale. Preview the row change again before applying it.</p> : null}
            {proposalState.kind === 'applying' ? <p className="mt-4" role="status">Applying row definition…</p> : null}
            {comparison ? (
              <section aria-label="Row definition preview" className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
                <h4 className="font-semibold text-slate-900">Preview</h4>
                <p className="mt-1">Base rows: {comparison.base?.rowCount ?? 'Unavailable'}{comparison.base?.sampled ? ' (sampled)' : ''}</p>
                <p>Candidate rows: {comparison.candidate?.rowCount ?? 'Unavailable'}{comparison.candidate?.sampled ? ' (sampled)' : ''}</p>
                {comparison.status === 'UNAVAILABLE' ? <p role="status" className="mt-2 text-amber-900">{comparison.reason}</p> : null}
                {comparison.affectedColumns.length > 0 ? <p className="mt-2">Affected columns: {comparison.affectedColumns.join(', ')}</p> : null}
                {comparison.examples.length > 0 ? (
                  <ul className="mt-2 space-y-1" aria-label="Membership changes">
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
          </div>
        </div>
      ) : null}
    </section>
  );
};
