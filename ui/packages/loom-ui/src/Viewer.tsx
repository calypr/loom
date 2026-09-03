import React, { useMemo, useReducer, useState } from 'react';
import { Alert, Button, Center, Group, Loader, MantineProvider, Modal, Stack, Tabs, Text, Title } from '@mantine/core';
import { createLoomClient, type LoomClient, type LoomOutputRequest, type LoomOutputResult } from './api';
import { ChartToggle, QuerySummary } from './features/ExplorerViewer/ViewerChrome';
import { FilterRail, OutputCharts, OutputTable, PageControls, textFor, type ViewerRow } from './features/ExplorerViewer/components';
import { runtimeSessionKey } from './features/ExplorerViewer/model';
import { createViewerReducerState, viewerReducer } from './features/ExplorerViewer/reducer';
import { outputRequestFor } from './features/ExplorerViewer/serialization';
import { LoomProvider, useLoomClient, useLoomOutput, useLoomRuntime } from './react';
import type { ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from './types';

type ViewerRuntimeAction = NonNullable<ExplorerRuntimeOutputV1['actions']>[number];

export interface LoomViewerActionContext {
  readonly project: string;
  readonly runtime: ExplorerRuntimeV1;
  readonly output: ExplorerRuntimeOutputV1;
  readonly action: ViewerRuntimeAction;
  readonly request: LoomOutputRequest;
  readonly result?: LoomOutputResult;
}

export type LoomViewerActionHandler = (context: LoomViewerActionContext, signal: AbortSignal) => Promise<void> | void;

export interface LoomExplorerViewerProps {
  readonly project: string;
  readonly explorerId?: string;
  readonly client?: LoomClient;
  readonly className?: string;
  readonly activeOutputId?: string;
  readonly onActiveOutputChange?: (outputId: string) => void;
  readonly renderRowDetails?: (row: ViewerRow) => React.ReactNode;
  readonly customActions?: Readonly<Record<string, LoomViewerActionHandler>>;
}

const downloadBlob = (blob: Blob, fileName: string) => {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = fileName;
  anchor.click();
  queueMicrotask(() => URL.revokeObjectURL(url));
};

const ViewerContent = ({ project, explorerId, activeOutputId, onActiveOutputChange, renderRowDetails, customActions }: Required<Pick<LoomExplorerViewerProps, 'project'>> & Pick<LoomExplorerViewerProps, 'explorerId' | 'activeOutputId' | 'onActiveOutputChange' | 'renderRowDetails' | 'customActions'>) => {
  const runtimeQuery = useLoomRuntime({ project, explorerId: explorerId ?? 'default' });
  if (runtimeQuery.isLoading && !runtimeQuery.data) return <Center mih="45vh"><Stack align="center" gap="sm"><Loader size="sm" /><Text size="sm">Loading published Explorer…</Text></Stack></Center>;
  if (runtimeQuery.error || !runtimeQuery.data) return <Center mih="45vh"><Alert color="red" title="Explorer unavailable"><Stack gap="sm"><Text size="sm">The published Explorer could not be loaded.</Text><Button size="xs" onClick={() => { void runtimeQuery.refetch(); }}>Try again</Button></Stack></Alert></Center>;
  if (runtimeQuery.data.outputs.length === 0) return <Center mih="45vh"><Alert title="No published outputs">This Explorer has no published tables yet.</Alert></Center>;
  return <ViewerSession key={runtimeSessionKey(runtimeQuery.data)} project={project} runtime={runtimeQuery.data} activeOutputId={activeOutputId} onActiveOutputChange={onActiveOutputChange} renderRowDetails={renderRowDetails} customActions={customActions} />;
};

const ViewerSession = ({ project, runtime, activeOutputId: controlledOutputId, onActiveOutputChange, renderRowDetails, customActions }: { readonly project: string; readonly runtime: ExplorerRuntimeV1; readonly activeOutputId?: string; readonly onActiveOutputChange?: (outputId: string) => void; readonly renderRowDetails?: (row: ViewerRow) => React.ReactNode; readonly customActions?: LoomExplorerViewerProps['customActions'] }) => {
  const [state, dispatch] = useReducer(viewerReducer, runtime, (value) => createViewerReducerState(value, controlledOutputId));
  const [pendingAction, setPendingAction] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const output = runtime.outputs.find((candidate) => candidate.outputId === controlledOutputId)
    ?? runtime.outputs.find((candidate) => candidate.outputId === state.activeOutputId)
    ?? runtime.outputs[0];
  const outputId = output?.outputId ?? '';
  const request = useMemo(() => output ? outputRequestFor(project, runtime, { ...state, activeOutputId: outputId }, output) : undefined, [output, outputId, project, runtime, state]);
  const resultQuery = useLoomOutput(request ?? { project, selector: { recipe: '', translationVersion: '', output: '' }, columns: [] }, { enabled: Boolean(request) });
  const client = useLoomClient();
  if (!output || !request) return null;

  const selectOutput = (nextOutputId: string | null) => {
    if (!nextOutputId) return;
    dispatch({ type: 'selectOutput', outputId: nextOutputId });
    onActiveOutputChange?.(nextOutputId);
  };
  const runAction = async (action?: ViewerRuntimeAction) => {
    const actionKey = action?.type ?? 'download';
    setPendingAction(actionKey);
    setActionError(undefined);
    try {
      const customAction = action ? customActions?.[action.type] : undefined;
      if (action && customAction) {
        await customAction({ project, runtime, output, action, request, result: resultQuery.data }, new AbortController().signal);
      } else {
        const targetOutput = action?.output
          ? runtime.outputs.find((candidate) => candidate.outputId === action.output || candidate.name === action.output) ?? output
          : output;
        const targetRequest = targetOutput.outputId === output.outputId
          ? request
          : outputRequestFor(project, runtime, { ...state, activeOutputId: targetOutput.outputId }, targetOutput);
        const exportRequest = {
          ...targetRequest,
          ...(action?.columns ? { columns: action.columns } : {}),
          ...(action?.exportHeaders ? { exportHeaders: action.exportHeaders } : {}),
        };
        const blob = await client.exportOutput(exportRequest);
        downloadBlob(blob, action?.fileName ?? `${targetOutput.name || targetOutput.outputId}.csv`);
      }
    } catch (error) {
      setActionError(error instanceof Error ? error.message : 'The action could not be completed.');
    } finally {
      setPendingAction(undefined);
    }
  };
  const overlay = state.overlay;

  return (
    <main className="loom-viewer min-h-screen bg-[var(--loom-viewer-bg)] px-4 py-3 text-[var(--loom-viewer-text)] md:px-8 lg:px-12" aria-label="Loom Explorer Viewer">
      <div className="mx-auto max-w-[1500px]">
        <Group component="header" className="loom-viewer-header" justify="space-between" align="center" gap="md" mb="sm" wrap="wrap">
          <div className="loom-viewer-heading">
            <Group className="loom-viewer-kicker" gap="sm" align="center" wrap="wrap">
              <Text c="blue" fw={700} fz="xs" tt="uppercase" className="tracking-widest">Loom Explorer</Text>
              <Text component="span" className="loom-viewer-status" size="xs">Published</Text>
            </Group>
            <Title order={1} fz={{ base: 24, md: 30 }}>{output.title}</Title>
          </div>
          <Group gap="xs" wrap="wrap">
            <Button loading={pendingAction === 'download'} onClick={() => { void runAction(); }}>Download CSV</Button>
            {output.actions?.map((action, index) => (
              <Button variant="default" key={`${action.type}-${index}`} loading={pendingAction === action.type} onClick={() => { void runAction(action); }}>
                {action.title}
              </Button>
            ))}
          </Group>
        </Group>
        {actionError ? <Alert color="red" title="Action failed" withCloseButton onClose={() => setActionError(undefined)} mb="md">{actionError}</Alert> : null}
        <Tabs value={outputId} onChange={selectOutput}>
          <Tabs.List aria-label="Output tables" className="loom-viewer-tabs-list flex-nowrap overflow-x-auto">
            {runtime.outputs.map((candidate) => (
              <Tabs.Tab value={candidate.outputId} key={candidate.outputId} className="loom-viewer-tab shrink-0">
                <Stack gap={0} align="flex-start"><Text size="sm" fw={600}>{candidate.title}</Text><Text c="dimmed" size="xs" className="loom-viewer-tab-subtitle">{candidate.rowLabel}</Text></Stack>
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {runtime.outputs.map((candidate) => (
            <Tabs.Panel value={candidate.outputId} key={candidate.outputId}>
              {candidate.outputId === outputId ? (
                <div className="grid grid-cols-1 gap-4 pt-3 lg:grid-cols-[17rem_minmax(0,1fr)]">
                  <FilterRail runtime={runtime} output={output} result={resultQuery.data} state={state} dispatch={dispatch} />
                  <section className="min-w-0" aria-label={output.title}>
                    <QuerySummary output={output} runtime={runtime} state={state} result={resultQuery.data} dispatch={dispatch} />
                    <Group justify="space-between" gap="sm" mih={40} mb="xs">
                      {resultQuery.isLoading
                        ? <Group gap="xs" role="status"><Loader size="xs" /><Text c="dimmed" size="xs">Updating results…</Text></Group>
                        : resultQuery.error
                          ? <Group gap="xs"><Text c="red" size="xs" role="alert">Results could not be loaded.</Text><Button variant="subtle" size="compact-xs" onClick={() => { void resultQuery.refetch(); }}>Retry</Button></Group>
                          : <Text c="dimmed" size="xs">Visual overview</Text>}
                      {output.charts.length > 0 ? <ChartToggle outputId={outputId} visible={state.chartsVisible[outputId] ?? true} dispatch={dispatch} /> : null}
                    </Group>
                    <OutputCharts output={output} result={resultQuery.data} visible={state.chartsVisible[outputId] ?? true} />
                    <PageControls output={output} result={resultQuery.data} state={state} dispatch={dispatch} />
                    <OutputTable runtime={runtime} output={output} result={resultQuery.data} state={state} dispatch={dispatch} />
                  </section>
                </div>
              ) : null}
            </Tabs.Panel>
          ))}
        </Tabs>
      </div>
      <Modal opened={overlay.kind === 'sharedFilterConfirmation'} onClose={() => dispatch({ type: 'clearOverlay' })} title="Apply shared filter?" centered>
        {overlay.kind === 'sharedFilterConfirmation' ? (
          <Stack>
            <Text size="sm">Apply <strong>{overlay.proposal.name}</strong> to every linked output?</Text>
            <Group justify="flex-end"><Button variant="default" onClick={() => dispatch({ type: 'clearOverlay' })}>Cancel</Button><Button onClick={() => dispatch({ type: 'setSharedFilter', name: overlay.proposal.name, values: overlay.proposal.values })}>Apply filter</Button></Group>
          </Stack>
        ) : null}
      </Modal>
      <Modal opened={overlay.kind === 'rowDetails'} onClose={() => dispatch({ type: 'hideOverlay' })} title="Row details" closeButtonProps={{ 'aria-label': 'Close row details' }} centered size="lg">
        {overlay.kind === 'rowDetails'
          ? renderRowDetails
            ? renderRowDetails(overlay.row)
            : <dl className="grid grid-cols-[minmax(9rem,.35fr)_1fr] gap-x-4 gap-y-2 text-sm">{Object.entries(overlay.row).map(([key, value]) => <React.Fragment key={key}><dt className="font-semibold text-slate-500">{key}</dt><dd className="m-0 break-words">{textFor(value)}</dd></React.Fragment>)}</dl>
          : null}
      </Modal>
    </main>
  );
};

export const LoomExplorerViewer = ({ project, explorerId, client, className, activeOutputId, onActiveOutputChange, renderRowDetails, customActions }: LoomExplorerViewerProps) => {
  const ownedClient = useMemo(() => client ?? createLoomClient(), [client]);
  return (
    <LoomProvider client={ownedClient}>
      <MantineProvider>
        <div className={['loom-ui-root', className].filter(Boolean).join(' ')}>
          <ViewerContent project={project} explorerId={explorerId} activeOutputId={activeOutputId} onActiveOutputChange={onActiveOutputChange} renderRowDetails={renderRowDetails} customActions={customActions} />
        </div>
      </MantineProvider>
    </LoomProvider>
  );
};

export default LoomExplorerViewer;
