import React, { useEffect, useRef, useState } from 'react';
import type { LoomClient } from '../../../api';
import type {
  TableShapeProposal as TableShapeProposalResult,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { TableShapeComparison } from './TableShapeComparison';
import { TableShapeEditor } from './TableShapeEditor';
import {
  contextKeyFor,
  editorChoicesFromCapabilities,
  editorIntentFromWire,
  pivotOutputLabelMap,
  pivotPairMatches,
  proposeTableShapeIntent,
  proposalMatches,
  savedShapeExists,
  scopeFromPanelProps,
  scopeRequest,
  type EditorScope,
  type EditorSession,
} from './tableShapeController';
import type { PivotCategoryPair, TableShapeProposalIntent } from './tableShapeModel';

type ControllerState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'loading' }
  | { readonly kind: 'error'; readonly message: string }
  | { readonly kind: 'editing'; readonly session: EditorSession; readonly recoverableError?: string }
  | { readonly kind: 'resolving'; readonly session: EditorSession; readonly intent: TableShapeProposalIntent }
  | { readonly kind: 'proposing'; readonly session: EditorSession; readonly intent: TableShapeProposalIntent }
  | { readonly kind: 'review'; readonly session: EditorSession; readonly intent: TableShapeProposalIntent; readonly proposal: TableShapeProposalResult }
  | {
      readonly kind: 'stale';
      readonly session: EditorSession;
      readonly message: string;
      readonly intent?: TableShapeProposalIntent;
      readonly proposal?: TableShapeProposalResult;
    }
  | { readonly kind: 'applying'; readonly session: EditorSession; readonly intent: TableShapeProposalIntent; readonly proposal: TableShapeProposalResult };

export interface TableShapeSettingsPanelProps {
  readonly client: Pick<LoomClient,
    'getTableShapeCapabilities' | 'discoverTableShapeCategories' | 'resolveTableShape' | 'proposeTableShape'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly onApply: (proposalId: string) => Promise<boolean>;
}

const currentSession = (state: ControllerState): EditorSession | undefined => {
  switch (state.kind) {
    case 'editing':
    case 'resolving':
    case 'proposing':
    case 'review':
    case 'stale':
    case 'applying':
      return state.session;
    default:
      return undefined;
  }
};

const currentIntent = (state: ControllerState): TableShapeProposalIntent | undefined => {
  switch (state.kind) {
    case 'resolving':
    case 'proposing':
    case 'review':
    case 'applying':
      return state.intent;
    case 'stale':
      return state.intent;
    default:
      return undefined;
  }
};

const currentProposal = (state: ControllerState): TableShapeProposalResult | undefined => {
  switch (state.kind) {
    case 'review':
    case 'applying':
      return state.proposal;
    case 'stale':
      return state.proposal;
    default:
      return undefined;
  }
};

const requestFailureMessage = (error: unknown, fallback: string): string =>
  typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string'
    ? error.message
    : fallback;

