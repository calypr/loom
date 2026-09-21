import React, { useEffect, useState } from 'react';
import type { LoomClient } from '../../../api';
import type { ExplicitGroupRevisionSummary } from '../../../types';
import type { SelectionPage, SelectionRevision } from '../../../selection';

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

export const ExplicitGroupAuthoring = ({
  client,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  selection,
  onCancel,
  onCreated,
}: {
  readonly client: Pick<LoomClient, 'getSelection' | 'createExplicitGroupRevision'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly selection: SelectionRevision;
  readonly onCancel: () => void;
  readonly onCreated: (revision: ExplicitGroupRevisionSummary) => Promise<void>;
}) => {
  const [state, setState] = useState<EditorState>({ kind: 'loading' });

  useEffect(() => {
    const controller = new AbortController();
    void readMembersPage(client, { project, explorerId, authResourcePath, selection }, controller.signal, undefined, 0).then(
      (page) => setState({ kind: 'editing', members: page.members, groups: initialGroups(), idempotencyKey: opaqueID('explicit-groups'), nextCursor: page.nextCursor, cursors: [], loadingMore: false }),
      (error: unknown) => {
        if (!controller.signal.aborted) {
          setState({ kind: 'editing', members: [], groups: initialGroups(), idempotencyKey: opaqueID('explicit-groups'), cursors: [], loadingMore: false, error: error instanceof Error ? error.message : 'Loom could not load the source selection.' });
        }
      },
    );
    return () => controller.abort();
  }, [authResourcePath, client, explorerId, project, selection]);

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
    setState({ ...state, loadingMore: true, loadError: undefined });
    try {
      const controller = new AbortController();
      const page = await readMembersPage(client, { project, explorerId, authResourcePath, selection }, controller.signal, cursor, state.members.length);
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

  const saving = state.kind === 'saving';
  const members = state.kind === 'loading' ? [] : state.members;
  const groups = state.kind === 'loading' ? [] : state.groups;
  const error = state.kind === 'editing' ? state.error : undefined;

  return (
    <section aria-label="Create explicit groups" className="mt-4 rounded-lg border border-slate-200 bg-slate-50 p-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h4 className="font-semibold text-slate-900">Create groups from selected records</h4>
          <p className="mt-1 text-xs text-slate-600">Source selection: {selection.id} · {selection.memberCount} selected records</p>
          <p className="mt-1 text-xs text-slate-600">Assignments are independent: a record may belong to multiple groups, and a group may stay empty.</p>
        </div>
        <button type="button" className="rounded-md border border-slate-300 px-3 py-1.5" onClick={onCancel} disabled={saving}>Cancel group setup</button>
      </div>
      {state.kind === 'loading' ? <p className="mt-3" role="status">Loading selected records…</p> : null}
      {state.kind === 'editing' && !state.error ? (
        <>
          <div className="mt-3 space-y-3">
            {state.groups.map((group) => (
              <fieldset key={group.id} className="rounded-md border border-slate-300 bg-white p-3">
                <legend className="px-1 text-sm font-medium">Group {group.ordinal + 1}</legend>
                <label className="block text-xs font-medium text-slate-700">
                  Group name
                  <input
                    aria-label={`Group ${group.ordinal + 1} name`}
                    className="mt-1 block w-full rounded border border-slate-300 px-2 py-1.5 text-sm"
                    value={group.label}
                    onChange={(event) => updateGroup(group.id, (current) => ({ ...current, label: event.currentTarget.value }))}
                  />
                </label>
                {state.groups.length > 1 ? (
                  <button type="button" aria-label={`Remove ${group.label}`} className="mt-2 text-xs text-red-700 underline" onClick={() => setState({ ...state, groups: state.groups.filter((current) => current.id !== group.id), idempotencyKey: opaqueID('explicit-groups') })}>
                    Remove group
                  </button>
                ) : null}
              </fieldset>
            ))}
          </div>
          {state.groups.length < 1000 ? (
            <button type="button" className="mt-3 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-xs" onClick={() => setState({
              ...state,
              groups: [...state.groups, { id: opaqueID('group'), label: `Group ${String.fromCharCode(65 + state.groups.length)}`, ordinal: Math.max(...state.groups.map((group) => group.ordinal)) + 1, memberIds: [] }],
              idempotencyKey: opaqueID('explicit-groups'),
            })}>
              Add group
            </button>
          ) : null}
          <div className="mt-4 max-h-72 space-y-2 overflow-auto rounded-md border border-slate-200 bg-white p-3">
            <h5 className="text-sm font-semibold text-slate-900">Selected records</h5>
            <p className="text-xs text-slate-600">Loaded {state.members.length} of {selection.memberCount}. Records not loaded or not assigned remain unassigned.</p>
            {state.members.length === 0 ? <p className="text-xs text-slate-600">This selection contains no records.</p> : null}
            {state.members.map((member) => (
              <fieldset key={member.memberId} className="rounded border border-slate-200 p-2">
                <legend className="px-1 text-xs font-medium text-slate-800">{member.label}</legend>
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  {state.groups.map((group) => (
                    <label key={group.id} className="inline-flex items-center gap-1 text-xs text-slate-700">
                      <input
                        type="checkbox"
                        aria-label={`Assign ${member.label} to ${group.label || `Group ${group.ordinal + 1}`}`}
                        checked={group.memberIds.includes(member.memberId)}
                        onChange={(event) => updateGroup(group.id, (current) => ({
                          ...current,
                          memberIds: event.currentTarget.checked
                            ? [...current.memberIds, member.memberId]
                            : current.memberIds.filter((memberID) => memberID !== member.memberId),
                        }))}
                      />
                      {group.label || `Group ${group.ordinal + 1}`}
                    </label>
                  ))}
                </div>
              </fieldset>
            ))}
            {state.nextCursor ? (
              <button type="button" className="rounded-md border border-slate-300 px-3 py-1.5 text-xs disabled:opacity-50" disabled={state.loadingMore} onClick={() => void loadMore()}>
                {state.loadingMore ? 'Loading more records…' : 'Load more selected records'}
              </button>
            ) : null}
            {state.loadError ? <p role="alert" className="text-xs text-red-800">{state.loadError}</p> : null}
            {state.error ? <p role="alert" className="text-xs text-red-800">{state.error}</p> : null}
          </div>
          <section aria-label="Exact group memberships" className="mt-3 rounded-md border border-slate-200 bg-white p-3">
            <h5 className="text-sm font-semibold text-slate-900">Exact memberships</h5>
            <ul className="mt-2 space-y-2 text-xs text-slate-700">
              {state.groups.map((group) => (
                <li key={group.id}>
                  <span className="font-medium">{group.label || `Group ${group.ordinal + 1}`}:</span>{' '}
                  {group.memberIds.length === 0 ? '(empty group)' : state.members.filter((member) => group.memberIds.includes(member.memberId)).map((member) => member.label).join(', ')}
                </li>
              ))}
            </ul>
          </section>
        </>
      ) : null}
      {error ? <p className="mt-3 text-sm text-red-800" role="alert">{error}</p> : null}
      {saving ? <p className="mt-3" role="status">Creating immutable group revision…</p> : null}
      <div className="mt-4 flex justify-end">
        <button
          type="button"
          className="rounded-md bg-blue-700 px-3 py-2 text-sm font-semibold text-white disabled:opacity-50"
          disabled={state.kind !== 'editing' || Boolean(state.error) || state.groups.some((group) => group.label.trim().length === 0)}
          onClick={() => void create()}
        >
          Create group revision
        </button>
      </div>
    </section>
  );
};
