import { useConstructionWorkspaceSession } from '../../session/ConstructionWorkspaceSession';
import type { ConstructionHistorySelection } from '../../model/constructionHistory';
import { ConstructionHistory, type ConstructionHistoryProps } from './ConstructionHistory';
import { ConstructionTableNavigation } from './ConstructionTableNavigation';
import React, { useCallback, useRef, useState } from 'react';
import { useDismissibleLayer } from '../../hooks/useDismissibleLayer';
import { useBrowserSubscription } from '../../hooks/useBrowserSubscription';

import { constructionOperationFamilies, type ConstructionOperationFamily } from '../../model/constructionOperationFamily';

export const ConstructionUndoButton = ({ onUndo, disabled = false }: {
  readonly onUndo: () => void;
  readonly disabled?: boolean;
}) => (
  <button
    type="button"
    data-testid="construction-undo"
    aria-label="Undo last saved draft change"
    disabled={disabled}
    onClick={onUndo}
    className="rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
  >
    Undo
  </button>
);
export const ConstructionActionBar = ({
  activeFamily,
  disabled = false,
  addColumnsDisabled = false,
  addColumnsReason,
  combineDisabled = false,
  calculateAvailable = false,
  calculateUnavailableReason = 'Calculation is unavailable for the current table stage.',
  variant = 'default',
  families,
  onSelect,
}: {
  readonly activeFamily?: ConstructionOperationFamily;
  readonly disabled?: boolean;
  readonly addColumnsDisabled?: boolean;
  readonly addColumnsReason?: string;
  readonly combineDisabled?: boolean;
  readonly calculateAvailable?: boolean;
  readonly calculateUnavailableReason?: string;
  readonly variant?: 'default' | 'compact';
  readonly families?: ReadonlyArray<ConstructionOperationFamily>;
  readonly onSelect: (family: ConstructionOperationFamily) => void;
}) => {
  const compactButtonClass = 'rounded border border-slate-300 bg-white px-2 py-1 text-left text-xs text-slate-700 disabled:opacity-50';
  const renderAction = (family: ConstructionOperationFamily) => {
    const operation = constructionOperationFamilies.find((candidate) => candidate.family === family);
    if (!operation) return null;
    const { label, description } = operation;
    return (
      <button
        key={family}
        type="button"
        aria-label={`${family === 'CALCULATE' ? 'Add a calculated column' : family === 'COMBINE' ? 'Combine tables' : label}: ${description}`}
        aria-pressed={activeFamily === family}
        data-testid={`construction-action-${family.toLowerCase().replace('_', '-')}`}
        disabled={disabled || (family === 'ADD_COLUMNS' && addColumnsDisabled) || (family === 'COMBINE' && combineDisabled) || (family === 'CALCULATE' && !calculateAvailable)}
        onClick={() => onSelect(family)}
        title={family === 'ADD_COLUMNS' && addColumnsReason
          ? addColumnsReason
          : family === 'CALCULATE' && !calculateAvailable ? calculateUnavailableReason : description}
        className={variant === 'compact'
          ? `${compactButtonClass} ${activeFamily === family ? 'bg-blue-100 text-slate-950' : 'hover:bg-blue-50'}`
          : `h-8 min-w-0 rounded-md px-3 text-left text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
            activeFamily === family
              ? 'bg-blue-100 text-blue-950'
              : 'bg-transparent text-slate-700 hover:bg-blue-50'
          }`}
      >
        {label}
      </button>
    );
  };

  return (
    <nav aria-label="Table actions" className={`flex flex-wrap items-center ${variant === 'compact' ? 'gap-1' : 'gap-1.5'}`}>
      {(families ?? constructionOperationFamilies.map(({ family }) => family)).map(renderAction)}
    </nav>
  );
};

