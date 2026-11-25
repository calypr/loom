import { useBuilderSession } from '../session/BuilderSessionProvider';
import { useWorkspaceUi, useWorkspaceUiActions } from '../session/useWorkspaceUi';
import { selectedTable } from '../model/authoring';
import React, { useId, useState } from 'react';
import { useColumnRuleInventory, type ColumnRuleInventoryScope } from '../session/columnRuleInventory';
import { observedCategorySystems, observedExtensionPaths, observedIdentifierSystems } from '../model/columnRuleInventory';
import { columnRulePresets, type ColumnRulePreset } from '../model/sourceColumnRule';
import type { AddColumnsGraphSource, AddColumnsView } from '../session/useAddColumns';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import type { ConstructionCapabilitiesResponse, ConstructionStep } from '../../../contracts/types';
import { useCodedPivotContext } from '../session/codedPivotContext';
import { sanitizeReasonText } from '../model/catalogItems';
import type { ConstructionOperationFamily } from '../constructionWorkspace/ConstructionWorkspace';
import type { ConstructionSelectableColumn } from '../constructionWorkspace/ConstructionColumnSelection';
import type { ConstructionCombineStep } from '../constructionOperations/combineEditorTypes';
import type { ConstructionReshapeEditorEntry } from '../constructionOperations/ConstructionReshapeEditor';
import { ConstructionColumnSelection } from '../constructionWorkspace/ConstructionColumnSelection';
import { ConceptCatalog } from '../components/ConceptCatalog';
import { useConstructionLifecycle } from '../session/useConstructionLifecycle';
import { useCombineCatalog } from '../session/useCombineCatalog';
import { ConstructionOperationEditor } from '../constructionOperations/ConstructionOperationEditor';
import { ConstructionReshapeEditor } from '../constructionOperations/ConstructionReshapeEditor';
import { FilterRowsEditor } from '../constructionOperations/FilterRowsEditor';
import { RelatedFieldEditor } from '../constructionOperations/RelatedFieldEditor';
import { ConstructionCombineEditor } from '../constructionOperations/ConstructionCombineEditor';

type Lifecycle = ReturnType<typeof useConstructionLifecycle>;
type CombineCatalog = ReturnType<typeof useCombineCatalog>;
type CatalogProps = React.ComponentProps<typeof ConceptCatalog>;
type ReshapeProps = React.ComponentProps<typeof ConstructionReshapeEditor>;
type FamilyOperation = { readonly family: ConstructionOperationFamily; readonly label: string };

type InlineSystemRule = { readonly active: boolean; readonly disabled: boolean; readonly onActivate: () => void };

type RuleInventory = {
  readonly systems: ReadonlyArray<string>;
  readonly categorySystems: ReadonlyArray<string>;
  readonly extensionPaths: ReadonlyArray<string>;
  readonly loading: boolean;
  readonly unavailable: boolean;
};

type ColumnRuleSelection =
  | { readonly kind: 'identifier' | 'categoryCoding' }
  | { readonly kind: 'extension'; readonly path: string }
  | { readonly kind: 'catalog'; readonly path?: string };

