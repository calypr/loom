import React, { useRef, useState } from 'react';
import type { LoomClient } from '../../../api';
import type {
  ExplorerRowDefinition,
  RowDefinitionChoice,
  RowDefinitionProposal,
} from '../../../types';

type ExpandedChoice = Extract<RowDefinitionChoice, { readonly kind: 'EXPANDED' }>;
type EmptyCollectionPolicy = ExpandedChoice['policies'][number]['options'][number];

export type SourceCollectionExpandContext = {
  readonly client: Pick<LoomClient, 'listRowDefinitionChoices' | 'proposeRowDefinition'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly rowsKind: ExplorerRowDefinition['kind'];
  readonly onApply: (proposalId: string) => Promise<boolean>;
};

type SourceSelection = {
  readonly rowChoiceId: string;
  readonly emptyCollectionPolicy: EmptyCollectionPolicy;
};

type SourceDialogState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'loading'; readonly identity: string }
  | { readonly kind: 'load-error'; readonly identity: string; readonly message: string }
  | {
      readonly kind: 'editing';
      readonly identity: string;
      readonly choices: ReadonlyArray<ExpandedChoice>;
      readonly selection?: SourceSelection;
      readonly proposalState: 'idle' | 'proposing' | 'ready' | 'error' | 'applying';
      readonly proposal?: RowDefinitionProposal;
      readonly message?: string;
    };

const contextIdentity = (context: SourceCollectionExpandContext): string => JSON.stringify([
  context.project,
  context.explorerId,
  context.authResourcePath ?? '',
  context.snapshotToken,
  context.draftVersion,
  context.draftDigest,
  context.outputId,
]);

const isCurrentProposal = (
  proposal: RowDefinitionProposal,
  context: SourceCollectionExpandContext,
): boolean => proposal.outputId === context.outputId
  && proposal.snapshotToken === context.snapshotToken
  && proposal.draftVersion === context.draftVersion
  && proposal.draftDigest === context.draftDigest;

const policyLabel = (policy: EmptyCollectionPolicy): string => {
  switch (policy) {
    case 'PRESERVE_PARENT': return 'Keep a record with no values as one empty row';
    case 'EXCLUDE': return 'Leave out records with no values';
    case 'ERROR': return 'Require at least one value for every record';
  }
};

const sourceChoiceLabel = (choice: ExpandedChoice): string =>
  `${choice.label} · ${choice.occurrenceSummary} (${choice.fieldPath})`;

const defaultPolicy = (choice: ExpandedChoice): EmptyCollectionPolicy =>
  choice.policies[0]?.options.includes('PRESERVE_PARENT')
    ? 'PRESERVE_PARENT'
    : choice.policies[0]!.options[0]!;

