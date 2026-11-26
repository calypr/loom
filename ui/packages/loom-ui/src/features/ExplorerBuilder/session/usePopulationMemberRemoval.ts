import { sameCatalogSnapshot, sameDraftVersion, sameDraftDigest } from '../../../contracts/identity';
import { useState } from 'react';
import { canonicalProject, type LoomClient, type PopulationMemberRemovalProposalResponse } from '../../../client/api';
import { useQuery } from '../../../react';
import type { ResourceRef, SelectionPage, SelectionRevision } from '../../../contracts/selection';

export type PopulationMemberTransport = Pick<LoomClient, 'getSelection' | 'proposePopulationMemberRemoval'>;

const PAGE_SIZE = 100;
const PREVIEW_LIMIT = 25;

type Member = SelectionPage['members'][number];
type MemberWindow = {
  readonly scopeKey: string;
  readonly client: PopulationMemberTransport;
  readonly cursor?: string;
  readonly loaded: ReadonlyArray<Member>;
  readonly seenCursors: ReadonlyArray<string>;
};
type RemovalIntent = {
  readonly scopeKey: string;
  readonly client: PopulationMemberTransport;
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
    selection.project !== canonicalProject(scope.project) || revision.id !== selection.id ||
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
    !sameCatalogSnapshot(response.snapshotToken, args.snapshotToken) || !sameDraftVersion(response.draftVersion, args.draftVersion) ||
    !sameDraftDigest(response.draftDigest, args.draftDigest) || response.outputId !== args.outputId ||
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

export const usePopulationMemberRemoval = ({
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
  readonly client: PopulationMemberTransport;
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
  const cancelRemoval = () => {
    setIntent(undefined);
    setApplyState(undefined);
  };

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

  return {
    memberQuery, displayedMembers, proposalQuery, currentIntent, activeApplyState, proposal,
    loadNextPage, startRemoval, applyRemoval, cancelRemoval,
  };
};
