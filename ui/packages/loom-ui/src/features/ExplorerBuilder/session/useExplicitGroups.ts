import { useState } from 'react';
import type { LoomClient } from '../../../client/api';
import { RequestOwner } from '../../../data/requests';
import { useQuery } from '../../../react';
import type { ExplicitGroupRevisionSummary } from '../../../contracts/types';
import type { SelectionPage, SelectionRevision } from '../../../contracts/selection';

export type ExplicitGroupTransport = Pick<LoomClient, 'getSelection' | 'createExplicitGroupRevision'>;

type SelectedMember = { readonly memberId: string; readonly label: string };
type GroupDraft = { readonly id: string; readonly label: string; readonly ordinal: number; readonly memberIds: ReadonlyArray<string> };
type EditorState =
  | { readonly kind: 'loading' }
  | { readonly kind: 'editing'; readonly members: ReadonlyArray<SelectedMember>; readonly groups: ReadonlyArray<GroupDraft>; readonly idempotencyKey: string; readonly nextCursor?: string; readonly cursors: ReadonlyArray<string>; readonly loadingMore: boolean; readonly loadError?: string; readonly error?: string }
  | { readonly kind: 'saving'; readonly members: ReadonlyArray<SelectedMember>; readonly groups: ReadonlyArray<GroupDraft>; readonly idempotencyKey: string };