export const SourceCollectionExpandEditor = ({
  context,
  disabled,
}: {
  readonly context: SourceCollectionExpandContext;
  readonly disabled: boolean;
}) => {
  const [state, setState] = useState<SourceDialogState>({ kind: 'closed' });
  const requestEpoch = useRef(0);
  const requestController = useRef<AbortController | undefined>(undefined);
  const latestContext = useRef(context);
  latestContext.current = context;

  const cancelRequest = () => {
    requestEpoch.current += 1;
    requestController.current?.abort();
    requestController.current = undefined;
  };

  const close = () => {
    cancelRequest();
    setState({ kind: 'closed' });
  };

  const open = async () => {
    const current = latestContext.current;
    if (!current || disabled || current.rowsKind === 'GROUPS') return;
    cancelRequest();
    const epoch = requestEpoch.current;
    const identity = contextIdentity(current);
    const controller = new AbortController();
    requestController.current = controller;
    setState({ kind: 'loading', identity });
    try {
      const response = await current.client.listRowDefinitionChoices({
        project: current.project,
        explorerId: current.explorerId,
        ...(current.authResourcePath ? { authResourcePath: current.authResourcePath } : {}),
        snapshotToken: current.snapshotToken,
        outputId: current.outputId,
      }, controller.signal);
      if (controller.signal.aborted || epoch !== requestEpoch.current) return;
      const latest = latestContext.current;
      if (!latest || contextIdentity(latest) !== identity
        || response.outputId !== current.outputId
        || response.snapshotToken !== current.snapshotToken) {
        setState({ kind: 'load-error', identity, message: 'The table or catalog changed while repeated fields were loading. Close this panel and try again.' });
        return;
      }
      const choices = response.choices.filter((choice): choice is ExpandedChoice => choice.kind === 'EXPANDED');
      if (choices.length === 0) {
        setState({ kind: 'load-error', identity, message: 'No repeated fields are available for this table.' });
        return;
      }
      setState({ kind: 'editing', identity, choices, proposalState: 'idle' });
    } catch (error) {
      if (controller.signal.aborted || epoch !== requestEpoch.current) return;
      setState({
        kind: 'load-error',
        identity,
        message: error instanceof Error ? error.message : 'Repeated fields could not be loaded.',
      });
    } finally {
      if (requestController.current === controller) requestController.current = undefined;
    }
  };

  const propose = async (choices: ReadonlyArray<ExpandedChoice>, selection: SourceSelection, identity: string) => {
    cancelRequest();
    const epoch = requestEpoch.current;
    const current = latestContext.current;
    if (!current || contextIdentity(current) !== identity || current.rowsKind === 'GROUPS') {
      setState({ kind: 'load-error', identity, message: 'Source expansion is unavailable for the current grouped rows. Keep or change the group row definition separately.' });
      return;
    }
    const controller = new AbortController();
    requestController.current = controller;
    setState({ kind: 'editing', identity, choices, selection, proposalState: 'proposing' });
    try {
      const proposal = await current.client.proposeRowDefinition({
        project: current.project,
        explorerId: current.explorerId,
        ...(current.authResourcePath ? { authResourcePath: current.authResourcePath } : {}),
        snapshotToken: current.snapshotToken,
        expectedDraftVersion: current.draftVersion,
        expectedDraftDigest: current.draftDigest,
        outputId: current.outputId,
        selection: { kind: 'EXPANDED', expanded: selection },
      }, controller.signal);
      if (controller.signal.aborted || epoch !== requestEpoch.current) return;
      const latest = latestContext.current;
      if (!latest || contextIdentity(latest) !== identity || !isCurrentProposal(proposal, latest)) {
        setState({
          kind: 'editing', identity, choices, selection, proposalState: 'error',
          message: 'The table changed while source expansion was being previewed. Close this panel and choose it again.',
        });
        return;
      }
      if (proposal.comparison.status !== 'AVAILABLE' || !proposal.proposalId) {
        setState({
          kind: 'editing', identity, choices, selection, proposalState: 'error',
          message: proposal.comparison.status === 'UNAVAILABLE'
            ? proposal.comparison.reason
            : 'Loom returned an incomplete source-expansion preview. The saved table was not changed.',
        });
        return;
      }
      setState({ kind: 'editing', identity, choices, selection, proposalState: 'ready', proposal });
    } catch (error) {
      if (controller.signal.aborted || epoch !== requestEpoch.current) return;
      setState({
        kind: 'editing', identity, choices, selection, proposalState: 'error',
        message: error instanceof Error ? error.message : 'Source expansion could not be previewed. The saved table was not changed.',
      });
    } finally {
      if (requestController.current === controller) requestController.current = undefined;
    }
  };

  const chooseScope = (choiceId: string) => {
    if (state.kind !== 'editing') return;
    const choice = state.choices.find((candidate) => candidate.choiceId === choiceId);
    if (!choice) return;
    const policies = choice.policies[0]?.options ?? [];
    const emptyCollectionPolicy = state.selection && state.selection.rowChoiceId === choiceId
      && policies.includes(state.selection.emptyCollectionPolicy)
      ? state.selection.emptyCollectionPolicy
      : defaultPolicy(choice);
    void propose(state.choices, { rowChoiceId: choice.choiceId, emptyCollectionPolicy }, state.identity);
  };

  const choosePolicy = (emptyCollectionPolicy: EmptyCollectionPolicy) => {
    if (state.kind !== 'editing' || !state.selection) return;
    const choice = state.choices.find((candidate) => candidate.choiceId === state.selection?.rowChoiceId);
    if (!choice || !choice.policies[0]?.options.includes(emptyCollectionPolicy)) return;
    void propose(state.choices, { ...state.selection, emptyCollectionPolicy }, state.identity);
  };

  const apply = async () => {
    if (state.kind !== 'editing' || state.proposalState !== 'ready' || !state.proposal?.proposalId) return;
    const current = latestContext.current;
    if (!current || contextIdentity(current) !== state.identity || !isCurrentProposal(state.proposal, current)) {
      setState({ ...state, proposalState: 'error', message: 'The table changed before source expansion could be applied. Close this panel and choose it again.' });
      return;
    }
    setState({ ...state, proposalState: 'applying' });
    let applied = false;
    try {
      applied = await current.onApply(state.proposal.proposalId);
    } catch (error) {
      setState({
        ...state, proposalState: 'error',
        message: error instanceof Error ? error.message : 'Source expansion could not be applied. The saved table was not changed.',
      });
      return;
    }
    if (applied) {
      close();
    } else {
      setState({
        ...state, proposalState: 'error',
        message: 'The source-expansion proposal is no longer current. The saved table was not changed.',
      });
    }
  };

  const disabledByRows = context?.rowsKind === 'GROUPS';
  const applying = state.kind === 'editing' && state.proposalState === 'applying';
  return (
    <>
      <div className="border-t border-slate-200 px-3 py-2">
        <button
          type="button"
          data-testid="construction-reshape-expand-source"
          disabled={disabled || disabledByRows}
          onClick={() => void open()}
          className="text-left text-xs font-semibold text-blue-800 hover:underline disabled:cursor-not-allowed disabled:text-slate-500"
          aria-describedby={disabledByRows ? 'source-expansion-grouped-rows-reason' : undefined}
        >
          Expand a repeated source field
        </button>
        <p className="mt-1 text-xs text-slate-600">Create one row per item in a repeated source field, then run the existing row operations.</p>
        {disabledByRows ? <p id="source-expansion-grouped-rows-reason" className="mt-1 text-xs text-amber-900">Source expansion cannot replace grouped rows. Change the row definition separately first.</p> : null}
      </div>
      {state.kind !== 'closed' ? (
        <div className="fixed inset-0 z-[60] overflow-y-auto bg-white">
          <section role="dialog" aria-modal="true" aria-label="Expand a repeated source field" className="mx-auto min-h-dvh w-full max-w-3xl px-4 py-5 sm:px-8 sm:py-8">
            <header className="flex items-start justify-between gap-4">
              <div>
                <h3 className="text-xl font-semibold text-slate-900">Expand a repeated source field</h3>
                <p className="mt-2 text-sm text-slate-700">This changes the table's source rows. Review the preview before applying.</p>
              </div>
              <button type="button" className="shrink-0 rounded-md border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50" disabled={applying} onClick={close}>Cancel</button>
            </header>
            {state.kind === 'loading' ? <p className="mt-5" role="status">Loading repeated fields…</p> : null}
            {state.kind === 'load-error' ? <p className="mt-5 text-sm text-red-800" role="alert">{state.message}</p> : null}
            {state.kind === 'editing' ? (() => {
                  const selectedChoice = state.choices.find((choice) => choice.choiceId === state.selection?.rowChoiceId);
                  const selectedPolicies = selectedChoice?.policies[0]?.options ?? [];
                  const comparison = state.proposalState === 'ready' ? state.proposal?.comparison : undefined;
              const contextIsCurrent = Boolean(context && contextIdentity(context) === state.identity);
              return (
                <div className="mt-5 grid gap-4">
                  {!contextIsCurrent ? <p role="alert" className="text-sm text-amber-900">The table changed while this panel was open. Cancel and choose a repeated field again.</p> : null}
                  <label className="grid gap-1 text-sm font-medium text-slate-800">
                    Repeated source field
                    <select
                      data-testid="construction-source-expand-choice"
                      aria-label="Repeated source field"
                      value={state.selection?.rowChoiceId ?? ''}
                      disabled={disabled || state.proposalState === 'applying'}
                      onChange={(event) => chooseScope(event.currentTarget.value)}
                      className="rounded-md border border-slate-300 bg-white px-3 py-2"
                    >
                      <option value="">Choose a repeated source field</option>
                      {state.choices.map((choice) => <option key={choice.choiceId} value={choice.choiceId}>{sourceChoiceLabel(choice)}</option>)}
                    </select>
                  </label>
                  <label className="grid gap-1 text-sm font-medium text-slate-800">
                    When a record has no values
                    <select
                      data-testid="construction-source-expand-empty-policy"
                      aria-label="When a record has no values"
                      value={state.selection?.emptyCollectionPolicy ?? ''}
                      disabled={disabled || !selectedChoice || state.proposalState === 'applying'}
                      onChange={(event) => choosePolicy(event.currentTarget.value as EmptyCollectionPolicy)}
                      className="rounded-md border border-slate-300 bg-white px-3 py-2"
                    >
                      <option value="" disabled>Choose how empty collections are handled</option>
                      {selectedPolicies.map((policy) => <option key={policy} value={policy}>{policyLabel(policy)}</option>)}
                    </select>
                  </label>
                  {state.proposalState === 'proposing' ? <p role="status">Previewing source row changes…</p> : null}
                  {state.proposalState === 'error' ? <p role="alert" className="text-sm text-red-800">{state.message}</p> : null}
                  {state.proposalState === 'applying' ? <p role="status">Applying source expansion…</p> : null}
                  {comparison?.status === 'AVAILABLE' ? (
                    <section aria-label="Source expansion preview" className="rounded-lg border border-slate-200 bg-slate-50 p-3">
                      <h4 className="font-semibold text-slate-900">Effect on rows</h4>
                      <p className="mt-1 text-sm text-slate-700">
                        {comparison.base.rowCount} rows → {comparison.candidate.rowCount} rows
                        {comparison.base.sampled || comparison.candidate.sampled ? ' · sampled' : ''}
                      </p>
                      {comparison.affectedColumns.length > 0 ? <p className="mt-2 text-xs text-slate-600">Affected columns: {comparison.affectedColumns.join(', ')}</p> : null}
                      {comparison.examples.length > 0 ? (
                        <ul aria-label="Membership changes" className="mt-2 space-y-1 text-xs text-slate-600">
                          {comparison.examples.map((example) => <li key={example.rowIdentity}>{example.basePresent === example.candidatePresent ? 'Unchanged' : example.candidatePresent ? 'Added' : 'Removed'} · {example.rowIdentity}</li>)}
                        </ul>
                      ) : null}
                      {comparison.notices.map((notice) => <p key={notice} className="mt-2 text-xs text-slate-600">{notice}</p>)}
                    </section>
                  ) : null}
                  <div className="flex justify-end gap-2">
                    <button type="button" className="rounded-md border border-slate-300 px-3 py-2 disabled:opacity-50" disabled={applying} onClick={close}>Cancel</button>
                    <button
                      type="button"
                      data-testid="construction-source-expand-apply"
                      className="rounded-md bg-blue-700 px-3 py-2 font-semibold text-white disabled:opacity-50"
                      disabled={disabled || !contextIsCurrent || state.proposalState !== 'ready' || !state.proposal?.proposalId || !context || !isCurrentProposal(state.proposal, context)}
                      onClick={() => void apply()}
                    >
                      Apply source expansion
                    </button>
                  </div>
                </div>
              );
            })() : null}
          </section>
        </div>
      ) : null}
    </>
  );
};
