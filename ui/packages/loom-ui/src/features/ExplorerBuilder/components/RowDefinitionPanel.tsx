import React, { useEffect, useMemo, useState } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  startingRootChoices,
  type RootChangeSelection,
} from '../authoring/rootChangeSelection';

export const RowDefinitionPanel = ({
  catalog,
  table,
  disabled,
  onChange,
  onReplace,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly onChange: (selection: RootChangeSelection) => void;
  readonly onReplace?: (rootNodeId: string) => void;
}) => {
  const choices = useMemo(
    () => startingRootChoices(table, catalog),
    [catalog, table],
  );
  const currentRootNodeId = derivedOccurrences(table, catalog)[0]?.nodeId;
  const replacementChoices = useMemo(
    () => catalog.nodes.filter((node) => node.rowRootEligible),
    [catalog.nodes],
  );
  const [replacementNodeId, setReplacementNodeId] = useState('');

  useEffect(() => {
    setReplacementNodeId('');
  }, [catalog.snapshotToken, table.outputId, currentRootNodeId]);

  if (!table.document.rootResourceType) return null;
  const currentChoice = choices.find((choice) => choice.current);
  const hasPreservationChoices = choices.length >= 2 && currentChoice !== undefined;
  const hasReplacementChoices = onReplace !== undefined && replacementChoices.some(
    (node) => node.nodeId !== currentRootNodeId,
  );
  if (!hasPreservationChoices && !hasReplacementChoices) return null;

  return (
    <section aria-label="Starting record type" className="mt-3 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-slate-800">
      <h4 className="font-semibold text-slate-900">Starting record type</h4>
      {hasPreservationChoices ? (
        <>
          <p className="mt-1 text-xs text-slate-600">Choose a connected record type for each row.</p>
          <label className="mt-2 block font-medium text-slate-900">
            <span>Choose starting record type</span>
            <select
              aria-label="Choose starting record type"
              className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 disabled:opacity-50"
              value={currentChoice.key}
              disabled={disabled}
              onChange={(event) => {
                const choice = choices.find((candidate) => candidate.key === event.currentTarget.value);
                if (choice && !choice.current) onChange(choice.selection);
              }}
            >
              {choices.map((choice) => (
                <option key={choice.key} value={choice.key}>
                  {choice.resourceType}{choice.relationship ? ` via ${choice.relationship}` : ''}{choice.current ? ' (current)' : ''}
                </option>
              ))}
            </select>
          </label>
          <p className="mt-2 text-xs text-slate-600">Loom checks whether features, filters, actions, and the starting collection can be preserved before changing rows.</p>
        </>
      ) : null}
      {hasReplacementChoices ? (
        <div className="mt-3 border-t border-blue-200 pt-3">
          <h5 className="font-medium text-slate-900">Replace current setup</h5>
          <p className="mt-1 text-xs text-slate-600">Choose any loaded eligible record type. Loom will show the changes before applying them.</p>
          <label className="mt-2 block font-medium text-slate-900">
            <span>Choose replacement starting type</span>
            <select
              aria-label="Choose replacement starting type"
              className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 disabled:opacity-50"
              value={replacementNodeId}
              disabled={disabled}
              onChange={(event) => setReplacementNodeId(event.currentTarget.value)}
            >
              <option value="">Choose a record type</option>
              {replacementChoices.map((node) => (
                <option key={node.nodeId} value={node.nodeId}>
                  {node.resourceType}{node.nodeId === currentRootNodeId ? ' (current)' : ''}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className="mt-2 rounded-md border border-amber-700 px-3 py-2 text-xs font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-50"
            disabled={disabled || !replacementNodeId || replacementNodeId === currentRootNodeId}
            onClick={() => {
              if (replacementNodeId && replacementNodeId !== currentRootNodeId) onReplace(replacementNodeId);
            }}
          >
            Replace current setup
          </button>
        </div>
      ) : null}
    </section>
  );
};