interface ConstructionWorkspacePresentation {
  readonly history?: ConstructionHistoryProps;
  readonly rowSetup?: React.ReactNode;
  readonly navigation?: React.ReactNode;
  readonly controllers?: React.ReactNode;
  readonly composition?: 'inspector-right' | 'inspector-below';
  readonly network?: React.ReactNode;
  readonly rowExpression?: React.ReactNode;
  readonly presentationActionRef?: { current: ((view: 'both' | 'network' | 'row') => void) | undefined };
  readonly preview: React.ReactNode;
  readonly editor?: React.ReactNode;
  readonly inspection?: React.ReactNode;
  readonly changeReview?: React.ReactNode;
  readonly onChangeReviewHostChange?: (node: HTMLDivElement | null) => void;
}

export const ConstructionWorkspace = ({
  history,
  rowSetup,
  navigation,
  controllers,
  composition = 'inspector-right',
  network,
  presentationActionRef,
  preview,
  editor,
  inspection,
  changeReview,
  onChangeReviewHostChange,
}: ConstructionWorkspacePresentation) => {
  const {
    tables,
    selectedOutputId,
    tableActionsDisabled,
    onSelectTable,
    onNewTable,
    onDuplicateTable,
    onDeleteTable,
    onRenameTable,
    actionsDisabled,
    activeFamily,
    addColumnsDisabled = false,
    addColumnsReason,
    combineDisabled = false,
    calculateAvailable = false,
    calculateUnavailableReason,
    onSelectFamily,
    previewStatus,
    previewReceiptId,
    previewOutputId,
    proposalId,
    draftVersion,
    draftDigest,
  } = useConstructionWorkspaceSession(session => session);
  type WorkspacePanel = 'graph' | 'preview' | 'editor';
  const [expandedPanel, setExpandedPanel] = useState<WorkspacePanel>();
  const workspaceRootRef = useRef<HTMLDivElement>(null);
  const panelRefs = useRef<Partial<Record<WorkspacePanel, HTMLElement | null>>>({});
  const resultRegionRef = useRef<HTMLDivElement | null>(null);
  const [inspectorWidth, setInspectorWidth] = useState<number>();
  const resizableInspector = composition !== 'inspector-below' && Boolean(network);
  const beginInspectorResize = (event: React.PointerEvent<HTMLDivElement>) => {
    const region = resultRegionRef.current;
    if (!region) return;
    event.preventDefault();
    const rect = region.getBoundingClientRect();
    const move = (moveEvent: PointerEvent) => {
      const next = Math.min(Math.max(rect.right - moveEvent.clientX, 288), Math.max(rect.width * 0.7, 288));
      setInspectorWidth(next);
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop, { once: true });
  };
  const panelPointerStart = useRef<{ readonly panel: WorkspacePanel; readonly pointerId: number; readonly x: number; readonly y: number; readonly maxDistance: number; readonly eligibleStart: boolean } | undefined>(undefined);
  if (expandedPanel === 'graph' && !network) {
    setExpandedPanel(undefined);
  }
  const changeExpandedPanel = (panel: WorkspacePanel | undefined) => {
    const focusPanel = panel ?? expandedPanel;
    if (focusPanel) panelRefs.current[focusPanel]?.focus({ preventScroll: true });
    setExpandedPanel(panel);
  };
  const handleExpandedPanelKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (!expandedPanel || event.defaultPrevented) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    if (target.closest('[role="dialog"], [role="listbox"], [role="menu"], [data-radix-popper-content-wrapper], .mantine-Popover-dropdown, [data-dismissible-layer]')) return;
    const panel = panelRefs.current[expandedPanel];
    if (!panel) return;
    if (event.key === 'Escape') {
      if (panel.contains(target)) {
        event.preventDefault();
        changeExpandedPanel(undefined);
      }
      return;
    }
    if (event.key !== 'Tab') return;
    const focusable = [...panel.querySelectorAll<HTMLElement>(
      'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex]:not([tabindex="-1"])',
    )].filter((element) => {
      if (element.matches(':disabled,[aria-disabled="true"]') || element.closest('[hidden], [inert], [aria-disabled="true"]')) return false;
      let ancestor = element.parentElement;
      while (ancestor && ancestor !== panel) {
        if (ancestor instanceof HTMLDetailsElement && !ancestor.open && !(element.tagName === 'SUMMARY' && element.parentElement === ancestor)) return false;
        ancestor = ancestor.parentElement;
      }
      const style = getComputedStyle(element);
      return style.visibility !== 'hidden' && style.display !== 'none';
    });
    if (!focusable.length) {
      event.preventDefault();
      panel.focus();
      return;
    }
    const currentIndex = focusable.indexOf(target as HTMLElement);
    const nextIndex = currentIndex < 0
      ? event.shiftKey ? focusable.length - 1 : 0
      : (currentIndex + (event.shiftKey ? -1 : 1) + focusable.length) % focusable.length;
    event.preventDefault();
    focusable[nextIndex]!.focus();
  };
  const panelControls = (panel: WorkspacePanel, label: string) => expandedPanel === panel ? (
    <div className="construction-panel-controls">
      <button type="button" aria-label={`Back from expanded ${label}`} title="Back to builder (Esc)"
        className="construction-panel-back" onClick={() => changeExpandedPanel(undefined)}>
        <span aria-hidden="true">←</span> Back
      </button>
    </div>
  ) : (
    <button type="button" className="construction-panel-expand" aria-label={`Expand ${label}`} title={`Expand ${label}`}
      onClick={() => changeExpandedPanel(panel)}>
      <span aria-hidden="true">⤢</span>
    </button>
  );
  const canExpandFromTarget = (panel: WorkspacePanel, panelElement: HTMLElement, target: EventTarget | null) => {
    if (!(target instanceof Element) || (target !== panelElement && !panelElement.contains(target))) return false;
    if (target.closest('button, a[href], input, select, textarea, label, summary, details, [role="button"], [role="link"], [role="menu"], [role="dialog"], [contenteditable="true"], [data-no-panel-expand], .construction-panel-controls')) return false;
    if (panel === 'graph' && target.closest('table, thead, tbody, tr, td, th, [role="table"], [role="grid"], [role="row"], [role="cell"], [data-testid="preview-table-scroll"], p, h1, h2, h3, h4, h5, h6, span, img, .react-flow__node, .react-flow__edge, .react-flow__controls, .react-flow__panel, .react-flow__handle, .react-flow__resize-control')) return false;
    const selection = window.getSelection();
    return !(selection && !selection.isCollapsed && selection.toString().length > 0);
  };
  const onPanelPointerDown = (panel: WorkspacePanel) => (event: React.PointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    panelPointerStart.current = { panel, pointerId: event.pointerId, x: event.clientX, y: event.clientY, maxDistance: 0,
      eligibleStart: canExpandFromTarget(panel, event.currentTarget, event.target) };
  };
  const onPanelPointerMove = (panel: WorkspacePanel) => (event: React.PointerEvent<HTMLElement>) => {
    const start = panelPointerStart.current;
    if (!start || start.panel !== panel || start.pointerId !== event.pointerId) return;
    panelPointerStart.current = { ...start, maxDistance: Math.max(start.maxDistance, Math.hypot(event.clientX - start.x, event.clientY - start.y)) };
  };
  const onPanelPointerCancel = (panel: WorkspacePanel) => (event: React.PointerEvent<HTMLElement>) => {
    if (panelPointerStart.current?.panel === panel && panelPointerStart.current.pointerId === event.pointerId) panelPointerStart.current = undefined;
  };
  const onPanelBackgroundClick = (panel: WorkspacePanel) => (event: React.MouseEvent<HTMLElement>) => {
    const start = panelPointerStart.current;
    panelPointerStart.current = undefined;
    if (expandedPanel || event.defaultPrevented || event.button !== 0 || event.detail === 0) return;
    if (start?.panel === panel && (!start.eligibleStart || Math.max(start.maxDistance, Math.hypot(event.clientX - start.x, event.clientY - start.y)) > 5)) return;
    if (!canExpandFromTarget(panel, event.currentTarget, event.target)) return;
    changeExpandedPanel(panel);
  };
  const onPanelKeyDown = (panel: WorkspacePanel) => (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.target !== event.currentTarget || expandedPanel || (event.key !== 'Enter' && event.key !== ' ')) return;
    event.preventDefault();
    changeExpandedPanel(panel);
  };
  const editorFocusRef = useRef<HTMLDivElement>(null);
  if (presentationActionRef) presentationActionRef.current = () => { editorFocusRef.current?.focus(); };
  const viewportRef = useCallback((node: HTMLDivElement | null) => {
    workspaceRootRef.current = node;
    if (!node) return;
    const measure = () => {
      const bounds = node.getBoundingClientRect();
      const top = Math.max(0, bounds.top + window.scrollY);
      const trailing = Math.max(12, Math.ceil(document.documentElement.scrollHeight - bounds.bottom - window.scrollY));
      node.style.setProperty('--construction-top', `${top}px`);
      node.style.setProperty('--construction-bottom', `${trailing}px`);
    };
    measure();
    window.addEventListener('resize', measure);
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure);
    observer?.observe(document.body);
    return () => {
      window.removeEventListener('resize', measure);
      observer?.disconnect();
    };
  }, []);
  const tableNavigation = <ConstructionTableNavigation
    compact={Boolean(network)}
    tables={tables}
    selectedOutputId={selectedOutputId}
    disabled={tableActionsDisabled || Boolean(expandedPanel)}
    onSelectTable={onSelectTable}
    onNewTable={onNewTable}
    onDuplicateTable={onDuplicateTable}
    onDeleteTable={onDeleteTable}
    onRenameTable={onRenameTable}
  />;
  return (
  <div
    onKeyDown={handleExpandedPanelKeyDown}
    ref={viewportRef}
    data-testid="construction-workspace"
    data-draft-version={draftVersion}
    data-draft-digest={draftDigest}
    className="construction-workspace mx-auto flex w-full max-w-[1920px] flex-col gap-2"
  >
      {!network || changeReview || onChangeReviewHostChange ? <div className="construction-workspace-actions flex flex-wrap items-center gap-2" inert={expandedPanel ? true : undefined}>
        {!network ? <div className="min-w-0 flex-1">{tableNavigation}</div> : null}
        {changeReview || onChangeReviewHostChange ? <div aria-label="Pending change actions" className="construction-change-review flex shrink-0 flex-wrap items-center justify-end gap-2"><div ref={onChangeReviewHostChange} className="min-w-0" />{changeReview}</div> : null}
      </div> : null}
      <div className="construction-controller-host" inert={expandedPanel ? true : undefined}>{controllers}</div>
      <div ref={resultRegionRef} data-composition={composition} data-has-editor={editor || inspection ? 'true' : undefined}
        data-expanded-panel={expandedPanel} data-has-resize-handle={resizableInspector ? 'true' : undefined}
        data-has-network={network ? 'true' : undefined} className="construction-result-region"
        style={inspectorWidth ? ({ '--inspector-width': `${inspectorWidth}px` } as React.CSSProperties) : undefined}>
        <div className="construction-main-panel">
        {network ? <section aria-label="Related records" tabIndex={expandedPanel === 'graph' ? -1 : 0} aria-keyshortcuts="Enter Space"
          onPointerDownCapture={onPanelPointerDown('graph')} onPointerMoveCapture={onPanelPointerMove('graph')} onPointerCancelCapture={onPanelPointerCancel('graph')} onClick={onPanelBackgroundClick('graph')} onKeyDown={onPanelKeyDown('graph')}
          ref={(node) => { panelRefs.current.graph = node; }} inert={expandedPanel !== undefined && expandedPanel !== 'graph' ? true : undefined}
          data-expanded={expandedPanel === 'graph' ? 'true' : undefined} className="construction-authored-region">
          {expandedPanel !== 'graph' ? <div className="min-w-0 shrink-0 pr-9 pl-3">{tableNavigation}</div> : null}
          {panelControls('graph', 'Related records')}
          <div ref={editorFocusRef} tabIndex={-1} className="construction-authored-content">{network}</div>
        </section> : null}

        <section
          aria-label="Table result"
          data-testid="construction-preview"
          data-preview-status={previewStatus}
          data-preview-receipt-id={previewReceiptId ?? ''}
          data-preview-output-id={previewOutputId ?? ''}
          data-preview-proposal-id={proposalId ?? ''}
          data-current-draft-version={draftVersion}
          data-current-draft-digest={draftDigest}
          data-expanded={expandedPanel === 'preview' ? 'true' : undefined}
          tabIndex={expandedPanel === 'preview' ? -1 : 0} aria-keyshortcuts="Enter Space"
          onPointerDownCapture={onPanelPointerDown('preview')} onPointerMoveCapture={onPanelPointerMove('preview')} onPointerCancelCapture={onPanelPointerCancel('preview')} onClick={onPanelBackgroundClick('preview')} onKeyDown={onPanelKeyDown('preview')}
          ref={(node) => { panelRefs.current.preview = node; }} inert={expandedPanel !== undefined && expandedPanel !== 'preview' ? true : undefined}
          className="construction-result-preview min-w-0 bg-white"
        >
          <p data-testid="recipe-result-identity" className="sr-only">{proposalId ? 'Proposed recipe result' : 'Saved recipe result'}</p>
          {panelControls('preview', 'Table result')}
          {preview}
        </section>
        </div>
        {resizableInspector ? <div role="separator" aria-orientation="vertical" aria-label="Resize graph and editor" inert={expandedPanel ? true : undefined}
          data-testid="construction-inspector-resize" onPointerDown={beginInspectorResize} className="construction-resize-handle construction-resize-handle-vertical" /> : null}
        <aside aria-label="Recipe editor" className="construction-side-panel"
          tabIndex={expandedPanel === 'editor' ? -1 : 0} aria-keyshortcuts="Enter Space"
          ref={(node) => { panelRefs.current.editor = node; }}
          inert={expandedPanel !== undefined && expandedPanel !== 'editor' ? true : undefined}
          data-expanded={expandedPanel === 'editor' ? 'true' : undefined}
          onPointerDownCapture={onPanelPointerDown('editor')} onPointerMoveCapture={onPanelPointerMove('editor')}
          onPointerCancelCapture={onPanelPointerCancel('editor')} onClick={onPanelBackgroundClick('editor')} onKeyDown={onPanelKeyDown('editor')}>
          {panelControls('editor', 'Recipe editor')}
          <div className="construction-source-controls">{rowSetup}{history?.sourceSteps}</div>
          {editor ? <section aria-label="Change editor" className="construction-active-editor min-h-0 min-w-0">{editor}</section> : null}
          <div hidden={Boolean(editor)} className="construction-scope-navigation">
            {navigation ?? (!editor ? <ConstructionActionBar
              activeFamily={activeFamily} disabled={actionsDisabled}
              addColumnsDisabled={addColumnsDisabled} addColumnsReason={addColumnsReason} combineDisabled={combineDisabled}
              calculateAvailable={calculateAvailable} calculateUnavailableReason={calculateUnavailableReason}
              onSelect={onSelectFamily}
            /> : null)}
            {!editor && inspection ? <div aria-label="Recipe fact inspector" className="construction-fact-inspector min-h-0 min-w-0">{inspection}</div> : null}
          </div>
          {history && !navigation ? <details className="construction-history-disclosure">
            <summary>Ordered operation history ({history.steps.length})</summary>
            <ConstructionHistory {...history} sourceSteps={undefined} sourceControl={undefined} />
          </details> : null}
        </aside>
      </div>
  </div>
  );
};
