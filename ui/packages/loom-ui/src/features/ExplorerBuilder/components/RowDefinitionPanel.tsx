import React, { useMemo, useState } from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from '../authoring/model';
import {
  compilerRootSelectionLabel,
  compilerRootRepairChoices,
  sameRootChangeRequestIdentity,
  startingRootChoices,
  type CompilerRootPickerChoice,
  type RootChangePickerState,
  type RootChangeRequestIdentity,
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
      readonly nodeId: string;
      readonly routeLabel?: string;
      readonly selection: RootChangeSelection;
    }
  | {
      readonly kind: 'relationship-prompt';
      readonly key: string;
      readonly resourceType: string;
      readonly nodeId: string;
    }
  | {
      readonly kind: 'selected-route';
      readonly key: string;
      readonly resourceType: string;
      readonly nodeId: string;
      readonly label: string;
    }
  | {
      readonly kind: 'pending-root';
      readonly key: string;
      readonly resourceType: string;
      readonly nodeId: string;
    }
  | {
      readonly kind: 'replacement-only';
      readonly key: string;
      readonly resourceType: string;
      readonly nodeId: string;
    }
  | CompilerRootPickerChoice;

const RootTypePicker = ({
  choices,
  currentChoiceKey,
  disabled,
  rootChangeState,
  onChange,
  onResolve,
  onRefresh,
  onKeepCurrent,
  onReplace,
}: {
  readonly choices: ReadonlyArray<RootPickerChoice>;
  readonly currentChoiceKey: string;
  readonly disabled: boolean;
  readonly rootChangeState: RootChangePickerState;
  readonly onChange: (selection: RootChangeSelection) => void;
  readonly onResolve: (referenceId: string, alternative: string) => void;
  readonly onRefresh: () => void;
  readonly onKeepCurrent: () => void;
  readonly onReplace?: (rootNodeId: string) => void;
}) => {
  const [localChoiceKey, setLocalChoiceKey] = useState(currentChoiceKey);
  const activeSelection = rootChangeState.kind === 'idle'
    ? undefined
    : rootChangeState.selection;
  const hasAmbiguousRoot = rootChangeState.kind === 'blocked' &&
    rootChangeState.assessment.unresolved.some(
      (reference) => reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
    );
  const requiresRefresh = rootChangeState.kind === 'failed' &&
    rootChangeState.reason !== 'request';
  const selectedChoiceKey = (() => {
    if (!activeSelection) return localChoiceKey;
    if (hasAmbiguousRoot || requiresRefresh) return `relationship-prompt:${activeSelection.nodeId}`;
    if (activeSelection.kind === 'compiler-route' ||
        activeSelection.kind === 'compiler-occurrence') {
      return `selected-route:${activeSelection.nodeId}`;
    }
    const match = choices.find((choice) => choice.kind === 'connected' &&
      (activeSelection.kind === 'authored-occurrence'
        ? choice.selection.kind === 'authored-occurrence' &&
          choice.selection.occurrenceId === activeSelection.occurrenceId
        : choice.nodeId === activeSelection.nodeId));
    return match?.key ?? `pending-root:${activeSelection.nodeId}`;
  })();
  const selectedChoice = choices.find((choice) => choice.key === selectedChoiceKey);
  const selectedRootNodeId = activeSelection?.nodeId ??
    (selectedChoice?.kind === 'connected' ? selectedChoice.nodeId : undefined);
  const selectedRootChoice = choices.find((choice): choice is Extract<RootPickerChoice, {
    readonly kind: 'connected' | 'selected-route' | 'relationship-prompt' | 'pending-root';
  }> => (choice.kind === 'connected' || choice.kind === 'selected-route' ||
    choice.kind === 'relationship-prompt' || choice.kind === 'pending-root') &&
    choice.nodeId === selectedRootNodeId);
  const selectedRootResourceType = selectedRootNodeId
    ? selectedRootChoice?.resourceType
    : undefined;
  const hasRepairChoices = choices.some((choice) =>
    choice.kind === 'compiler-route' || choice.kind === 'compiler-occurrence' ||
    choice.kind === 'compiler-edge',
  );
  const showRefresh = rootChangeState.kind === 'failed' ||
    (rootChangeState.kind === 'blocked' && !hasRepairChoices);

  const handleSelection = (choiceKey: string) => {
    const choice = choices.find((candidate) => candidate.key === choiceKey);
    if (!choice || choice.kind === 'current' || choice.kind === 'relationship-prompt' ||
        choice.kind === 'selected-route' || choice.kind === 'pending-root') return;
    setLocalChoiceKey(choice.key);
    if (choice.kind === 'connected') {
      onChange(choice.selection);
    } else if (choice.kind === 'compiler-route' || choice.kind === 'compiler-occurrence') {
      onChange(choice.selection);
    } else if (choice.kind === 'compiler-edge') {
      onResolve(choice.referenceId, choice.edgeId);
    } else if (choice.kind === 'replacement-only') {
      if (rootChangeState.kind !== 'idle') onKeepCurrent();
      onReplace?.(choice.nodeId);
    }
  };

  return (
    <section aria-label="Starting record type" className="min-w-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-800">
      <label className="flex min-w-0 flex-col gap-1 font-medium text-slate-900 sm:flex-row sm:items-center sm:gap-3">
        <span className="shrink-0 text-xs">Starting record type</span>
        <select
          aria-label="Record type"
          className="min-w-0 flex-1 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm outline-blue-500 disabled:opacity-50"
          value={selectedChoiceKey}
          disabled={disabled}
          onChange={(event) => handleSelection(event.currentTarget.value)}
        >
          {choices.map((choice) => (
            <option
              key={choice.key}
              value={choice.key}
              disabled={choice.kind === 'current' || choice.kind === 'relationship-prompt' ||
                choice.kind === 'selected-route' || choice.kind === 'pending-root'}
            >
              {choice.kind === 'current'
                ? `${choice.resourceType} (current)`
                : choice.kind === 'connected'
                  ? `${choice.resourceType}${choice.routeLabel ? ` via ${choice.routeLabel}` : ''}`
                  : choice.kind === 'compiler-route' || choice.kind === 'compiler-occurrence' ||
                    choice.kind === 'compiler-edge'
                    ? choice.label
                    : choice.kind === 'relationship-prompt'
                      ? `${choice.resourceType} — Choose a relationship…`
                      : choice.kind === 'selected-route'
                        ? choice.label
                        : choice.kind === 'pending-root'
                          ? `${choice.resourceType} (selected)`
                        : choice.resourceType}
            </option>
          ))}
        </select>
      </label>

      {rootChangeState.kind === 'assessing' ? (
        <p className="mt-1 text-xs text-slate-700" role="status">
          Checking how the selected rows connect to this table…
        </p>
      ) : null}
      {rootChangeState.kind === 'previewing' ? (
        <p className="mt-1 text-xs text-slate-700" role="status">
          Loading the receipt-bound row preview…
        </p>
      ) : null}
      {rootChangeState.kind === 'failed' ? (
        <p className="mt-1 text-xs text-amber-900" role="alert">
          {rootChangeState.message}
        </p>
      ) : null}
      {rootChangeState.kind === 'blocked' ? (
        <div className="mt-1 space-y-1" aria-label="Record type choices">
          {rootChangeState.assessment.unresolved.map((reference) => (
            <p key={`${reference.code}:${reference.id}`} className="text-xs text-amber-900" role="status">
              {reference.message}
            </p>
          ))}
        </div>
      ) : null}

      {showRefresh ? (
        <div className="mt-1 flex flex-wrap items-center gap-3">
          <button type="button" className="text-xs font-semibold text-slate-700 underline" disabled={disabled} onClick={() => {
            setLocalChoiceKey(currentChoiceKey);
            onKeepCurrent();
          }}>
            Keep current rows
          </button>
          <button type="button" className="text-xs font-semibold text-blue-800 underline" disabled={disabled} onClick={onRefresh}>
            Refresh choices
          </button>
        </div>
      ) : null}

      {selectedRootNodeId && selectedRootResourceType && onReplace ? (
        <button
          type="button"
          className="mt-1 rounded-md border border-amber-700 px-3 py-1.5 text-xs font-semibold text-amber-900 hover:bg-amber-50 disabled:opacity-50"
          disabled={disabled}
          onClick={() => {
            if (rootChangeState.kind !== 'idle') onKeepCurrent();
            onReplace(selectedRootNodeId);
          }}
        >
          Start fresh with {selectedRootResourceType}
        </button>
      ) : null}
    </section>
  );
};