export const TableShapeSettingsPanel = (props: TableShapeSettingsPanelProps) => {
  const [state, setState] = useState<ControllerState>({ kind: 'closed' });
  const stateRef = useRef(state);
  const epoch = useRef(0);
  const nextSessionId = useRef(0);
  const scope = scopeFromPanelProps(props);
  const contextKey = contextKeyFor(scope);
  const contextKeyRef = useRef(contextKey);
  stateRef.current = state;
  contextKeyRef.current = contextKey;

  useEffect(() => {
    const session = currentSession(stateRef.current);
    if (session && session.contextKey !== contextKey) {
      epoch.current += 1;
      const current = stateRef.current;
      setState({
        kind: 'stale',
        session,
        message: 'The table, snapshot, or draft changed. Reload settings before making another proposal.',
        ...(currentIntent(current) ? { intent: currentIntent(current) } : {}),
        ...(currentProposal(current) ? { proposal: currentProposal(current) } : {}),
      });
    }
  }, [contextKey]);

  const isOperationCurrent = (operation: number, session: EditorSession): boolean =>
    epoch.current === operation && session.contextKey === contextKeyRef.current;

  const updateEditing = (
    sessionId: number,
    update: (session: EditorSession) => EditorSession,
    recoverableError?: string,
  ) => {
    setState((current) => current.kind === 'editing' && current.session.id === sessionId
      ? { kind: 'editing', session: update(current.session), ...(recoverableError ? { recoverableError } : {}) }
      : current);
  };

  const loadCatalog = async () => {
    const requestScope = scopeFromPanelProps(props);
    const requestContextKey = contextKeyFor(requestScope);
    const operation = epoch.current + 1;
    epoch.current = operation;
    setState({ kind: 'loading' });
    try {
      const capabilities = await props.client.getTableShapeCapabilities(scopeRequest(requestScope));
      if (epoch.current !== operation || contextKeyRef.current !== requestContextKey) return;
      if (capabilities.outputId !== requestScope.outputId) {
        throw new Error('Loom returned table-shape choices for a different table. Reload the Builder and try again.');
      }
      const savedIntent = editorIntentFromWire(capabilities.savedProposalIntent);
      let choices = editorChoicesFromCapabilities(capabilities);
      let savedDiscoveryError: string | undefined;
      if (savedIntent.kind === 'GROUPED_PIVOT') {
        const pair = {
          categoryColumn: savedIntent.pivot.categoryColumn,
          valueColumn: savedIntent.pivot.valueColumn,
        };
        try {
          const discovery = await props.client.discoverTableShapeCategories({
            ...scopeRequest(requestScope),
            catalogId: capabilities.catalogId,
            categoryColumnChoiceId: pair.categoryColumn.choiceId,
            valueColumnChoiceId: pair.valueColumn.choiceId,
          });
          if (epoch.current !== operation || contextKeyRef.current !== requestContextKey) return;
          if (discovery.catalogId !== capabilities.catalogId || !pivotPairMatches(discovery, pair)) {
            throw new Error('Loom returned categories for a different pivot. Reload the table shape settings.');
          }
          choices = {
            ...choices,
            pivotCategoryDiscovery: {
              kind: 'complete',
              discoveryIdentity: discovery.discoveryIdentity,
              pair: discovery.pair,
              categories: discovery.categories,
            },
          };
        } catch (error) {
          if (epoch.current !== operation || contextKeyRef.current !== requestContextKey) return;
          savedDiscoveryError = requestFailureMessage(error, 'Loom could not reload the saved pivot categories.');
        }
      }
      const session: EditorSession = {
        id: nextSessionId.current++,
        contextKey: requestContextKey,
        scope: requestScope,
        catalogId: capabilities.catalogId,
        choices,
        savedProposalIntent: savedIntent,
        savedShapeExists: savedShapeExists(savedIntent),
        ...(capabilities.savedProposalAvailability.kind === 'unsupported'
          ? { savedSupportNotice: capabilities.savedProposalAvailability.reason }
          : {}),
      };
      setState({
        kind: 'editing',
        session,
        ...(savedDiscoveryError ? { recoverableError: savedDiscoveryError } : {}),
      });
    } catch (error) {
      if (epoch.current === operation && contextKeyRef.current === requestContextKey) {
        setState({ kind: 'error', message: requestFailureMessage(error, 'Loom could not load table-shape choices.') });
      }
    }
  };

  const requestCategoryDiscovery = async (pair: PivotCategoryPair) => {
    const current = stateRef.current;
    if (current.kind !== 'editing') return;
    const session = current.session;
    const operation = epoch.current;
    try {
      const discovery = await props.client.discoverTableShapeCategories({
        ...scopeRequest(session.scope),
        catalogId: session.catalogId,
        categoryColumnChoiceId: pair.categoryColumn.choiceId,
        valueColumnChoiceId: pair.valueColumn.choiceId,
      });
      if (!isOperationCurrent(operation, session)) return;
      if (discovery.catalogId !== session.catalogId || !pivotPairMatches(discovery, pair)) {
        throw new Error('Loom returned categories for a different pivot selection.');
      }
      updateEditing(session.id, (latest) => ({
        ...latest,
        choices: {
          ...latest.choices,
          pivotCategoryDiscovery: {
            kind: 'complete',
            discoveryIdentity: discovery.discoveryIdentity,
            pair: discovery.pair,
            categories: discovery.categories,
          },
        },
      }));
    } catch (error) {
      if (isOperationCurrent(operation, session)) {
          updateEditing(session.id, (latest) => ({
            ...latest,
            choices: {
              ...latest.choices,
              pivotCategoryDiscovery: {
                kind: 'unavailable',
                pair,
                reason: requestFailureMessage(error, 'Loom could not discover categories.'),
              },
            },
          }),
          requestFailureMessage(error, 'Loom could not discover categories. Your table-shape edits are still here.'));
      }
    }
  };

  const resolveAndPropose = async (intent: TableShapeProposalIntent) => {
    const current = stateRef.current;
    if (current.kind !== 'editing') return;
    const session = current.session;
    if (!isOperationCurrent(epoch.current, session)) {
      setState({ kind: 'stale', session, intent, message: 'The table or draft changed. Reload settings before making a proposal.' });
      return;
    }
    const removesShape = intent.kind === 'NONE' && intent.derivedColumns.length === 0;
    if (removesShape && !session.savedShapeExists) {
      updateEditing(session.id, (latest) => latest, 'There is no saved table shape to remove.');
      return;
    }

    const operation = epoch.current;
    setState({ kind: 'resolving', session, intent });
    try {
      const mode = removesShape ? 'REMOVE' : session.savedShapeExists ? 'REPLACE' : 'ADD';
      const proposal = await proposeTableShapeIntent({
        client: props.client,
        session,
        intent,
        isCurrent: () => isOperationCurrent(operation, session),
        onProposing: () => setState({ kind: 'proposing', session, intent }),
      });
      if (!proposal) return;
      if (!isOperationCurrent(operation, session)) return;
      if (!proposalMatches(proposal, session) || proposal.mode !== mode) {
        setState({ kind: 'stale', session, intent, proposal, message: 'Loom returned a proposal for an older table or draft. Reload settings.' });
        return;
      }
      setState({ kind: 'review', session, intent, proposal });
    } catch (error) {
      if (isOperationCurrent(operation, session)) {
        setState({
          kind: 'editing',
          session,
          recoverableError: requestFailureMessage(error, 'Loom could not resolve or preview this table shape. Your edits are still here.'),
        });
      }
    }
  };

  const confirmApply = async () => {
    const current = stateRef.current;
    if (current.kind !== 'review') return;
    const { session, intent, proposal } = current;
    if (!isOperationCurrent(epoch.current, session) || !proposalMatches(proposal, session)) {
      setState({ kind: 'stale', session, intent, proposal, message: 'This preview is stale. Reload settings before applying it.' });
      return;
    }
    if (!proposal.proposalId || proposal.comparison.status !== 'AVAILABLE') return;
    const operation = epoch.current;
    setState({ kind: 'applying', session, intent, proposal });
    try {
      const applied = await props.onApply(proposal.proposalId);
      if (!isOperationCurrent(operation, session)) return;
      if (applied) {
        setState({ kind: 'closed' });
        epoch.current += 1;
      } else {
        setState({ kind: 'stale', session, intent, proposal, message: 'The Builder draft changed before Loom applied this preview. Reload settings.' });
      }
    } catch (error) {
      if (isOperationCurrent(operation, session)) {
        setState({ kind: 'stale', session, intent, proposal, message: requestFailureMessage(error, 'Loom could not apply the proposal. Reload settings and review it again.') });
      }
    }
  };

  const cancel = () => {
    epoch.current += 1;
    setState({ kind: 'closed' });
  };

  const session = currentSession(state);
  const intent = currentIntent(state);
  const proposal = currentProposal(state);
  const editorIsBusy = state.kind === 'loading' || state.kind === 'resolving' || state.kind === 'proposing' || state.kind === 'review' || state.kind === 'applying' || state.kind === 'stale';
  const editorDisabled = props.disabled || editorIsBusy || Boolean(session && session.contextKey !== contextKey);

  return (
    <section aria-label="Table shape settings" data-testid="ui04-table-shape-settings" className="rounded-xl border border-indigo-200 bg-indigo-50/40 p-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold text-slate-900">Table shape</h2>
          <p className="mt-1 text-xs text-slate-600">Pivot, unpivot, or add calculated columns. Loom previews the change before it reaches the saved draft.</p>
        </div>
        {state.kind === 'closed' ? (
          <button
            type="button"
            className="rounded bg-indigo-700 px-3 py-2 text-sm font-semibold text-white hover:bg-indigo-800 disabled:opacity-50"
            data-testid="ui04-open-table-shape-settings"
            disabled={props.disabled}
            onClick={() => void loadCatalog()}
          >
            Open settings
          </button>
        ) : null}
      </div>

      {state.kind !== 'closed' ? (
        <div role="dialog" aria-modal="false" aria-labelledby="ui04-table-shape-dialog-title" data-testid="ui04-table-shape-dialog" className="mt-3 grid gap-3 rounded-lg border border-indigo-200 bg-white p-3">
          <h3 id="ui04-table-shape-dialog-title" className="sr-only">Table shape settings</h3>
          {state.kind === 'loading' ? <p role="status">Loading table-shape choices…</p> : null}
          {state.kind === 'error' ? (
            <div className="grid gap-2">
              <p role="alert" data-testid="ui04-table-shape-error">{state.message}</p>
              <div className="flex gap-2">
                <button type="button" data-testid="ui04-reload-table-shape" onClick={() => void loadCatalog()}>Reload settings</button>
                <button type="button" data-testid="ui04-close-table-shape" onClick={cancel}>Close</button>
              </div>
            </div>
          ) : null}
          {session ? (
            <>
              {session.savedSupportNotice ? (
                <p role="status" data-testid="ui04-saved-shape-support">The saved table shape needs review. {session.savedSupportNotice}</p>
              ) : null}
              {state.kind === 'stale' ? (
                <div role="alert" data-testid="ui04-stale-table-shape" className="flex flex-wrap items-center justify-between gap-2 rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
                  <span>{state.message}</span>
                  <button type="button" data-testid="ui04-reload-table-shape" onClick={() => void loadCatalog()}>Reload settings</button>
                </div>
              ) : null}
              <TableShapeEditor
                key={session.id}
                savedProposalKey={`${session.scope.snapshotToken}:${session.scope.expectedDraftVersion}:${session.scope.expectedDraftDigest}:${session.catalogId}`}
                savedProposalIntent={session.savedProposalIntent}
                choices={session.choices}
                recoverableError={state.kind === 'editing' ? state.recoverableError : undefined}
                disabled={editorDisabled}
                onApply={(proposalIntent) => void resolveAndPropose(proposalIntent)}
                onRequestCategoryDiscovery={(pair) => void requestCategoryDiscovery(pair)}
                onCancel={cancel}
              />
              {state.kind === 'resolving' || state.kind === 'proposing' || state.kind === 'applying' ? (
                <p role="status" data-testid="ui04-table-shape-progress">
                  {state.kind === 'resolving' ? 'Checking the selected choices…' : state.kind === 'proposing' ? 'Building a before-and-after preview…' : 'Applying the reviewed table shape…'}
                </p>
              ) : null}
              {proposal ? (
                <>
                  <TableShapeComparison
                    comparison={proposal.comparison}
                    columnLabels={intent ? pivotOutputLabelMap(intent, props.table) : {}}
                  />
                  <div className="flex flex-wrap justify-end gap-2">
                    <button type="button" data-testid="ui04-cancel-table-shape-proposal" onClick={cancel}>Cancel</button>
                    {state.kind === 'review' ? (
                      <button
                        type="button"
                        className="rounded bg-emerald-700 px-3 py-2 text-sm font-semibold text-white hover:bg-emerald-800 disabled:opacity-50"
                        data-testid="ui04-confirm-table-shape"
                        disabled={props.disabled || proposal.comparison.status !== 'AVAILABLE' || !proposal.proposalId}
                        onClick={() => void confirmApply()}
                      >
                        Confirm and apply
                      </button>
                    ) : null}
                  </div>
                </>
              ) : null}
            </>
          ) : null}
        </div>
      ) : null}
    </section>
  );
};