function ColumnSourceRows({ entries, disabled, onSelect, selected }: {
  readonly entries: ReadonlyArray<string>;
  readonly disabled: boolean;
  readonly onSelect: (entry: string) => void;
  readonly selected?: string;
}) {
  const labels = entries.map((entry) => /^https?:\/\//.test(entry)
    ? entry.replace(/\/$/, '').split(/[/#]/).at(-1) || entry : entry);
  return <ul className="max-h-56 divide-y divide-slate-100 overflow-auto border-y border-slate-200 text-xs">
    {entries.map((entry, index) => {
      const short = labels[index];
      const label = labels.indexOf(short) === labels.lastIndexOf(short) ? short : entry;
      return <li key={entry} className="min-w-0">
        <button type="button" disabled={disabled} title={entry} aria-pressed={selected === entry}
          className="block w-full truncate px-2 py-1.5 text-left text-blue-800 hover:bg-slate-50 aria-pressed:bg-blue-50 aria-pressed:font-semibold"
          onClick={() => onSelect(entry)}>{label}</button>
      </li>;
    })}
  </ul>;
}

function ColumnRulePicker({ inventoryScope, fieldPaths, disabled, reviewing, selection, onSelect, onClear, renderRule }: {
  readonly inventoryScope: ColumnRuleInventoryScope;
  readonly fieldPaths: ReadonlyArray<string>;
  readonly disabled: boolean;
  readonly reviewing: boolean;
  readonly selection?: ColumnRuleSelection;
  readonly onSelect: (selection: ColumnRuleSelection) => void;
  readonly onClear: () => boolean;
  readonly renderRule?: (preset: ColumnRulePreset, inventory: RuleInventory, path?: string, onClear?: () => void, inline?: InlineSystemRule) => React.ReactNode;
}) {
  const categoryHelpId = useId();
  const [categoryHelpOpen, setCategoryHelpOpen] = useState(false);
  const [sourceKind, setSourceKind] = useState<ColumnRuleSelection['kind']>();
  const identifiers = useColumnRuleInventory(inventoryScope, 'identifier', true);
  const extensions = useColumnRuleInventory(inventoryScope, 'extension', true);
  const categories = useColumnRuleInventory(inventoryScope, 'category', true);
  const inventory = {
    systems: observedIdentifierSystems(identifiers.data ?? []),
    categorySystems: observedCategorySystems(categories.data ?? []),
    extensionPaths: observedExtensionPaths(extensions.data ?? []),
    loading: identifiers.isLoading,
    unavailable: Boolean(identifiers.error),
  };
  const { categorySystems } = inventory;
  const groups = [
    { kind: 'identifier', label: 'Identifier systems', entries: inventory.systems },
    { kind: 'categoryCoding', label: 'Category coding systems', entries: categorySystems },
  ] as const;
  const pathGroups = [
    { kind: 'extension', label: 'Extension paths', entries: inventory.extensionPaths },
    { kind: 'catalog', label: 'Catalog fields', entries: fieldPaths },
  ] as const;
  const selectedPreset = columnRulePresets.find((preset) => preset.id === selection?.kind);
  const loading = identifiers.isLoading || extensions.isLoading || categories.isLoading;
  const failed = Boolean(identifiers.error || extensions.error || categories.error);
  const sources = [...groups, ...pathGroups].filter((group) => group.entries.length > 0);
  const activeKind = sources.find((group) => group.kind === sourceKind)?.kind
    ?? sources.find((group) => group.kind === selection?.kind)?.kind
    ?? sources.find((group) => group.kind === 'catalog')?.kind ?? sources[0]?.kind;
  return <section aria-label="Available advanced columns" className="grid min-w-0 content-start gap-3">
    {activeKind ? <label className="grid gap-1 text-xs font-medium text-slate-700">
      Column source
      <select value={activeKind} disabled={disabled} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
        onChange={(event) => {
          const source = sources.find((group) => group.kind === event.currentTarget.value);
          if (source && source.kind !== activeKind && onClear()) setSourceKind(source.kind);
        }}>
        {sources.map((source) => <option key={source.kind} value={source.kind}>{source.label}</option>)}
      </select>
    </label> : null}
    {groups.filter((group) => group.entries.length > 0).map(({ kind, label }) => {
      const preset = columnRulePresets.find((item) => item.id === kind);
      return <section key={kind} aria-label={label} hidden={activeKind !== kind} className={activeKind === kind ? "grid gap-1" : undefined}>
        <div className="flex flex-wrap items-center gap-2">
          <h4 className="text-sm font-semibold">{label}</h4>
          {kind === 'categoryCoding' ? (
            <button type="button" aria-label="How category columns differ from Simple"
              aria-expanded={categoryHelpOpen} aria-controls={categoryHelpId}
              className="h-5 w-5 shrink-0 rounded-full border border-slate-300 text-[11px] font-semibold leading-none text-slate-600 hover:bg-slate-100"
              onClick={() => setCategoryHelpOpen((open) => !open)}>i</button>
          ) : null}
        </div>
        {kind === 'categoryCoding' ? (
          <div id={categoryHelpId} hidden={!categoryHelpOpen} className="space-y-1 rounded bg-slate-50 p-2 text-xs text-slate-700">
            <p className="m-0"><strong>Advanced</strong> creates one column per selected coding system from a shared rule, using the coding’s display text.</p>
            <p className="m-0"><strong>Simple</strong> lets you add individual columns by coding system and version. It uses the code when the display text is blank.</p>
          </div>
        ) : null}
        {preset && renderRule?.(preset, inventory, undefined, onClear, {
          active: selection?.kind === kind,
          disabled: disabled || (reviewing && selection?.kind !== kind),
          onActivate: () => onSelect({ kind }),
        })}
      </section>;
    })}
    {pathGroups.filter((group) => group.entries.length > 0).map(({ kind, label, entries }) =>
      <section key={kind} aria-label={label} hidden={activeKind !== kind} className={activeKind === kind ? "grid gap-1" : undefined}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-sm font-semibold">{label}</h4>
          {kind === 'catalog' ? <button type="button" disabled={disabled}
            className="rounded border border-blue-700 px-2 py-1 text-xs font-semibold text-blue-800"
            onClick={() => onSelect({ kind })}>Build a multi-field rule</button> : null}
        </div>
        <p className="text-xs text-slate-600">{kind === 'catalog' ? 'Select a field to add.' : 'Choose a path to generate columns from its extensions.'}</p>
        <ColumnSourceRows entries={entries} disabled={disabled} selected={selection?.kind === kind ? selection.path : undefined}
          onSelect={(path) => onSelect({ kind, path })} />
        {selection?.kind === kind && selectedPreset ? renderRule?.(selectedPreset, inventory, selection.path, onClear) : null}
      </section>,
    )}
    {loading ? <p role="status" className="text-xs text-slate-600">Loading observed column sources…</p>
      : failed ? <p role="status" className="text-xs text-slate-600">Some observed column sources could not be loaded.</p>
      : !activeKind ? <p role="status" className="text-xs text-slate-600">No advanced column sources were observed on this node.</p> : null}
  </section>;
}

interface OperationEditorHostProps {
  readonly activeOperation: FamilyOperation;
  readonly rowIntentSession: boolean;
  readonly editingConstructionStep?: ConstructionStep;
  readonly lifecycle: Lifecycle;
  readonly pendingCommands: number;
  readonly publishing: boolean;
  readonly showReshapeColumnSelection: boolean;
  readonly selectedColumnIds: ReadonlyArray<string>;
  readonly selectableColumns: ReadonlyArray<ConstructionSelectableColumn>;
  readonly currentStage?: ConstructionCapabilitiesResponse['stages'][number];
  readonly activeConstructionFamily?: ConstructionOperationFamily;
  readonly onToggleSelectedColumn: (columnId: string) => void;
  readonly setColumnSelection: (selection: { readonly kind: 'empty' }) => void;
  readonly rowValuesSupported: boolean;
  readonly graphSource?: AddColumnsGraphSource;
  readonly addSourceRouteContext: CatalogProps['routeContext'];
  readonly addColumnsViewRevision: number;
  readonly fieldPresentationContext: CatalogProps['presentationContext'];
  readonly addColumnsView: AddColumnsView;
  readonly columnRulesUnavailableReason?: string;
  readonly columnRuleInventoryScope: ColumnRuleInventoryScope;
  readonly columnRuleFieldPaths: ReadonlyArray<string>;
  readonly configuredAdvancedColumns?: React.ReactNode;
  readonly renderColumnRule?: (preset: ColumnRulePreset, inventory: RuleInventory, path?: string, onClear?: () => void, inline?: InlineSystemRule) => React.ReactNode;
  readonly reviewingAdvancedColumns?: boolean;
  readonly onBackFromColumnRuleReview: () => boolean;
  readonly onSwitchAddColumnsView: (view: AddColumnsView) => void;
  readonly existingSourceKeys: CatalogProps['existingSourceKeys'];
  readonly capabilityNodeOccurrences: CatalogProps['nodeOccurrences'];
  readonly sourceAvailability: CatalogProps['sourceProjectionAvailability'];
  readonly relatedSourceAvailability: CatalogProps['relatedSourceAvailability'];
  readonly hasUnsupportedSavedSourceColumns: boolean;
  readonly sourceSelectionDisabledReason?: string;
  readonly groupedRowValuePolicy: CatalogProps['groupedRowValuePolicy'];
  readonly onAddSelected: NonNullable<CatalogProps['onAddSelected']>;
  readonly onPreviewSelected: NonNullable<CatalogProps['onPreviewSelected']>;
  readonly onInspectRouteCoverage: NonNullable<CatalogProps['onInspectRouteCoverage']>;
  readonly activeReshapeEditorKey: string | undefined;
  readonly activeReshapeInitialKind: ReshapeProps['initialKind'];
  readonly activeReshapeInitialEntry: ConstructionReshapeEditorEntry | undefined;
  readonly onReshapeFormKindChange: NonNullable<ReshapeProps['onFormKindChange']>;
  readonly relatedExpandQueryOwnerRef: ReshapeProps['relatedExpandQueryOwnerRef'];
  readonly combineCatalog: CombineCatalog;
  readonly onEditConstructionStep: NonNullable<ReshapeProps['onEditStep']>;
  readonly onCancelConstructionEditor: NonNullable<ReshapeProps['onCancel']>;
  readonly onClose: () => void;
}

export const OperationEditorHost = ({
  activeOperation,
  rowIntentSession,
  editingConstructionStep,
  lifecycle: constructionLifecycle,
  pendingCommands,
  publishing,
  showReshapeColumnSelection,
  selectedColumnIds,
  selectableColumns,
  currentStage,
  activeConstructionFamily,
  onToggleSelectedColumn: toggleSelectedColumn,
  setColumnSelection,
  rowValuesSupported,
  graphSource,
  addSourceRouteContext,
  addColumnsViewRevision,
  fieldPresentationContext,
  addColumnsView: columnKind,
  columnRulesUnavailableReason,
  columnRuleInventoryScope,
  columnRuleFieldPaths,
  configuredAdvancedColumns,
  renderColumnRule,
  reviewingAdvancedColumns,
  onBackFromColumnRuleReview,
  onSwitchAddColumnsView: selectColumnKind,
  existingSourceKeys,
  capabilityNodeOccurrences,
  sourceAvailability,
  relatedSourceAvailability,
  hasUnsupportedSavedSourceColumns,
  sourceSelectionDisabledReason,
  groupedRowValuePolicy,
  onAddSelected: addSelectedFeatures,
  onPreviewSelected: previewRelatedSourceColumns,
  onInspectRouteCoverage: inspectRelatedRouteCoverage,
  activeReshapeEditorKey,
  activeReshapeInitialKind,
  activeReshapeInitialEntry,
  onReshapeFormKindChange: setReshapeFormKind,
  relatedExpandQueryOwnerRef,
  combineCatalog,
  onEditConstructionStep: editConstructionStep,
  onCancelConstructionEditor: cancelConstructionEditor,
  onClose: closeConstructionWorkspace,
}: OperationEditorHostProps) => {
  const { state, ownerKey, builderQueryArgs } = useBuilderSession(draft => draft);
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const table = selectedTable(state)!;
  const construction = table.document.construction;
  const codedPivotContext = useCodedPivotContext({
    project: projectId, explorerId: state.explorerId, authResourcePath,
    snapshotToken: state.catalog.snapshotToken, outputId: table.outputId, rowRoot: table.document.rootResourceType,
  });
  const { advancedView, rowValuePolicy } = useWorkspaceUi(view => view);
  const { setPendingSavedRuleRemoval, setAdvancedView, setRowValuePolicy } = useWorkspaceUiActions();
  const onAdvancedViewChange = (view: 'add' | 'saved') => {
    setPendingSavedRuleRemoval(undefined);
    setAdvancedView(view);
  };
  const columnPickerId = useId();
  const advanced = columnKind === 'advanced' && !graphSource?.exactRelated;
  const columnRuleOwner = JSON.stringify([ownerKey, table.outputId, graphSource?.occurrenceId]);
  const [ruleSelection, setRuleSelection] = useState<{ owner: string; selection: ColumnRuleSelection }>();
  const selectedRule = advanced && activeOperation.family === 'ADD_COLUMNS' && ruleSelection?.owner === columnRuleOwner
    ? ruleSelection.selection : undefined;
  const backFromOperation = () => {
    if (reviewingAdvancedColumns) {
      if (!onBackFromColumnRuleReview()) return;
      if (selectedRule?.kind === 'catalog' && selectedRule.path) setRuleSelection(undefined);
    }
    else if (selectedRule) setRuleSelection(undefined);
    else closeConstructionWorkspace();
  };
  const backLabel = reviewingAdvancedColumns ? 'Back to rule'
    : selectedRule ? 'Back to available columns' : 'Back to table';
  useEscapeToClose(
    !reviewingAdvancedColumns && (
      activeOperation.family === 'ADD_COLUMNS' || activeOperation.family === 'CALCULATE' || activeOperation.family === 'COMBINE'
    ),
    backFromOperation,
  );
  const catalogView = graphSource?.exactRelated || columnKind === 'advanced' ? 'all' : columnKind;
  const columnTabs = [{ view: 'all', label: 'Simple' }, { view: 'advanced', label: 'Advanced' }, { view: 'saved', label: 'Saved rules' }] as const;
  const activeColumnTab = advanced ? advancedView === 'saved' ? 'saved' : 'advanced' : 'all';
  const selectColumnTab = (view: typeof activeColumnTab) => {
    if (view === activeColumnTab) return;
    setRuleSelection(undefined);
    if (view !== 'all') onAdvancedViewChange(view === 'saved' ? 'saved' : 'add');
    selectColumnKind(view === 'all' ? 'all' : 'advanced');
  };
  const columnControlsDisabled = pendingCommands > 0 || state.reconciliation === 'pending' || publishing;
  return (
    <section
      aria-label={`${activeOperation.label} editor`}
      data-testid="construction-operation-editor"
      data-operation-family={activeOperation.family}
      data-output-id={table.outputId}
      className={activeOperation.family === 'ADD_COLUMNS' ? "flex h-full min-h-0 min-w-0 flex-col bg-white" : "min-w-0 bg-white"}
    >
      <header className={activeOperation.family === 'ADD_COLUMNS' ? "px-3 py-2" : rowIntentSession ? "px-0 py-1" : "border-b border-slate-200 px-3 py-2.5"}>
        <div className="flex items-center justify-between gap-3">
          <div>
            {!rowIntentSession && activeOperation.family !== 'RESHAPE' ? <h2 className="text-base font-semibold text-slate-950" style={activeOperation.family === 'ADD_COLUMNS' ? { color: '#000' } : undefined}>{activeOperation.family === 'ADD_COLUMNS' && graphSource ? `Add columns from ${graphSource.resourceType}` : activeOperation.label}</h2> : null}
            {editingConstructionStep ? <p className="text-xs text-slate-500">Editing a saved step</p> : null}
          </div>
          <button
            type="button"
            aria-label={reviewingAdvancedColumns || selectedRule ? backLabel : 'Close operation editor'}
            data-testid="construction-close-operation-editor"
            disabled={reviewingAdvancedColumns && (pendingCommands > 0 || publishing)}
            onClick={backFromOperation}
            className="inline-flex shrink-0 items-center gap-1 rounded border border-slate-300 bg-white px-2.5 py-1 text-xs font-semibold text-slate-700 shadow-sm hover:bg-slate-50"
          >
            <span aria-hidden="true">←</span> {backLabel}
          </button>
        </div>
        {activeOperation.family === 'CALCULATE' || showReshapeColumnSelection ? (
          <div className="mt-3">
            <ConstructionColumnSelection
              key={`${activeConstructionFamily}:${currentStage?.id ?? ''}:${selectableColumns.map((column) => column.id).join(',')}`}
              columns={selectableColumns}
              selectedColumnIds={selectedColumnIds}
              disabled={pendingCommands > 0 || !currentStage}
              onToggleColumn={toggleSelectedColumn}
              onClear={() => setColumnSelection({ kind: 'empty' })}
            />
          </div>
        ) : activeOperation.family !== 'KEEP_ROWS' && selectedColumnIds.length > 0 ? (
          <p className="mt-3 rounded bg-slate-50 px-2.5 py-2 text-xs text-slate-600">
            Inputs from your selection:{' '}
            <span className="font-medium text-slate-800">
              {selectableColumns
                .filter((column) => selectedColumnIds.includes(column.id))
                .map((column) => column.label)
                .join(', ')}
            </span>
          </p>
        ) : null}
      </header>

      <div className={activeOperation.family === 'ADD_COLUMNS' ? "min-h-0 flex-1" : rowIntentSession ? "py-2" : "p-3"}>
        {activeOperation.family === 'ADD_COLUMNS' ? (
          <div className="grid gap-4">
            {editingConstructionStep?.operation.kind === 'GROUP' && constructionLifecycle.capabilities.status === 'ready' ? (
              <ConstructionReshapeEditor
                key={`${ownerKey}:${state.catalog.snapshotToken}:${table?.outputId ?? ''}:${editingConstructionStep.id}:${constructionLifecycle.capabilities.response.selectedStage.id}`}
                construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                capabilities={constructionLifecycle.capabilities.response}
                editingStep={editingConstructionStep}
                initialKind="group"
                rowValuesOnly
                disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                onCandidateChange={constructionLifecycle.onCandidateChange}
                onEditStep={editConstructionStep}
                onCancel={cancelConstructionEditor}
              />
            ) : null}
            {rowValuesSupported ? (
              <label className="flex flex-wrap items-center gap-2 text-sm text-slate-700">
                Values from each row's contributing records
                <select aria-label="Values per grouped row" value={rowValuePolicy} onChange={(event) => {
                  const value = event.currentTarget.value;
                  if (value === 'ALL' || value === 'ONE') setRowValuePolicy(value);
                }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                  <option value="ALL">Keep all distinct values</option>
                  <option value="ONE">Require one distinct value</option>
                </select>
                <span className="text-xs text-slate-500">Missing values stay empty. Require one stops if records disagree.</span>
              </label>
            ) : null}
            {table.document.rootResourceType ? (
              <>
                {!graphSource?.exactRelated ? (
                  <div role="tablist" aria-label="Column picker" className="flex gap-1 border-b border-slate-200 px-3"
                    onKeyDown={(event) => {
                      if (columnControlsDisabled || reviewingAdvancedColumns || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                      event.preventDefault();
                      const current = columnTabs.findIndex(({ view }) => view === activeColumnTab);
                      const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? columnTabs.length - 1
                        : (current + (event.key === 'ArrowLeft' ? -1 : 1) + columnTabs.length) % columnTabs.length;
                      const next = columnTabs[nextIndex]?.view;
                      if (!next) return;
                      selectColumnTab(next);
                      event.currentTarget.querySelector<HTMLButtonElement>(`[data-column-tab="${next}"]`)?.focus();
                    }}>
                    {columnTabs.map(({ view, label }) => {
                      const selected = activeColumnTab === view;
                      return <button key={view} type="button" role="tab"
                        id={`${columnPickerId}-${view}`}
                        aria-controls={`${columnPickerId}-panel`}
                        aria-selected={selected}
                        tabIndex={selected ? 0 : -1}
                        disabled={columnControlsDisabled || (reviewingAdvancedColumns && !selected)}
                        data-column-tab={view}
                        onClick={() => selectColumnTab(view)}
                        className="border-b-2 border-transparent px-2.5 py-1.5 text-sm font-medium text-slate-600 aria-selected:border-blue-700 aria-selected:text-blue-800">
                        {label}
                      </button>;
                    })}
                  </div>
                ) : null}
                <div role={graphSource?.exactRelated ? undefined : 'tabpanel'}
                  id={`${columnPickerId}-panel`}
                  aria-labelledby={graphSource?.exactRelated ? undefined : `${columnPickerId}-${activeColumnTab}`}
                  className="grid gap-3">
                  {advanced ? <div className="px-3 pb-3">
                    {advancedView === 'saved' && !selectedRule ? configuredAdvancedColumns
                      : !columnRulesUnavailableReason ? <ColumnRulePicker
                        key={columnRuleOwner}
                        inventoryScope={columnRuleInventoryScope} fieldPaths={columnRuleFieldPaths}
                        disabled={columnControlsDisabled} reviewing={Boolean(reviewingAdvancedColumns)} selection={selectedRule} renderRule={renderColumnRule}
                        onSelect={(selection) => {
                          if (selection.kind === selectedRule?.kind
                              && ('path' in selection ? selection.path : undefined) === ('path' in selectedRule ? selectedRule.path : undefined)) return;
                          if (reviewingAdvancedColumns && !onBackFromColumnRuleReview()) return;
                          setRuleSelection({ owner: columnRuleOwner, selection });
                        }}
                        onClear={() => {
                          if (reviewingAdvancedColumns && !onBackFromColumnRuleReview()) return false;
                          setRuleSelection(undefined);
                          return true;
                        }}
                      /> : <p className="text-xs text-slate-600">{columnRulesUnavailableReason}</p>}
                  </div> : graphSource && !graphSource.isRoot && !graphSource.nodeId ? (
                    <p role="status" className="px-3 text-sm text-amber-900">Columns are unavailable because this graph relationship could not be resolved.</p>
                  ) : (
                    <>
                      {graphSource?.exactRelated && constructionLifecycle.capabilities.status === 'ready' ? (
                        <RelatedFieldEditor
                          key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${constructionLifecycle.capabilities.response.selectedStage.id}:${graphSource?.occurrenceId ?? ''}`}
                          presentation="standalone"
                          project={projectId}
                          explorerId={state.explorerId}
                          authResourcePath={authResourcePath}
                          snapshotToken={state.catalog.snapshotToken}
                          outputId={table.outputId}
                          construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                          capabilities={constructionLifecycle.capabilities.response}
                          disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                          onCandidateChange={constructionLifecycle.onCandidateChange}
                        />
                      ) : (
                        <>
                          <ConceptCatalog
                            key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${graphSource?.occurrenceId ?? ''}:${advanced}`}
                            project={projectId}
                            explorerId={state.explorerId}
                            authResourcePath={authResourcePath}
                            snapshotToken={state.catalog.snapshotToken}
                            outputId={table.outputId}
                            tableContextKey={`${state.draftVersion}:${state.draftDigest}`}
                            rowRoot={table.document.rootResourceType}
                            resourceType={graphSource?.resourceType}
                            sourceNodeId={graphSource?.nodeId}
                            scopeToSource
                            routeContext={addSourceRouteContext}
                            layout="panel"
                            dense
                            view={catalogView}
                            viewRevision={addColumnsViewRevision}
                            presentationContext={fieldPresentationContext}
                            onSwitchView={selectColumnKind}
                            semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
                            existingSourceKeys={existingSourceKeys}
                            nodeOccurrences={capabilityNodeOccurrences}
                            catalog={state.catalog}
                            sourceProjectionAvailability={sourceAvailability}
                            relatedSourceAvailability={relatedSourceAvailability}
                            suppressUnavailableNotices={hasUnsupportedSavedSourceColumns}
                            disabledReason={sourceSelectionDisabledReason}
                            disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                            groupedRowValuePolicy={groupedRowValuePolicy}
                            onAddSelected={addSelectedFeatures}
                            onPreviewSelected={previewRelatedSourceColumns}
                            onInspectRouteCoverage={inspectRelatedRouteCoverage}
                          />
                        </>
                      )}
                    </>
                  )}
                </div>
              </>
            ) : null}
          </div>
        ) : null}
        {activeOperation.family === 'KEEP_ROWS' || activeOperation.family === 'CALCULATE' ? (
          constructionLifecycle.capabilities.status === 'loading' ? (
            <p role="status" className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              Loading the current table columns and operation support…
            </p>
          ) : constructionLifecycle.capabilities.status === 'error' ? (
            <p role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
              {sanitizeReasonText(constructionLifecycle.capabilities.message)}
            </p>
          ) : constructionLifecycle.capabilities.status === 'ready' ? (
            activeOperation.family === 'KEEP_ROWS' ? (
              <FilterRowsEditor
                key={`${ownerKey}:${table.outputId}:${constructionLifecycle.capabilities.response.selectedStage.id}:${editingConstructionStep?.id ?? 'new'}`}
                project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                snapshotToken={state.catalog.snapshotToken} outputId={table.outputId} catalog={state.catalog}
                construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                capabilities={constructionLifecycle.capabilities.response}
                editingStep={editingConstructionStep}
                selectedColumns={selectedColumnIds}
                disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                onCandidateChange={constructionLifecycle.onCandidateChange}
                onEditStep={editConstructionStep}
                onCancel={cancelConstructionEditor}
              />
            ) : <ConstructionOperationEditor
              family="CALCULATE"
              construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
              capabilities={constructionLifecycle.capabilities.response}
              editingStep={editingConstructionStep}
              selectedColumns={selectedColumnIds}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
              onCandidateChange={constructionLifecycle.onCandidateChange}
              onEditStep={editConstructionStep}
            />
          ) : null
        ) : null}
        {activeOperation.family === 'RESHAPE' ? (
          constructionLifecycle.capabilities.status === 'loading' ? (
            <p role="status" className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              Loading the current table columns and reshape support…
            </p>
          ) : constructionLifecycle.capabilities.status === 'error' ? (
            <p role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
              {sanitizeReasonText(constructionLifecycle.capabilities.message)}
            </p>
          ) : constructionLifecycle.capabilities.status === 'ready' ? (
            <ConstructionReshapeEditor
              key={activeReshapeEditorKey}
              construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
              capabilities={constructionLifecycle.capabilities.response}
              editingStep={editingConstructionStep}
              initialKind={activeReshapeInitialKind}
              initialEntry={activeReshapeInitialEntry}
              hideChangeRowOperation={rowIntentSession}
              selectedColumns={selectedColumnIds}
              useExternalColumnSelection={showReshapeColumnSelection}
              onFormKindChange={setReshapeFormKind}
              pivotDiscovery={constructionLifecycle.pivotDiscovery}
              onDiscoverCategories={constructionLifecycle.onDiscoverCategories}
              relatedExpandContext={{
                project: projectId,
                explorerId: state.explorerId,
                authResourcePath,
                snapshotToken: state.catalog.snapshotToken,
                outputId: table.outputId,
                catalog: state.catalog,
              }}
              relatedExpandQueryOwnerRef={relatedExpandQueryOwnerRef}
              codedPivotContext={codedPivotContext}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
              onCandidateChange={constructionLifecycle.onCandidateChange}
              onEditStep={editConstructionStep}
              onCancel={cancelConstructionEditor}
            />
          ) : (
            null
          )
        ) : null}
        {activeOperation.family === 'COMBINE' ? (
          <ConstructionCombineEditor
            key={`${ownerKey}:${combineCatalog.identity}:${editingConstructionStep?.id ?? 'new'}`}
            catalog={combineCatalog.currentLoad.catalog}
            workspaceInputs={constructionLifecycle.capabilities.status === 'ready'
              ? constructionLifecycle.capabilities.response.workspaceInputs.map((input) => ({
                  kind: 'WORKSPACE_OUTPUT' as const,
                  outputId: input.outputId,
                  title: input.title,
                  columns: input.columns.map((column) => ({
                    id: column.id,
                    name: column.name,
                    label: column.label,
                    type: column.logicalType,
                    nullable: column.nullable,
                    repeated: column.cardinality === 'many',
                    cardinality: column.cardinality,
                    joinCompatibilityKey: column.joinCompatibilityKey,
                    appendCompatibilityKey: column.appendCompatibilityKey,
                  })),
                }))
              : []}
            constructionVersion={construction?.version ?? 1}
            editingStep={editingConstructionStep?.operation.kind === 'COMBINE'
              ? editingConstructionStep as ConstructionCombineStep
              : undefined}
            disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
            loadingMore={combineCatalog.currentLoad.loadingMore}
            loadMoreError={combineCatalog.currentLoad.loadMoreError}
            onLoadMore={() => {
              if (combineCatalog.currentLoad.catalog.kind === 'ready' && combineCatalog.currentLoad.catalog.nextCursor) {
                void combineCatalog.loadPage(combineCatalog.currentLoad.catalog.nextCursor);
              }
            }}
            onRetryCatalog={() => void combineCatalog.loadPage()}
            onCandidateChange={constructionLifecycle.onCandidateChange}
          />
        ) : null}
      </div>
    </section>
  );
};