export const RowDefinitionPanel = ({
  catalog,
  table,
  disabled,
  rootChangeState,
  currentIdentity,
  onChange,
  onResolve,
  onRefresh,
  onKeepCurrent,
  onReplace,
}: {
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly disabled: boolean;
  readonly rootChangeState: RootChangePickerState;
  readonly currentIdentity: RootChangeRequestIdentity;
  readonly onChange: (selection: RootChangeSelection) => void;
  readonly onResolve: (referenceId: string, alternative: string) => void;
  readonly onRefresh: () => void;
  readonly onKeepCurrent: () => void;
  readonly onReplace?: (rootNodeId: string) => void;
}) => {
  const connectedChoices = useMemo(
    () => startingRootChoices(table, catalog),
    [catalog, table],
  );
  const occurrences = useMemo(() => derivedOccurrences(table, catalog), [catalog, table]);
  const currentOccurrence = occurrences[0];
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
  const baseChoices: ReadonlyArray<RootPickerChoice> = [
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
        nodeId: choice.selection.nodeId,
        routeLabel: choice.relationship,
        selection: choice.selection,
      })),
    ...replacementOnlyChoices,
  ];
  const stateIdentityIsCurrent = rootChangeState.kind === 'idle' ||
    sameRootChangeRequestIdentity(rootChangeState.identity, currentIdentity);
  const visibleState: RootChangePickerState = rootChangeState.kind !== 'idle' && !stateIdentityIsCurrent
    ? {
        kind: 'failed',
        reason: 'stale',
        selection: rootChangeState.selection,
        resolution: rootChangeState.resolution,
        identity: rootChangeState.identity,
        message: 'These row choices are out of date. Refresh them or keep the current rows.',
      }
    : rootChangeState;
  const blockedChoices = visibleState.kind === 'blocked'
    ? compilerRootRepairChoices({
        assessment: visibleState.assessment,
        catalog,
        table,
        target: visibleState.selection,
        resolution: visibleState.resolution,
        boundaryIdentity: currentIdentity,
      })
    : [];
  const selectedRouteLabel = visibleState.kind !== 'idle' && stateIdentityIsCurrent
    ? compilerRootSelectionLabel({
        selection: visibleState.selection,
        catalog,
        table,
        boundaryIdentity: currentIdentity,
      })
    : undefined;
  const selectedRouteChoice: RootPickerChoice | undefined = visibleState.kind !== 'idle' &&
    selectedRouteLabel &&
    (visibleState.selection.kind === 'compiler-route' ||
      visibleState.selection.kind === 'compiler-occurrence')
    ? {
        kind: 'selected-route',
        key: `selected-route:${visibleState.selection.nodeId}`,
        nodeId: visibleState.selection.nodeId,
        resourceType: catalog.nodes.find((node) => node.nodeId === visibleState.selection.nodeId)?.resourceType ?? currentResourceType,
        label: selectedRouteLabel,
      }
    : undefined;
  const hasAmbiguousRoot = visibleState.kind === 'blocked' &&
    visibleState.assessment.unresolved.some(
      (reference) => reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
    );
  const relationshipPrompt: RootPickerChoice | undefined = visibleState.kind !== 'idle' &&
    (hasAmbiguousRoot || (visibleState.kind === 'failed' && visibleState.reason !== 'request'))
    ? {
        kind: 'relationship-prompt',
        key: `relationship-prompt:${visibleState.selection.nodeId}`,
        nodeId: visibleState.selection.nodeId,
        resourceType: catalog.nodes.find((node) => node.nodeId === visibleState.selection.nodeId)?.resourceType ?? currentResourceType,
      }
    : undefined;
  const hasSelectedBaseChoice = visibleState.kind !== 'idle' && baseChoices.some((choice) =>
    choice.kind === 'connected' && choice.nodeId === visibleState.selection.nodeId,
  );
  const pendingRootChoice: RootPickerChoice | undefined = visibleState.kind !== 'idle' &&
    !hasSelectedBaseChoice && !selectedRouteChoice && !relationshipPrompt
    ? {
        kind: 'pending-root',
        key: `pending-root:${visibleState.selection.nodeId}`,
        nodeId: visibleState.selection.nodeId,
        resourceType: catalog.nodes.find((node) => node.nodeId === visibleState.selection.nodeId)?.resourceType ?? currentResourceType,
      }
    : undefined;
  const choices: ReadonlyArray<RootPickerChoice> = [
    ...baseChoices,
    ...(selectedRouteChoice ? [selectedRouteChoice] : []),
    ...(relationshipPrompt ? [relationshipPrompt] : []),
    ...(pendingRootChoice ? [pendingRootChoice] : []),
    ...blockedChoices,
  ];
  if (choices.length === 1 && visibleState.kind === 'idle') return null;

  const contextKey = [
    catalog.snapshotToken,
    table.outputId,
    currentOccurrence?.nodeId ?? currentResourceType,
  ].join('\u0000');

  return (
    <RootTypePicker
      key={contextKey}
      choices={choices}
      currentChoiceKey={currentChoiceKey}
      disabled={disabled}
      rootChangeState={visibleState}
      onChange={onChange}
      onResolve={onResolve}
      onRefresh={onRefresh}
      onKeepCurrent={onKeepCurrent}
      onReplace={onReplace}
    />
  );
};
