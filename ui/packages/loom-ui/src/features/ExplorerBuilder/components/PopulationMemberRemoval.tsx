import React, { useState } from 'react';
import type { LoomClient } from '../../../api';
import { useQuery } from '../../../react';
import type { ResourceRef, SelectionPage, SelectionRevision } from '../../../selection';
import type { PopulationMemberRemovalProposalResponse } from '../../../api';

const PAGE_SIZE = 100;
const PREVIEW_LIMIT = 25;

type Member = SelectionPage['members'][number];
type MemberWindow = {
  readonly scopeKey: string;
  readonly client: LoomClient;
  readonly cursor?: string;
  readonly loaded: ReadonlyArray<Member>;
  readonly seenCursors: ReadonlyArray<string>;
};
type RemovalIntent = {
  readonly scopeKey: string;
  readonly client: LoomClient;
  readonly member: Member;
  readonly requestId: string;
};
type ApplyState = {
  readonly intentKey: string;
  readonly pending: boolean;
  readonly error?: string;
};

const refKey = (ref: ResourceRef): string => JSON.stringify([
  ref.project, ref.generation, ref.resourceType, ref.id,
]);

const sameRef = (left: ResourceRef, right: ResourceRef): boolean =>
  left.project === right.project && left.generation === right.generation &&
  left.resourceType === right.resourceType && left.id === right.id;

const validateSelectionPage = (
  page: SelectionPage,
  selection: SelectionRevision,
  scope: { readonly project: string; readonly explorerId: string },
  cursor: string | undefined,
  loaded: ReadonlyArray<Member>,
  seenCursors: ReadonlyArray<string>,
): void => {
  const revision = page.revision;
  if (
    selection.project !== scope.project || revision.id !== selection.id ||
    revision.project !== selection.project || revision.generation !== selection.generation ||
    revision.resourceType !== selection.resourceType || revision.scopeDigest !== selection.scopeDigest ||
    revision.membershipDigest !== selection.membershipDigest || revision.memberCount !== selection.memberCount ||
    revision.complete !== true
  ) {
    throw new Error('The attached collection changed scope while its members were being loaded.');
  }
  if (page.members.length > PAGE_SIZE) {
    throw new Error('The attached collection returned an invalid member page.');
  }
  if (page.nextCursor && (page.nextCursor === cursor || seenCursors.includes(page.nextCursor))) {
    throw new Error('The attached collection returned a repeated page cursor.');
  }
  const memberKeys = new Set(loaded.map((member) => member.memberKey));
  const refs = new Set(loaded.map((member) => refKey(member.ref)));
  for (const member of page.members) {
    if (
      !member.memberKey || memberKeys.has(member.memberKey) || refs.has(refKey(member.ref)) ||
      member.ref.project !== selection.project || member.ref.generation !== selection.generation ||
      member.ref.resourceType !== selection.resourceType
    ) {
      throw new Error('The attached collection returned a duplicate or out-of-scope member.');
    }
    memberKeys.add(member.memberKey);
    refs.add(refKey(member.ref));
  }
  const total = loaded.length + page.members.length;
  if (total > selection.memberCount || (!page.nextCursor && total !== selection.memberCount)) {
    throw new Error('The attached collection page count does not match its signed membership header.');
  }
}

const validateProposal = (
  response: PopulationMemberRemovalProposalResponse,
  args: {
    readonly snapshotToken: string;
    readonly draftVersion: number;
    readonly draftDigest: string;
    readonly outputId: string;
    readonly selection: SelectionRevision;
    readonly member: ResourceRef;
  },
): void => {
  const base = response.baseSelection;
  const candidate = response.candidateSelection;
  const selectionMatches = (value: SelectionRevision) =>
    value.project === args.selection.project && value.generation === args.selection.generation &&
    value.resourceType === args.selection.resourceType && value.scopeDigest === args.selection.scopeDigest;
  if (
    response.snapshotToken !== args.snapshotToken || response.draftVersion !== args.draftVersion ||
    response.draftDigest !== args.draftDigest || response.outputId !== args.outputId ||
    base.complete !== true || candidate.complete !== true ||
    base.id !== args.selection.id || base.membershipDigest !== args.selection.membershipDigest ||
    base.memberCount !== args.selection.memberCount || !selectionMatches(base) ||
    candidate.id === base.id || candidate.memberCount !== base.memberCount - 1 ||
    candidate.membershipDigest === base.membershipDigest || !selectionMatches(candidate) ||
    !sameRef(response.removedMember, args.member) ||
    response.preview.receiptId !== response.proposalId || response.preview.outputId !== args.outputId
  ) {
    throw new Error('The member-removal proposal or preview does not match the current table and attached collection.');
  }
}

