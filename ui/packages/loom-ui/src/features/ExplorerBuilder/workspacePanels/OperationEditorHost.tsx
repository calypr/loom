import React from 'react';
import type { Construction, ConstructionCapabilitiesResponse, ConstructionChoiceSelection, ConstructionStep, ExplorerBuilderCommand } from '../../../types';
import type { LoomClient } from '../../../api';
import type { BuilderAuthoringState } from '../authoring/model';
import type { CatalogSourceOption } from '../catalogItems';
import { sanitizeReasonText } from '../catalogItems';
import type { ConstructionOperationFamily } from '../constructionWorkspace/ConstructionWorkspace';
import type { ConstructionSelectableColumn } from '../constructionWorkspace/ConstructionColumnSelection';
import type { ConstructionCombineStep } from '../constructionOperations/combineEditorTypes';
import type { ConstructionReshapeEditorEntry } from '../constructionOperations/ConstructionReshapeEditor';
import { ConstructionColumnSelection } from '../constructionWorkspace/ConstructionColumnSelection';
import { ConceptCatalog } from '../components/ConceptCatalog';
import { FrameSourcePanel } from '../constructionWorkspace/FrameSourcePanel';
import { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import { useCombineCatalog } from '../authoring/useCombineCatalog';
import { ConstructionOperationEditor } from '../constructionOperations/ConstructionOperationEditor';
import { ConstructionReshapeEditor } from '../constructionOperations/ConstructionReshapeEditor';
import { FilterRowsEditor } from '../constructionOperations/FilterRowsEditor';
import { RelatedFieldEditor } from '../constructionOperations/RelatedFieldEditor';
import { ConstructionCombineEditor } from '../constructionOperations/ConstructionCombineEditor';

type Table = BuilderAuthoringState['tables'][number];
type Lifecycle = ReturnType<typeof useConstructionLifecycle>;
type CombineCatalog = ReturnType<typeof useCombineCatalog>;
type CatalogProps = React.ComponentProps<typeof ConceptCatalog>;
type ReshapeProps = React.ComponentProps<typeof ConstructionReshapeEditor>;
type AddSourceOption = CatalogSourceOption | { readonly kind: 'EXACT_RELATED'; readonly key: string; readonly label: string; readonly resourceType: string; readonly sourceNodeId?: string };
type FamilyOperation = { readonly family: ConstructionOperationFamily; readonly label: string };

export interface OperationEditorHostProps {
  readonly table: Table;
  readonly activeOperation: FamilyOperation;
  readonly state: BuilderAuthoringState;
  readonly ownerKey: string;
  readonly rowIntentSession: boolean;
  readonly editingConstructionStep?: ConstructionStep;
  readonly construction?: Construction;
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
  readonly rowValuePolicy: NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;
  readonly setRowValuePolicy: (value: NonNullable<ConstructionChoiceSelection['rowValuePolicy']>) => void;
  readonly selectedAddSource?: AddSourceOption;
  readonly project: string;
  readonly authResourcePath?: string;
  readonly addColumnsSourceControls?: React.ReactNode;
  readonly addSourceRouteContext: CatalogProps['routeContext'];
  readonly addColumnsViewRevision: number;
  readonly fieldPresentationContext: CatalogProps['presentationContext'];
  readonly onSwitchAddColumnsView: NonNullable<CatalogProps['onSwitchView']>;
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
  readonly onApplyCommands: (commands: ReadonlyArray<ExplorerBuilderCommand>) => Promise<boolean>;
  readonly activeReshapeEditorKey: string | undefined;
  readonly activeReshapeInitialKind: ReshapeProps['initialKind'];
  readonly activeReshapeInitialEntry: ConstructionReshapeEditorEntry | undefined;
  readonly onReshapeFormKindChange: NonNullable<ReshapeProps['onFormKindChange']>;
  readonly relatedExpandQueryOwnerRef: ReshapeProps['relatedExpandQueryOwnerRef'];
  readonly loomClient: LoomClient;
  readonly combineCatalog: CombineCatalog;
  readonly onEditConstructionStep: NonNullable<ReshapeProps['onEditStep']>;
  readonly onCancelConstructionEditor: NonNullable<ReshapeProps['onCancel']>;
  readonly onClose: () => void;
}

export const OperationEditorHost = ({
  table,
  activeOperation,
  state,
  ownerKey,
  rowIntentSession,
  editingConstructionStep,
  construction,
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
  rowValuePolicy,
  setRowValuePolicy,
  selectedAddSource,
  project: projectId,
  authResourcePath,
  addColumnsSourceControls,
  addSourceRouteContext,
  addColumnsViewRevision,
  fieldPresentationContext,
  onSwitchAddColumnsView: selectAddColumnsView,
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
  onApplyCommands: applyCommands,
  activeReshapeEditorKey,
  activeReshapeInitialKind,
  activeReshapeInitialEntry,
  onReshapeFormKindChange: setReshapeFormKind,
  relatedExpandQueryOwnerRef,
  loomClient,
  combineCatalog,
  onEditConstructionStep: editConstructionStep,
  onCancelConstructionEditor: cancelConstructionEditor,
  onClose: closeConstructionWorkspace,
}: OperationEditorHostProps) => {
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
            {!rowIntentSession && activeOperation.family !== 'RESHAPE' ? <h2 className="text-base font-semibold text-slate-950">{activeOperation.label}</h2> : null}
            {editingConstructionStep ? <p className="text-xs text-slate-500">Editing a saved step</p> : null}
          </div>
          <button
            type="button"
            aria-label="Close operation editor"
            data-testid="construction-close-operation-editor"
            onClick={closeConstructionWorkspace}
            className="inline-flex shrink-0 items-center gap-1 rounded border border-slate-300 bg-white px-2.5 py-1 text-xs font-semibold text-slate-700 shadow-sm hover:bg-slate-50"
          >
            <span aria-hidden="true">←</span> Back to table
          </button>
        </div>
        {activeOperation.family === 'KEEP_ROWS' || activeOperation.family === 'CALCULATE' || showReshapeColumnSelection ? (
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
        ) : selectedColumnIds.length > 0 ? (
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
                {selectedAddSource?.kind === 'EXACT_RELATED' && constructionLifecycle.capabilities.status === 'ready' ? (
                  <RelatedFieldEditor
                    key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${constructionLifecycle.capabilities.response.selectedStage.id}`}
                    presentation="standalone"
                    project={projectId}
                    explorerId={state.explorerId}
                    authResourcePath={authResourcePath}
                    snapshotToken={state.catalog.snapshotToken}
                    outputId={table.outputId}
                    construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                    capabilities={constructionLifecycle.capabilities.response}
                    disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                    sourceControls={addColumnsSourceControls}
                    onCandidateChange={constructionLifecycle.onCandidateChange}
                  />
                ) : (
                  <>
                    <ConceptCatalog
                      key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}`}
                      project={projectId}
                      explorerId={state.explorerId}
                      authResourcePath={authResourcePath}
                      snapshotToken={state.catalog.snapshotToken}
                      outputId={table.outputId}
                      tableContextKey={`${state.draftVersion}:${state.draftDigest}`}
                      rowRoot={table.document.rootResourceType}
                      resourceType={selectedAddSource?.resourceType}
                      sourceNodeId={selectedAddSource?.sourceNodeId}
                      routeContext={addSourceRouteContext}
                      layout="panel"
                      dense
                      view="all"
                      viewRevision={addColumnsViewRevision}
                      presentationContext={fieldPresentationContext}
                      onSwitchView={selectAddColumnsView}
                      semanticSelectionMode="SYSTEM_VERSION_CATEGORY"
                      existingSourceKeys={existingSourceKeys}
                      nodeOccurrences={capabilityNodeOccurrences}
                      catalog={state.catalog}
                      sourceControls={addColumnsSourceControls}
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
                {table.document.frames?.length ? (
                  <details className="rounded-md border border-slate-200 bg-white p-3">
                    <summary className="cursor-pointer text-sm font-semibold text-slate-700">Manage saved coded-value sources</summary>
                    <FrameSourcePanel
                      key={`${table.outputId}:${state.catalog.snapshotToken}`}
                      project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                      snapshotToken={state.catalog.snapshotToken} outputId={table.outputId}
                      rowRoot={table.document.rootResourceType} frames={table.document.frames ?? []}
                      columns={table.document.columns} disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      allowNewSource={false}
                      onSet={(choice, form) => applyCommands([{
                        type: 'SET_FRAME_SOURCE', outputId: table.outputId, frameChoiceId: choice.choiceId, form,
                      }])}
                      onReplace={(frameId, choice, form) => applyCommands([{
                        type: 'REPLACE_FRAME_SOURCE', outputId: table.outputId, frameId, frameChoiceId: choice.choiceId, form,
                      }])}
                      onRemove={(frameId) => applyCommands([{
                        type: 'REMOVE_FRAME_SOURCE', outputId: table.outputId, frameId,
                      }])}
                      onAddSelected={addSelectedFeatures}
                      onRemoveColumn={(column) => applyCommands([{
                        type: 'REMOVE_COLUMN', outputId: table.outputId, column,
                      }])}
                    />
                  </details>
                ) : null}
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
              codedPivotContext={{
                client: loomClient,
                project: projectId,
                explorerId: state.explorerId,
                authResourcePath,
                snapshotToken: state.catalog.snapshotToken,
                outputId: table.outputId,
                rowRoot: table.document.rootResourceType,
              }}
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
