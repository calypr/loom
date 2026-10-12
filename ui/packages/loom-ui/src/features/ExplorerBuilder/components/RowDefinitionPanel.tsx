import React, { useMemo, useState } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  startingRootChoices,
  type RootChangeSelection,
} from '../authoring/rootChangeSelection';

type RootPickerChoice =
  | {
      readonly kind: 'current';
      readonly key: string;
      readonly resourceType: string;
    }
  | {
      readonly kind: 'connected';
      readonly key: string;
      readonly resourceType: string;
      readonly routeLabel?: string;
      readonly selection: RootChangeSelection;
    }
  | {
      readonly kind: 'replacement-only';
      readonly key: string;
      readonly resourceType: string;
      readonly nodeId: string;
    };

const RootTypePicker = ({
  choices,
  currentChoiceKey,
  disabled,
  onChange,
  onReplace,
}: {
  readonly choices: ReadonlyArray<RootPickerChoice>;
  readonly currentChoiceKey: string;
  readonly disabled: boolean;
  readonly onChange: (selection: RootChangeSelection) => void;
  readonly onReplace?: (rootNodeId: string) => void;
}) => {
  const [selectedChoiceKey, setSelectedChoiceKey] = useState(currentChoiceKey);
  const selectedChoice = choices.find((choice) => choice.key === selectedChoiceKey) ??
    choices.find((choice) => choice.key === currentChoiceKey);
  const selectedConnectedChoice = selectedChoice?.kind === 'connected' ? selectedChoice : undefined;
  const reviewDisabled = disabled || !selectedChoice || selectedChoice.kind === 'current';
  const selectionExplanation = selectedChoice?.kind === 'connected'
    ? 'Loom will check which existing columns and filters can carry over.'
    : selectedChoice?.kind === 'replacement-only'
      ? 'A new setup is required. Review will list the current setup items that would be removed.'
      : undefined;

  return (
    <section aria-label="What should each row represent?" className="mt-3 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-slate-800">
      <h4 className="font-semibold text-slate-900">What should each row represent?</h4>
      <p className="mt-1 text-xs text-slate-600">
        Choose a record type. Loom will show how this changes your rows and whether existing columns or filters need updating.
      </p>
      {selectionExplanation ? <p className="mt-1 text-xs text-slate-600">{selectionExplanation}</p> : null}
      <label className="mt-2 block font-medium text-slate-900">
        <span>Record type</span>
        <select
          aria-label="Record type"
          className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-3 py-2 outline-blue-500 disabled:opacity-50"
          value={selectedChoice?.key ?? currentChoiceKey}
          disabled={disabled}
          onChange={(event) => setSelectedChoiceKey(event.currentTarget.value)}
        >
          {choices.map((choice) => (
            <option key={choice.key} value={choice.key} disabled={choice.kind === 'current'}>
              {choice.kind === 'current'
                ? `${choice.resourceType} (current)`
                : choice.kind === 'connected'
                  ? `${choice.resourceType}${choice.routeLabel ? ` via ${choice.routeLabel}` : ''}`
                  : choice.resourceType}
            </option>
          ))}
        </select>
      </label>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="rounded-md bg-blue-700 px-3 py-2 text-xs font-semibold text-white hover:bg-blue-800 disabled:opacity-50"
          disabled={reviewDisabled}
          onClick={() => {
            if (selectedChoice?.kind === 'connected') onChange(selectedChoice.selection);
            if (selectedChoice?.kind === 'replacement-only') onReplace?.(selectedChoice.nodeId);
          }}
        >
          Review changes
        </button>
        {selectedConnectedChoice && onReplace ? (
          <button
            type="button"
            className="rounded-md border border-amber-700 px-3 py-2 text-xs font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-50"
            disabled={disabled}
            onClick={() => onReplace(selectedConnectedChoice.selection.nodeId)}
          >
            Start fresh with {selectedConnectedChoice.resourceType}
          </button>
        ) : null}
      </div>
    </section>
  );
};

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
  const connectedChoices = useMemo(
    () => startingRootChoices(table, catalog),
    [catalog, table],
  );
  const currentOccurrence = derivedOccurrences(table, catalog)[0];
  const currentRootNodeId = currentOccurrence?.nodeId;
  const currentResourceType = table.document.rootResourceType;
  const currentConnectedChoice = connectedChoices.find((choice) => choice.current);
  const replacementOnlyChoices = useMemo(() => {
    if (!onReplace) return [];
    const representedResourceTypes = new Set([
      currentResourceType,
      ...connectedChoices.map((choice) => choice.resourceType),
    ]);
    const choices: Array<Extract<RootPickerChoice, { kind: 'replacement-only' }>> = [];
    for (const node of catalog.nodes) {
      if (!node.rowRootEligible || representedResourceTypes.has(node.resourceType)) continue;
      representedResourceTypes.add(node.resourceType);
      choices.push({
        kind: 'replacement-only',
        key: `replacement:${node.nodeId}`,
        resourceType: node.resourceType,
        nodeId: node.nodeId,
      });
    }
    return choices;
  }, [catalog.nodes, connectedChoices, currentResourceType, onReplace]);

  if (!currentResourceType) return null;

  const currentChoiceKey = currentConnectedChoice?.key ?? 'current';
  const choices: ReadonlyArray<RootPickerChoice> = [
    {
      kind: 'current',
      key: currentChoiceKey,
      resourceType: currentResourceType,
    },
    ...connectedChoices
      .filter((choice) => !choice.current)
      .map((choice): RootPickerChoice => ({
        kind: 'connected',
        key: choice.key,
        resourceType: choice.resourceType,
        routeLabel: choice.relationship,
        selection: choice.selection,
      })),
    ...replacementOnlyChoices,
  ];
  const hasAlternative = choices.some((choice) => choice.kind !== 'current');
  if (!hasAlternative) return null;

  const contextKey = [
    catalog.snapshotToken,
    table.outputId,
    currentRootNodeId ?? currentResourceType,
  ].join('\u0000');

  return (
    <RootTypePicker
      key={contextKey}
      choices={choices}
      currentChoiceKey={currentChoiceKey}
      disabled={disabled}
      onChange={onChange}
      onReplace={onReplace}
    />
  );
};