const displayCell = (value: unknown): string => {
  if (value === null) return 'NULL';
  if (value === undefined) return '—';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

export const PopulationMemberRemoval = ({
  client,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  draftVersion,
  draftDigest,
  outputId,
  selection,
  disabled,
  onApply,
}: {
  readonly client: LoomClient;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly selection: SelectionRevision;
  readonly disabled: boolean;
  readonly onApply: (proposalId: string) => Promise<boolean>;
}) => {
  const scopeKey = JSON.stringify([
    project, explorerId, authResourcePath?.trim() ?? '', snapshotToken, draftVersion,
    draftDigest, outputId, selection.id, selection.project, selection.generation,
    selection.resourceType, selection.scopeDigest, selection.membershipDigest, selection.memberCount,
  ]);
  const [windowState, setWindowState] = useState<MemberWindow>();
  const [intent, setIntent] = useState<RemovalIntent>();
  const [applyState, setApplyState] = useState<ApplyState>();
  const currentWindow = windowState?.scopeKey === scopeKey && windowState.client === client
    ? windowState
    : { scopeKey, client, loaded: [], seenCursors: [] };
  const memberQuery = useQuery(async (signal) => {
    if (signal.aborted) return undefined;
    const page = await client.getSelection({
      project,
      explorerId,
      authResourcePath,
      selectionRevision: selection.id,
      cursor: currentWindow.cursor,
      limit: PAGE_SIZE,
    });
    if (signal.aborted) return undefined;
    validateSelectionPage(page, selection, { project, explorerId }, currentWindow.cursor, currentWindow.loaded, currentWindow.seenCursors);
    return page;
  }, [client, scopeKey, currentWindow.cursor, JSON.stringify(currentWindow.loaded.map((member) => member.memberKey)), JSON.stringify(currentWindow.seenCursors)]);
  const displayedMembers = memberQuery.data
    ? [...currentWindow.loaded, ...memberQuery.data.members]
    : currentWindow.loaded;
  const intentKey = intent?.scopeKey === scopeKey && intent.client === client
    ? JSON.stringify([scopeKey, intent.member.memberKey, refKey(intent.member.ref), intent.requestId])
    : undefined;
  const currentIntent = intentKey ? intent : undefined;
  const proposalQuery = useQuery(async (signal) => {
    if (!currentIntent) return undefined;
    const response = await client.proposePopulationMemberRemoval({
      project,
      explorerId,
      authResourcePath,
      snapshotToken,
      expectedDraftVersion: draftVersion,
      expectedDraftDigest: draftDigest,
      outputId,
      baseSelectionRevisionId: selection.id,
      removedMember: currentIntent.member.ref,
      limit: PREVIEW_LIMIT,
      requestId: currentIntent.requestId,
    }, signal);
    validateProposal(response, { snapshotToken, draftVersion, draftDigest, outputId, selection, member: currentIntent.member.ref });
    return response;
  }, [client, intentKey], Boolean(currentIntent));
  const proposal = proposalQuery.data;
  const activeApplyState = intentKey && applyState?.intentKey === intentKey ? applyState : undefined;

  const loadNextPage = () => {
    if (!memberQuery.data?.nextCursor || memberQuery.isLoading) return;
    const nextCursor = memberQuery.data.nextCursor;
    if (currentWindow.seenCursors.includes(nextCursor)) return;
    setWindowState({
      scopeKey,
      client,
      cursor: nextCursor,
      loaded: [...currentWindow.loaded, ...memberQuery.data.members],
      seenCursors: currentWindow.cursor
        ? [...currentWindow.seenCursors, currentWindow.cursor]
        : currentWindow.seenCursors,
    });
  };

  const startRemoval = (member: Member) => {
    const random = globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    setApplyState(undefined);
    setIntent({ scopeKey, client, member, requestId: `population-member-removal-${random}` });
  };

  const applyRemoval = async () => {
    if (!proposal || !currentIntent || disabled || activeApplyState?.pending) return;
    const appliedIntent = currentIntent;
    const appliedIntentKey = intentKey!;
    setApplyState({ intentKey: appliedIntentKey, pending: true });
    try {
      const applied = await onApply(proposal.proposalId);
      if (!applied) throw new Error('Loom could not apply this member removal. The accepted collection is unchanged.');
      setIntent((current) => current?.scopeKey === appliedIntent.scopeKey &&
        current.client === appliedIntent.client && current.requestId === appliedIntent.requestId
        ? undefined
        : current);
      setApplyState((current) => current?.intentKey === appliedIntentKey ? undefined : current);
    } catch (error) {
      setApplyState((current) => current?.intentKey === appliedIntentKey ? {
        intentKey: appliedIntentKey,
        pending: false,
        error: error instanceof Error ? error.message : 'Loom could not apply this member removal.',
      } : current);
    }
  };

  return (
    <section className="mt-3 border-t border-slate-200 pt-3" aria-label="Attached collection members" data-testid="population-member-list">
      <h3 className="font-semibold">Selected collection members</h3>
      <p className="mt-1 text-xs text-slate-600">Removing a member previews the table before you decide whether to apply it.</p>
      {memberQuery.isLoading && displayedMembers.length === 0 ? <p role="status" className="mt-2 text-slate-600">Loading collection members…</p> : null}
      {memberQuery.error ? <p role="alert" className="mt-2 text-red-700">{memberQuery.error instanceof Error ? memberQuery.error.message : 'Loom could not load collection members.'}</p> : null}
      {displayedMembers.length > 0 ? (
        <ul className="mt-2 space-y-1">
          {displayedMembers.map((member) => (
            <li key={member.memberKey} className="flex flex-wrap items-center justify-between gap-2 rounded border border-slate-200 px-2 py-1">
              <span>{member.ref.resourceType}/{member.ref.id}</span>
              <button
                type="button"
                disabled={disabled || proposalQuery.isLoading || activeApplyState?.pending === true}
                onClick={() => startRemoval(member)}
                aria-label={`Review removal of ${member.ref.resourceType}/${member.ref.id}`}
                className="rounded border border-amber-400 bg-white px-2 py-1 text-xs font-semibold text-amber-900 disabled:opacity-40"
              >Review removal</button>
            </li>
          ))}
        </ul>
      ) : !memberQuery.isLoading && !memberQuery.error ? (
        <p className="mt-2 text-slate-600">This attached collection has no members to remove.</p>
      ) : null}
      {memberQuery.data?.nextCursor ? (
        <button type="button" disabled={memberQuery.isLoading} onClick={loadNextPage} className="mt-2 rounded border border-slate-300 bg-white px-3 py-2 text-xs font-semibold disabled:opacity-40">
          {memberQuery.isLoading ? 'Loading members…' : 'Load next member page'}
        </button>
      ) : null}
      {currentIntent ? (
        <div
          role="region"
          aria-label="Member removal preview"
          data-testid="population-member-removal-proposal"
          data-proposal-id={proposal?.proposalId}
          data-base-selection-revision-id={proposal?.baseSelection.id ?? selection.id}
          data-candidate-selection-revision-id={proposal?.candidateSelection.id}
          data-preview-status={proposal?.previewStatus ?? (proposalQuery.isLoading ? 'LOADING' : undefined)}
          className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3"
        >
          <h3 className="font-semibold">Preview collection member removal</h3>
          <p className="mt-1" data-testid="population-member-removal-target">Remove {currentIntent.member.ref.resourceType}/{currentIntent.member.ref.id}</p>
          {proposalQuery.isLoading ? <p role="status" className="mt-2">Building the automatic impact preview…</p> : null}
          {proposalQuery.error ? <p role="alert" className="mt-2 text-red-700">{proposalQuery.error instanceof Error ? proposalQuery.error.message : 'Loom could not preview this member removal.'}</p> : null}
          {proposal ? (
            <div className="mt-2" data-testid="population-member-candidate-preview">
              <p className="font-medium">Table after removal · {proposal.baseSelection.memberCount} → {proposal.candidateSelection.memberCount} selected members · {proposal.preview.rowCount} rows</p>
              {proposal.preview.rows === null ? <p className="mt-1">The preview did not return table rows.</p> : (
                <div className="mt-2 overflow-x-auto">
                  <table aria-label="Candidate table after member removal" className="min-w-full border-collapse text-left text-xs">
                    <thead><tr>{proposal.preview.columns.map((column) => <th key={column.column} className="border border-slate-300 px-2 py-1">{column.label}</th>)}</tr></thead>
                    <tbody>
                      {proposal.preview.rows.map((row, index) => (
                        <tr key={index} data-testid="population-member-candidate-preview-row">
                          {proposal.preview.columns.map((column) => <td key={column.column} className="border border-slate-300 px-2 py-1">{displayCell(row[column.column])}</td>)}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          ) : null}
          {activeApplyState?.error ? <p role="alert" className="mt-2 text-red-700">{activeApplyState.error}</p> : null}
          <div className="mt-3 flex flex-wrap gap-2">
            <button type="button" disabled={activeApplyState?.pending === true} onClick={() => { setIntent(undefined); setApplyState(undefined); }} className="rounded border border-slate-300 bg-white px-3 py-2 font-semibold disabled:opacity-40">
              Cancel member removal
            </button>
            <button type="button" disabled={disabled || !proposal || activeApplyState?.pending === true} onClick={() => void applyRemoval()} className="rounded bg-indigo-700 px-3 py-2 font-semibold text-white disabled:opacity-40">
              {activeApplyState?.pending ? 'Applying member removal…' : 'Apply member removal'}
            </button>
          </div>
        </div>
      ) : null}
    </section>
  );
};
