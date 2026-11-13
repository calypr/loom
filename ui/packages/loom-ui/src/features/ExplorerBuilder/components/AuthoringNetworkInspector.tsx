import { useRef, useState } from 'react';
import type { DescribeAuthoringNetworkArgs, LoomClient } from '../../../api';
import type { AuthoringNetworkResponse } from '../../../types';

const valuePolicyLabels: Readonly<Record<string, string>> = {
  AUTO: 'Automatic source policy', VALUE: 'Scalar source value', FIRST: 'First value',
  ALL: 'All matching values', DISTINCT: 'Distinct matching values',
};
const valuePolicyLabel = (mode: string | undefined) => valuePolicyLabels[mode ?? ''] ?? mode ?? 'Configured source policy';

// Keyed by draft, snapshot and table by the caller. Inspecting is a read and
// never replays edits or writes the derived network into the saved document.
export function AuthoringNetworkInspector({ client, identity }: {
  readonly client: LoomClient;
  readonly identity: DescribeAuthoringNetworkArgs;
}) {
  const [plan, setPlan] = useState<AuthoringNetworkResponse>();
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const revision = useRef(0);
  const load = async () => {
    const token = ++revision.current;
    setBusy(true); setError(undefined);
    try {
      const response = await client.describeAuthoringNetwork(identity);
      if (token !== revision.current) return;
      if (response.draftVersion !== identity.expectedDraftVersion || response.draftDigest !== identity.expectedDraftDigest ||
          response.snapshotToken !== identity.snapshotToken || response.network.outputId !== identity.outputId || response.rowIdentity.outputId !== identity.outputId) {
        throw new Error('The saved plan changed. Inspect the current table again.');
      }
      setPlan(response);
    } catch (cause) { if (token === revision.current) setError(cause instanceof Error ? cause.message : 'The saved plan could not be inspected.'); }
    finally { if (token === revision.current) setBusy(false); }
  };
  return <details className="rounded border border-slate-200 p-2 text-xs" onToggle={(event) => {
    if (event.currentTarget.open && !plan && !busy) void load();
  }}>
    <summary className="cursor-pointer font-semibold">Inspect saved row identity and value scopes</summary>
    {busy ? <p role="status">Loading the saved plan…</p> : null}
    {error ? <p role="alert">{error} <button type="button" onClick={() => void load()}>Retry</button></p> : null}
    {plan ? <div className="max-h-48 overflow-auto">
      <p>{plan.network.owner === 'IMPORTED_SOURCE' ? 'Pinned recipe with project-local source changes.' : 'Saved route and source row definition.'}</p>
      <ol>{plan.rowIdentity.stages.map((stage) => <li key={stage.stageId} className="mt-1">
        <strong>{stage.operationId ?? 'Source'}</strong>: {stage.meaning}
        {stage.mayExcludeRows ? ' Unmatched or filtered rows may be excluded.' : ''}
        {stage.unsupportedReason ? <p>{stage.unsupportedReason}</p> : null}
        <p>Count unknown · multiplicity unknown</p>
        {stage.identity.anchorColumnId ? <p>Anchor: {stage.identity.anchorColumnId} · input: {stage.identity.inputStageId}</p> : null}
        {stage.identity.keyColumnIds ? <p>Complete key tuple: {stage.identity.keyColumnIds.join(', ')} · missing: {stage.identity.missingPolicy}</p> : null}
      </li>)}</ol>
      <details><summary>Column bindings</summary><ul>{plan.network.bindings.map((binding) => <li key={binding.columnId}>
        {binding.columnId}: {binding.status === 'EXACT' ? `${binding.occurrenceId} · ${valuePolicyLabel(binding.valueMode)}` : 'Source provenance unavailable; no occurrence inferred.'}
      </li>)}</ul></details>
    </div> : null}
  </details>;
}
