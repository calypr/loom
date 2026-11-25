import React from 'react';
import { useEscapeToClose } from '../../dom/useEscapeToClose';
import { useExplicitGroups, type ExplicitGroupTransport } from '../../session/useExplicitGroups';
import type { ExplicitGroupRevisionSummary } from '../../../../contracts/types';
import type { SelectionRevision } from '../../../../contracts/selection';

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
  readonly client: ExplicitGroupTransport;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly selection: SelectionRevision;
  readonly onCancel: () => void;
  readonly onCreated: (revision: ExplicitGroupRevisionSummary) => Promise<void>;
}) => {
  const { state, setState, updateGroup, loadMore, create, opaqueID } = useExplicitGroups({
    client, project, explorerId, authResourcePath, snapshotToken, selection, onCreated,
  });
  const saving = state.kind === 'saving';
  const members = state.kind === 'loading' ? [] : state.members;
  const groups = state.kind === 'loading' ? [] : state.groups;
  const error = state.kind === 'editing' ? state.error : undefined;

  useEscapeToClose(state.kind !== 'saving', onCancel);
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