const opaqueID = (prefix: string): string => {
  const random = globalThis.crypto?.randomUUID?.();
  return `${prefix}-${random ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`}`;
};

const initialGroups = (): ReadonlyArray<GroupDraft> => [
  { id: opaqueID('group'), label: 'Group A', ordinal: 0, memberIds: [] },
  { id: opaqueID('group'), label: 'Group B', ordinal: 1, memberIds: [] },
];

const readMembersPage = async (
  client: Pick<LoomClient, 'getSelection'>,
  args: { readonly project: string; readonly explorerId: string; readonly authResourcePath?: string; readonly selection: SelectionRevision },
  signal: AbortSignal,
  cursor: string | undefined,
  startIndex: number,
): Promise<{ readonly members: ReadonlyArray<SelectedMember>; readonly nextCursor?: string }> => {
  const page: SelectionPage = await client.getSelection({
    project: args.project,
    explorerId: args.explorerId,
    authResourcePath: args.authResourcePath,
    selectionRevision: args.selection.id,
    cursor,
    limit: 100,
  }, signal);
  if (page.revision.id !== args.selection.id || page.revision.project !== args.selection.project ||
      page.revision.generation !== args.selection.generation || page.revision.resourceType !== args.selection.resourceType ||
      page.revision.scopeDigest !== args.selection.scopeDigest || page.revision.membershipDigest !== args.selection.membershipDigest ||
      page.revision.memberCount !== args.selection.memberCount || page.revision.complete !== true) {
    throw new Error('The source selection changed while its members were being loaded.');
  }
  const seenKeys = new Set<string>();
  const members = page.members.map((member, index) => {
    const memberId = member.memberKey;
    if (!memberId || seenKeys.has(memberId) || member.ref.project !== args.selection.project ||
        member.ref.generation !== args.selection.generation || member.ref.resourceType !== args.selection.resourceType) {
      throw new Error('The source selection did not return unique opaque member identities from the pinned selection.');
    }
    seenKeys.add(memberId);
    return { memberId, label: `Record ${startIndex + index + 1} · ${member.ref.id || `item ${index + 1}`}` };
  });
  return { members, nextCursor: page.nextCursor };
};

export const useExplicitGroups = ({
  client,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  selection,
  onCreated,
}: {
  readonly client: ExplicitGroupTransport;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly selection: SelectionRevision;
  readonly onCreated: (revision: ExplicitGroupRevisionSummary) => Promise<void>;
}) => {
  const [requests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity('explicit-groups');
    return owner;
  });
  const [state, setState] = useState<EditorState>({ kind: 'loading' });
  const initialPageKey = JSON.stringify([
    selection.id, selection.project, selection.generation, selection.resourceType,
    selection.scopeDigest, selection.membershipDigest, selection.memberCount,
  ]);
  const initialQuery = useQuery(
    (signal) => readMembersPage(
      client,
      { project, explorerId, authResourcePath, selection },
      signal,
      undefined,
      0,
    ),
    [authResourcePath, client, explorerId, project, selection],
  );
  const [initializedPageKey, setInitializedPageKey] = useState<string | undefined>(undefined);
  if (initializedPageKey !== initialPageKey) {
    setInitializedPageKey(initialPageKey);
    setState({ kind: 'loading' });
  } else if (state.kind === 'loading') {
    if (initialQuery.error) {
      setState({
        kind: 'editing', members: [], groups: initialGroups(),
        idempotencyKey: opaqueID('explicit-groups'), cursors: [], loadingMore: false,
        error: initialQuery.error instanceof Error
          ? initialQuery.error.message
          : 'Loom could not load the source selection.',
      });
    } else if (initialQuery.data) {
      setState({
        kind: 'editing', members: initialQuery.data.members, groups: initialGroups(),
        idempotencyKey: opaqueID('explicit-groups'), nextCursor: initialQuery.data.nextCursor,
        cursors: [], loadingMore: false,
      });
    }
  }

  const updateGroup = (groupID: string, update: (group: GroupDraft) => GroupDraft) => {
    if (state.kind !== 'editing') return;
    setState({ ...state, groups: state.groups.map((group) => group.id === groupID ? update(group) : group), idempotencyKey: opaqueID('explicit-groups'), error: undefined });
  };

  const loadMore = async () => {
    if (state.kind !== 'editing' || !state.nextCursor || state.loadingMore) return;
    const cursor = state.nextCursor;
    if (state.cursors.includes(cursor)) {
      setState({ ...state, loadError: 'The source selection returned a repeated page cursor.' });
      return;
    }
    const request = requests.start('member-page', 'explicit-groups');
    if (!request) return;
    setState({ ...state, loadingMore: true, loadError: undefined });
    try {
      const page = await readMembersPage(client, { project, explorerId, authResourcePath, selection }, request.signal, cursor, state.members.length);
      setState((current) => {
        if (current.kind !== 'editing') return current;
        const existingKeys = new Set(current.members.map((member) => member.memberId));
        if (page.members.some((member) => existingKeys.has(member.memberId))) {
          return { ...current, loadingMore: false, loadError: 'The source selection returned a repeated member across pages.' };
        }
        return {
          ...current,
          members: [...current.members, ...page.members],
          nextCursor: page.nextCursor,
          cursors: [...current.cursors, cursor],
          loadingMore: false,
          loadError: undefined,
        };
      });
    } catch (error) {
      setState((current) => current.kind === 'editing'
        ? { ...current, loadingMore: false, loadError: error instanceof Error ? error.message : 'Loom could not load more selected records.' }
        : current);
    } finally {
      request.finish();
    }
  };

  const create = async () => {
    if (state.kind !== 'editing' || state.error) return;
    const groups = state.groups.map((group) => ({
      id: group.id,
      label: group.label.trim(),
      ordinal: group.ordinal,
      memberIds: [...group.memberIds],
    }));
    if (groups.some((group) => group.label.length === 0)) {
      setState({ ...state, error: 'Give every group a name before creating this revision.' });
      return;
    }
    setState({ kind: 'saving', members: state.members, groups: state.groups, idempotencyKey: state.idempotencyKey });
    try {
      const revision = await client.createExplicitGroupRevision({
        project, explorerId, authResourcePath, snapshotToken,
        selectionRevision: selection.id,
        idempotencyKey: state.idempotencyKey,
        groups,
      });
      if (revision.sourceSelectionRevisionId !== selection.id || revision.groupCount !== groups.length ||
          revision.groups.length !== groups.length || revision.groups.some((group, index) =>
            group.id !== groups[index]?.id || group.label !== groups[index]?.label ||
            group.memberCount !== groups[index]?.memberIds.length)) {
        throw new Error('Loom returned a group revision that does not match the exact request.');
      }
      await onCreated(revision);
    } catch (error) {
      setState({ kind: 'editing', members: state.members, groups: state.groups, idempotencyKey: state.idempotencyKey, cursors: state.kind === 'editing' ? state.cursors : [], nextCursor: state.kind === 'editing' ? state.nextCursor : undefined, loadingMore: false, loadError: state.kind === 'editing' ? state.loadError : undefined, error: error instanceof Error ? error.message : 'Loom could not create this explicit group revision.' });
    }
  };

  return { state, setState, updateGroup, loadMore, create, opaqueID };
};
