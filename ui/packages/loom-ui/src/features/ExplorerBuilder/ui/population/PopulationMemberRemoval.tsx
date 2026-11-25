import React from 'react';
import { useEscapeToClose } from '../../hooks/useEscapeToClose';
import { usePopulationMemberRemoval, type PopulationMemberTransport } from '../../session/usePopulationMemberRemoval';
import type { SelectionRevision } from '../../../../contracts/selection';

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
  const {
    memberQuery, displayedMembers, proposalQuery, currentIntent, activeApplyState, proposal,
    loadNextPage, startRemoval, applyRemoval, cancelRemoval,
  } = usePopulationMemberRemoval({
    client, project, explorerId, authResourcePath, snapshotToken, draftVersion, draftDigest,
    outputId, selection, disabled, onApply,
  });
  useEscapeToClose(Boolean(currentIntent) && activeApplyState?.pending !== true, cancelRemoval);

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
            <button type="button" disabled={activeApplyState?.pending === true} onClick={cancelRemoval} className="rounded border border-slate-300 bg-white px-3 py-2 font-semibold disabled:opacity-40">
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
